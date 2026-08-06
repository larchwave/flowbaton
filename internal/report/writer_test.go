package report

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSanitizeFlowName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "checkout", want: "checkout"},
		{name: " Checkout VIP ", want: "Checkout-VIP"},
		{name: `../../Checkout: VIP 🚀`, want: "Checkout-VIP"},
		{name: `a/b\\c:d`, want: "a-b-c-d"},
		{name: "A__B---C", want: "A__B---C"},
		{name: "日本語", want: "flow"},
		{name: "...", want: "flow"},
		{name: "", want: "flow"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := SanitizeFlowName(test.name); got != test.want {
				t.Fatalf("SanitizeFlowName(%q) = %q, want %q", test.name, got, test.want)
			}
		})
	}
}

func TestWriterCreatesCanonicalArtifactsAndFiltersManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	flowName := `../../Checkout: VIP 🚀`
	screenshotBytes := []byte("provided screenshot bytes\x00\x01")
	deletedScreenshot, err := writer.WriteFailureScreenshot(flowName, 7, screenshotBytes)
	if err != nil {
		t.Fatalf("WriteFailureScreenshot(7) error = %v", err)
	}
	if deletedScreenshot != (Artifact{Kind: ArtifactKindFailureScreenshot, Path: "Checkout-VIP/failure-000007.png"}) {
		t.Fatalf("WriteFailureScreenshot(7) = %#v", deletedScreenshot)
	}
	if got := mustReadFile(t, root, deletedScreenshot.Path); !bytes.Equal(got, screenshotBytes) {
		t.Fatalf("failure screenshot bytes = %q, want %q", got, screenshotBytes)
	}

	keptScreenshot, err := writer.WriteFailureScreenshot(flowName, 8, []byte("kept"))
	if err != nil {
		t.Fatalf("WriteFailureScreenshot(8) error = %v", err)
	}

	started := time.Date(2026, time.July, 15, 18, 30, 0, 0, time.UTC)
	flow := FlowResult{
		Name: flowName, Status: Completed,
		StartedAt: started, EndedAt: started.Add(time.Second), DurationMillis: 1000,
	}
	commandsArtifact, err := writer.WriteCommands(flow)
	if err != nil {
		t.Fatalf("WriteCommands() error = %v", err)
	}
	if commandsArtifact != (Artifact{Kind: ArtifactKindCommands, Path: "Checkout-VIP/commands.json"}) {
		t.Fatalf("WriteCommands() = %#v", commandsArtifact)
	}
	wantCommands, err := MarshalCommands(flow)
	if err != nil {
		t.Fatalf("MarshalCommands() error = %v", err)
	}
	if got := mustReadFile(t, root, commandsArtifact.Path); !bytes.Equal(got, wantCommands) {
		t.Fatalf("commands.json mismatch\n--- got ---\n%s--- want ---\n%s", got, wantCommands)
	}

	junitArtifact, err := writer.WriteJUnit(
		JUnitOptions{SuiteName: "mobile", Timestamp: started},
		[]FlowResult{flow},
	)
	if err != nil {
		t.Fatalf("WriteJUnit() error = %v", err)
	}
	if junitArtifact != (Artifact{Kind: ArtifactKindJUnit, Path: "junit.xml"}) {
		t.Fatalf("WriteJUnit() = %#v", junitArtifact)
	}

	if err := os.Remove(filepath.Join(root, filepath.FromSlash(deletedScreenshot.Path))); err != nil {
		t.Fatalf("remove tracked screenshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("not writer-owned"), 0o644); err != nil {
		t.Fatalf("write unrelated file: %v", err)
	}

	manifestArtifact, err := writer.WriteManifest()
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	if manifestArtifact != (Artifact{Kind: ArtifactKindManifest, Path: "artifacts.json"}) {
		t.Fatalf("WriteManifest() = %#v", manifestArtifact)
	}

	wantManifest := `{
  "schemaVersion": "flowbaton.artifacts/v1",
  "artifacts": [
    {
      "kind": "commands",
      "path": "Checkout-VIP/commands.json"
    },
    {
      "kind": "failure-screenshot",
      "path": "Checkout-VIP/failure-000008.png"
    },
    {
      "kind": "junit",
      "path": "junit.xml"
    }
  ]
}
`
	gotManifest := mustReadFile(t, root, manifestArtifact.Path)
	if string(gotManifest) != wantManifest {
		t.Fatalf("artifacts.json mismatch\n--- got ---\n%s--- want ---\n%s", gotManifest, wantManifest)
	}
	if bytes.Contains(gotManifest, []byte(deletedScreenshot.Path)) {
		t.Fatalf("manifest contains deleted artifact %q", deletedScreenshot.Path)
	}
	if bytes.Contains(gotManifest, []byte("unrelated.txt")) {
		t.Fatal("manifest contains file not created through Writer")
	}
	if bytes.Contains(gotManifest, []byte(manifestArtifact.Path)) {
		t.Fatal("manifest recursively contains itself")
	}

	if _, err := writer.WriteManifest(); err != nil {
		t.Fatalf("second WriteManifest() error = %v", err)
	}
	if got := mustReadFile(t, root, manifestArtifact.Path); !bytes.Equal(got, gotManifest) {
		t.Fatal("WriteManifest() changed for unchanged artifacts")
	}
	if got := mustReadFile(t, root, keptScreenshot.Path); string(got) != "kept" {
		t.Fatalf("kept screenshot = %q, want kept", got)
	}
}

func TestWriterRejectsInvalidInputsWithoutManifestEntries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	if _, err := writer.WriteCommands(FlowResult{Name: "bad", Status: Status("unknown")}); err == nil {
		t.Fatal("WriteCommands() error = nil, want invalid status error")
	}
	if _, err := writer.WriteFailureScreenshot("bad", -1, []byte("bytes")); err == nil {
		t.Fatal("WriteFailureScreenshot() error = nil, want negative sequence error")
	}

	manifest, err := writer.WriteManifest()
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	got := mustReadFile(t, root, manifest.Path)
	if !bytes.Contains(got, []byte(`"artifacts": []`)) {
		t.Fatalf("manifest = %s, want no artifact entries", got)
	}
}

func TestNewWriterRejectsEmptyRootAndNonDirectory(t *testing.T) {
	t.Parallel()

	if _, err := NewWriter(""); err == nil {
		t.Fatal("NewWriter(\"\") error = nil, want error")
	}

	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("file"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := NewWriter(path); err == nil {
		t.Fatal("NewWriter(file) error = nil, want error")
	}
}

func TestWriterSupportsConcurrentArtifactCalls(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writer, err := NewWriter(root)
	if err != nil {
		t.Fatalf("NewWriter() error = %v", err)
	}

	const count = 24
	var wait sync.WaitGroup
	errors := make(chan error, count)
	for sequence := int64(1); sequence <= count; sequence++ {
		sequence := sequence
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := writer.WriteFailureScreenshot("concurrent flow", sequence, []byte(fmt.Sprintf("%d", sequence)))
			if err != nil {
				errors <- err
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent WriteFailureScreenshot() error = %v", err)
	}

	manifest, err := writer.WriteManifest()
	if err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	got := string(mustReadFile(t, root, manifest.Path))
	if count := strings.Count(got, `"kind": "failure-screenshot"`); count != 24 {
		t.Fatalf("manifest screenshot count = %d, want 24", count)
	}
}

func mustReadFile(t *testing.T, root, relativePath string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %q: %v", relativePath, err)
	}
	return data
}
