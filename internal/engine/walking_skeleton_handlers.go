package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

type launchAppCompiled struct {
	explicitAppID *string
	clearState    bool
	clearKeychain bool
	// stopApp defaults to true: specs/06-launch-app-semantics.md section 1.
	stopApp     bool
	permissions map[string]string
	arguments   []device.LaunchArgument
}

type launchAppEvaluated struct {
	appID         string
	clearState    bool
	clearKeychain bool
	stopApp       bool
	permissions   map[string]string
	arguments     []device.LaunchArgument
}

type assertVisibleCompiled struct{}

type assertVisibleEvaluated struct{}

func productionHandlerRegistry() (handlerRegistry, error) {
	specs := []handlerSpec{
		{
			keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation,
			postAction: postActionSettle, settleRequest: launchAppSettleRequest,
			compile: pureCompiler(compileLaunchApp), evaluate: evaluateLaunchApp, execute: executeLaunchApp,
		},
		{
			keyword: model.CommandTapOn, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compileTapOn), evaluate: evaluateTapOn, execute: executeTapOn,
		},
		{
			keyword: model.CommandAssertVisible, effectClass: EffectObserved,
			compile: pureCompiler(compileAssertVisible), evaluate: evaluateAssertVisible, execute: executeAssertVisible,
		},
		assertNotVisibleHandlerSpec(),
		assertTrueHandlerSpec(),
		extendedWaitUntilHandlerSpec(),
		runFlowHandlerSpec(),
		repeatHandlerSpec(),
		retryHandlerSpec(),
	}
	// Register the complete non-AI interaction command family.
	specs = append(specs,
		doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec(),
		backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec(),
		scrollUntilVisibleHandlerSpec(), inputTextHandlerSpec(), eraseTextHandlerSpec(),
	)
	specs = append(specs, randomInputHandlerSpecs()...)
	specs = append(specs, clipboardHandlerSpecs()...)
	specs = append(specs, actionHandlerSpec())
	// App-lifecycle commands map to their driver operations.
	specs = append(specs, lifecycleHandlerSpecs()...)
	// Navigation commands include links, browser launch, and animation waits.
	specs = append(specs, navigationHandlerSpecs()...)
	// Device-state commands map directly to driver operations.
	specs = append(specs, deviceStateHandlerSpecs()...)
	// Media and artifact commands use their shared service set.
	specs = append(specs, mediaHandlerSpecs()...)
	// assertScreenshot also requires expected-image resolution and image checking.
	specs = append(specs, assertScreenshotHandlerSpec())
	// Scripting commands share the JavaScript runtime.
	specs = append(specs, scriptHandlerSpecs()...)
	// Travel follows the emission schedule in specs/05-command-semantics-addendum.md §2.3.
	specs = append(specs, travelHandlerSpec())
	// Screenshot-based AI commands require an injected AIPredictionEngine and
	// fail closed when none is configured (specs/01-core-engine.md).
	specs = append(specs, aiHandlerSpecs()...)
	return newHandlerRegistry(specs...)
}

func compileLaunchApp(command model.Command) (any, error) {
	object, _, err := decodeOptionalObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown(
		"appId", "clearState", "clearKeychain", "stopApp", "permissions", "arguments",
	); err != nil {
		return nil, err
	}
	appID, explicit, err := object.optionalString("appId")
	if err != nil {
		return nil, err
	}
	if len(command.Children) != 0 {
		return nil, commandDecodeError(command.Kind, "must not contain child commands")
	}
	payload := launchAppCompiled{stopApp: true}
	if explicit {
		payload.explicitAppID = &appID
	}
	if payload.clearState, _, err = object.optionalBool("clearState"); err != nil {
		return nil, err
	}
	if payload.clearKeychain, _, err = object.optionalBool("clearKeychain"); err != nil {
		return nil, err
	}
	if stopApp, authored, stopErr := object.optionalBool("stopApp"); stopErr != nil {
		return nil, stopErr
	} else if authored {
		payload.stopApp = stopApp
	}
	if payload.permissions, err = decodeLaunchPermissions(command, object); err != nil {
		return nil, err
	}
	if payload.arguments, err = decodeLaunchArguments(command, object); err != nil {
		return nil, err
	}
	return payload, nil
}

// decodeLaunchPermissions reuses setPermissions' exact grant validation, so one
// vocabulary is enforced in both places rather than two that can drift.
func decodeLaunchPermissions(command model.Command, object decodedObject) (map[string]string, error) {
	authored, exists := object.raw("permissions")
	if !exists {
		return nil, nil
	}
	fields, ok := authored.(map[string]any)
	if !ok {
		return nil, object.fieldError("permissions", "must be an object")
	}
	return decodePermissionGrants(command.Kind, decodedObject{command: command.Kind, fields: fields})
}

