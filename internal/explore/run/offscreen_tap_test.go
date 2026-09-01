package run

import (
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// An element scrolled entirely off the screen still sits in the element
// table, and hierarchy.VisibleCenter falls back to its own geometric centre
// when it does not touch the viewport at all. Session mmx59 tapped 372,-51
// twice that way: the taps hit nothing, the scenario passed, and the flow it
// exported cannot run -- the engine refuses it with "command tapOn point
// coordinates must not be negative".
func TestResolvePointRefusesAnElementOffTheScreen(t *testing.T) {
	t.Parallel()

	eidx := 0
	state := &explore.ScreenState{
		Viewport: device.Bounds{Width: 402, Height: 874},
		Elements: []explore.FlatElement{{EIDX: 0, Node: device.TreeNode{
			Attributes: map[string]string{"bounds": "[344,-79][400,-23]", "elementType": "9",
				"accessibilityText": "Add"},
		}}},
	}
	_, err := resolvePoint(state, targetArgs{EIDX: &eidx})
	if err == nil {
		t.Fatal("resolvePoint accepted an element that is not on the screen")
	}
	var miss explore.TargetMissError
	if !errors.As(err, &miss) {
		t.Fatalf("error %v is not a target miss", err)
	}
	if !strings.Contains(miss.Reason, "off the screen") {
		t.Fatalf("reason = %q, want it to say the element is off the screen", miss.Reason)
	}
}

// A row half past the bottom edge is still tappable, and the point is the
// centre of the part that shows.
func TestResolvePointKeepsAPartlyVisibleElement(t *testing.T) {
	t.Parallel()

	eidx := 0
	state := &explore.ScreenState{
		Viewport: device.Bounds{Width: 402, Height: 874},
		Elements: []explore.FlatElement{{EIDX: 0, Node: device.TreeNode{
			Attributes: map[string]string{"bounds": "[0,850][402,950]", "elementType": "9"},
		}}},
	}
	point, err := resolvePoint(state, targetArgs{EIDX: &eidx})
	if err != nil {
		t.Fatal(err)
	}
	if point.X != 201 || point.Y != 862 {
		t.Fatalf("point = %+v, want the centre of the visible strip", point)
	}
}
