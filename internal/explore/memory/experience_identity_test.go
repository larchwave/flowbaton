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

// Experience is not a cache: it has no TTL and holds what past sessions
// learned about a screen. Its filename is ScreenSignature.Key(), which mixes
// the salient LABELS with the digest -- and the labels are display text.
// Same() already says the DIGEST is the screen's identity, so a change to
// how labels are picked must not orphan a screen's recipes. b1a79f5 was
// exactly such a change: every iOS screen was renamed.
func TestExperienceFindsAScreenAfterItsLabelsChange(t *testing.T) {
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
		t.Fatalf("body = %q, want the recipe recorded under the old name", body)
	}
}

// The next write moves the file to the current name, so the directory stays
// readable instead of accumulating one file per naming era.
func TestExperienceRenamesTheFileOnTheNextRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewExperience(dir)
	old := screenSig("aabbccddeeff0011", "Contacts", "Back")
	renamed := screenSig("aabbccddeeff0011", "Back", "Contacts")

	if err := store.Record(context.Background(), old,
		explore.MemoryEntry{Title: "first", Body: "one"}); err != nil {
		t.Fatalf("Record old: %v", err)
	}
	if err := store.Record(context.Background(), renamed,
		explore.MemoryEntry{Title: "second", Body: "two"}); err != nil {
		t.Fatalf("Record renamed: %v", err)
	}

	names := []string{}
	entries, err := os.ReadDir(filepath.Join(dir, "experience"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 1 || names[0] != renamed.Key()+".md" {
		t.Fatalf("files = %v, want just %q", names, renamed.Key()+".md")
	}
	for _, title := range []string{"first", "second"} {
		if _, err := store.Get(context.Background(), renamed, title); err != nil {
			t.Fatalf("Get(%q) after the rename: %v", title, err)
		}
	}
}

// Two screens are two screens. The fallback matches the digest, which is
// what Same() reads, so it must not merge screens that differ.
func TestExperienceKeepsDifferentScreensApart(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewExperience(dir)
	one := screenSig("1111111111111111", "Lists")
	two := screenSig("2222222222222222", "Lists")

	if err := store.Record(context.Background(), one,
		explore.MemoryEntry{Title: "only on one", Body: "x"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if _, err := store.Get(context.Background(), two, "only on one"); err == nil {
		t.Fatal("Get found a recipe from a different screen")
	}
}
