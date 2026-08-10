package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/larchwave/flowbaton/internal/android"
)

// A sharded run uses all attached devices for the selected platform unless
// --device narrows the pool. The run fails when the pool cannot satisfy the
// requested shard count.

func (options TestOptions) devicePool(ctx context.Context, count int) ([]string, error) {
	if len(options.Devices) > 0 || count < 2 {
		// Named devices win, and an unsharded run must not list at all: the
		// single-device path resolves its own serial later, and listing here
		// would turn a one-device run into a listing error.
		return options.Devices, nil
	}
	list := options.attachedDevices
	if list == nil {
		list = attachedDevices
	}
	pool, err := list(ctx, options.Platform)
	if err != nil {
		return nil, fmt.Errorf("listing devices to shard across: %w", err)
	}
	return pool, nil
}

// attachedDevices reads the same inventory `list-devices` prints.
func attachedDevices(ctx context.Context, platform string) ([]string, error) {
	switch platform {
	case "android":
		devices, err := android.ListDevices(ctx, nil)
		if err != nil {
			return nil, err
		}
		serials := make([]string, 0, len(devices))
		for _, device := range devices {
			serials = append(serials, device.Serial)
		}
		return serials, nil
	case "ios":
		// Simulators and attached hardware shard together under one platform
		// token. Either tool may be absent on this machine; only both failing
		// with nothing listed is an error.
		simulators, simulatorErr := iosSimulatorInventory(ctx)
		physical, physicalErr := iosPhysicalInventory(ctx)
		udids := make([]string, 0, len(simulators)+len(physical))
		for _, device := range simulators {
			udids = append(udids, device.UDID)
		}
		for _, device := range physical {
			udids = append(udids, device.UDID)
		}
		if len(udids) == 0 && simulatorErr != nil && physicalErr != nil {
			return nil, errors.Join(simulatorErr, physicalErr)
		}
		return udids, nil
	default:
		// web has no device inventory to shard across, and an empty platform
		// is refused before a driver is built.
		return nil, nil
	}
}
