package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// selectorMatchCount asks how many elements the DEVICE would find, and its
// own comment says why counting a narrower set is wrong: the flow taps the
// wrong element. The tree it counts in must not drop rows either -- an
// off-screen row is one scroll away from being a second match, and this
// function exists to refuse a selector that could ever hit two.
func TestGeneralizingRefusesAPatternAnOffScreenRowWouldAlsoMatch(t *testing.T) {
	t.Parallel()

	row := func(text, bounds string) device.TreeNode {
		return device.TreeNode{Attributes: map[string]string{
			"class": "android.widget.TextView", "text": text, "bounds": bounds,
		}}
	}
	state := &explore.ScreenState{
		Viewport: device.Bounds{Width: 400, Height: 800},
		Hierarchy: device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
			Children: []device.TreeNode{
				row("All, 12 reminders", "[0,100][400,160]"),
				row("All, 5 reminders", "[0,2000][400,2060]"),
			},
		},
	}
	if pattern, ok := generalizeCount(state, "All, 12 reminders"); ok {
		t.Fatalf("generalized to %q, which the row scrolled below would match too", pattern)
	}
}

// A pattern nothing else matches still generalizes, so the caution is not
// refusal in every case.
func TestGeneralizingStillHappensWhenTheCountIsTheOnlyDifference(t *testing.T) {
	t.Parallel()

	state := &explore.ScreenState{
		Viewport: device.Bounds{Width: 400, Height: 800},
		Hierarchy: device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{
					"class": "android.widget.TextView", "text": "All, 12 reminders", "bounds": "[0,100][400,160]",
				},
			}},
		},
	}
	if _, ok := generalizeCount(state, "All, 12 reminders"); !ok {
		t.Fatal("a label whose only variable part is its count must still generalize")
	}
}
