package ios

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	// SpringBoard can briefly refuse the runner while XCTest replaces its app.
	// Three total attempts keep that known transient bounded without replaying
	// arbitrary exit 65 failures.
	runnerInstallBusyRetryDelay = 2 * time.Second
	runnerInstallBusyAttempts   = 3
)

const runnerInstallBusySignature = "is installing or uninstalling, and cannot be launched"

// RunnerStartupError carries xcodebuild's bounded output separately from the
// operator-facing error. Callers that own an artifact directory can retain the
// raw diagnostic without parsing prose, even though startup failed before any
// flow command or report existed.
type RunnerStartupError struct {
	err        error
	diagnostic string
}

func (err *RunnerStartupError) Error() string { return err.err.Error() }

func (err *RunnerStartupError) Unwrap() error { return err.err }

// RunnerStartupDiagnostics returns the bounded xcodebuild tail captured for
// this failed launch. It may be empty when xcodebuild produced no output.
func (err *RunnerStartupError) RunnerStartupDiagnostics() string { return err.diagnostic }

func runnerStartupError(err error, process runnerProcess) error {
	return &RunnerStartupError{err: err, diagnostic: strings.TrimSpace(process.diagnostics())}
}

// runnerEnvPrefix is how a variable reaches the runner. xcodebuild passes only
// TEST_RUNNER_-prefixed names into the test process on the simulator, with the
// prefix stripped — a plain PORT never arrives.
const runnerEnvPrefix = "TEST_RUNNER_"

// runnerIDEnv carries the id a managed runner echoes in /status. A stranger
// on the port — another driver's runner, or an older build — answers with a
// different id or none, and the host does not take it for its own child.
const runnerIDEnv = "FLOWBATON_RUNNER_ID"

func newRunnerID() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("runner id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

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
	// exitReason answers the same thing without consuming it, for the paths
	// that only want to explain a failure they already have. Receiving from
	// exited() is destructive, and the stop path waits on that channel: a
	// reader that took the value would leave stopRunner waiting out its whole
	// ten-second budget before killing a process that was already dead.
	exitReason() string
	// diagnostics returns the bounded tail xcodebuild has written so far. It
	// remains useful while the child is alive, when exitReason is necessarily
	// empty but startup has stalled.
	diagnostics() string
}

// runnerArgs builds the xcodebuild invocation that launches the managed runner.
func (driver *Driver) runnerArgs() []string {
	return []string{
		"test-without-building",
		"-xctestrun", driver.runner.XCTestRun,
		"-destination", "platform=iOS Simulator,id=" + driver.udid,
		"-only-testing:" + runnerServeTest,
		// The host ends this runner, so xcodebuild reads every session as a
		// failed test and would collect a whole simulator sysdiagnose --
		// 168M of system log archive -- into the directory below.
		"-collect-test-diagnostics", "never",
		// Somewhere this driver can delete. The default is a new hashed
		// directory under Xcode's DerivedData per launch, kept forever.
		"-derivedDataPath", driver.derivedData,
	}
}

