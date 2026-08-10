package android

import (
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
)

// DeclaredCapabilities is the non-mutating Android capability document used
// by both the driver and selected-platform flow preflight.
func DeclaredCapabilities() device.Capabilities {
	document := drivercontract.Android()
	return device.Capabilities{Platform: Platform, Features: document.Features()}
}
