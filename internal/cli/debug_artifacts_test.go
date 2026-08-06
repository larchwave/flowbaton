package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specs/03-cli-tooling.md:36 lists command metadata among the debug artifacts,
// and internal/report owns the document shape. This test keeps every completed
// flow reachable through both command metadata and the manifest.

func TestARunWritesCommandMetadataForEveryFlow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	output := t.TempDir()

	runner := TestRunner{NewSession: shardSessionFactory(dir, newDriverRecorder().record)}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--device", "udid-one", "--test-output-dir", output,
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}

	for _, flow := range []string{"a", "b"} {
		path := filepath.Join(output, flow, "commands.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v\ntree: %v", path, err, tree(t, output))
		}
		// The frozen shape: a schemaVersion beside one flow carrying its
		// commands (internal/report/model.go, commandsDocument).
		var document struct {
			SchemaVersion string `json:"schemaVersion"`
			Flow          struct {
				Name     string           `json:"name"`
				Commands []map[string]any `json:"commands"`
			} `json:"flow"`
		}
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatalf("%s is not a commands document: %v", path, err)
		}
		if document.SchemaVersion == "" {
			t.Fatalf("%s carries no schema version", path)
		}
		if document.Flow.Name != flow {
			t.Fatalf("%s names flow %q, want %q", path, document.Flow.Name, flow)
		}
		if len(document.Flow.Commands) == 0 {
			t.Fatalf("%s recorded no commands for a flow that ran one", path)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "artifacts.json")); err != nil {
		t.Fatalf("no manifest beside the command metadata: %v\ntree: %v", err, tree(t, output))
	}
}

func TestEachShardWritesItsOwnCommandMetadata(t *testing.T) {
	t.Parallel()

	// Two devices writing one file is the failure assignShardDirectories exists
	// to prevent, so the artifacts have to land under the shard's own root.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "b.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	output := t.TempDir()

	runner := TestRunner{NewSession: shardSessionFactory(dir, newDriverRecorder().record)}
	var stdout, stderr strings.Builder
	if code := runner.Run(context.Background(), []string{
		"--shard-split", "2", "--device", "udid-one,udid-two",
		"--test-output-dir", output,
		filepath.Join(dir, "a.yaml"), filepath.Join(dir, "b.yaml"),
	}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}

	// a.yaml is shard 1, b.yaml is shard 2 — index % shards, and the plan is
	// sorted, so this is not a race.
	for shard, flow := range map[string]string{"shard-1": "a", "shard-2": "b"} {
		path := filepath.Join(output, shard, flow, "commands.json")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("no metadata at %s: %v\ntree: %v", path, err, tree(t, output))
		}
	}
}

// tree lists the output root, so a failure says what WAS written instead of
// only what was not.
func tree(t *testing.T, root string) []string {
	t.Helper()
	var found []string
	_ = filepath.Walk(root, func(path string, _ os.FileInfo, err error) error {
		if err == nil {
			if relative, relErr := filepath.Rel(root, path); relErr == nil {
				found = append(found, relative)
			}
		}
		return nil
	})
	return found
}
