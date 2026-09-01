package ios

import (
	"context"
	"net/http"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// A switch reports its state in the accessibility VALUE, not in the selected
// trait. Measured live on iOS 26.2, Settings > Accessibility > Motion,
// 2026-09-01: six switches, three showing "1" and three "0", every one of them
// with selected false. Reading the trait made FlowBaton answer that an on
// switch is off -- `assertVisible: {text: "^1$", checked: true}` failed on that
// screen while `checked: false` passed, which is a false pass, the one wrong
// answer a testing tool must not give.
func TestSwitchStateComesFromTheValue(t *testing.T) {
	t.Parallel()

	on, off := "1", "0"
	hierarchy := ViewHierarchy{Depth: 2, AXElement: AXElement{
		Identifier:  "root",
		ElementType: 1,
		Enabled:     true,
		Frame:       Frame{X: 0, Y: 0, Width: 402, Height: 874},
		Children: []AXElement{
			{Identifier: "on", Label: "Auto-Play Animated Images", Value: &on,
				ElementType: 40, Enabled: true, Selected: false,
				Frame: Frame{X: 16, Y: 100, Width: 60, Height: 30}},
			{Identifier: "off", Label: "Reduce Motion", Value: &off,
				ElementType: 40, Enabled: true, Selected: true,
				Frame: Frame{X: 16, Y: 200, Width: 60, Height: 30}},
			{Identifier: "box", Label: "Remember me",
				ElementType: 12, Enabled: true, Selected: true,
				Frame: Frame{X: 16, Y: 300, Width: 30, Height: 30}},
			{Identifier: "cell", Label: "General", Value: &on,
				ElementType: 75, Enabled: true, Selected: false,
				Frame: Frame{X: 0, Y: 400, Width: 402, Height: 44}},
		},
	}}

	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		writeJSON(t, writer, hierarchy)
	})
	root, err := driver.ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{AppIDs: []string{"com.example.a"}})
	if err != nil {
		t.Fatal(err)
	}

	checked := func(node device.TreeNode) bool { return node.Checked != nil && *node.Checked }
	if !checked(root.Children[0]) {
		t.Error(`a switch valued "1" reports unchecked`)
	}
	// The trait is not the state: an app that sets both must not make an off
	// switch read as on.
	if checked(root.Children[1]) {
		t.Error(`a switch valued "0" reports checked`)
	}
	// A checkable element with no value keeps the trait, which is all there
	// is to read.
	if !checked(root.Children[2]) {
		t.Error("a selected checkbox with no value reports unchecked")
	}
	// A type with no checked state at all keeps false whatever it is valued;
	// checkableTypes is the whole list.
	if checked(root.Children[3]) {
		t.Error(`a cell valued "1" reports checked`)
	}
}
