package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

const swipeDefaultDurationMillis = int64(400)

type swipeMode uint8

const (
	swipeModeDirection swipeMode = iota + 1
	swipeModePoints
	swipeModeElement
)

type swipeCompiled struct {
	mode                swipeMode
	direction           string
	start               string
	end                 string
	startPoint          *tapPointPlan
	endPoint            *tapPointPlan
	startRequiresEval   bool
	endRequiresEval     bool
	selector            *model.ElementSelector
	durationMillis      int64
	settleTimeoutMillis *int64
}

type swipeEvaluated struct {
	mode                swipeMode
	appID               string
	direction           device.Direction
	start               *tapPointPlan
	end                 *tapPointPlan
	selector            *model.ElementSelector
	durationMillis      int64
	settleTimeoutMillis *int64
}

func swipeHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandSwipe, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileSwipe), evaluate: evaluateSwipe, execute: executeSwipe,
	}
}

func compileSwipe(command model.Command) (any, error) {
	if command.Kind != model.CommandSwipe {
		return swipeCompiled{}, NewConfigurationError("swipe keyword is invalid", nil)
	}
	if command.Form != model.CommandFormObject || len(command.Children) != 0 {
		return swipeCompiled{}, commandDecodeError(command.Kind, "requires object form without child commands")
	}
	// `label` and `optional` are universal metadata. A sibling `when:` remains invalid.
	if command.Condition != nil {
		return swipeCompiled{}, commandDecodeError(command.Kind, "does not accept a typed condition")
	}
	object, err := decodeObject(command)
	if err != nil {
		return swipeCompiled{}, err
	}
	if err := object.rejectUnknown("direction", "start", "end", "from", "duration", "waitToSettleTimeoutMs"); err != nil {
		return swipeCompiled{}, err
	}

	direction, hasDirection, err := object.optionalString("direction")
	if err != nil {
		return swipeCompiled{}, err
	}
	start, hasStart, err := object.optionalString("start")
	if err != nil {
		return swipeCompiled{}, err
	}
	end, hasEnd, err := object.optionalString("end")
	if err != nil {
		return swipeCompiled{}, err
	}
	_, hasFrom := object.fields["from"]
	duration := swipeDefaultDurationMillis
	if raw, exists := object.raw("duration"); exists {
		// swipe.duration is an integer or a quoted base-10 integer. Parser
		// validation excludes interpolation and non-integer strings.
		switch value := raw.(type) {
		case int64:
			duration = value
		case string:
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil {
				return swipeCompiled{}, NewConfigurationError("command swipe duration must be a strict base-10 integer", nil)
			}
			duration = int64(parsed)
		default:
			return swipeCompiled{}, NewConfigurationError("command swipe duration must be an integer", nil)
		}
	}
	if duration < 1 || duration > 60000 {
		return swipeCompiled{}, NewConfigurationError("command swipe duration must be in [1,60000]", nil)
	}
	settle, hasSettle, err := object.optionalInt("waitToSettleTimeoutMs")
	if err != nil {
		return swipeCompiled{}, err
	}
	if hasSettle && (settle < 0 || settle > MaximumSettleTimeout.Milliseconds()) {
		return swipeCompiled{}, NewConfigurationError("command swipe waitToSettleTimeoutMs must be in [0,30000]", nil)
	}

	compiled := swipeCompiled{direction: direction, start: start, end: end, durationMillis: duration}
	if hasSettle {
		compiled.settleTimeoutMillis = &settle
	}
	switch {
	case hasStart || hasEnd:
		if !hasStart || !hasEnd {
			return swipeCompiled{}, NewConfigurationError("command swipe coordinate form requires both start and end", nil)
		}
		if hasDirection || hasFrom {
			return swipeCompiled{}, NewConfigurationError("command swipe variants are mutually exclusive", nil)
		}
		if command.Selector != nil {
			return swipeCompiled{}, NewConfigurationError("command swipe coordinate form must not contain a selector", nil)
		}
		compiled.mode = swipeModePoints
		compiled.startRequiresEval = js.HasInterpolationExpression(start)
		compiled.endRequiresEval = js.HasInterpolationExpression(end)
		if !compiled.startRequiresEval {
			startPoint, pointErr := parseInteractionGesturePoint(model.CommandSwipe, start)
			if pointErr != nil {
				return swipeCompiled{}, pointErr
			}
			compiled.startPoint = &startPoint
		}
		if !compiled.endRequiresEval {
			endPoint, pointErr := parseInteractionGesturePoint(model.CommandSwipe, end)
			if pointErr != nil {
				return swipeCompiled{}, pointErr
			}
			compiled.endPoint = &endPoint
		}
		if compiled.startPoint != nil && compiled.endPoint != nil && compiled.startPoint.mode != compiled.endPoint.mode {
			return swipeCompiled{}, NewConfigurationError("command swipe start and end must use the same coordinate mode", nil)
		}
	case hasFrom:
		if !hasDirection {
			return swipeCompiled{}, NewConfigurationError("command swipe element form requires from and direction", nil)
		}
		if command.Selector == nil {
			return swipeCompiled{}, NewConfigurationError("command swipe from selector snapshot is unavailable", nil)
		}
		if !selectorArgumentsMatch(object.fields["from"], command.Selector) {
			return swipeCompiled{}, NewConfigurationError("command swipe from selector does not match its typed snapshot", nil)
		}
		if command.Selector.Optional != nil {
			return swipeCompiled{}, NewConfigurationError("command swipe from does not accept optional", nil)
		}
		if err := rejectUnsupportedSelectorFeatures(command.Kind, command.Selector, false, true); err != nil {
			return swipeCompiled{}, err
		}
		if err := validateImplementedSelectorTargets(command.Kind, command.Selector, "from"); err != nil {
			return swipeCompiled{}, err
		}
		compiled.mode = swipeModeElement
		compiled.selector = cloneSelector(command.Selector)
	default:
		if !hasDirection {
			return swipeCompiled{}, NewConfigurationError("command swipe requires direction, start/end, or from/direction", nil)
		}
		if command.Selector != nil {
			return swipeCompiled{}, NewConfigurationError("command swipe direction form must not contain a selector", nil)
		}
		compiled.mode = swipeModeDirection
	}
	if compiled.mode != swipeModePoints && !js.HasInterpolationExpression(direction) {
		if _, err := validatedSwipeDirection(direction); err != nil {
			return swipeCompiled{}, err
		}
	}
	return compiled, nil
}

