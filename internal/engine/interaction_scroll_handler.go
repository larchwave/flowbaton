package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

const (
	scrollUntilVisibleDefaultTimeoutMillis = int64(20000)
	scrollUntilVisibleMaximumTimeoutMillis = int64(120000)
	scrollUntilVisibleDefaultSpeed         = int64(40)
	scrollUntilVisibleDefaultVisibility    = int64(100)
	scrollUntilVisibleMaximumObservations  = 1201
	scrollUntilVisiblePollInterval         = 100 * time.Millisecond
)

type scrollUntilVisibleCompiled struct {
	selector                    *model.ElementSelector
	direction                   string
	directionRequiresEvaluation bool
	timeoutMillis               int64
	timeoutSource               string
	timeoutRequiresEvaluation   bool
	// speed remains scalar text until evaluation, so interpolation and the
	// [1,100] bound are resolved together.
	speedSource             string
	speedRequiresEvaluation bool
	speed                   int64
	visibilityPercentage    int64
	settleTimeoutMillis     *int64
	centerElement           bool
}

type scrollUntilVisibleEvaluated struct {
	selector             *model.ElementSelector
	appID                string
	direction            device.Direction
	timeoutMillis        int64
	speed                int64
	visibilityPercentage int64
	settleTimeoutMillis  *int64
	centerElement        bool
}

func scrollUntilVisibleHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandScrollUntilVisible, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileScrollUntilVisible),
		evaluate:   evaluateScrollUntilVisible,
		execute:    executeScrollUntilVisible,
	}
}

func compileScrollUntilVisible(command model.Command) (any, error) {
	if command.Kind != model.CommandScrollUntilVisible {
		return nil, NewConfigurationError("scrollUntilVisible keyword is invalid", nil)
	}
	if command.Form != model.CommandFormObject || len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "requires object form without child commands")
	}
	// `label` and `optional` are universal metadata. A sibling `when:` remains invalid.
	if command.Condition != nil || len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "does not accept typed condition or links")
	}
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown(
		"element", "direction", "timeout", "speed", "visibilityPercentage", "waitToSettleTimeoutMs", "centerElement",
	); err != nil {
		return nil, err
	}
	elementArguments, hasElement := object.raw("element")
	if !hasElement {
		return nil, NewConfigurationError("command scrollUntilVisible field element is required", nil)
	}
	if command.Selector == nil || !selectorArgumentsMatch(elementArguments, command.Selector) {
		return nil, NewConfigurationError("command scrollUntilVisible element does not match its typed selector snapshot", nil)
	}
	if command.Selector.Optional != nil {
		return nil, NewConfigurationError("command scrollUntilVisible element does not accept optional", nil)
	}
	if err := rejectUnsupportedSelectorFeatures(command.Kind, command.Selector, false, true); err != nil {
		return nil, err
	}
	if err := validateImplementedSelectorTargets(command.Kind, command.Selector, "element"); err != nil {
		return nil, err
	}
	if !scrollSelectorRequiresEvaluation(command.Selector) {
		if _, err := matching.Find(nil, *command.Selector); err != nil {
			return nil, NewConfigurationError("command scrollUntilVisible element selector is invalid", err)
		}
	}

	compiled := scrollUntilVisibleCompiled{
		selector:             cloneSelector(command.Selector),
		direction:            "DOWN",
		timeoutMillis:        scrollUntilVisibleDefaultTimeoutMillis,
		speed:                scrollUntilVisibleDefaultSpeed,
		visibilityPercentage: scrollUntilVisibleDefaultVisibility,
	}
	if direction, exists, err := object.optionalString("direction"); err != nil {
		return nil, err
	} else if exists {
		compiled.direction = direction
		compiled.directionRequiresEvaluation = js.HasInterpolationExpression(direction)
		if !compiled.directionRequiresEvaluation {
			if _, err := validatedScrollUntilVisibleDirection(direction); err != nil {
				return nil, err
			}
		}
	}

	if timeout, exists := object.raw("timeout"); exists {
		switch value := timeout.(type) {
		case int64:
			compiled.timeoutMillis = value
		case string:
			compiled.timeoutSource = value
			compiled.timeoutRequiresEvaluation = js.HasInterpolationExpression(value)
			if !compiled.timeoutRequiresEvaluation {
				compiled.timeoutMillis, err = parseScrollUntilVisibleTimeout(value)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, NewConfigurationError("command scrollUntilVisible field timeout must be an integer or strict base-10 integer string", nil)
		}
	}
	if !compiled.timeoutRequiresEvaluation {
		if err := validateScrollUntilVisibleTimeout(compiled.timeoutMillis); err != nil {
			return nil, err
		}
	}

	if speed, exists := object.raw("speed"); exists {
		switch value := speed.(type) {
		case int64:
			compiled.speed = value
		case string:
			compiled.speedSource = value
			compiled.speedRequiresEvaluation = js.HasInterpolationExpression(value)
			if !compiled.speedRequiresEvaluation {
				compiled.speed, err = parseScrollUntilVisibleSpeed(value)
				if err != nil {
					return nil, err
				}
			}
		default:
			return nil, NewConfigurationError("command scrollUntilVisible field speed must be an integer or a string", nil)
		}
	}
	if !compiled.speedRequiresEvaluation {
		if err := validateScrollUntilVisibleSpeed(compiled.speed); err != nil {
			return nil, err
		}
	}
	if visibility, exists, err := object.optionalInt("visibilityPercentage"); err != nil {
		return nil, err
	} else if exists {
		compiled.visibilityPercentage = visibility
	}
	if compiled.visibilityPercentage < 10 || compiled.visibilityPercentage > 100 {
		return nil, NewConfigurationError("command scrollUntilVisible visibilityPercentage must be in [10,100]", nil)
	}
	if settle, exists, err := object.optionalInt("waitToSettleTimeoutMs"); err != nil {
		return nil, err
	} else if exists {
		if settle < 0 || settle > MaximumSettleTimeout.Milliseconds() {
			return nil, NewConfigurationError("command scrollUntilVisible waitToSettleTimeoutMs must be in [0,30000]", nil)
		}
		compiled.settleTimeoutMillis = &settle
	}
	if center, exists, err := object.optionalBool("centerElement"); err != nil {
		return nil, err
	} else if exists {
		compiled.centerElement = center
	}
	return compiled, nil
}

