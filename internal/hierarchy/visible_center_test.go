package hierarchy_test

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

// The numbers come from a live capture of Apple's Reminders on a 402x874
// simulator: the last list row sits half past the bottom edge, FilterVisible
// keeps it at 44% visible, and its geometric center lands 4 points below the
// screen. Aiming a tap there misses the device entirely.
func TestVisibleCenterStaysOnScreenForARowPastTheBottomEdge(t *testing.T) {
	viewport := device.Bounds{Width: 402, Height: 874}
	row := device.Bounds{X: 16, Y: 847, Width: 370, Height: 62}

	if got := hierarchy.Center(row).Y; got <= float64(viewport.Height) {
		t.Fatalf("premise broken: the plain center %v is already on screen", got)
	}

	point := hierarchy.VisibleCenter(row, viewport)
	if point.Y >= float64(viewport.Height) {
		t.Errorf("VisibleCenter aimed at y=%v, past the %d-point bottom edge", point.Y, viewport.Height)
	}
	if point.Y < float64(row.Y) {
		t.Errorf("VisibleCenter aimed at y=%v, above the row's own top %d", point.Y, row.Y)
	}
	if point.X != hierarchy.Center(row).X {
		t.Errorf("VisibleCenter moved x to %v; the row is fully within the width", point.X)
	}
}

func TestVisibleCenterLeavesAFullyVisibleElementWhereItWas(t *testing.T) {
	viewport := device.Bounds{Width: 402, Height: 874}
	button := device.Bounds{X: 100, Y: 200, Width: 80, Height: 40}

	if got, want := hierarchy.VisibleCenter(button, viewport), hierarchy.Center(button); got != want {
		t.Errorf("VisibleCenter moved a fully visible element to %v, want %v", got, want)
	}
}

// An unknown viewport and an element with no overlap both fall back to the
// plain center: this change may not invent a new failure for a caller that
// cannot supply a screen size.
func TestVisibleCenterFallsBackWithoutAUsableIntersection(t *testing.T) {
	element := device.Bounds{X: 10, Y: 20, Width: 30, Height: 40}
	for name, viewport := range map[string]device.Bounds{
		"unknown viewport": {},
		"no overlap":       {X: 900, Y: 900, Width: 402, Height: 874},
	} {
		t.Run(name, func(t *testing.T) {
			if got, want := hierarchy.VisibleCenter(element, viewport), hierarchy.Center(element); got != want {
				t.Errorf("VisibleCenter returned %v, want the plain center %v", got, want)
			}
		})
	}
}
