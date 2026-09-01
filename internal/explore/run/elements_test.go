package run

import (
	"fmt"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
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
	// The two rungs that only say where the element is carry its label too,
	// for the report to name it with. e4 has no label to carry and e5 has
	// neither label nor id, which is why they fell this far.
	want := []explore.Locator{
		{Kind: explore.LocatorID, Value: "add"},
		{Kind: explore.LocatorText, Value: "New Reminder"},
		{Kind: explore.LocatorPoint, Value: "20,30", Label: "Today"},
		{Kind: explore.LocatorPoint, Value: "5,5", Label: "Today"},
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

func TestDecodeTargetReadsARowNamePassedAsAnID(t *testing.T) {
	// A weak model writes the table's row name into the id field
	// (`{"id":"e5"}`); that is the row, not a resource id, unless some
	// element on the screen really carries that id.
	// e7 sits only in the tree: the table left it out, and the id selector
	// searches the tree, so it must still be read as a real id.
	state := &explore.ScreenState{
		Hierarchy: device.TreeNode{Children: []device.TreeNode{
			{Attributes: map[string]string{"text": "Title"}},
			{Children: []device.TreeNode{{Attributes: map[string]string{"resource-id": "e7"}}}},
			// Android ids match on the suffix after the last "/".
			{Attributes: map[string]string{"resource-id": "com.app:id/e8"}},
		}},
		Elements: []explore.FlatElement{
			{EIDX: 0, Node: device.TreeNode{Attributes: map[string]string{"text": "Title"}}},
		},
	}
	got, err := decodeTarget([]byte(`{"id":"e0"}`), state)
	if err != nil || got.EIDX == nil || *got.EIDX != 0 || got.ID != "" {
		t.Fatalf("e0 as id -> %+v, %v", got, err)
	}
	got, err = decodeTarget([]byte(`{"id":"e7"}`), state)
	if err != nil || got.EIDX != nil || got.ID != "e7" {
		t.Fatalf("a real id e7 was rewritten: %+v, %v", got, err)
	}
	got, err = decodeTarget([]byte(`{"id":"e8"}`), state)
	if err != nil || got.EIDX != nil || got.ID != "e8" {
		t.Fatalf("an Android id suffix e8 was rewritten: %+v, %v", got, err)
	}
	got, err = decodeTarget([]byte(`{"id":"e9"}`), state)
	if err != nil || got.EIDX == nil || *got.EIDX != 9 {
		t.Fatalf("an unknown row keeps its index for the miss message: %+v, %v", got, err)
	}
	if _, err = decodeTarget([]byte(`{"idx":1}`), state); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestLabelLocatorGeneralizesACountSoTheFlowSurvivesOtherData(t *testing.T) {
	// A label unique on the screen can still carry app state: session mmx23
	// exported `tapOn "All, 12 reminders"`, which stops matching the moment
	// the list holds a different number. The engine compiles every text
	// selector as a regexp anchored to the whole value, so the digits can
	// become \d+ and the selector keeps meaning the row it came from.
	state := screenOfLabels(
		"All, 12 reminders",
		// No digits: nothing to generalize, so the label stays literal.
		"New Reminder",
		// Regexp metacharacters in the label must be escaped, or the
		// selector stops matching the very row it was written for.
		"Total: $5.00 (2 items)",
		// Generalizing would collide with the next row, so the literal
		// label wins: a selector that matches two rows is worse.
		"Slot 1",
		"Slot 2",
	)
	want := []string{
		`All, \d+ reminders`,
		"New Reminder",
		`Total: \$\d+\.\d+ \(\d+ items\)`,
		`Slot 1`,
		`Slot 2`,
	}
	for eidx, expected := range want {
		if got := labelLocator(t, state, eidx); got.Kind != explore.LocatorText || got.Value != expected {
			t.Fatalf("e%d locator = %+v, want text %q", eidx, got, expected)
		}
	}
}

func TestAGeneralizedCountSelectorStillFindsTheRowOnAnyData(t *testing.T) {
	// The point of generalizing is that the DEVICE matcher accepts the
	// selector: a pattern the exporter is proud of and the engine cannot
	// use is worse than the literal it replaced. This runs the real
	// matcher, once on the count the flow was recorded with and once on a
	// different one, and pins that the literal fails the second.
	state := screenOfLabels("All, 12 reminders")
	got := labelLocator(t, state, 0)
	if got.Kind != explore.LocatorText {
		t.Fatalf("locator = %+v, want a text selector", got)
	}

	find := func(pattern, label string) int {
		root, err := hierarchy.New(device.TreeNode{Children: []device.TreeNode{
			{Attributes: map[string]string{"text": label, "bounds": "[0,0][10,10]"}},
		}})
		if err != nil {
			t.Fatalf("normalize tree: %v", err)
		}
		found, err := matching.Find(root, model.ElementSelector{TextRegex: &pattern})
		if err != nil {
			t.Fatalf("selector %q: %v", pattern, err)
		}
		return len(found)
	}

	for _, label := range []string{"All, 12 reminders", "All, 3 reminders", "All, 0 reminders"} {
		if find(got.Value, label) != 1 {
			t.Fatalf("selector %q did not find %q", got.Value, label)
		}
	}
	// Negative control: the literal the exporter wrote before only matches
	// the one count it was recorded with.
	if find("All, 12 reminders", "All, 3 reminders") != 0 {
		t.Fatal("the literal label matched a different count, so this test proves nothing")
	}
}

// screenOfLabels builds a state whose flat elements and tree agree, so a
// uniqueness check that runs the device's own matcher sees the same screen
// the element table describes.
func screenOfLabels(labels ...string) *explore.ScreenState {
	state := &explore.ScreenState{}
	children := make([]device.TreeNode, 0, len(labels))
	for index, label := range labels {
		node := device.TreeNode{Attributes: map[string]string{
			"accessibilityText": label, "bounds": "[0,0][10,10]"}}
		children = append(children, node)
		state.Elements = append(state.Elements, explore.FlatElement{
			EIDX: index, Path: fmt.Sprintf("0/%d", index), Node: node})
	}
	state.Hierarchy = device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][100,100]"},
		Children: children}
	return state
}

func labelLocator(t *testing.T, state *explore.ScreenState, eidx int) explore.Locator {
	t.Helper()
	got := (targetArgs{EIDX: &eidx}).locator(state)
	if got == nil {
		t.Fatalf("e%d produced no locator", eidx)
	}
	return *got
}

// A label that is ONLY a count identifies nothing once the count is gone:
// `\d+` matches every number on the screen, so the exported flow taps a
// price, a page number, or a badge instead of the row it was recorded on.
// That is the outcome generalizing exists to avoid, reached by generalizing.
func TestACountWithNoLabelAroundItIsNotGeneralized(t *testing.T) {
	state := screenOfLabels("3", "Inbox")
	if got := labelLocator(t, state, 0); got.Value != "3" {
		t.Fatalf("locator = %+v, want the literal label", got)
	}
}

// The literal branch put the label on the wire unescaped, and every text
// selector is compiled as a regexp: "a*b" matches "b" and "aab", and an
// unbalanced bracket does not compile at all, which fails the step.
func TestALiteralLabelIsEscapedBeforeItBecomesASelector(t *testing.T) {
	for _, want := range []struct{ label, selector string }{
		{"a*b", `a\*b`},
		{"Reply (draft", `Reply \(draft`},
		{"Save", "Save"},
	} {
		state := screenOfLabels(want.label)
		if got := labelLocator(t, state, 0); got.Value != want.selector {
			t.Fatalf("label %q gave %q, want %q", want.label, got.Value, want.selector)
		}
	}
}

// The uniqueness check has to be the device's own matcher. The flat element
// table drops nodes the matcher still searches, so counting rows in the
// table calls a pattern unique that the device resolves to two elements --
// and the first one in document order wins.
func TestGeneralizingCountsWithTheMatcherTheDeviceUses(t *testing.T) {
	state := screenOfLabels("Sort by 3")
	// A container the flat table never lists, carrying a label the matcher
	// does see.
	state.Hierarchy.Children = append([]device.TreeNode{{
		Attributes: map[string]string{"accessibilityText": "Sort by 9", "bounds": "[0,0][100,20]"},
	}}, state.Hierarchy.Children...)
	if got := labelLocator(t, state, 0); got.Value != `Sort by 3` {
		t.Fatalf("locator = %+v, want the literal label: the pattern matches two elements", got)
	}
}

// A row for a text field reads the same whether the field is empty or holds
// the word it prompts with, so the judge cannot answer "is it empty?".
// Session mmx56 filed a [High] defect saying exactly that: "e1 searchField
// 'Search' is visible but the expected outcome requires it to be empty with
// no entered text; no text content or emptiness confirmation is provided".
// The fact was there and unreadable: captured on iOS 26.2, 2026-08-31, the
// empty shortcuts field sends only hintText and accessibilityText "Search",
// the filled contacts field adds text and value "ZZZNoSuchContact".
func TestElementTableSaysWhenAFieldIsEmpty(t *testing.T) {
	t.Parallel()

	state := &explore.ScreenState{
		Signature: explore.ScreenSignature{TreeDigest: "abcdef0123456789"},
		Elements: []explore.FlatElement{
			{EIDX: 0, Node: device.TreeNode{Attributes: map[string]string{
				"elementType": "45", "hintText": "Search", "accessibilityText": "Search",
				"bounds": "[0,0][100,50]",
			}}},
			{EIDX: 1, Node: device.TreeNode{Attributes: map[string]string{
				"elementType": "45", "hintText": "Search", "accessibilityText": "Search",
				"text": "ZZZNoSuchContact", "value": "ZZZNoSuchContact", "bounds": "[0,50][100,100]",
			}}},
		},
	}
	lines := strings.Split(elementTable(state), "\n")
	if !strings.Contains(lines[1], "empty") {
		t.Fatalf("an empty field is not marked empty: %q", lines[1])
	}
	if strings.Contains(lines[2], "empty") {
		t.Fatalf("a field holding text is marked empty: %q", lines[2])
	}
}

// An element scrolled off the screen is still listed, and the tester picks
// it: mmx59 tapped one twice at 372,-51. The row now says so, so the model
// can scroll first instead of spending a turn on a tap that cannot land.
func TestElementTableMarksAnElementOffTheScreen(t *testing.T) {
	t.Parallel()

	state := &explore.ScreenState{
		Signature: explore.ScreenSignature{TreeDigest: "abcdef0123456789"},
		Viewport:  device.Bounds{Width: 402, Height: 874},
		Elements: []explore.FlatElement{
			{EIDX: 0, Node: device.TreeNode{Attributes: map[string]string{
				"elementType": "9", "accessibilityText": "Add", "bounds": "[344,-79][400,-23]"}}},
			{EIDX: 1, Node: device.TreeNode{Attributes: map[string]string{
				"elementType": "9", "accessibilityText": "Search", "bounds": "[0,100][402,150]"}}},
		},
	}
	lines := strings.Split(elementTable(state), "\n")
	if !strings.Contains(lines[1], "off-screen") {
		t.Fatalf("an element off the screen is not marked: %q", lines[1])
	}
	if strings.Contains(lines[2], "off-screen") {
		t.Fatalf("an element on the screen is marked off-screen: %q", lines[2])
	}
}
