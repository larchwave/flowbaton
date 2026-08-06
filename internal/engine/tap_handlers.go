package engine

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

const (
	maximumInt64                = int64(^uint64(0) >> 1)
	tapRetryDifferenceThreshold = 0.005
)

type tapTargetMode uint8

const (
	tapTargetInvalid tapTargetMode = iota
	tapTargetSelectorCenter
	tapTargetScreenPoint
	tapTargetSelectorPoint
)

type tapCoordinateMode uint8

const (
	tapCoordinateInvalid tapCoordinateMode = iota
	tapCoordinateAbsolute
	tapCoordinatePercent
)

type tapPointPlan struct {
	mode tapCoordinateMode
	x    int64
	y    int64
}

type tapOnCompiled struct {
	targetMode          tapTargetMode
	point               *tapPointPlan
	pointRequiresEval   bool
	repeat              int
	repeatPresent       bool
	delayMillis         int64
	delayMetadata       bool
	settleTimeoutMillis *int64
	retryTapIfNoChange  bool
	waitUntilVisible    bool
}

type tapOnEvaluated struct {
	appID                 string
	targetMode            tapTargetMode
	targetSelector        *model.ElementSelector
	optional              bool
	point                 *tapPointPlan
	repeat                int
	delay                 time.Duration
	effectiveSettleMillis *int64
	retryTapIfNoChange    bool
	waitUntilVisible      bool
}

func compileTapOn(command model.Command) (any, error) {
	if err := validateTapOnCommand(command); err != nil {
		return nil, err
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

	compiled := tapOnCompiled{
		targetMode:          targetMode,
		repeat:              1,
		settleTimeoutMillis: cloneSettleTimeout(selector.WaitToSettleTimeoutMS),
	}
	if selector.RetryTapIfNoChange != nil {
		compiled.retryTapIfNoChange = *selector.RetryTapIfNoChange
	}
	if selector.WaitUntilVisible != nil {
		compiled.waitUntilVisible = *selector.WaitUntilVisible
	}
	if selector.Point != nil {
		compiled.pointRequiresEval = tapPointRequiresEvaluation(*selector.Point)
		if !compiled.pointRequiresEval {
			point, err := parseTapPoint(*selector.Point)
			if err != nil {
				return nil, err
			}
			compiled.point = &point
		}
	}
	if selector.Repeat != nil {
		if *selector.Repeat < 1 {
			return nil, NewConfigurationError("command tapOn repeat must be at least 1", nil)
		}
		compiled.repeat = *selector.Repeat
		compiled.repeatPresent = true
		compiled.delayMillis = RepeatDelay.Milliseconds()
		compiled.delayMetadata = true
	}
	if selector.Delay != nil {
		if *selector.Delay < 0 {
			return nil, NewConfigurationError("command tapOn delay must not be negative", nil)
		}
		if compiled.repeatPresent {
			compiled.delayMillis = int64(*selector.Delay)
			compiled.delayMetadata = true
		}
	}
	if compiled.delayMillis > maximumInt64/int64(time.Millisecond) {
		return nil, NewConfigurationError("command tapOn delay is too large", nil)
	}
	return compiled, nil
}

func validateTapOnCommand(command model.Command) error {
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
		return NewConfigurationError("command tapOn selector, label, or optional flag does not match its typed snapshot", nil)
	}
	if err := validateTapSelectorFeatures(command.Kind, command.Selector, true); err != nil {
		return err
	}
	if err := validateTapSelectorModifierSemantics(command.Kind, command.Selector); err != nil {
		return err
	}
	hasSelector := selectorHasImplementedTarget(command.Selector)
	if !hasSelector && command.Selector.Point == nil {
		return NewConfigurationError("command tapOn requires an implemented target predicate or point", nil)
	}
	if !hasSelector {
		if command.Selector.WaitUntilVisible != nil {
			return NewConfigurationError("command tapOn point-only form cannot observe waitUntilVisible", nil)
		}
		if tapPointOnlyHasUnrelatedSelectorFields(command.Selector) {
			return NewConfigurationError("command tapOn point-only form contains unrelated selector fields", nil)
		}
		return nil
	}
	return validateImplementedSelectorTargets(command.Kind, command.Selector, "selector")
}

