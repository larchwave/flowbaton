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

// iosSecureTextFieldType is the XCUIElementType code of secureTextField
// (50), the field that masks what is typed into it.
const iosSecureTextFieldType = "50"

// iosStatusBarType is the XCUIElementType of the status bar (25). It is a
// sibling window of the app, not part of it.
const iosStatusBarType = "25"

// iosApplicationType is the XCUIElementType of the application element (2).
// It wraps the whole app and carries the app's own name.
const iosApplicationType = "2"

// namesTheApplication reports whether this node is the app's own element
// rather than anything on the screen. Its label is the application name and
// its frame is the whole viewport, so it names every screen of an app
// identically -- and ScreenSignature.AppID already carries that identity, so
// the label adds nothing. Captured on Contacts, iOS 26.2: two different
// screens both opened with elementType 2 at [0,0][402,874] labelled
// "Contacts", and every screen the session recorded keyed the same.
//
// The node itself stays in the digest: it is real structure, and skipping it
// there would change the identity of every screen already recorded.
//
// Only iOS is recognized, for the same reason as isChrome: the Android agent
// has not been seen emitting an application-level labelled wrapper, so
// nothing is skipped there until a device shows one.
func namesTheApplication(node device.TreeNode) bool {
	return node.Attributes["elementType"] == iosApplicationType
}

// isChrome reports whether this node roots operating-system furniture rather
// than the app under test. Its clock, carrier, and battery labels otherwise
// read as app content: live sessions planned Wi-Fi and signal scenarios in a
// reminders app off exactly those rows.
//
// Only the iOS status bar is recognized. The Android agent has not been seen
// emitting system-user-interface windows in an app dump, so nothing is
// skipped there until a device shows one.
func isChrome(node device.TreeNode) bool {
	return node.Attributes["elementType"] == iosStatusBarType
}

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

// IsSecureTextInput reports whether this element masks what is typed into
// it: an iOS secure text field or an Android password input. What lands in
// such a field is a secret by declaration and must never reach recordings.
func IsSecureTextInput(node device.TreeNode) bool {
	if node.Attributes["elementType"] == iosSecureTextFieldType {
		return true
	}
	return node.Attributes["password"] == "true" && IsTextInput(node)
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
		if isChrome(element.Node) {
			return
		}
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
	// Nothing can be tapped, read, or typed into where there is no area to
	// touch, so listing it only offers the agent a row that cannot work.
	if offscreen(node) {
		return false
	}
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

// offscreen reports whether the tree measured this node and gave it no area.
// A node with no bounds attribute is not offscreen -- it is unmeasured, and
// dropping those would take real containers with them.
func offscreen(node device.TreeNode) bool {
	bounds, ok := parseBounds(node)
	return ok && bounds.Width == 0 && bounds.Height == 0
}

// ElementBounds returns bounds a caller can act on: parsed, and enclosing a
// real area. A box of zero width and height parses cleanly and centers on the
// screen corner, so reporting it as usable sent every consumer somewhere the
// element is not -- a tap to (0,0), an exported point locator aimed there.
// Measured on iOS 26.2: 12 of 18 rows on one captured screen were such boxes.
func ElementBounds(node device.TreeNode) (device.Bounds, bool) {
	bounds, ok := parseBounds(node)
	if !ok || (bounds.Width == 0 && bounds.Height == 0) {
		return device.Bounds{}, false
	}
	return bounds, true
}

func parseBounds(node device.TreeNode) (device.Bounds, bool) {
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
		// Chrome is skipped here for the same reason as in FlattenScreen, and
		// twice over: it would name the screen after the carrier and split one
		// screen into many as reception changes.
		if isChrome(node) {
			return
		}
		// A node the accessibility layer gives no area is not on the screen,
		// and two of them arrive in whichever order it happened to walk them:
		// measured on iOS 26.2, two captures of one untouched screen agreed on
		// 267 of 269 nodes and swapped a pair of zero-bounds siblings. Their
		// order reaches this digest, so leaving them in lets an untouched
		// screen report two signatures -- and a step that moved nothing then
		// records as one that did. Children still walk: an invisible parent
		// does not make its children invisible.
		if !offscreen(node) {
			role := signatureRole(node)
			label := signatureLabel(node)
			parts = append(parts, role+"|"+signatureID(node)+"|"+normalizeText(label))
			if len(salient) < 2 && !namesTheApplication(node) {
				trimmed := strings.TrimSpace(label)
				if trimmed != "" && len(trimmed) <= 40 {
					salient = append(salient, trimmed)
				}
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

// The two platforms spell the same three facts differently: Android sends
// class, resource-id, and text; iOS sends elementType, id, and
// accessibilityText. Reading one dialect only makes every node of the other
// platform contribute the same empty triple, which collapses the digest onto
// tree shape and leaves the salient labels empty.

func signatureRole(node device.TreeNode) string {
	return firstAttr(node, "class", "type", "elementType")
}

func signatureID(node device.TreeNode) string {
	return firstAttr(node, "resource-id", "id")
}

func signatureLabel(node device.TreeNode) string {
	return firstAttr(node, "text", "label", "name", "accessibilityText", "title")
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
