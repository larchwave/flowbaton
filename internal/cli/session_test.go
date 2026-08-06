package cli

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// These tests exercise the command-line path through discovery, preflight,
// dependency assembly, execution, reporting, and exit-code selection. A
// FakeDriver keeps the path deterministic without a simulator.

func TestASingleFlowRunsEndToEndAndReportsPass(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "smoke.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- launchApp\n- tapOn: OK\n")

	stdout, stderr, code := runSession(t, permissiveDriver(), path)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, ExitOK, stdout, stderr)
	}
	if !strings.Contains(stdout, "PASS") {
		t.Fatalf("stdout = %q, want a PASS line", stdout)
	}
	if !strings.Contains(stdout, "smoke.yaml") {
		t.Fatalf("stdout = %q, want the flow named", stdout)
	}
}

func TestAFailingFlowExitsNonZero(t *testing.T) {
	t.Parallel()

	// A failed command must propagate through reporting and exit-code selection.
	dir := t.TempDir()
	path := filepath.Join(dir, "failing.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- launchApp\n- assertVisible: NothingIsEverHere\n")

	stdout, _, code := runSession(t, emptyScreenDriver(), path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d\nstdout: %s", code, ExitFailure, stdout)
	}
	if !strings.Contains(stdout, "FAIL") {
		t.Fatalf("stdout = %q, want a FAIL line", stdout)
	}
}

func TestAnUnopenableDeviceIsReportedAsASetupFailureNotAFlowFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "smoke.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- launchApp\n")

	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		Open: []enginetest.Result[struct{}]{{Err: errors.New("runner did not answer")}},
	})

	_, stderr, code := runSession(t, driver, path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "runner did not answer") {
		t.Fatalf("stderr = %q, want the driver's own reason", stderr)
	}
}

func TestTheDeviceIsClosedEvenWhenAFlowFails(t *testing.T) {
	t.Parallel()

	// A leaked device costs the next run, and a run that fails is exactly when
	// cleanup is easiest to skip.
	dir := t.TempDir()
	path := filepath.Join(dir, "failing.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")

	driver := emptyScreenDriver()
	if _, _, code := runSession(t, driver, path); code != ExitFailure {
		t.Fatalf("exit = %d, want a failure to set up the check", code)
	}
	if !calledMethod(driver, enginetest.MethodClose) {
		t.Fatalf("driver actions = %v, want Close after a failed flow", driver.Actions())
	}
}

func TestEnvironmentValuesReachTheFlow(t *testing.T) {
	t.Parallel()

	// -e is only useful if the value arrives. A flow that asserts on it fails
	// when it does not.
	dir := t.TempDir()
	path := filepath.Join(dir, "env.yaml")
	writeFile(t, path,
		"appId: com.example.a\n---\n- assertTrue: ${FROM_CLI == 'arrived'}\n")

	stdout, stderr, code := runSessionWithArgs(
		t, permissiveDriver(), []string{"-e", "FROM_CLI=arrived", path})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, ExitOK, stdout, stderr)
	}
}

func TestAWrongEnvironmentValueStillFails(t *testing.T) {
	t.Parallel()

	// The control for the test above: if the assertion passed regardless, the
	// environment test would prove nothing.
	dir := t.TempDir()
	path := filepath.Join(dir, "env.yaml")
	writeFile(t, path,
		"appId: com.example.a\n---\n- assertTrue: ${FROM_CLI == 'arrived'}\n")

	_, _, code := runSessionWithArgs(
		t, permissiveDriver(), []string{"-e", "FROM_CLI=something-else", path})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d — the assertion should have failed", code, ExitFailure)
	}
}

// An authored screenshot name resolves against the process working directory,
// not the run's artifact directory.
//
// Not parallel: the behavior under test is cwd-dependent, so the test owns the
// cwd.
func TestAnAuthoredScreenshotLandsInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	output := t.TempDir()
	path := filepath.Join(dir, "shot.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- takeScreenshot: evidence\n")

	working := t.TempDir()
	t.Chdir(working)

	runner := fakeRunner(permissiveDriver(), dir)
	var stdout, stderr bytes.Buffer
	if code := runner.Run(
		context.Background(),
		[]string{"--test-output-dir", output, path},
		&stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(working, "evidence.png")); err != nil {
		listing, _ := filepath.Glob(filepath.Join(working, "*"))
		t.Fatalf("evidence.png is not in the working directory: %v (has %v)", err, listing)
	}
	// The authored screenshot must not also appear in the run artifact directory.
	if matches, _ := filepath.Glob(filepath.Join(output, "*.png")); len(matches) != 0 {
		t.Fatalf("the authored screenshot also landed in the run directory: %v", matches)
	}
}