func validateTapSelectorModifierSemantics(keyword model.CommandKeyword, selector *model.ElementSelector) error {
	if selector == nil {
		return nil
	}
	if selector.Size != nil && selector.Size.Width == nil && selector.Size.Height == nil {
		return NewConfigurationError(fmt.Sprintf("command %s selector tolerance requires width or height", keyword), nil)
	}
	for _, nested := range []*model.ElementSelector{
		selector.Below,
		selector.Above,
		selector.LeftOf,
		selector.RightOf,
		selector.ContainsChild,
		selector.ChildOf,
	} {
		if err := validateTapSelectorModifierSemantics(keyword, nested); err != nil {
			return err
		}
	}
	for index := range selector.ContainsDescendants {
		if err := validateTapSelectorModifierSemantics(keyword, &selector.ContainsDescendants[index]); err != nil {
			return err
		}
	}
	return nil
}

func tapPointOnlyHasUnrelatedSelectorFields(selector *model.ElementSelector) bool {
	if selector == nil {
		return false
	}
	return selector.TextRegex != nil || selector.IDRegex != nil || selector.Size != nil ||
		selector.Below != nil || selector.Above != nil || selector.LeftOf != nil || selector.RightOf != nil ||
		selector.ContainsChild != nil || selector.ContainsDescendants != nil || selector.ChildOf != nil ||
		selector.Traits != nil || selector.Index != nil || selector.Enabled != nil || selector.Selected != nil ||
		selector.Checked != nil || selector.Focused != nil || selector.CSS != nil
}

func validateTapSelectorFeatures(keyword model.CommandKeyword, selector *model.ElementSelector, topLevel bool) error {
	if selector == nil {
		return nil
	}
	unsupported := ""
	switch {
	case selector.Start != nil:
		unsupported = "start"
	case selector.End != nil:
		unsupported = "end"
	case !topLevel && selector.RetryTapIfNoChange != nil:
		unsupported = "retryTapIfNoChange"
	case !topLevel && selector.WaitUntilVisible != nil:
		unsupported = "waitUntilVisible"
	// CSS selectors are resolved by ElementLookup.resolveCSS.
	case !topLevel && selector.Point != nil:
		unsupported = "nested point"
	case !topLevel && selector.Repeat != nil:
		unsupported = "nested repeat"
	case !topLevel && selector.Delay != nil:
		unsupported = "nested delay"
	case !topLevel && selector.WaitToSettleTimeoutMS != nil:
		unsupported = "nested waitToSettleTimeoutMs"
	case !topLevel && selector.Optional != nil:
		unsupported = "nested optional"
	}
	if unsupported != "" {
		return NewConfigurationError(fmt.Sprintf("command %s selector feature %s is not implemented", keyword, unsupported), nil)
	}
	for _, nested := range []*model.ElementSelector{
		selector.Below,
		selector.Above,
		selector.LeftOf,
		selector.RightOf,
		selector.ContainsChild,
		selector.ChildOf,
	} {
		if err := validateTapSelectorFeatures(keyword, nested, false); err != nil {
			return err
		}
	}
	for index := range selector.ContainsDescendants {
		if err := validateTapSelectorFeatures(keyword, &selector.ContainsDescendants[index], false); err != nil {
			return err
		}
	}
	return nil
}

func tapPointRequiresEvaluation(source string) bool {
	return js.HasInterpolationExpression(source)
}

