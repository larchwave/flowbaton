package engine

import (
	"context"
	"errors"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestEvaluateConditionUsesOrderedShortCircuitAND(t *testing.T) {
	t.Parallel()

	android := model.PlatformAndroid
	visibleText := "visible"
	notVisibleText := "gone"
	script := "probe"
	source := &model.Condition{
		Platform:        &android,
		ScriptCondition: &script,
		Visible:         &model.ElementSelector{TextRegex: &visibleText},
		NotVisible:      &model.ElementSelector{TextRegex: &notVisibleText},
	}

	tests := []struct {
		name           string
		platform       device.Platform
		scriptResult   js.Result
		descriptors    []enginetest.Result[device.TreeNode]
		wantMatch      bool
		wantTrace      []string
		wantDescriptor int
	}{
		{
			name: "platform mismatch stops before JavaScript and selectors", platform: device.Platform("ios"),
			wantTrace: nil,
		},
		{
			name: "false script stops before selectors", platform: device.Platform("android"),
			scriptResult: js.Result{Value: false}, wantTrace: []string{"script"},
		},
		{
			name: "missing visible stops before notVisible", platform: device.Platform("android"),
			scriptResult: js.Result{Value: true},
			descriptors:  []enginetest.Result[device.TreeNode]{{Value: conditionRoot()}},
			wantTrace:    []string{"script", "visible"}, wantDescriptor: 1,
		},
		{
			name: "notVisible runs only after visible matches", platform: device.Platform("android"),
			scriptResult: js.Result{Value: true},
			descriptors: []enginetest.Result[device.TreeNode]{
				{Value: conditionRoot("visible")},
				{Value: conditionRoot("visible")},
			},
			wantMatch: true, wantTrace: []string{"script", "visible", "notVisible"}, wantDescriptor: 2,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newConditionClock(time.Unix(17, 0))
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: test.platform, WidthGrid: 100, HeightGrid: 100}}},
				ContentDescriptor: test.descriptors,
			})
			lookup := NewElementLookup(driver, clock)
			lookup.RecordInteraction(time.Unix(0, 0))
			trace := []string(nil)
			evaluation := evaluationContext{
				evaluateFn: func(context.Context, js.EvalRequest) (js.Result, error) {
					if got := conditionDriverCalls(driver, enginetest.MethodDeviceInfo); got != 1 {
						t.Fatalf("DeviceInfo calls before script = %d, want 1", got)
					}
					if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != 0 {
						t.Fatalf("ContentDescriptor calls before script = %d, want 0", got)
					}
					trace = append(trace, "script")
					return test.scriptResult, nil
				},
				interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
					if input == "probe" {
						return input, nil
					}
					if len(trace) == 0 || trace[0] != "script" {
						t.Fatalf("selector interpolation ran before script: %#v", trace)
					}
					name := input
					if input == "gone" {
						name = "notVisible"
					}
					trace = append(trace, name)
					return input, nil
				},
			}

			evaluated, matched, err := evaluateCondition(context.Background(), evaluation, lookup, source)
			if err != nil {
				t.Fatalf("evaluateCondition() error = %v", err)
			}
			if matched != test.wantMatch {
				t.Fatalf("evaluateCondition() matched = %v, want %v", matched, test.wantMatch)
			}
			if evaluated == source || !reflect.DeepEqual(evaluated, source) {
				t.Fatalf("evaluated condition is not an owned semantic snapshot: got=%#v source=%#v", evaluated, source)
			}
			if !reflect.DeepEqual(trace, test.wantTrace) {
				t.Fatalf("evaluation trace = %#v, want %#v", trace, test.wantTrace)
			}
			if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != test.wantDescriptor {
				t.Fatalf("ContentDescriptor calls = %d, want %d", got, test.wantDescriptor)
			}
			if got := conditionDriverCalls(driver, enginetest.MethodDeviceInfo); got != 1 {
				t.Fatalf("DeviceInfo calls = %d, want 1", got)
			}
		})
	}
}