func validatedScrollUntilVisibleDirection(value string) (device.Direction, error) {
	// Normalize the case-insensitive direction to the driver's uppercase form.
	switch strings.ToUpper(value) {
	case "UP", "DOWN", "LEFT", "RIGHT":
		return device.Direction(strings.ToUpper(value)), nil
	default:
		return "", NewConfigurationError("command scrollUntilVisible direction must be one of UP, DOWN, LEFT, RIGHT", nil)
	}
}

// parseScrollUntilVisibleSpeed resolves authored or interpolated speed text at
// runtime.
func parseScrollUntilVisibleSpeed(value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, NewConfigurationError(
			"command scrollUntilVisible speed must be an integer, got "+strconv.Quote(value), nil)
	}
	return parsed, nil
}

func validateScrollUntilVisibleSpeed(value int64) error {
	if value < 1 || value > 100 {
		return NewConfigurationError("command scrollUntilVisible speed must be in [1,100]", nil)
	}
	return nil
}

func parseScrollUntilVisibleTimeout(value string) (int64, error) {
	if value == "" {
		return 0, NewConfigurationError("command scrollUntilVisible timeout must be a strict base-10 integer string", nil)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, NewConfigurationError("command scrollUntilVisible timeout must be a strict base-10 integer string", nil)
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, NewConfigurationError("command scrollUntilVisible timeout is outside the supported integer range", err)
	}
	return parsed, nil
}

func validateScrollUntilVisibleTimeout(value int64) error {
	if value < 0 || value > scrollUntilVisibleMaximumTimeoutMillis {
		return NewConfigurationError("command scrollUntilVisible timeout must be in [0,120000]", nil)
	}
	return nil
}

