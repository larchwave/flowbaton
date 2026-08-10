package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// specs/04-wire-protocols.md:90: "Per-shard ports selected by CLI (ephemeral).
// No dynamic port scanning — explicit port agreement host↔device."
// specs/03-cli-tooling.md:28 adds that the port is OS-assigned via an ephemeral
// socket unless the user set one, and specs/02-device-drivers.md:68 says the iOS
// runner takes its port from the PORT environment variable, defaulting to 22087.
//
// Each shard receives a distinct port so suites cannot share one runner.

func TestTheFirstShardKeepsTheContractPort(t *testing.T) {
	t.Parallel()

	// The contract pins 22087, and an unsharded run is the overwhelming case.
	// Moving it would break every runner already listening there.
	shards := []Shard{{Index: 0}}
	if err := assignDriverPorts(shards, 22087, nil, failingAllocator(t)); err != nil {
		t.Fatalf("assignDriverPorts() error = %v", err)
	}
	if shards[0].DriverPort != 22087 {
		t.Fatalf("port = %d, want the contract's 22087", shards[0].DriverPort)
	}
}

func TestLaterShardsGetTheirOwnPorts(t *testing.T) {
	t.Parallel()

	shards := []Shard{{Index: 0}, {Index: 1}, {Index: 2}}
	if err := assignDriverPorts(shards, 22087, nil, fixedAllocator(41001, 41002)); err != nil {
		t.Fatalf("assignDriverPorts() error = %v", err)
	}
	want := []int{22087, 41001, 41002}
	for index, shard := range shards {
		if shard.DriverPort != want[index] {
			t.Fatalf("shard %d port = %d, want %d", index, shard.DriverPort, want[index])
		}
	}
}

func TestDuplicatePortsAreRefused(t *testing.T) {
	t.Parallel()

	// An allocator that hands out the same port twice would put two shards on
	// one runner. Both suites would run on one device and the report would
	// still claim two.
	shards := []Shard{{Index: 0}, {Index: 1}}
	err := assignDriverPorts(shards, 22087, nil, fixedAllocator(22087))
	if err == nil {
		t.Fatal("assignDriverPorts() accepted two shards on one port")
	}
	if !strings.Contains(err.Error(), "22087") {
		t.Fatalf("error = %q, want it to name the clashing port", err)
	}
}

func TestAnAllocationFailureFailsTheRun(t *testing.T) {
	t.Parallel()

	// Falling back to the default port would be the duplicate case with no
	// error, which is the outcome this whole batch exists to prevent.
	shards := []Shard{{Index: 0}, {Index: 1}}
	if err := assignDriverPorts(shards, 22087, nil, failingAllocator(t)); err == nil {
		t.Fatal("assignDriverPorts() carried on after the allocator failed")
	}
}

func TestTheEphemeralAllocatorReturnsUsablePorts(t *testing.T) {
	t.Parallel()

	// The positive control for the fake allocators above: the real one has to
	// produce a port in range, and two calls must not collide.
	first, err := ephemeralPort()
	if err != nil {
		t.Fatalf("ephemeralPort() error = %v", err)
	}
	if first <= 1024 || first > 65535 {
		t.Fatalf("port = %d, want an ephemeral one", first)
	}
	second, err := ephemeralPort()
	if err != nil {
		t.Fatalf("ephemeralPort() error = %v", err)
	}
	if first == second {
		t.Fatalf("both calls returned %d", first)
	}
}

func TestTheBasePortComesFromTheEnvironmentWhenSet(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md:68 — the runner reads PORT, so the host has to
	// read the same variable or the two disagree about where to meet.
	got := basePort(TestOptions{Platform: "ios"}, []string{"PORT=39001"})
	if got != 39001 {
		t.Fatalf("base port = %d, want the environment's 39001", got)
	}
}

func TestTheBasePortFallsBackPerPlatform(t *testing.T) {
	t.Parallel()

	// The control for the test above: reading the environment unconditionally
	// would hand every run port 0.
	for _, test := range []struct {
		platform string
		environ  []string
		want     int
	}{
		{platform: "ios", want: 22087},
		{platform: "android", want: 7001},
		// A web run's port is a DevTools port, not a runner port, and 9222 is
		// the number every browser tool already means by it.
		{platform: "web", want: 9222},
		{platform: "ios", environ: []string{"PORT="}, want: 22087},
		{platform: "ios", environ: []string{"PORT=not-a-number"}, want: 22087},
		{platform: "ios", environ: []string{"PORT=0"}, want: 22087},
		{platform: "ios", environ: []string{"PORT=70000"}, want: 22087},
	} {
		if got := basePort(TestOptions{Platform: test.platform}, test.environ); got != test.want {
			t.Fatalf("basePort(%s, %v) = %d, want %d",
				test.platform, test.environ, got, test.want)
		}
	}
}

func TestAShardTalksToItsOwnPort(t *testing.T) {
	t.Parallel()

	// The plumbing that matters: the driver has to be built against the port
	// the shard was assigned, not the package default.
	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}},
		Shard{Device: "UDID-1", DriverPort: 41001})
	if err != nil {
		t.Fatal(err)
	}
	if got := session.Driver.Name(); !strings.Contains(got, "41001") {
		t.Fatalf("driver name = %q, want it to carry the shard's port", got)
	}
}

