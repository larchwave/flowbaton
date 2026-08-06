package flow

import (
	"strings"
	"testing"
)

// scrollUntilVisible.scrollDuration and openLink.appId are driver-level
// values, not authored fields. The authored contract exposes speed for
// scrollUntilVisible and routes app identity from flow configuration.

func refusedField(t *testing.T, yaml, field string) {
	t.Helper()
	_, err := ParseBytes("/leaked.yaml", []byte("appId: com.example.app\n---\n"+yaml+"\n"))
	if err == nil {
		t.Fatalf("parse accepted unsupported field %s", field)
	}
	if !strings.Contains(err.Error(), field) {
		t.Fatalf("error = %q, want it to name %s", err.Error(), field)
	}
}

func TestScrollUntilVisibleRefusesScrollDuration(t *testing.T) {
	t.Parallel()

	// Both scalar spellings remain unsupported.
	refusedField(t, "- scrollUntilVisible: {element: {text: Ready}, scrollDuration: '40'}", "scrollDuration")
	refusedField(t, "- scrollUntilVisible: {element: {text: Ready}, scrollDuration: 40}", "scrollDuration")
}

func TestOpenLinkRefusesAppID(t *testing.T) {
	t.Parallel()

	refusedField(t, "- openLink: {link: 'https://example.invalid', appId: com.example.app}", "appId")
}

// Supported authored fields remain available after driver-level fields are
// rejected.
func TestScrollUntilVisibleKeepsSupportedFields(t *testing.T) {
	t.Parallel()

	const yaml = "- scrollUntilVisible: {element: {text: Ready}, direction: DOWN, speed: 40, " +
		"visibilityPercentage: 100, timeout: '20000', waitToSettleTimeoutMs: 30000, centerElement: true}"
	if _, err := ParseBytes("/kept.yaml",
		[]byte("appId: com.example.app\n---\n"+yaml+"\n")); err != nil {
		t.Fatalf("parse rejected supported fields: %v", err)
	}
}

func TestOpenLinkKeepsSupportedFields(t *testing.T) {
	t.Parallel()

	for _, yaml := range []string{
		"- openLink: 'https://example.invalid'",
		"- openLink: {link: 'https://example.invalid'}",
		"- openLink: {link: 'https://example.invalid', autoVerify: true}",
		"- openLink: {link: 'https://example.invalid', browser: true}",
	} {
		if _, err := ParseBytes("/kept.yaml",
			[]byte("appId: com.example.app\n---\n"+yaml+"\n")); err != nil {
			t.Fatalf("parse rejected supported form %q: %v", yaml, err)
		}
	}
}

// addMedia accepts a non-empty list only; a scalar path is invalid.
func TestAddMediaRefusesTheScalarForm(t *testing.T) {
	t.Parallel()

	if _, err := ParseBytes("/leaked.yaml",
		[]byte("appId: com.example.app\n---\n- addMedia: media/a.png\n")); err == nil {
		t.Fatal("parse accepted unsupported scalar form")
	}
}

func TestAddMediaKeepsTheListForm(t *testing.T) {
	t.Parallel()

	if _, err := ParseBytes("/kept.yaml",
		[]byte("appId: com.example.app\n---\n- addMedia: [media/a.png, media/b.mp4]\n")); err != nil {
		t.Fatalf("parse rejected supported list form: %v", err)
	}
}
