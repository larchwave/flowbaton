package engine

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

// parseTimeoutMillis resolves a post-interpolation timeout string as a strict
// non-negative base-10 integer at runtime.
func parseTimeoutMillis(kind model.CommandKeyword, value string) (int64, error) {
	if value == "" {
		return 0, commandDecodeError(kind, "timeout must be a base-10 integer string")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, commandDecodeError(kind, "timeout must be a base-10 integer string")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, commandDecodeError(kind, "timeout is outside the supported integer range")
	}
	return parsed, nil
}

type assertNotVisibleCompiled struct{}

type assertNotVisibleEvaluated struct{}

type assertTrueCompiled struct {
	condition string
}

type assertTrueEvaluated struct {
	matched bool
}

type extendedWaitUntilCompiled struct {
	timeout                   *time.Duration
	timeoutSource             string
	timeoutRequiresEvaluation bool
}

type extendedWaitUntilEvaluated struct {
	timeout    *time.Duration
	evaluation evaluationContext
}

func assertNotVisibleHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandAssertNotVisible, effectClass: EffectObserved,
		compile:  pureCompiler(compileAssertNotVisible),
		evaluate: evaluateAssertNotVisible,
		execute:  executeAssertNotVisible,
	}
}

func assertTrueHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandAssertTrue, effectClass: EffectObserved,
		compile:  pureCompiler(compileAssertTrue),
		evaluate: evaluateAssertTrue,
		execute:  executeAssertTrue,
	}
}

func extendedWaitUntilHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandExtendedWaitUntil, effectClass: EffectObserved,
		compile:  pureCompiler(compileExtendedWaitUntil),
		evaluate: evaluateExtendedWaitUntil,
		execute:  executeExtendedWaitUntil,
	}
}

func compileAssertNotVisible(command model.Command) (any, error) {
	if err := validateSelectorCommand(command); err != nil {
		return nil, err
	}
	if !selectorCommandSnapshotMatches(command) {
		return nil, NewConfigurationError("command assertNotVisible selector or optional flag does not match its typed snapshot", nil)
	}
	if err := rejectUnsupportedSelectorFeatures(command.Kind, command.Selector, false, true); err != nil {
		return nil, err
	}
	return assertNotVisibleCompiled{}, nil
}

func compileAssertTrue(command model.Command) (any, error) {
	if command.Selector != nil || command.Condition != nil {
		return nil, commandDecodeError(command.Kind, "must not contain selector or condition metadata")
	}
	if len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain child commands")
	}
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	if decoded.stringValue != nil {
		if command.Label != nil || command.Optional != nil {
			return nil, commandDecodeError(command.Kind, "scalar condition must not contain label or optional metadata")
		}
		return assertTrueCompiled{condition: *decoded.stringValue}, nil
	}
	if decoded.objectValue == nil {
		return nil, commandDecodeError(command.Kind, "requires a condition")
	}
	object := *decoded.objectValue
	if err := object.rejectUnknown("condition", "label", "optional"); err != nil {
		return nil, err
	}
	condition, err := object.requireString("condition")
	if err != nil {
		return nil, err
	}
	if err := validateAssertTrueTypedMetadata(object, command); err != nil {
		return nil, err
	}
	return assertTrueCompiled{condition: condition}, nil
}

func validateAssertTrueTypedMetadata(object decodedObject, command model.Command) error {
	label, hasLabel, err := object.optionalString("label")
	if err != nil {
		return err
	}
	if hasLabel != (command.Label != nil) || hasLabel && label != *command.Label {
		return NewConfigurationError("command assertTrue label does not match its typed snapshot", nil)
	}
	optional, hasOptional, err := object.optionalBool("optional")
	if err != nil {
		return err
	}
	if hasOptional != (command.Optional != nil) || hasOptional && optional != *command.Optional {
		return NewConfigurationError("command assertTrue optional flag does not match its typed snapshot", nil)
	}
	return nil
}