func TestEvaluateConditionScriptTruthinessAndReevaluation(t *testing.T) {
	t.Parallel()

	factory, err := js.NewFactory(js.Config{Random: rand.New(rand.NewSource(41))})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	evaluation := evaluationContext{evaluateFn: runtime.Evaluate, interpolateFn: runtime.Interpolate}

	for _, test := range []struct {
		name   string
		script string
		want   bool
	}{
		{name: "blank source", script: "", want: false},
		{name: "false", script: "false", want: false},
		{name: "undefined", script: "undefined", want: false},
		{name: "null", script: "null", want: false},
		{name: "integer zero", script: "0", want: false},
		{name: "floating zero", script: "0.0", want: false},
		{name: "NaN", script: "NaN", want: false},
		{name: "BigInt zero", script: "0n", want: false},
		{name: "BigInt one", script: "1n", want: true},
		{name: "exact BigInt zero", script: "${0n}", want: false},
		{name: "exact BigInt one", script: "${1n}", want: true},
		{name: "blank string", script: "''", want: false},
		{name: "false string", script: "'false'", want: true},
		{name: "zero string", script: "'0'", want: true},
		{name: "null string", script: "'null'", want: true},
		{name: "undefined string", script: "'undefined'", want: true},
		{name: "NaN string", script: "'NaN'", want: true},
		{name: "nonzero value", script: "42", want: true},
		{name: "non-sentinel string", script: "'go'", want: true},
		{name: "object", script: "({ready: true})", want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			condition := &model.Condition{ScriptCondition: &test.script}
			_, matched, evalErr := evaluateCondition(context.Background(), evaluation, nil, condition)
			if evalErr != nil {
				t.Fatalf("evaluateCondition() error = %v", evalErr)
			}
			if matched != test.want {
				t.Fatalf("evaluateCondition() matched = %v, want %v", matched, test.want)
			}
		})
	}

	if conditionScriptTruthy(math.NaN()) {
		t.Fatal("conditionScriptTruthy(NaN) = true, want JavaScript-false")
	}
	if conditionScriptTruthy(big.NewInt(0)) || !conditionScriptTruthy(big.NewInt(1)) {
		t.Fatal("conditionScriptTruthy(*big.Int) does not follow JavaScript BigInt zero/nonzero truthiness")
	}

	if err := runtime.PutEnv("READY", false); err != nil {
		t.Fatalf("PutEnv(false) error = %v", err)
	}
	dynamic := "READY"
	condition := &model.Condition{ScriptCondition: &dynamic}
	_, first, err := evaluateCondition(context.Background(), evaluation, nil, condition)
	if err != nil {
		t.Fatalf("first evaluateCondition() error = %v", err)
	}
	if err := runtime.PutEnv("READY", true); err != nil {
		t.Fatalf("PutEnv(true) error = %v", err)
	}
	_, second, err := evaluateCondition(context.Background(), evaluation, nil, condition)
	if err != nil {
		t.Fatalf("second evaluateCondition() error = %v", err)
	}
	if first || !second {
		t.Fatalf("re-evaluated matches = (%v, %v), want (false, true)", first, second)
	}

	wrapped := "${READY}"
	wrappedCondition := &model.Condition{ScriptCondition: &wrapped}
	if err := runtime.PutEnv("READY", false); err != nil {
		t.Fatalf("PutEnv(wrapped false) error = %v", err)
	}
	firstSnapshot, first, err := evaluateCondition(context.Background(), evaluation, nil, wrappedCondition)
	if err != nil {
		t.Fatalf("first wrapped evaluateCondition() error = %v", err)
	}
	if err := runtime.PutEnv("READY", true); err != nil {
		t.Fatalf("PutEnv(wrapped true) error = %v", err)
	}
	secondSnapshot, second, err := evaluateCondition(context.Background(), evaluation, nil, wrappedCondition)
	if err != nil {
		t.Fatalf("second wrapped evaluateCondition() error = %v", err)
	}
	if first || !second {
		t.Fatalf("wrapped re-evaluated matches = (%v, %v), want (false, true)", first, second)
	}
	if *firstSnapshot.ScriptCondition != "false" || *secondSnapshot.ScriptCondition != "true" || *wrappedCondition.ScriptCondition != "${READY}" {
		t.Fatalf("wrapped snapshots/source = (%q, %q, %q), want (false, true, ${READY})", *firstSnapshot.ScriptCondition, *secondSnapshot.ScriptCondition, *wrappedCondition.ScriptCondition)
	}

	if err := runtime.PutEnv("READY", "go"); err != nil {
		t.Fatalf("PutEnv(wrapped string) error = %v", err)
	}
	stringSnapshot, stringMatch, err := evaluateCondition(context.Background(), evaluation, nil, wrappedCondition)
	if err != nil {
		t.Fatalf("wrapped string evaluateCondition() error = %v", err)
	}
	if !stringMatch || *stringSnapshot.ScriptCondition != "go" {
		t.Fatalf("wrapped string evaluation = (%q, %v), want (go, true)", *stringSnapshot.ScriptCondition, stringMatch)
	}

	if err := runtime.PutEnv("READY", map[string]any{"ready": true}); err != nil {
		t.Fatalf("PutEnv(wrapped object) error = %v", err)
	}
	objectSnapshot, objectMatch, err := evaluateCondition(context.Background(), evaluation, nil, wrappedCondition)
	if err != nil {
		t.Fatalf("wrapped object evaluateCondition() error = %v", err)
	}
	if !objectMatch || *objectSnapshot.ScriptCondition == "" {
		t.Fatalf("wrapped object evaluation = (%q, %v), want nonblank true", *objectSnapshot.ScriptCondition, objectMatch)
	}

	if err := runtime.PutEnv("READY", math.NaN()); err != nil {
		t.Fatalf("PutEnv(wrapped NaN) error = %v", err)
	}
	nanSnapshot, nanMatch, err := evaluateCondition(context.Background(), evaluation, nil, wrappedCondition)
	if err != nil {
		t.Fatalf("wrapped NaN evaluateCondition() error = %v", err)
	}
	if nanMatch || *nanSnapshot.ScriptCondition != "NaN" {
		t.Fatalf("wrapped NaN evaluation = (%q, %v), want (NaN, false)", *nanSnapshot.ScriptCondition, nanMatch)
	}
}