func scrollSelectorRequiresEvaluation(selector *model.ElementSelector) bool {
	if selector == nil {
		return false
	}
	for _, value := range []*string{
		selector.TextRegex, selector.IDRegex, selector.Point, selector.Start, selector.End, selector.Label, selector.CSS,
	} {
		if value != nil && js.HasInterpolationExpression(*value) {
			return true
		}
	}
	for _, nested := range []*model.ElementSelector{
		selector.Below, selector.Above, selector.LeftOf, selector.RightOf, selector.ContainsChild, selector.ChildOf,
	} {
		if scrollSelectorRequiresEvaluation(nested) {
			return true
		}
	}
	for index := range selector.ContainsDescendants {
		if scrollSelectorRequiresEvaluation(&selector.ContainsDescendants[index]) {
			return true
		}
	}
	return false
}

func evaluateScrollUntilVisible(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(scrollUntilVisibleCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("scrollUntilVisible received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: scrollUntilVisibleEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	arguments, ok := evaluated.command.Arguments.(map[string]any)
	if !ok {
		return evaluated, NewConfigurationError("scrollUntilVisible evaluated arguments are unavailable", nil)
	}

	directionSource := plan.direction
	if plan.directionRequiresEvaluation {
		directionSource, err = evaluation.Interpolate(ctx, directionSource, nil)
		if err != nil {
			return evaluated, err
		}
		arguments["direction"] = directionSource
	}
	direction, err := validatedScrollUntilVisibleDirection(directionSource)
	if err != nil {
		return evaluated, err
	}
	timeoutMillis := plan.timeoutMillis
	if plan.timeoutRequiresEvaluation {
		timeoutSource, interpolateErr := evaluation.Interpolate(ctx, plan.timeoutSource, nil)
		if interpolateErr != nil {
			return evaluated, interpolateErr
		}
		arguments["timeout"] = timeoutSource
		timeoutMillis, err = parseScrollUntilVisibleTimeout(timeoutSource)
		if err != nil {
			return evaluated, err
		}
	}
	speed := plan.speed
	if plan.speedRequiresEvaluation {
		speedSource, interpolateErr := evaluation.Interpolate(ctx, plan.speedSource, nil)
		if interpolateErr != nil {
			return evaluated, interpolateErr
		}
		arguments["speed"] = speedSource
		speed, err = parseScrollUntilVisibleSpeed(speedSource)
		if err != nil {
			return evaluated, err
		}
	}
	if err := validateScrollUntilVisibleSpeed(speed); err != nil {
		return evaluated, err
	}
	if err := validateScrollUntilVisibleTimeout(timeoutMillis); err != nil {
		return evaluated, err
	}

	selector := cloneSelector(plan.selector)
	if selector == nil {
		return evaluated, NewConfigurationError("scrollUntilVisible compiled selector is unavailable", nil)
	}
	if err := interpolateSelector(ctx, evaluation, selector); err != nil {
		return evaluated, err
	}
	if _, err := matching.Find(nil, *selector); err != nil {
		return evaluated, NewConfigurationError("command scrollUntilVisible element selector is invalid after interpolation", err)
	}
	arguments["element"] = synchronizeSelectorArguments(arguments["element"], selector)
	evaluated.command.Arguments = arguments
	evaluated.command.Selector = cloneSelector(selector)
	evaluated.value = scrollUntilVisibleEvaluated{
		selector: cloneSelector(selector), appID: appID, direction: direction,
		timeoutMillis: timeoutMillis, speed: speed,
		visibilityPercentage: plan.visibilityPercentage,
		settleTimeoutMillis:  clonePointer(plan.settleTimeoutMillis),
		centerElement:        plan.centerElement,
	}
	return evaluated, nil
}

func executeScrollUntilVisible(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	if ctx == nil {
		return effect, NewConfigurationError("scrollUntilVisible execution context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	plan, lookup, err := scrollUntilVisibleExecutionPlan(state, evaluated)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	deadline := state.dependencies.Clock.Now().Add(time.Duration(plan.timeoutMillis) * time.Millisecond)
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return effect, err
	}
	if info.WidthGrid <= 0 || info.HeightGrid <= 0 {
		return effect, NewConfigurationError("scrollUntilVisible requires positive DeviceInfo grid dimensions", nil)
	}
	viewport := device.Bounds{X: 0, Y: 0, Width: info.WidthGrid, Height: info.HeightGrid}
	threshold := float64(plan.visibilityPercentage) / 100

	for observation := 1; observation <= scrollUntilVisibleMaximumObservations; observation++ {
		element, observeErr := observeScrollUntilVisible(ctx, lookup, plan.selector)
		if observeErr != nil {
			return effect, observeErr
		}
		if scrollUntilVisibleThresholdSatisfied(element, viewport, threshold) {
			if !plan.centerElement {
				return effect, nil
			}
			request, needed := scrollUntilVisibleCenterRequest(element.Bounds, viewport)
			if !needed {
				return effect, nil
			}
			if err := executeOwnedScrollUntilVisibleScroll(ctx, state, lookup, request); err != nil {
				return effect, err
			}
			if plan.settleTimeoutMillis != nil {
				if err := waitForScrollUntilVisibleSettle(ctx, lookup, plan.appID, *plan.settleTimeoutMillis, deadline); err != nil {
					return effect, err
				}
			}
			return effect, ctx.Err()
		}

		now := state.dependencies.Clock.Now()
		if !now.Before(deadline) {
			return effect, NewAssertionError("scrollUntilVisible target did not reach the required visibility before timeout", nil)
		}
		if observation >= scrollUntilVisibleMaximumObservations {
			return effect, NewAssertionError("scrollUntilVisible exhausted the 1201-observation guard", nil)
		}
		request := device.ScrollVerticalRequest{
			Direction: plan.direction,
			Amount:    float64(plan.speed) / 100,
		}
		if err := executeOwnedScrollUntilVisibleScroll(ctx, state, lookup, request); err != nil {
			return effect, err
		}
		if plan.settleTimeoutMillis != nil {
			if err := waitForScrollUntilVisibleSettle(ctx, lookup, plan.appID, *plan.settleTimeoutMillis, deadline); err != nil {
				return effect, err
			}
		} else {
			now = state.dependencies.Clock.Now()
			remaining := deadline.Sub(now)
			if remaining < 0 {
				remaining = 0
			}
			if err := state.dependencies.Clock.Wait(ctx, minDuration(scrollUntilVisiblePollInterval, remaining)); err != nil {
				if cancellation := ctx.Err(); cancellation != nil {
					return effect, cancellation
				}
				return effect, err
			}
		}
		if err := ctx.Err(); err != nil {
			return effect, err
		}
	}
	return effect, NewAssertionError("scrollUntilVisible exhausted the 1201-observation guard", nil)
}

func scrollUntilVisibleExecutionPlan(
	state *executionState,
	evaluated evaluatedDispatch,
) (scrollUntilVisibleEvaluated, *ElementLookup, error) {
	plan, ok := evaluated.value.(scrollUntilVisibleEvaluated)
	if !ok {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible received an invalid evaluated payload", nil)
	}
	if state == nil || isNilValue(state.dependencies.Driver) || isNilValue(state.dependencies.Clock) {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible requires driver and clock services", nil)
	}
	if plan.selector == nil || strings.TrimSpace(plan.appID) == "" {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible evaluated selector or appId is invalid", nil)
	}
	if _, err := validatedScrollUntilVisibleDirection(string(plan.direction)); err != nil {
		return scrollUntilVisibleEvaluated{}, nil, err
	}
	if err := validateScrollUntilVisibleTimeout(plan.timeoutMillis); err != nil {
		return scrollUntilVisibleEvaluated{}, nil, err
	}
	if plan.speed < 1 || plan.speed > 100 || plan.visibilityPercentage < 10 || plan.visibilityPercentage > 100 {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible evaluated numeric plan is invalid", nil)
	}
	if plan.settleTimeoutMillis != nil && (*plan.settleTimeoutMillis < 0 || *plan.settleTimeoutMillis > MaximumSettleTimeout.Milliseconds()) {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible evaluated settle timeout is invalid", nil)
	}
	if _, err := matching.Find(nil, *plan.selector); err != nil {
		return scrollUntilVisibleEvaluated{}, nil, NewConfigurationError("scrollUntilVisible evaluated selector is invalid", err)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return scrollUntilVisibleEvaluated{}, nil, err
	}
	plan.selector = cloneSelector(plan.selector)
	plan.settleTimeoutMillis = clonePointer(plan.settleTimeoutMillis)
	return plan, lookup, nil
}

func observeScrollUntilVisible(
	ctx context.Context,
	lookup *ElementLookup,
	selector *model.ElementSelector,
) (*hierarchy.Element, error) {
	root, err := lookup.visibleHierarchy(ctx)
	if err != nil {
		return nil, err
	}
	matches, err := matching.Find(root, *cloneSelector(selector))
	if err != nil {
		return nil, NewConfigurationError("scrollUntilVisible selector observation failed", err)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches[0], nil
}

func scrollUntilVisibleThresholdSatisfied(
	element *hierarchy.Element,
	viewport device.Bounds,
	threshold float64,
) bool {
	return element != nil && element.HasBounds && hierarchy.Area(element.Bounds) > 0 &&
		hierarchy.VisiblePercentage(element.Bounds, viewport) >= threshold
}

func scrollUntilVisibleCenterRequest(bounds, viewport device.Bounds) (device.ScrollVerticalRequest, bool) {
	center := hierarchy.Center(bounds)
	viewportMidpointY := float64(viewport.Y) + float64(viewport.Height)/2
	delta := center.Y - viewportMidpointY
	if delta == 0 {
		return device.ScrollVerticalRequest{}, false
	}
	direction := device.Direction("UP")
	if delta < 0 {
		direction = "DOWN"
	}
	amount := math.Min(1, math.Abs(delta)/float64(viewport.Height))
	if amount == 0 {
		return device.ScrollVerticalRequest{}, false
	}
	return device.ScrollVerticalRequest{Direction: direction, Amount: amount, ElementPoint: &center}, true
}

func executeOwnedScrollUntilVisibleScroll(
	ctx context.Context,
	state *executionState,
	lookup *ElementLookup,
	request device.ScrollVerticalRequest,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	owned := request
	if request.ElementPoint != nil {
		point := *request.ElementPoint
		owned.ElementPoint = &point
	}
	if err := state.dependencies.Driver.ScrollVertical(ctx, owned); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return cancellation
		}
		return err
	}
	lookup.RecordInteraction(state.dependencies.Clock.Now())
	return ctx.Err()
}

func waitForScrollUntilVisibleSettle(
	ctx context.Context,
	lookup *ElementLookup,
	appID string,
	authoredTimeoutMillis int64,
	deadline time.Time,
) error {
	remaining := deadline.Sub(lookup.clock.Now())
	remainingMillis := remaining.Milliseconds()
	if remainingMillis < 0 {
		remainingMillis = 0
	}
	timeoutMillis := authoredTimeoutMillis
	if remainingMillis < timeoutMillis {
		timeoutMillis = remainingMillis
	}
	settleLookup := NewElementLookup(
		lookup.driver,
		&scrollUntilVisibleSettleClock{
			Clock:           lookup.clock,
			remainingBudget: explicitSettleTimeout(timeoutMillis),
		},
	)
	_, err := settleLookup.WaitForHierarchySettle(ctx, device.SettleRequest{
		AppID: appID, TimeoutMillis: &timeoutMillis,
	})
	if err == nil {
		return ctx.Err()
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return cancellation
	}
	switch classifyTerminalError(err) {
	case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
		return err
	case terminalErrorRetryable:
		var ordinary *OperationError
		if errors.As(err, &ordinary) {
			return nil
		}
		return err
	default:
		return normalizeTerminalError(fmt.Sprintf("command %s settle failed", model.CommandScrollUntilVisible), err)
	}
}

type scrollUntilVisibleSettleClock struct {
	Clock
	remainingBudget time.Duration
}

func (clock *scrollUntilVisibleSettleClock) Wait(ctx context.Context, delay time.Duration) error {
	if ctx == nil {
		return NewConfigurationError("scrollUntilVisible settle context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		if clock.remainingBudget <= 0 {
			return NewOperationError("hierarchy did not settle before timeout", nil)
		}
		if delay > clock.remainingBudget {
			delay = clock.remainingBudget
		}
		clock.remainingBudget -= delay
	}
	return clock.Clock.Wait(ctx, delay)
}
