package ios

import "github.com/larchwave/flowbaton/internal/device"

// XCUIElementTypes, as the hierarchy's elementType attribute renders them,
// that change where a touch lands or where an element can be seen.
const (
	applicationElementType = "2"
	switchElementType      = "40"
	toggleElementType      = "41"
)

// TapsInside implements device.TapTargeter. A SwiftUI Toggle is a switch that
// spans its Form row with the real switch nested at the trailing edge; a tap
// at the row's centre lands on the label and turns nothing (issue #40).
func (driver *Driver) TapsInside(element, descendant device.TreeNode) bool {
	kind := element.Attributes["elementType"]
	return (kind == switchElementType || kind == toggleElementType) &&
		descendant.Attributes["elementType"] == kind
}

// ClipsDescendants implements device.ViewportClipper: an application draws
// inside its own frame. On an iPhone that is the screen; for an iPhone-only
// app on an iPad it is the compatibility window, and the app's nodes are in
// that window's grid (issue #44).
func (driver *Driver) ClipsDescendants(node device.TreeNode) bool {
	return node.Attributes["elementType"] == applicationElementType
}