func TestEvaluateConditionExactInterpolationUsesTypedValueOnce(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		result js.Result
		want   bool
	}{
		{name: "boolean false", result: js.Result{Value: false, Text: "false"}},
		{name: "integer zero", result: js.Result{Value: int64(0), Text: "0"}},
		{name: "null", result: js.Result{Value: nil, Text: "null"}},
		{name: "undefined", result: js.Result{Value: nil, Text: "undefined"}},
		{name: "NaN", result: js.Result{Value: math.NaN(), Text: "NaN"}},
		{name: "empty string", result: js.Result{Value: "", Text: ""}},
		{name: "false string", result: js.Result{Value: "false", Text: "false"}, want: true},
		{name: "zero string", result: js.Result{Value: "0", Text: "0"}, want: true},
		{name: "null string", result: js.Result{Value: "null", Text: "null"}, want: true},
		{name: "undefined string", result: js.Result{Value: "undefined", Text: "undefined"}, want: true},
		{name: "NaN string", result: js.Result{Value: "NaN", Text: "NaN"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := "${VALUE}"
			evaluateCalls := 0
			evaluation := evaluationContext{
				evaluateFn: func(_ context.Context, request js.EvalRequest) (js.Result, error) {
					evaluateCalls++
					if request.Script != "VALUE" {
						t.Fatalf("Evaluate() script = %q, want exact expression VALUE", request.Script)
					}
					return test.result, nil
				},
				interpolateFn: func(context.Context, string, map[string]any) (string, error) {
					return "", errors.New("exact interpolation must not call Interpolate")
				},
			}

			evaluated, matched, err := evaluateCondition(context.Background(), evaluation, nil, &model.Condition{ScriptCondition: &script})
			if err != nil {
				t.Fatalf("evaluateCondition() error = %v", err)
			}
			if evaluateCalls != 1 {
				t.Fatalf("Evaluate() calls = %d, want 1", evaluateCalls)
			}
			if matched != test.want {
				t.Fatalf("evaluateCondition() matched = %v, want %v", matched, test.want)
			}
			if evaluated.ScriptCondition == nil || *evaluated.ScriptCondition != test.result.Text {
				t.Fatalf("evaluated script snapshot = %#v, want text %q", evaluated.ScriptCondition, test.result.Text)
			}
			if script != "${VALUE}" {
				t.Fatalf("source script mutated to %q", script)
			}
		})
	}
}