func parseTapPoint(source string) (tapPointPlan, error) {
	if strings.Count(source, ",") != 1 {
		return tapPointPlan{}, NewConfigurationError("command tapOn point requires exactly two comma-separated integers", nil)
	}
	tokens := strings.Split(source, ",")
	values := [2]int64{}
	percent := [2]bool{}
	for index, sourceToken := range tokens {
		token := strings.TrimSpace(sourceToken)
		if token == "" {
			return tapPointPlan{}, NewConfigurationError("command tapOn point contains an empty coordinate", nil)
		}
		percent[index] = strings.HasSuffix(token, "%")
		if percent[index] {
			token = strings.TrimSuffix(token, "%")
		}
		if !strictBase10Integer(token) {
			return tapPointPlan{}, NewConfigurationError("command tapOn point coordinates must be base-10 integers", nil)
		}
		value, err := strconv.ParseInt(token, 10, 64)
		if err != nil {
			return tapPointPlan{}, NewConfigurationError("command tapOn point coordinate is outside the supported integer range", err)
		}
		if value < 0 {
			return tapPointPlan{}, NewConfigurationError("command tapOn point coordinates must not be negative", nil)
		}
		if percent[index] && value >= 100 {
			return tapPointPlan{}, NewConfigurationError("command tapOn percentage coordinates must be less than 100", nil)
		}
		values[index] = value
	}
	if percent[0] != percent[1] {
		return tapPointPlan{}, NewConfigurationError("command tapOn point coordinates must both be absolute or both be percentages", nil)
	}
	mode := tapCoordinateAbsolute
	if percent[0] {
		mode = tapCoordinatePercent
	}
	return tapPointPlan{mode: mode, x: values[0], y: values[1]}, nil
}

func strictBase10Integer(value string) bool {
	if value == "" {
		return false
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return false
		}
		start = 1
	}
	for index := start; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func evaluateTapOn(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(tapOnCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("tapOn received an invalid compiled payload", nil)
	}
	evaluated, err := evaluateSelectorCommand(ctx, evaluation, command)
	evaluated.value = tapOnEvaluated{}
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
		return evaluated, NewConfigurationError("tapOn requires an active appId", nil)
	}

	var point *tapPointPlan
	if plan.targetMode == tapTargetScreenPoint || plan.targetMode == tapTargetSelectorPoint {
		if evaluated.command.Selector == nil || evaluated.command.Selector.Point == nil {
			return evaluated, NewConfigurationError("tapOn evaluated point is unavailable", nil)
		}
		parsed := tapPointPlan{}
		if plan.pointRequiresEval {
			parsed, err = parseTapPoint(*evaluated.command.Selector.Point)
			if err != nil {
				return evaluated, err
			}
		} else if plan.point != nil {
			parsed = *plan.point
		} else {
			return evaluated, NewConfigurationError("tapOn compiled point is unavailable", nil)
		}
		point = &parsed
	}

	updateTapEvaluatedMetadata(&evaluated.command, plan)
	var targetSelector *model.ElementSelector
	if plan.targetMode == tapTargetSelectorCenter || plan.targetMode == tapTargetSelectorPoint {
		targetSelector, err = tapTargetOnlySelector(evaluated.command)
		if err != nil {
			return evaluated, err
		}
	}
	evaluated.value = tapOnEvaluated{
		appID:                 appID,
		targetMode:            plan.targetMode,
		targetSelector:        targetSelector,
		optional:              commandIsOptional(evaluated.command),
		point:                 point,
		repeat:                plan.repeat,
		delay:                 time.Duration(plan.delayMillis) * time.Millisecond,
		effectiveSettleMillis: effectiveTapSettleTimeout(plan.settleTimeoutMillis),
		retryTapIfNoChange:    plan.retryTapIfNoChange,
		waitUntilVisible:      plan.waitUntilVisible,
	}
	return evaluated, nil
}

func updateTapEvaluatedMetadata(command *model.Command, plan tapOnCompiled) {
	if command == nil || command.Selector == nil {
		return
	}
	fields, object := command.Arguments.(map[string]any)
	if object {
		fields = cloneDynamic(fields).(map[string]any)
	}
	if plan.repeatPresent && plan.delayMetadata {
		delay := int(plan.delayMillis)
		command.Selector.Delay = &delay
		if object {
			fields["delay"] = plan.delayMillis
		}
	} else {
		command.Selector.Delay = nil
		if object {
			delete(fields, "delay")
		}
	}
	if object {
		command.Arguments = fields
	}
}

func effectiveTapSettleTimeout(authored *int64) *int64 {
	if authored == nil {
		return nil
	}
	effective := *authored
	if effective < 0 {
		effective = 0
	}
	if maximum := MaximumSettleTimeout.Milliseconds(); effective > maximum {
		effective = maximum
	}
	return &effective
}

