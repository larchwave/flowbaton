package cli

import (
	"fmt"

	"github.com/larchwave/flowbaton/internal/workspace"
)

// Splitting a run across devices.
//
// specs/03-cli-tooling.md:28 gives two shapes: --shard-split partitions the
// selected flows across N devices by `index % shards`, and --shard-all
// replicates the whole suite to each of N devices. Both need N devices, and
// neither can be combined with a configured flow order.
//
// Planning is a pure function on purpose. It is the part where getting the
// arithmetic wrong is invisible — a suite that quietly ran three of its five
// flows still exits 0 — so it is the part that has to be checkable without a
// device.

// Shard is one device's share of a run.
type Shard struct {
	// Index is 0-based; the human-facing number is Index+1.
	Index int
	// Roots are the flow paths this shard runs, in the plan's own order.
	Roots []string
	// Device is the udid this shard runs on. It is blank for an unsharded run
	// with no --device, so the driver keeps saying which flag is missing.
	Device string
	// OutputDirectory is where this shard's artifacts go. The runner fills it
	// in after planning; planning itself has no opinion about the filesystem.
	OutputDirectory string
	// DriverPort is the loopback port this shard's driver talks to. Shard 1
	// keeps the contract's port; later shards get one each, because two shards
	// on one port drive the same runner.
	DriverPort int
}

// Count is the human-facing shard number, matching the `shard-N` directories.
func (shard Shard) Count() int { return shard.Index + 1 }

// PlanShards divides a discovered plan into per-device shards.
func PlanShards(options TestOptions, plan workspace.Plan) ([]Shard, error) {
	count, replicate := shardCount(options)
	// What is attached, unless --device narrows it. See shard_devices.go.
	devices, err := options.devicePool(count)
	if err != nil {
		return nil, err
	}
	switch {
	case count > 1:
		if err := shardable(options, devices, plan, count); err != nil {
			return nil, err
		}
	case len(devices) > 1:
		// Several devices and no shard flag. Taking the first would run one
		// device's worth of a suite the operator handed several devices to,
		// and report success.
		return nil, fmt.Errorf(
			"%d devices were given but no sharding was asked for; pass --shard-split or --shard-all",
			len(devices))
	}

	shards := make([]Shard, count)
	for index := range shards {
		shards[index] = Shard{Index: index, Device: deviceAt(devices, index)}
	}
	for index, flow := range plan.Flows {
		if replicate {
			for shard := range shards {
				shards[shard].Roots = append(shards[shard].Roots, flow.Path)
			}
			continue
		}
		target := index % count
		shards[target].Roots = append(shards[target].Roots, flow.Path)
	}
	return shards, nil
}

// shardCount reports how many shards were asked for and whether the whole
// suite goes to each. ParseTestOptions has already refused both flags at once.
func shardCount(options TestOptions) (count int, replicate bool) {
	switch {
	case options.ShardAll > 0:
		return options.ShardAll, true
	case options.ShardSplit > 0:
		return options.ShardSplit, false
	default:
		return 1, false
	}
}

func shardable(options TestOptions, devices []string, plan workspace.Plan, count int) error {
	if len(plan.Sequence) > 0 {
		// The workspace named an execution order. Shards run at the same time
		// on different devices, so the order cannot survive; running anyway
		// would execute a suite whose whole point was the ordering.
		return fmt.Errorf(
			"cannot shard a workspace that configures a flow order: %d flows run in a fixed order",
			len(plan.Sequence))
	}
	if len(devices) < count {
		// Sharding onto fewer devices than shards either serializes the run —
		// the thing sharding exists to avoid — or puts two shards on one
		// device, where they fight over the same screen. The contract refuses
		// the same way ("You have 2 devices connected, which is not enough to
		// run 3 shards"), and the count has to be in the message or the
		// operator cannot act on it.
		return fmt.Errorf(
			"%d shards need %d devices; %d are available (attached, or named with --device)",
			count, count, len(devices))
	}
	if options.ShardSplit > 0 && len(plan.Flows) < count {
		// An empty shard holds a device for the whole run and reports nothing.
		return fmt.Errorf(
			"--shard-split %d was asked for but only %d flows were selected",
			count, len(plan.Flows))
	}
	return nil
}

func deviceAt(devices []string, index int) string {
	if index < len(devices) {
		return devices[index]
	}
	return ""
}