func TestEvaluateConditionRecursivelyInterpolatesOwnedSelectors(t *testing.T) {
	t.Parallel()

	text := "${TARGET}"
	child := "${CHILD}"
	label := "${LABEL}"
	indexExpr := "${INDEX}"
	source := &model.Condition{Visible: &model.ElementSelector{
		TextRegex: &text,
		Label:     &label,
		Index:     &indexExpr,
		ContainsDescendants: []model.ElementSelector{{
			TextRegex: &child,
		}},
	}}
	clock := newConditionClock(time.Unix(17, 0))
	lookup := NewElementLookup(conditionDriver(device.Platform("android"), conditionRoot()), clock)
	lookup.RecordInteraction(time.Unix(0, 0))
	evaluation := evaluationContext{interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
		switch input {
		case "${TARGET}":
			return "parent", nil
		case "${CHILD}":
			return "child", nil
		case "${LABEL}":
			return "evaluated label", nil
		case "${INDEX}":
			return "2", nil
		default:
			return input, nil
		}
	}}

	evaluated, _, err := evaluateCondition(context.Background(), evaluation, lookup, source)
	if err != nil {
		t.Fatalf("evaluateCondition() error = %v", err)
	}
	if got := *evaluated.Visible.TextRegex; got != "parent" {
		t.Fatalf("evaluated visible text = %q, want parent", got)
	}
	if got := *evaluated.Visible.ContainsDescendants[0].TextRegex; got != "child" {
		t.Fatalf("evaluated descendant text = %q, want child", got)
	}
	if got := *evaluated.Visible.Label; got != "evaluated label" {
		t.Fatalf("evaluated selector label = %q, want evaluated label", got)
	}
	if got := *evaluated.Visible.Index; got != "2" {
		t.Fatalf("evaluated selector index = %q, want 2", got)
	}
	if *source.Visible.TextRegex != "${TARGET}" || *source.Visible.ContainsDescendants[0].TextRegex != "${CHILD}" || *source.Visible.Label != "${LABEL}" || *source.Visible.Index != "${INDEX}" {
		t.Fatalf("source selector mutated: %#v", source.Visible)
	}
	*evaluated.Visible.TextRegex = "mutated"
	if *source.Visible.TextRegex != "${TARGET}" {
		t.Fatalf("returned selector aliases source: %q", *source.Visible.TextRegex)
	}
}

func TestEvaluateConditionTreatsConditionLabelAsMetadataOnly(t *testing.T) {
	t.Parallel()

	label := "${MUST_NOT_EVALUATE}"
	source := &model.Condition{Label: &label}
	evaluation := evaluationContext{
		evaluateFn: func(context.Context, js.EvalRequest) (js.Result, error) {
			t.Fatal("condition label triggered JavaScript evaluation")
			return js.Result{}, nil
		},
		interpolateFn: func(context.Context, string, map[string]any) (string, error) {
			t.Fatal("condition label triggered interpolation")
			return "", nil
		},
	}

	evaluated, matched, err := evaluateCondition(context.Background(), evaluation, nil, source)
	if err != nil {
		t.Fatalf("evaluateCondition() error = %v", err)
	}
	if !matched || evaluated.Label == nil || *evaluated.Label != label {
		t.Fatalf("metadata-only label evaluation = (%#v, %v), want unchanged match", evaluated, matched)
	}
}

