package engine

import (
	"context"
	"path"
	"strings"

	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

// specs/01-core-engine.md:98 is the whole contract: runScript evaluates the
// linked file with runInSubScope=true and scriptDir set, gated by `when`;
// evalScript interpolates `${...}` in the authored source and then evaluates
// it in the session scope, the way a script condition does. Both count as
// mutating, so both perform their effect in execute — running the script
// during evaluation would run the mutation a phase early and repeat it on
// every re-evaluation.

type scriptCompiled struct {
	keyword model.CommandKeyword
	// script is the authored evalScript body, uninterpolated.
	script string
	// resolvedPath is the runScript link's canonical host path.
	resolvedPath string
	environment  map[string]string
}

type scriptEvaluated struct {
	keyword      model.CommandKeyword
	script       string
	resolvedPath string
	environment  map[string]string
}

func scriptHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandRunScript, effectClass: EffectHostMutation,
			compile: pureCompiler(compileScript), evaluate: evaluateScript, execute: executeScript,
		},
		{
			keyword: model.CommandEvalScript, effectClass: EffectHostMutation,
			compile: pureCompiler(compileScript), evaluate: evaluateScript, execute: executeScript,
		},
	}
}

func compileScript(command model.Command) (any, error) {
	switch command.Kind {
	case model.CommandEvalScript:
		return compileEvalScript(command)
	case model.CommandRunScript:
		return compileRunScript(command)
	default:
		return nil, NewConfigurationError("script keyword is invalid", nil)
	}
}

func compileEvalScript(command model.Command) (any, error) {
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	if len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "must not carry file links")
	}
	script, err := decodeString(command)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(script) == "" {
		return nil, commandDecodeError(command.Kind, "requires a non-blank script")
	}
	return scriptCompiled{keyword: command.Kind, script: script}, nil
}

func compileRunScript(command model.Command) (any, error) {
	if len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain child commands")
	}
	if command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "must not carry a selector")
	}
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	file := ""
	var environment map[string]string
	if decoded.stringValue != nil {
		if command.Condition != nil || command.Label != nil || command.Optional != nil {
			return nil, commandDecodeError(command.Kind, "scalar file form has forged typed metadata")
		}
		file = *decoded.stringValue
	} else {
		object := *decoded.objectValue
		if err := object.rejectUnknown("file", "env", "when", "label"); err != nil {
			return nil, err
		}
		if file, err = object.requireString("file"); err != nil {
			return nil, err
		}
		if environment, _, err = object.optionalStringMap("env"); err != nil {
			return nil, err
		}
		if err := validateScriptTypedMetadata(object, command); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(file) == "" {
		return nil, commandDecodeError(command.Kind, "file must not be blank")
	}
	if len(command.Links) != 1 {
		return nil, commandDecodeError(command.Kind, "requires exactly one prepared script link")
	}
	link := command.Links[0]
	if link.Kind != model.FileLinkScript {
		return nil, commandDecodeError(command.Kind, "file link must be a script")
	}
	if link.Path != file {
		return nil, commandDecodeError(command.Kind, "file argument does not match its prepared script link")
	}
	if strings.TrimSpace(link.ResolvedPath) == "" {
		return nil, commandDecodeError(command.Kind, "script link is missing its resolved path")
	}
	return scriptCompiled{
		keyword: command.Kind, resolvedPath: link.ResolvedPath,
		environment: cloneStringMap(environment),
	}, nil
}

// validateScriptTypedMetadata requires the authored map and parser snapshot to
// agree so neither representation can be forged independently.
func validateScriptTypedMetadata(object decodedObject, command model.Command) error {
	label, hasLabel, err := object.optionalString("label")
	if err != nil {
		return err
	}
	if hasLabel != (command.Label != nil) || hasLabel && label != *command.Label {
		return NewConfigurationError("command runScript label does not match its typed snapshot", nil)
	}
	condition, hasCondition := object.raw("when")
	if hasCondition != (command.Condition != nil) {
		return NewConfigurationError("command runScript condition does not match its typed snapshot", nil)
	}
	if hasCondition {
		if _, ok := condition.(map[string]any); !ok {
			return object.fieldError("when", "must be an object")
		}
	}
	return nil
}

func evaluateScript(
	_ context.Context,
	_ evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(scriptCompiled)
	if !ok || payload.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError("script command received an invalid compiled payload", nil)
	}
	// Nothing is interpolated here: the script body and the env overlay are both
	// effects, and the condition is gated in execute alongside them.
	return evaluatedDispatch{
		command: cloneCommand(command),
		value: scriptEvaluated{
			keyword: payload.keyword, script: payload.script, resolvedPath: payload.resolvedPath,
			environment: cloneStringMap(payload.environment),
		},
	}, nil
}

func executeScript(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectHostMutation}
	payload, ok := evaluated.value.(scriptEvaluated)
	if !ok {
		return effect, NewConfigurationError("script command received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	runtime, err := state.jsRuntime()
	if err != nil {
		return effect, err
	}
	if payload.keyword == model.CommandEvalScript {
		return effect, evalAuthoredScript(ctx, runtime, payload.script, evaluated.command.Source.Path)
	}

	condition, matches, err := state.evaluateCondition(ctx, evaluated.command.Condition)
	evaluated.command.Condition = condition
	effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
	if err != nil {
		return effect, err
	}
	if !matches {
		return effect, NewCommandSkippedError("runScript condition was false", nil)
	}
	overlay, err := evaluateEnvironmentOverlay(ctx, state, payload.environment)
	if err != nil {
		return effect, err
	}
	source, err := state.readResource(ctx, ResourceReadRequest{Path: payload.resolvedPath})
	if err != nil {
		return effect, err
	}
	_, err = runtime.Evaluate(ctx, js.EvalRequest{
		Script: string(source.Data), SourceName: payload.resolvedPath,
		ScriptDir: path.Dir(payload.resolvedPath), Env: stringMapAsAny(overlay),
		RunInSubScope: true,
	})
	return effect, err
}

// evalAuthoredScript runs an inline evalScript body. Interpolation first keeps
// the documented `output.value = ${VALUE}` form working; the interpolated text
// is then evaluated as a statement so plain assignments take effect and a thrown
// error fails the command instead of being copied through as literal bytes.
func evalAuthoredScript(ctx context.Context, runtime js.Runtime, authored, sourcePath string) error {
	script, err := runtime.Interpolate(ctx, authored, nil)
	if err != nil {
		return err
	}
	request := js.EvalRequest{Script: script, SourceName: sourcePath}
	if sourcePath != "" {
		request.ScriptDir = path.Dir(sourcePath)
	}
	_, err = runtime.Evaluate(ctx, request)
	return err
}

func stringMapAsAny(source map[string]string) map[string]any {
	if len(source) == 0 {
		return nil
	}
	overlay := make(map[string]any, len(source))
	for name, value := range source {
		overlay[name] = value
	}
	return overlay
}