func compileExtendedWaitUntil(command model.Command) (any, error) {
	// `label` and `optional` are universal metadata.
	if command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "contains unsupported typed metadata")
	}
	if len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain child commands")
	}
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("visible", "notVisible", "timeout"); err != nil {
		return nil, err
	}
	if command.Condition == nil || command.Condition.Visible == nil && command.Condition.NotVisible == nil {
		return nil, commandDecodeError(command.Kind, "requires visible or notVisible")
	}
	if command.Condition.Platform != nil || command.Condition.ScriptCondition != nil ||
		command.Condition.Label != nil || command.Condition.Optional != nil {
		return nil, commandDecodeError(command.Kind, "contains unsupported condition metadata")
	}
	for _, entry := range []struct {
		name     string
		selector *model.ElementSelector
	}{
		{name: "visible", selector: command.Condition.Visible},
		{name: "notVisible", selector: command.Condition.NotVisible},
	} {
		raw, present := object.raw(entry.name)
		if present != (entry.selector != nil) {
			return nil, NewConfigurationError(
				fmt.Sprintf("command extendedWaitUntil %s does not match its typed snapshot", entry.name),
				nil,
			)
		}
		if entry.selector == nil {
			continue
		}
		if !selectorArgumentsMatch(raw, entry.selector) {
			return nil, NewConfigurationError(
				fmt.Sprintf("command extendedWaitUntil %s does not match its typed snapshot", entry.name),
				nil,
			)
		}
		if err := validateImplementedSelectorTargets(command.Kind, entry.selector, entry.name); err != nil {
			return nil, err
		}
		if err := rejectUnsupportedSelectorFeatures(command.Kind, entry.selector, false, true); err != nil {
			return nil, err
		}
	}
	payload := extendedWaitUntilCompiled{}
	if timeout, exists := object.raw("timeout"); exists {
		switch value := timeout.(type) {
		case int64:
			duration, err := durationFromMilliseconds(value)
			if err != nil {
				return nil, NewConfigurationError("command extendedWaitUntil timeout is out of range", err)
			}
			payload.timeout = &duration
		case string:
			payload.timeoutSource = value
			payload.timeoutRequiresEvaluation = js.HasInterpolationExpression(value)
			if !payload.timeoutRequiresEvaluation {
				millis, parseErr := parseTimeoutMillis(model.CommandExtendedWaitUntil, value)
				if parseErr != nil {
					return nil, parseErr
				}
				duration, durationErr := durationFromMilliseconds(millis)
				if durationErr != nil {
					return nil, NewConfigurationError("command extendedWaitUntil timeout is out of range", durationErr)
				}
				payload.timeout = &duration
			}
		default:
			return nil, commandDecodeError(command.Kind, "timeout must be an integer or string")
		}
	}
	return payload, nil
}