func TestExecutionStateEvaluateConditionRefreshesServicesAndSkipsUnusedOnes(t *testing.T) {
	t.Parallel()

	falseRuntime := conditionRuntime(t, false)
	trueRuntime := conditionRuntime(t, true)
	thirdRuntime := conditionRuntime(t, true)
	clock := newConditionClock(time.Unix(17, 0))
	firstDriver := conditionDriver(device.Platform("android"), conditionRoot())
	secondDriver := conditionDriver(device.Platform("android"), conditionRoot("ready"))
	firstLookup := NewElementLookup(firstDriver, clock)
	firstLookup.RecordInteraction(time.Unix(0, 0))
	secondLookup := NewElementLookup(secondDriver, clock)
	secondLookup.RecordInteraction(time.Unix(0, 0))
	runtimes := []js.Runtime{falseRuntime, trueRuntime, thirdRuntime}
	lookups := []*ElementLookup{secondLookup, firstLookup}
	runtimeCalls := 0
	lookupCalls := 0
	state := &executionState{
		runtimeFn: func() (js.Runtime, error) {
			runtime := runtimes[runtimeCalls]
			runtimeCalls++
			return runtime, nil
		},
		lookupFn: func() (*ElementLookup, error) {
			lookup := lookups[lookupCalls]
			lookupCalls++
			return lookup, nil
		},
	}
	script := "READY"
	text := "ready"
	source := &model.Condition{
		ScriptCondition: &script,
		Visible:         &model.ElementSelector{TextRegex: &text},
	}

	firstSnapshot, firstMatch, err := state.evaluateCondition(context.Background(), source)
	if err != nil {
		t.Fatalf("first state.evaluateCondition() error = %v", err)
	}
	if lookupCalls != 0 {
		t.Fatalf("false-script evaluation captured lookup %d times, want 0", lookupCalls)
	}
	secondSnapshot, secondMatch, err := state.evaluateCondition(context.Background(), source)
	if err != nil {
		t.Fatalf("second state.evaluateCondition() error = %v", err)
	}
	thirdSnapshot, thirdMatch, err := state.evaluateCondition(context.Background(), source)
	if err != nil {
		t.Fatalf("third state.evaluateCondition() error = %v", err)
	}
	if firstMatch || !secondMatch || thirdMatch {
		t.Fatalf("state matches = (%v, %v, %v), want (false, true, false)", firstMatch, secondMatch, thirdMatch)
	}
	if firstSnapshot == source || secondSnapshot == source || thirdSnapshot == source ||
		firstSnapshot == secondSnapshot || secondSnapshot == thirdSnapshot {
		t.Fatal("state.evaluateCondition() did not return independent snapshots")
	}
	if runtimeCalls != 3 || lookupCalls != 2 {
		t.Fatalf("service captures = runtime:%d lookup:%d, want 3/2", runtimeCalls, lookupCalls)
	}
	if got := conditionDriverCalls(firstDriver, enginetest.MethodContentDescriptor); got != 1 {
		t.Fatalf("third lookup descriptor calls = %d, want 1", got)
	}
	if got := conditionDriverCalls(secondDriver, enginetest.MethodContentDescriptor); got != 1 {
		t.Fatalf("true-script second lookup made %d descriptor calls, want 1", got)
	}

	label := "metadata"
	optional := true
	for _, condition := range []*model.Condition{nil, {Label: &label}, {Optional: &optional}} {
		_, matched, evalErr := state.evaluateCondition(context.Background(), condition)
		if evalErr != nil || !matched {
			t.Fatalf("unused-service condition %#v = (%v, %v), want true nil", condition, matched, evalErr)
		}
	}
	if runtimeCalls != 3 || lookupCalls != 2 {
		t.Fatalf("unused conditions captured services: runtime:%d lookup:%d", runtimeCalls, lookupCalls)
	}

	var nilState *executionState
	if _, _, err := nilState.evaluateCondition(context.Background(), source); err == nil {
		t.Fatal("nil state evaluateCondition() error = nil")
	}
	if _, _, err := state.evaluateCondition(nil, nil); err == nil {
		t.Fatal("nil context state.evaluateCondition() error = nil")
	}
}

func TestExecutionStateEvaluateConditionAcquiresServicesInPredicateOrder(t *testing.T) {
	t.Parallel()

	runtimeFailure := errors.New("runtime must remain unreachable")
	runtimeCalls := 0
	lookupCalls := 0
	platformLookup := NewElementLookup(
		conditionDriver(device.Platform("ios")),
		newConditionClock(time.Unix(0, 0)),
	)
	platformState := &executionState{
		runtimeFn: func() (js.Runtime, error) {
			runtimeCalls++
			return nil, runtimeFailure
		},
		lookupFn: func() (*ElementLookup, error) {
			lookupCalls++
			return platformLookup, nil
		},
	}
	android := model.PlatformAndroid
	script := "true"
	_, matched, err := platformState.evaluateCondition(context.Background(), &model.Condition{
		Platform: &android, ScriptCondition: &script,
	})
	if err != nil || matched {
		t.Fatalf("platform mismatch = (%v, %v), want false nil", matched, err)
	}
	if runtimeCalls != 0 || lookupCalls != 1 {
		t.Fatalf("platform mismatch service calls = runtime:%d lookup:%d, want 0/1", runtimeCalls, lookupCalls)
	}

	falseRuntime := conditionRuntime(t, false)
	lookupFailure := errors.New("lookup must remain unreachable")
	runtimeCalls = 0
	lookupCalls = 0
	scriptState := &executionState{
		runtimeFn: func() (js.Runtime, error) {
			runtimeCalls++
			return falseRuntime, nil
		},
		lookupFn: func() (*ElementLookup, error) {
			lookupCalls++
			return nil, lookupFailure
		},
	}
	visible := "ready"
	condition := &model.Condition{
		ScriptCondition: pointer("READY"),
		Visible:         &model.ElementSelector{TextRegex: &visible},
	}
	_, matched, err = scriptState.evaluateCondition(context.Background(), condition)
	if err != nil || matched {
		t.Fatalf("false script = (%v, %v), want false nil", matched, err)
	}
	if runtimeCalls != 1 || lookupCalls != 0 {
		t.Fatalf("false script service calls = runtime:%d lookup:%d, want 1/0", runtimeCalls, lookupCalls)
	}
}

