package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/workspace"
)

// TestUsage is the one-line usage for the subcommand.
const TestUsage = "usage: flowbaton test [options] FILE|DIR...\n"

// TestRunner discovers flows, preflights them, splits them into shards, and
// runs each shard on its own device.
//
// Discovery, preflight, and shard planning all run BEFORE anything touches a
// device. That order is the point: a config error, an unknown command, a tag
// filter that selects nothing, or a shard count with no devices behind it are
// all knowable without a simulator, and finding them after devices have been
// acquired wastes the slowest part of a run and reports a setup mistake as a
// flow failure.
type TestRunner struct {
	// NewSession acquires one shard's device and executes its flows. It is
	// injected because no production session can be built without a device; a
	// nil factory falls back to the real one.
	NewSession SessionFactory
	Loader     capability.FlowLoader
	// Clock is optional; nil uses the wall clock. It fixes the run's own notion
	// of now, which both the default output directory and the report timestamp
	// read, so two identical runs render identical bytes.
	Clock engine.Clock
	// Environ is optional; nil reads the process environment. It supplies the
	// shell's FLOWBATON_ variables, per specs/01-core-engine.md:101.
	Environ func() []string
	// AllocatePort is optional; nil asks the operating system for an ephemeral
	// port. Injected so a test can pin the ports a sharded run picks.
	AllocatePort func() (int, error)
	// PollInterval is how often continuous mode re-stamps its watch set; zero
	// uses defaultPollInterval. Injected so a test does not wait on it.
	PollInterval time.Duration
	// RecordTo is set by RecordRunner, never by argv. See TestOptions.RecordTo.
	RecordTo string
}

// SessionFactory builds the session for one shard.
type SessionFactory func(Shard, TestOptions) (TestSession, error)

// TestSession is the boundary between the host pipeline and a device.
type TestSession interface {
	Execute(context.Context, *engine.Program, TestOptions) ([]engine.FlowResult, error)
}

// Run executes one `test` invocation and returns its process exit code.
func (runner TestRunner) Run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	options, err := ParseTestOptions(args)
	if err != nil {
		return reportTestError(stderr, err, true)
	}
	return runner.RunOptions(ctx, options, stdout, stderr)
}

// RunOptions is Run past its argument parsing. `record` uses it: it has to read
// the positionals itself (a flow plus an optional output file), so it parses
// the same options and then hands them over rather than rebuilding a command
// line for this to parse a second time.
func (runner TestRunner) RunOptions(
	ctx context.Context,
	options TestOptions,
	stdout io.Writer,
	stderr io.Writer,
) int {
	// The shell's FLOWBATON_ variables join the operator's map before anything
	// reads it, so every downstream consumer sees one environment.
	options.Env = mergeShellEnvironment(runner.environ(), options.Env)
	options.RecordTo = runner.RecordTo

	if options.Continuous {
		return runner.runContinuously(ctx, options, stdout, stderr)
	}
	return runner.runOnce(ctx, options, stdout, stderr).code
}

