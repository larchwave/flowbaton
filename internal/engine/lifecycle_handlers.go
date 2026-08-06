package engine

import (
	"context"
	"fmt"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

// App-lifecycle handlers. stopApp, killApp and clearState take an
// optional appId and fall back to the active one; clearKeychain is device-wide
// and takes no argument at all. Each maps onto exactly one Driver method and
// none of them settles — only launchApp owns a settle policy.

type lifecycleCompiled struct {
	keyword       model.CommandKeyword
	explicitAppID *string
}

type lifecycleEvaluated struct {
	keyword model.CommandKeyword
	appID   string
}

func lifecycleHandlerSpecs() []handlerSpec {
	keywords := []model.CommandKeyword{
		model.CommandStopApp,
		model.CommandKillApp,
		model.CommandClearState,
		model.CommandClearKeychain,
	}
	specs := make([]handlerSpec, len(keywords))
	for index, keyword := range keywords {
		specs[index] = handlerSpec{
			keyword: keyword, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compileLifecycle), evaluate: evaluateLifecycle, execute: executeLifecycle,
		}
	}
	return specs
}

func lifecycleTakesAppID(keyword model.CommandKeyword) bool {
	switch keyword {
	case model.CommandStopApp, model.CommandKillApp, model.CommandClearState:
		return true
	default:
		return false
	}
}

func compileLifecycle(command model.Command) (any, error) {
	if command.Kind != model.CommandClearKeychain && !lifecycleTakesAppID(command.Kind) {
		return nil, NewConfigurationError("lifecycle keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "must be a bare command without metadata, selector, or children")
	}
	if !lifecycleTakesAppID(command.Kind) {
		if err := decodeNoArguments(command); err != nil {
			return nil, err
		}
		return lifecycleCompiled{keyword: command.Kind}, nil
	}
	payload := lifecycleCompiled{keyword: command.Kind}
	if command.Arguments == nil {
		return payload, nil
	}
	authored, err := decodeString(command)
	if err != nil {
		return nil, err
	}
	payload.explicitAppID = &authored
	return payload, nil
}

func evaluateLifecycle(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(lifecycleCompiled)
	if !ok || payload.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(
			fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: lifecycleEvaluated{}}
	if !lifecycleTakesAppID(payload.keyword) {
		evaluated.value = lifecycleEvaluated{keyword: payload.keyword}
		return evaluated, nil
	}
	if payload.explicitAppID == nil {
		appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
		if err != nil {
			return evaluated, err
		}
		evaluated.value = lifecycleEvaluated{keyword: payload.keyword, appID: appID}
		return evaluated, nil
	}
	appID, err := evaluation.Interpolate(ctx, *payload.explicitAppID, nil)
	if err != nil {
		return evaluated, err
	}
	if appID == "" {
		return evaluated, NewConfigurationError(
			fmt.Sprintf("command %s appId must not be empty after interpolation", command.Kind), nil)
	}
	evaluated.command.Arguments = appID
	evaluated.value = lifecycleEvaluated{keyword: payload.keyword, appID: appID}
	return evaluated, nil
}

func executeLifecycle(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	payload, ok := evaluated.value.(lifecycleEvaluated)
	if !ok {
		return effect, NewConfigurationError("lifecycle command received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.AppRequest{AppID: payload.appID}
	var err error
	switch payload.keyword {
	case model.CommandStopApp:
		err = state.dependencies.Driver.StopApp(ctx, request)
	case model.CommandKillApp:
		err = state.dependencies.Driver.KillApp(ctx, request)
	case model.CommandClearState:
		err = state.dependencies.Driver.ClearAppState(ctx, request)
	case model.CommandClearKeychain:
		err = state.dependencies.Driver.ClearKeychain(ctx)
	default:
		return effect, NewConfigurationError("lifecycle command keyword is invalid", nil)
	}
	if err != nil {
		return effect, err
	}
	return effect, nil
}