// decodeLaunchArguments renders the authored map into the frozen typed list,
// sorted by key. The parser hands over an unordered map, and map iteration
// order must never be what a driver request carries.
func decodeLaunchArguments(command model.Command, object decodedObject) ([]device.LaunchArgument, error) {
	authored, exists := object.raw("arguments")
	if !exists {
		return nil, nil
	}
	fields, ok := authored.(map[string]any)
	if !ok {
		return nil, object.fieldError("arguments", "must be an object")
	}
	if len(fields) == 0 {
		return nil, object.fieldError("arguments", "must not be empty when authored")
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	arguments := make([]device.LaunchArgument, 0, len(keys))
	for _, key := range keys {
		value, valueType, err := renderLaunchArgument(command.Kind, key, fields[key])
		if err != nil {
			return nil, err
		}
		arguments = append(arguments, device.LaunchArgument{Key: key, Value: value, Type: valueType})
	}
	return arguments, nil
}

// renderLaunchArgument carries the authored YAML type alongside the rendered
// string, using the vocabulary the public documentation names: string,
// boolean, double, integer. Anything else is not an authorable argument.
func renderLaunchArgument(keyword model.CommandKeyword, key string, value any) (string, string, error) {
	switch typed := value.(type) {
	case string:
		return typed, "string", nil
	case bool:
		return strconv.FormatBool(typed), "boolean", nil
	case int64:
		return strconv.FormatInt(typed, 10), "integer", nil
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64), "double", nil
	default:
		return "", "", NewConfigurationError(fmt.Sprintf(
			"command %s argument %s must be a string, boolean, integer, or double", keyword, key), nil)
	}
}

func compileAssertVisible(command model.Command) (any, error) {
	if err := validateSelectorCommand(command); err != nil {
		return nil, err
	}
	if err := rejectUnsupportedSelectorFeatures(command.Kind, command.Selector, false, true); err != nil {
		return nil, err
	}
	return assertVisibleCompiled{}, nil
}

func cloneSettleTimeout(timeout *int) *int64 {
	if timeout == nil {
		return nil
	}
	value := int64(*timeout)
	return &value
}

func validateSelectorCommand(command model.Command) error {
	if command.Form != model.CommandFormObject {
		return commandDecodeError(command.Kind, "requires object form with a selector")
	}
	if command.Selector == nil {
		return commandDecodeError(command.Kind, "requires a selector")
	}
	if err := validateImplementedSelectorTargets(command.Kind, command.Selector, "selector"); err != nil {
		return err
	}
	if len(command.Children) != 0 {
		return commandDecodeError(command.Kind, "must not contain child commands")
	}
	return nil
}

func validateImplementedSelectorTargets(
	keyword model.CommandKeyword,
	selector *model.ElementSelector,
	path string,
) error {
	if selector == nil || !selectorHasImplementedTarget(selector) {
		return NewConfigurationError(
			fmt.Sprintf("command %s %s requires an implemented target predicate", keyword, path),
			nil,
		)
	}
	nested := []struct {
		name     string
		selector *model.ElementSelector
	}{
		{name: "below", selector: selector.Below},
		{name: "above", selector: selector.Above},
		{name: "leftOf", selector: selector.LeftOf},
		{name: "rightOf", selector: selector.RightOf},
		{name: "containsChild", selector: selector.ContainsChild},
		{name: "childOf", selector: selector.ChildOf},
	}
	for _, entry := range nested {
		if entry.selector == nil {
			continue
		}
		if err := validateImplementedSelectorTargets(
			keyword,
			entry.selector,
			path+"."+entry.name,
		); err != nil {
			return err
		}
	}
	for index := range selector.ContainsDescendants {
		if err := validateImplementedSelectorTargets(
			keyword,
			&selector.ContainsDescendants[index],
			fmt.Sprintf("%s.containsDescendants[%d]", path, index),
		); err != nil {
			return err
		}
	}
	return nil
}

func selectorHasImplementedTarget(selector *model.ElementSelector) bool {
	if selector == nil {
		return false
	}
	return selector.TextRegex != nil ||
		selector.IDRegex != nil ||
		selector.Size != nil && (selector.Size.Width != nil || selector.Size.Height != nil) ||
		len(selector.Traits) > 0 ||
		selector.Enabled != nil ||
		selector.Selected != nil ||
		selector.Checked != nil ||
		selector.Focused != nil ||
		selector.Below != nil ||
		selector.Above != nil ||
		selector.LeftOf != nil ||
		selector.RightOf != nil ||
		selector.ContainsChild != nil ||
		len(selector.ContainsDescendants) > 0 ||
		selector.ChildOf != nil ||
		// CSS queries are resolved by ElementLookup.resolveCSS.
		selector.CSS != nil
}

