package web

import (
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// The injected script (spec 02 §4) emits one JSON node per kept DOM element.
// Conversion to the neutral tree must follow the same conventions the Android
// and iOS drivers already use, because selectors match the same way on every
// platform: bounds stays the Android-style "[l,t][r,b]" string, empty values
// are dropped, and the booleans are tri-state so an absent attribute never
// reads as false.
func TestParseHierarchyBuildsNeutralTree(t *testing.T) {
	t.Parallel()

	const payload = `{
	  "attributes": {
	    "tagName": "body",
	    "bounds": "[0,0][800,600]",
	    "css": "body"
	  },
	  "children": [
	    {
	      "attributes": {
	        "tagName": "button",
	        "text": "Sign in",
	        "accessibilityText": "Sign in",
	        "resource-id": "login",
	        "bounds": "[10,20][110,60]",
	        "css": "#login",
	        "enabled": "true",
	        "focused": "false"
	      }
	    },
	    {
	      "attributes": {
	        "tagName": "input",
	        "hintText": "Email",
	        "bounds": "[10,80][310,120]",
	        "css": "input[name=email]",
	        "enabled": "true",
	        "checked": "false",
	        "text": ""
	      }
	    }
	  ]
	}`

	root, err := parseHierarchy([]byte(payload))
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}

	yes, no := true, false
	want := device.TreeNode{
		Attributes: map[string]string{"tagName": "body", "bounds": "[0,0][800,600]", "css": "body"},
		Children: []device.TreeNode{
			{
				Attributes: map[string]string{
					"tagName": "button", "text": "Sign in", "accessibilityText": "Sign in",
					"resource-id": "login", "bounds": "[10,20][110,60]", "css": "#login",
					"enabled": "true", "focused": "false",
				},
				Enabled: &yes,
				Focused: &no,
			},
			{
				Attributes: map[string]string{
					"tagName": "input", "hintText": "Email", "bounds": "[10,80][310,120]",
					"css": "input[name=email]", "enabled": "true", "checked": "false",
				},
				Enabled: &yes,
				Checked: &no,
			},
		},
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("parseHierarchy() =\n%#v\nwant\n%#v", root, want)
	}
}

// An empty attribute value must be dropped rather than stored: present-but-empty
// and absent read the same to a person and differently to a matcher (the same
// rule the Android converter follows).
func TestParseHierarchyDropsEmptyAttributes(t *testing.T) {
	t.Parallel()

	root, err := parseHierarchy([]byte(`{"attributes":{"tagName":"div","text":"","css":"div"}}`))
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}
	if _, exists := root.Attributes["text"]; exists {
		t.Fatalf("empty text was kept: %#v", root.Attributes)
	}
	if root.Attributes["tagName"] != "div" {
		t.Fatalf("attributes = %#v", root.Attributes)
	}
	// No boolean attribute was emitted, so every state stays unknown.
	if root.Enabled != nil || root.Focused != nil || root.Checked != nil || root.Selected != nil {
		t.Fatalf("absent booleans became known: %#v", root)
	}
}

func TestParseHierarchyRejectsMalformedPayload(t *testing.T) {
	t.Parallel()

	for _, payload := range []string{"", "not json", "[]", "null"} {
		if _, err := parseHierarchy([]byte(payload)); err == nil {
			t.Errorf("parseHierarchy(%q) error = nil, want a failure", payload)
		}
	}
}
