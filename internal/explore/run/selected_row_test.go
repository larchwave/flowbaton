package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// iOS marks the day the calendar is showing, and nothing in the table said
// so. Sessions mmx57 and mmx60 both filed a defect whose evidence is that
// absence -- "the day strip element for Aug 31 is listed as 'other' (e5), not
// as the selected/active day-cell indicator" -- and after the judge learned to
// answer `undecidable` those became honest no-verdict lines instead. The fact
// was on the wire the whole time. Captured live, iOS 26.2, Calendar day view,
// 2026-09-01: of 125 nodes exactly one is flagged, the button labelled
// "Today, Tuesday, September 1" valued "3 events".
func TestElementTableSaysWhichRowIsSelected(t *testing.T) {
	t.Parallel()

	yes, no := true, false
	day := device.TreeNode{
		Attributes: map[string]string{
			"elementType": "9", "accessibilityText": "Today, Tuesday, September 1",
			"bounds": "[36,146][96,190]"},
		Selected: &yes,
	}
	other := device.TreeNode{
		Attributes: map[string]string{
			"elementType": "9", "accessibilityText": "Wednesday, September 2",
			"bounds": "[96,146][156,190]"},
		Selected: &no,
	}
	table := elementTable(makeState("com.apple.mobilecal", screen("September", day, other)))
	for _, line := range strings.Split(table, "\n") {
		switch {
		case strings.Contains(line, "Today, Tuesday"):
			if !strings.Contains(line, " selected") {
				t.Errorf("the selected day is not marked: %s", line)
			}
		case strings.Contains(line, "Wednesday"):
			if strings.Contains(line, " selected") {
				t.Errorf("an unselected day is marked: %s", line)
			}
		}
	}
}