func rejectUnsupportedSelectorFeatures(
	keyword model.CommandKeyword,
	selector *model.ElementSelector,
	allowTopLevelSettleTimeout bool,
	topLevel bool,
) error {
	if selector == nil {
		return nil
	}
	unsupported := ""
	switch {
	case selector.Point != nil:
		unsupported = "point"
	case selector.Start != nil:
		unsupported = "start"
	case selector.End != nil:
		unsupported = "end"
	case selector.RetryTapIfNoChange != nil:
		unsupported = "retryTapIfNoChange"
	case selector.WaitUntilVisible != nil:
		unsupported = "waitUntilVisible"
	case selector.Repeat != nil:
		unsupported = "repeat"
	case selector.Delay != nil:
		unsupported = "delay"
	case selector.WaitToSettleTimeoutMS != nil && (!allowTopLevelSettleTimeout || !topLevel):
		unsupported = "waitToSettleTimeoutMs"
	// CSS selectors pass to ElementLookup.resolveCSS. Capability preflight
	// restricts them to supported platforms.
	case selector.Optional != nil && !topLevel:
		unsupported = "nested optional"
	}
	if unsupported != "" {
		return NewConfigurationError(
			fmt.Sprintf("command %s selector feature %s is not implemented", keyword, unsupported),
			nil,
		)
	}

	for _, nested := range []*model.ElementSelector{
		selector.Below,
		selector.Above,
		selector.LeftOf,
		selector.RightOf,
		selector.ContainsChild,
		selector.ChildOf,
	} {
		if err := rejectUnsupportedSelectorFeatures(keyword, nested, allowTopLevelSettleTimeout, false); err != nil {
			return err
		}
	}
	for index := range selector.ContainsDescendants {
		if err := rejectUnsupportedSelectorFeatures(
			keyword,
			&selector.ContainsDescendants[index],
			allowTopLevelSettleTimeout,
			false,
		); err != nil {
			return err
		}
	}
	return nil
}