// runOnce is one discovery-to-report pass. It returns the files the pass turned
// out to depend on alongside its exit code, because continuous mode has to
// watch them — including after a pass that failed before it learned much, which
// is the pass whose files are about to be edited.
func (runner TestRunner) runOnce(
	ctx context.Context,
	options TestOptions,
	stdout io.Writer,
	stderr io.Writer,
) runAttempt {
	// Widest to narrowest: the roots are all that is known until discovery
	// succeeds, and the closure only exists once preflight has passed. The
	// snapshot is stamped alongside, so a failure at any stage still hands
	// continuous mode something it can watch and check.
	watched := walkRoots(options.Roots)
	snapshot := stampFiles(watched)

	plan, err := workspace.Discover(options.Roots, workspace.Options{
		ConfigPath:  options.ConfigPath,
		IncludeTags: options.IncludeTags,
		ExcludeTags: options.ExcludeTags,
	})
	if err != nil {
		return runAttempt{reportTestError(stderr, err, false), watched, snapshot}
	}
	if len(plan.Flows) == 0 {
		// Discovery refuses an empty selection itself, and names the filter
		// that caused it. This is the fail-closed backstop for a plan that
		// somehow comes back empty without an error: an empty selection that
		// exited 0 would report a passing suite that ran nothing at all.
		return runAttempt{reportTestError(stderr, fmt.Errorf(
			"no flows selected from %s", strings.Join(options.Roots, ", ")), false), watched, snapshot}
	}

	loader := runner.Loader
	if loader == nil {
		loader = capability.FileLoader{}
	}
	// Preflight covers EVERY selected flow, not just one shard's, so an
	// unsupported command fails the run before any device is acquired.
	program, err := engine.Prepare(ctx, plan.ExecutionPlan(), loader)
	if err != nil {
		return runAttempt{reportTestError(stderr, err, false), watched, snapshot}
	}
	// The closure is known now, and it is both narrower and more accurate than
	// the walk: exactly the flows, scripts, and media this run reads.
	watched = watchSetFor(program)
	// Merged, not replaced: the walk's stamps are older and so catch more, while
	// these cover closure files the walk never saw — a shared subflow outside
	// the roots the operator named. Taken between preflight and execution, the
	// last moment before the suite could change anything itself.
	snapshot = mergeStamps(snapshot, stampFiles(watched))

	// A sharded run has no sequence at all — shardable() refuses one — so this
	// only ever bites the single-shard case, where a shard's roots ARE the
	// plan's.
	options.SequencedRoots = len(plan.Sequence)
	options.ContinueOnFailure = plan.ContinueOnFailure

	shards, err := PlanShards(options, plan)
	if err != nil {
		return runAttempt{reportTestError(stderr, err, false), watched, snapshot}
	}

	for _, selected := range plan.Flows {
		fmt.Fprintf(stdout, "selected %s (%s)\n", selected.Name, selected.Path)
	}

	if err := assignDriverPorts(
		shards, basePort(options, runner.environ()),
		driverPortsFrom(runner.environ()), runner.allocatePort()); err != nil {
		return runAttempt{reportTestError(stderr, err, false), watched, snapshot}
	}

	now := runner.now()
	outputDirectory := runner.outputDirectory(options, now)
	assignShardDirectories(shards, outputDirectory)

	results, err := runner.executeShards(ctx, options, shards, loader, stderr)
	if len(results) == 0 {
		if err != nil {
			return runAttempt{reportTestError(stderr, err, false), watched, snapshot}
		}
		return runAttempt{reportResults(stdout, stderr, results), watched, snapshot}
	}

	exitCode := reportResults(stdout, stderr, results)
	if err != nil {
		fmt.Fprintf(stderr, "flowbaton test: %s\n", err)
		exitCode = ExitFailure
	}
	// The report is written for a failing run too — that is the one anybody
	// opens. A report that could not be written fails the run regardless of
	// how the flows went: exiting 0 would tell CI it has a report it does not.
	if reportErr := writeReport(options, results, now); reportErr != nil {
		fmt.Fprintf(stderr, "flowbaton test: %s\n", reportErr)
		return runAttempt{ExitFailure, watched, snapshot}
	}
	return runAttempt{exitCode, watched, snapshot}
}

// executeShards runs every shard at the same time and merges the outcome.
//
// Shards run concurrently because that is the entire reason sharding exists:
// N devices finishing in one device's time. They are merged back in shard
// order so the output does not depend on which device happened to be fastest.
func (runner TestRunner) executeShards(
	ctx context.Context,
	options TestOptions,
	shards []Shard,
	loader capability.FlowLoader,
	stderr io.Writer,
) ([]engine.FlowResult, error) {
	outcomes := make([]shardOutcome, len(shards))
	var group sync.WaitGroup
	for index, shard := range shards {
		group.Add(1)
		go func(index int, shard Shard) {
			defer group.Done()
			outcomes[index] = runner.executeShard(ctx, options, shard, loader, stderr)
		}(index, shard)
	}
	// A failing shard does NOT cancel its siblings. Whether the other devices
	// passed is exactly what tells an operator if the failure is the flow or
	// the device, and cancelling would destroy that answer.
	group.Wait()

	results := make([]engine.FlowResult, 0, len(shards))
	var firstError error
	for index, outcome := range outcomes {
		results = append(results, outcome.results...)
		if outcome.err == nil || firstError != nil {
			continue
		}
		firstError = outcome.err
		if len(shards) > 1 {
			firstError = fmt.Errorf("shard %d: %w", index+1, outcome.err)
		}
	}
	return results, firstError
}

type shardOutcome struct {
	results []engine.FlowResult
	err     error
}

