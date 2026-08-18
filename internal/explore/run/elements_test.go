package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

func TestElementTableMarksTextInputsAndKeyboardFocus(t *testing.T) {
	// Typing needs a focused text field. Without both marks the model cannot
	// tell which row accepts text, nor whether its tap actually took focus.
	yes := true
	state := &explore.ScreenState{
		Elements: []explore.FlatElement{
			{EIDX: 0, Node: device.TreeNode{Attributes: map[string]string{
				"class": "android.widget.TextView", "text": "Title"}}},
			{EIDX: 1, Node: device.TreeNode{
				Attributes: map[string]string{"class": "android.widget.EditText", "hintText": "Name"},
				Focused:    &yes,
			}},
			{EIDX: 2, Node: device.TreeNode{Attributes: map[string]string{
				"elementType": "49", "hintText": "Search"}}},
		},
	}
	lines := strings.Split(strings.TrimSpace(elementTable(state)), "\n")
	if len(lines) != 4 {
		t.Fatalf("table has %d lines: %q", len(lines), lines)
	}
	if strings.Contains(lines[1], "text-field") || strings.Contains(lines[1], "focused") {
		t.Fatalf("static text row marked as an input: %q", lines[1])
	}
	if !strings.Contains(lines[2], "text-field") || !strings.Contains(lines[2], "focused") {
		t.Fatalf("focused field row missing marks: %q", lines[2])
	}
	if !strings.Contains(lines[3], "text-field") || strings.Contains(lines[3], "focused") {
		t.Fatalf("unfocused field row marks wrong: %q", lines[3])
	}
}
