package cli

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// The host half of the reserved environment.
//
// specs/01-core-engine.md:101 lists FLOWBATON_DEVICE_UDID and the shard variables
// as injected defaults, and adds that shell variables prefixed FLOWBATON_ are
// auto-injected. All three are the host's to supply: the engine cannot know
// which simulator it holds, which shard it is, or what was in the operator's
// shell.

func TestAShardTellsItsFlowsWhichShardAndDeviceItIs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// ${...} interpolation is the only way a flow can observe this, so the flow
	// itself is the assertion: a label built from the reserved variables.
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_SHARD_ID}-${FLOWBATON_SHARD_INDEX}-${FLOWBATON_DEVICE_UDID}\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	// Collected per shard because shards run concurrently and a single variable
	// would retain only whichever shard finished last.
	drivers := newDriverRecorder()
	runner := TestRunner{NewSession: shardSessionFactory(dir, drivers.record)}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-one,udid-two",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}

	// a.yaml is shard 1 (index 0), which PlanShards puts on the first device.
	if got := typedText(drivers.at(0)); got != "1-0-udid-one" {
		t.Fatalf("interpolated text = %q, want the first shard's identity", got)
	}
}

// driverRecorder collects the driver built for each shard. Shards run
// concurrently, so the map needs a lock and the reads need a shard index.
type driverRecorder struct {
	mutex   sync.Mutex
	drivers map[int]*enginetest.FakeDriver
}

func newDriverRecorder() *driverRecorder {
	return &driverRecorder{drivers: map[int]*enginetest.FakeDriver{}}
}

func (recorder *driverRecorder) record(shard Shard, driver *enginetest.FakeDriver) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	recorder.drivers[shard.Index] = driver
}

func (recorder *driverRecorder) at(index int) *enginetest.FakeDriver {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.drivers[index]
}

func TestASecondShardSeesItsOwnIdentity(t *testing.T) {
	t.Parallel()

	// The control for the test above: hardcoding shard 1 and the first device
	// would satisfy it and make every shard claim to be the first.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "b.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_SHARD_ID}-${FLOWBATON_SHARD_INDEX}-${FLOWBATON_DEVICE_UDID}\n")

	drivers := newDriverRecorder()
	runner := TestRunner{NewSession: shardSessionFactory(dir, drivers.record)}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-one,udid-two",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(drivers.at(1)); got != "2-1-udid-two" {
		t.Fatalf("second shard identity = %q, want 2-1-udid-two", got)
	}
}

func TestAnUnshardedRunStillNamesItsDevice(t *testing.T) {
	t.Parallel()

	// One shard is still a shard. A flow that reads FLOWBATON_DEVICE_UDID on an
	// ordinary run must not get nothing.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_SHARD_ID}/${FLOWBATON_DEVICE_UDID}\n")

	var driver *enginetest.FakeDriver
	runner := TestRunner{NewSession: shardSessionFactory(dir,
		func(_ Shard, built *enginetest.FakeDriver) { driver = built })}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(driver); got != "1/solo-udid" {
		t.Fatalf("unsharded identity = %q, want 1/solo-udid", got)
	}
}

func TestShellFlowBatonVariablesReachTheFlow(t *testing.T) {
	t.Parallel()

	// specs/01-core-engine.md:101: shell vars prefixed FLOWBATON_ are
	// auto-injected. This is how CI passes a build number to a suite without
	// spelling out -e for each one.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_BUILD}\n")

	driver := permissiveDriver()
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string {
		return []string{"HOME=/home/operator", "FLOWBATON_BUILD=4711", "PATH=/usr/bin"}
	}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(driver); got != "4711" {
		t.Fatalf("interpolated text = %q, want the shell value", got)
	}
}

func TestAnUnprefixedShellVariableIsNotInjected(t *testing.T) {
	t.Parallel()

	// The control for the test above: injecting the whole environment would
	// satisfy it and hand every flow the operator's entire shell, credentials
	// included.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${OPERATOR_ONLY_VALUE}\n")

	driver := permissiveDriver()
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string { return []string{"OPERATOR_ONLY_VALUE=must-not-appear"} }
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	// An unset name interpolates to the JavaScript "undefined" rather than
	// failing the flow, so the assertion is on the value: whatever the flow saw,
	// it must not be the shell's.
	got := typedText(driver)
	if got == "must-not-appear" {
		t.Fatal("an unprefixed shell variable reached the flow")
	}
	if got != "undefined" {
		t.Fatalf("interpolated text = %q, want undefined", got)
	}
}

func TestAnExplicitEnvBeatsTheShell(t *testing.T) {
	t.Parallel()

	// -e is what the operator typed for this run. A shell variable left over
	// from another one must not win.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_BUILD}\n")

	driver := permissiveDriver()
	runner := fakeRunner(driver, dir)
	runner.Environ = func() []string { return []string{"FLOWBATON_BUILD=stale"} }
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"-e", "FLOWBATON_BUILD=explicit", "--device", "solo-udid",
		"--test-output-dir", t.TempDir(), filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(driver); got != "explicit" {
		t.Fatalf("interpolated text = %q, want the -e value", got)
	}
}

func TestAShellShardVariableCannotForgeAShardIdentity(t *testing.T) {
	t.Parallel()

	// FLOWBATON_SHARD_ID carries the prefix, so it would ride in on the shell
	// pass-through. The engine strips it from external input, and the host's
	// own value is applied after — so the forgery has to lose.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- inputText: ${FLOWBATON_SHARD_ID}\n")

	var driver *enginetest.FakeDriver
	runner := TestRunner{
		Environ: func() []string { return []string{"FLOWBATON_SHARD_ID=999"} },
		NewSession: shardSessionFactory(dir,
			func(_ Shard, built *enginetest.FakeDriver) { driver = built }),
	}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "solo-udid", "--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if got := typedText(driver); got != "1" {
		t.Fatalf("shard id = %q, want the host's 1 rather than the shell's 999", got)
	}
}

// shardSessionFactory builds a real DeviceSession per shard — so the reserved
// variables come from the production path — with a fake driver in place of the
// simulator.
func shardSessionFactory(
	baseDirectory string,
	record func(Shard, *enginetest.FakeDriver),
) SessionFactory {
	return func(shard Shard, _ TestOptions) (TestSession, error) {
		session, err := NewDeviceSession(
			TestOptions{Platform: "ios", Roots: []string{baseDirectory}}, shard)
		if err != nil {
			return nil, err
		}
		driver := permissiveDriver()
		record(shard, driver)
		session.Driver = driver
		session.BaseDirectory = baseDirectory
		session.Clock = &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()}
		session.ExecutionID = "test-execution"
		return session, nil
	}
}

// typedText returns the text an inputText command sent to the driver, which is
// the only way a flow can report what ${...} resolved to.
func typedText(driver *enginetest.FakeDriver) string {
	for _, action := range driver.Actions() {
		if action.Method != enginetest.MethodInputText {
			continue
		}
		if request, ok := action.Request.(device.InputTextRequest); ok {
			return request.Text
		}
	}
	return ""
}
