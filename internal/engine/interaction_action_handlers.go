package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

type batch2ADirectCompiled struct {
	keyword model.CommandKeyword
}

type batch2ADirectEvaluated struct {
	keyword model.CommandKeyword
	appID   string
}

type pressKeyCompiled struct {
	authored           string
	canonical          device.KeyCode
	requiresEvaluation bool
}

type pressKeyEvaluated struct {
	appID string
	code  device.KeyCode
}

func backHandlerSpec() handlerSpec {
	return batch2ADirectHandlerSpec(model.CommandBack)
}

func hideKeyboardHandlerSpec() handlerSpec {
	return batch2ADirectHandlerSpec(model.CommandHideKeyboard)
}

func scrollHandlerSpec() handlerSpec {
	return batch2ADirectHandlerSpec(model.CommandScroll)
}

func batch2ADirectHandlerSpec(keyword model.CommandKeyword) handlerSpec {
	return handlerSpec{
		keyword: keyword, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileBatch2ADirect), evaluate: evaluateBatch2ADirect, execute: executeBatch2ADirect,
	}
}

func pressKeyHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandPressKey, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle, exactErrorPolicy: exactErrorPublicationPressKey,
		compile: pureCompiler(compilePressKey), evaluate: evaluatePressKey, execute: executePressKey,
	}
}

func compileBatch2ADirect(command model.Command) (any, error) {
	switch command.Kind {
	case model.CommandBack, model.CommandHideKeyboard, model.CommandScroll:
	default:
		return nil, NewConfigurationError(fmt.Sprintf("%s is not a direct interaction command", command.Kind), nil)
	}
	if err := decodeNoArguments(command); err != nil {
		return nil, err
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "must be a bare command without metadata, selector, or children")
	}
	return batch2ADirectCompiled{keyword: command.Kind}, nil
}

