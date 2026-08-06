package engine

import (
	"context"
	"fmt"
	"strconv"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

const eraseTextDefaultCount uint32 = 50
const eraseTextMaximumCount uint32 = 100

type inputTextCompiled struct {
	authoredText  string
	authoredLabel *string
	objectForm    bool
}

type inputTextEvaluated struct {
	text  string
	appID string
}

type eraseTextCompiled struct {
	count              uint32
	authoredCount      *string
	requiresEvaluation bool
}

type eraseTextEvaluated struct {
	count uint32
	appID string
}

func inputTextHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandInputText, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileInputText), evaluate: evaluateInputText, execute: executeInputText,
	}
}

func eraseTextHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandEraseText, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileEraseText), evaluate: evaluateEraseText, execute: executeEraseText,
	}
}

func compileInputText(command model.Command) (any, error) {
	if command.Kind != model.CommandInputText {
		return nil, NewConfigurationError("inputText keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "accepts only a scalar string or exact text/label object")
	}
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	compiled := inputTextCompiled{}
	switch {
	case decoded.stringValue != nil:
		if command.Label != nil {
			return nil, commandDecodeError(command.Kind, "scalar form must not carry a typed label")
		}
		compiled.authoredText = *decoded.stringValue
	case decoded.objectValue != nil:
		object := *decoded.objectValue
		if err := object.rejectUnknown("text", "label"); err != nil {
			return nil, err
		}
		compiled.authoredText, err = object.requireString("text")
		if err != nil {
			return nil, err
		}
		label, hasLabel, labelErr := object.optionalString("label")
		if labelErr != nil {
			return nil, labelErr
		}
		if hasLabel {
			compiled.authoredLabel = &label
		}
		if !stringPointersEqual(command.Label, compiled.authoredLabel) {
			return nil, NewConfigurationError("inputText raw label does not match its typed snapshot", nil)
		}
		compiled.objectForm = true
	default:
		return nil, commandDecodeError(command.Kind, "requires a scalar string or exact text/label object")
	}
	return compiled, nil
}

func compileEraseText(command model.Command) (any, error) {
	if command.Kind != model.CommandEraseText {
		return nil, NewConfigurationError("eraseText keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "accepts only bare, integer, or strict decimal string form")
	}
	if command.Form == model.CommandFormScalar {
		if command.Arguments != nil {
			return nil, commandDecodeError(command.Kind, "bare form must not carry arguments")
		}
		return eraseTextCompiled{count: eraseTextDefaultCount}, nil
	}
	if command.Form != model.CommandFormObject {
		return nil, commandDecodeError(command.Kind, "has an invalid command form")
	}
	switch value := command.Arguments.(type) {
	case int64:
		count, err := checkedEraseTextCount(value)
		if err != nil {
			return nil, err
		}
		return eraseTextCompiled{count: count}, nil
	case string:
		authored := value
		compiled := eraseTextCompiled{
			authoredCount: &authored, requiresEvaluation: js.HasInterpolationExpression(authored),
		}
		if compiled.requiresEvaluation {
			return compiled, nil
		}
		count, err := parseEraseTextCount(authored)
		if err != nil {
			return nil, err
		}
		compiled.count = count
		return compiled, nil
	default:
		return nil, commandDecodeError(command.Kind, "argument must be an integer or strict decimal string")
	}
}

func evaluateInputText(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(inputTextCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("inputText received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: inputTextEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	text, err := evaluation.Interpolate(ctx, plan.authoredText, nil)
	if err != nil {
		return evaluated, err
	}
	var label *string
	if plan.authoredLabel != nil {
		interpolated, labelErr := evaluation.Interpolate(ctx, *plan.authoredLabel, nil)
		if labelErr != nil {
			return evaluated, labelErr
		}
		label = &interpolated
	}
	if plan.objectForm {
		arguments := map[string]any{"text": text}
		if label != nil {
			arguments["label"] = *label
		}
		evaluated.command.Arguments = arguments
		evaluated.command.Label = clonePointer(label)
	} else {
		evaluated.command.Arguments = text
		evaluated.command.Label = nil
	}
	evaluated.value = inputTextEvaluated{text: text, appID: appID}
	return evaluated, nil
}

func evaluateEraseText(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(eraseTextCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("eraseText received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: eraseTextEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	count := plan.count
	if plan.authoredCount != nil {
		interpolated, interpolationErr := evaluation.Interpolate(ctx, *plan.authoredCount, nil)
		if interpolationErr != nil {
			return evaluated, interpolationErr
		}
		count, err = parseEraseTextCount(interpolated)
		if err != nil {
			return evaluated, err
		}
		evaluated.command.Arguments = interpolated
	}
	evaluated.value = eraseTextEvaluated{count: count, appID: appID}
	return evaluated, nil
}

func executeInputText(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(inputTextEvaluated)
	if !ok {
		return effect, NewConfigurationError("inputText received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("inputText execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	lookup, err := batch2AExecutionLookup(state, model.CommandInputText, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.InputTextRequest{Text: plan.text, AppIDs: []string{plan.appID}}
	if err := state.dependencies.Driver.InputText(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, model.CommandInputText, plan.appID, effect)
}

func executeEraseText(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(eraseTextEvaluated)
	if !ok || plan.count > eraseTextMaximumCount {
		return effect, NewConfigurationError("eraseText received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("eraseText execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	lookup, err := batch2AExecutionLookup(state, model.CommandEraseText, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.EraseTextRequest{CharactersToErase: plan.count, AppIDs: []string{plan.appID}}
	if err := state.dependencies.Driver.EraseText(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, model.CommandEraseText, plan.appID, effect)
}

func parseEraseTextCount(raw string) (uint32, error) {
	if raw == "" {
		return 0, NewConfigurationError("eraseText count must be a non-empty strict base-10 integer", nil)
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, NewConfigurationError("eraseText count must contain only ASCII base-10 digits", nil)
		}
	}
	parsed, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		return 0, NewConfigurationError("eraseText count overflows uint32", err)
	}
	if parsed > uint64(eraseTextMaximumCount) {
		return 0, NewConfigurationError(
			fmt.Sprintf("eraseText count must be in [0,%d]", eraseTextMaximumCount),
			nil,
		)
	}
	return uint32(parsed), nil
}

func checkedEraseTextCount(value int64) (uint32, error) {
	if value < 0 || value > int64(eraseTextMaximumCount) {
		return 0, NewConfigurationError(
			fmt.Sprintf("eraseText count must be in [0,%d]", eraseTextMaximumCount),
			nil,
		)
	}
	return uint32(value), nil
}