func executeTapOn(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation, numberOfRunsSet: true}
	plan, ok := evaluated.value.(tapOnEvaluated)
	if !ok {
		return effect, NewConfigurationError("tapOn received an invalid evaluated payload", nil)
	}
	if err := validateTapExecutionPlan(plan); err != nil {
		return effect, err
	}
	if state == nil || isNilValue(state.dependencies.Driver) || isNilValue(state.dependencies.Clock) {
		return effect, NewConfigurationError("tapOn requires driver and clock services", nil)
	}
	if plan.retryTapIfNoChange && isNilValue(state.dependencies.ImageChecker) {
		return effect, NewConfigurationError("image checker is required by tapOn retryTapIfNoChange", nil)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return effect, err
	}

	point, err := resolveTapPoint(ctx, lookup, plan)
	if err != nil {
		return effect, err
	}
	var expectedHierarchy *device.ViewHierarchy
	var expectedScreenshot []byte
	if plan.retryTapIfNoChange {
		expectedHierarchy, err = captureTapRetryHierarchy(ctx, state)
		if err != nil {
			return effect, err
		}
		expectedScreenshot, err = captureTapRetryScreenshot(ctx, state)
		if err != nil {
			return effect, err
		}
	}

	effect.numberOfRuns = 1
	postHierarchy, err := executeResolvedTapBatch(ctx, state, lookup, plan, point, expectedHierarchy)
	if err != nil {
		return effect, err
	}
	if plan.retryTapIfNoChange && reflect.DeepEqual(expectedHierarchy, postHierarchy) {
		actual, screenshotErr := captureTapRetryScreenshot(ctx, state)
		if screenshotErr != nil {
			return effect, screenshotErr
		}
		check, checkErr := state.checkImages(ctx, ImageCheckRequest{
			Expected: expectedScreenshot,
			Actual:   actual,
		})
		if checkErr != nil {
			return effect, checkErr
		}
		withinThreshold, thresholdErr := check.WithinThreshold(tapRetryDifferenceThreshold)
		if thresholdErr != nil {
			return effect, NewConfigurationError("tapOn retry check threshold is invalid", thresholdErr)
		}
		if withinThreshold {
			point, err = resolveTapPoint(ctx, lookup, plan)
			if err != nil {
				return effect, err
			}
			effect.numberOfRuns = 2
			if _, err = executeResolvedTapBatch(ctx, state, lookup, plan, point, nil); err != nil {
				return effect, err
			}
		}
	}
	if plan.waitUntilVisible {
		if waitErr := waitForTapTargetVisible(ctx, lookup, *cloneSelector(plan.targetSelector)); waitErr != nil {
			return effect, waitErr
		}
	}
	return effect, nil
}

func executeResolvedTapBatch(
	ctx context.Context,
	state *executionState,
	lookup *ElementLookup,
	plan tapOnEvaluated,
	point device.Point,
	initialHierarchy *device.ViewHierarchy,
) (*device.ViewHierarchy, error) {
	for index := 0; index < plan.repeat; index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := state.dependencies.Driver.Tap(ctx, device.TapRequest{Point: point}); err != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				return nil, cancellation
			}
			return nil, err
		}
		lookup.RecordInteraction(state.dependencies.Clock.Now())
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if index+1 < plan.repeat {
			if err := state.dependencies.Clock.Wait(ctx, plan.delay); err != nil {
				if cancellation := ctx.Err(); cancellation != nil {
					return nil, cancellation
				}
				return nil, err
			}
			if cancellation := ctx.Err(); cancellation != nil {
				return nil, cancellation
			}
		}
	}
	return waitForTapOwnedSettle(ctx, lookup, plan, initialHierarchy)
}