func TestEvaluateConditionVisibleUsesAdjustedRequiredAndOptionalWindows(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		optional bool
		wantWait time.Duration
	}{
		{name: "required", wantWait: 15 * time.Second},
		{name: "optional", optional: true, wantWait: 5 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newConditionClock(time.Unix(2, 0))
			driver := conditionDriver(device.Platform("android"))
			lookup := NewElementLookup(driver, clock)
			lookup.RecordInteraction(time.Unix(0, 0))
			text := "absent"
			condition := &model.Condition{Visible: &model.ElementSelector{TextRegex: &text}, Optional: &test.optional}

			_, matched, err := evaluateCondition(context.Background(), identityConditionEvaluation(), lookup, condition)
			if err != nil {
				t.Fatalf("evaluateCondition() error = %v", err)
			}
			if matched {
				t.Fatal("evaluateCondition() matched = true, want false for selector absence")
			}
			if got := clock.TotalWait(); got != test.wantWait {
				t.Fatalf("condition lookup wait = %v, want %v", got, test.wantWait)
			}
		})
	}
}

func TestEvaluateConditionNotVisibleCadenceAndExhaustion(t *testing.T) {
	t.Parallel()

	text := "loading"
	for _, test := range []struct {
		name        string
		descriptors []device.TreeNode
		optional    bool
		want        bool
	}{
		{name: "becomes absent", descriptors: []device.TreeNode{conditionRoot("loading"), conditionRoot("loading"), conditionRoot()}, want: true},
		{name: "optional still visible at exhaustion", descriptors: []device.TreeNode{conditionRoot("loading"), conditionRoot("loading"), conditionRoot("loading")}, optional: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := int64(16)
			if test.optional {
				start = 6
			}
			clock := newConditionClock(time.Unix(start, 0))
			driver := conditionDriver(device.Platform("android"), test.descriptors...)
			lookup := NewElementLookup(driver, clock)
			lookup.RecordInteraction(time.Unix(0, 0))
			condition := &model.Condition{NotVisible: &model.ElementSelector{TextRegex: &text}, Optional: &test.optional}

			_, matched, err := evaluateCondition(context.Background(), identityConditionEvaluation(), lookup, condition)
			if err != nil {
				t.Fatalf("evaluateCondition() error = %v", err)
			}
			if matched != test.want {
				t.Fatalf("evaluateCondition() matched = %v, want %v", matched, test.want)
			}
			if got, want := clock.Waits(), []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}; !reflect.DeepEqual(got, want) {
				t.Fatalf("notVisible waits = %#v, want %#v", got, want)
			}
		})
	}
}

