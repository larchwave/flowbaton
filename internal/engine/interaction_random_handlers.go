package engine

import (
	"context"
	"fmt"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

const randomInputDefaultLength = 8

type randomInputDefinition struct {
	keyword        model.CommandKeyword
	kind           InputKind
	variableLength bool
}

type randomInputCompiled struct {
	keyword model.CommandKeyword
	kind    InputKind
	length  int
}

type randomInputEvaluated struct {
	keyword model.CommandKeyword
	kind    InputKind
	length  int
	appID   string
}

func randomInputDefinitions() []randomInputDefinition {
	return []randomInputDefinition{
		{keyword: model.CommandInputRandomText, kind: InputText, variableLength: true},
		{keyword: model.CommandInputRandomNumber, kind: InputNumber, variableLength: true},
		{keyword: model.CommandInputRandomEmail, kind: InputEmail},
		{keyword: model.CommandInputRandomPersonName, kind: InputPersonName},
		{keyword: model.CommandInputRandomCityName, kind: InputCityName},
		{keyword: model.CommandInputRandomCountryName, kind: InputCountryName},
		{keyword: model.CommandInputRandomColorName, kind: InputColorName},
	}
}

func randomInputDefinitionFor(keyword model.CommandKeyword) (randomInputDefinition, bool) {
	for _, definition := range randomInputDefinitions() {
		if definition.keyword == keyword {
			return definition, true
		}
	}
	return randomInputDefinition{}, false
}

func randomInputHandlerSpecs() []handlerSpec {
	definitions := randomInputDefinitions()
	specs := make([]handlerSpec, len(definitions))
	for index, definition := range definitions {
		specs[index] = handlerSpec{
			keyword: definition.keyword, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle, requiredService: requiredServiceInputGenerator,
			compile: pureCompiler(compileRandomInput), evaluate: evaluateRandomInput, execute: executeRandomInput,
		}
	}
	return specs
}

func compileRandomInput(command model.Command) (any, error) {
	definition, ok := randomInputDefinitionFor(command.Kind)
	if !ok {
		return nil, NewConfigurationError("random input keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "accepts only bare or exact length-object form")
	}
	object, _, err := decodeOptionalObject(command)
	if err != nil {
		return nil, err
	}
	length := inputNamedLengthSentinel
	if definition.variableLength {
		if err := object.rejectUnknown("length"); err != nil {
			return nil, err
		}
		length = randomInputDefaultLength
		if authored, exists, fieldErr := object.optionalInt("length"); fieldErr != nil {
			return nil, fieldErr
		} else if exists {
			if authored < 0 || authored > inputVariableMaximumLength {
				return nil, NewConfigurationError(
					fmt.Sprintf("command %s length must be in [0,%d]", command.Kind, inputVariableMaximumLength),
					nil,
				)
			}
			length = int(authored)
		}
	} else if err := object.rejectUnknown(); err != nil {
		return nil, err
	}
	return randomInputCompiled{keyword: definition.keyword, kind: definition.kind, length: length}, nil
}

func evaluateRandomInput(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(randomInputCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("random input received an invalid compiled payload", nil)
	}
	expectedValue, err := compileRandomInput(command)
	if err != nil {
		return evaluatedDispatch{}, err
	}
	expected := expectedValue.(randomInputCompiled)
	if plan != expected {
		return evaluatedDispatch{}, NewConfigurationError("random input compiled payload does not match its command snapshot", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: randomInputEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	evaluated.value = randomInputEvaluated{
		keyword: plan.keyword,
		kind:    plan.kind,
		length:  plan.length,
		appID:   appID,
	}
	return evaluated, nil
}

func executeRandomInput(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(randomInputEvaluated)
	if !ok || !validRandomInputEvaluated(plan, evaluated.command.Kind) {
		return effect, NewConfigurationError("random input received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("random input execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	lookup, err := batch2AExecutionLookup(state, plan.keyword, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	generated, err := state.generateInput(ctx, InputRequest{Kind: plan.kind, Length: plan.length})
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.InputTextRequest{Text: generated, AppIDs: []string{plan.appID}}
	if err := state.dependencies.Driver.InputText(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, plan.keyword, plan.appID, effect)
}

func validRandomInputEvaluated(plan randomInputEvaluated, keyword model.CommandKeyword) bool {
	definition, ok := randomInputDefinitionFor(keyword)
	if !ok || plan.keyword != keyword || plan.kind != definition.kind {
		return false
	}
	if definition.variableLength {
		return plan.length >= 0 && plan.length <= inputVariableMaximumLength
	}
	return plan.length == inputNamedLengthSentinel
}