func (driver *Driver) runnerEnv() []string {
	return append(os.Environ(),
		runnerEnvPrefix+"FLOWBATON_RUNNER_SERVE=1",
		runnerEnvPrefix+"PORT="+strconv.Itoa(driver.port),
		runnerEnvPrefix+runnerIDEnv+"="+driver.runnerID,
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
	startupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// A runner already answering on the port cannot be the one about to be
	// started. Another driver holds it — one serving a different simulator
	// answers /status just as well — and the child would bind nothing while
	// every request went to the stranger. A session took its screens from
	// another simulator that way.
	if probeErr := driver.client.Status(startupCtx); probeErr == nil {
		if err := contextBudgetError(startupCtx); err != nil {
			return fmt.Errorf("checking the runner port for %s within the %v startup budget: %w", driver.udid, timeout, err)
		}
		return fmt.Errorf(
			"127.0.0.1:%d already answers as a runner before this driver started one for %s; another FlowBaton process or stale runner holds the port — stop that process, or set FLOWBATON_DRIVER_PORTS to an unused port (for example FLOWBATON_DRIVER_PORTS=22090)",
			driver.port, driver.udid)
	} else if err := contextBudgetError(startupCtx); err != nil {
		return fmt.Errorf("checking the runner port for %s within the %v startup budget: %w", driver.udid, timeout, err)
	}
	for attempt := 1; attempt <= runnerInstallBusyAttempts; attempt++ {
		if err := contextBudgetError(startupCtx); err != nil {
			return fmt.Errorf("starting the runner for %s within the %v startup budget: %w", driver.udid, timeout, err)
		}
		process, startErr := driver.startManagedRunnerAttempt(startupCtx)
		if startErr != nil {
			return startErr
		}
		if runErr := driver.awaitRunner(startupCtx, timeout, process); runErr == nil {
			return nil
		} else {
			_ = driver.stopRunnerProcess()
			if !isRunnerInstallBusy(runErr) {
				return runErr
			}
			if attempt == runnerInstallBusyAttempts {
				return fmt.Errorf(
					"the runner for %s remained install-busy after %d attempts: %w",
					driver.udid, attempt, runErr)
			}
			if waitErr := driver.waitForInstallBusyRetry(startupCtx); waitErr != nil {
				return fmt.Errorf(
					"the runner for %s remained install-busy after %d attempts within the %v startup budget: %w",
					driver.udid, attempt, timeout, errors.Join(waitErr, runErr))
			}
		}
	}
	panic("unreachable")
}

func (driver *Driver) startManagedRunnerAttempt(ctx context.Context) (runnerProcess, error) {
	var err error
	if driver.runnerID, err = newRunnerID(); err != nil {
		return nil, err
	}
	if driver.derivedData, err = os.MkdirTemp("", "flowbaton-ios-runner-"); err != nil {
		return nil, fmt.Errorf("creating a derived-data directory for the runner: %w", err)
	}
	spawn := driver.spawnRunner
	if spawn == nil {
		spawn = realRunnerSpawn
	}
	process, err := spawn(ctx, driver.runnerArgs(), driver.runnerEnv())
	if err != nil {
		driver.removeDerivedData()
		return nil, fmt.Errorf("starting the runner for %s: %w", driver.udid, err)
	}
	driver.process = process
	driver.client.SetTransportHint(process.exitReason)
	return process, nil
}

func isRunnerInstallBusy(err error) bool {
	return err != nil && strings.Contains(err.Error(), runnerInstallBusySignature)
}

func (driver *Driver) waitForInstallBusyRetry(ctx context.Context) error {
	delay := driver.installBusyRetryDelay
	if delay <= 0 {
		delay = runnerInstallBusyRetryDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// contextBudgetError checks the absolute deadline as well as Err. A response
// can race the context timer at the deadline; accepting it because Err has not
// been published yet would let startup succeed outside its wall-time budget.
func contextBudgetError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return context.DeadlineExceeded
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
	var lastErr error
	for {
		var identity string
		if identity, lastErr = driver.client.Identity(ctx); lastErr == nil {
			if err := contextBudgetError(ctx); err != nil {
				return driver.runnerContextError(err, timeout, lastErr, process)
			}
			if identity == driver.runnerID {
				return nil
			}
			// An answer without this driver's id is a stranger who took the
			// port after the probe; the child that lost the bind is dying or
			// dead, and its exit reason is the useful part when it is in.
			reason := "the child is still running"
			select {
			case exit := <-process.exited():
				reason = exit.Error()
			default:
			}
			return fmt.Errorf(
				"127.0.0.1:%d answers as runner %q, not the one started for %s; someone else holds the port: %s",
				driver.port, identity, driver.udid, reason)
		}
		select {
		case reason := <-process.exited():
			return runnerStartupError(fmt.Errorf(
				"the runner for %s stopped before it answered: %v", driver.udid, reason), process)
		case <-ctx.Done():
			return driver.runnerContextError(ctx.Err(), timeout, lastErr, process)
		case <-time.After(driver.startupPoll):
		}
	}
}

func (driver *Driver) runnerContextError(
	ctxErr error, timeout time.Duration, lastErr error, process runnerProcess,
) error {
	if !errors.Is(ctxErr, context.DeadlineExceeded) {
		return runnerStartupError(fmt.Errorf(
			"waiting for the runner for %s: %w", driver.udid, ctxErr), process)
	}
	failure := fmt.Errorf(
		"the runner for %s did not answer within %v (%s overrides the wait, in milliseconds): %w",
		driver.udid, timeout, startupTimeoutEnv, errors.Join(ctxErr, lastErr))
	if diagnostic := strings.TrimSpace(process.diagnostics()); diagnostic != "" {
		return runnerStartupError(fmt.Errorf(
			"%w; xcodebuild was still running and its latest output was:\n%s\nThe runner or simulator app-launch service may be stalled; verify that this simulator can launch an installed app directly, then restart the dedicated simulator and retry if it cannot",
			failure, diagnostic), process)
	}
	return runnerStartupError(fmt.Errorf(
		"%w; xcodebuild was still running but produced no launch diagnostic; the runner or simulator app-launch service may be stalled — verify that this simulator can launch an installed app directly, then restart the dedicated simulator and retry if it cannot",
		failure), process)
}

// stopRunnerProcess ends a runner this driver started, once.
func (driver *Driver) stopRunnerProcess() error {
	process := driver.process
	if process == nil {
		return nil
	}
	driver.process = nil
	// After the child, so xcodebuild is not still writing into it.
	defer driver.removeDerivedData()
	return process.stopRunner()
}

// removeDerivedData discards the runner's throwaway log directory. Its loss is
// not worth failing a session over: the tail of what xcodebuild said is
// already carried in the error, and a directory left behind costs disk, not
// correctness.
func (driver *Driver) removeDerivedData() {
	if driver.derivedData == "" {
		return
	}
	_ = os.RemoveAll(driver.derivedData)
	driver.derivedData = ""
}

// xcodebuildRunner is the real child.
type xcodebuildRunner struct {
	cmd *exec.Cmd
	// output is the tail xcodebuild wrote, which is where a wrong .xctestrun
	// path or an unbooted simulator says so.
	output *boundedBuffer
	done   chan error
	// exit latches what done carried, so asking why the child died does not
	// take the answer away from whoever asks next.
	exit exitLatch
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
	return watchRunnerChild(cmd, output), nil
}

// watchRunnerChild wraps a started child and keeps watch for its exit.
func watchRunnerChild(cmd *exec.Cmd, output *boundedBuffer) *xcodebuildRunner {
	runner := &xcodebuildRunner{cmd: cmd, output: output, done: make(chan error, 1)}
	go func() {
		reason := runner.describeExit(cmd.Wait())
		runner.exit.set(reason)
		runner.done <- reason
	}()
	return runner
}

// exitLatch remembers a child's exit for every later reader.
type exitLatch struct {
	mu     sync.Mutex
	exited bool
	reason error
}

func (latch *exitLatch) set(reason error) {
	latch.mu.Lock()
	defer latch.mu.Unlock()
	latch.exited, latch.reason = true, reason
}

// String is empty while the child runs, so a caller can tell "still up" from
// "gone, and here is why" without a second question.
func (latch *exitLatch) String() string {
	latch.mu.Lock()
	defer latch.mu.Unlock()
	switch {
	case !latch.exited:
		return ""
	case latch.reason == nil:
		return "the runner exited"
	default:
		return "the runner exited: " + latch.reason.Error()
	}
}

func (runner *xcodebuildRunner) describeExit(err error) error {
	if tail := runner.output.String(); tail != "" {
		return fmt.Errorf("%v: %s", err, tail)
	}
	return err
}

func (runner *xcodebuildRunner) exited() <-chan error { return runner.done }

func (runner *xcodebuildRunner) exitReason() string { return runner.exit.String() }

func (runner *xcodebuildRunner) diagnostics() string {
	if runner.output == nil {
		return ""
	}
	return runner.output.String()
}

func (runner *xcodebuildRunner) stopRunner() error {
	// A child that is already gone is not a stop failure, and this is the one
	// moment its exit reason is both latched and still worth printing.
	// Signalling a dead process group answers "no such process", which is the
	// errno for a question nobody asked: three sessions closed with that and
	// none of them said the runner had died.
	if reason := runner.exit.String(); reason != "" {
		return errors.New(reason)
	}
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
