package memory

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func screenSig(digest string, salient ...string) explore.ScreenSignature {
	return explore.ScreenSignature{AppID: "app", Salient: salient, TreeDigest: digest}
}

// Experience is not a cache: no TTL, and it holds what past sessions learned
// about a screen, which navigator.Reach replays as actions on the device.
// Its filename must therefore be the screen's identity and nothing else.
// ScreenSignature.Key() is not that -- it renders the salient LABELS before
// a TRUNCATED digest, so it changes when label selection changes (b1a79f5
// renamed every iOS screen) and it cannot tell apart two screens whose
// digests share eight hex characters, which Same() reads as different.
func TestExperienceIsFoundAfterItsLabelsChange(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewExperience(dir)
	old := screenSig("1122334455667788", "Contacts", "Back")
	renamed := screenSig("1122334455667788", "Back", "Contacts")

	if err := store.Record(context.Background(), old,
		explore.MemoryEntry{Title: "open the list", Body: "tap All Contacts"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	body, err := store.Get(context.Background(), renamed, "open the list")
	if err != nil {
		t.Fatalf("Get after the labels changed: %v", err)
	}
	if body != "tap All Contacts" {
		t.Fatalf("body = %q, want the recipe recorded before the labels changed", body)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "experience"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != old.TreeDigest+".md" {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("files = %v, want one named after the whole digest", names)
	}
}

// Two screens whose digests agree on the first eight characters are two
// screens: Same() reads the whole digest, so the store must too. Key()
// truncates to eight, which is why the key cannot be the filename.
func TestExperienceKeepsScreensApartOnATruncatedDigestMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewExperience(dir)
	one := screenSig("aabbccdd00000001", "Lists")
	two := screenSig("aabbccdd00000002", "Lists")
	if one.Key() != two.Key() {
		t.Fatalf("keys %q and %q differ; this test needs a truncation collision", one.Key(), two.Key())
	}

	if err := store.Record(context.Background(), one,
		explore.MemoryEntry{Title: "only on one", Body: "x"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := store.Get(context.Background(), two, "only on one"); err == nil {
		t.Fatal("a recipe from a different screen was served")
	}
}

// A signature with no digest has no identity to file under, and guessing one
// would file it with whatever else has none.
func TestExperienceRefusesAScreenWithNoDigest(t *testing.T) {
	t.Parallel()

	store := NewExperience(t.TempDir())
	if err := store.Record(context.Background(), screenSig("", "Lists"),
		explore.MemoryEntry{Title: "t", Body: "b"}); err == nil {
		t.Fatal("recorded a screen with no digest")
	}
}
