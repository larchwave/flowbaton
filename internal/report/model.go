package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// CommandsSchemaVersion identifies the on-disk commands.json contract.
const CommandsSchemaVersion = "flowbaton.commands/v1"

// Status is the terminal outcome of a flow or command.
type Status string

const (
	Completed Status = "Completed"
	Skipped   Status = "Skipped"
	Warned    Status = "Warned"
	Failed    Status = "Failed"
	Cancelled Status = "Cancelled"
)

// Valid reports whether status is part of the stable result contract.
func (status Status) Valid() bool {
	switch status {
	case Completed, Skipped, Warned, Failed, Cancelled:
		return true
	default:
		return false
	}
}

// Failure carries a consumer-safe failure summary and optional detail.
type Failure struct {
	Message string `json:"message"`
	Details string `json:"details"`
}

// Artifact identifies an output-root-relative file produced for a result.
type Artifact struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

// CommandResult is the engine-neutral report shape for one executed command.
type CommandResult struct {
	Sequence       int64             `json:"sequence"`
	Depth          int               `json:"depth"`
	Keyword        string            `json:"keyword"`
	Description    string            `json:"description"`
	Status         Status            `json:"status"`
	StartedAt      time.Time         `json:"startedAt"`
	EndedAt        time.Time         `json:"endedAt"`
	DurationMillis int64             `json:"durationMillis"`
	Failure        *Failure          `json:"failure"`
	Metadata       map[string]string `json:"metadata"`
	Artifacts      []Artifact        `json:"artifacts"`
}

// FlowResult is the engine-neutral report shape for one executed flow.
type FlowResult struct {
	Name string `json:"name"`
	// File is the flow's path relative to the run root, which is what the
	// JUnit report's file= attribute carries. Separate from Description
	// (the absolute path) because the contract reports sub/alpha.yaml, and
	// only the caller that knows the root can work that out.
	File           string            `json:"file,omitempty"`
	Description    string            `json:"description"`
	Status         Status            `json:"status"`
	StartedAt      time.Time         `json:"startedAt"`
	EndedAt        time.Time         `json:"endedAt"`
	DurationMillis int64             `json:"durationMillis"`
	Failure        *Failure          `json:"failure"`
	Metadata       map[string]string `json:"metadata"`
	Artifacts      []Artifact        `json:"artifacts"`
	Commands       []CommandResult   `json:"commands"`
}

type commandsDocument struct {
	SchemaVersion string     `json:"schemaVersion"`
	Flow          FlowResult `json:"flow"`
}

// MarshalCommands renders a canonical commands.json document. It sorts copies
// of sequence-bearing and artifact fields and never mutates the supplied DTO.
func MarshalCommands(flow FlowResult) ([]byte, error) {
	if !flow.Status.Valid() {
		return nil, fmt.Errorf("flow status %q is invalid", flow.Status)
	}

	normalized := normalizeFlow(flow)
	for _, command := range normalized.Commands {
		if !command.Status.Valid() {
			return nil, fmt.Errorf("command %d status %q is invalid", command.Sequence, command.Status)
		}
	}

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	err := encoder.Encode(commandsDocument{
		SchemaVersion: CommandsSchemaVersion,
		Flow:          normalized,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal commands: %w", err)
	}

	return output.Bytes(), nil
}

func normalizeFlow(flow FlowResult) FlowResult {
	flow.StartedAt = flow.StartedAt.UTC()
	flow.EndedAt = flow.EndedAt.UTC()
	flow.Metadata = cloneMetadata(flow.Metadata)
	flow.Artifacts = cloneAndSortArtifacts(flow.Artifacts)
	flow.Commands = append([]CommandResult(nil), flow.Commands...)
	if flow.Commands == nil {
		flow.Commands = []CommandResult{}
	}

	for i := range flow.Commands {
		flow.Commands[i].StartedAt = flow.Commands[i].StartedAt.UTC()
		flow.Commands[i].EndedAt = flow.Commands[i].EndedAt.UTC()
		flow.Commands[i].Metadata = cloneMetadata(flow.Commands[i].Metadata)
		flow.Commands[i].Artifacts = cloneAndSortArtifacts(flow.Commands[i].Artifacts)
	}
	sort.SliceStable(flow.Commands, func(i, j int) bool {
		return flow.Commands[i].Sequence < flow.Commands[j].Sequence
	})

	return flow
}

func cloneMetadata(metadata map[string]string) map[string]string {
	cloned := make(map[string]string, len(metadata))
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

func cloneAndSortArtifacts(artifacts []Artifact) []Artifact {
	cloned := append([]Artifact(nil), artifacts...)
	if cloned == nil {
		cloned = []Artifact{}
	}
	sort.SliceStable(cloned, func(i, j int) bool {
		if cloned[i].Kind == cloned[j].Kind {
			return cloned[i].Path < cloned[j].Path
		}
		return cloned[i].Kind < cloned[j].Kind
	})
	return cloned
}
