package web

import (
	"encoding/json"
	"fmt"

	"github.com/nohavewho/flowbaton/internal/device"
)

// The web hierarchy is produced in the page, not in Go: an injected script
// walks the DOM and returns one JSON node per kept element (spec
// 02-device-drivers.md §4). Go only converts that payload to the neutral tree.
//
// The conventions are deliberately the ones the Android and iOS drivers already
// follow, because a selector matches the same way on every platform:
//   - bounds is the Android-style "[l,t][r,b]" string, which spec 02 line 26
//     parses everywhere and line 77 has the web driver emit "for uniform
//     parsing";
//   - an empty attribute value is dropped, because present-but-empty and absent
//     read the same to a person and differently to a matcher;
//   - the booleans are tri-state: a DOM element that has no checked state at
//     all must not report checked=false, or `checked: false` would match every
//     element on the page.

// scriptNode is the wire shape the injected script emits.
type scriptNode struct {
	Attributes map[string]string `json:"attributes"`
	Children   []scriptNode      `json:"children,omitempty"`
}

func parseHierarchy(payload []byte) (device.TreeNode, error) {
	var root scriptNode
	if err := json.Unmarshal(payload, &root); err != nil {
		return device.TreeNode{}, fmt.Errorf("web hierarchy: %w", err)
	}
	if root.Attributes == nil {
		return device.TreeNode{}, fmt.Errorf("web hierarchy: payload carries no root node")
	}
	return convertScriptNode(root), nil
}

func convertScriptNode(source scriptNode) device.TreeNode {
	attributes := make(map[string]string, len(source.Attributes))
	for name, value := range source.Attributes {
		if value == "" {
			continue
		}
		attributes[name] = value
	}
	node := device.TreeNode{
		Attributes: attributes,
		Enabled:    attributeBool(attributes, "enabled"),
		Focused:    attributeBool(attributes, "focused"),
		Checked:    attributeBool(attributes, "checked"),
		Selected:   attributeBool(attributes, "selected"),
	}
	if len(source.Children) != 0 {
		node.Children = make([]device.TreeNode, 0, len(source.Children))
		for _, child := range source.Children {
			node.Children = append(node.Children, convertScriptNode(child))
		}
	}
	return node
}

// attributeBool reads a tri-state boolean: nil when the script did not emit the
// attribute at all, which is how an element without that state stays unknown.
func attributeBool(attributes map[string]string, key string) *bool {
	value, exists := attributes[key]
	if !exists {
		return nil
	}
	parsed := value == "true"
	return &parsed
}
