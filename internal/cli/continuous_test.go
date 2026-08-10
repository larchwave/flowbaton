package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
)

// specs/03-cli-tooling.md:20 names -c/--continuous "(file-watch rerun)" and :29
// requires a file watcher. These tests pin rerun and cancellation behavior.
//
// The interesting behavior is not the happy path — it is that a watcher which
// dies on a broken file is useless, because saving a broken file is the ordinary
// event in the loop this mode exists for.

// syncBuffer exists because the runner writes from its own goroutine while the
// test reads — a plain bytes.Buffer there is a data race, not a style choice.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// continuousRunner drives continuous mode on a real filesystem with a poll
// interval short enough that a test does not wait on it. A mode that never
// exits on its own has to be stopped from outside, the same way ^C stops it.
type continuousRunner struct {
	runner TestRunner
	cancel context.CancelFunc
	mu     sync.Mutex
	runs   int
	perRun []func()
	stopAt int
	stdout syncBuffer
	stderr syncBuffer
}

func newContinuousRunner(
	t *testing.T,
	driver *enginetest.FakeDriver,
	stopAt int,
) *continuousRunner {
	t.Helper()
	harness := &continuousRunner{stopAt: stopAt}
	base := t.TempDir()
	moment := time.Unix(1_700_000_000, 0).UTC()
	harness.runner = TestRunner{
		Clock:        &advancingClock{now: moment},
		PollInterval: time.Millisecond,
		NewSession: func(_ context.Context, shard Shard, _ TestOptions) (TestSession, error) {
			harness.mu.Lock()
			harness.runs++
			current := harness.runs
			var hook func()
			if current-1 < len(harness.perRun) {
				hook = harness.perRun[current-1]
			}
			stop := harness.stopAt > 0 && current >= harness.stopAt
			harness.mu.Unlock()
			// Hooks must not block: they run inside the very execution whose
			// completion the loop is waiting on.
			if hook != nil {
				hook()
			}
			if stop && harness.cancel != nil {
				harness.cancel()
			}
			return DeviceSession{
				Driver:          driver,
				OutputDirectory: shard.OutputDirectory,
				BaseDirectory:   base,
				Clock:           &advancingClock{now: moment},
				ExecutionID:     "test-execution",
			}, nil
		},
	}
	return harness
}

// beforeRun schedules non-blocking work for the start of the nth execution
// (1-based), which is where a test edits a watched file to provoke the next one.
func (harness *continuousRunner) beforeRun(n int, hook func()) {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	for len(harness.perRun) < n {
		harness.perRun = append(harness.perRun, nil)
	}
	harness.perRun[n-1] = hook
}

