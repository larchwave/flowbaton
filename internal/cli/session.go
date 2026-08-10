package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/aiengine"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/js"
)

// DeviceSession runs a prepared program against one device.
//
// It exists to hold the assembly of engine.Dependencies in one place. That
// assembly is the part where a missing service is invisible: the engine takes
// every boundary as an interface, so a nil one compiles and then fails inside
// a flow, where it reads as a flow failure rather than a wiring mistake.
type DeviceSession struct {
	Driver device.Driver
	// OutputDirectory receives screenshots and other run artifacts.
	OutputDirectory string
	// BaseDirectory resolves flow resources such as runScript files.
	BaseDirectory string
	// Clock is optional; a nil clock uses the wall clock. Tests supply their
	// own so a flow's waits do not take real time.
	Clock engine.Clock
	// ExecutionID is optional; a blank one is derived from the run's own start.
	ExecutionID string
	// Shard is which share of the run this session executes. It is what the
	// reserved FLOWBATON_SHARD_* and FLOWBATON_DEVICE_UDID variables are built
	// from, so a flow can report where it ran.
	Shard Shard
}

// Execute opens the device, runs every selected root, and closes the device
// even when a flow fails.
func (session DeviceSession) Execute(
	ctx context.Context,
	program *engine.Program,
	options TestOptions,
) ([]engine.FlowResult, error) {
	if session.Driver == nil {
		return nil, fmt.Errorf("device session: a driver is required")
	}
	if err := session.Driver.Open(ctx); err != nil {
		return nil, fmt.Errorf("device session: opening %s: %w", session.Driver.Name(), err)
	}

	var recordingFinalizer func(context.Context) error
	var recordingController engine.RecordingController
	var results []engine.FlowResult
	var executeErr error

	dependencies, err := session.dependencies(options)
	if err != nil {
		executeErr = err
	} else {
		recordingController = dependencies.RecordingController
		recordingFinalizer, executeErr = startSessionRecording(
			ctx, recordingController, options.RecordTo)
		if executeErr == nil {
			results, executeErr = engine.Execute(ctx, program, dependencies)
		}
	}

	recordingCleanupCtx, cancelRecordingCleanup := context.WithTimeout(
		context.WithoutCancel(ctx), deviceSessionCleanupTimeout)

	var recordingErr error
	if recordingFinalizer != nil {
		recordingErr = recordingFinalizer(recordingCleanupCtx)
	}
	// An authored startRecording can remain active when a later command fails or
	// the execution context is cancelled. StopAll is idempotent, so it is also
	// safe after whole-run recording finalization.
	if finalizer, ok := recordingController.(interface {
		StopAll(context.Context) ([]device.Artifact, error)
	}); ok {
		_, stopAllErr := finalizer.StopAll(recordingCleanupCtx)
		if stopAllErr != nil {
			stopAllErr = fmt.Errorf("device session: finalizing active recording: %w", stopAllErr)
		}
		recordingErr = errors.Join(recordingErr, stopAllErr)
	}
	cancelRecordingCleanup()

	// Driver cleanup gets its own bounded context so a recording timeout cannot
	// prevent the driver from removing ports, processes, or installed helpers.
	driverCleanupCtx, cancelDriverCleanup := context.WithTimeout(
		context.WithoutCancel(ctx), deviceSessionCleanupTimeout)
	closeErr := session.Driver.Close(driverCleanupCtx)
	cancelDriverCleanup()
	if closeErr != nil {
		closeErr = fmt.Errorf("device session: closing driver: %w", closeErr)
	}

	return results, errors.Join(executeErr, recordingErr, closeErr)
}

const deviceSessionCleanupTimeout = 15 * time.Second

// startSessionRecording brackets a whole run with one recording, which is what
// `record` asks for. It returns no finalizer when nothing was asked for.
func startSessionRecording(
	ctx context.Context, controller engine.RecordingController, output string,
) (func(context.Context) error, error) {
	if output == "" {
		return nil, nil
	}
	if err := controller.Start(ctx, engine.RecordingStartRequest{Name: output}); err != nil {
		return nil, fmt.Errorf("device session: recording %s: %w", output, err)
	}
	return func(cleanupCtx context.Context) error {
		artifacts, stopErr := controller.Stop(cleanupCtx)
		if stopErr != nil {
			return fmt.Errorf("device session: finalizing recording %s: %w", output, stopErr)
		}
		finalized := false
		for _, artifact := range artifacts {
			if strings.TrimSpace(artifact.Path) != "" {
				finalized = true
				break
			}
		}
		if !finalized {
			return fmt.Errorf("device session: recording %s produced no finalized artifact", output)
		}
		return nil
	}, nil
}

