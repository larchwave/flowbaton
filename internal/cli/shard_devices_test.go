package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/ios"
	"github.com/larchwave/flowbaton/internal/iosdevice"
	"github.com/larchwave/flowbaton/internal/workspace"
)

// A sharded run uses all attached devices for the selected platform unless
// --device narrows the pool. The run fails when the pool cannot satisfy the
// requested shard count.

func devicePlan(paths ...string) workspace.Plan {
	flows := make([]workspace.Flow, 0, len(paths))
	for _, path := range paths {
		flows = append(flows, workspace.Flow{Path: path})
	}
	return workspace.Plan{Flows: flows}
}

func TestShardingFindsTheAttachedDevicesWhenNoneAreNamed(t *testing.T) {
	t.Parallel()

	options := TestOptions{Platform: "android", ShardSplit: 2}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		return []string{"emulator-5554", "emulator-5556"}, nil
	}

	shards, err := PlanShards(context.Background(), options, devicePlan("a.yaml", "b.yaml", "c.yaml", "d.yaml"))
	if err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("shards = %d, want 2", len(shards))
	}
	if shards[0].Device == shards[1].Device {
		t.Fatalf("both shards took %q; two shards on one device fight over the screen",
			shards[0].Device)
	}
}

// --device still narrows the pool: an operator naming one device for two
// shards is refused, the way the contract refuses it.
func TestNamingFewerDevicesThanShardsIsStillRefused(t *testing.T) {
	t.Parallel()

	options := TestOptions{Platform: "android", ShardSplit: 2, Devices: []string{"emulator-5554"}}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		return []string{"emulator-5554", "emulator-5556"}, nil
	}

	_, err := PlanShards(context.Background(), options, devicePlan("a.yaml", "b.yaml"))
	if err == nil {
		t.Fatal("one named device was stretched across two shards")
	}
}

// Fewer attached than asked for is the contract's refusal too, and the error
// has to say how many were actually found or the operator cannot act on it.
func TestTooFewAttachedDevicesIsRefusedWithTheCount(t *testing.T) {
	t.Parallel()

	options := TestOptions{Platform: "android", ShardSplit: 3}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		return []string{"emulator-5554", "emulator-5556"}, nil
	}

	_, err := PlanShards(context.Background(), options, devicePlan("a.yaml", "b.yaml", "c.yaml"))
	if err == nil {
		t.Fatal("three shards were planned onto two devices")
	}
	if !strings.Contains(err.Error(), "2") || !strings.Contains(err.Error(), "3") {
		t.Fatalf("the error did not carry both counts: %v", err)
	}
}

// More attached than asked for uses only what was asked for, which is what the
// With --shard-split 1 and two emulators attached, the first device is selected.
func TestExtraAttachedDevicesAreLeftAlone(t *testing.T) {
	t.Parallel()

	options := TestOptions{Platform: "android", ShardSplit: 1}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		return []string{"emulator-5554", "emulator-5556"}, nil
	}

	shards, err := PlanShards(context.Background(), options, devicePlan("a.yaml"))
	if err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("shards = %d, want 1", len(shards))
	}
}

// A broken listing is a real error, not an empty pool: reporting "0 devices"
// when adb itself failed sends the operator looking for the wrong problem.
func TestAFailedListingIsReportedAsOne(t *testing.T) {
	t.Parallel()

	options := TestOptions{Platform: "android", ShardSplit: 2}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		return nil, errors.New("adb: no such tool")
	}

	_, err := PlanShards(context.Background(), options, devicePlan("a.yaml", "b.yaml"))
	if err == nil || !strings.Contains(err.Error(), "no such tool") {
		t.Fatalf("the listing failure was swallowed: %v", err)
	}
}

func TestCanceledContextReachesShardInventory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	called := false
	options := TestOptions{Platform: "android", ShardSplit: 2}
	options.attachedDevices = func(got context.Context, _ string) ([]string, error) {
		called = true
		return nil, got.Err()
	}

	_, err := PlanShards(ctx, options, devicePlan("a.yaml", "b.yaml"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PlanShards() error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("shard planning did not call the device inventory")
	}
}

// An unsharded run must not go looking for devices at all — the single-device
// path resolves its own serial later, and asking here would turn a one-device
// run into a listing error on a machine with none attached yet.
func TestAnUnshardedRunDoesNotListDevices(t *testing.T) {
	t.Parallel()

	listed := false
	options := TestOptions{Platform: "android"}
	options.attachedDevices = func(context.Context, string) ([]string, error) {
		listed = true
		return nil, errors.New("should not have been called")
	}

	if _, err := PlanShards(context.Background(), options, devicePlan("a.yaml")); err != nil {
		t.Fatalf("PlanShards() error = %v", err)
	}
	if listed {
		t.Fatal("an unsharded run listed devices")
	}
}

// The direct inventory: simulators and attached hardware shard together
// under one platform token. The fake inventories come from the package-wide
// init in device_selection_test.go.
func TestIOSInventoryUnionsSimulatorsAndHardware(t *testing.T) {
	t.Parallel()

	pool, err := attachedDevices(context.Background(), "ios")
	if err != nil {
		t.Fatalf("attachedDevices() error = %v", err)
	}
	want := []string{"UDID-1", "UDID-2", "udid-one", "udid-two", "solo-udid", "00008110-PHYS"}
	if len(pool) != len(want) {
		t.Fatalf("pool = %v, want %v", pool, want)
	}
	for i, udid := range want {
		if pool[i] != udid {
			t.Fatalf("pool = %v, want %v", pool, want)
		}
	}
}

// A machine with only one of the two iOS tools still shards across what the
// working tool lists; only both failing with nothing found is an error.
func TestIOSInventoryToleratesOneAbsentTool(t *testing.T) {
	previousSim, previousPhys := iosSimulatorInventory, iosPhysicalInventory
	t.Cleanup(func() {
		iosSimulatorInventory, iosPhysicalInventory = previousSim, previousPhys
	})

	iosSimulatorInventory = func(context.Context) ([]ios.Device, error) {
		return nil, errors.New("xcrun: command not found")
	}
	iosPhysicalInventory = func(context.Context) ([]iosdevice.Device, error) {
		return []iosdevice.Device{{UDID: "00008110-ONLY"}}, nil
	}
	pool, err := attachedDevices(context.Background(), "ios")
	if err != nil {
		t.Fatalf("a missing simctl sank the hardware listing: %v", err)
	}
	if len(pool) != 1 || pool[0] != "00008110-ONLY" {
		t.Fatalf("pool = %v, want the attached phone alone", pool)
	}

	iosPhysicalInventory = func(context.Context) ([]iosdevice.Device, error) {
		return nil, errors.New("connect to usbmuxd: no such file")
	}
	if _, err := attachedDevices(context.Background(), "ios"); err == nil {
		t.Fatal("both tools failing with nothing listed must be an error")
	} else if !strings.Contains(err.Error(), "usbmuxd") || !strings.Contains(err.Error(), "xcrun") {
		t.Fatalf("error = %q, want both tool failures carried", err)
	}
}
