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

func TestPruneElementTablesDropsTheOpeningTableOnceANewerOneExists(t *testing.T) {
	// The opening user message embeds the first element table. Once a tool
	// result carries a newer one, the opening table is stale like any other
	// and must go, while the scenario text before it stays.
	state := &explore.ScreenState{Elements: []explore.FlatElement{
		{EIDX: 0, Node: device.TreeNode{Attributes: map[string]string{"text": "Title"}}},
	}}
	opening := "Scenario: elements on screen count\n\n" + elementTable(state)
	messages := []explore.Message{
		{Role: explore.RoleSystem, Text: "system"},
		{Role: explore.RoleUser, Text: opening},
	}
	alone := pruneElementTables(messages)
	if alone[1].Text != opening {
		t.Fatalf("the only table was pruned: %q", alone[1].Text)
	}

	messages = append(messages,
		explore.Message{Role: explore.RoleAssistant, Text: "tap"},
		explore.Message{Role: explore.RoleTool, Text: "tap ok, screen changed\n\n" + elementTable(state)},
	)
	pruned := pruneElementTables(messages)
	if got := pruned[1].Text; !strings.HasPrefix(got, "Scenario: elements on screen count\n") || strings.Contains(got, "e0 ") {
		t.Fatalf("opening table not pruned or scenario text lost: %q", got)
	}
	if !strings.Contains(pruned[3].Text, "e0 ") {
		t.Fatalf("newest table was pruned: %q", pruned[3].Text)
	}
	if messages[1].Text != opening {
		t.Fatal("input slice was mutated")
	}
}

func TestEIDXLocatorClimbsTheExportLadder(t *testing.T) {
	// An eidx target leaves the flow exporter with a selector it can
	// write: an id, else a label unique on the screen, else the tap point.
	// A tree path only survives when the element has no bounds.
	state := &explore.ScreenState{Elements: []explore.FlatElement{
		{EIDX: 0, Path: "0/0", Node: device.TreeNode{Attributes: map[string]string{
			"resource-id": "add", "text": "Add", "bounds": "[0,0][10,10]"}}},
		{EIDX: 1, Path: "0/1", Node: device.TreeNode{Attributes: map[string]string{
			"label": "New Reminder", "bounds": "[0,0][10,10]"}}},
		{EIDX: 2, Path: "0/2", Node: device.TreeNode{Attributes: map[string]string{
			"label": "Today", "bounds": "[10,20][30,40]"}}},
		{EIDX: 3, Path: "0/3", Node: device.TreeNode{Attributes: map[string]string{
			"label": "Today", "bounds": "[0,0][10,10]"}}},
		{EIDX: 4, Path: "0/4", Node: device.TreeNode{Attributes: map[string]string{
			"elementType": "9"}}},
		{EIDX: 5, Path: "0/5", Node: device.TreeNode{Attributes: map[string]string{
			"bounds": "[379,0][390,11]"}}},
	}}
	want := []explore.Locator{
		{Kind: explore.LocatorID, Value: "add"},
		{Kind: explore.LocatorText, Value: "New Reminder"},
		{Kind: explore.LocatorPoint, Value: "20,30"},
		{Kind: explore.LocatorPoint, Value: "5,5"},
		{Kind: explore.LocatorPath, Value: "0/4"},
		// The engine's tapOn point takes integers only, and a screen 390
		// wide ends at 389: the half-pixel center 384.5,5.5 floors so the
		// point stays inside the element instead of past an edge.
		{Kind: explore.LocatorPoint, Value: "384,5"},
	}
	for eidx, expected := range want {
		index := eidx
		got := (targetArgs{EIDX: &index}).locator(state)
		if got == nil || *got != expected {
			t.Fatalf("e%d locator = %+v, want %+v", eidx, got, expected)
		}
	}
	missing := 9
	got := targetArgs{EIDX: &missing}.locator(state)
	if got != nil {
		t.Fatalf("unknown eidx produced a locator: %+v", got)
	}
}
