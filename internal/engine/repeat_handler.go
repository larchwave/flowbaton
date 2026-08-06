package engine

import (
	"context"
	"math"
	"strconv"

	"github.com/larchwave/flowbaton/internal/model"
)

type repeatCounterKind uint8

const (
	repeatCounterMissing repeatCounterKind = iota + 1
	repeatCounterFixed
	repeatCounterDynamic
)

type repeatCompiled struct {
	counterKind     repeatCounterKind
	fixedTimes      int
	timesExpression string
	hasCondition    bool
}

type repeatEvaluated struct {
	times        int
	hasCondition bool
}

type repeatDeferredEvaluation struct {
	compiled repeatCompiled
}

func repeatHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandRepeat, effectClass: EffectComposite,
		compile: compileRepeat, evaluate: evaluateRepeatForExecution, execute: executeRepeat,
	}
}

func evaluateRepeatForExecution(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(repeatCompiled)
	if !ok || !payload.valid() {
		return evaluatedDispatch{}, NewConfigurationError("repeat received an invalid compiled payload", nil)
	}
	if payload.counterKind != repeatCounterDynamic {
		return evaluateRepeat(ctx, evaluation, command, payload)
	}
	return evaluatedDispatch{
		command: cloneCommand(command),
		value: repeatDeferredEvaluation{
			compiled: payload,
		},
	}, nil
}

func compileRepeat(_ context.Context, _ compileContext, command model.Command) (any, error) {
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("times", "while", "commands"); err != nil {
		return nil, err
	}
	if len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain file links")
	}
	// `label` and `optional` are universal metadata.

	rawCommands, hasCommands := object.raw("commands")
	if !hasCommands {
		return nil, object.fieldError("commands", "is required")
	}
	sequence, ok := rawCommands.([]any)
	if !ok {
		return nil, object.fieldError("commands", "must be an array")
	}
	if len(sequence) != len(command.Children) {
		return nil, object.fieldError("commands", "does not match the typed child sequence")
	}

	rawCondition, hasCondition := object.raw("while")
	if hasCondition != (command.Condition != nil) {
		return nil, NewConfigurationError("command repeat condition does not match its typed snapshot", nil)
	}
	if hasCondition {
		if _, ok := rawCondition.(map[string]any); !ok {
			return nil, object.fieldError("while", "must be an object")
		}
	}

	payload := repeatCompiled{counterKind: repeatCounterMissing, hasCondition: hasCondition}
	rawTimes, hasTimes := object.raw("times")
	if !hasTimes {
		return payload, nil
	}
	switch value := rawTimes.(type) {
	case int64:
		if value < 0 || value > math.MaxInt32 {
			return nil, object.fieldError("times", "must be a nonnegative Int32")
		}
		payload.counterKind = repeatCounterFixed
		payload.fixedTimes = int(value)
	case string:
		payload.counterKind = repeatCounterDynamic
		payload.timesExpression = value
	default:
		return nil, object.fieldError("times", "must be an int64 integer or string")
	}
	return payload, nil
}

func evaluateRepeat(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(repeatCompiled)
	if !ok || !payload.valid() {
		return evaluatedDispatch{}, NewConfigurationError("repeat received an invalid compiled payload", nil)
	}
	evaluated := cloneCommand(command)
	times := math.MaxInt32
	switch payload.counterKind {
	case repeatCounterMissing:
	case repeatCounterFixed:
		times = payload.fixedTimes
	case repeatCounterDynamic:
		interpolated, err := evaluation.Interpolate(ctx, payload.timesExpression, nil)
		if err != nil {
			return evaluatedDispatch{command: evaluated}, err
		}
		parsed, err := parseRepeatTimes(interpolated)
		if err != nil {
			return evaluatedDispatch{command: evaluated}, err
		}
		times = parsed
		synchronizeRepeatTimes(&evaluated, interpolated)
	default:
		return evaluatedDispatch{}, NewConfigurationError("repeat counter kind is invalid", nil)
	}
	return evaluatedDispatch{
		command: evaluated,
		value:   repeatEvaluated{times: times, hasCondition: payload.hasCondition},
	}, nil
}

func (payload repeatCompiled) valid() bool {
	switch payload.counterKind {
	case repeatCounterMissing:
		return payload.fixedTimes == 0 && payload.timesExpression == ""
	case repeatCounterFixed:
		return payload.fixedTimes >= 0 && payload.fixedTimes <= math.MaxInt32 && payload.timesExpression == ""
	case repeatCounterDynamic:
		return payload.fixedTimes == 0
	default:
		return false
	}
}

func parseRepeatTimes(value string) (int, error) {
	if value == "" {
		return 0, NewConfigurationError("command repeat field times must be an unsigned base-10 Int32", nil)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, NewConfigurationError("command repeat field times must be an unsigned base-10 Int32", nil)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 31)
	if err != nil {
		return 0, NewConfigurationError("command repeat field times must be an unsigned base-10 Int32", err)
	}
	return int(parsed), nil
}

func synchronizeRepeatTimes(command *model.Command, value string) {
	if command == nil {
		return
	}
	arguments, ok := command.Arguments.(map[string]any)
	if ok {
		arguments["times"] = value
	}
}

func executeRepeat(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{
		effectClass: EffectComposite, numberOfRunsSet: true,
		evaluatedCommand: clonedCommandPointer(evaluated.command),
	}
	if deferred, ok := evaluated.value.(repeatDeferredEvaluation); ok {
		evaluation, err := captureEvaluationContext(state)
		if err != nil {
			return effect, err
		}
		resolved, err := evaluateRepeat(ctx, evaluation, evaluated.command, deferred.compiled)
		if resolved.command.Kind != "" {
			effect.evaluatedCommand = clonedCommandPointer(resolved.command)
		}
		if err != nil {
			return effect, err
		}
		evaluated = resolved
		effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
	}
	payload, ok := evaluated.value.(repeatEvaluated)
	if !ok || payload.times < 0 || payload.times > math.MaxInt32 ||
		payload.hasCondition != (evaluated.command.Condition != nil) {
		return effect, NewConfigurationError("repeat received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("repeat execution context must not be nil", nil)
	}
	if payload.times == 0 {
		return effect, nil
	}
	if state == nil {
		return effect, NewConfigurationError("repeat execution state must not be nil", nil)
	}

	conditionSource := cloneCondition(evaluated.command.Condition)
	condition, shouldRun, err := state.evaluateCondition(ctx, conditionSource)
	evaluated.command.Condition = condition
	effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
	if err != nil || !shouldRun {
		return effect, err
	}

	for {
		effect.numberOfRuns++
		children, childErr := state.executeChildren(ctx, evaluated.command.Children)
		metadataErr := state.updateCommandMetadata(ctx, NewCommandMetadata(
			effect.numberOfRuns, effect.evaluatedCommand, nil, "", "",
		))
		if childErr != nil {
			return effect, childErr
		}
		if metadataErr != nil {
			return effect, metadataErr
		}
		if effect.numberOfRuns >= payload.times {
			return effect, nil
		}

		condition, shouldRun, err = state.evaluateCondition(ctx, conditionSource)
		evaluated.command.Condition = condition
		effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
		if err != nil || !shouldRun {
			return effect, err
		}
		if isNilValue(state.dependencies.Clock) {
			return effect, NewConfigurationError("repeat clock is unavailable", nil)
		}
		if err := state.dependencies.Clock.Wait(ctx, RepeatDelay); err != nil {
			return effect, err
		}
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		for _, child := range children {
			if err := state.resetCommand(ctx, child); err != nil {
				return effect, err
			}
		}
	}
}
