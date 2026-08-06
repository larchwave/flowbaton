package engine

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/model"
)

const (
	doubleTapDefaultDelay   = 100 * time.Millisecond
	longPressDurationMillis = int64(3000)
)

type interactionGestureCompiled struct {
	keyword             model.CommandKeyword
	targetMode          tapTargetMode
	point               *tapPointPlan
	pointRequiresEval   bool
	delayMillis         int64
	settleTimeoutMillis *int64
}

type interactionGestureTarget struct {
	targetMode     tapTargetMode
	targetSelector *model.ElementSelector
	optional       bool
	point          *tapPointPlan
}

type interactionGestureEvaluated struct {
	keyword             model.CommandKeyword
	appID               string
	target              interactionGestureTarget
	delay               time.Duration
	settleTimeoutMillis *int64
}

func doubleTapOnHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandDoubleTapOn, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileDoubleTapOn), evaluate: evaluateInteractionGesture, execute: executeDoubleTapOn,
	}
}

func longPressOnHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandLongPressOn, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileLongPressOn), evaluate: evaluateInteractionGesture, execute: executeLongPressOn,
	}
}

func compileDoubleTapOn(command model.Command) (any, error) {
	return compileInteractionGesture(command, true)
}

func compileLongPressOn(command model.Command) (any, error) {
	return compileInteractionGesture(command, false)
}

func compileInteractionGesture(command model.Command, allowDelay bool) (interactionGestureCompiled, error) {
	if err := validateInteractionGestureCommand(command, allowDelay); err != nil {
		return interactionGestureCompiled{}, err
	}

	selector := command.Selector
	hasSelector := selectorHasImplementedTarget(selector)
	targetMode := tapTargetSelectorCenter
	switch {
	case selector.Point != nil && hasSelector:
		targetMode = tapTargetSelectorPoint
	case selector.Point != nil:
		targetMode = tapTargetScreenPoint
	}

	compiled := interactionGestureCompiled{
		keyword:             command.Kind,
		targetMode:          targetMode,
		settleTimeoutMillis: cloneSettleTimeout(selector.WaitToSettleTimeoutMS),
	}
	if command.Kind == model.CommandDoubleTapOn {
		compiled.delayMillis = doubleTapDefaultDelay.Milliseconds()
		if selector.Delay != nil {
			compiled.delayMillis = int64(*selector.Delay)
		}
	}
	if selector.Point != nil {
		compiled.pointRequiresEval = tapPointRequiresEvaluation(*selector.Point)
		if !compiled.pointRequiresEval {
			point, err := parseInteractionGesturePoint(command.Kind, *selector.Point)
			if err != nil {
				return interactionGestureCompiled{}, err
			}
			compiled.point = &point
		}
	}
	return compiled, nil
}

