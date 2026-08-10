package iosdevice

import (
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/ios"
)

// DeclaredCapabilities is the physical-iOS capability document. The feature
// truth lives in drivercontract.IOSPhysical() so preflight and this driver
// cannot disagree; every false there has a matching device.ErrUnsupported at
// call time. The platform stays the shared iOS token: flows target "ios",
// and the flavor is the driver's concern, not the flow's.
func DeclaredCapabilities() device.Capabilities {
	return device.Capabilities{
		Platform: ios.Platform,
		Features: drivercontract.IOSPhysical().Features(),
	}
}