// when runs action once the condition holds, from outside the run loop. It is
// how a test reacts to something only the loop can produce — an error message
// from a run that never reached a device, or the watching announcement itself.
func (harness *continuousRunner) when(condition func() bool, action func()) {
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if condition() {
				action()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
}

func (harness *continuousRunner) stdoutHas(substring string) func() bool {
	return func() bool { return strings.Contains(harness.stdout.String(), substring) }
}

func (harness *continuousRunner) stderrHas(substring string) func() bool {
	return func() bool { return strings.Contains(harness.stderr.String(), substring) }
}

func (harness *continuousRunner) run(t *testing.T, args ...string) int {
	t.Helper()
	ctx, cancelTimeout := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancelTimeout()
	ctx, harness.cancel = context.WithCancel(ctx)
	defer harness.cancel()

	done := make(chan int, 1)
	go func() {
		done <- harness.runner.Run(ctx,
			append([]string{"--continuous", "--test-output-dir", t.TempDir()}, args...),
			&harness.stdout, &harness.stderr)
	}()
	select {
	case code := <-done:
		return code
	case <-time.After(30 * time.Second):
		t.Fatal("continuous mode never returned")
		return -1
	}
}

func (harness *continuousRunner) executions() int {
	harness.mu.Lock()
	defer harness.mu.Unlock()
	return harness.runs
}

// touch rewrites a file so its size and modification time both move. Rewriting
// rather than calling os.Chtimes on purpose: an editor writes content, and a
// watcher that only noticed a bumped timestamp would miss a same-second save.
func touch(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func TestTheDefaultPollIntervalIsUsedWhenNoneIsInjected(t *testing.T) {
	t.Parallel()

	// Every test above injects a millisecond interval, so nothing else exercises
	// the fallback. A zero here would spin the watcher on a tight loop.
	if got := (TestRunner{}).pollInterval(); got != defaultPollInterval {
		t.Fatalf("pollInterval() = %v, want %v", got, defaultPollInterval)
	}
	if got := (TestRunner{PollInterval: time.Second}).pollInterval(); got != time.Second {
		t.Fatalf("pollInterval() = %v, want the injected value", got)
	}
}

func TestContinuousRerunsWhenAWatchedFlowChanges(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	flow := filepath.Join(dir, "watched.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- launchApp\n")

	harness := newContinuousRunner(t, permissiveDriver(), 2)
	harness.beforeRun(1, func() {
		touch(t, flow, "appId: com.example.a\n---\n- launchApp\n- back\n")
	})
	harness.run(t, flow)

	if harness.executions() < 2 {
		t.Fatalf("executions = %d, want the edit to trigger a rerun\nstdout: %s",
			harness.executions(), harness.stdout.String())
	}
}

func TestASingleRunDoesNotWatchAnything(t *testing.T) {
	t.Parallel()

	// The control. A runner that watched unconditionally would hang here, and
	// every other CLI test would hang with it.
	dir := t.TempDir()
	flow := filepath.Join(dir, "once.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- launchApp\n")

	runner := fakeRunner(permissiveDriver(), t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(),
		[]string{"--test-output-dir", t.TempDir(), flow}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "watching") {
		t.Fatalf("a single run announced watching:\n%s", stdout.String())
	}
}

func TestContinuousKeepsWatchingAfterAFailingRun(t *testing.T) {
	t.Parallel()

	// A watcher that stops on a red suite stops the first time it is useful.
	dir := t.TempDir()
	flow := filepath.Join(dir, "failing.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")

	harness := newContinuousRunner(t, emptyScreenDriver(), 2)
	harness.beforeRun(1, func() {
		touch(t, flow, "appId: com.example.a\n---\n- assertVisible: StillNotHere\n")
	})
	harness.run(t, flow)

	if harness.executions() < 2 {
		t.Fatalf("executions = %d, want a failing run to keep watching\nstderr: %s",
			harness.executions(), harness.stderr.String())
	}
}

// surviveABrokenSave breaks the flow, waits for the run that chokes on it to
// say so, repairs it, and reports whether a run happened after the repair.
//
// The stage that rejects the file matters, because each stage returns its own
// watch set and a mode that keeps watching after one can still die after
// another. `marker` is the text the rejecting stage is expected to print.
func surviveABrokenSave(t *testing.T, broken string, repaired string, marker string) {
	t.Helper()

	dir := t.TempDir()
	flow := filepath.Join(dir, "flow.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- launchApp\n")

	// The rejected run never reaches a device, so the repair has to be driven
	// from outside the session factory.
	harness := newContinuousRunner(t, permissiveDriver(), 2)
	harness.when(harness.stderrHas(marker), func() { touch(t, flow, repaired) })
	harness.beforeRun(1, func() { touch(t, flow, broken) })
	harness.run(t, flow)

	if !strings.Contains(harness.stderr.String(), marker) {
		t.Fatalf("the broken save was never reported:\nstderr: %s", harness.stderr.String())
	}
	if harness.executions() < 2 {
		t.Fatalf("executions = %d, want the repaired flow to run\nstderr: %s",
			harness.executions(), harness.stderr.String())
	}
}

func TestContinuousSurvivesASaveDiscoveryCannotRead(t *testing.T) {
	t.Parallel()

	// The reason this mode exists. Saving mid-edit leaves a file broken, and a
	// watcher that exits on it makes the operator restart the watcher every time
	// they save early — which is every time.
	//
	// An unknown command is rejected during discovery, before preflight ever
	// runs, so this pins the discovery-stage return.
	surviveABrokenSave(t,
		"appId: com.example.a\n---\n- thisCommandDoesNotExist\n",
		"appId: com.example.a\n---\n- launchApp\n- back\n",
		"thisCommandDoesNotExist")
}

func TestContinuousSurvivesASavePreflightRejects(t *testing.T) {
	t.Parallel()

	// The other stage, and a different return: a dangling runFlow parses fine
	// and fails in preflight, where the watch set is the closure rather than the
	// walk. This input reaches preflight because parsing accepts the command and
	// dependency resolution rejects the missing flow.
	surviveABrokenSave(t,
		"appId: com.example.a\n---\n- runFlow: nowhere.yaml\n",
		"appId: com.example.a\n---\n- launchApp\n- back\n",
		"nowhere.yaml")
}

func TestContinuousWatchesFlowsReachedThroughRunFlow(t *testing.T) {
	t.Parallel()

	// The watch set is the dependency closure, not the selected roots. Editing
	// the subflow is the ordinary case — it is where the steps live — and a
	// watcher that only knew the root would sit still through it.
	dir := t.TempDir()
	root := filepath.Join(dir, "root.yaml")
	sub := filepath.Join(dir, "sub.yaml")
	touch(t, root, "appId: com.example.a\n---\n- runFlow: sub.yaml\n")
	touch(t, sub, "appId: com.example.a\n---\n- launchApp\n")

	harness := newContinuousRunner(t, permissiveDriver(), 2)
	harness.beforeRun(1, func() {
		touch(t, sub, "appId: com.example.a\n---\n- launchApp\n- back\n")
	})
	harness.run(t, root)

	if harness.executions() < 2 {
		t.Fatalf("executions = %d, want editing the subflow to trigger a rerun\nstdout: %s",
			harness.executions(), harness.stdout.String())
	}
}

func TestContinuousWatchesASubflowOutsideTheSelectedRoots(t *testing.T) {
	t.Parallel()

	// A shared flow in a sibling directory is normal — that is what runFlow with
	// a relative path up a level is for. The pre-run walk cannot see it, because
	// it is not under any root, so only the closure knows it exists. Without the
	// closure's stamps merged in, an edit to it during the run has no snapshot to
	// be checked against and the rerun never happens.
	parent := t.TempDir()
	selected := filepath.Join(parent, "suite")
	shared := filepath.Join(parent, "shared")
	for _, dir := range []string{selected, shared} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	root := filepath.Join(selected, "root.yaml")
	sub := filepath.Join(shared, "login.yaml")
	touch(t, root, "appId: com.example.a\n---\n- runFlow: ../shared/login.yaml\n")
	touch(t, sub, "appId: com.example.a\n---\n- launchApp\n")

	harness := newContinuousRunner(t, permissiveDriver(), 2)
	harness.beforeRun(1, func() {
		touch(t, sub, "appId: com.example.a\n---\n- launchApp\n- back\n")
	})
	harness.run(t, root)

	if harness.executions() < 2 {
		t.Fatalf("executions = %d, want editing the shared subflow to trigger a rerun\nstdout: %s",
			harness.executions(), harness.stdout.String())
	}
}

func TestContinuousExitsWithTheLastRunsVerdict(t *testing.T) {
	t.Parallel()

	// An operator who stops a continuous run wants the verdict of the run they
	// were looking at. Reporting OK because the MODE ended cleanly would tell CI
	// a red suite was green. Cancelled once the run has finished and the watcher
	// has announced itself, so the verdict under test is a completed run's.
	dir := t.TempDir()
	flow := filepath.Join(dir, "failing.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")

	harness := newContinuousRunner(t, emptyScreenDriver(), 0)
	harness.when(harness.stdoutHas("watching"), func() { harness.cancel() })
	if code := harness.run(t, flow); code != ExitFailure {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailure, harness.stderr.String())
	}
	if harness.executions() != 1 {
		t.Fatalf("executions = %d, want exactly the one run under test", harness.executions())
	}
}

func TestContinuousExitsCleanlyAfterAPassingRun(t *testing.T) {
	t.Parallel()

	// The other half of the control above: the exit code follows the run, not
	// the mode, in both directions.
	dir := t.TempDir()
	flow := filepath.Join(dir, "passing.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- launchApp\n")

	harness := newContinuousRunner(t, permissiveDriver(), 0)
	harness.when(harness.stdoutHas("watching"), func() { harness.cancel() })
	if code := harness.run(t, flow); code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, harness.stderr.String())
	}
}

func TestContinuousAnnouncesWhatItIsWatching(t *testing.T) {
	t.Parallel()

	// Without a line saying so, a terminal that has stopped printing is
	// indistinguishable from a hung one.
	dir := t.TempDir()
	flow := filepath.Join(dir, "watched.yaml")
	touch(t, flow, "appId: com.example.a\n---\n- launchApp\n")

	harness := newContinuousRunner(t, permissiveDriver(), 0)
	harness.when(harness.stdoutHas("watching"), func() { harness.cancel() })
	harness.run(t, flow)
	if !strings.Contains(harness.stdout.String(), "watching") {
		t.Fatalf("continuous mode never said it was watching:\n%s", harness.stdout.String())
	}
}
