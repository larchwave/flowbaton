package ios

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// RunnerBundle names a prebuilt runner the driver may start itself. A nil
// bundle selects operator-started mode, where the runner must already serve.
//
// XCTestRun is the .xctestrun `xcodebuild build-for-testing` leaves in the
// derived-data products directory. It is the whole reason a self-starting
// runner is possible: `test-without-building -xctestrun <path>` needs no Xcode
// project at run time, only the built products the file points at.
type RunnerBundle struct {
	XCTestRun string
}

// runnerServeTest is the one test in the suite that serves the wire. Naming it
// matters: the suite also holds finite tests, and running them all would run
// the server test last, after minutes of unrelated work.
const runnerServeTest = "FlowBatonIOSRunnerUITests/RunnerHostTests/testServeTheWireUntilTheHostIsDone"

const (
	// startupTimeoutEnv overrides the runner startup wait, in milliseconds —
	// the same knob the Android driver reads (specs/04-wire-protocols.md
	// line 93).
	startupTimeoutEnv = "FLOWBATON_DRIVER_STARTUP_TIMEOUT"
	// runnerStartupPoll paces the reachability probe, as on Android.
	runnerStartupPoll = 100 * time.Millisecond
	// runnerStartupTimeout is far longer than Android's 15s because starting
	// this runner is not starting a process: xcodebuild installs the runner
	// app on the simulator, attaches XCTest, and only then starts the test whose
	// body serves requests. Cold installations can take substantially longer,
	// and a budget that expires mid-install
	// would report a working setup as broken.
	runnerStartupTimeout = 180 * time.Second
)

// runnerEnvPrefix is how a variable reaches the runner. xcodebuild passes only
// TEST_RUNNER_-prefixed names into the test process on the simulator, with the
// prefix stripped — a plain PORT never arrives.
const runnerEnvPrefix = "TEST_RUNNER_"

// runnerProcess is the xcodebuild child serving the wire. It is an interface so
// a test drives the whole lifecycle without a simulator.
type runnerProcess interface {
	// stopRunner ends the runner and everything it started.
	stopRunner() error
	// exited fires when the child dies on its own, carrying why. A runner that
	// dies before it answers has failed for a reason worth printing — a wrong
	// .xctestrun path, a simulator that is not booted — and waiting out the
	// whole budget to say "no answer" hides it.
	exited() <-chan error
}

// runnerArgs builds the xcodebuild invocation that launches the managed runner.
func (driver *Driver) runnerArgs() []string {
	return []string{
		"test-without-building",
		"-xctestrun", driver.runner.XCTestRun,
		"-destination", "platform=iOS Simulator,id=" + driver.udid,
		"-only-testing:" + runnerServeTest,
	}
}

func (driver *Driver) runnerEnv() []string {
	return append(os.Environ(),
		runnerEnvPrefix+"FLOWBATON_RUNNER_SERVE=1",
		runnerEnvPrefix+"PORT="+strconv.Itoa(driver.port),
	)
}

// openManagedRunner starts the runner and waits for it to answer. A failure
// here takes the child down with it: an orphaned xcodebuild holds the port for
// its whole hour-long lifetime, and the next run would be refused by a runner
// nobody remembers starting.
func (driver *Driver) openManagedRunner(ctx context.Context) error {
	timeout, err := driver.runnerStartupBudget()
	if err != nil {
		return err
	}
	// A runner already answering on the port cannot be the one about to be
	// started. Another driver holds it — one serving a different simulator
	// answers /status just as well — and the child would bind nothing while
	// every request went to the stranger. A session took its screens from
	// another simulator that way.
	if err := driver.client.Status(ctx); err == nil {
		return fmt.Errorf(
			"127.0.0.1:%d already answers as a runner before this driver started one for %s; another driver holds the port — stop it, or pass --driver-port",
			driver.port, driver.udid)
	}
	spawn := driver.spawnRunner
	if spawn == nil {
		spawn = realRunnerSpawn
	}
	process, err := spawn(ctx, driver.runnerArgs(), driver.runnerEnv())
	if err != nil {
		return fmt.Errorf("starting the runner for %s: %w", driver.udid, err)
	}
	driver.process = process

	if err := driver.awaitRunner(ctx, timeout, process); err != nil {
		_ = driver.stopRunnerProcess()
		return err
	}
	return nil
}

// runnerStartupBudget is the reachability wait, overridable in milliseconds by
// the same variable Android reads.
func (driver *Driver) runnerStartupBudget() (time.Duration, error) {
	raw := os.Getenv(startupTimeoutEnv)
	if raw == "" {
		return driver.startupTimeout, nil
	}
	millis, err := strconv.Atoi(raw)
	if err != nil || millis <= 0 {
		return 0, fmt.Errorf(
			"%s must be a positive millisecond count, not %q", startupTimeoutEnv, raw)
	}
	return time.Duration(millis) * time.Millisecond, nil
}

func (driver *Driver) awaitRunner(
	ctx context.Context, timeout time.Duration, process runnerProcess,
) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if lastErr = driver.client.Status(ctx); lastErr == nil {
			// An answer counts only while the child is alive: a stranger who
			// took the port after the probe answers /status just as well,
			// and the child that lost the bind is gone by now.
			select {
			case reason := <-process.exited():
				return fmt.Errorf(
					"the runner for %s stopped before it answered; 127.0.0.1:%d answers for someone else: %v",
					driver.udid, driver.port, reason)
			default:
				return nil
			}
		}
		select {
		case reason := <-process.exited():
			return fmt.Errorf(
				"the runner for %s stopped before it answered: %v", driver.udid, reason)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(driver.startupPoll):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"the runner for %s did not answer within %v (%s overrides the wait, in milliseconds): %w",
				driver.udid, timeout, startupTimeoutEnv, lastErr)
		}
	}
}

// stopRunnerProcess ends a runner this driver started, once.
func (driver *Driver) stopRunnerProcess() error {
	process := driver.process
	if process == nil {
		return nil
	}
	driver.process = nil
	return process.stopRunner()
}

// xcodebuildRunner is the real child.
type xcodebuildRunner struct {
	cmd *exec.Cmd
	// output is the tail xcodebuild wrote, which is where a wrong .xctestrun
	// path or an unbooted simulator says so.
	output *boundedBuffer
	done   chan error
}

func realRunnerSpawn(_ context.Context, args, environment []string) (runnerProcess, error) {
	cmd := exec.Command("xcodebuild", args...)
	cmd.Env = environment
	output := &boundedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = output, output
	// Its own process group, so stopping it stops the whole test run rather
	// than just the xcodebuild that fronts it.
	configureManagedRunnerCommand(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	runner := &xcodebuildRunner{cmd: cmd, output: output, done: make(chan error, 1)}
	go func() { runner.done <- runner.describeExit(cmd.Wait()) }()
	return runner, nil
}

func (runner *xcodebuildRunner) describeExit(err error) error {
	if tail := runner.output.String(); tail != "" {
		return fmt.Errorf("%v: %s", err, tail)
	}
	return err
}

func (runner *xcodebuildRunner) exited() <-chan error { return runner.done }

func (runner *xcodebuildRunner) stopRunner() error {
	return stopManagedRunnerCommand(runner.cmd, runner.done)
}

// boundedBuffer keeps the last limit bytes written to it. xcodebuild is loud,
// and only the end of what it said explains why it stopped.
type boundedBuffer struct {
	mu     sync.Mutex
	limit  int
	buffer bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buffer.Write(p)
	if excess := b.buffer.Len() - b.limit; excess > 0 {
		b.buffer.Next(excess)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
