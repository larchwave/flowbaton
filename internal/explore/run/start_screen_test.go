package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
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

// Reach asks the worker model to bring the app to a screen named by key,
// and the key the crew hands it is a ScreenSignature.Key():
// "settings-apple-account-c6c0dfad". A model cannot navigate to a digest.
// The labels in front of it are the screen's own salient labels, which is
// the one readable thing the key carries, so the prompt spells them out.
func TestReachTellsTheModelWhatTheScreenLooksLike(t *testing.T) {
	t.Parallel()

	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home, home, home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "finish", `{"status":"passed","summary":"done"}`),
	}}
	navigator := newNavigator(driver, observer, worker, nil)
	_, _, _ = navigator.Reach(context.Background(), "settings-apple-account-c6c0dfad")

	if len(worker.requests) == 0 {
		t.Fatal("the worker was never asked")
	}
	joined := ""
	for _, message := range worker.requests[0].Messages {
		joined += message.Text + "\n"
	}
	if !strings.Contains(joined, "settings apple account") {
		t.Errorf("the prompt does not say what the screen shows:\n%s", joined)
	}
}

// mmx69, live: four scenarios asked to reach "search-add-4d3ffed3" and all
// four spent the whole eight-turn budget and gave up, on a screen the app
// was almost certainly already showing -- every flow the session exported
// carries that same key in its header.
//
// The digest covers the WHOLE tree, so a different day highlighted or a
// different scroll offset renames the screen. Exact equality can therefore
// only ever match a screen nothing has touched. The labels are what Reach
// means by "looks like the named screen", so the labels decide.
func TestScreenMatchesAcceptsAKeyWithTheSameLabels(t *testing.T) {
	t.Parallel()

	root := device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Search",
				"bounds": "[0,0][200,60]"}},
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "Add",
				"bounds": "[200,0][402,60]"}},
			// The month grid below the toolbar, which changes every day.
			{Attributes: map[string]string{"elementType": "48", "text": "September 2026",
				"bounds": "[0,60][402,120]"}},
		},
	}
	state := makeState("app", root)
	if key := state.Signature.Key(); key == "search-add-4d3ffed3" {
		t.Fatalf("the fixture accidentally has the live digest %q", key)
	}
	if !screenMatches(state, "search-add-4d3ffed3") {
		t.Errorf("screenMatches refused a screen whose labels are the key's: state key %q",
			state.Signature.Key())
	}
	if screenMatches(state, "inbox-close-4d3ffed3") {
		t.Error("a key with different labels matched")
	}
}
