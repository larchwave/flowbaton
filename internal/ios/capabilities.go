package ios

import (
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
)

// DeclaredCapabilities is the non-mutating iOS Simulator capability document
// used by both the driver and selected-platform flow preflight.
func DeclaredCapabilities() device.Capabilities {
	document := drivercontract.IOSSimulator()
	return device.Capabilities{Platform: Platform, Features: document.Features()}
}