func TestDefaultOutputDirectoryFollowsTheDocumentedPrecedence(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 7, 29, 13, 5, 9, 0, time.UTC)
	home := filepath.Join("/home", "operator")

	for _, test := range []struct {
		name    string
		options TestOptions
		want    string
	}{
		{
			name:    "test-output-dir wins",
			options: TestOptions{TestOutputDir: "/explicit", DebugOutput: "/ignored"},
			want:    "/explicit",
		},
		{
			name:    "debug-output is next",
			options: TestOptions{DebugOutput: "/debug"},
			want:    "/debug",
		},
		{
			name:    "otherwise a timestamped directory under home",
			options: TestOptions{},
			want:    filepath.Join(home, ".flowbaton", "tests", "2026-07-29_130509"),
		},
		{
			name:    "flatten drops the timestamp for a fixed CI path",
			options: TestOptions{FlattenDebugOutput: true},
			want:    filepath.Join(home, ".flowbaton", "tests"),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := DefaultOutputDirectory(test.options, home, moment); got != test.want {
				t.Fatalf("DefaultOutputDirectory() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSessionRefusesToRunWithoutADriver(t *testing.T) {
	t.Parallel()

	_, err := DeviceSession{}.Execute(context.Background(), nil, TestOptions{})
	if err == nil {
		t.Fatal("Execute() ran without a driver")
	}
}

func runSession(t *testing.T, driver *enginetest.FakeDriver, path string) (string, string, int) {
	t.Helper()
	return runSessionWithArgs(t, driver, []string{path})
}

func runSessionWithArgs(
	t *testing.T,
	driver *enginetest.FakeDriver,
	args []string,
) (string, string, int) {
	t.Helper()
	runner := fakeRunner(driver, t.TempDir())
	var stdout, stderr bytes.Buffer
	code := runner.Run(
		context.Background(),
		append([]string{"--test-output-dir", t.TempDir()}, args...),
		&stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

// fakeRunner builds a runner whose every shard runs on the given driver, with
// a fixed clock so the report and the artifact paths are reproducible.
func fakeRunner(driver *enginetest.FakeDriver, baseDirectory string) TestRunner {
	moment := time.Unix(1_700_000_000, 0).UTC()
	return TestRunner{
		Clock: &advancingClock{now: moment},
		NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
			return DeviceSession{
				Driver:          driver,
				OutputDirectory: shard.OutputDirectory,
				BaseDirectory:   baseDirectory,
				Clock:           &advancingClock{now: moment},
				ExecutionID:     "test-execution",
			}, nil
		},
	}
}

func calledMethod(driver *enginetest.FakeDriver, method enginetest.Method) bool {
	for _, action := range driver.Actions() {
		if action.Method == method {
			return true
		}
	}
	return false
}

// callBudget is generous on purpose: these tests assert on the run's OUTCOME,
// not on how many times a command touched the device.
const callBudget = 512

// permissiveDriver answers everything, with one element on screen so a tapOn
// has something to find.
func permissiveDriver() *enginetest.FakeDriver {
	return fakeDriverShowing(device.TreeNode{
		Attributes: map[string]string{"text": "OK", "bounds": "[0,0][100,50]"},
	})
}

// emptyScreenDriver answers everything but shows nothing, so an assertVisible
// has to fail.
func emptyScreenDriver() *enginetest.FakeDriver {
	return fakeDriverShowing(device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][300,600]"},
	})
}

func fakeDriverShowing(root device.TreeNode) *enginetest.FakeDriver {
	return fakeDriverShowingWithScreenshot(root, fakeScreenshotPNG)
}

// fakeDriverShowingWithScreenshot exists because an artifact's NAME depends on
// what the screenshot bytes actually are, and a driver that only ever answers
// PNG cannot show that.
func fakeDriverShowingWithScreenshot(root device.TreeNode, screenshot []byte) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	void := make([]enginetest.Result[struct{}], callBudget)
	trees := make([]enginetest.Result[device.TreeNode], callBudget)
	settles := make([]enginetest.Result[*device.ViewHierarchy], callBudget)
	infos := make([]enginetest.Result[device.DeviceInfo], callBudget)
	settled := &device.ViewHierarchy{Root: root}
	for index := range trees {
		trees[index] = enginetest.Result[device.TreeNode]{Value: root}
		settles[index] = enginetest.Result[*device.ViewHierarchy]{Value: settled}
		infos[index] = enginetest.Result[device.DeviceInfo]{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600,
			WidthPixels: 300, HeightPixels: 600,
		}}
	}
	driver.Enqueue(enginetest.DriverScript{
		Open: void, Close: void, DeviceInfo: infos,
		ContentDescriptor: trees, WaitForAppToSettle: settles,
		LaunchApp: void, StopApp: void, KillApp: void, ClearAppState: void, ClearKeychain: void,
		Tap: void, LongPress: void, PressKey: void, ScrollVertical: void, Swipe: void,
		BackPress: void, InputText: void, OpenLink: void, HideKeyboard: void, EraseText: void,
		SetLocation: void, SetOrientation: void, SetPermissions: void, AddMedia: void,
		IsKeyboardVisible:       repeatValue(true),
		WaitUntilScreenIsStatic: repeatValue(true),
		TakeScreenshot:          repeatValue(screenshot),
	})
	return driver
}

// fakeScreenshotPNG is a real image rather than the bytes "PNG". tapOn's
// retryTapIfNoChange DECODES the snapshot screenshot, so a placeholder failed
// the command on a check no test was asking about.
var fakeScreenshotPNG = func() []byte {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}()

func repeatValue[T any](value T) []enginetest.Result[T] {
	results := make([]enginetest.Result[T], callBudget)
	for index := range results {
		results[index] = enginetest.Result[T]{Value: value}
	}
	return results
}

// advancingClock never blocks. A real clock would make every wait in a flow
// take its documented time, which turns a 17-second lookup budget into a
// 17-second test.
type advancingClock struct{ now time.Time }

func (clock *advancingClock) Now() time.Time { return clock.now }

func (clock *advancingClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		clock.now = clock.now.Add(delay)
	}
	return nil
}
