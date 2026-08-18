package cli

// `flowbaton explore` — one autonomous AI-driven exploration session against
// an app on a device.
//
// The runner owns flags, driver lifetime, and artifact placement; the session
// itself is explore.RunSession. The model and crew constructors are injected
// fields because they land in sibling slices: until both are wired, the
// command refuses with a typed error instead of half-running.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/explore"
)

// ExploreUsage is the one-line usage for the subcommand.
const ExploreUsage = "usage: flowbaton explore --app ID -p ios|android|web [--device UDID] " +
	"[--driver-port PORT] [--max-tests N] [--max-steps N] [--styles LIST] " +
	"[--state-dir DIR] [--output DIR] [--session-name NAME] [--pilot] [--record] " +
	"[--api-key KEY] [--api-url URL]\n"

// ErrExploreNotAssembled reports that this build has no exploration crew
// wiring behind the runner's injectable seams.
var ErrExploreNotAssembled = errors.New("explore support is not assembled in this build")

// Explore session bounds an operator did not override.
const (
	defaultExploreMaxTests = 5
	defaultExploreMaxSteps = 30
)

func defaultExploreStyles() []string {
	return []string{"normal", "curious", "edge"}
}

// ExploreDeps carries everything crew assembly needs.
type ExploreDeps struct {
	Driver device.Driver
	Models explore.ModelSet
	Config explore.Config
	Stdout io.Writer
}

// ExploreRunner runs one exploration session end to end: resolve the device,
// open the driver once, drive the session, write the report and exported
// flows, close the driver.
type ExploreRunner struct {
	// NewModels builds the tiered model set from the environment. Nil means
	// this build carries no model wiring, and the command refuses after flag
	// validation.
	NewModels func(getenv func(string) string) (explore.ModelSet, error)
	// NewCrew assembles the role implementations. Nil refuses like NewModels.
	NewCrew func(deps ExploreDeps) (explore.Crew, error)
	// NewDriver is injected by tests; nil constructs the real platform driver
	// through the same path the test session uses.
	NewDriver func(ctx context.Context, options TestOptions, udid string, port int) (device.Driver, error)
	// Clock is optional; nil uses the wall clock. The default session name
	// and output directory read it.
	Clock engine.Clock
	// Getenv is optional; nil reads the process environment.
	Getenv func(string) string
	// Environ is optional; nil reads the process environment. It feeds the
	// same diagnostic-port fallback `hierarchy` uses.
	Environ func() []string
}

// exploreOptions is one parsed `explore` command line.
type exploreOptions struct {
	AppID       string
	Platform    string
	Device      string
	DriverPort  int
	MaxTests    int
	MaxSteps    int
	Styles      []string
	StateDir    string
	OutputDir   string
	SessionName string
	Pilot       bool
	Record      bool
	APIKey      string
	APIURL      string
}

// Run executes one `explore` invocation and returns its process exit code.
// A completed session exits 0 even when scenarios failed — the report is the
// artifact; only run-level failures exit nonzero.
func (runner ExploreRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, err := parseExploreArgs(args)
	if err != nil {
		return reportExploreError(stderr, err, true)
	}
	if _, _, err := runner.executeSession(ctx, options, stdout, stderr); err != nil {
		return reportExploreError(stderr, err, false)
	}
	return ExitOK
}

