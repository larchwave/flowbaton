package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestWriterWriteAndRegisterArtifactIncludeOwnedFilesInManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	writtenData := []byte("host-owned debug output\x00\x01")
	writtenBefore := bytes.Clone(writtenData)
	written, err := writer.WriteArtifact("debug", "nested/debug/output.bin", writtenData)
	if err != nil {
		t.Fatalf("WriteArtifact() error = %v", err)
	}
	if written != (Artifact{Kind: "debug", Path: "nested/debug/output.bin"}) {
		t.Fatalf("WriteArtifact() = %#v", written)
	}
	if got := mustReadFile(t, root, written.Path); !bytes.Equal(got, writtenBefore) {
		t.Fatalf("written bytes = %q, want %q", got, writtenBefore)
	}
	if !bytes.Equal(writtenData, writtenBefore) {
		t.Fatal("WriteArtifact() mutated input bytes")
	}

	recordingPath := "recordings/session/run.mp4"
	recordingData := []byte("already finalized recording")
	writeExistingArtifactFile(t, root, recordingPath, recordingData)
	fullRecordingPath := filepath.Join(root, filepath.FromSlash(recordingPath))
	beforeInfo, err := os.Stat(fullRecordingPath)
	if err != nil {
		t.Fatalf("stat recording before registration: %v", err)
	}
	registered, err := writer.RegisterArtifact("recording", recordingPath)
	if err != nil {
		t.Fatalf("RegisterArtifact() error = %v", err)
	}
	if registered != (Artifact{Kind: "recording", Path: recordingPath}) {
		t.Fatalf("RegisterArtifact() = %#v", registered)
	}
	afterInfo, err := os.Stat(fullRecordingPath)
	if err != nil {
		t.Fatalf("stat recording after registration: %v", err)
	}
	if beforeInfo.Mode() != afterInfo.Mode() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("RegisterArtifact() rewrote or changed the existing file")
	}
	if got := mustReadFile(t, root, recordingPath); !bytes.Equal(got, recordingData) {
		t.Fatalf("registered bytes = %q, want %q", got, recordingData)
	}

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
	wantArtifacts := []Artifact{
		{Kind: "debug", Path: written.Path},
		{Kind: "recording", Path: registered.Path},
	}
	if !reflect.DeepEqual(document.Artifacts, wantArtifacts) {
		t.Fatalf("manifest artifacts = %#v, want %#v", document.Artifacts, wantArtifacts)
	}
}

func TestWriterArtifactReplacementIsDeterministicByRelativePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	if _, err := writer.WriteArtifact("first-kind", "shared/result.bin", []byte("first")); err != nil {
		t.Fatalf("first WriteArtifact() error = %v", err)
	}
	replacement, err := writer.WriteArtifact("second-kind", "shared/result.bin", []byte("second"))
	if err != nil {
		t.Fatalf("replacement WriteArtifact() error = %v", err)
	}
	if got := mustReadFile(t, root, replacement.Path); string(got) != "second" {
		t.Fatalf("replacement content = %q, want second", got)
	}
	registered, err := writer.RegisterArtifact("registered-kind", replacement.Path)
	if err != nil {
		t.Fatalf("replacement RegisterArtifact() error = %v", err)
	}
	if registered != (Artifact{Kind: "registered-kind", Path: replacement.Path}) {
		t.Fatalf("replacement RegisterArtifact() = %#v", registered)
	}
	if got := mustReadFile(t, root, replacement.Path); string(got) != "second" {
		t.Fatalf("RegisterArtifact() changed replacement content to %q", got)
	}

	manifest, err := writer.WriteManifest()
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	manifestData := mustReadFile(t, root, manifest.Path)
	if bytes.Contains(manifestData, []byte("first-kind")) || bytes.Contains(manifestData, []byte("second-kind")) {
		t.Fatal("manifest retained a replaced artifact kind")
	}
	if !bytes.Contains(manifestData, []byte("registered-kind")) {
		t.Fatal("manifest omitted final registered artifact kind")
	}
	if count := bytes.Count(manifestData, []byte("shared/result.bin")); count != 1 {
		t.Fatalf("manifest path count = %d, want 1", count)
	}
}

func TestWriterWriteAndRegisterArtifactAreConcurrentSafe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	const count = 24
	for index := range count {
		writeExistingArtifactFile(
			t,
			root,
			fmt.Sprintf("recordings/%02d.mp4", index),
			[]byte(fmt.Sprintf("recording-%02d", index)),
		)
	}

	var waitGroup sync.WaitGroup
	errorsByCall := make(chan error, count*2)
	for index := range count {
		index := index
		waitGroup.Add(2)
		go func() {
			defer waitGroup.Done()
			_, err := writer.WriteArtifact(
				"debug",
				fmt.Sprintf("debug/%02d.txt", index),
				[]byte(fmt.Sprintf("debug-%02d", index)),
			)
			if err != nil {
				errorsByCall <- err
			}
		}()
		go func() {
			defer waitGroup.Done()
			_, err := writer.RegisterArtifact("recording", fmt.Sprintf("recordings/%02d.mp4", index))
			if err != nil {
				errorsByCall <- err
			}
		}()
	}
	waitGroup.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		t.Errorf("concurrent artifact operation: %v", err)
	}

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
	if len(document.Artifacts) != count*2 {
		t.Fatalf("manifest artifact count = %d, want %d", len(document.Artifacts), count*2)
	}
}

func writeExistingArtifactFile(t *testing.T, root, relativePath string, data []byte) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create artifact parent: %v", err)
	}
	if err := os.WriteFile(fullPath, data, 0o640); err != nil {
		t.Fatalf("write existing artifact: %v", err)
	}
}
