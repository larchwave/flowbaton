package flow

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

const (
	parserSnapshotManifestPath = "../../testdata/flows/command-manifest.json"
	parserSnapshotGoldenPath   = "../../contracts/v0/parser-commands.json"
)

type parserSnapshotManifest struct {
	SchemaVersion string                        `json:"schemaVersion"`
	Entries       []parserSnapshotManifestEntry `json:"entries"`
}

type parserSnapshotManifestEntry struct {
	Keyword       model.CommandKeyword         `json:"keyword"`
	RuntimeStatus string                       `json:"runtimeStatus"`
	Minimal       string                       `json:"minimal"`
	Maximal       string                       `json:"maximal"`
	Invalid       parserSnapshotInvalidCommand `json:"invalid"`
	ChoiceID      string                       `json:"choiceId"`
	Choice        string                       `json:"choice"`
}

type parserSnapshotInvalidCommand struct {
	Kind string `json:"kind"`
	YAML string `json:"yaml"`
	Code string `json:"code"`
}

type parserCommandGoldenV0 struct {
	SchemaVersion         int                        `json:"schema_version"`
	ContractVersion       string                     `json:"contract_version"`
	ManifestSchemaVersion string                     `json:"manifest_schema_version"`
	Entries               []parserCommandGoldenEntry `json:"entries"`
}

type parserCommandGoldenEntry struct {
	Keyword model.CommandKeyword `json:"keyword"`
	Command json.RawMessage      `json:"command"`
}

func TestParserMaximalCommandsMatchStaticV0Golden(t *testing.T) {
	manifest := readParserSnapshotJSON[parserSnapshotManifest](t, parserSnapshotManifestPath)
	golden := readParserSnapshotJSON[parserCommandGoldenV0](t, parserSnapshotGoldenPath)

	if manifest.SchemaVersion != "flowbaton.command-manifest/v0" {
		t.Fatalf("manifest schema = %q, want flowbaton.command-manifest/v0", manifest.SchemaVersion)
	}
	if golden.SchemaVersion != 1 || golden.ContractVersion != "v0" || golden.ManifestSchemaVersion != manifest.SchemaVersion {
		t.Fatalf(
			"golden header = schema %d / contract %q / manifest %q, want 1 / v0 / %q",
			golden.SchemaVersion,
			golden.ContractVersion,
			golden.ManifestSchemaVersion,
			manifest.SchemaVersion,
		)
	}

	keywords := model.CommandKeywords()
	if len(keywords) != 53 || len(manifest.Entries) != len(keywords) || len(golden.Entries) != len(keywords) {
		t.Fatalf(
			"catalog/manifest/golden counts = %d/%d/%d, want 53/53/53",
			len(keywords),
			len(manifest.Entries),
			len(golden.Entries),
		)
	}

	for index, keyword := range keywords {
		manifestEntry := manifest.Entries[index]
		goldenEntry := golden.Entries[index]
		if manifestEntry.Keyword != keyword || goldenEntry.Keyword != keyword {
			t.Fatalf(
				"keyword[%d] catalog/manifest/golden = %q/%q/%q",
				index,
				keyword,
				manifestEntry.Keyword,
				goldenEntry.Keyword,
			)
		}

		t.Run(string(keyword), func(t *testing.T) {
			var reviewed model.Command
			decodeParserSnapshotJSON(t, string(keyword)+" golden command", goldenEntry.Command, &reviewed)
			if reviewed.Kind != keyword {
				t.Fatalf("reviewed command kind = %q, want %q", reviewed.Kind, keyword)
			}

			base := parseParserSnapshotCommand(t, "/snapshot/base.yaml", manifestEntry.Maximal, 0)
			shifted := parseParserSnapshotCommand(t, "/snapshot/shifted.yaml", manifestEntry.Maximal, 7)
			baseJSON := marshalParserSnapshotCommand(t, base)
			shiftedJSON := marshalParserSnapshotCommand(t, shifted)
			if !bytes.Equal(baseJSON, shiftedJSON) {
				t.Fatalf("normalized command depends on source position:\nbase=%s\nshifted=%s", baseJSON, shiftedJSON)
			}
			assertParserSnapshotHasNoOrigin(t, baseJSON)
			assertParserSnapshotHasNoOrigin(t, goldenEntry.Command)

			actualSemantic := decodeParserSnapshotSemanticJSON(t, string(keyword)+" parsed command", baseJSON)
			goldenSemantic := decodeParserSnapshotSemanticJSON(t, string(keyword)+" reviewed command", goldenEntry.Command)
			if !reflect.DeepEqual(actualSemantic, goldenSemantic) {
				t.Fatalf("maximal parser output differs from reviewed v0 golden:\nactual=%s\ngolden=%s", baseJSON, goldenEntry.Command)
			}
		})
	}
}

func readParserSnapshotJSON[T any](t *testing.T, path string) T {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value T
	decodeParserSnapshotJSON(t, path, contents, &value)
	return value
}

func decodeParserSnapshotJSON(t *testing.T, name string, contents []byte, destination any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatalf("decode %s: trailing JSON value", name)
		}
		t.Fatalf("decode %s trailing data: %v", name, err)
	}
}

func parseParserSnapshotCommand(t *testing.T, path, commandYAML string, blankLines int) model.Command {
	t.Helper()
	flowYAML := "appId: com.example.catalog\n---\n" + strings.Repeat("\n", blankLines) + commandYAML + "\n"
	parsed, err := ParseBytes(path, []byte(flowYAML))
	if err != nil {
		t.Fatalf("parse maximal command %q: %v", commandYAML, err)
	}
	if len(parsed.Commands) != 1 {
		t.Fatalf("parsed command count = %d, want 1", len(parsed.Commands))
	}
	return parsed.Commands[0]
}

func marshalParserSnapshotCommand(t *testing.T, command model.Command) []byte {
	t.Helper()
	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("marshal parser command: %v", err)
	}
	return encoded
}

func decodeParserSnapshotSemanticJSON(t *testing.T, name string, contents []byte) any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode semantic %s: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("decode semantic %s trailing data: %v", name, err)
	}
	return value
}

func assertParserSnapshotHasNoOrigin(t *testing.T, contents []byte) {
	t.Helper()
	text := string(contents)
	for _, forbidden := range []string{`"source"`, `"fieldSources"`, `"resolvedPath"`, "/snapshot/"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("parser snapshot contains origin token %q: %s", forbidden, contents)
		}
	}
}