// executeSession is Run past its argument parsing. It returns the report
// markdown and the exported flow paths so the MCP tool can serve them.
func (runner ExploreRunner) executeSession(
	ctx context.Context, options exploreOptions, stdout, stderr io.Writer,
) (string, []string, error) {
	// The seams are checked after flag validation so a usage mistake stays a
	// usage error even in a build without exploration support.
	if runner.NewModels == nil || runner.NewCrew == nil {
		return "", nil, ErrExploreNotAssembled
	}
	options = runner.applyExploreDefaults(options)

	// Fail closed before any device work: exploration is AI-driven, and a
	// session that opened a driver first would leave the operator a device
	// mutation with nothing to show for it.
	models, err := runner.NewModels(aiEnvironment(
		TestOptions{APIKey: options.APIKey, APIURL: options.APIURL}, runner.getenv()))
	if err != nil {
		return "", nil, fmt.Errorf("explore: models: %w", err)
	}
	if models.Worker == nil || models.Manager == nil || models.Vision == nil {
		return "", nil, fmt.Errorf(
			"%w: set FLOWBATON_AI_PROVIDER plus OPENAI_API_KEY or ANTHROPIC_API_KEY, or pass --api-key",
			explore.ErrNoAIProvider)
	}

	if err := os.MkdirAll(options.OutputDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("explore: output directory: %w", err)
	}

	port := options.DriverPort
	if port == 0 {
		port, err = diagnosticPort(options.Platform, runner.environ())
		if err != nil {
			return "", nil, err
		}
	}
	// ReinstallDriver mirrors the `test` default: a plain device has no
	// operator-managed runner listening, so explore must provision one.
	driver, err := runner.newDriver()(
		ctx, TestOptions{Platform: options.Platform, ReinstallDriver: true}, options.Device, port)
	if err != nil {
		return "", nil, err
	}
	if err := driver.Open(ctx); err != nil {
		return "", nil, fmt.Errorf("explore: opening %s: %w", driver.Name(), err)
	}

	report, crew, sessionErr := runner.session(ctx, options, models, driver, stdout)

	// Cleanup mirrors DeviceSession.Execute: fresh bounded contexts derived
	// via WithoutCancel, so a cancelled session still stops its recording and
	// closes its driver; every error is joined, never swallowed.
	closeCtx, cancelClose := context.WithTimeout(
		context.WithoutCancel(ctx), deviceSessionCleanupTimeout)
	closeErr := driver.Close(closeCtx)
	cancelClose()
	if closeErr != nil {
		closeErr = fmt.Errorf("explore: closing driver: %w", closeErr)
	}
	if err := errors.Join(sessionErr, closeErr); err != nil {
		// The session loop hands back what it has with every error, and a run
		// that died mid-scenario is exactly when its steps are worth reading.
		// A log that cannot be written must not replace the failure itself.
		if path, logErr := writeStepLog(report, options); logErr != nil {
			fmt.Fprintf(stderr, "explore: the step log could not be written: %v\n", logErr)
		} else if path != "" {
			fmt.Fprintf(stderr, "explore: steps up to the failure are in %s\n", path)
		}
		return "", nil, err
	}
	return writeExploreArtifacts(report, crew, options, stdout, stderr)
}

// session runs the exploration between Open and Close: assemble the crew,
// bracket the run with a recording when asked, and drive the session loop.
func (runner ExploreRunner) session(
	ctx context.Context,
	options exploreOptions,
	models explore.ModelSet,
	driver device.Driver,
	stdout io.Writer,
) (*explore.SessionReport, explore.Crew, error) {
	config := explore.Config{
		AppID:           options.AppID,
		Platform:        options.Platform,
		StateDir:        options.StateDir,
		OutputDir:       options.OutputDir,
		MaxTests:        options.MaxTests,
		MaxStepsPerTest: options.MaxSteps,
		Styles:          options.Styles,
		PilotEnabled:    options.Pilot,
		RecordVideo:     options.Record,
		SessionName:     options.SessionName,
		Clock:           runner.nowFunc(),
	}
	crew, err := runner.NewCrew(ExploreDeps{
		Driver: driver, Models: models, Config: config, Stdout: stdout,
	})
	if err != nil {
		return nil, explore.Crew{}, fmt.Errorf("explore: crew: %w", err)
	}

	var recordingFinalizer func(context.Context) error
	if options.Record {
		recordingFinalizer, err = startSessionRecording(
			ctx, exploreRecordingController(driver, options.OutputDir), options.SessionName)
		if err != nil {
			return nil, crew, err
		}
	}

	report, sessionErr := explore.RunSession(ctx, config, crew)

	var recordingErr error
	if recordingFinalizer != nil {
		recordingCtx, cancelRecording := context.WithTimeout(
			context.WithoutCancel(ctx), deviceSessionCleanupTimeout)
		recordingErr = recordingFinalizer(recordingCtx)
		cancelRecording()
	}
	return report, crew, errors.Join(sessionErr, recordingErr)
}

// exploreRecordingController records into the session's output directory
// rather than the working directory the test contract uses: explore has a
// directory of its own, and that is where an operator looks for the video.
func exploreRecordingController(driver device.Driver, directory string) engine.RecordingController {
	recorder, ok := driver.(screenRecordingDriver)
	if !ok {
		return UnsupportedRecordingController{}
	}
	return NewDriverRecordingController(recorder, directory)
}

