package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/workspace"
)

// --shard-split and --shard-all distribute planned flows across device shards.

func plannedFlows(paths ...string) workspace.Plan {
	flows := make([]workspace.Flow, len(paths))
	for index, path := range paths {
		flows[index] = workspace.Flow{Path: path, Name: filepath.Base(path)}
	}
	return workspace.Plan{Flows: flows}
}

func TestShardSplitPartitionsFlowsAcrossDevices(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:28 gives the partition as index % shards.
	plan := plannedFlows("a.yaml", "b.yaml", "c.yaml", "d.yaml", "e.yaml")
	options := TestOptions{ShardSplit: 2, Devices: []string{"udid-1", "udid-2"}}

	shards, err := PlanShards(options, plan)
	if err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("shards = %d, want 2", len(shards))
	}
	if want := []string{"a.yaml", "c.yaml", "e.yaml"}; !reflect.DeepEqual(shards[0].Roots, want) {
		t.Fatalf("shard 0 roots = %v, want %v", shards[0].Roots, want)
	}
	if want := []string{"b.yaml", "d.yaml"}; !reflect.DeepEqual(shards[1].Roots, want) {
		t.Fatalf("shard 1 roots = %v, want %v", shards[1].Roots, want)
	}
	if shards[0].Device != "udid-1" || shards[1].Device != "udid-2" {
		t.Fatalf("devices = %q and %q, want one each in order",
			shards[0].Device, shards[1].Device)
	}
}

func TestShardAllReplicatesEveryFlowToEveryDevice(t *testing.T) {
	t.Parallel()

	// The control for the test above: a planner that always partitioned would
	// satisfy that one and get this one wrong. --shard-all is the opposite
	// operation — the same suite on every device.
	plan := plannedFlows("a.yaml", "b.yaml", "c.yaml")
	options := TestOptions{ShardAll: 3, Devices: []string{"udid-1", "udid-2", "udid-3"}}

	shards, err := PlanShards(options, plan)
	if err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("shards = %d, want 3", len(shards))
	}
	for index, shard := range shards {
		want := []string{"a.yaml", "b.yaml", "c.yaml"}
		if !reflect.DeepEqual(shard.Roots, want) {
			t.Fatalf("shard %d roots = %v, want every flow %v", index, shard.Roots, want)
		}
	}
}

func TestWithoutShardingThereIsOneShardHoldingEveryFlow(t *testing.T) {
	t.Parallel()

	plan := plannedFlows("a.yaml", "b.yaml")
	shards, err := PlanShards(TestOptions{Devices: []string{"udid-1"}}, plan)
	if err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("shards = %d, want 1 when neither shard flag was given", len(shards))
	}
	if want := []string{"a.yaml", "b.yaml"}; !reflect.DeepEqual(shards[0].Roots, want) {
		t.Fatalf("roots = %v, want %v", shards[0].Roots, want)
	}
}

func TestSeveralDevicesWithoutAShardFlagAreRefused(t *testing.T) {
	t.Parallel()

	// Taking the first would run one device's worth of a suite the operator
	// handed several devices to, and report it as the whole thing.
	_, err := PlanShards(
		TestOptions{Devices: []string{"udid-1", "udid-2"}}, plannedFlows("a.yaml"))
	if err == nil {
		t.Fatal("two devices were accepted with no sharding asked for")
	}
	if !strings.Contains(err.Error(), "--shard-split") {
		t.Fatalf("error = %q, want it to name the flag that would use them", err)
	}
}

func TestShardingRefusesFewerDevicesThanShards(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:28 requires enough connected devices. Running
	// four shards on two devices would either serialize them — which is the
	// thing sharding exists to avoid — or run two of them on a device already
	// in use, which corrupts both.
	plan := plannedFlows("a.yaml", "b.yaml", "c.yaml", "d.yaml")
	options := TestOptions{ShardSplit: 4, Devices: []string{"udid-1", "udid-2"}}

	_, err := PlanShards(options, plan)
	if err == nil {
		t.Fatal("PlanShards() accepted four shards on two devices")
	}
	if !strings.Contains(err.Error(), "device") {
		t.Fatalf("error = %q, want it to name the device shortage", err)
	}
}

