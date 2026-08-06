package report

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
)

// FromEngineFlowResult converts one immutable engine result into the report v1
// DTO without transferring ownership of engine state.
func FromEngineFlowResult(result engine.FlowResult, config model.Config) (FlowResult, error) {
	flowStatus, err := engineOutcomeToReport(result.Outcome())
	if err != nil {
		return FlowResult{}, engine.NewConfigurationError("convert engine flow status", err)
	}

	// Three sources, narrowest first. An explicit config wins, followed by the
	// name recorded in the engine result. The file stem is the fallback for an
	// unnamed flow.
	name := config.Name
	if name == "" {
		name = result.Name()
	}
	if name == "" {
		// Unnamed flows use the file stem as their report identity.
		base := filepath.Base(result.Path())
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	engineCommands := result.Commands()
	converted := FlowResult{
		Name: name,
		// The base name is the honest default here: only a caller that knows
		// the run root can say sub/alpha.yaml, and it overwrites this.
		File:           filepath.Base(result.Path()),
		Description:    result.Path(),
		Status:         flowStatus,
		StartedAt:      result.StartedAt().UTC(),
		EndedAt:        result.FinishedAt().UTC(),
		DurationMillis: result.Duration().Milliseconds(),
		Failure:        reportFailure(result.ProductError()),
		Metadata:       map[string]string{},
		Artifacts:      []Artifact{},
		Commands:       make([]CommandResult, 0, len(engineCommands)),
	}
	if result.RootRunID() != "" {
		converted.Metadata["rootRunId"] = result.RootRunID()
	}
	artifactSet := make(map[artifactIdentity]struct{})
	for _, command := range engineCommands {
		sequence, sequenceErr := engineSequenceToReport(command.Sequence())
		if sequenceErr != nil {
			return FlowResult{}, engine.NewConfigurationError("convert engine command sequence", sequenceErr)
		}
		commandStatus, statusErr := engineOutcomeToReport(command.Outcome())
		if statusErr != nil {
			return FlowResult{}, engine.NewConfigurationError("convert engine command status", statusErr)
		}
		metadataSnapshot := command.Metadata()
		modelCommand := command.Command()
		if evaluated, exists := metadataSnapshot.EvaluatedCommand(); exists {
			modelCommand = evaluated
		}
		description := string(modelCommand.Kind)
		if modelCommand.Label != nil && strings.TrimSpace(*modelCommand.Label) != "" {
			description = strings.TrimSpace(*modelCommand.Label)
		}
		metadata, metadataErr := engineMetadataToReport(metadataSnapshot)
		if metadataErr != nil {
			return FlowResult{}, engine.NewConfigurationError("convert engine command metadata", metadataErr)
		}
		if command.RootRunID() != "" {
			metadata["rootRunId"] = command.RootRunID()
		}
		artifacts := engineArtifactsToReport(command.Artifacts())
		converted.Commands = append(converted.Commands, CommandResult{
			Sequence:       sequence,
			Depth:          command.Depth(),
			Keyword:        string(modelCommand.Kind),
			Description:    description,
			Status:         commandStatus,
			StartedAt:      command.StartedAt().UTC(),
			EndedAt:        command.FinishedAt().UTC(),
			DurationMillis: command.Duration().Milliseconds(),
			Failure:        reportFailure(command.ProductError()),
			Metadata:       metadata,
			Artifacts:      artifacts,
		})
		for _, artifact := range artifacts {
			identity := artifactIdentity{kind: artifact.Kind, path: artifact.Path}
			if _, exists := artifactSet[identity]; exists {
				continue
			}
			artifactSet[identity] = struct{}{}
			converted.Artifacts = append(converted.Artifacts, artifact)
		}
	}
	if _, err := MarshalCommands(converted); err != nil {
		return FlowResult{}, engine.NewConfigurationError("validate report flow result", err)
	}
	return converted, nil
}

func engineSequenceToReport(sequence uint64) (int64, error) {
	if sequence > math.MaxInt64 {
		return 0, engine.NewConfigurationError(
			fmt.Sprintf("engine command sequence %d overflows report int64", sequence),
			nil,
		)
	}
	return int64(sequence), nil
}

type artifactIdentity struct {
	kind string
	path string
}

func engineArtifactsToReport(artifacts []device.Artifact) []Artifact {
	converted := make([]Artifact, len(artifacts))
	for index, artifact := range artifacts {
		converted[index] = Artifact{Kind: artifact.Kind, Path: artifact.Path}
	}
	return converted
}

func engineMetadataToReport(metadata engine.CommandMetadata) (map[string]string, error) {
	converted := make(map[string]string)
	if metadata.HasNumberOfRuns() {
		if metadata.NumberOfRuns() < 0 {
			return nil, engine.NewConfigurationError("engine command metadata numberOfRuns must not be negative", nil)
		}
		converted["numberOfRuns"] = strconv.Itoa(metadata.NumberOfRuns())
	}
	if logs := metadata.LogMessages(); len(logs) > 0 {
		encoded, err := json.Marshal(logs)
		if err != nil {
			return nil, engine.NewConfigurationError("encode engine command log messages", err)
		}
		converted["logMessages"] = string(encoded)
	}
	if evaluated, exists := metadata.EvaluatedCommand(); exists {
		encoded, err := json.Marshal(evaluated)
		if err != nil {
			return nil, engine.NewConfigurationError("encode evaluated engine command", err)
		}
		converted["evaluatedCommand"] = string(encoded)
	}
	if metadata.Insight() != "" {
		converted["insight"] = metadata.Insight()
	}
	if metadata.AIReasoning() != "" {
		converted["aiReasoning"] = metadata.AIReasoning()
	}
	return converted, nil
}

func reportFailure(productError error) *Failure {
	if productError == nil {
		return nil
	}
	return &Failure{Message: fmt.Sprint(productError), Details: ""}
}

func engineOutcomeToReport(outcome engine.Outcome) (Status, error) {
	switch outcome {
	case engine.Completed:
		return Completed, nil
	case engine.Skipped:
		return Skipped, nil
	case engine.Warned:
		return Warned, nil
	case engine.Failed:
		return Failed, nil
	case engine.Cancelled:
		return Cancelled, nil
	default:
		return Status(""), engine.NewConfigurationError(fmt.Sprintf("engine outcome %q is invalid", outcome), nil)
	}
}
