package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

type actionAliasCompiled struct {
	target model.CommandKeyword
}

type actionAliasEvaluated struct {
	target model.CommandKeyword
	value  any
}

func actionHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandAction, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileActionAlias), evaluate: evaluateActionAlias, execute: executeActionAlias,
	}
}

func compileActionAlias(command model.Command) (any, error) {
	target, err := actionAliasTarget(command)
	if err != nil {
		return nil, err
	}
	return actionAliasCompiled{target: target}, nil
}

func actionAliasTarget(command model.Command) (model.CommandKeyword, error) {
	if command.Kind != model.CommandAction {
		return "", NewConfigurationError("action keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return "", commandDecodeError(command.Kind, "accepts only one exact static alias string")
	}
	alias, err := decodeString(command)
	if err != nil {
		return "", err
	}
	if js.HasInterpolationExpression(alias) {
		return "", NewConfigurationError("command action does not permit alias interpolation", nil)
	}
	target, ok := canonicalActionAliasTarget(alias)
	if !ok {
		return "", NewConfigurationError("command action value is not in the exact supported set", nil)
	}
	return target, nil
}

// canonicalActionAliasTarget resolves a case-insensitive alias to the canonical
// keyword used by the dispatcher registry.
func canonicalActionAliasTarget(alias string) (model.CommandKeyword, bool) {
	for _, target := range []model.CommandKeyword{
		model.CommandBack, model.CommandHideKeyboard, model.CommandScroll, model.CommandPasteText,
	} {
		if strings.EqualFold(alias, string(target)) {
			return target, true
		}
	}
	return "", false
}

// validActionAliasTarget defensively checks an already-canonical keyword (the
// compiled/evaluated target is produced by canonicalActionAliasTarget, so this
// guards against a corrupt payload, not authored case).
func validActionAliasTarget(target model.CommandKeyword) bool {
	switch target {
	case model.CommandBack, model.CommandHideKeyboard, model.CommandScroll, model.CommandPasteText:
		return true
	default:
		return false
	}
}

func evaluateActionAlias(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(actionAliasCompiled)
	if !ok || !validActionAliasTarget(plan.target) {
		return evaluatedDispatch{}, NewConfigurationError("action received an invalid compiled payload", nil)
	}
	target, err := actionAliasTarget(command)
	if err != nil || target != plan.target {
		return evaluatedDispatch{}, NewConfigurationError("action command does not match its compiled target", err)
	}
	evaluated := evaluatedDispatch{
		command: cloneCommand(command),
		value:   actionAliasEvaluated{target: plan.target},
	}
	appID, err := evaluatedActiveAppID(ctx, evaluation, plan.target)
	if err != nil {
		return evaluated, normalizeActionAliasTargetError(plan.target, err)
	}
	appID = strings.Clone(appID)
	value := actionAliasEvaluated{target: plan.target}
	switch plan.target {
	case model.CommandBack, model.CommandHideKeyboard, model.CommandScroll:
		value.value = batch2ADirectEvaluated{keyword: plan.target, appID: appID}
	case model.CommandPasteText:
		value.value = pasteTextEvaluated{appID: appID}
	}
	evaluated.value = value
	return evaluated, nil
}

func executeActionAlias(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(actionAliasEvaluated)
	if !ok || !validActionAliasTarget(plan.target) {
		return effect, NewConfigurationError("action received an invalid evaluated payload", nil)
	}
	target, err := actionAliasTarget(evaluated.command)
	if err != nil || target != plan.target {
		return effect, NewConfigurationError("action evaluated command does not match its target", err)
	}
	targetDispatch := evaluatedDispatch{command: cloneCommand(evaluated.command)}
	switch plan.target {
	case model.CommandBack, model.CommandHideKeyboard, model.CommandScroll:
		direct, valid := plan.value.(batch2ADirectEvaluated)
		if !valid || direct.keyword != plan.target {
			return effect, NewConfigurationError("action direct target payload is invalid", nil)
		}
		targetDispatch.value = direct
		effect, err = executeBatch2ADirect(ctx, state, targetDispatch)
	case model.CommandPasteText:
		paste, valid := plan.value.(pasteTextEvaluated)
		if !valid {
			return effect, NewConfigurationError("action pasteText target payload is invalid", nil)
		}
		targetDispatch.value = paste
		effect, err = executePasteText(ctx, state, targetDispatch)
	}
	if err != nil {
		return effect, normalizeActionAliasTargetError(plan.target, err)
	}
	return effect, nil
}

func normalizeActionAliasTargetError(target model.CommandKeyword, err error) error {
	return normalizeTerminalError(fmt.Sprintf("command %s failed", target), err)
}
