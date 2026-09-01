package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// The years screen of a calendar names the current month "Current month,
// September 2026" and every other month "October 2026". Generalizing the
// year made `September \d+` -- honestly unique the day it was recorded,
// because the prefix set it apart -- and the exported flow tapped a month
// picked by which year the screen happened to list first. Measured on that
// live screen with the host matcher: `October \d+` matched two rows and
// `January \d+` matched two, while the recorded `September \d+` matched one.
// The uniqueness check runs at record time and cannot see the month roll
// over, so the year never becomes \d+ now.
func TestLocatorKeepsAYearLiteral(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9",
				"accessibilityText": "Current month, September 2026", "bounds": "[0,0][402,60]"}},
			{Attributes: map[string]string{"elementType": "9",
				"accessibilityText": "October 2026", "bounds": "[0,60][402,120]"}},
		},
	}
	locator := locatorFor(t, root, 0)
	if locator.Kind != explore.LocatorText || locator.Value != `Current month, September 2026` {
		t.Errorf("locator = %s %q, want the label with its year intact", locator.Kind, locator.Value)
	}
}

// A count still generalizes: it is what the feature is for, and a row named
// by how much is in it stops matching the moment the data differs.
func TestLocatorStillGeneralizesACount(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9",
				"accessibilityText": "All, 12 reminders", "bounds": "[0,0][402,60]"}},
		},
	}
	locator := locatorFor(t, root, 0)
	if locator.Kind != explore.LocatorText || locator.Value != `All, \d+ reminders` {
		t.Errorf("locator = %s %q, want the count generalized", locator.Kind, locator.Value)
	}
}