func (session DeviceSession) dependencies(options TestOptions) (engine.Dependencies, error) {
	platform := options.Platform
	if platform == "" {
		platform = string(session.Driver.Capabilities().Platform)
	}
	jsFactory, err := js.NewFactory(js.Config{
		Random:   CryptoRandom{},
		Platform: platform,
	})
	if err != nil {
		return engine.Dependencies{}, fmt.Errorf("device session: javascript runtime: %w", err)
	}

	clock := session.Clock
	if clock == nil {
		clock = engine.RealClock{}
	}
	outputDirectory := session.OutputDirectory
	if outputDirectory == "" {
		outputDirectory = "."
	}
	baseDirectory := session.BaseDirectory
	if baseDirectory == "" {
		baseDirectory = "."
	}

	// AI commands need a provider; with none configured this is a nil engine and
	// they fail closed when no provider is configured. A
	// misconfigured provider (unknown name) is a loud error, not a silent skip.
	aiEngine, err := aiengine.FromEnv(aiEnvironment(options, os.Getenv))
	if err != nil {
		return engine.Dependencies{}, fmt.Errorf("device session: ai engine: %w", err)
	}

	return engine.Dependencies{
		AIEngine:    aiEngine,
		ExecutionID: session.executionID(clock),
		// Only a declared execution order lets one failed flow end the suite,
		// and only when it did not ask to continue. Everything else runs.
		SequencedRoots:    options.SequencedRoots,
		ContinueOnFailure: options.ContinueOnFailure,
		// The operator's map comes from the parsed command line, not from a
		// field set when the session was built. A session constructed once and
		// run twice must see the options it was actually run with.
		ExternalEnvironment: options.Env,
		// The reserved channel is separate because the engine strips these names
		// out of the external one: a FLOWBATON_SHARD_ID left in an operator's
		// shell must not be able to forge a shard identity.
		ReservedEnvironment: reservedEnvironment(session.Shard),
		Driver:              session.Driver,
		Clock:               clock,
		JSFactory:           jsFactory,
		Controller:          engine.NoopController{},
		// "." rather than baseDirectory: an authored screenshot name resolves
		// against the process working directory, not the flow's directory.
		ArtifactSink:        NewArtifactSink(outputDirectory, "."),
		RecordingController: recordingController(session.Driver),
		ResourceReader:      NewResourceReader(baseDirectory),
		InputGenerator:      NewInputGenerator(),
		ImageChecker:        ImageChecker{},
	}, nil
}

// aiEnvironment gives explicit CLI values precedence without changing the
// provider selected by FLOWBATON_AI_PROVIDER. The generic --api-key applies to
// whichever supported provider FromEnv selects.
func aiEnvironment(options TestOptions, getenv func(string) string) func(string) string {
	return func(name string) string {
		switch name {
		case "FLOWBATON_AI_BASE_URL":
			if value := strings.TrimSpace(options.APIURL); value != "" {
				return value
			}
		case "OPENAI_API_KEY", "ANTHROPIC_API_KEY":
			if value := strings.TrimSpace(options.APIKey); value != "" {
				return value
			}
		}
		return getenv(name)
	}
}

// executionID derives a run identity from the clock the run itself uses, so a
// test with a fake clock gets a stable id instead of a wall-clock one.
func (session DeviceSession) executionID(clock engine.Clock) string {
	if session.ExecutionID != "" {
		return session.ExecutionID
	}
	return "flowbaton-" + clock.Now().UTC().Format("20060102-150405.000000000")
}

// DefaultOutputDirectory resolves where a run's artifacts go.
//
// specs/03-cli-tooling.md section 1 gives the precedence: an explicit
// --test-output-dir wins, then --debug-output, then a timestamped directory
// under the user's home. --flatten-debug-output drops the timestamp segment,
// which is what a CI job wants when it collects a fixed path.
func DefaultOutputDirectory(options TestOptions, home string, now time.Time) string {
	if options.TestOutputDir != "" {
		return options.TestOutputDir
	}
	if options.DebugOutput != "" {
		return options.DebugOutput
	}
	root := filepath.Join(home, ".flowbaton", "tests")
	if options.FlattenDebugOutput {
		return root
	}
	return filepath.Join(root, now.Format("2006-01-02_150405"))
}