func validateTapExecutionPlan(plan tapOnEvaluated) error {
	if strings.TrimSpace(plan.appID) == "" {
		return NewConfigurationError("tapOn evaluated plan requires a non-blank appId", nil)
	}
	if plan.repeat < 1 || plan.delay < 0 {
		return NewConfigurationError("tapOn evaluated repeat/delay plan is invalid", nil)
	}
	switch plan.targetMode {
	case tapTargetSelectorCenter:
		if plan.point != nil {
			return NewConfigurationError("tapOn selector-center plan must not contain a point", nil)
		}
		if plan.targetSelector == nil {
			return NewConfigurationError("tapOn selector-center plan requires a selector", nil)
		}
	case tapTargetScreenPoint:
		if plan.point == nil || plan.point.mode < tapCoordinateAbsolute || plan.point.mode > tapCoordinatePercent {
			return NewConfigurationError("tapOn point plan is invalid", nil)
		}
		if plan.targetSelector != nil {
			return NewConfigurationError("tapOn screen-point plan must not contain a selector", nil)
		}
	case tapTargetSelectorPoint:
		if plan.point == nil || plan.point.mode < tapCoordinateAbsolute || plan.point.mode > tapCoordinatePercent || plan.targetSelector == nil {
			return NewConfigurationError("tapOn selector-point plan is invalid", nil)
		}
	default:
		return NewConfigurationError("tapOn target mode is invalid", nil)
	}
	if plan.effectiveSettleMillis != nil && (*plan.effectiveSettleMillis < 0 || *plan.effectiveSettleMillis > MaximumSettleTimeout.Milliseconds()) {
		return NewConfigurationError("tapOn effective settle timeout is invalid", nil)
	}
	if plan.waitUntilVisible && plan.targetMode == tapTargetScreenPoint {
		return NewConfigurationError("tapOn point-only plan cannot observe waitUntilVisible", nil)
	}
	return nil
}

func resolveTapPoint(
	ctx context.Context,
	lookup *ElementLookup,
	plan tapOnEvaluated,
) (device.Point, error) {
	switch plan.targetMode {
	case tapTargetScreenPoint:
		info, err := lookup.cachedDeviceInfo(ctx)
		if err != nil {
			return device.Point{}, err
		}
		return plan.point.resolveScreen(info.WidthGrid, info.HeightGrid)
	case tapTargetSelectorCenter, tapTargetSelectorPoint:
		if plan.targetSelector == nil {
			return device.Point{}, NewConfigurationError("tapOn selector target is unavailable", nil)
		}
		element, err := lookup.Find(ctx, *cloneSelector(plan.targetSelector), LookupOptions{Optional: plan.optional})
		if err != nil {
			return device.Point{}, err
		}
		if element == nil {
			return device.Point{}, NewOperationError("tapOn target not found", nil)
		}
		stability, err := lookup.WaitForElementStability(ctx, element)
		if err != nil {
			return device.Point{}, err
		}
		if plan.targetMode == tapTargetSelectorCenter {
			return hierarchy.Center(stability.Bounds), nil
		}
		return plan.point.resolveRelative(stability.Bounds)
	default:
		return device.Point{}, NewConfigurationError("tapOn target mode is invalid", nil)
	}
}

func (point tapPointPlan) resolveScreen(width, height int) (device.Point, error) {
	if width <= 0 || height <= 0 {
		return device.Point{}, NewConfigurationError("tapOn requires a positive screen grid", nil)
	}
	x, err := point.resolveAxis(int64(width), point.x, "x")
	if err != nil {
		return device.Point{}, err
	}
	y, err := point.resolveAxis(int64(height), point.y, "y")
	if err != nil {
		return device.Point{}, err
	}
	return device.Point{X: float64(x), Y: float64(y)}, nil
}

func (point tapPointPlan) resolveRelative(bounds device.Bounds) (device.Point, error) {
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return device.Point{}, NewConfigurationError("tapOn relative point requires positive stable bounds", nil)
	}
	xOffset, err := point.resolveAxis(int64(bounds.Width), point.x, "x")
	if err != nil {
		return device.Point{}, err
	}
	yOffset, err := point.resolveAxis(int64(bounds.Height), point.y, "y")
	if err != nil {
		return device.Point{}, err
	}
	x, err := addTapCoordinate(int64(bounds.X), xOffset)
	if err != nil {
		return device.Point{}, err
	}
	y, err := addTapCoordinate(int64(bounds.Y), yOffset)
	if err != nil {
		return device.Point{}, err
	}
	return device.Point{X: float64(x), Y: float64(y)}, nil
}

