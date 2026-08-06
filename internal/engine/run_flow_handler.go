package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/larchwave/flowbaton/internal/model"
)

type runFlowSource uint8

const (
	runFlowLinkedSource runFlowSource = iota + 1
	runFlowInlineSource
)

type runFlowCompiled struct {
	source      runFlowSource
	linked      *compiledFlow
	inlinePath  string
	environment map[string]string
}

type runFlowEvaluated struct {
	source      runFlowSource
	linked      *compiledFlow
	inlinePath  string
	environment map[string]string
}

func runFlowHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandRunFlow, effectClass: EffectComposite,
		compile: compileRunFlow, evaluate: evaluateRunFlow, execute: executeRunFlow,
	}
}

func compileRunFlow(_ context.Context, compileCtx compileContext, command model.Command) (any, error) {
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	if decoded.stringValue != nil {
		if strings.TrimSpace(*decoded.stringValue) == "" {
			return nil, commandDecodeError(command.Kind, "file path must not be blank")
		}
		if command.Condition != nil || command.Label != nil || command.Optional != nil {
			return nil, commandDecodeError(command.Kind, "scalar file form has forged typed metadata")
		}
		if len(command.Children) != 0 {
			return nil, commandDecodeError(command.Kind, "file form must not contain inline child commands")
		}
		return compileLinkedRunFlow(compileCtx, command, *decoded.stringValue, nil)
	}

	object := *decoded.objectValue
	if err := object.rejectUnknown("file", "env", "when", "commands", "label", "optional"); err != nil {
		return nil, err
	}
	_, hasFile := object.raw("file")
	commands, hasCommands := object.raw("commands")
	if hasFile == hasCommands {
		return nil, commandDecodeError(command.Kind, "requires exactly one of file or commands")
	}
	environment, _, err := object.optionalStringMap("env")
	if err != nil {
		return nil, err
	}
	if err := validateRunFlowTypedMetadata(object, command); err != nil {
		return nil, err
	}

	if hasFile {
		file, _, err := object.optionalString("file")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(file) == "" {
			return nil, object.fieldError("file", "must not be blank")
		}
		if len(command.Children) != 0 {
			return nil, commandDecodeError(command.Kind, "file form must not contain inline child commands")
		}
		return compileLinkedRunFlow(compileCtx, command, file, environment)
	}

	sequence, ok := commands.([]any)
	if !ok {
		return nil, object.fieldError("commands", "must be an array")
	}
	if len(sequence) != len(command.Children) {
		return nil, object.fieldError("commands", "does not match the typed child sequence")
	}
	if len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "inline form must not contain file links")
	}
	return runFlowCompiled{
		source: runFlowInlineSource, inlinePath: inlineRunFlowPath(compileCtx.FlowPath(), command.Source),
		environment: cloneStringMap(environment),
	}, nil
}

func compileLinkedRunFlow(
	compileCtx compileContext,
	command model.Command,
	file string,
	environment map[string]string,
) (any, error) {
	if len(command.Links) != 1 {
		return nil, commandDecodeError(command.Kind, "file form requires exactly one prepared flow link")
	}
	link := command.Links[0]
	if link.Kind != model.FileLinkFlow {
		return nil, commandDecodeError(command.Kind, "file link must be a flow")
	}
	if link.Path != file {
		return nil, commandDecodeError(command.Kind, "file argument does not match its prepared flow link")
	}
	child, err := compileCtx.RequireFlow(link)
	if err != nil {
		return nil, err
	}
	if child == nil {
		return nil, NewConfigurationError("runFlow linked compiler returned a nil flow", nil)
	}
	return runFlowCompiled{
		source: runFlowLinkedSource, linked: child,
		environment: cloneStringMap(environment),
	}, nil
}

func validateRunFlowTypedMetadata(object decodedObject, command model.Command) error {
	label, hasLabel, err := object.optionalString("label")
	if err != nil {
		return err
	}
	if hasLabel != (command.Label != nil) || hasLabel && label != *command.Label {
		return NewConfigurationError("command runFlow label does not match its typed snapshot", nil)
	}
	optional, hasOptional, err := object.optionalBool("optional")
	if err != nil {
		return err
	}
	if hasOptional != (command.Optional != nil) || hasOptional && optional != *command.Optional {
		return NewConfigurationError("command runFlow optional flag does not match its typed snapshot", nil)
	}
	condition, hasCondition := object.raw("when")
	if hasCondition != (command.Condition != nil) {
		return NewConfigurationError("command runFlow condition does not match its typed snapshot", nil)
	}
	if hasCondition {
		if _, ok := condition.(map[string]any); !ok {
			return object.fieldError("when", "must be an object")
		}
	}
	return nil
}

