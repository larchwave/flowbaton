package explore

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// The iOS runner sends elementType as a number. A table row reading "-" or
// "9" gives a model nothing to tell a button from a list cell, and a live
// planner (2026-08-29) read the home screen's UPDATE button as a reminder.
func TestElementRoleNamesIOSTypesAndTrimsAndroidClasses(t *testing.T) {
	cases := map[string]device.TreeNode{
		"button":     {Attributes: map[string]string{"elementType": "9"}},
		"cell":       {Attributes: map[string]string{"elementType": "75"}},
		"staticText": {Attributes: map[string]string{"elementType": "48"}},
		"statusBar":  {Attributes: map[string]string{"elementType": "25"}},
		"999":        {Attributes: map[string]string{"elementType": "999"}},
		"EditText":   {Attributes: map[string]string{"class": "android.widget.EditText"}},
		"Web":        {Attributes: map[string]string{"type": "Web"}},
		"":           {Attributes: map[string]string{}},
	}
	for want, node := range cases {
		if got := ElementRole(node); got != want {
			t.Errorf("ElementRole(%v) = %q, want %q", node.Attributes, got, want)
		}
	}
	if len(iosElementTypeNames) != 83 {
		t.Fatalf("iOS type table has %d entries, want the header's 83", len(iosElementTypeNames))
	}
}

// iOS labels arrive as accessibilityText (or title), never as text; the
// research table showed "-" for every row of a live Reminders home screen
// while the tester's table, reading the iOS keys, showed them all.
func TestElementLabelReadsBothDialects(t *testing.T) {
	cases := map[string]device.TreeNode{
		"New Reminder": {Attributes: map[string]string{"elementType": "9", "accessibilityText": "New Reminder"}},
		"Today":        {Attributes: map[string]string{"elementType": "9", "title": "Today"}},
		"Search":       {Attributes: map[string]string{"class": "android.widget.EditText", "hintText": "Search"}},
		"Done":         {Attributes: map[string]string{"text": "Done", "accessibilityText": "ignored"}},
	}
	for want, node := range cases {
		if got := ElementLabel(node); got != want {
			t.Errorf("ElementLabel(%v) = %q, want %q", node.Attributes, got, want)
		}
	}
}