func (point tapPointPlan) resolveAxis(dimension, authored int64, axis string) (int64, error) {
	if dimension <= 0 {
		return 0, NewConfigurationError(fmt.Sprintf("tapOn %s dimension must be positive", axis), nil)
	}
	switch point.mode {
	case tapCoordinateAbsolute:
		if authored < 0 || authored >= dimension {
			return 0, NewConfigurationError(fmt.Sprintf("tapOn absolute %s coordinate is outside the half-open bounds", axis), nil)
		}
		return authored, nil
	case tapCoordinatePercent:
		if authored < 0 || authored >= 100 {
			return 0, NewConfigurationError(fmt.Sprintf("tapOn percentage %s coordinate is outside [0,100)", axis), nil)
		}
		return floorTapPercentage(dimension, authored), nil
	default:
		return 0, NewConfigurationError("tapOn coordinate mode is invalid", nil)
	}
}

func floorTapPercentage(dimension, percent int64) int64 {
	quotient, remainder := dimension/100, dimension%100
	return quotient*percent + remainder*percent/100
}

func addTapCoordinate(origin, offset int64) (int64, error) {
	if offset > 0 && origin > maximumInt64-offset {
		return 0, NewConfigurationError("tapOn relative coordinate overflows the supported integer range", nil)
	}
	return origin + offset, nil
}

func captureTapRetryHierarchy(ctx context.Context, state *executionState) (*device.ViewHierarchy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := state.dependencies.Driver.ContentDescriptor(
		ctx, device.ContentDescriptorRequest{AppIDs: activeAppIDsFor(state)})
	if cancellation := ctx.Err(); cancellation != nil {
		return nil, cancellation
	}
	if err != nil {
		return nil, err
	}
	return &device.ViewHierarchy{Root: cloneSettleTreeNode(root)}, nil
}

func captureTapRetryScreenshot(ctx context.Context, state *executionState) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := state.dependencies.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{Compressed: false})
	if cancellation := ctx.Err(); cancellation != nil {
		return nil, cancellation
	}
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), data...), nil
}

func tapTargetOnlySelector(command model.Command) (*model.ElementSelector, error) {
	if command.Selector == nil || !selectorHasImplementedTarget(command.Selector) {
		return nil, NewConfigurationError("tapOn requires a selector target", nil)
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

func waitForTapTargetVisible(ctx context.Context, lookup *ElementLookup, selector model.ElementSelector) error {
	for observation := 0; observation < WaitUntilVisibleAttempts; observation++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		element, err := lookup.findOnce(ctx, selector)
		if cancellation := ctx.Err(); cancellation != nil {
			return cancellation
		}
		if err != nil {
			return err
		}
		if element != nil {
			return nil
		}
		if observation+1 == WaitUntilVisibleAttempts {
			break
		}
		if err := lookup.clock.Wait(ctx, WaitUntilVisiblePollInterval); err != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				return cancellation
			}
			return err
		}
		if cancellation := ctx.Err(); cancellation != nil {
			return cancellation
		}
	}
	return NewAssertionError("tapOn target did not become visible after 10 observations", nil)
}

func waitForTapOwnedSettle(
	ctx context.Context,
	lookup *ElementLookup,
	plan tapOnEvaluated,
	initialHierarchy *device.ViewHierarchy,
) (*device.ViewHierarchy, error) {
	request := device.SettleRequest{
		AppID:            plan.appID,
		TimeoutMillis:    clonePointer(plan.effectiveSettleMillis),
		InitialHierarchy: cloneSettleHierarchy(initialHierarchy),
	}
	latest, err := lookup.WaitForHierarchySettle(ctx, request)
	if err == nil {
		return cloneSettleHierarchy(latest), nil
	}
	normalized := normalizeTerminalError("command tapOn settle failed", err)
	switch classifyTerminalError(normalized) {
	case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
		return cloneSettleHierarchy(latest), normalized
	default:
		return cloneSettleHierarchy(latest), nil
	}
}
