package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// "[0,0][0,0]" parses cleanly and centers on the screen corner. Before the
// bounds check below it, a target landing on such an element resolved to a
// tap at (0,0): a real touch, somewhere the element is not, reported as a
// hit.
func TestAZeroAreaElementIsNotATapTarget(t *testing.T) {
	zero := 0
	state := &explore.ScreenState{Elements: []explore.FlatElement{{
		EIDX: 0,
		Node: device.TreeNode{Attributes: map[string]string{
			"id": "Reminders", "accessibilityText": "Reminders", "bounds": "[0,0][0,0]",
		}},
	}}}
	point, err := resolvePoint(state, targetArgs{EIDX: &zero})
	if err == nil {
		t.Fatalf("a zero-area element resolved to %v", point)
	}
	if !strings.Contains(err.Error(), "no usable bounds") {
		t.Fatalf("want a bounds error, got: %v", err)
	}
}