func validateInteractionGestureCommand(command model.Command, allowDelay bool) error {
	if command.Kind != model.CommandDoubleTapOn && command.Kind != model.CommandLongPressOn {
		return NewConfigurationError("interaction gesture keyword is invalid", nil)
	}
	if command.Form != model.CommandFormObject {
		return commandDecodeError(command.Kind, "requires object form with a selector or point")
	}
	if command.Selector == nil {
		return commandDecodeError(command.Kind, "requires a selector or point")
	}
	if len(command.Children) != 0 {
		return commandDecodeError(command.Kind, "must not contain child commands")
	}
	if !selectorCommandSnapshotMatches(command) {
		return NewConfigurationError(fmt.Sprintf("command %s selector, label, or optional flag does not match its typed snapshot", command.Kind), nil)
	}
	selector := command.Selector
	if err := validateTapSelectorFeatures(command.Kind, selector, true); err != nil {
		return err
	}
	if err := validateTapSelectorModifierSemantics(command.Kind, selector); err != nil {
		return err
	}
	switch {
	case selector.Repeat != nil:
		return NewConfigurationError(fmt.Sprintf("command %s does not accept repeat", command.Kind), nil)
	case selector.RetryTapIfNoChange != nil:
		return NewConfigurationError(fmt.Sprintf("command %s does not accept retryTapIfNoChange", command.Kind), nil)
	case selector.WaitUntilVisible != nil:
		return NewConfigurationError(fmt.Sprintf("command %s does not accept waitUntilVisible", command.Kind), nil)
	case !allowDelay && selector.Delay != nil:
		return NewConfigurationError(fmt.Sprintf("command %s does not accept delay", command.Kind), nil)
	}
	if selector.Delay != nil && (*selector.Delay < 0 || *selector.Delay > int(MaximumSettleTimeout.Milliseconds())) {
		return NewConfigurationError(fmt.Sprintf("command %s delay must be in [0,30000]", command.Kind), nil)
	}
	if selector.WaitToSettleTimeoutMS != nil && (*selector.WaitToSettleTimeoutMS < 0 || *selector.WaitToSettleTimeoutMS > int(MaximumSettleTimeout.Milliseconds())) {
		return NewConfigurationError(fmt.Sprintf("command %s waitToSettleTimeoutMs must be in [0,30000]", command.Kind), nil)
	}

	hasSelector := selectorHasImplementedTarget(selector)
	if !hasSelector && selector.Point == nil {
		return NewConfigurationError(fmt.Sprintf("command %s requires an implemented target predicate or point", command.Kind), nil)
	}
	if !hasSelector {
		if tapPointOnlyHasUnrelatedSelectorFields(selector) {
			return NewConfigurationError(fmt.Sprintf("command %s point-only form contains unrelated selector fields", command.Kind), nil)
		}
		return nil
	}
	return validateImplementedSelectorTargets(command.Kind, selector, "selector")
}

func evaluateInteractionGesture(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(interactionGestureCompiled)
	if !ok || plan.keyword != command.Kind {
		return evaluatedDispatch{}, NewConfigurationError(fmt.Sprintf("%s received an invalid compiled payload", command.Kind), nil)
	}
	evaluated, err := evaluateSelectorCommand(ctx, evaluation, command)
	evaluated.value = interactionGestureEvaluated{}
	if err != nil {
		return evaluated, err
	}

	rawAppID, err := evaluation.ActiveAppID()
	if err != nil {
		return evaluated, err
	}
	appID, err := evaluation.Interpolate(ctx, rawAppID, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(appID) == "" {
		return evaluated, NewConfigurationError(fmt.Sprintf("%s requires an active appId", command.Kind), nil)
	}

	var point *tapPointPlan
	if plan.targetMode == tapTargetScreenPoint || plan.targetMode == tapTargetSelectorPoint {
		if evaluated.command.Selector == nil || evaluated.command.Selector.Point == nil {
			return evaluated, NewConfigurationError(fmt.Sprintf("%s evaluated point is unavailable", command.Kind), nil)
		}
		parsed := tapPointPlan{}
		if plan.pointRequiresEval {
			parsed, err = parseInteractionGesturePoint(command.Kind, *evaluated.command.Selector.Point)
			if err != nil {
				return evaluated, err
			}
		} else if plan.point != nil {
			parsed = *plan.point
		} else {
			return evaluated, NewConfigurationError(fmt.Sprintf("%s compiled point is unavailable", command.Kind), nil)
		}
		point = &parsed
	}

	var targetSelector *model.ElementSelector
	if plan.targetMode == tapTargetSelectorCenter || plan.targetMode == tapTargetSelectorPoint {
		targetSelector, err = interactionTargetOnlySelector(evaluated.command)
		if err != nil {
			return evaluated, err
		}
	}
	evaluated.value = interactionGestureEvaluated{
		keyword: plan.keyword,
		appID:   appID,
		target: interactionGestureTarget{
			targetMode: plan.targetMode, targetSelector: targetSelector,
			optional: commandIsOptional(evaluated.command), point: point,
		},
		delay:               time.Duration(plan.delayMillis) * time.Millisecond,
		settleTimeoutMillis: clonePointer(plan.settleTimeoutMillis),
	}
	return evaluated, nil
}

func parseInteractionGesturePoint(keyword model.CommandKeyword, source string) (tapPointPlan, error) {
	if strings.IndexFunc(source, unicode.IsSpace) >= 0 {
		return tapPointPlan{}, NewConfigurationError(fmt.Sprintf("command %s point must not contain whitespace", keyword), nil)
	}
	point, err := parseTapPoint(source)
	if err != nil {
		return tapPointPlan{}, NewConfigurationError(fmt.Sprintf("command %s point is invalid", keyword), err)
	}
	return point, nil
}

func interactionTargetOnlySelector(command model.Command) (*model.ElementSelector, error) {
	if command.Selector == nil || !selectorHasImplementedTarget(command.Selector) {
		return nil, NewConfigurationError(fmt.Sprintf("%s requires a selector target", command.Kind), nil)
	}
	selector := cloneSelector(command.Selector)
	selector.Point = nil
	selector.Repeat = nil
	selector.Delay = nil
	selector.RetryTapIfNoChange = nil
	selector.WaitUntilVisible = nil
	selector.WaitToSettleTimeoutMS = nil
	selector.Optional = nil
	selector.Label = nil
	return selector, nil
}

func executeDoubleTapOn(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, lookup, err := interactionGestureExecutionPlan(state, evaluated, model.CommandDoubleTapOn)
	if err != nil {
		return effect, err
	}
	point, err := resolveInteractionGestureTarget(ctx, lookup, plan.target)
	if err != nil {
		return effect, err
	}
	requests := ownedDoubleTapRequests(point)
	if err := executeGestureTap(ctx, state, lookup, requests[0]); err != nil {
		return effect, err
	}
	if err := state.dependencies.Clock.Wait(ctx, plan.delay); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return effect, cancellation
	}
	if err := executeGestureTap(ctx, state, lookup, requests[1]); err != nil {
		return effect, err
	}
	return effect, waitForInteractionGestureSettle(ctx, lookup, plan)
}

