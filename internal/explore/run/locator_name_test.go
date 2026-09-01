package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

func locatorFor(t *testing.T, root device.TreeNode, index int) *explore.Locator {
	t.Helper()
	state := makeState("app", root)
	state.Viewport = device.Bounds{Width: 402, Height: 874}
	if index >= len(state.Elements) {
		t.Fatalf("only %d rows", len(state.Elements))
	}
	return elementLocator(state, state.Elements[index])
}

// A day of the calendar month strip is named by its value, so thirty-one of
// them shared one label and none could be selected on: every tap on the strip
// exported as a coordinate, which breaks the moment the layout shifts. The
// locator now goes on the row's NAME, which the device matches through
// accessibilityText. Census over nineteen captures: 40 of 104 point locators
// become text ones.
func TestLocatorGoesOnTheRowName(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Monday, August 31",
				"text": "No events", "bounds": "[36,146][96,190]"}},
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Monday, August 24",
				"text": "No events", "bounds": "[96,146][156,190]"}},
		},
	}
	locator := locatorFor(t, root, 0)
	if locator.Kind != explore.LocatorText || locator.Value != "Monday, August 31" {
		t.Errorf("locator = %s %q, want the row's name", locator.Kind, locator.Value)
	}
}

// A label unique among the table's rows can still match a node the matcher
// searches and the table drops, and a selector matching two elements taps the
// wrong one. The count now comes from the matcher the device runs, which is
// what the generalized branch beside it already used.
func TestLocatorRefusesALabelTheDeviceFindsTwice(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			// The row the table lists.
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Library",
				"bounds": "[0,0][402,60]"}},
			// The same name on a node the table drops for having no area,
			// and the matcher searches anyway.
			{Attributes: map[string]string{"elementType": "48", "accessibilityText": "Library"}},
		},
	}
	if locator := locatorFor(t, root, 0); locator.Kind != explore.LocatorPoint {
		t.Errorf("locator = %s %q, want a point for an ambiguous label", locator.Kind, locator.Value)
	}
}