func TestTheRunnerGivesEveryShardItsOwnPort(t *testing.T) {
	t.Parallel()

	// The runner assigns a distinct port to every shard instead of allowing each
	// shard to fall back to the same default.
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeFile(t, filepath.Join(dir, name), "appId: com.example.a\n---\n- launchApp\n")
	}

	var mutex sync.Mutex
	ports := map[int]int{}
	runner := TestRunner{
		Environ:      func() []string { return nil },
		AllocatePort: fixedAllocator(41001),
		NewSession: func(_ context.Context, shard Shard, _ TestOptions) (TestSession, error) {
			mutex.Lock()
			ports[shard.Index] = shard.DriverPort
			mutex.Unlock()
			return DeviceSession{
				Driver:          permissiveDriver(),
				OutputDirectory: shard.OutputDirectory,
				BaseDirectory:   dir,
				Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
				ExecutionID:     "test-execution",
			}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-one,udid-two",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	if ports[0] != 22087 {
		t.Fatalf("shard 1 port = %d, want the contract's 22087", ports[0])
	}
	if ports[1] != 41001 {
		t.Fatalf("shard 2 port = %d, want the allocated 41001", ports[1])
	}
}

func TestAnAllocationFailureStopsTheRunBeforeAnyDevice(t *testing.T) {
	t.Parallel()

	// The runner half of the refusal: an exhausted allocator must not reach a
	// session at all.
	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeFile(t, filepath.Join(dir, name), "appId: com.example.a\n---\n- launchApp\n")
	}

	var built atomic.Bool
	runner := TestRunner{
		Environ:      func() []string { return nil },
		AllocatePort: func() (int, error) { return 0, errAllocatorExhausted },
		NewSession: func(_ context.Context, _ Shard, _ TestOptions) (TestSession, error) {
			built.Store(true)
			return DeviceSession{Driver: permissiveDriver(), BaseDirectory: dir}, nil
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-one,udid-two",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitFailure {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailure, stderr.String())
	}
	if built.Load() {
		t.Fatal("a device session was built for a run with no ports")
	}
}

func fixedAllocator(ports ...int) func() (int, error) {
	index := 0
	return func() (int, error) {
		if index >= len(ports) {
			return 0, errAllocatorExhausted
		}
		port := ports[index]
		index++
		return port, nil
	}
}

func failingAllocator(t *testing.T) func() (int, error) {
	t.Helper()
	return func() (int, error) { return 0, errAllocatorExhausted }
}

// iOS diagnostics use the configured runner port because the runner starts
// before the host connects. Android diagnostics keep an ephemeral host port
// for the forward created by this process.
func TestADiagnosticPortMeetsTheRunnerWhereItIs(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		platform string
		environ  []string
		want     int
		wantAny  bool
	}{
		{name: "ios honors PORT", platform: "ios", environ: []string{"PORT=41401"}, want: 41401},
		{name: "ios without PORT uses the contract default", platform: "ios", want: defaultIOSDriverPort},
		{name: "android still takes any free port", platform: "android", environ: []string{"PORT=41401"}, wantAny: true},
	} {
		got, err := diagnosticPort(test.platform, test.environ)
		if err != nil {
			t.Fatalf("%s: diagnosticPort() error = %v", test.name, err)
		}
		if test.wantAny {
			if got == 0 {
				t.Fatalf("%s: port = 0, want one the OS assigned", test.name)
			}
			continue
		}
		if got != test.want {
			t.Fatalf("%s: port = %d, want %d", test.name, got, test.want)
		}
	}
}

// Operator-started iOS runners need explicit ports for every shard. Shard 1
// uses the base port, while later shards otherwise receive OS-assigned ports
// that no prestarted runner knows. Android starts its own agent and forwards to
// any assigned port, so the operator port list applies only to iOS and lets N
// prestarted runners say where they are.
func TestAnOperatorCanNameThePortOfEveryShardsRunner(t *testing.T) {
	t.Parallel()

	shards := []Shard{{}, {}, {}}
	if err := assignDriverPorts(shards, 22087, operatorPorts([]string{"41401", "41402", "41403"}),
		func() (int, error) { return 0, errors.New("no shard should have needed an allocation") }); err != nil {
		t.Fatalf("assignDriverPorts() error = %v", err)
	}
	for index, want := range []int{41401, 41402, 41403} {
		if shards[index].DriverPort != want {
			t.Fatalf("shard %d port = %d, want %d", index+1, shards[index].DriverPort, want)
		}
	}
}

// A list that runs out is refused rather than quietly falling back: a shard on
// a port the operator did not start a runner on fails later, further from the
// mistake, and with a message about a connection instead of a command line.
func TestAShortPortListIsRefusedRatherThanTopppedUp(t *testing.T) {
	t.Parallel()

	shards := []Shard{{}, {}, {}}
	err := assignDriverPorts(shards, 22087, operatorPorts([]string{"41401", "41402"}),
		func() (int, error) { return 0, errors.New("no shard should have needed an allocation") })
	if err == nil {
		t.Fatal("a port list shorter than the shard count was accepted")
	}
	if !strings.Contains(err.Error(), "3") || !strings.Contains(err.Error(), "2") {
		t.Fatalf("error = %q, want it to name both counts", err)
	}
}

// With no list the behavior is exactly what it was.
func TestWithoutAPortListNothingChanges(t *testing.T) {
	t.Parallel()

	shards := []Shard{{}, {}}
	next := 30000
	if err := assignDriverPorts(shards, 22087, nil, func() (int, error) {
		next++
		return next, nil
	}); err != nil {
		t.Fatalf("assignDriverPorts() error = %v", err)
	}
	if shards[0].DriverPort != 22087 || shards[1].DriverPort != 30001 {
		t.Fatalf("ports = %d, %d, want 22087 and an allocated one",
			shards[0].DriverPort, shards[1].DriverPort)
	}
}
