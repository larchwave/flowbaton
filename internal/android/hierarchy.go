package android

import (
	"encoding/xml"
	"fmt"

	"github.com/nohavewho/flowbaton/internal/device"
)

// The agent's viewHierarchy reply is the spec 04 §2 XML: a <hierarchy> root
// carrying the display rotation, <node> elements for every window root, and a
// toast appended as its own root child when one is showing.
//
// The conversion is a passthrough on purpose. bounds stays the Android-style
// "[l,t][r,b]" string the engine parses on every platform (specs/02 line 26),
// and every attribute the agent emitted is kept, because the attribute set is
// exactly what selectors match against. Only two things are added or dropped:
// content-desc is mirrored into accessibilityText — the matcher's name for
// what a screen reader announces (specs/04 §5) — and empty values are dropped,
// because present-but-empty and absent read the same to a person and
// differently to a matcher.

type hierarchyElement struct {
	XMLName  xml.Name
	Attrs    []xml.Attr         `xml:",any,attr"`
	Children []hierarchyElement `xml:",any"`
}

// parseHierarchy converts the agent's XML into the neutral tree. The
// <hierarchy> element itself becomes the root node, so several window roots
// and an appended toast are all reachable from one tree.
func parseHierarchy(hierarchy string) (device.TreeNode, error) {
	var root hierarchyElement
	if err := xml.Unmarshal([]byte(hierarchy), &root); err != nil {
		return device.TreeNode{}, fmt.Errorf("android hierarchy: %w", err)
	}
	if root.XMLName.Local != "hierarchy" {
		return device.TreeNode{}, fmt.Errorf(
			"android hierarchy: root element is %q, want hierarchy", root.XMLName.Local)
	}
	return convertHierarchyElement(root), nil
}

func convertHierarchyElement(element hierarchyElement) device.TreeNode {
	attributes := make(map[string]string, len(element.Attrs)+1)
	for _, attribute := range element.Attrs {
		if attribute.Value == "" {
			continue
		}
		attributes[attribute.Name.Local] = attribute.Value
	}
	if description := attributes["content-desc"]; description != "" {
		attributes["accessibilityText"] = description
	}

	node := device.TreeNode{
		Attributes: attributes,
		Clickable:  attributeBool(attributes, "clickable"),
		Enabled:    attributeBool(attributes, "enabled"),
		Focused:    attributeBool(attributes, "focused"),
		Selected:   attributeBool(attributes, "selected"),
	}
	// Every Android node carries checked="false", and every one of them reports
	// it -- so a `checked: false` selector does match nearly every node on
	// screen. Preserve the raw attribute so selectors operate on the hierarchy
	// the device actually emitted.
	node.Checked = attributeBool(attributes, "checked")

	if len(element.Children) != 0 {
		node.Children = make([]device.TreeNode, 0, len(element.Children))
		for _, child := range element.Children {
			node.Children = append(node.Children, convertHierarchyElement(child))
		}
	}
	return node
}

// attributeBool reads a tri-state boolean: nil when the agent did not emit
// the attribute at all (the toast node omits most of them).
func attributeBool(attributes map[string]string, key string) *bool {
	value, exists := attributes[key]
	if !exists {
		return nil
	}
	parsed := value == "true"
	return &parsed
}