func TestShardingRefusesAConfiguredFlowOrder(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:28: cannot shard with a sequential sequence. The
	// workspace asked for these flows in this order, and shards run at the same
	// time on different devices, so the order cannot survive. Silently dropping
	// it would violate the configured execution order.
	plan := plannedFlows("first.yaml", "second.yaml")
	plan.Sequence = plan.Flows
	options := TestOptions{ShardSplit: 2, Devices: []string{"udid-1", "udid-2"}}

	_, err := PlanShards(options, plan)
	if err == nil {
		t.Fatal("PlanShards() sharded a suite with a configured order")
	}
	if !strings.Contains(err.Error(), "order") {
		t.Fatalf("error = %q, want it to name the ordering as the reason", err)
	}
}

func TestAConfiguredFlowOrderIsFineWithoutSharding(t *testing.T) {
	t.Parallel()

	// The control for the test above: refusing every ordered suite would pass
	// it and break the ordinary case.
	plan := plannedFlows("first.yaml", "second.yaml")
	plan.Sequence = plan.Flows
	if _, err := PlanShards(TestOptions{Devices: []string{"udid-1"}}, plan); err != nil {
		t.Fatalf("PlanShards() refused an ordered suite that was not sharded: %v", err)
	}
}

func TestShardSplitRefusesMoreShardsThanFlows(t *testing.T) {
	t.Parallel()

	// A shard with no flows holds a device for the whole run and reports
	// nothing. The operator asked for a partition that does not exist.
	plan := plannedFlows("only.yaml")
	options := TestOptions{ShardSplit: 3, Devices: []string{"a", "b", "c"}}

	_, err := PlanShards(options, plan)
	if err == nil {
		t.Fatal("PlanShards() produced empty shards")
	}
	if !strings.Contains(err.Error(), "flow") {
		t.Fatalf("error = %q, want it to name the flow shortage", err)
	}
}

func TestEachShardRunsItsOwnFlowsOnItsOwnDevice(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeFile(t, filepath.Join(dir, name), "appId: com.example.a\n---\n- launchApp\n")
	}

	// Guarded because shards run concurrently.
	var mutex sync.Mutex
	drivers := map[string]*enginetest.FakeDriver{}
	runner := TestRunner{NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
		driver := permissiveDriver()
		mutex.Lock()
		drivers[shard.Device] = driver
		mutex.Unlock()
		return DeviceSession{
			Driver:          driver,
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   dir,
			Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
			ExecutionID:     "test-execution",
		}, nil
	}}

	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-1,udid-2",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if len(drivers) != 2 {
		t.Fatalf("sessions built = %d (%v), want one per device", len(drivers), drivers)
	}
	// Assert result lines, because selected-flow announcements are not execution
	// results.
	for _, name := range []string{"a.yaml", "b.yaml"} {
		if !hasResultLine(stdout.String(), "PASS", name) {
			t.Fatalf("stdout = %q, missing a PASS line for %s", stdout.String(), name)
		}
	}
}

// hasResultLine looks for a reported result rather than a mention. The paths
// are matched by suffix because a temporary directory reaches the runner as
// /var/... and comes back resolved as /private/var/....
func hasResultLine(output, status, name string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, status+" ") && strings.Contains(line, "/"+name+" (") {
			return true
		}
	}
	return false
}

func TestPreflightRefusesBeforeAnyDeviceIsAcquired(t *testing.T) {
	t.Parallel()

	// Each shard prepares its own roots before building its own session, so a
	// SINGLE-shard run refuses early either way. The plan-wide preflight is
	// what stops the OTHER shards: without it, the shard holding the good flow
	// takes a simulator and starts running while the shard holding the broken
	// one is still discovering the flow does not exist.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "b.yaml"),
		"appId: com.example.a\n---\n- runFlow: missing.yaml\n")

	var built atomic.Bool
	runner := TestRunner{NewSession: func(Shard, TestOptions) (TestSession, error) {
		built.Store(true)
		return DeviceSession{Driver: permissiveDriver(), BaseDirectory: dir}, nil
	}}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-1,udid-2",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitFailure {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitFailure, stderr.String())
	}
	if built.Load() {
		t.Fatal("a device was acquired for a run preflight had already doomed")
	}
}

func TestAFailureInOneShardFailsTheWholeRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "b.yaml"),
		"appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")

	runner := TestRunner{NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
		// Only the shard holding the failing flow gets a driver that fails it,
		// so a runner that reported the first shard's exit code would pass.
		driver := permissiveDriver()
		if strings.Contains(strings.Join(shard.Roots, " "), "b.yaml") {
			driver = emptyScreenDriver()
		}
		return DeviceSession{
			Driver:          driver,
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   dir,
			Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
			ExecutionID:     "test-execution",
		}, nil
	}}

	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-1,udid-2",
		"--test-output-dir", t.TempDir(),
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d — one shard failed\nstdout: %s", code, ExitFailure, stdout.String())
	}
}

func TestShardArtifactsAreKeptApartPerShard(t *testing.T) {
	t.Parallel()

	// Two devices writing "evidence.png" into one directory produce one file
	// and a renamed sibling, and nothing says which device made which.
	dir := t.TempDir()
	output := t.TempDir()
	// A FAILING flow on purpose: the failure screenshot is a capture the RUN
	// owns, and run captures are what the shard directory is for. An authored
	// `takeScreenshot: evidence` lands in the author's shared working directory,
	// not in a shard-specific run directory.
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeFile(t, filepath.Join(dir, name),
			"appId: com.example.a\n---\n- assertVisible: Nope\n")
	}

	runner := TestRunner{NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
		return DeviceSession{
			Driver:          emptyScreenDriver(),
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   dir,
			Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
			ExecutionID:     "test-execution",
		}, nil
	}}

	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-1,udid-2", "--test-output-dir", output,
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr)
	if code == ExitOK {
		t.Fatalf("the flows passed; they were supposed to fail and leave a capture each\nstderr: %s",
			stderr.String())
	}
	for _, name := range []string{"shard-1", "shard-2"} {
		entries, err := os.ReadDir(filepath.Join(output, name))
		if err != nil {
			t.Fatalf("no %s directory: %v", name, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s produced no artifacts", name)
		}
	}
}

func TestAnUnshardedRunKeepsItsArtifactsWhereTheyWere(t *testing.T) {
	t.Parallel()

	// The control for the test above: a runner that always added a shard
	// subdirectory would move every existing run's artifacts.
	dir := t.TempDir()
	output := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"),
		"appId: com.example.a\n---\n- assertVisible: Nope\n")

	runner := TestRunner{NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
		return DeviceSession{
			Driver:          emptyScreenDriver(),
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   dir,
			Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
			ExecutionID:     "test-execution",
		}, nil
	}}
	if code := runner.Run(context.Background(), []string{
		"--test-output-dir", output, filepath.Join(dir, "a.yaml"),
	}, &stdoutSink, &stdoutSink); code == ExitOK {
		t.Fatal("the flow passed; it was supposed to fail and leave a capture")
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "shard-") {
			t.Fatalf("an unsharded run created %s", entry.Name())
		}
	}
	if len(entries) == 0 {
		t.Fatal("the failure capture did not reach the output directory")
	}
}

var stdoutSink discard

// The runner carries the plan's failure policy into the session.

func TestTheRunnerCarriesTheWorkspaceOrderIntoTheSession(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"a.yaml", "b.yaml"} {
		writeFile(t, filepath.Join(dir, name), "appId: com.example.a\n---\n- launchApp\n")
	}
	writeFile(t, filepath.Join(dir, "config.yaml"),
		"executionOrder:\n  flowsOrder:\n    - a\n    - b\n  continueOnFailure: true\n")

	var seen TestOptions
	runner := TestRunner{NewSession: func(shard Shard, options TestOptions) (TestSession, error) {
		seen = options
		return DeviceSession{
			Driver:          permissiveDriver(),
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   dir,
			Clock:           &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()},
			ExecutionID:     "test-execution",
		}, nil
	}}

	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{
		"--test-output-dir", t.TempDir(), dir,
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	if seen.SequencedRoots != 2 {
		t.Fatalf("SequencedRoots = %d, want the two flows the order named", seen.SequencedRoots)
	}
	if !seen.ContinueOnFailure {
		t.Fatal("ContinueOnFailure did not reach the session")
	}
}
