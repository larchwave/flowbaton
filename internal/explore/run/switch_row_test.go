package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func toggle(attrs map[string]string, checked bool) device.TreeNode {
	attrs["bounds"] = "[16,100][76,130]"
	return device.TreeNode{Attributes: attrs, Checked: &checked}
}

func rowFor(t *testing.T, node device.TreeNode) string {
	t.Helper()
	state := makeState("app", screen("Motion", node))
	for _, line := range strings.Split(elementTable(state), "\n") {
		if strings.HasPrefix(line, "e0 ") {
			return line
		}
	}
	t.Fatalf("no element row:\n%s", elementTable(state))
	return ""
}

// A switch keeps its name and says which way it is set. iOS answers a switch
// with its state in the VALUE and ElementLabel prefers text, so every switch
// row read `e3 switch "0"`: the control had no name and its state was a bare
// digit the model had to know to read. Captured live, iOS 26.2, Settings >
// Accessibility > Motion, 2026-09-01 -- six such rows on one screen, each
// named only by the button row above it.
func TestElementTableNamesASwitchAndSaysHowItIsSet(t *testing.T) {
	t.Parallel()

	on := rowFor(t, toggle(map[string]string{
		"elementType": "40", "accessibilityText": "Auto-Play Animated Images", "text": "1"}, true))
	if !strings.Contains(on, `"Auto-Play Animated Images"`) {
		t.Errorf("the switch row does not name the control: %s", on)
	}
	if !strings.HasSuffix(on, " on") {
		t.Errorf("the switch row does not say it is on: %s", on)
	}

	off := rowFor(t, toggle(map[string]string{
		"elementType": "40", "accessibilityText": "Reduce Motion", "text": "0"}, false))
	if !strings.HasSuffix(off, " off") {
		t.Errorf("the switch row does not say it is off: %s", off)
	}

	// Android names the widget in class and puts the name in text, which is
	// already the label; only the state is missing.
	android := rowFor(t, toggle(map[string]string{
		"class": "android.widget.Switch", "text": "Wi-Fi"}, true))
	if !strings.Contains(android, `"Wi-Fi"`) || !strings.HasSuffix(android, " on") {
		t.Errorf("android switch row = %s", android)
	}

	// Nothing else carries the mark: a button reports checked false on iOS
	// for every row on screen, and marking those would say every control is
	// a switch that is off.
	plain := rowFor(t, toggle(map[string]string{
		"elementType": "9", "accessibilityText": "General"}, false))
	if strings.HasSuffix(plain, " off") {
		t.Errorf("a button is marked like a switch: %s", plain)
	}
}

// iOS frames the whole settings row as the switch and nests the label and the
// knob inside it, so the outer node is the only one carrying both the name and
// the identifier. Captured live on the settings app, Accessibility > Motion,
// 2026-09-01:
//
//	switch "Reduce Motion" id=REDUCE_MOTION text="0"  [36,146][366,174]
//	  button "Reduce Motion" id=REDUCE_MOTION         [36,150][150,170]
//	  switch (no label)      text="0"                 [305,146][368,174]
//
// The container rule in isInteresting dropped the outer node and kept the
// knob, so the table offered `switch "0"` and no row said which setting it
// belonged to.
func TestElementTableListsTheNamedSwitchAndNotItsKnob(t *testing.T) {
	t.Parallel()

	knob := toggle(map[string]string{"elementType": "40", "text": "0"}, false)
	knob.Attributes["bounds"] = "[305,146][368,174]"
	label := device.TreeNode{Attributes: map[string]string{
		"elementType": "9", "accessibilityText": "Reduce Motion",
		"id": "REDUCE_MOTION", "bounds": "[36,150][150,170]"}}
	row := toggle(map[string]string{
		"elementType": "40", "accessibilityText": "Reduce Motion",
		"id": "REDUCE_MOTION", "text": "0"}, false)
	row.Attributes["bounds"] = "[36,146][366,174]"
	row.Children = []device.TreeNode{label, knob}

	table := elementTable(makeState("com.apple.settings", screen("Motion", row)))
	switches := 0
	for _, line := range strings.Split(table, "\n") {
		if strings.Contains(line, "switch ") {
			switches++
			if !strings.Contains(line, `"Reduce Motion"`) {
				t.Errorf("the switch row does not name the setting: %s", line)
			}
			if !strings.HasSuffix(line, " off") {
				t.Errorf("the switch row does not say how it is set: %s", line)
			}
		}
	}
	if switches != 1 {
		t.Errorf("%d switch rows for one switch:\n%s", switches, table)
	}
}