func TestEvaluateConditionPropagatesCancellationAndBoundaryErrors(t *testing.T) {
	t.Parallel()

	text := "loading"
	condition := &model.Condition{Visible: &model.ElementSelector{TextRegex: &text}}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	driver := conditionDriver(device.Platform("android"), conditionRoot("loading"))
	if _, _, err := evaluateCondition(cancelled, identityConditionEvaluation(), NewElementLookup(driver, newConditionClock(time.Unix(0, 0))), condition); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled evaluateCondition() error = %v, want context.Canceled", err)
	}
	if got := len(driver.Actions()); got != 0 {
		t.Fatalf("pre-cancelled driver actions = %d, want 0", got)
	}

	ctx, cancelDuring := context.WithCancel(context.Background())
	clock := &conditionClock{now: time.Unix(16, 0), cancel: cancelDuring}
	driver = conditionDriver(device.Platform("android"), conditionRoot("loading"), conditionRoot())
	lookup := NewElementLookup(driver, clock)
	lookup.RecordInteraction(time.Unix(0, 0))
	notVisible := &model.Condition{NotVisible: &model.ElementSelector{TextRegex: &text}}
	if _, _, err := evaluateCondition(ctx, identityConditionEvaluation(), lookup, notVisible); !errors.Is(err, context.Canceled) {
		t.Fatalf("during-poll evaluateCondition() error = %v, want context.Canceled", err)
	}
	if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != 1 {
		t.Fatalf("ContentDescriptor calls after cancellation = %d, want 1", got)
	}

	deviceFailure := NewDeviceConnectionError("lost device", nil)
	driver = enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: deviceFailure}}})
	platform := model.PlatformAndroid
	if _, _, err := evaluateCondition(context.Background(), identityConditionEvaluation(), NewElementLookup(driver, newConditionClock(time.Unix(0, 0))), &model.Condition{Platform: &platform}); err != deviceFailure {
		t.Fatalf("device error = %T %v, want exact %p", err, err, deviceFailure)
	}

	infrastructureFailure := errors.New("JavaScript unavailable")
	script := "true"
	evaluation := evaluationContext{
		evaluateFn: func(context.Context, js.EvalRequest) (js.Result, error) {
			return js.Result{}, infrastructureFailure
		},
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			return input, nil
		},
	}
	if _, _, err := evaluateCondition(context.Background(), evaluation, nil, &model.Condition{ScriptCondition: &script}); err != infrastructureFailure {
		t.Fatalf("JavaScript error = %T %v, want exact sentinel", err, err)
	}

	descriptorFailure := errors.New("hierarchy unavailable")
	driver = enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 100, HeightGrid: 100}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: descriptorFailure}},
	})
	if _, _, err := evaluateCondition(context.Background(), identityConditionEvaluation(), NewElementLookup(driver, newConditionClock(time.Unix(0, 0))), condition); err != descriptorFailure {
		t.Fatalf("ContentDescriptor error = %T %v, want exact sentinel", err, err)
	}

	invalid := "${BROKEN}"
	invalidSource := &model.Condition{Visible: &model.ElementSelector{TextRegex: &invalid}}
	evaluation = evaluationContext{interpolateFn: func(context.Context, string, map[string]any) (string, error) {
		return "[", nil
	}}
	_, _, err := evaluateCondition(context.Background(), evaluation, NewElementLookup(conditionDriver(device.Platform("android")), newConditionClock(time.Unix(0, 0))), invalidSource)
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("invalid evaluated selector error = %T %v, want *ConfigurationError", err, err)
	}
	if *invalidSource.Visible.TextRegex != "${BROKEN}" {
		t.Fatalf("invalid interpolation mutated source = %q", *invalidSource.Visible.TextRegex)
	}
}

type conditionClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	cancel context.CancelFunc
}

func newConditionClock(now time.Time) *conditionClock { return &conditionClock{now: now} }

func (clock *conditionClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *conditionClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.waits = append(clock.waits, delay)
	clock.now = clock.now.Add(delay)
	cancel := clock.cancel
	clock.cancel = nil
	clock.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return ctx.Err()
}

func (clock *conditionClock) Waits() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]time.Duration(nil), clock.waits...)
}

func (clock *conditionClock) TotalWait() time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	var total time.Duration
	for _, wait := range clock.waits {
		total += wait
	}
	return total
}

func identityConditionEvaluation() evaluationContext {
	return evaluationContext{interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
		return input, nil
	}}
}

func conditionRuntime(t *testing.T, ready bool) js.Runtime {
	t.Helper()
	factory, err := js.NewFactory(js.Config{Random: rand.New(rand.NewSource(67))})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error = %v", err)
	}
	if err := runtime.PutEnv("READY", ready); err != nil {
		_ = runtime.Close()
		t.Fatalf("runtime.PutEnv() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func conditionDriver(platform device.Platform, roots ...device.TreeNode) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	descriptors := make([]enginetest.Result[device.TreeNode], len(roots))
	for index := range roots {
		descriptors[index] = enginetest.Result[device.TreeNode]{Value: roots[index]}
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: platform, WidthGrid: 100, HeightGrid: 100}}},
		ContentDescriptor: descriptors,
	})
	return driver
}

func conditionRoot(text ...string) device.TreeNode {
	root := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][100,100]"}}
	for _, value := range text {
		root.Children = append(root.Children, device.TreeNode{Attributes: map[string]string{
			"text": value, "bounds": "[10,10][30,30]",
		}})
	}
	return root
}

func conditionDriverCalls(driver *enginetest.FakeDriver, method enginetest.Method) int {
	count := 0
	for _, action := range driver.Actions() {
		if action.Method == method {
			count++
		}
	}
	return count
}