// writeExploreArtifacts renders the completed session: markdown to stdout and
// the report file, passing runs to flow YAML. A report that could not be
// written fails the run — exiting 0 would tell an agent it has artifacts it
// does not.
func writeExploreArtifacts(
	report *explore.SessionReport,
	crew explore.Crew,
	options exploreOptions,
	stdout, stderr io.Writer,
) (string, []string, error) {
	markdown := report.Markdown
	if markdown != "" && !strings.HasSuffix(markdown, "\n") {
		markdown += "\n"
	}
	if _, err := io.WriteString(stdout, markdown); err != nil {
		return "", nil, fmt.Errorf("explore: writing report to stdout: %w", err)
	}
	reportPath := filepath.Join(options.OutputDir, "report-"+options.SessionName+".md")
	if err := os.WriteFile(reportPath, []byte(markdown), 0o644); err != nil {
		return "", nil, fmt.Errorf("explore: writing report: %w", err)
	}
	if _, err := writeStepLog(report, options); err != nil {
		return "", nil, err
	}

	passing := make([]explore.TestResult, 0, len(report.Results))
	for _, result := range report.Results {
		if result.Status == explore.TestPassed {
			passing = append(passing, result)
		}
	}
	if len(passing) == 0 {
		return markdown, nil, nil
	}
	if crew.Exporter == nil {
		fmt.Fprintln(stderr, "explore: no flow exporter is assembled in this build; skipping flow export")
		return markdown, nil, nil
	}
	flowsDir := filepath.Join(options.OutputDir, "flows")
	if err := os.MkdirAll(flowsDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("explore: flows directory: %w", err)
	}
	flowPaths := make([]string, 0, len(passing))
	for index, result := range passing {
		data, err := crew.Exporter.ExportFlow(&result, options.AppID)
		if err != nil {
			return "", nil, fmt.Errorf("explore: exporting flow for %q: %w", result.Scenario.Name, err)
		}
		path := filepath.Join(flowsDir, fmt.Sprintf("flow-%02d.yaml", index+1))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", nil, fmt.Errorf("explore: writing flow: %w", err)
		}
		flowPaths = append(flowPaths, path)
	}
	return markdown, flowPaths, nil
}

// writeStepLog writes the step log and answers the path it wrote.
//
// It writes even when nothing ran, because session names repeat: a log left
// by an earlier run under the same name would be read as this run's evidence.
// Saying "no scenario ran" is the honest replacement.
func writeStepLog(report *explore.SessionReport, options exploreOptions) (string, error) {
	path := filepath.Join(options.OutputDir, "steps-"+options.SessionName+".md")
	if err := os.WriteFile(path, []byte(stepLogMarkdown(report)), 0o644); err != nil {
		return "", fmt.Errorf("explore: writing step log: %w", err)
	}
	return path, nil
}

// stepLogMarkdown renders what the agent did, scenario by scenario. The report
// answers what the app did; a run that ends without a product verdict leaves
// the tool calls as the only record, and re-running a session to recover them
// costs a live device and a model budget.
func stepLogMarkdown(report *explore.SessionReport) string {
	builder := &strings.Builder{}
	builder.WriteString("# session step log\n")
	if report == nil || len(report.Results) == 0 {
		builder.WriteString("\n(no scenario ran)\n")
		return builder.String()
	}
	for _, result := range report.Results {
		fmt.Fprintf(builder, "\n## %s (%s)\n", result.Scenario.Name, result.Status)
		for _, line := range explore.StepLines(result.Steps) {
			fmt.Fprintf(builder, "- %s\n", line)
		}
	}
	return builder.String()
}

// applyExploreDefaults resolves the values only the environment can supply:
// the session name from the clock, the state and output directories from the
// user's home. It returns a filled copy.
func (runner ExploreRunner) applyExploreDefaults(options exploreOptions) exploreOptions {
	now := runner.now()
	if options.SessionName == "" {
		options.SessionName = "explore-" + now.UTC().Format("20060102-150405")
	}
	if options.StateDir == "" || options.OutputDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			// The same fallback the test runner takes: a missing home
			// directory is not a reason to abandon a run.
			home = "."
		}
		if options.StateDir == "" {
			options.StateDir = filepath.Join(home, ".flowbaton", "explore", options.AppID)
		}
		if options.OutputDir == "" {
			options.OutputDir = filepath.Join(
				home, ".flowbaton", "explore-runs", now.Format("2006-01-02_150405"))
		}
	}
	return options
}

func (runner ExploreRunner) newDriver() func(context.Context, TestOptions, string, int) (device.Driver, error) {
	if runner.NewDriver != nil {
		return runner.NewDriver
	}
	return func(ctx context.Context, options TestOptions, udid string, port int) (device.Driver, error) {
		return newDriver(ctx, options, udid, port, 1)
	}
}

func (runner ExploreRunner) getenv() func(string) string {
	if runner.Getenv != nil {
		return runner.Getenv
	}
	return os.Getenv
}

func (runner ExploreRunner) environ() []string {
	if runner.Environ != nil {
		return runner.Environ()
	}
	return os.Environ()
}

func (runner ExploreRunner) now() time.Time {
	if runner.Clock != nil {
		return runner.Clock.Now()
	}
	return time.Now()
}

func (runner ExploreRunner) nowFunc() func() time.Time {
	if runner.Clock != nil {
		return runner.Clock.Now
	}
	return nil
}

