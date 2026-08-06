package android

import (
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// The agent answers viewHierarchy with the spec 04 §2 XML. This parser turns
// it into the neutral tree every selector runs against; bounds stays the
// Android-style "[l,t][r,b]" string because that is the shape the engine
// parses on every platform (specs/02-device-drivers.md line 26).

const sampleHierarchy = `<?xml version="1.0" encoding="UTF-8"?>
<hierarchy rotation="0">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="com.example.a" content-desc="" checkable="false" checked="false" clickable="false" enabled="true" focusable="false" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" visible-to-user="true" bounds="[0,0][1080,2400]">
    <node NAF="true" index="0" text="Sign in" resource-id="com.example.a:id/sign_in" class="android.widget.Button" package="com.example.a" content-desc="Sign in button" checkable="false" checked="false" clickable="true" enabled="true" focusable="true" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" visible-to-user="true" bounds="[40,2100][1040,2260]"/>
    <node index="1" hintText="Email" text="" resource-id="com.example.a:id/email" class="android.widget.EditText" package="com.example.a" content-desc="" checkable="false" checked="false" clickable="true" enabled="true" focusable="true" focused="true" scrollable="false" long-clickable="true" password="false" selected="false" visible-to-user="true" bounds="[40,400][1040,560]"/>
    <node index="2" text="" resource-id="com.example.a:id/remember" class="android.widget.CheckBox" package="com.example.a" content-desc="" checkable="true" checked="true" clickable="true" enabled="true" focusable="true" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" visible-to-user="true" bounds="[40,600][200,700]"/>
  </node>
</hierarchy>`

func TestParseHierarchyBuildsTheNeutralTree(t *testing.T) {
	t.Parallel()

	root, err := parseHierarchy(sampleHierarchy)
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}
	// The root is the <hierarchy> element itself, so several window roots and
	// an appended toast are all reachable from one tree.
	if got := root.Attributes["rotation"]; got != "0" {
		t.Fatalf("rotation = %q, want 0", got)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root children = %d, want 1", len(root.Children))
	}
	frame := root.Children[0]
	if len(frame.Children) != 3 {
		t.Fatalf("frame children = %d, want 3", len(frame.Children))
	}

	button := frame.Children[0]
	if got := button.Attributes["bounds"]; got != "[40,2100][1040,2260]" {
		t.Fatalf("bounds = %q, want the exact string passed through", got)
	}
	if got := button.Attributes["resource-id"]; got != "com.example.a:id/sign_in" {
		t.Fatalf("resource-id = %q", got)
	}
	// content-desc is what a screen reader announces; the matcher's name for
	// that attribute set is accessibilityText (specs/04 section 5).
	if got := button.Attributes["accessibilityText"]; got != "Sign in button" {
		t.Fatalf("accessibilityText = %q, want the content-desc value", got)
	}
	if got := button.Attributes["content-desc"]; got != "Sign in button" {
		t.Fatalf("content-desc = %q, want it kept verbatim too", got)
	}
	// NAF is only emitted for accessibility-unfriendly nodes; when present it
	// must survive the conversion.
	if got := button.Attributes["NAF"]; got != "true" {
		t.Fatalf("NAF = %q, want true", got)
	}
	if button.Clickable == nil || !*button.Clickable {
		t.Fatal("button clickable = nil or false, want true")
	}
	// A non-checkable node still reports checked=false, and a `checked: false`
	// selector still matches it. That reads like a bug -- it makes the trait
	// match nearly every node on screen, but it preserves the device's raw
	// hierarchy and keeps selector behavior consistent with that hierarchy.
	if button.Checked == nil || *button.Checked {
		t.Fatal("non-checkable button checked = nil or true, want a reported false")
	}

	email := frame.Children[1]
	if got := email.Attributes["hintText"]; got != "Email" {
		t.Fatalf("hintText = %q", got)
	}
	if email.Focused == nil || !*email.Focused {
		t.Fatal("focused EditText did not report focused")
	}
	// An empty text attribute must be dropped: present-but-empty and absent
	// read the same to a person and differently to a matcher (the TEXT trait
	// keys on existence).
	if _, exists := email.Attributes["text"]; exists {
		t.Fatal(`empty text="" survived as an attribute; it must be dropped`)
	}

	checkbox := frame.Children[2]
	if checkbox.Checked == nil || !*checkbox.Checked {
		t.Fatal("checkable checkbox with checked=true did not report Checked")
	}
}

