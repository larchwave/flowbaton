package flow

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

type commandManifest struct {
	SchemaVersion string                 `json:"schemaVersion"`
	Entries       []commandManifestEntry `json:"entries"`
}

type commandManifestEntry struct {
	Keyword       model.CommandKeyword `json:"keyword"`
	RuntimeStatus string               `json:"runtimeStatus"`
	Minimal       string               `json:"minimal"`
	Maximal       string               `json:"maximal"`
	Invalid       invalidCommandCase   `json:"invalid"`
	ChoiceID      string               `json:"choiceId"`
	Choice        string               `json:"choice"`
}

type invalidCommandCase struct {
	Kind string `json:"kind"`
	YAML string `json:"yaml"`
	Code string `json:"code"`
}

func TestCommandManifestCoversTypedV0Catalog(t *testing.T) {
	t.Parallel()

	manifest := loadCommandManifest(t)
	if manifest.SchemaVersion != "flowbaton.command-manifest/v0" {
		t.Fatalf("manifest schemaVersion = %q", manifest.SchemaVersion)
	}
	if got, want := len(manifest.Entries), 53; got != want {
		t.Fatalf("manifest entry count = %d, want %d", got, want)
	}

	manifestKeywords := make([]model.CommandKeyword, 0, len(manifest.Entries))
	seenChoices := make(map[string]struct{})
	for _, entry := range manifest.Entries {
		entry := entry
		manifestKeywords = append(manifestKeywords, entry.Keyword)
		if entry.ChoiceID == "" || entry.Choice == "" {
			t.Fatalf("%s: isolated v0 choice must be named and explained", entry.Keyword)
		}
		if _, duplicate := seenChoices[entry.ChoiceID]; duplicate {
			t.Fatalf("duplicate choiceId %q", entry.ChoiceID)
		}
		seenChoices[entry.ChoiceID] = struct{}{}
		t.Run(string(entry.Keyword), func(t *testing.T) {
			t.Parallel()
			if entry.RuntimeStatus == "" {
				t.Fatal("runtimeStatus must be declared")
			}

			minimal := parseManifestCase(t, entry.Minimal, 0)
			maximal := parseManifestCase(t, entry.Maximal, 0)
			for caseName, command := range map[string]model.Command{"minimal": minimal, "maximal": maximal} {
				if command.Kind != entry.Keyword {
					t.Fatalf("%s kind = %q, want %q", caseName, command.Kind, entry.Keyword)
				}
				assertNonzeroSourceSpan(t, caseName, command.Source)
			}

			_, err := ParseBytes("/independent/invalid.yaml", []byte(manifestFlow(entry.Invalid.YAML, 0)))
			diagnostic := requireDiagnostic(t, err)
			if diagnostic.Code != entry.Invalid.Code {
				t.Fatalf("invalid %s diagnostic = %q, want %q (%v)", entry.Invalid.Kind, diagnostic.Code, entry.Invalid.Code, diagnostic)
			}

			shifted := parseManifestCase(t, entry.Maximal, 3)
			maximalJSON, err := json.Marshal(maximal)
			if err != nil {
				t.Fatalf("marshal maximal command: %v", err)
			}
			shiftedJSON, err := json.Marshal(shifted)
			if err != nil {
				t.Fatalf("marshal shifted command: %v", err)
			}
			if string(maximalJSON) != string(shiftedJSON) {
				t.Fatalf("source-free parser snapshot drifted with source position:\nbase=%s\nshifted=%s", maximalJSON, shiftedJSON)
			}
			if strings.Contains(string(maximalJSON), "/independent/") || strings.Contains(string(maximalJSON), `"source"`) {
				t.Fatalf("parser snapshot contains source origin: %s", maximalJSON)
			}
		})
	}

	if !reflect.DeepEqual(manifestKeywords, model.CommandKeywords()) {
		t.Fatalf("manifest keyword order = %#v, want canonical catalog", manifestKeywords)
	}
	for _, kind := range []string{"type", "enum", "unknown-field"} {
		found := false
		for _, entry := range manifest.Entries {
			if entry.Invalid.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("manifest has no %s invalid case", kind)
		}
	}
}

func loadCommandManifest(t *testing.T) commandManifest {
	t.Helper()
	data, err := os.ReadFile("../../testdata/flows/command-manifest.json")
	if err != nil {
		t.Fatalf("read command manifest: %v", err)
	}
	var manifest commandManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("decode command manifest: %v", err)
	}
	return manifest
}

func parseManifestCase(t *testing.T, commandYAML string, blankLines int) model.Command {
	t.Helper()
	parsed, err := ParseBytes("/independent/catalog.yaml", []byte(manifestFlow(commandYAML, blankLines)))
	if err != nil {
		t.Fatalf("parse command case %q: %v", commandYAML, err)
	}
	if len(parsed.Commands) != 1 {
		t.Fatalf("command case count = %d, want 1", len(parsed.Commands))
	}
	return parsed.Commands[0]
}

func manifestFlow(commandYAML string, blankLines int) string {
	return "appId: com.example.catalog\n---\n" + strings.Repeat("\n", blankLines) + commandYAML + "\n"
}

func assertNonzeroSourceSpan(t *testing.T, caseName string, source model.SourceInfo) {
	t.Helper()
	positions := []int{
		source.Start.Line,
		source.Start.Column,
		source.End.Line,
		source.End.Column,
		source.End.Offset,
	}
	for _, value := range positions {
		if value <= 0 {
			t.Fatalf("%s source span is incomplete: %#v", caseName, source)
		}
	}
	if source.End.Offset <= source.Start.Offset {
		t.Fatalf("%s source span does not advance: %#v", caseName, source)
	}
}

func TestCommandManifestChoiceIDsAreSortedAndStable(t *testing.T) {
	t.Parallel()

	manifest := loadCommandManifest(t)
	choiceIDs := make([]string, 0, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		choiceIDs = append(choiceIDs, entry.ChoiceID)
	}
	sorted := append([]string(nil), choiceIDs...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(choiceIDs, sorted) {
		t.Fatalf("choice IDs must remain sorted for reviewable manifest diffs")
	}
}
