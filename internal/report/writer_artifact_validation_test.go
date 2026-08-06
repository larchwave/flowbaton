package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriterArtifactAPIsRejectInvalidKindAndRelativePath(t *testing.T) {
	t.Parallel()

	scope := t.TempDir()
	root := filepath.Join(scope, "root")
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}
	writeExistingArtifactFile(t, root, "valid.bin", []byte("existing"))

	tests := []struct {
		name string
		kind string
		path string
	}{
		{name: "empty kind", kind: "", path: "valid.bin"},
		{name: "blank kind", kind: " \t\n", path: "valid.bin"},
		{name: "empty path", kind: "debug", path: ""},
		{name: "blank path", kind: "debug", path: " \t"},
		{name: "dot path", kind: "debug", path: "."},
		{name: "absolute path", kind: "debug", path: "/absolute/output.bin"},
		{name: "windows absolute path", kind: "debug", path: "C:/absolute/output.bin"},
		{name: "parent path", kind: "debug", path: "../outside.bin"},
		{name: "nested parent path", kind: "debug", path: "nested/../../outside.bin"},
		{name: "dot segment", kind: "debug", path: "nested/./output.bin"},
		{name: "duplicate slash", kind: "debug", path: "nested//output.bin"},
		{name: "trailing slash", kind: "debug", path: "nested/output.bin/"},
		{name: "backslash", kind: "debug", path: `nested\output.bin`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := writer.WriteArtifact(test.kind, test.path, []byte("data")); err == nil {
				t.Errorf("WriteArtifact(%q, %q) error = nil", test.kind, test.path)
			}
			if _, err := writer.RegisterArtifact(test.kind, test.path); err == nil {
				t.Errorf("RegisterArtifact(%q, %q) error = nil", test.kind, test.path)
			}
		})
	}

	if _, err := os.Stat(filepath.Join(scope, "outside.bin")); !os.IsNotExist(err) {
		t.Fatalf("invalid traversal created outside file: %v", err)
	}
	assertManifestHasNoArtifacts(t, writer, root)
}

func TestWriterArtifactAPIsRejectDirectoriesSymlinksMissingAndOutsideFiles(t *testing.T) {
	t.Parallel()

	scope := t.TempDir()
	root := filepath.Join(scope, "root")
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatalf("create directory target: %v", err)
	}
	outsideFile := filepath.Join(scope, "outside.bin")
	outsideData := []byte("outside must remain unchanged")
	if err := os.WriteFile(outsideFile, outsideData, 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	outsideDir := filepath.Join(scope, "outside-directory")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("create outside directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "existing.bin"), []byte("existing outside"), 0o644); err != nil {
		t.Fatalf("write outside nested file: %v", err)
	}
	if err := os.Symlink(outsideFile, filepath.Join(root, "file-link.bin")); err != nil {
		t.Skipf("symlink setup unavailable: %v", err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "directory-link")); err != nil {
		t.Skipf("directory symlink setup unavailable: %v", err)
	}

	for _, relativePath := range []string{
		"missing.bin",
		"directory",
		"file-link.bin",
		"directory-link/existing.bin",
		"../outside.bin",
	} {
		if _, err := writer.RegisterArtifact("recording", relativePath); err == nil {
			t.Errorf("RegisterArtifact(%q) error = nil", relativePath)
		}
	}
	for _, relativePath := range []string{
		"directory",
		"file-link.bin",
		"directory-link/new.bin",
		"../outside.bin",
	} {
		if _, err := writer.WriteArtifact("debug", relativePath, []byte("replacement")); err == nil {
			t.Errorf("WriteArtifact(%q) error = nil", relativePath)
		}
	}

	if got, err := os.ReadFile(outsideFile); err != nil || !bytes.Equal(got, outsideData) {
		t.Fatalf("outside file = %q, %v; want unchanged %q", got, err, outsideData)
	}
	if _, err := os.Stat(filepath.Join(outsideDir, "new.bin")); !os.IsNotExist(err) {
		t.Fatalf("write followed directory symlink: %v", err)
	}
	assertManifestHasNoArtifacts(t, writer, root)
}

func TestNilWriterArtifactAPIsReturnErrors(t *testing.T) {
	t.Parallel()

	var writer *Writer
	if _, err := writer.WriteArtifact("debug", "debug.bin", []byte("data")); err == nil {
		t.Fatal("nil Writer.WriteArtifact() error = nil")
	}
	if _, err := writer.RegisterArtifact("recording", "recording.mp4"); err == nil {
		t.Fatal("nil Writer.RegisterArtifact() error = nil")
	}
}

func assertManifestHasNoArtifacts(t *testing.T, writer *Writer, root string) {
	t.Helper()
	manifest, err := writer.WriteManifest()
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	var document struct {
		Artifacts []Artifact `json:"artifacts"`
	}
	if err := json.Unmarshal(mustReadFile(t, root, manifest.Path), &document); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if len(document.Artifacts) != 0 {
		t.Fatalf("manifest artifacts = %#v, want none", document.Artifacts)
	}
}