func evaluateLaunchApp(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(launchAppCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("launchApp received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: launchAppEvaluated{}}
	rawAppID := ""
	if payload.explicitAppID != nil {
		rawAppID = *payload.explicitAppID
	} else {
		var err error
		rawAppID, err = evaluation.ActiveAppID()
		if err != nil {
			return evaluated, err
		}
	}
	appID, err := evaluation.Interpolate(ctx, rawAppID, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(appID) == "" {
		return evaluated, NewConfigurationError("launchApp requires an active or explicit appId", nil)
	}
	evaluated.command.Form = model.CommandFormObject
	evaluated.command.Arguments = map[string]any{"appId": appID}
	// The grant map and the argument list are copied so the driver requests
	// never alias the compiled payload, which outlives every evaluation.
	evaluated.value = launchAppEvaluated{
		appID: appID, clearState: payload.clearState, clearKeychain: payload.clearKeychain,
		stopApp: payload.stopApp, permissions: cloneStringMap(payload.permissions),
		arguments: append([]device.LaunchArgument(nil), payload.arguments...),
	}
	return evaluated, nil
}

func evaluateAssertVisible(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if _, ok := compiled.(assertVisibleCompiled); !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertVisible received an invalid compiled payload", nil)
	}
	evaluated, err := evaluateSelectorCommand(ctx, evaluation, command)
	evaluated.value = assertVisibleEvaluated{}
	return evaluated, err
}

func evaluateSelectorCommand(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
) (evaluatedDispatch, error) {
	evaluated := evaluatedDispatch{command: cloneCommand(command)}
	if evaluated.command.Selector == nil {
		return evaluated, NewConfigurationError(fmt.Sprintf("command %s requires a selector", command.Kind), nil)
	}
	if err := interpolateSelector(ctx, evaluation, evaluated.command.Selector); err != nil {
		return evaluated, err
	}
	if _, err := matching.Find(nil, *evaluated.command.Selector); err != nil {
		return evaluated, NewConfigurationError(fmt.Sprintf("command %s selector is invalid after interpolation", command.Kind), err)
	}
	evaluated.command.Arguments = synchronizeSelectorArguments(command.Arguments, evaluated.command.Selector)
	evaluated.command.Label = clonePointer(evaluated.command.Selector.Label)
	return evaluated, nil
}

func executeLaunchApp(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	payload, ok := evaluated.value.(launchAppEvaluated)
	if !ok {
		return commandEffect{}, NewConfigurationError("launchApp received an invalid evaluated payload", nil)
	}
	// specs/06-launch-app-semantics.md section 2 defines the order: keychain,
	// state, permissions, then launch. Launch stops the app first unless stopApp
	// was authored false.
	driver := state.dependencies.Driver
	if payload.clearKeychain {
		if err := driver.ClearKeychain(ctx); err != nil {
			return commandEffect{}, err
		}
	}
	if payload.clearState {
		if err := driver.ClearAppState(ctx, device.AppRequest{AppID: payload.appID}); err != nil {
			return commandEffect{}, err
		}
	}
	if len(payload.permissions) != 0 {
		if err := driver.SetPermissions(ctx, device.PermissionsRequest{
			AppID: payload.appID, Permissions: payload.permissions,
		}); err != nil {
			return commandEffect{}, err
		}
	}
	if payload.stopApp {
		if err := driver.StopApp(ctx, device.AppRequest{AppID: payload.appID}); err != nil {
			return commandEffect{}, err
		}
	}
	if err := driver.LaunchApp(ctx, device.LaunchAppRequest{
		AppID: payload.appID, Arguments: payload.arguments,
	}); err != nil {
		return commandEffect{}, err
	}
	return commandEffect{effectClass: EffectDeviceMutation}, nil
}

func executeAssertVisible(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	if _, ok := evaluated.value.(assertVisibleEvaluated); !ok {
		return commandEffect{}, NewConfigurationError("assertVisible received an invalid evaluated payload", nil)
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return commandEffect{}, err
	}
	options := LookupOptions{Optional: commandIsOptional(evaluated.command)}
	deadline := lookup.adjustedDeadline(options)
	element, err := lookup.findUntil(ctx, *evaluated.command.Selector, LookupOptions{Optional: true}, deadline)
	if err != nil {
		return commandEffect{}, err
	}
	if element == nil {
		return commandEffect{}, NewAssertionError("assertVisible target not found", nil)
	}
	return commandEffect{effectClass: EffectObserved}, nil
}

func launchAppSettleRequest(evaluated evaluatedDispatch) (device.SettleRequest, error) {
	payload, ok := evaluated.value.(launchAppEvaluated)
	if !ok {
		return device.SettleRequest{}, NewConfigurationError("launchApp received an invalid evaluated settle payload", nil)
	}
	return device.SettleRequest{AppID: payload.appID}, nil
}

func synchronizeSelectorArguments(arguments any, selector *model.ElementSelector) any {
	if selector == nil {
		return cloneDynamic(arguments)
	}
	if _, scalar := arguments.(string); scalar {
		if selector.TextRegex == nil {
			return cloneDynamic(arguments)
		}
		return *selector.TextRegex
	}
	fields, ok := arguments.(map[string]any)
	if !ok {
		return cloneDynamic(arguments)
	}
	synchronized := cloneDynamic(fields).(map[string]any)
	for name, value := range map[string]*string{
		"text":  selector.TextRegex,
		"id":    selector.IDRegex,
		"point": selector.Point,
		"start": selector.Start,
		"end":   selector.End,
		"label": selector.Label,
		"css":   selector.CSS,
	} {
		if _, exists := synchronized[name]; exists && value != nil {
			synchronized[name] = *value
		}
	}
	for name, nested := range map[string]*model.ElementSelector{
		"below":         selector.Below,
		"above":         selector.Above,
		"leftOf":        selector.LeftOf,
		"rightOf":       selector.RightOf,
		"containsChild": selector.ContainsChild,
		"childOf":       selector.ChildOf,
	} {
		if original, exists := synchronized[name]; exists && nested != nil {
			synchronized[name] = synchronizeSelectorArguments(original, nested)
		}
	}
	if originals, ok := synchronized["containsDescendants"].([]any); ok {
		for index := range originals {
			if index < len(selector.ContainsDescendants) {
				originals[index] = synchronizeSelectorArguments(originals[index], &selector.ContainsDescendants[index])
			}
		}
		synchronized["containsDescendants"] = originals
	}
	return synchronized
}

func interpolateSelector(ctx context.Context, evaluation evaluationContext, selector *model.ElementSelector) error {
	if selector == nil {
		return nil
	}
	for _, value := range []**string{
		&selector.TextRegex,
		&selector.IDRegex,
		&selector.Point,
		&selector.Start,
		&selector.End,
		&selector.Label,
		&selector.CSS,
		&selector.Index,
	} {
		if err := interpolateStringPointer(ctx, evaluation, value); err != nil {
			return err
		}
	}
	for _, nested := range []*model.ElementSelector{
		selector.Below,
		selector.Above,
		selector.LeftOf,
		selector.RightOf,
		selector.ContainsChild,
		selector.ChildOf,
	} {
		if err := interpolateSelector(ctx, evaluation, nested); err != nil {
			return err
		}
	}
	for index := range selector.ContainsDescendants {
		if err := interpolateSelector(ctx, evaluation, &selector.ContainsDescendants[index]); err != nil {
			return err
		}
	}
	return nil
}

func interpolateStringPointer(ctx context.Context, evaluation evaluationContext, target **string) error {
	if target == nil || *target == nil {
		return nil
	}
	evaluated, err := evaluation.Interpolate(ctx, **target, nil)
	if err != nil {
		return err
	}
	*target = &evaluated
	return nil
}
