package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// startLogCapture / stopLogCapture bracket one device-log stream. Like
// recording, the lifecycle lives in an injected controller because frozen
// Driver v0 hands out a capture id the engine has no place to keep; unlike
// recording, the start carries the flow's application so a driver that can
// filter by process does.

// deviceLogArtifactKind labels the finished capture in every report.
const deviceLogArtifactKind = "device-log"

type logCaptureCompiled struct {
	keyword model.CommandKeyword
	// name is the authored capture name, uninterpolated.
	name string
}

type logCaptureEvaluated struct {
	keyword model.CommandKeyword
	name    string
	appID   string
}

func logCaptureHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandStartLogCapture, effectClass: EffectHostMutation,
			compile: pureCompiler(compileLogCapture), evaluate: evaluateLogCapture, execute: executeLogCapture,
		},
		{
			keyword: model.CommandStopLogCapture, effectClass: EffectArtifact,
			compile: pureCompiler(compileLogCapture), evaluate: evaluateLogCapture, execute: executeLogCapture,
		},
	}
}

func compileLogCapture(command model.Command) (any, error) {
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	payload := logCaptureCompiled{keyword: command.Kind}
	switch command.Kind {
	case model.CommandStopLogCapture:
		if err := decodeNoArguments(command); err != nil {
			return nil, err
		}
		return payload, nil
	case model.CommandStartLogCapture:
		name, err := decodeString(command)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			return nil, commandDecodeError(command.Kind, "requires a non-blank name")
		}
		payload.name = name
		return payload, nil
	default:
		return nil, NewConfigurationError("log capture keyword is invalid", nil)
	}
}

func evaluateLogCapture(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(logCaptureCompiled)
	if !ok || payload.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(
			fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: logCaptureEvaluated{}}
	value := logCaptureEvaluated{keyword: payload.keyword}
	if payload.keyword == model.CommandStartLogCapture {
		name, err := evaluation.Interpolate(ctx, payload.name, nil)
		if err != nil {
			return evaluated, err
		}
		if strings.TrimSpace(name) == "" {
			return evaluated, NewConfigurationError(
				fmt.Sprintf("command %s name must not be blank after interpolation", command.Kind), nil)
		}
		value.name = name
		// The flow's application scopes the capture where the driver can.
		appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
		if err != nil {
			return evaluated, err
		}
		value.appID = appID
	}
	evaluated.value = value
	return evaluated, nil
}

func executeLogCapture(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	payload, ok := evaluated.value.(logCaptureEvaluated)
	if !ok {
		return commandEffect{}, NewConfigurationError("log capture command received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	switch payload.keyword {
	case model.CommandStartLogCapture:
		effect := commandEffect{effectClass: EffectHostMutation}
		return effect, state.startLogCapture(ctx, LogCaptureStartRequest{Name: payload.name, AppID: payload.appID})
	case model.CommandStopLogCapture:
		effect := commandEffect{effectClass: EffectArtifact}
		artifacts, err := state.stopLogCapture(ctx)
		if err != nil {
			return effect, err
		}
		effect.finalizedArtifacts = artifacts
		for _, artifact := range artifacts {
			effect.logMessages = append(effect.logMessages, describeDeviceLogArtifact(artifact))
		}
		return effect, nil
	default:
		return commandEffect{}, NewConfigurationError("log capture command keyword is invalid", nil)
	}
}

// describeDeviceLogArtifact is the one line a reader gets per capture: which
// file, which log source, and whether it covers the application or the whole
// device. Absent metadata is said to be absent rather than guessed.
func describeDeviceLogArtifact(artifact device.Artifact) string {
	parts := []string{artifact.Kind + " " + artifact.Path}
	if source := artifact.Metadata["source"]; source != "" {
		parts = append(parts, "source "+source)
	}
	switch artifact.Metadata["scope"] {
	case "app":
		parts = append(parts, "scope app "+artifact.Metadata["appId"])
	case "device":
		parts = append(parts, "scope device-wide")
	}
	if bytes := artifact.Metadata["bytes"]; bytes != "" {
		parts = append(parts, bytes+" bytes")
	}
	if artifact.Metadata["truncated"] == "true" {
		parts = append(parts, "truncated at the driver's byte cap")
	}
	return strings.Join(parts, " · ")
}