func TestParseHierarchyKeepsTheToastAsItsOwnRootChild(t *testing.T) {
	t.Parallel()

	// spec 04 §2: the toast is appended as a separate node with a minimal
	// attribute set — no enabled, no focused, none of the usual booleans.
	hierarchy := `<hierarchy rotation="0">
  <node index="0" class="android.widget.FrameLayout" enabled="true" visible-to-user="true" bounds="[0,0][1080,2400]"/>
  <node index="0" class="android.widget.Toast" text="Saved" visible-to-user="true" checkable="false" clickable="false" bounds="[200,2000][880,2100]"/>
</hierarchy>`
	root, err := parseHierarchy(hierarchy)
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root children = %d, want the window root and the toast", len(root.Children))
	}
	toast := root.Children[1]
	if got := toast.Attributes["text"]; got != "Saved" {
		t.Fatalf("toast text = %q", got)
	}
	if toast.Enabled != nil {
		t.Fatal("the toast has no enabled attribute; Enabled must stay nil, not become false")
	}
	if toast.Clickable == nil || *toast.Clickable {
		t.Fatal("toast clickable = nil or true, want false from its own attribute")
	}
}

func TestParseHierarchyKeepsInvisibleNodes(t *testing.T) {
	t.Parallel()

	// The agent already skips invisible children EXCEPT inside a WebView
	// (spec 04 §2), so any invisible node that reaches the host is one the
	// agent wanted kept. Dropping it here would undo the WebView rule.
	hierarchy := `<hierarchy rotation="0">
  <node class="android.webkit.WebView" visible-to-user="true" bounds="[0,0][1080,2400]">
    <node class="android.view.View" text="offscreen" visible-to-user="false" bounds="[0,2400][1080,2500]"/>
  </node>
</hierarchy>`
	root, err := parseHierarchy(hierarchy)
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}
	web := root.Children[0]
	if len(web.Children) != 1 {
		t.Fatal("the invisible WebView child was dropped; the agent emitted it on purpose")
	}
	if got := web.Children[0].Attributes["visible-to-user"]; got != "false" {
		t.Fatalf("visible-to-user = %q, want false carried as data", got)
	}
}

func TestParseHierarchyRefusesMalformedInput(t *testing.T) {
	t.Parallel()

	for name, input := range map[string]string{
		"empty":          "",
		"truncated":      "<hierarchy rotation=\"0\"><node bounds=\"[0,0][1,1]\">",
		"not the schema": "<html><body/></html>",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseHierarchy(input); err == nil {
				t.Fatalf("parseHierarchy(%q) accepted malformed input", input)
			} else if !strings.Contains(err.Error(), "hierarchy") {
				t.Fatalf("error = %q, want it to name the hierarchy", err)
			}
		})
	}
}

func TestParseHierarchyReportsCheckedForEveryNode(t *testing.T) {
	t.Parallel()

	// Every Android node carries checked="false" and every one of them reports
	// it, so a `checked: false` selector matches nearly every node on screen.
	// Preserve that raw state even when checkable=false; otherwise selectors
	// silently see a different hierarchy from the one Android emitted.
	hierarchy := `<hierarchy rotation="0">
  <node class="android.widget.Button" checkable="false" checked="false" bounds="[0,0][10,10]"/>
  <node class="android.widget.Switch" checkable="true" checked="false" bounds="[0,10][10,20]"/>
</hierarchy>`
	root, err := parseHierarchy(hierarchy)
	if err != nil {
		t.Fatalf("parseHierarchy() error = %v", err)
	}
	if plain := root.Children[0].Checked; plain == nil || *plain {
		t.Fatal("uncheckable node Checked = nil or true, want a reported false")
	}
	toggle := root.Children[1].Checked
	if toggle == nil || *toggle {
		t.Fatal("checkable switch with checked=false must report *false")
	}
	var _ device.TreeNode = root
}