// parseExploreArgs reads an `explore` command line. Unknown flags are
// refused during parsing, before any device or provider work.
func parseExploreArgs(args []string) (exploreOptions, error) {
	options := exploreOptions{
		MaxTests: defaultExploreMaxTests,
		MaxSteps: defaultExploreMaxSteps,
		Styles:   defaultExploreStyles(),
	}
	values := map[string]*string{
		"--app":          &options.AppID,
		"-p":             &options.Platform,
		"--platform":     &options.Platform,
		"--device":       &options.Device,
		"--state-dir":    &options.StateDir,
		"--output":       &options.OutputDir,
		"--session-name": &options.SessionName,
		"--api-key":      &options.APIKey,
		"--api-url":      &options.APIURL,
	}
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch argument {
		case "--pilot":
			options.Pilot = true
			continue
		case "--record":
			options.Record = true
			continue
		}
		if argument == "" || !strings.HasPrefix(argument, "-") {
			return exploreOptions{}, usageErrorf(
				"flowbaton explore takes no positional arguments, got %q", argument)
		}
		value, consumed, err := nextValue(args, index)
		if err != nil {
			return exploreOptions{}, err
		}
		if target, ok := values[argument]; ok {
			*target = value
			index += consumed
			continue
		}
		switch argument {
		case "--driver-port":
			port, portErr := strconv.Atoi(strings.TrimSpace(value))
			if portErr != nil || port < 1 || port > 65535 {
				return exploreOptions{}, usageErrorf(
					"option --driver-port requires a port between 1 and 65535, got %q", value)
			}
			options.DriverPort = port
		case "--max-tests":
			options.MaxTests, err = positiveCount(argument, value)
			if err != nil {
				return exploreOptions{}, err
			}
		case "--max-steps":
			options.MaxSteps, err = positiveCount(argument, value)
			if err != nil {
				return exploreOptions{}, err
			}
		case "--styles":
			options.Styles = splitList(value)
		default:
			return exploreOptions{}, usageErrorf("unknown option %q for flowbaton explore", argument)
		}
		index += consumed
	}
	return options, validateExploreOptions(options)
}

// validateExploreOptions holds the rules both entry points share: the CLI
// parser and the MCP tool both build an exploreOptions and must be refused
// for the same shapes.
func validateExploreOptions(options exploreOptions) error {
	if strings.TrimSpace(options.AppID) == "" {
		return usageErrorf("flowbaton explore requires --app <application id>")
	}
	switch strings.ToLower(strings.TrimSpace(options.Platform)) {
	case "ios", "android", "web":
	case "":
		return usageErrorf("a platform is required: pass -p ios, -p android or -p web")
	default:
		return usageErrorf("unsupported platform %q; supported: ios, android, web", options.Platform)
	}
	if options.MaxTests <= 0 {
		return usageErrorf("the test budget must be positive, got %d", options.MaxTests)
	}
	if options.MaxSteps <= 0 {
		return usageErrorf("the step bound must be positive, got %d", options.MaxSteps)
	}
	if len(options.Styles) == 0 {
		return usageErrorf("option --styles requires at least one style name")
	}
	if options.DriverPort < 0 || options.DriverPort > 65535 {
		return usageErrorf("driver port %d is not a valid port", options.DriverPort)
	}
	return nil
}

// reportExploreError prints a failure and returns its exit code, with the
// usage line for command lines that could not be understood.
func reportExploreError(stderr io.Writer, err error, mayBeUsage bool) int {
	fmt.Fprintf(stderr, "flowbaton explore: %s\n", err)
	code := ExitCodeFor(err)
	if mayBeUsage && code == ExitInvalid {
		_, _ = io.WriteString(stderr, ExploreUsage)
	}
	return code
}

// exploreRunnerInvoker adapts ExploreRunner to the MCP explore tool. Its
// zero value carries the unassembled seams, so the tool answers with the
// same typed refusal the CLI gives.
type exploreRunnerInvoker struct {
	runner ExploreRunner
}

func (invoker exploreRunnerInvoker) Explore(
	ctx context.Context, request ExploreToolOptions,
) (ExploreToolResult, error) {
	options := exploreOptions{
		AppID:      request.AppID,
		Platform:   request.Platform,
		Device:     request.Device,
		DriverPort: request.DriverPort,
		MaxTests:   request.MaxTests,
		MaxSteps:   request.MaxSteps,
		OutputDir:  request.OutputDir,
		Styles:     defaultExploreStyles(),
	}
	if options.MaxTests == 0 {
		options.MaxTests = defaultExploreMaxTests
	}
	if options.MaxSteps == 0 {
		options.MaxSteps = defaultExploreMaxSteps
	}
	if err := validateExploreOptions(options); err != nil {
		return ExploreToolResult{}, err
	}
	markdown, flowPaths, err := invoker.runner.executeSession(ctx, options, io.Discard, io.Discard)
	if err != nil {
		return ExploreToolResult{}, err
	}
	return ExploreToolResult{Report: markdown, Flows: flowPaths}, nil
}
