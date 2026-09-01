package explore

import (
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
)

// iosElementTypeNames maps XCUIElementType codes to their enum names,
// lower-camel as XCUIAutomation spells them. Generated from
// XCUIElementTypes.h of the installed Xcode (83 cases); the
// runner sends the number and a model reading "9" cannot tell a button
// from a cell (75), which is how a live planner took a toolbar button for
// a list entry.
var iosElementTypeNames = map[string]string{
	"0":  "any",
	"1":  "other",
	"2":  "application",
	"3":  "group",
	"4":  "window",
	"5":  "sheet",
	"6":  "drawer",
	"7":  "alert",
	"8":  "dialog",
	"9":  "button",
	"10": "radioButton",
	"11": "radioGroup",
	"12": "checkBox",
	"13": "disclosureTriangle",
	"14": "popUpButton",
	"15": "comboBox",
	"16": "menuButton",
	"17": "toolbarButton",
	"18": "popover",
	"19": "keyboard",
	"20": "key",
	"21": "navigationBar",
	"22": "tabBar",
	"23": "tabGroup",
	"24": "toolbar",
	"25": "statusBar",
	"26": "table",
	"27": "tableRow",
	"28": "tableColumn",
	"29": "outline",
	"30": "outlineRow",
	"31": "browser",
	"32": "collectionView",
	"33": "slider",
	"34": "pageIndicator",
	"35": "progressIndicator",
	"36": "activityIndicator",
	"37": "segmentedControl",
	"38": "picker",
	"39": "pickerWheel",
	"40": "switch",
	"41": "toggle",
	"42": "link",
	"43": "image",
	"44": "icon",
	"45": "searchField",
	"46": "scrollView",
	"47": "scrollBar",
	"48": "staticText",
	"49": "textField",
	"50": "secureTextField",
	"51": "datePicker",
	"52": "textView",
	"53": "menu",
	"54": "menuItem",
	"55": "menuBar",
	"56": "menuBarItem",
	"57": "map",
	"58": "webView",
	"59": "incrementArrow",
	"60": "decrementArrow",
	"61": "timeline",
	"62": "ratingIndicator",
	"63": "valueIndicator",
	"64": "splitGroup",
	"65": "splitter",
	"66": "relevanceIndicator",
	"67": "colorWell",
	"68": "helpTag",
	"69": "matte",
	"70": "dockItem",
	"71": "ruler",
	"72": "rulerMarker",
	"73": "grid",
	"74": "levelIndicator",
	"75": "cell",
	"76": "layoutArea",
	"77": "layoutItem",
	"78": "handle",
	"79": "stepper",
	"80": "tab",
	"81": "touchBar",
	"82": "statusItem",
}

// ElementRole names what kind of control a node is, in the vocabulary a
// model can use: the Android class without its package, the iOS element
// type by name, or the raw type value when neither applies.
func ElementRole(node device.TreeNode) string {
	if role := node.Attributes["class"]; role != "" {
		if dot := strings.LastIndex(role, "."); dot >= 0 {
			return role[dot+1:]
		}
		return role
	}
	if code := node.Attributes["elementType"]; code != "" {
		if name, ok := iosElementTypeNames[code]; ok {
			return name
		}
		return code
	}
	return node.Attributes["type"]
}

// ElementLabel returns the human-visible label of a node across both
// dialects: Android carries it in text (or a hint), iOS in
// accessibilityText or title. A table that reads only the Android keys
// shows every iOS row as "-", which left a live researcher with nothing
// but geometry to name the screen's controls by.
// ControlLabel names a row for a model-facing table. iOS answers with an
// element's accessibility VALUE in text and its NAME in accessibilityText,
// and ElementLabel prefers text -- so a switch read "1" and the month strip of
// a calendar read "No events" thirty-one times, one name between every day on
// screen. Android puts a widget's name in text and has no such split, so no
// Android row moves. Nothing matches on this: selectors, locators, and
// exported flows go on reading ElementLabel.
func ControlLabel(node device.TreeNode) string {
	if node.Attributes["elementType"] == "" {
		return ElementLabel(node)
	}
	for _, key := range []string{"accessibilityText", "label", "name", "title"} {
		// A whitespace-only name names nothing, and taking it would leave a
		// row blank where it had a value to show.
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ElementLabel(node)
}

// RowState says how a row is set, for a model-facing table: whether a control
// with an on/off state is on, and whether the platform marks this row as the
// selected one. Empty for a row with neither, which is nearly all of them --
// iOS reports checked and selected false for every element on screen, and
// marking those would call every button an unselected switch.
//
// The selected mark carries the calendar's own day: of the 125 nodes of a
// Calendar day view captured on iOS 26.2, exactly one is flagged, and two
// sessions filed a defect whose evidence was that no fact said which day the
// screen was showing.
func RowState(node device.TreeNode) string {
	marks := make([]string, 0, 2)
	if IsCheckable(node) && node.Checked != nil {
		if *node.Checked {
			marks = append(marks, "on")
		} else {
			marks = append(marks, "off")
		}
	}
	if node.Selected != nil && *node.Selected {
		marks = append(marks, "selected")
	}
	return strings.Join(marks, " ")
}

func ElementLabel(node device.TreeNode) string {
	for _, key := range []string{"text", "label", "name", "hintText", "accessibilityText", "title"} {
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}
