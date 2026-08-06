package report

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func FuzzMarshalCommandsDeterministic(f *testing.F) {
	f.Add("flow 🚀", "tapOn", "Tap <Pay> & continue", "meta", uint16(25), uint8(3))
	f.Add("\x00invalid", "runFlow", "\xff", "", uint16(0), uint8(0))

	statuses := [...]Status{Completed, Skipped, Warned, Failed, Cancelled}
	started := time.Date(2026, time.July, 15, 18, 30, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, name, keyword, description, metadata string, millis uint16, statusIndex uint8) {
		status := statuses[int(statusIndex)%len(statuses)]
		flow := FlowResult{
			Name:           name,
			Description:    description,
			Status:         status,
			StartedAt:      started,
			EndedAt:        started.Add(time.Duration(millis) * time.Millisecond),
			DurationMillis: int64(millis),
			Metadata:       map[string]string{metadata: description},
			Artifacts:      []Artifact{{Kind: "debug", Path: name}},
			Commands: []CommandResult{
				{
					Sequence:       1,
					Keyword:        keyword,
					Description:    description,
					Status:         status,
					StartedAt:      started,
					EndedAt:        started.Add(time.Duration(millis) * time.Millisecond),
					DurationMillis: int64(millis),
				},
			},
		}

		first, err := MarshalCommands(flow)
		if err != nil {
			t.Fatalf("MarshalCommands() error = %v", err)
		}
		second, err := MarshalCommands(flow)
		if err != nil {
			t.Fatalf("second MarshalCommands() error = %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatal("MarshalCommands() is not deterministic")
		}
		if !json.Valid(first) {
			t.Fatalf("MarshalCommands() produced invalid JSON: %q", first)
		}

		var document struct {
			SchemaVersion string `json:"schemaVersion"`
		}
		if err := json.Unmarshal(first, &document); err != nil {
			t.Fatalf("unmarshal commands document: %v", err)
		}
		if document.SchemaVersion != CommandsSchemaVersion {
			t.Fatalf("schemaVersion = %q, want %q", document.SchemaVersion, CommandsSchemaVersion)
		}
	})
}

func FuzzMarshalJUnitProducesDeterministicValidXML(f *testing.F) {
	f.Add("suite 🚀", "flow ✅", "expected <Pay> & continue", "details \x01", uint16(250), uint8(3))
	f.Add("\x00suite", "\xffflow", "", "", uint16(0), uint8(0))

	statuses := [...]Status{Completed, Skipped, Warned, Failed, Cancelled}
	started := time.Date(2026, time.July, 15, 18, 30, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, suiteName, flowName, message, details string, millis uint16, statusIndex uint8) {
		flow := FlowResult{
			Name:           flowName,
			Status:         statuses[int(statusIndex)%len(statuses)],
			StartedAt:      started,
			EndedAt:        started.Add(time.Duration(millis) * time.Millisecond),
			DurationMillis: int64(millis),
			Failure:        &Failure{Message: message, Details: details},
		}
		options := JUnitOptions{SuiteName: suiteName, Timestamp: started}

		first, err := MarshalJUnit(options, []FlowResult{flow})
		if err != nil {
			t.Fatalf("MarshalJUnit() error = %v", err)
		}
		second, err := MarshalJUnit(options, []FlowResult{flow})
		if err != nil {
			t.Fatalf("second MarshalJUnit() error = %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatal("MarshalJUnit() is not deterministic")
		}

		var document struct {
			XMLName xml.Name `xml:"testsuites"`
		}
		if err := xml.Unmarshal(first, &document); err != nil {
			t.Fatalf("MarshalJUnit() produced invalid XML: %v\n%s", err, first)
		}
		if document.XMLName.Local != "testsuites" {
			t.Fatalf("root element = %q, want testsuites", document.XMLName.Local)
		}
	})
}

func FuzzSanitizeFlowNameProducesOneSafeComponent(f *testing.F) {
	f.Add("../../Checkout: VIP 🚀")
	f.Add("\x00\xff")

	f.Fuzz(func(t *testing.T, name string) {
		got := SanitizeFlowName(name)
		if got == "" || got == "." || got == ".." {
			t.Fatalf("SanitizeFlowName(%q) = %q, want non-empty component", name, got)
		}
		if strings.ContainsAny(got, `/\\`) {
			t.Fatalf("SanitizeFlowName(%q) = %q, contains path separator", name, got)
		}
		for _, r := range got {
			if !asciiLetterOrDigit(r) && r != '-' && r != '_' {
				t.Fatalf("SanitizeFlowName(%q) = %q, contains unsafe rune %q", name, got, r)
			}
		}
	})
}
