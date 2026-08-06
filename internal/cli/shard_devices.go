package cli

import (
	"context"
	"fmt"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/ios"
)

// A sharded run uses all attached devices for the selected platform unless
// --device narrows the pool. The run fails when the pool cannot satisfy the
// requested shard count.

func (options TestOptions) devicePool(count int) ([]string, error) {
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
	pool, err := list(context.Background(), options.Platform)
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
		devices, err := ios.NewSimctl("", nil).ListDevices(ctx)
		if err != nil {
			return nil, err
		}
		udids := make([]string, 0, len(devices))
		for _, device := range devices {
			udids = append(udids, device.UDID)
		}
		return udids, nil
	default:
		// web has no device inventory to shard across, and an empty platform
		// is refused before a driver is built.
		return nil, nil
	}
}
