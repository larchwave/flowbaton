package research

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// The researcher builds the UI map from this table, so a switch it cannot
// name or read reaches the planner as a row labelled with a digit. The
// tester's own table already names it; this is the other half. Captured
// live on the settings app, Accessibility > Motion, iOS 26.2, 2026-09-01.
func TestResearchTableNamesASwitchAndSaysHowItIsSet(t *testing.T) {
	t.Parallel()

	on, off := true, false
	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{
				"elementType": "40", "accessibilityText": "Auto-Play Animated Images",
				"id": "AUTOPLAY", "text": "1", "bounds": "[36,379][366,407]"}, Checked: &on},
			{Attributes: map[string]string{
				"elementType": "40", "accessibilityText": "Reduce Motion",
				"id": "REDUCE_MOTION", "text": "0", "bounds": "[36,146][366,174]"}, Checked: &off},
			{Attributes: map[string]string{
				"elementType": "9", "accessibilityText": "Back",
				"id": "BackButton", "bounds": "[0,0][60,40]"}},
		},
	}
	elements, err := explore.FlattenScreen(root)
	if err != nil {
		t.Fatal(err)
	}
	table := elementTable(elements)
	t.Log("\n" + table)

	for _, want := range []string{"Auto-Play Animated Images", "Reduce Motion", "| on |", "| off |"} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not carry %q:\n%s", want, table)
		}
	}
	// The platform's own selection reaches the same cell.
	sel := true
	root.Children = append(root.Children, device.TreeNode{
		Attributes: map[string]string{
			"elementType": "9", "accessibilityText": "Today, Tuesday, September 1",
			"bounds": "[36,600][96,644]"},
		Selected: &sel,
	})
	elements, err = explore.FlattenScreen(root)
	if err != nil {
		t.Fatal(err)
	}
	if withDay := elementTable(elements); !strings.Contains(withDay, "| selected |") {
		t.Errorf("the selected row is not marked:\n%s", withDay)
	}

	// A row with no checked state says nothing rather than claiming off.
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "Back") && strings.Contains(line, "| off |") {
			t.Errorf("a button is marked like a switch: %s", line)
		}
	}
}
