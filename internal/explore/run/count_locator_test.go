package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// A digit run becomes \d+ only where it counts something. Every generalized
// selector the exported flows carry is below. The dates among them had no
// business being generalized: they are measured ambiguous on a screen one
// tap from the one they were recorded on, with the host matcher, on
// captures of the same calendar:
//
//	week strip  text=Wednesday, September \d+  -> 1   (recorded here)
//	month grid  text=Wednesday, September \d+  -> 5
//	years       text=October \d+               -> 2
//
// The record-time uniqueness check sees only the screen in front of it, so
// it passed on both and the flow tapped whichever day the other screen
// listed first.
func TestLocatorGeneralizesOnlyACount(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		label string
		want  string
	}{
		{"All, 12 reminders", `All, \d+ reminders`},
		{"Scheduled, 4 reminder", `Scheduled, \d+ reminder`},
		{"7 events", `\d+ events`},
		{"Today, 2 reminder, August 30", `Today, \d+ reminder, August 30`},
		{"Wednesday, September 2", "Wednesday, September 2"},
		{"Sunday, August 30", "Sunday, August 30"},
		{"Current month, September 2026", "Current month, September 2026"},
	} {
		root := device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
			Children: []device.TreeNode{
				{Attributes: map[string]string{"elementType": "9",
					"accessibilityText": test.label, "bounds": "[0,0][402,60]"}},
			},
		}
		locator := locatorFor(t, root, 0)
		if locator.Kind != explore.LocatorText || locator.Value != test.want {
			t.Errorf("%q -> %s %q, want text %q", test.label, locator.Kind, locator.Value, test.want)
		}
	}
}
