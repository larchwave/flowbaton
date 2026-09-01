package explore

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

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

// iosCheckableTypes are the XCUIElementType codes with a checked state:
// checkbox=12, switch=40, toggle=41, the same three internal/ios reads a
// value for. Every other type has no such state, which is not the same as
// false -- iOS reports checked false for every row on screen.
var iosCheckableTypes = map[string]bool{"12": true, "40": true, "41": true}

// androidCheckableClasses are the widget class fragments with a checked
// state. Substrings on purpose: SwitchCompat and AppCompatCheckBox carry the
// plain names inside their own.
var androidCheckableClasses = []string{"Switch", "CheckBox", "ToggleButton", "CheckedTextView"}

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

// iosNavigationBarType is the XCUIElementType of a navigation bar (21), and
// iosStaticTextType that of static text (48). A screen carries one bar, and
// the label a person reads as the screen's name is the static text among its
// buttons.
const (
	iosNavigationBarType = "21"
	iosStaticTextType    = "48"
)

// navigationTitle reports whether this node is the title of the navigation
// bar it sits in. Salient labels are otherwise taken in document order,
// which means the bar's leftmost BUTTON: a screen titled "Completed" was
// named "Back, More", and one titled "Lists" was named "Edit, Lists".
//
// Captured on iOS 26.2, 2026-08-31, six apps: every screen with a bar had
// exactly one, and where that bar held a static text it was the title --
// reminders Back/More/Completed, contacts Edit/Lists/Add List and
// Lists/Contacts, shortcuts Library/Edit/add/All Shortcuts. Three screens
// had none: Calendar titles itself with a button, a Settings modal carries
// only Close, and a locked Passwords screen has no bar at all. Those keep
// the labels they had, which is why this promotes a title and never
// requires one.
func navigationTitle(node device.TreeNode, insideBar bool) bool {
	return insideBar && node.Attributes["elementType"] == iosStaticTextType
}

// withTitleFirst puts the screen's own name in front of the labels taken in
// document order, without growing the list: a title that is already among
// them moves up rather than repeating.
func withTitleFirst(title string, salient []string) []string {
	if title == "" {
		return salient
	}
	ordered := []string{title}
	for _, label := range salient {
		if len(ordered) >= salientLabelCount {
			break
		}
		if sameName(label, title) {
			continue
		}
		ordered = append(ordered, label)
	}
	return ordered
}

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

// iosTabBarType and iosToolbarType are the XCUIElementTypes of a tab bar
// (22) and a toolbar (24). Both carry their own role as their label.
const (
	iosTabBarType  = "22"
	iosToolbarType = "24"
)

// namesAnIcon reports whether this label is a symbol identifier rather than
// words. iOS hands an icon the name of its glyph when the app sets no
// label, and that name reached a screen: session mmx57 mapped a calendar
// screen as august-calendar-day-timeline-leading-26d1eb3d from a button
// labelled "calendar.day.timeline.leading" whose id is the honest
// "toggle-day-list-view". Three more on iOS 26.2, 2026-08-31:
// "doc.viewfinder.fill" and "deskclock.fill" in shortcuts,
// "photo.fill.on.rectangle.fill" in photos.
//
// The shape is the test: lowercase words joined by dots, no spaces. A
// filename typed by a person is the near miss, and the cost there is small
// -- the screen takes its next label, which is another row of the same list
// -- while the cost of keeping these is a screen named after a glyph.
func namesAnIcon(label string) bool {
	if !strings.Contains(label, ".") {
		return false
	}
	for _, segment := range strings.Split(label, ".") {
		if segment == "" {
			return false
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return false
			}
		}
	}
	return true
}

// namesItsOwnRole reports whether this node's label is the name of the
// container rather than of anything on the screen. A tab bar is labelled
// "Tab Bar" and a toolbar "Toolbar", so a screen took the word Toolbar for a
// name: session mmx56 keyed the contacts search screen as
// toolbar-search-32d1546f. Captured on iOS 26.2, 2026-08-31: shortcuts and
// photos hold a tab bar labelled "Tab Bar", calendar and contacts a toolbar
// labelled "Toolbar", and all seven navigation bars carry no label at all,
// which is why that type needs no rule here.
//
// The children keep their labels: what sits IN a toolbar is the app's, and
// the bar itself stays in the digest as structure.
func namesItsOwnRole(node device.TreeNode) bool {
	switch node.Attributes["elementType"] {
	case iosTabBarType, iosToolbarType:
		return true
	}
	return false
}

