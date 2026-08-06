package web

import (
	"strings"
	"testing"
)

// The injected script is the whole hierarchy source on web, so its shape is
// pinned here: spec 02-device-drivers.md §4 names the skipped tags, the
// <option> synthetic offset, the text precedence, and the bounds format. A
// silent edit to any of those changes what every selector matches.
func TestContentDescriptionScriptHonorsSpecShape(t *testing.T) {
	t.Parallel()

	script := contentDescriptionScript()

	// Spec §4: skip noscript/script/br/img/svg/g/path/style.
	for _, tag := range []string{"noscript", "script", "br", "img", "svg", "g", "path", "style"} {
		if !strings.Contains(script, "'"+tag+"'") {
			t.Errorf("script does not skip %q", tag)
		}
	}
	// Spec §4: <option> synthetic nodes live at a +100000 offset space.
	if !strings.Contains(script, "100000") {
		t.Error("script lacks the <option> synthetic offset")
	}
	// Spec §4: bounds via getBoundingClientRect, emitted Android-style.
	if !strings.Contains(script, "getBoundingClientRect") {
		t.Error("script does not measure bounds with getBoundingClientRect")
	}
	// Spec §4: text precedence value|placeholder|ariaLabel|selectedOptions|text.
	for _, source := range []string{"value", "placeholder", "aria-label", "selectedOptions"} {
		if !strings.Contains(script, source) {
			t.Errorf("script ignores the %q text source", source)
		}
	}
	// The driver calls the entry point by name; the injection defines it.
	if !strings.Contains(script, "getContentDescription") {
		t.Error("script does not define the getContentDescription entry point")
	}
	if !strings.Contains(script, "window.flowbaton") {
		t.Error("script does not install the FlowBaton page namespace")
	}
	retiredNamespace := "window." + strings.Join([]string{"mae", "stro"}, "")
	if strings.Contains(script, retiredNamespace) {
		t.Error("script still installs the retired page namespace")
	}
}

// The script is evaluated as one expression whose value is the JSON hierarchy,
// so it must be self-contained: a statement-only body would evaluate to
// undefined and the driver would see an empty page on every settle.
func TestContentDescriptionScriptIsASelfContainedExpression(t *testing.T) {
	t.Parallel()

	script := contentDescriptionScript()
	trimmed := strings.TrimSpace(script)
	if !strings.HasPrefix(trimmed, "(") {
		t.Fatalf("script is not an expression: starts with %.30q", trimmed)
	}
	if !strings.HasSuffix(trimmed, ")()") && !strings.HasSuffix(trimmed, "})()") {
		t.Fatalf("script is not immediately invoked: ends with %.30q", trimmed[len(trimmed)-30:])
	}
}
