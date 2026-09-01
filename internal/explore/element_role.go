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
// ControlLabel names a row for a model-facing table. A switch is the reason
// it is not ElementLabel: iOS answers one with its state in the value,
// ElementLabel prefers text, and the row came out labelled "1" with the
// control's name only on the row above it. Everything else keeps the label it
// always had, and nothing matches on this -- selectors and exported flows go
// on reading ElementLabel.
func ControlLabel(node device.TreeNode) string {
	if IsCheckable(node) {
		for _, key := range []string{"accessibilityText", "label", "name", "title"} {
			if value := strings.TrimSpace(node.Attributes[key]); value != "" {
				return value
			}
		}
	}
	return ElementLabel(node)
}

// ControlState answers how a row with an on/off state is set, and says
// whether it has one at all. A row without one must say nothing: iOS reports
// checked false for every element on screen, and marking those would call
// every control a switch that is off.
func ControlState(node device.TreeNode) (string, bool) {
	if !IsCheckable(node) || node.Checked == nil {
		return "", false
	}
	if *node.Checked {
		return "on", true
	}
	return "off", true
}

func ElementLabel(node device.TreeNode) string {
	for _, key := range []string{"text", "label", "name", "hintText", "accessibilityText", "title"} {
		if value := strings.TrimSpace(node.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}
