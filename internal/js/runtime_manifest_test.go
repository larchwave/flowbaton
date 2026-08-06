package js

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

type runtimeManifest struct {
	Version int            `yaml:"version"`
	Entries []runtimeEntry `yaml:"entries"`
}

type runtimeEntry struct {
	ID             string  `yaml:"id"`
	Group          string  `yaml:"group"`
	Expression     string  `yaml:"expression"`
	Classification string  `yaml:"classification"`
	Expected       *string `yaml:"expected,omitempty"`
	Rationale      string  `yaml:"rationale,omitempty"`
	Critical       bool    `yaml:"critical,omitempty"`
}

func TestRuntimeManifestIsCompleteAndSupportedEntriesExecute(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "testdata", "js", "runtime-manifest.yaml")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	var manifest runtimeManifest
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode runtime manifest: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("runtime manifest has trailing YAML document or decode error: %v", err)
	}
	if manifest.Version != 1 {
		t.Fatalf("runtime manifest version = %d, want 1", manifest.Version)
	}
	if len(manifest.Entries) < 100 {
		t.Fatalf("runtime manifest has %d expressions, want at least 100", len(manifest.Entries))
	}

	requiredGroups := map[string]bool{
		"primitives":     false,
		"missing-vars":   false,
		"strings-regex":  false,
		"arrays-objects": false,
		"scopes":         false,
		"interpolation":  false,
		"async-promise":  false,
		"helpers":        false,
		"http":           false,
		"errors":         false,
	}
	classificationCounts := map[string]int{
		"supported":        0,
		"runtime-specific": 0,
		"unsupported-case": 0,
	}
	ids := make(map[string]bool, len(manifest.Entries))
	runtime := newRuntimeWithConfig(t, Config{
		Random:     rand.New(rand.NewSource(101)),
		Platform:   "test",
		CopiedText: stringPtr("clip"),
	})
	for _, entry := range manifest.Entries {
		entry := entry
		t.Run(entry.ID, func(t *testing.T) {
			if entry.ID == "" || entry.Expression == "" {
				t.Fatalf("entry has blank id or expression: %#v", entry)
			}
			if ids[entry.ID] {
				t.Fatalf("duplicate runtime entry id %q", entry.ID)
			}
			ids[entry.ID] = true
			if _, ok := requiredGroups[entry.Group]; !ok {
				t.Fatalf("entry %q has unknown group %q", entry.ID, entry.Group)
			}
			requiredGroups[entry.Group] = true
			if _, ok := classificationCounts[entry.Classification]; !ok {
				t.Fatalf("entry %q has unknown classification %q", entry.ID, entry.Classification)
			}
			classificationCounts[entry.Classification]++
			if entry.Critical && entry.Classification != "supported" {
				t.Fatalf("critical entry %q is classified %q; critical unsupported cases block release", entry.ID, entry.Classification)
			}

			switch entry.Classification {
			case "supported":
				if entry.Expected == nil {
					t.Fatalf("supported entry %q has no expected result", entry.ID)
				}
				result, err := runtime.Evaluate(context.Background(), EvalRequest{Script: entry.Expression})
				if err != nil {
					t.Fatalf("Evaluate(%q) error = %v", entry.Expression, err)
				}
				if result.Text != *entry.Expected {
					t.Fatalf("Evaluate(%q) = %q, want %q", entry.Expression, result.Text, *entry.Expected)
				}
			case "runtime-specific", "unsupported-case":
				if entry.Rationale == "" {
					t.Fatalf("%s entry %q has no rationale", entry.Classification, entry.ID)
				}
			}
		})
	}
	for group, present := range requiredGroups {
		if !present {
			t.Errorf("runtime manifest is missing required group %q", group)
		}
	}
	for classification, count := range classificationCounts {
		if count == 0 {
			t.Errorf("runtime manifest has no %s entries", classification)
		}
	}
}