func validatedSwipeDirection(value string) (device.Direction, error) {
	// Normalize the case-insensitive direction to the driver's uppercase form.
	switch strings.ToUpper(value) {
	case "UP", "DOWN", "LEFT", "RIGHT":
		return device.Direction(strings.ToUpper(value)), nil
	default:
		return "", NewConfigurationError("command swipe direction must be exactly UP, DOWN, LEFT, or RIGHT", nil)
	}
}

func evaluateSwipe(ctx context.Context, evaluation evaluationContext, command model.Command, compiled any) (evaluatedDispatch, error) {
	plan, ok := compiled.(swipeCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("swipe received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: swipeEvaluated{}}
	rawAppID, err := evaluation.ActiveAppID()
	if err != nil {
		return evaluated, err
	}
	appID, err := evaluation.Interpolate(ctx, rawAppID, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(appID) == "" {
		return evaluated, NewConfigurationError("swipe requires an active appId", nil)
	}
	result := swipeEvaluated{
		mode: plan.mode, appID: appID, durationMillis: plan.durationMillis,
		settleTimeoutMillis: clonePointer(plan.settleTimeoutMillis),
	}
	arguments, ok := evaluated.command.Arguments.(map[string]any)
	if !ok {
		return evaluated, NewConfigurationError("swipe evaluated arguments are unavailable", nil)
	}
	switch plan.mode {
	case swipeModeDirection, swipeModeElement:
		direction, interpolateErr := evaluation.Interpolate(ctx, plan.direction, nil)
		if interpolateErr != nil {
			return evaluated, interpolateErr
		}
		result.direction, err = validatedSwipeDirection(direction)
		if err != nil {
			return evaluated, err
		}
		arguments["direction"] = direction
	case swipeModePoints:
		startPoint, endPoint := plan.startPoint, plan.endPoint
		if plan.startRequiresEval {
			start, interpolateErr := evaluation.Interpolate(ctx, plan.start, nil)
			if interpolateErr != nil {
				return evaluated, interpolateErr
			}
			parsedStart, parseErr := parseInteractionGesturePoint(model.CommandSwipe, start)
			if parseErr != nil {
				return evaluated, parseErr
			}
			startPoint = &parsedStart
			arguments["start"] = start
		}
		if plan.endRequiresEval {
			end, interpolateErr := evaluation.Interpolate(ctx, plan.end, nil)
			if interpolateErr != nil {
				return evaluated, interpolateErr
			}
			parsedEnd, parseErr := parseInteractionGesturePoint(model.CommandSwipe, end)
			if parseErr != nil {
				return evaluated, parseErr
			}
			endPoint = &parsedEnd
			arguments["end"] = end
		}
		if startPoint == nil || endPoint == nil || startPoint.mode != endPoint.mode {
			return evaluated, NewConfigurationError("command swipe start and end must use the same coordinate mode", nil)
		}
		result.start, result.end = clonePointer(startPoint), clonePointer(endPoint)
	default:
		return evaluated, NewConfigurationError("swipe compiled mode is invalid", nil)
	}
	if plan.mode == swipeModeElement {
		selector := cloneSelector(plan.selector)
		if err := interpolateSelector(ctx, evaluation, selector); err != nil {
			return evaluated, err
		}
		if _, err := matching.Find(nil, *selector); err != nil {
			return evaluated, NewConfigurationError("command swipe from selector is invalid after interpolation", err)
		}
		result.selector = selector
		arguments["from"] = synchronizeSelectorArguments(arguments["from"], selector)
		evaluated.command.Selector = cloneSelector(selector)
	}
	evaluated.command.Arguments = arguments
	evaluated.value = result
	return evaluated, nil
}

func executeSwipe(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(swipeEvaluated)
	if !ok {
		return effect, NewConfigurationError("swipe received an invalid evaluated payload", nil)
	}
	if state == nil || isNilValue(state.dependencies.Driver) || isNilValue(state.dependencies.Clock) {
		return effect, NewConfigurationError("swipe requires driver and clock services", nil)
	}
	if plan.durationMillis < 1 || plan.durationMillis > 60000 || strings.TrimSpace(plan.appID) == "" {
		return effect, NewConfigurationError("swipe evaluated plan is invalid", nil)
	}
	if plan.settleTimeoutMillis != nil && (*plan.settleTimeoutMillis < 0 || *plan.settleTimeoutMillis > MaximumSettleTimeout.Milliseconds()) {
		return effect, NewConfigurationError("swipe evaluated settle timeout is invalid", nil)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return effect, err
	}
	request, err := resolveSwipeRequest(ctx, lookup, plan)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	if err := state.dependencies.Driver.Swipe(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	lookup.RecordInteraction(state.dependencies.Clock.Now())
	if cancellation := ctx.Err(); cancellation != nil {
		return effect, cancellation
	}
	return effect, waitForSwipeSettle(ctx, lookup, plan)
}

func resolveSwipeRequest(ctx context.Context, lookup *ElementLookup, plan swipeEvaluated) (device.SwipeRequest, error) {
	switch plan.mode {
	case swipeModeDirection:
		if plan.start != nil || plan.end != nil || plan.selector != nil {
			return device.SwipeRequest{}, NewConfigurationError("swipe direction plan contains incompatible fields", nil)
		}
		return ownedSwipeDirectionRequest(plan.direction, plan.durationMillis), nil
	case swipeModePoints:
		if plan.start == nil || plan.end == nil || plan.direction != "" || plan.selector != nil {
			return device.SwipeRequest{}, NewConfigurationError("swipe coordinate plan contains incompatible fields", nil)
		}
		info, err := lookup.cachedDeviceInfo(ctx)
		if err != nil {
			return device.SwipeRequest{}, err
		}
		start, err := plan.start.resolveScreen(info.WidthGrid, info.HeightGrid)
		if err != nil {
			return device.SwipeRequest{}, err
		}
		end, err := plan.end.resolveScreen(info.WidthGrid, info.HeightGrid)
		if err != nil {
			return device.SwipeRequest{}, err
		}
		return ownedSwipePointRequest(start, end, plan.durationMillis), nil
	case swipeModeElement:
		if plan.selector == nil || plan.direction == "" || plan.start != nil || plan.end != nil {
			return device.SwipeRequest{}, NewConfigurationError("swipe element plan contains incompatible fields", nil)
		}
		element, err := lookup.Find(ctx, *cloneSelector(plan.selector), LookupOptions{})
		if err != nil {
			return device.SwipeRequest{}, err
		}
		if element == nil {
			return device.SwipeRequest{}, NewOperationError("swipe from target not found", nil)
		}
		stability, err := lookup.WaitForElementStability(ctx, element)
		if err != nil {
			return device.SwipeRequest{}, err
		}
		return ownedSwipeElementRequest(hierarchy.Center(stability.Bounds), plan.direction, plan.durationMillis), nil
	default:
		return device.SwipeRequest{}, NewConfigurationError("swipe evaluated mode is invalid", nil)
	}
}

func ownedSwipeDirectionRequest(direction device.Direction, duration int64) device.SwipeRequest {
	return device.SwipeRequest{Direction: direction, DurationMillis: duration}
}

func ownedSwipePointRequest(start, end device.Point, duration int64) device.SwipeRequest {
	ownedStart := device.Point{X: start.X, Y: start.Y}
	ownedEnd := device.Point{X: end.X, Y: end.Y}
	return device.SwipeRequest{Start: &ownedStart, End: &ownedEnd, DurationMillis: duration}
}

func ownedSwipeElementRequest(point device.Point, direction device.Direction, duration int64) device.SwipeRequest {
	ownedPoint := device.Point{X: point.X, Y: point.Y}
	return device.SwipeRequest{Direction: direction, ElementPoint: &ownedPoint, DurationMillis: duration}
}

func waitForSwipeSettle(ctx context.Context, lookup *ElementLookup, plan swipeEvaluated) error {
	request := device.SettleRequest{AppID: plan.appID, TimeoutMillis: clonePointer(plan.settleTimeoutMillis)}
	_, err := lookup.WaitForHierarchySettle(ctx, request)
	if err == nil {
		return nil
	}
	normalized := normalizeTerminalError(fmt.Sprintf("command %s settle failed", model.CommandSwipe), err)
	switch classifyTerminalError(normalized) {
	case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
		return normalized
	default:
		return nil
	}
}
