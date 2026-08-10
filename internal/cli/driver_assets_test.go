package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/larchwave/flowbaton/internal/assets"
)

func TestDriverVersionComparisonIsNumeric(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "17.10", right: "17.9", want: 1},
		{left: "26", right: "26.0", want: 0},
		{left: "16.4", right: "17.0", want: -1},
	} {
		if got := compareNumericVersions(test.left, test.right); got != test.want {
			t.Errorf("compareNumericVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestDriverManifestCacheRejectsASymlinkBoundary(t *testing.T) {
	cacheRoot := t.TempDir()
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(cacheRoot, "manifests")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := secureDriverManifestDirectory(cacheRoot, false)
	if !errors.Is(err, assets.ErrInvalidAssetCache) {
		t.Fatalf("secureDriverManifestDirectory() error = %v, want ErrInvalidAssetCache", err)
	}
}

func TestStoredDriverManifestIsPrivateAndAtomic(t *testing.T) {
	temporary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(temporary, "drivers")
	contents := []byte("manifest\n")
	if err := storeReleaseDriverManifest(cacheRoot, "1.2.3", contents); err != nil {
		t.Fatalf("storeReleaseDriverManifest() error = %v", err)
	}
	path := storedDriverManifest(cacheRoot, "1.2.3")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(contents) {
		t.Fatalf("manifest = %q, want %q", got, contents)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("manifest mode = %#o, want 0600", info.Mode().Perm())
	}
}