func executeLongPressOn(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, lookup, err := interactionGestureExecutionPlan(state, evaluated, model.CommandLongPressOn)
	if err != nil {
		return effect, err
	}
	point, err := resolveInteractionGestureTarget(ctx, lookup, plan.target)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := ownedLongPressRequest(point)
	if err := state.dependencies.Driver.LongPress(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	lookup.RecordInteraction(state.dependencies.Clock.Now())
	if cancellation := ctx.Err(); cancellation != nil {
		return effect, cancellation
	}
	return effect, waitForInteractionGestureSettle(ctx, lookup, plan)
}

func interactionGestureExecutionPlan(
	state *executionState,
	evaluated evaluatedDispatch,
	keyword model.CommandKeyword,
) (interactionGestureEvaluated, *ElementLookup, error) {
	plan, ok := evaluated.value.(interactionGestureEvaluated)
	if !ok || plan.keyword != keyword {
		return interactionGestureEvaluated{}, nil, NewConfigurationError(fmt.Sprintf("%s received an invalid evaluated payload", keyword), nil)
	}
	if state == nil || isNilValue(state.dependencies.Driver) || isNilValue(state.dependencies.Clock) {
		return interactionGestureEvaluated{}, nil, NewConfigurationError(fmt.Sprintf("%s requires driver and clock services", keyword), nil)
	}
	if strings.TrimSpace(plan.appID) == "" {
		return interactionGestureEvaluated{}, nil, NewConfigurationError(fmt.Sprintf("%s evaluated plan requires a non-blank appId", keyword), nil)
	}
	if keyword == model.CommandDoubleTapOn && (plan.delay < 0 || plan.delay > MaximumSettleTimeout) {
		return interactionGestureEvaluated{}, nil, NewConfigurationError("doubleTapOn evaluated delay is invalid", nil)
	}
	if plan.settleTimeoutMillis != nil && (*plan.settleTimeoutMillis < 0 || *plan.settleTimeoutMillis > MaximumSettleTimeout.Milliseconds()) {
		return interactionGestureEvaluated{}, nil, NewConfigurationError(fmt.Sprintf("%s evaluated settle timeout is invalid", keyword), nil)
	}
	if err := validateInteractionGestureTarget(plan.target, keyword); err != nil {
		return interactionGestureEvaluated{}, nil, err
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return interactionGestureEvaluated{}, nil, err
	}
	return plan, lookup, nil
}

func validateInteractionGestureTarget(target interactionGestureTarget, keyword model.CommandKeyword) error {
	switch target.targetMode {
	case tapTargetSelectorCenter:
		if target.point != nil || target.targetSelector == nil {
			return NewConfigurationError(fmt.Sprintf("%s selector-center target is invalid", keyword), nil)
		}
	case tapTargetScreenPoint:
		if target.point == nil || target.targetSelector != nil {
			return NewConfigurationError(fmt.Sprintf("%s screen-point target is invalid", keyword), nil)
		}
	case tapTargetSelectorPoint:
		if target.point == nil || target.targetSelector == nil {
			return NewConfigurationError(fmt.Sprintf("%s selector-point target is invalid", keyword), nil)
		}
	default:
		return NewConfigurationError(fmt.Sprintf("%s target mode is invalid", keyword), nil)
	}
	if target.point != nil && (target.point.mode < tapCoordinateAbsolute || target.point.mode > tapCoordinatePercent) {
		return NewConfigurationError(fmt.Sprintf("%s point target is invalid", keyword), nil)
	}
	return nil
}

func resolveInteractionGestureTarget(
	ctx context.Context,
	lookup *ElementLookup,
	target interactionGestureTarget,
) (device.Point, error) {
	switch target.targetMode {
	case tapTargetScreenPoint:
		info, err := lookup.cachedDeviceInfo(ctx)
		if err != nil {
			return device.Point{}, err
		}
		return target.point.resolveScreen(info.WidthGrid, info.HeightGrid)
	case tapTargetSelectorCenter, tapTargetSelectorPoint:
		selector := cloneSelector(target.targetSelector)
		element, err := lookup.Find(ctx, *selector, LookupOptions{Optional: target.optional})
		if err != nil {
			return device.Point{}, err
		}
		if element == nil {
			return device.Point{}, NewOperationError("interaction target not found", nil)
		}
		stability, err := lookup.WaitForElementStability(ctx, element)
		if err != nil {
			return device.Point{}, err
		}
		if target.targetMode == tapTargetSelectorCenter {
			return hierarchy.Center(stability.Bounds), nil
		}
		return target.point.resolveRelative(stability.Bounds)
	default:
		return device.Point{}, NewConfigurationError("interaction target mode is invalid", nil)
	}
}

func ownedDoubleTapRequests(point device.Point) [2]device.TapRequest {
	owned := device.Point{X: point.X, Y: point.Y}
	return [2]device.TapRequest{{Point: owned}, {Point: owned}}
}

func ownedLongPressRequest(point device.Point) device.LongPressRequest {
	return device.LongPressRequest{
		Point: device.Point{X: point.X, Y: point.Y}, DurationMillis: longPressDurationMillis,
	}
}

func executeGestureTap(
	ctx context.Context,
	state *executionState,
	lookup *ElementLookup,
	request device.TapRequest,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := state.dependencies.Driver.Tap(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return cancellation
		}
		return err
	}
	lookup.RecordInteraction(state.dependencies.Clock.Now())
	return ctx.Err()
}

func waitForInteractionGestureSettle(
	ctx context.Context,
	lookup *ElementLookup,
	plan interactionGestureEvaluated,
) error {
	request := device.SettleRequest{
		AppID: plan.appID, TimeoutMillis: clonePointer(plan.settleTimeoutMillis),
	}
	_, err := lookup.WaitForHierarchySettle(ctx, request)
	if err == nil {
		return nil
	}
	normalized := normalizeTerminalError(fmt.Sprintf("command %s settle failed", plan.keyword), err)
	switch classifyTerminalError(normalized) {
	case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
		return normalized
	default:
		return nil
	}
}
