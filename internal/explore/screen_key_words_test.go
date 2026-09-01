package explore

import "testing"

// The crew asks whether a scenario's start screen is the one in front, and
// the navigator asks whether it has arrived. Both hold a Key() and both must
// answer the same, or the crew sends a reach that the navigator ends on its
// first check -- after a kill, a launch and an observation the crew had just
// paid for.
func TestASignatureKnowsItsOwnKeyByName(t *testing.T) {
	t.Parallel()

	signature := ScreenSignature{AppID: "app", Salient: []string{"Search", "Add"}, TreeDigest: "4d3ffed33b89e74c"}
	if key := signature.Key(); !signature.NamesTheSameScreen(key) {
		t.Errorf("a signature did not recognise its own key %q", key)
	}
	// The digest covers the whole tree, so the same screen showing a
	// different month is a different key with the same labels.
	if !signature.NamesTheSameScreen("search-add-cbc05d8a") {
		t.Error("a key with the same labels was refused")
	}
	if signature.NamesTheSameScreen("inbox-close-4d3ffed3") {
		t.Error("a key with different labels was accepted")
	}
	if signature.NamesTheSameScreen("") {
		t.Error("an empty key was accepted")
	}
	// A key that is only a digest names one screen exactly and nothing else.
	bare := ScreenSignature{AppID: "app", TreeDigest: "4d3ffed33b89e74c"}
	if !bare.NamesTheSameScreen(bare.Key()) {
		t.Error("a digest-only key did not recognise itself")
	}
	if bare.NamesTheSameScreen("cbc05d8a") {
		t.Error("a digest-only key matched a different digest")
	}
}

func TestScreenKeyWordsStripsTheDigest(t *testing.T) {
	t.Parallel()

	for key, want := range map[string]string{
		"settings-apple-account-c6c0dfad": "settings apple account",
		"search-add-4d3ffed3":             "search add",
		"4d3ffed3":                        "",
		"":                                "",
		"settings":                        "",
		"apple-account":                   "apple account",
	} {
		if got := ScreenKeyWords(key); got != want {
			t.Errorf("ScreenKeyWords(%q) = %q, want %q", key, got, want)
		}
	}
}