func durationFromMilliseconds(milliseconds int64) (time.Duration, error) {
	if milliseconds <= 0 {
		return 0, nil
	}
	const maxMilliseconds = int64((1<<63 - 1) / int64(time.Millisecond))
	if milliseconds > maxMilliseconds {
		return 0, fmt.Errorf("%dms exceeds time.Duration", milliseconds)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func evaluateAssertNotVisible(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if _, ok := compiled.(assertNotVisibleCompiled); !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertNotVisible received an invalid compiled payload", nil)
	}
	evaluated, err := evaluateSelectorCommand(ctx, evaluation, command)
	evaluated.value = assertNotVisibleEvaluated{}
	return evaluated, err
}

func evaluateAssertTrue(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(assertTrueCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertTrue received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{
		command: cloneCommand(command),
		value:   assertTrueEvaluated{},
	}
	condition := payload.condition
	conditionSnapshot, matched, err := evaluateCondition(ctx, evaluation, nil, &model.Condition{
		ScriptCondition: &condition,
		Source:          command.Source,
	})
	if conditionSnapshot != nil && conditionSnapshot.ScriptCondition != nil {
		condition = *conditionSnapshot.ScriptCondition
		evaluated.command.Arguments = synchronizeAssertTrueArguments(evaluated.command.Arguments, condition, evaluated.command.Label)
	}
	if err != nil {
		return evaluated, err
	}
	if evaluated.command.Label != nil {
		label, interpolationErr := evaluation.Interpolate(ctx, *evaluated.command.Label, nil)
		if interpolationErr != nil {
			return evaluated, interpolationErr
		}
		evaluated.command.Label = &label
		evaluated.command.Arguments = synchronizeAssertTrueArguments(evaluated.command.Arguments, condition, &label)
	}
	evaluated.value = assertTrueEvaluated{matched: matched}
	return evaluated, nil
}

func synchronizeAssertTrueArguments(arguments any, condition string, label *string) any {
	if _, scalar := arguments.(string); scalar {
		return condition
	}
	fields, ok := arguments.(map[string]any)
	if !ok {
		return cloneDynamic(arguments)
	}
	synchronized := cloneDynamic(fields).(map[string]any)
	if _, exists := synchronized["condition"]; exists {
		synchronized["condition"] = condition
	}
	if _, exists := synchronized["label"]; exists && label != nil {
		synchronized["label"] = *label
	}
	return synchronized
}

func evaluateExtendedWaitUntil(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(extendedWaitUntilCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("extendedWaitUntil received an invalid compiled payload", nil)
	}
	if ctx == nil {
		return evaluatedDispatch{}, NewConfigurationError("extendedWaitUntil evaluation context must not be nil", nil)
	}
	timeout := clonePointer(payload.timeout)
	if payload.timeoutRequiresEvaluation {
		resolved, err := evaluation.Interpolate(ctx, payload.timeoutSource, nil)
		if err != nil {
			return evaluatedDispatch{command: cloneCommand(command)}, err
		}
		millis, err := parseTimeoutMillis(model.CommandExtendedWaitUntil, resolved)
		if err != nil {
			return evaluatedDispatch{command: cloneCommand(command)}, err
		}
		duration, err := durationFromMilliseconds(millis)
		if err != nil {
			return evaluatedDispatch{command: cloneCommand(command)}, NewConfigurationError("command extendedWaitUntil timeout is out of range", err)
		}
		timeout = &duration
	}
	evaluated := evaluatedDispatch{
		command: cloneCommand(command),
		value: extendedWaitUntilEvaluated{
			timeout:    timeout,
			evaluation: evaluation,
		},
	}
	if err := ctx.Err(); err != nil {
		return evaluated, err
	}
	if evaluated.command.Condition == nil {
		return evaluated, NewConfigurationError("extendedWaitUntil requires a condition", nil)
	}
	return evaluated, nil
}

func synchronizeExtendedWaitArguments(arguments any, field string, selector *model.ElementSelector) any {
	fields, ok := arguments.(map[string]any)
	if !ok {
		return cloneDynamic(arguments)
	}
	synchronized := cloneDynamic(fields).(map[string]any)
	if original, exists := synchronized[field]; exists {
		synchronized[field] = synchronizeSelectorArguments(original, selector)
	}
	return synchronized
}

func executeAssertNotVisible(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	if _, ok := evaluated.value.(assertNotVisibleEvaluated); !ok {
		return commandEffect{}, NewConfigurationError("assertNotVisible received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return commandEffect{}, NewConfigurationError("assertNotVisible execution context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	if evaluated.command.Selector == nil {
		return commandEffect{}, NewConfigurationError("assertNotVisible requires an evaluated selector", nil)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return commandEffect{}, err
	}
	deadline := lookup.adjustedDeadline(LookupOptions{Optional: commandIsOptional(evaluated.command)})
	absent, err := lookup.conditionNotVisibleUntil(ctx, *evaluated.command.Selector, deadline)
	if err != nil {
		return commandEffect{}, err
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	if !absent {
		return commandEffect{}, NewAssertionError("assertNotVisible target remained visible", nil)
	}
	return commandEffect{effectClass: EffectObserved}, nil
}

func executeAssertTrue(
	ctx context.Context,
	_ *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	payload, ok := evaluated.value.(assertTrueEvaluated)
	if !ok {
		return commandEffect{}, NewConfigurationError("assertTrue received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return commandEffect{}, NewConfigurationError("assertTrue execution context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	if !payload.matched {
		return commandEffect{}, NewAssertionError("assertTrue condition was false", nil)
	}
	return commandEffect{effectClass: EffectObserved}, nil
}

func executeExtendedWaitUntil(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	payload, ok := evaluated.value.(extendedWaitUntilEvaluated)
	if !ok {
		return commandEffect{}, NewConfigurationError("extendedWaitUntil received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return commandEffect{}, NewConfigurationError("extendedWaitUntil execution context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return commandEffect{}, err
	}
	if evaluated.command.Condition == nil ||
		evaluated.command.Condition.Visible == nil && evaluated.command.Condition.NotVisible == nil {
		return commandEffect{}, NewConfigurationError("extendedWaitUntil requires an evaluated predicate", nil)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return commandEffect{}, err
	}
	effect := commandEffect{
		effectClass:      EffectObserved,
		evaluatedCommand: clonedCommandPointer(evaluated.command),
	}
	deadline := lookup.adjustedDeadline(LookupOptions{Timeout: payload.timeout})
	condition := evaluated.command.Condition
	if condition.Visible != nil {
		if err := evaluateConditionSelector(ctx, payload.evaluation, condition.Visible, "visible"); err != nil {
			effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
			return effect, err
		}
		evaluated.command.Arguments = synchronizeExtendedWaitArguments(
			evaluated.command.Arguments,
			"visible",
			condition.Visible,
		)
		effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
		element, findErr := lookup.findUntil(ctx, *condition.Visible, LookupOptions{Optional: true}, deadline)
		if findErr != nil {
			return effect, findErr
		}
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		if element == nil {
			return effect, NewAssertionError("extendedWaitUntil visible target not found", nil)
		}
	}
	if condition.NotVisible != nil {
		if err := evaluateConditionSelector(ctx, payload.evaluation, condition.NotVisible, "notVisible"); err != nil {
			effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
			return effect, err
		}
		evaluated.command.Arguments = synchronizeExtendedWaitArguments(
			evaluated.command.Arguments,
			"notVisible",
			condition.NotVisible,
		)
		effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
		absent, waitErr := lookup.conditionNotVisibleUntil(ctx, *condition.NotVisible, deadline)
		if waitErr != nil {
			return effect, waitErr
		}
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		if !absent {
			return effect, NewAssertionError("extendedWaitUntil notVisible target remained visible", nil)
		}
	}
	return effect, nil
}
