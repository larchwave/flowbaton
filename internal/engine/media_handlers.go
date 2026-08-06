package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// Recording start/stop route through RecordingController rather than the
// driver: frozen Driver v0 declares StartScreenRecording with no stop
// counterpart, and the controller exists precisely to complete that lifecycle
// outside the frozen contract and return finalized artifacts.
//
// `assertScreenshot` uses a dedicated handler because it also needs expected
// resolution and ImageChecker thresholding.

// screenshotArtifactKind labels captures an author requested explicitly, so a
// consumer can tell them apart from the failure-evidence captures the executor
// takes on its own.
const screenshotArtifactKind = "screenshot"

type mediaCompiled struct {
	keyword model.CommandKeyword
	// name is the authored screenshot or recording name, uninterpolated.
	name string
	// paths is the addMedia file list, already resolved to canonical host
	// paths by the parser. Unlike a screenshot name these are not
	// interpolated: the capability preflight canonicalizes every media
	// prepared file before execution, so a path that still held a placeholder
	// could never have been resolved in the first place.
	paths []string
}

type mediaEvaluated struct {
	keyword model.CommandKeyword
	name    string
	paths   []string
}

func mediaHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandTakeScreenshot, effectClass: EffectArtifact,
			compile: pureCompiler(compileMedia), evaluate: evaluateMedia, execute: executeMedia,
		},
		{
			keyword: model.CommandStartRecording, effectClass: EffectHostMutation,
			compile: pureCompiler(compileMedia), evaluate: evaluateMedia, execute: executeMedia,
		},
		{
			keyword: model.CommandStopRecording, effectClass: EffectArtifact,
			compile: pureCompiler(compileMedia), evaluate: evaluateMedia, execute: executeMedia,
		},
		{
			keyword: model.CommandAddMedia, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compileMedia), evaluate: evaluateMedia, execute: executeMedia,
		},
	}
}

func compileMedia(command model.Command) (any, error) {
	// addMedia is the one media command that carries file links, so the
	// shared envelope check is applied to the rest of the envelope only.
	if command.Kind == model.CommandAddMedia {
		if err := rejectCommandEnvelopeExceptLinks(command); err != nil {
			return nil, err
		}
	} else if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	payload := mediaCompiled{keyword: command.Kind}
	switch command.Kind {
	case model.CommandStopRecording:
		if err := decodeNoArguments(command); err != nil {
			return nil, err
		}
		return payload, nil

	case model.CommandTakeScreenshot, model.CommandStartRecording:
		name, err := decodeString(command)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) == "" {
			return nil, commandDecodeError(command.Kind, "requires a non-blank name")
		}
		payload.name = name
		return payload, nil

	case model.CommandAddMedia:
		authored, err := decodeMediaPaths(command)
		if err != nil {
			return nil, err
		}
		if payload.paths, err = resolveMediaLinks(command, authored); err != nil {
			return nil, err
		}
		return payload, nil

	default:
		return nil, NewConfigurationError("media keyword is invalid", nil)
	}
}

// decodeMediaPaths accepts the single-path and ordered-list forms the parser
// already validates, and returns an owned slice so the authored command can
// never be reached through the driver request.
func decodeMediaPaths(command model.Command) ([]string, error) {
	if command.Form != model.CommandFormObject {
		return nil, commandDecodeError(command.Kind, "requires object form with a path or list of paths")
	}
	authored, isList := command.Arguments.([]any)
	if !isList {
		return nil, commandDecodeError(command.Kind, "argument must be a list of paths")
	}
	if len(authored) == 0 {
		return nil, commandDecodeError(command.Kind, "requires at least one path")
	}
	paths := make([]string, 0, len(authored))
	for _, entry := range authored {
		path, ok := entry.(string)
		if !ok {
			return nil, commandDecodeError(command.Kind, "every path must be a string")
		}
		if strings.TrimSpace(path) == "" {
			return nil, commandDecodeError(command.Kind, "path must not be blank")
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// resolveMediaLinks pairs every authored path with the prepared media
// link the parser produced for it, and returns the canonical host paths.
// The pairing is positional and total, so neither side can be forged alone.
func resolveMediaLinks(command model.Command, authored []string) ([]string, error) {
	if len(command.Links) != len(authored) {
		return nil, commandDecodeError(command.Kind, "requires exactly one prepared media link per path")
	}
	resolved := make([]string, 0, len(authored))
	for index, link := range command.Links {
		if link.Kind != model.FileLinkMedia {
			return nil, commandDecodeError(command.Kind, "file link must be media")
		}
		if link.Path != authored[index] {
			return nil, commandDecodeError(command.Kind, "path does not match its prepared media link")
		}
		if strings.TrimSpace(link.ResolvedPath) == "" {
			return nil, commandDecodeError(command.Kind, "media link is missing its resolved path")
		}
		resolved = append(resolved, link.ResolvedPath)
	}
	return resolved, nil
}

func evaluateMedia(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(mediaCompiled)
	if !ok || payload.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(
			fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: mediaEvaluated{}}
	value := mediaEvaluated{keyword: payload.keyword}
	if payload.name != "" {
		name, err := evaluation.Interpolate(ctx, payload.name, nil)
		if err != nil {
			return evaluated, err
		}
		if strings.TrimSpace(name) == "" {
			return evaluated, NewConfigurationError(
				fmt.Sprintf("command %s name must not be blank after interpolation", command.Kind), nil)
		}
		value.name = name
	}
	if len(payload.paths) != 0 {
		// Copied, not aliased: the compiled payload outlives every evaluation.
		value.paths = append(make([]string, 0, len(payload.paths)), payload.paths...)
	}
	evaluated.value = value
	return evaluated, nil
}

func executeMedia(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	payload, ok := evaluated.value.(mediaEvaluated)
	if !ok {
		return commandEffect{}, NewConfigurationError("media command received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	switch payload.keyword {
	case model.CommandTakeScreenshot:
		effect := commandEffect{effectClass: EffectArtifact}
		// The capture is handed to the executor as a write request rather than
		// written here, so artifact finalization stays owned by one place.
		screenshot, err := state.dependencies.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{})
		if err != nil {
			return effect, err
		}
		effect.artifactWrites = []ArtifactWriteRequest{{
			Kind: screenshotArtifactKind,
			// Named after the bytes: the author wrote a name, not a name minus
			// its type, and a file with no extension opens in nothing.
			SuggestedName: screenshotFileName(payload.name, screenshot),
			Data:          screenshot,
		}}
		return effect, nil

	case model.CommandStartRecording:
		effect := commandEffect{effectClass: EffectHostMutation}
		return effect, state.startRecording(ctx, RecordingStartRequest{Name: payload.name})

	case model.CommandStopRecording:
		effect := commandEffect{effectClass: EffectArtifact}
		artifacts, err := state.stopRecording(ctx)
		if err != nil {
			return effect, err
		}
		effect.finalizedArtifacts = artifacts
		return effect, nil

	case model.CommandAddMedia:
		effect := commandEffect{effectClass: EffectDeviceMutation}
		files := make([]device.MediaFile, 0, len(payload.paths))
		for _, path := range payload.paths {
			files = append(files, device.MediaFile{Path: path})
		}
		return effect, state.dependencies.Driver.AddMedia(ctx, device.AddMediaRequest{Files: files})

	default:
		return commandEffect{}, NewConfigurationError("media command keyword is invalid", nil)
	}
}
