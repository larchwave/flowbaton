package explore

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

// interestingAttrs are the attribute names whose presence makes a leaf
// node worth listing for agents.
var interestingAttrs = []string{"text", "label", "name", "resource-id", "id", "hintText", "accessibilityText"}

// iosTextInputTypes are the XCUIElementType codes that accept typed text:
// searchField 45, textField 49, secureTextField 50, textView 52. Static text
// is 48 and accepts nothing.
var iosTextInputTypes = map[string]bool{"45": true, "49": true, "50": true, "52": true}

// androidTextInputClasses are the widget class fragments that accept typed
// text. TextView alone is a label, so only its editable descendants count.
var androidTextInputClasses = []string{"EditText", "AutoCompleteTextView"}

// IsTextInput reports whether typed text can land in this element once it
// holds keyboard focus. Android names the widget in class; iOS carries a
// numeric element type.
func IsTextInput(node device.TreeNode) bool {
	if iosTextInputTypes[node.Attributes["elementType"]] {
		return true
	}
	class := node.Attributes["class"]
	for _, fragment := range androidTextInputClasses {
		if strings.Contains(class, fragment) {
			return true
		}
	}
	return false
}

// FlattenScreen lists the elements of a screen tree that agents interact
// with, assigning each a stable EIDX in document order. The same tree
// always yields the same indexes, so research maps and tester tools agree
// on element identity within one observation.
func FlattenScreen(root device.TreeNode) ([]FlatElement, error) {
	tree, err := hierarchy.New(root)
	if err != nil {
		return nil, err
	}
	flat := []FlatElement{}
	var walk func(element *hierarchy.Element, path string, depth int)
	walk = func(element *hierarchy.Element, path string, depth int) {
		if isInteresting(element) {
			flat = append(flat, FlatElement{
				EIDX:  len(flat),
				Node:  element.Node,
				Path:  path,
				Depth: depth,
			})
		}
		for index, child := range element.Children {
			childPath := strconv.Itoa(index)
			if path != "" {
				childPath = path + "/" + childPath
			}
			walk(child, childPath, depth+1)
		}
	}
	walk(tree, "", 0)
	return flat, nil
}

func isInteresting(element *hierarchy.Element) bool {
	node := element.Node
	if node.Clickable != nil && *node.Clickable {
		return true
	}
	// An empty field carries none of the interesting attributes, so listing
	// it takes its own rule -- otherwise no agent can tap it to type.
	if IsTextInput(node) {
		return true
	}
	if len(element.Children) > 0 {
		return false
	}
	for _, key := range interestingAttrs {
		if strings.TrimSpace(node.Attributes[key]) != "" {
			return true
		}
	}
	return false
}

// ElementBounds parses the node bounds when present.
func ElementBounds(node device.TreeNode) (device.Bounds, bool) {
	raw := node.Attributes["bounds"]
	if raw == "" {
		return device.Bounds{}, false
	}
	bounds, err := hierarchy.ParseBounds(raw)
	if err != nil {
		return device.Bounds{}, false
	}
	return bounds, true
}

// ComputeSignature derives the screen signature for a tree: a digest over
// normalized structure and text, plus a few salient labels. Digit runs
// collapse so counters and timestamps do not split one logical screen
// into many signatures.
func ComputeSignature(appID string, root device.TreeNode) ScreenSignature {
	parts := []string{}
	salient := []string{}
	var walk func(node device.TreeNode)
	walk = func(node device.TreeNode) {
		role := node.Attributes["class"]
		if role == "" {
			role = node.Attributes["type"]
		}
		label := firstAttr(node, "text", "label", "name")
		parts = append(parts, role+"|"+node.Attributes["resource-id"]+"|"+normalizeText(label))
		if len(salient) < 2 {
			trimmed := strings.TrimSpace(label)
			if trimmed != "" && len(trimmed) <= 40 {
				salient = append(salient, trimmed)
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return ScreenSignature{
		AppID:      appID,
		Salient:    salient,
		TreeDigest: hex.EncodeToString(digest[:8]),
	}
}

func firstAttr(node device.TreeNode, keys ...string) string {
	for _, key := range keys {
		if value := node.Attributes[key]; value != "" {
			return value
		}
	}
	return ""
}

// normalizeText collapses digit runs and whitespace so volatile values
// (counts, clocks) do not change a screen's identity.
func normalizeText(value string) string {
	out := make([]rune, 0, len(value))
	lastDigit := false
	lastSpace := false
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			if !lastDigit {
				out = append(out, '#')
			}
			lastDigit = true
			lastSpace = false
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if !lastSpace && len(out) > 0 {
				out = append(out, ' ')
			}
			lastSpace = true
			lastDigit = false
		default:
			out = append(out, r)
			lastDigit = false
			lastSpace = false
		}
	}
	return strings.TrimSpace(string(out))
}