func compilePressKey(command model.Command) (any, error) {
	if command.Kind != model.CommandPressKey {
		return nil, NewConfigurationError("pressKey keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "must contain only one string key value")
	}
	authored, err := decodeString(command)
	if err != nil {
		return nil, err
	}
	compiled := pressKeyCompiled{authored: authored, requiresEvaluation: js.HasInterpolationExpression(authored)}
	if compiled.requiresEvaluation {
		return compiled, nil
	}
	compiled.canonical, err = canonicalPressKey(authored)
	if err != nil {
		return nil, err
	}
	return compiled, nil
}

// canonicalPressKey normalizes an authored key through the shared model set used
// by both parser and compiler validation.
func canonicalPressKey(authored string) (device.KeyCode, error) {
	canonical, ok := model.PressKeyCanonical(authored)
	if !ok {
		return "", NewConfigurationError("command pressKey value is not in the exact supported set", nil)
	}
	return device.KeyCode(canonical), nil
}

func validCanonicalPressKey(code device.KeyCode) bool {
	_, ok := model.PressKeyCanonical(string(code))
	return ok
}

func evaluateBatch2ADirect(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(batch2ADirectCompiled)
	if !ok || plan.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: batch2ADirectEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	evaluated.value = batch2ADirectEvaluated{keyword: plan.keyword, appID: appID}
	return evaluated, nil
}

func evaluatePressKey(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(pressKeyCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("pressKey received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: pressKeyEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	authored := plan.authored
	if plan.requiresEvaluation {
		authored, err = evaluation.Interpolate(ctx, plan.authored, nil)
		if err != nil {
			return evaluated, err
		}
	}
	canonical := plan.canonical
	if plan.requiresEvaluation {
		canonical, err = canonicalPressKey(authored)
		if err != nil {
			return evaluated, err
		}
	}
	evaluated.command.Arguments = authored
	evaluated.value = pressKeyEvaluated{appID: appID, code: canonical}
	return evaluated, nil
}

func evaluatedActiveAppID(
	ctx context.Context,
	evaluation evaluationContext,
	keyword model.CommandKeyword,
) (string, error) {
	rawAppID, err := evaluation.ActiveAppID()
	if err != nil {
		return "", err
	}
	appID, err := evaluation.Interpolate(ctx, rawAppID, nil)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(appID) == "" {
		return "", NewConfigurationError(fmt.Sprintf("%s requires an active appId", keyword), nil)
	}
	return appID, nil
}

func executeBatch2ADirect(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(batch2ADirectEvaluated)
	if !ok {
		return effect, NewConfigurationError(
			fmt.Sprintf("%s received an invalid evaluated payload", evaluated.command.Kind), nil)
	}
	lookup, err := batch2AExecutionLookup(state, plan.keyword, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	switch plan.keyword {
	case model.CommandBack, model.CommandHideKeyboard:
		info, infoErr := lookup.cachedDeviceInfo(ctx)
		if infoErr != nil {
			return effect, infoErr
		}
		if err := validateBatch2APlatform(info.Platform, plan.keyword); err != nil {
			return effect, err
		}
		if err := ctx.Err(); err != nil {
			return effect, err
		}
		if plan.keyword == model.CommandBack {
			err = state.dependencies.Driver.BackPress(ctx)
		} else {
			err = state.dependencies.Driver.HideKeyboard(ctx)
		}
	case model.CommandScroll:
		err = state.dependencies.Driver.ScrollVertical(ctx, device.ScrollVerticalRequest{
			Direction: device.Direction("DOWN"), Amount: 0.40, ElementPoint: nil,
		})
	default:
		return effect, NewConfigurationError(
			fmt.Sprintf("%s is not executable as a direct interaction command", plan.keyword), nil)
	}
	if err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, plan.keyword, plan.appID, effect)
}

func executePressKey(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(pressKeyEvaluated)
	if !ok || !validCanonicalPressKey(plan.code) {
		return effect, NewConfigurationError("pressKey received an invalid evaluated payload", nil)
	}
	lookup, err := batch2AExecutionLookup(state, model.CommandPressKey, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.PressKeyRequest{Code: plan.code, AppIDs: []string{plan.appID}}
	if err := state.dependencies.Driver.PressKey(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		effect.exactErrorRequest = &exactErrorRequest{underlying: err}
		return effect, nil
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, model.CommandPressKey, plan.appID, effect)
}

func batch2AExecutionLookup(
	state *executionState,
	keyword model.CommandKeyword,
	appID string,
) (*ElementLookup, error) {
	if state == nil || isNilValue(state.dependencies.Driver) || isNilValue(state.dependencies.Clock) {
		return nil, NewConfigurationError(fmt.Sprintf("%s requires driver and clock services", keyword), nil)
	}
	if strings.TrimSpace(appID) == "" {
		return nil, NewConfigurationError(fmt.Sprintf("%s evaluated plan requires a non-blank appId", keyword), nil)
	}
	return state.elementLookup()
}

func validateBatch2APlatform(platform device.Platform, keyword model.CommandKeyword) error {
	switch platform {
	case device.Platform("android"), device.Platform("ios"), device.Platform("web"):
		return nil
	default:
		return NewConfigurationError(fmt.Sprintf("command %s requires platform android, ios, or web", keyword), nil)
	}
}

func finishBatch2APhysicalAction(
	ctx context.Context,
	state *executionState,
	lookup *ElementLookup,
	keyword model.CommandKeyword,
	appID string,
	effect commandEffect,
) (commandEffect, error) {
	lookup.RecordInteraction(state.dependencies.Clock.Now())
	if cancellation := ctx.Err(); cancellation != nil {
		return effect, cancellation
	}
	_, err := lookup.WaitForHierarchySettle(ctx, device.SettleRequest{AppID: appID})
	if err == nil {
		return effect, nil
	}
	normalized := normalizeTerminalError(fmt.Sprintf("command %s settle failed", keyword), err)
	switch classifyTerminalError(normalized) {
	case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
		return effect, normalized
	default:
		return effect, nil
	}
}
