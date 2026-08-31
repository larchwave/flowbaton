package explore

import "testing"

// A screen keeps its name in whatever script the device speaks. Key kept
// only a-z and 0-9, so every label of a Russian, Greek, or Chinese app
// reduced to nothing and the screen reached the planner, the judge, and the
// element table as a bare digest. Captured on iOS 26.2, 2026-08-31: the
// contacts search screen carries a keyboard key labelled "Русская", which
// named nothing at all.
func TestScreenSignatureKeyKeepsNonLatinLabels(t *testing.T) {
	t.Parallel()

	signature := ScreenSignature{
		AppID:      "com.example.app",
		Salient:    []string{"Настройки", "Экран 2"},
		TreeDigest: "abcdef0123456789",
	}
	if got, want := signature.Key(), "настройки-экран-2-abcdef01"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
	chinese := ScreenSignature{Salient: []string{"设置"}, TreeDigest: "beefbeefbeefbeef"}
	if got, want := chinese.Key(), "设置-beefbeef"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

// A label that carries no letters or digits at all still names nothing, and
// the screen falls back to its digest rather than to a row of dashes.
func TestScreenSignatureKeyDropsPunctuationOnlyLabels(t *testing.T) {
	t.Parallel()

	signature := ScreenSignature{Salient: []string{"···", "!!!"}, TreeDigest: "0123456789abcdef"}
	if got, want := signature.Key(), "01234567"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}