func (runner TestRunner) executeShard(
	ctx context.Context,
	options TestOptions,
	shard Shard,
	loader capability.FlowLoader,
	stderr io.Writer,
) shardOutcome {
	// Preparing per shard rather than slicing one program: a Program is built
	// from a root set, and a shard's root set is its own.
	program, err := engine.Prepare(
		ctx, model.ExecutionPlan{SelectedRoots: shard.Roots}, loader)
	if err != nil {
		return shardOutcome{err: err}
	}
	session, err := runner.session(shard, options)
	if err != nil {
		return shardOutcome{err: err}
	}
	// Execute returns the results it produced ALONGSIDE the error that stopped
	// it. Reporting only the error would hide which flows ran and which never
	// started, which is the first thing an operator needs from a failed suite.
	results, err := session.Execute(ctx, program, options)
	// Written from here, not from runOnce: only the shard knows which output
	// directory is its own, and a merged result list has lost that.
	writeDebugArtifacts(stderr, options, shard, results)
	return shardOutcome{results: results, err: err}
}

func (runner TestRunner) session(shard Shard, options TestOptions) (TestSession, error) {
	if runner.NewSession != nil {
		return runner.NewSession(shard, options)
	}
	// Resolving the device is the LAST step, after everything knowable without
	// one has already passed. An operator who mistyped a flag learns that
	// before being told to connect a simulator.
	return NewDeviceSession(options, shard)
}

func (runner TestRunner) allocatePort() func() (int, error) {
	if runner.AllocatePort != nil {
		return runner.AllocatePort
	}
	return ephemeralPort
}

func (runner TestRunner) environ() []string {
	if runner.Environ != nil {
		return runner.Environ()
	}
	return os.Environ()
}

func (runner TestRunner) now() time.Time {
	if runner.Clock != nil {
		return runner.Clock.Now()
	}
	return time.Now()
}

func (runner TestRunner) outputDirectory(options TestOptions, now time.Time) string {
	home, err := os.UserHomeDir()
	if err != nil {
		// A missing home directory is not a reason to abandon a run; the
		// working directory is a serviceable place for artifacts.
		home = "."
	}
	return DefaultOutputDirectory(options, home, now)
}

// assignShardDirectories gives each shard somewhere of its own to write.
//
// Two devices writing "evidence.png" into one directory produce one file and a
// renamed sibling, and nothing records which device made which. An unsharded
// run keeps writing where it always did — adding a subdirectory there would
// move every existing run's artifacts for no gain.
func assignShardDirectories(shards []Shard, outputDirectory string) {
	for index := range shards {
		shards[index].OutputDirectory = outputDirectory
		if len(shards) > 1 {
			shards[index].OutputDirectory = filepath.Join(
				outputDirectory, fmt.Sprintf("shard-%d", shards[index].Count()))
		}
	}
}

// reportResults prints one line per flow and exits 0 only if every flow
// passed, per specs/03-cli-tooling.md:31.
func reportResults(stdout, stderr io.Writer, results []engine.FlowResult) int {
	if len(results) == 0 {
		// Nothing ran, so nothing passed. Reporting OK here would be the same
		// lie as an empty selection.
		_, _ = io.WriteString(stderr, "flowbaton: no flow produced a result\n")
		return ExitFailure
	}
	exitCode := ExitOK
	for _, result := range results {
		status := "PASS"
		if !passingOutcomes[result.Outcome()] {
			status = "FAIL"
			exitCode = ExitFailure
		}
		fmt.Fprintf(stdout, "%s %s (%s)\n", status, result.Path(), result.Outcome())
		if err := result.ProductError(); err != nil {
			fmt.Fprintf(stderr, "  %s\n", err)
		}
	}
	return exitCode
}

// passingOutcomes lists the outcomes that do NOT fail a run. The set is
// allow-list rather than deny-list on purpose: a new outcome added to the
// engine should fail the suite until someone decides it shouldn't, not pass it
// silently.
var passingOutcomes = map[engine.Outcome]bool{
	engine.Completed: true,
	engine.Skipped:   true,
	engine.Warned:    true,
}

// reportTestError prints a failure and returns its exit code. Usage errors
// also print the usage line, because that is the failure an operator can fix
// by reading one more line.
func reportTestError(stderr io.Writer, err error, mayBeUsage bool) int {
	fmt.Fprintf(stderr, "flowbaton test: %s\n", err)
	code := ExitCodeFor(err)
	if mayBeUsage && code == ExitInvalid {
		_, _ = io.WriteString(stderr, TestUsage)
	}
	return code
}