// coversTheScreen reports whether this node's frame is the whole viewport.
// A label on such a node says what the whole screen does, never what the
// screen is: iOS gives the dimming view behind a sheet the label "Activate
// to dismiss", and because that view sits above the sheet in document order
// it took the first label slot -- the contacts search screen with a sheet
// open keyed as activate-to-dismiss-search, 2026-08-31.
//
// The viewport comes from the application element, so the rule is quiet on
// a platform that sends none -- the same reason isChrome names only iOS. The
// tree root is not used for it: a root is as large as whatever it holds, and
// on a screen that is one labelled view that would drop the only name it
// has. Eight iOS apps carried exactly two labels the size of the viewport:
// the application element and that one dimming view.
func coversTheScreen(node device.TreeNode, viewport device.Bounds, known bool) bool {
	if !known {
		return false
	}
	bounds, ok := ElementBounds(node)
	return ok && bounds == viewport
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

// IsCheckable reports whether this element has an on/off state at all.
// Android names the widget in class; iOS carries a numeric element type.
func IsCheckable(node device.TreeNode) bool {
	if iosCheckableTypes[node.Attributes["elementType"]] {
		return true
	}
	class := node.Attributes["class"]
	for _, fragment := range androidCheckableClasses {
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
	// A switch is the control, not a container for one: iOS frames the whole
	// settings row as the switch, nests the label and the knob inside it, and
	// carries the name, the identifier, and the state only on the outer node.
	// The children-mean-container rule below dropped it and kept the unnamed
	// knob, so the table offered `switch "0"` and no row said which setting
	// that was. The inner copy is the same control, listed once.
	if IsCheckable(node) {
		return element.Parent == nil || !IsCheckable(element.Parent.Node)
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

// salientLabelCount is how many labels name a screen; salientLabelLimit is
// the longest one worth using as a name rather than as content, counted in
// characters. Counting bytes made the limit shrink with the script: a
// Russian title of 29 characters weighs 55 bytes, a Chinese one reaches 40
// bytes at 14 characters, and both were dropped as content.
const (
	salientLabelCount = 2
	salientLabelLimit = 40
)

// readsAsAName reports whether a label reads as a name at all: it must say
// something once Key has rendered it (a label of symbols alone lands there
// as a blank), it must be words rather than a glyph identifier, and it must
// be short enough to be a name rather than content.
func readsAsAName(label string) bool {
	return slugify(label) != "" && !namesAnIcon(label) &&
		utf8.RuneCountInString(label) <= salientLabelLimit
}

// ComputeSignature derives the screen signature for a tree: a digest over
// normalized structure and text, plus a few salient labels. Digit runs
// collapse so counters and timestamps do not split one logical screen
// into many signatures.
func ComputeSignature(appID string, root device.TreeNode) ScreenSignature {
	parts := []string{}
	salient := []string{}
	title := ""
	viewport := device.Bounds{}
	haveViewport := false
	// canNameTheScreen reports whether this node's label may stand as the
	// screen's name: the platform's own furniture may not, and neither may a
	// second spelling of a name already taken.
	canNameTheScreen := func(node device.TreeNode, label string) bool {
		if namesTheApplication(node) || namesItsOwnRole(node) ||
			coversTheScreen(node, viewport, haveViewport) {
			return false
		}
		// A container and the label inside it carry the same name --
		// Android's ViewGroup product_lockup around TextView product_name,
		// iOS's cell around its own text, an image "Search" beside a keyboard
		// key "search" -- so taking both spends the whole list on one word
		// and names the screen "contacts-contacts".
		return !slices.ContainsFunc(salient, func(taken string) bool {
			return sameName(taken, label)
		})
	}
	var walk func(node device.TreeNode, insideBar bool)
	walk = func(node device.TreeNode, insideBar bool) {
		// Chrome is skipped here for the same reason as in FlattenScreen, and
		// twice over: it would name the screen after the carrier and split one
		// screen into many as reception changes.
		if isChrome(node) {
			return
		}
		insideBar = insideBar || node.Attributes["elementType"] == iosNavigationBarType
		if !haveViewport && namesTheApplication(node) {
			viewport, haveViewport = ElementBounds(node)
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
			trimmed := strings.TrimSpace(label)
			if readsAsAName(trimmed) {
				if title == "" && navigationTitle(node, insideBar) {
					title = trimmed
				}
				if len(salient) < salientLabelCount && canNameTheScreen(node, trimmed) {
					salient = append(salient, trimmed)
				}
			}
		}
		for _, child := range node.Children {
			walk(child, insideBar)
		}
	}
	walk(root, false)
	digest := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return ScreenSignature{
		AppID:      appID,
		Salient:    withTitleFirst(title, salient),
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