func inlineRunFlowPath(containingPath string, source model.SourceInfo) string {
	base := strings.TrimSpace(containingPath)
	if base == "" {
		base = strings.TrimSpace(source.Path)
	}
	if base == "" {
		base = "<inline-flow>"
	}
	return fmt.Sprintf(
		"%s%s%d:%d:%d",
		base, inlineFlowPathMarker, source.Start.Line, source.Start.Column, source.Start.Offset,
	)
}

func evaluateRunFlow(
	_ context.Context,
	_ evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(runFlowCompiled)
	if !ok || !payload.valid() {
		return evaluatedDispatch{}, NewConfigurationError("runFlow received an invalid compiled payload", nil)
	}
	return evaluatedDispatch{
		command: cloneCommand(command),
		value: runFlowEvaluated{
			source: payload.source, linked: payload.linked, inlinePath: payload.inlinePath,
			environment: cloneStringMap(payload.environment),
		},
	}, nil
}

func (payload runFlowCompiled) valid() bool {
	switch payload.source {
	case runFlowLinkedSource:
		return payload.linked != nil && payload.inlinePath == ""
	case runFlowInlineSource:
		return payload.linked == nil && strings.TrimSpace(payload.inlinePath) != ""
	default:
		return false
	}
}

func executeRunFlow(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	payload, ok := evaluated.value.(runFlowEvaluated)
	if !ok || !payload.valid() {
		return commandEffect{}, NewConfigurationError("runFlow received an invalid evaluated payload", nil)
	}
	condition, matches, err := state.evaluateCondition(ctx, evaluated.command.Condition)
	evaluated.command.Condition = condition
	if err != nil {
		return commandEffect{evaluatedCommand: clonedCommandPointer(evaluated.command)}, err
	}
	if !matches {
		return commandEffect{
			effectClass: EffectComposite, evaluatedCommand: clonedCommandPointer(evaluated.command),
		}, NewCommandSkippedError("runFlow condition was false", nil)
	}

	overlay, err := evaluateEnvironmentOverlay(ctx, state, payload.environment)
	if err != nil {
		return commandEffect{evaluatedCommand: clonedCommandPointer(evaluated.command)}, err
	}
	synchronizeRunFlowEnvironment(&evaluated.command, overlay)

	switch payload.source {
	case runFlowLinkedSource:
		_, err = state.executeFlow(ctx, payload.linked, overlay)
	case runFlowInlineSource:
		var config model.Config
		config, err = state.activeConfig()
		if err == nil {
			config.Env = nil
			config.OnFlowStart = nil
			config.OnFlowComplete = nil
			var body []compiledDispatch
			body, err = runFlowInlineChildren(state, evaluated.command.Children)
			if err == nil {
				_, err = state.executeFlow(ctx, &compiledFlow{
					path: payload.inlinePath, config: config, body: body,
				}, overlay)
			}
		}
	default:
		err = NewConfigurationError("runFlow source is invalid", nil)
	}
	return commandEffect{
		effectClass: EffectComposite, evaluatedCommand: clonedCommandPointer(evaluated.command),
	}, err
}

func (payload runFlowEvaluated) valid() bool {
	switch payload.source {
	case runFlowLinkedSource:
		return payload.linked != nil && payload.inlinePath == ""
	case runFlowInlineSource:
		return payload.linked == nil && strings.TrimSpace(payload.inlinePath) != ""
	default:
		return false
	}
}

func evaluateEnvironmentOverlay(
	ctx context.Context,
	state *executionState,
	source map[string]string,
) (map[string]string, error) {
	if len(source) == 0 {
		return cloneStringMap(source), nil
	}
	runtime, err := state.jsRuntime()
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(source))
	for key := range source {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	evaluated := make(map[string]string, len(source))
	for _, key := range keys {
		value, err := runtime.Interpolate(ctx, source[key], nil)
		if err != nil {
			return nil, err
		}
		evaluated[key] = value
	}
	return evaluated, nil
}

func synchronizeRunFlowEnvironment(command *model.Command, environment map[string]string) {
	if command == nil || environment == nil {
		return
	}
	arguments, ok := command.Arguments.(map[string]any)
	if !ok {
		return
	}
	values := make(map[string]any, len(environment))
	for key, value := range environment {
		values[key] = value
	}
	arguments["env"] = values
}

func runFlowInlineChildren(state *executionState, commands []model.Command) ([]compiledDispatch, error) {
	if state == nil {
		return nil, NewConfigurationError("runFlow execution state must not be nil", nil)
	}
	if len(commands) != len(state.compiledChildren) {
		return nil, NewConfigurationError("runFlow inline commands do not match the precompiled sequence", nil)
	}
	for index, command := range commands {
		if !command.Equivalent(state.compiledChildren[index].command) {
			return nil, NewConfigurationError("runFlow inline commands do not match the precompiled sequence", nil)
		}
	}
	return append([]compiledDispatch(nil), state.compiledChildren...), nil
}

func clonedCommandPointer(command model.Command) *model.Command {
	cloned := cloneCommand(command)
	return &cloned
}
