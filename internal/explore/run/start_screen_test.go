package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// Reach is given a screen key, and the only key the crew has for a scenario
// is Scenario.StartScreen -- a ScreenSignature.Key(), which is the salient
// labels slugified plus eight digest characters. screenMatches read that
// whole string against one salient label at a time, so no signature key
// could ever match: "search-add-4d3ffed3" is not a substring of "Search".
// Reach would have spent its whole turn budget on a screen it was already
// standing on. Every test the navigator had used a plain word for a key.
func TestScreenMatchesAcceptsASignatureKey(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Search",
				"bounds": "[0,0][200,60]"}},
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Add",
				"bounds": "[200,0][402,60]"}},
		},
	}
	state := makeState("app", root)
	key := state.Signature.Key()
	if key == "" {
		t.Fatal("no key")
	}
	if !screenMatches(state, key) {
		t.Errorf("screenMatches(state, %q) = false, want true for the state's own key", key)
	}
	if screenMatches(state, "some-other-screen-00000000") {
		t.Error("a different key matched")
	}
}

// A plain word still matches a salient label: that is what every stored
// recipe and every navigator test uses.
func TestScreenMatchesStillAcceptsAPlainWord(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Settings",
				"bounds": "[0,0][402,60]"}},
		},
	}
	if !screenMatches(makeState("app", root), "settings") {
		t.Error("a plain word no longer matches a salient label")
	}
}
