package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestBatch5AssertionWaitHandlersAreAtomicallyRegistered(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	for _, keyword := range []model.CommandKeyword{
		model.CommandAssertNotVisible,
		model.CommandAssertTrue,
		model.CommandExtendedWaitUntil,
	} {
		if _, exists := registry.lookup(keyword); !exists {
			t.Fatalf("production registry missing Batch 5 keyword %q", keyword)
		}
	}
	if len(registry.byKeyword) != len(productionKeywords()) {
		t.Fatalf("production registry size = %d, want the complete set", len(registry.byKeyword))
	}

	direct, err := newHandlerRegistry(
		assertNotVisibleHandlerSpec(),
		assertTrueHandlerSpec(),
		extendedWaitUntilHandlerSpec(),
	)
	if err != nil || len(direct.byKeyword) != 3 {
		t.Fatalf("direct assertion/wait registry = %#v, %v; want three handlers", direct.byKeyword, err)
	}

	root := fixturePath(t, "assertion-wait", "batch1.yaml")
	program, err := Prepare(
		context.Background(),
		model.ExecutionPlan{SelectedRoots: []string{root}},
		capability.FileLoader{},
	)
	if err != nil {
		t.Fatalf("Prepare(Batch 1 fixture) error = %v", err)
	}
	prepared, exists := program.Flow(program.Roots()[0])
	if !exists || len(prepared.Commands) != 3 {
		t.Fatalf("prepared Batch 1 fixture = %#v, want three commands", prepared)
	}
	for _, command := range prepared.Commands {
		if _, exists := registry.lookup(command.Kind); !exists {
			t.Fatalf("prepared public command %s is not registered", command.Kind)
		}
	}
}

func TestAssertNotVisibleHandlerInterpolatesLateAndPollsEvery500Milliseconds(t *testing.T) {
	t.Parallel()

	raw := "${TARGET}"
	source := model.Command{
		Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
		Arguments: raw, Selector: &model.ElementSelector{TextRegex: &raw},
	}
	snapshot := cloneCommand(source)
	clock := newConditionClock(time.Unix(0, 0))
	driver := conditionDriver(
		device.Platform("ios"),
		conditionRoot("Loading"),
		conditionRoot("Loading"),
		conditionRoot(),
	)
	lookup := NewElementLookup(driver, clock)
	dispatcher, compiled := compileBatch1Command(t, assertNotVisibleHandlerSpec(), source)
	evaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			if input == raw {
				return "Loading", nil
			}
			return input, nil
		},
	}, compiled)
	if err != nil {
		t.Fatalf("evaluate(assertNotVisible) error = %v", err)
	}
	effect, err := dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) { return lookup, nil },
	}, compiled, evaluated)
	if err != nil || effect.effectClass != EffectObserved {
		t.Fatalf("execute(assertNotVisible) = effect %#v, error %v", effect, err)
	}
	if got, want := clock.Waits(), []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assertNotVisible waits = %#v, want %#v", got, want)
	}
	if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != 3 {
		t.Fatalf("assertNotVisible descriptor calls = %d, want 3", got)
	}
	if evaluated.command.Selector == nil || evaluated.command.Selector.TextRegex == nil || *evaluated.command.Selector.TextRegex != "Loading" {
		t.Fatalf("evaluated assertNotVisible selector = %#v, want Loading", evaluated.command.Selector)
	}
	if !reflect.DeepEqual(source, snapshot) {
		t.Fatalf("assertNotVisible source mutated: got %#v want %#v", source, snapshot)
	}
}

func TestExtendedWaitUntilBothPredicatesShareOneDeadline(t *testing.T) {
	t.Parallel()

	visible := "Ready"
	notVisible := "Gone"
	source := model.Command{
		Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"visible":    map[string]any{"text": visible},
			"notVisible": map[string]any{"text": notVisible},
			"timeout":    int64(1200),
		},
		Condition: &model.Condition{
			Visible:    &model.ElementSelector{TextRegex: &visible},
			NotVisible: &model.ElementSelector{TextRegex: &notVisible},
		},
	}
	snapshot := cloneCommand(source)
	roots := make([]device.TreeNode, 0, 13)
	for range 10 {
		roots = append(roots, conditionRoot("Gone"))
	}
	for range 3 {
		roots = append(roots, conditionRoot("Ready", "Gone"))
	}
	clock := newConditionClock(time.Unix(0, 0))
	driver := conditionDriver(device.Platform("ios"), roots...)
	lookup := NewElementLookup(driver, clock)
	dispatcher, compiled := compileBatch1Command(t, extendedWaitUntilHandlerSpec(), source)
	evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
	if err != nil {
		t.Fatalf("evaluate(extendedWaitUntil) error = %v", err)
	}
	_, err = dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) { return lookup, nil },
	}, compiled, evaluated)
	var assertion *AssertionError
	if !errors.As(err, &assertion) {
		t.Fatalf("execute(extendedWaitUntil) error = %T %v, want *AssertionError", err, err)
	}
	waits := clock.Waits()
	if len(waits) != 11 {
		t.Fatalf("extended wait count = %d (%#v), want ten 100ms plus one 200ms", len(waits), waits)
	}
	for index := range 10 {
		if waits[index] != 100*time.Millisecond {
			t.Fatalf("extended visible wait[%d] = %v, want 100ms", index, waits[index])
		}
	}
	if waits[10] != 200*time.Millisecond || clock.TotalWait() != 1200*time.Millisecond {
		t.Fatalf("extended shared deadline waits = %#v total %v, want final 200ms total 1200ms", waits, clock.TotalWait())
	}
	if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != 13 {
		t.Fatalf("extended descriptor calls = %d, want 13", got)
	}
	if !reflect.DeepEqual(source, snapshot) {
		t.Fatalf("extendedWaitUntil source mutated: got %#v want %#v", source, snapshot)
	}
}

func TestExtendedWaitUntilDoesNotEvaluateUnreachedNotVisible(t *testing.T) {
	t.Parallel()

	visible := "${VISIBLE}"
	notVisible := "${BROKEN}"
	timeout := int64(0)
	source := model.Command{
		Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"visible":    map[string]any{"text": visible},
			"notVisible": map[string]any{"text": notVisible},
			"timeout":    timeout,
		},
		Condition: &model.Condition{
			Visible:    &model.ElementSelector{TextRegex: &visible},
			NotVisible: &model.ElementSelector{TextRegex: &notVisible},
		},
	}
	snapshot := cloneCommand(source)
	dispatcher, compiled := compileBatch1Command(t, extendedWaitUntilHandlerSpec(), source)
	trace := []string(nil)
	evaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			trace = append(trace, input)
			switch input {
			case visible:
				return "Missing", nil
			case notVisible:
				return "", errors.New("unreached notVisible interpolation")
			default:
				return input, nil
			}
		},
	}, compiled)
	if err != nil {
		t.Fatalf("evaluate(extendedWaitUntil) error = %v; notVisible must remain unreached", err)
	}

	clock := newConditionClock(time.Unix(0, 0))
	driver := conditionDriver(device.Platform("ios"), conditionRoot())
	effect, err := dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) { return NewElementLookup(driver, clock), nil },
	}, compiled, evaluated)
	var assertion *AssertionError
	if !errors.As(err, &assertion) {
		t.Fatalf("execute(extendedWaitUntil) error = %T %v, want visible *AssertionError", err, err)
	}
	if want := []string{visible}; !reflect.DeepEqual(trace, want) {
		t.Fatalf("interpolation trace = %#v, want only reached visible %#v", trace, want)
	}
	if effect.evaluatedCommand == nil || effect.evaluatedCommand.Condition == nil ||
		effect.evaluatedCommand.Condition.Visible == nil || effect.evaluatedCommand.Condition.Visible.TextRegex == nil ||
		*effect.evaluatedCommand.Condition.Visible.TextRegex != "Missing" ||
		effect.evaluatedCommand.Condition.NotVisible == nil || effect.evaluatedCommand.Condition.NotVisible.TextRegex == nil ||
		*effect.evaluatedCommand.Condition.NotVisible.TextRegex != notVisible {
		t.Fatalf("evaluated short-circuit snapshot = %#v, want reached visible and raw notVisible", effect.evaluatedCommand)
	}
	if !reflect.DeepEqual(source, snapshot) {
		t.Fatalf("extendedWaitUntil source mutated: got %#v want %#v", source, snapshot)
	}
}

func TestExtendedWaitUntilVisibleFailurePreemptsUnreachedInvalidNotVisibleRegex(t *testing.T) {
	t.Parallel()

	visible := "Missing"
	notVisible := "["
	command := extendedWaitCommand(visible, notVisible, int64Pointer(0))
	dispatcher, compiled := compileBatch1Command(t, extendedWaitUntilHandlerSpec(), command)
	evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
	if err != nil {
		t.Fatalf("evaluate(extendedWaitUntil) error = %v; invalid notVisible regex is unreached", err)
	}
	effect, err := dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) {
			return NewElementLookup(
				conditionDriver(device.Platform("ios"), conditionRoot()),
				newConditionClock(time.Unix(0, 0)),
			), nil
		},
	}, compiled, evaluated)
	var assertion *AssertionError
	if !errors.As(err, &assertion) {
		t.Fatalf("execute(extendedWaitUntil) error = %T %v, want visible *AssertionError", err, err)
	}
	if effect.evaluatedCommand == nil || effect.evaluatedCommand.Condition == nil ||
		effect.evaluatedCommand.Condition.NotVisible == nil ||
		effect.evaluatedCommand.Condition.NotVisible.TextRegex == nil ||
		*effect.evaluatedCommand.Condition.NotVisible.TextRegex != notVisible {
		t.Fatalf("short-circuit snapshot = %#v, want raw invalid notVisible selector retained", effect.evaluatedCommand)
	}
}

func TestAssertionWaitHandlersPreferCancellationAfterContextIgnoringDescriptor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		spec      handlerSpec
		command   model.Command
		root      device.TreeNode
		configure func(*ElementLookup, time.Time)
	}{
		{
			name: "assertNotVisible", spec: assertNotVisibleHandlerSpec(),
			command: selectorCommand(model.CommandAssertNotVisible, "Visible", false),
			root:    conditionRoot("Visible"),
			configure: func(lookup *ElementLookup, now time.Time) {
				lookup.RecordInteraction(now.Add(-LookupTimeout))
			},
		},
		{
			name: "extended visible", spec: extendedWaitUntilHandlerSpec(),
			command: extendedWaitCommand("Missing", "", int64Pointer(0)),
			root:    conditionRoot(),
		},
		{
			name: "extended notVisible", spec: extendedWaitUntilHandlerSpec(),
			command: extendedWaitCommand("", "Visible", int64Pointer(0)),
			root:    conditionRoot("Visible"),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			start := time.Unix(400, 0)
			clock := newConditionClock(start)
			ctx, cancel := context.WithCancel(context.Background())
			driver := newCancelAfterDescriptorDriver(cancel, test.root)
			lookup := NewElementLookup(driver, clock)
			if test.configure != nil {
				test.configure(lookup, start)
			}
			dispatcher, compiled := compileBatch1Command(t, test.spec, test.command)
			evaluated, err := dispatcher.evaluate(ctx, identityConditionEvaluation(), compiled)
			if err != nil {
				t.Fatalf("evaluate(%s) error = %v", test.command.Kind, err)
			}
			_, err = dispatcher.execute(ctx, &executionState{
				lookupFn: func() (*ElementLookup, error) { return lookup, nil },
			}, compiled, evaluated)
			if err != context.Canceled {
				t.Fatalf("execute(%s) error = %T %v, want exact context cancellation precedence", test.command.Kind, err, err)
			}
		})
	}
}

func TestExtendedWaitUntilReusesOneAbsoluteDeadlineWhenNowAdvances(t *testing.T) {
	t.Parallel()

	start := time.Unix(500, 0)
	clock := newSteppingNowClock(start, 10*time.Millisecond)
	driver := conditionDriver(device.Platform("ios"), conditionRoot(), conditionRoot())
	command := extendedWaitCommand("Missing", "", int64Pointer(100))
	dispatcher, compiled := compileBatch1Command(t, extendedWaitUntilHandlerSpec(), command)
	evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
	if err != nil {
		t.Fatalf("evaluate(extendedWaitUntil) error = %v", err)
	}
	_, err = dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) { return NewElementLookup(driver, clock), nil },
	}, compiled, evaluated)
	var assertion *AssertionError
	if !errors.As(err, &assertion) {
		t.Fatalf("execute(extendedWaitUntil) error = %T %v, want *AssertionError", err, err)
	}
	deadline := start.Add(100 * time.Millisecond)
	for index, waitedUntil := range clock.WaitDeadlines() {
		if waitedUntil.After(deadline) {
			t.Fatalf("wait deadline[%d] = %v, exceeds shared absolute deadline %v", index, waitedUntil, deadline)
		}
	}
}

func TestAssertionWaitCompilersRejectRawTypedSelectorDisagreement(t *testing.T) {
	t.Parallel()

	falseValue := false
	trueValue := true
	tests := []struct {
		name    string
		spec    handlerSpec
		command model.Command
	}{
		{
			name: "assertNotVisible selector",
			spec: assertNotVisibleHandlerSpec(),
			command: model.Command{
				Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
				Arguments: "Raw", Selector: &model.ElementSelector{TextRegex: stringPointer("Typed")},
			},
		},
		{
			name: "assertNotVisible optional",
			spec: assertNotVisibleHandlerSpec(),
			command: model.Command{
				Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
				Arguments: map[string]any{"text": "Target", "optional": trueValue},
				Selector:  &model.ElementSelector{TextRegex: stringPointer("Target"), Optional: &falseValue},
				Optional:  &falseValue,
			},
		},
		{
			name: "assertNotVisible command optional",
			spec: assertNotVisibleHandlerSpec(),
			command: model.Command{
				Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
				Arguments: map[string]any{"text": "Target", "optional": trueValue},
				Selector:  &model.ElementSelector{TextRegex: stringPointer("Target"), Optional: &trueValue},
				Optional:  &falseValue,
			},
		},
		{
			name: "extended visible",
			spec: extendedWaitUntilHandlerSpec(),
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{"visible": "Raw"},
				Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: stringPointer("Typed")}},
			},
		},
		{
			name: "extended notVisible nested selector",
			spec: extendedWaitUntilHandlerSpec(),
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"notVisible": map[string]any{"text": "Target", "below": "Raw"},
				},
				Condition: &model.Condition{NotVisible: &model.ElementSelector{
					TextRegex: stringPointer("Target"),
					Below:     &model.ElementSelector{TextRegex: stringPointer("Typed")},
				}},
			},
		},
		{
			name: "assertNotVisible empty typed size",
			spec: assertNotVisibleHandlerSpec(),
			command: model.Command{
				Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
				Arguments: "Target",
				Selector:  &model.ElementSelector{TextRegex: stringPointer("Target"), Size: &model.SizeSelector{}},
			},
		},
		{
			name: "extended nested empty typed size",
			spec: extendedWaitUntilHandlerSpec(),
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"visible": map[string]any{"text": "Target", "below": "Anchor"},
				},
				Condition: &model.Condition{Visible: &model.ElementSelector{
					TextRegex: stringPointer("Target"),
					Below: &model.ElementSelector{
						TextRegex: stringPointer("Anchor"),
						Size:      &model.SizeSelector{},
					},
				}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry, err := newHandlerRegistry(test.spec)
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			_, err = newDispatcher(registry).compile(context.Background(), compileContext{}, test.command)
			var configuration *ConfigurationError
			if !errors.As(err, &configuration) {
				t.Fatalf("compile(%s) error = %T %v, want *ConfigurationError", test.command.Kind, err, err)
			}
		})
	}
}

func TestAssertionWaitSnapshotMismatchCompilesBeforeEffects(t *testing.T) {
	t.Parallel()

	for _, mismatch := range []model.Command{
		{
			Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
			Arguments: "Raw", Selector: &model.ElementSelector{TextRegex: stringPointer("Typed")},
		},
		{
			Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
			Arguments: map[string]any{"visible": "Raw"},
			Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: stringPointer("Typed")}},
		},
		{
			Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
			Arguments: "Target",
			Selector:  &model.ElementSelector{TextRegex: stringPointer("Target"), Size: &model.SizeSelector{}},
		},
		{
			Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
			Arguments: map[string]any{
				"visible": map[string]any{"text": "Target", "below": "Anchor"},
			},
			Condition: &model.Condition{Visible: &model.ElementSelector{
				TextRegex: stringPointer("Target"),
				Below: &model.ElementSelector{
					TextRegex: stringPointer("Anchor"),
					Size:      &model.SizeSelector{},
				},
			}},
		},
	} {
		mismatch := mismatch
		t.Run(string(mismatch.Kind), func(t *testing.T) {
			driver := enginetest.NewFakeDriver()
			launchEffects := 0
			launchSpec := handlerSpec{
				keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation,
				postAction: postActionNoSettle,
				compile:    pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
				evaluate:   identityEvaluator,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					launchEffects++
					return commandEffect{effectClass: EffectDeviceMutation}, nil
				},
			}
			core, err := newExecutorCore(
				coreDependencies(driver, newConditionClock(time.Unix(600, 0))),
				launchSpec,
				assertNotVisibleHandlerSpec(),
				extendedWaitUntilHandlerSpec(),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			results, err := core.executeSequence(context.Background(), []model.Command{
				{Kind: model.CommandLaunchApp},
				mismatch,
			}, 0)
			var configuration *ConfigurationError
			if !errors.As(err, &configuration) {
				t.Fatalf("executeSequence() error = %T %v, want compile-time *ConfigurationError", err, err)
			}
			if len(results) != 0 || launchEffects != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("snapshot mismatch reached effects: results=%#v launches=%d driver=%#v", results, launchEffects, driver.Actions())
			}
		})
	}
}

func TestAssertTrueHandlerUsesJSTruthinessWithoutLookupOrDeviceAction(t *testing.T) {
	t.Parallel()

	evaluation := newBatch1Evaluation(t, nil)
	tests := []struct {
		name    string
		script  string
		matched bool
	}{
		{name: "true", script: "true", matched: true},
		{name: "positive number", script: "1", matched: true},
		{name: "negative number", script: "-1", matched: true},
		{name: "nonblank string", script: "'ready'", matched: true},
		{name: "false string", script: "'false'", matched: true},
		{name: "zero string", script: "'0'", matched: true},
		{name: "null string", script: "'null'", matched: true},
		{name: "undefined string", script: "'undefined'", matched: true},
		{name: "NaN string", script: "'NaN'", matched: true},
		{name: "object", script: "({ready: true})", matched: true},
		{name: "false", script: "false"},
		{name: "zero", script: "0"},
		{name: "blank string", script: "''"},
		{name: "null", script: "null"},
		{name: "undefined", script: "undefined"},
		{name: "NaN", script: "NaN"},
		{name: "BigInt zero", script: "0n"},
		{name: "BigInt one", script: "1n", matched: true},
		{name: "exact BigInt zero", script: "${0n}"},
		{name: "exact BigInt one", script: "${1n}", matched: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := model.Command{
				Kind: model.CommandAssertTrue, Form: model.CommandFormObject, Arguments: test.script,
			}
			snapshot := cloneCommand(source)
			dispatcher, compiled := compileBatch1Command(t, assertTrueHandlerSpec(), source)
			evaluateCalls := 0
			counted := evaluation
			counted.evaluateFn = func(ctx context.Context, request js.EvalRequest) (js.Result, error) {
				evaluateCalls++
				return evaluation.Evaluate(ctx, request)
			}
			evaluated, err := dispatcher.evaluate(context.Background(), counted, compiled)
			if err != nil {
				t.Fatalf("evaluate(assertTrue %q) error = %v", test.script, err)
			}
			lookupCalls := 0
			driver := enginetest.NewFakeDriver()
			effect, err := dispatcher.execute(context.Background(), &executionState{
				dependencies: Dependencies{Driver: driver},
				lookupFn: func() (*ElementLookup, error) {
					lookupCalls++
					return nil, errors.New("lookup must remain unreachable")
				},
			}, compiled, evaluated)
			if test.matched {
				if err != nil || effect.effectClass != EffectObserved {
					t.Fatalf("execute(assertTrue %q) = effect %#v, error %v", test.script, effect, err)
				}
			} else {
				var assertion *AssertionError
				if !errors.As(err, &assertion) {
					t.Fatalf("execute(assertTrue %q) error = %T %v, want *AssertionError", test.script, err, err)
				}
			}
			if evaluateCalls != 1 || lookupCalls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("assertTrue %q effects = evaluate:%d lookup:%d driver:%#v, want 1/0/none", test.script, evaluateCalls, lookupCalls, driver.Actions())
			}
			if !reflect.DeepEqual(source, snapshot) {
				t.Fatalf("assertTrue %q source mutated: got %#v want %#v", test.script, source, snapshot)
			}
		})
	}
}

func TestAssertTrueObjectInterpolatesLateAndOptionalFalseWarns(t *testing.T) {
	t.Parallel()

	condition := "${READY}"
	label := "${LABEL}"
	optional := true
	source := model.Command{
		Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"condition": condition,
			"label":     label,
			"optional":  optional,
		},
		Label: &label, Optional: &optional,
	}
	snapshot := cloneCommand(source)
	dispatcher, compiled := compileBatch1Command(t, assertTrueHandlerSpec(), source)
	evaluation := newBatch1Evaluation(t, map[string]any{
		"READY": false,
		"LABEL": "evaluated label",
	})
	evaluated, err := dispatcher.evaluate(context.Background(), evaluation, compiled)
	if err != nil {
		t.Fatalf("evaluate(assertTrue object) error = %v", err)
	}
	arguments, ok := evaluated.command.Arguments.(map[string]any)
	if !ok || arguments["condition"] != "false" || arguments["label"] != "evaluated label" ||
		evaluated.command.Label == nil || *evaluated.command.Label != "evaluated label" {
		t.Fatalf("evaluated assertTrue object = %#v", evaluated.command)
	}
	_, err = dispatcher.execute(context.Background(), nil, compiled, evaluated)
	var assertion *AssertionError
	if !errors.As(err, &assertion) || ClassifyOutcome(err, true) != Warned {
		t.Fatalf("optional false = %T %v outcome %q, want AssertionError/Warned", err, err, ClassifyOutcome(err, true))
	}
	if !reflect.DeepEqual(source, snapshot) {
		t.Fatalf("assertTrue object source mutated: got %#v want %#v", source, snapshot)
	}

	driver := conditionDriver(device.Platform("ios"))
	rootCondition := `READY === "true"`
	rootSource := cloneCommand(source)
	rootSource.Arguments.(map[string]any)["condition"] = rootCondition
	_, rootCompiled := compileBatch1Command(t, assertTrueHandlerSpec(), rootSource)
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	result, err := executeCompiledRoot(context.Background(), Dependencies{
		Driver: driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	}, &compiledFlow{
		path: "/workspace/assert-true-optional.yaml",
		config: model.Config{
			AppID: "com.example.assert-true",
			Env:   map[string]string{"READY": "false", "LABEL": "evaluated label"},
		},
		body: []compiledDispatch{rootCompiled},
	})
	if err != nil || result.Outcome() != Warned || len(result.Commands()) != 1 || result.Commands()[0].Outcome() != Warned {
		t.Fatalf("optional assertTrue root = outcome %q commands %#v error %v", result.Outcome(), result.Commands(), err)
	}
	if got := conditionDriverCalls(driver, enginetest.MethodContentDescriptor); got != 0 {
		t.Fatalf("optional assertTrue descriptor calls = %d, want zero", got)
	}
	for _, action := range driver.Actions() {
		if action.Method != enginetest.MethodDeviceInfo {
			t.Fatalf("optional assertTrue unexpected driver action = %#v", action)
		}
	}
}

func TestAssertTrueEvaluationErrorsDoNotBecomeOptionalWarnings(t *testing.T) {
	t.Parallel()

	optional := true
	command := model.Command{
		Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
		Arguments: map[string]any{"condition": "(", "optional": optional},
		Optional:  &optional,
	}
	dispatcher, compiled := compileBatch1Command(t, assertTrueHandlerSpec(), command)
	_, err := dispatcher.evaluate(context.Background(), newBatch1Evaluation(t, nil), compiled)
	var evaluationError *js.EvaluationError
	if !errors.As(err, &evaluationError) || ClassifyOutcome(err, true) != Failed {
		t.Fatalf("optional JS error = %T %v outcome %q, want EvaluationError/Failed", err, err, ClassifyOutcome(err, true))
	}
}

func TestAssertNotVisibleUsesAdjustedRequiredAndOptionalBudgets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		optional      bool
		start         time.Time
		interaction   time.Time
		wantWaits     []time.Duration
		wantOutcome   Outcome
		descriptorLen int
	}{
		{
			name: "required leaves one second", start: time.Unix(16, 0), interaction: time.Unix(0, 0),
			wantWaits:   []time.Duration{500 * time.Millisecond, 500 * time.Millisecond},
			wantOutcome: Failed, descriptorLen: 3,
		},
		{
			name: "optional budget already consumed", optional: true,
			start: time.Unix(8, 0), interaction: time.Unix(0, 0),
			wantOutcome: Warned, descriptorLen: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := selectorCommand(model.CommandAssertNotVisible, "Loading", test.optional)
			clock := newConditionClock(test.start)
			roots := make([]device.TreeNode, test.descriptorLen)
			for index := range roots {
				roots[index] = conditionRoot("Loading")
			}
			driver := conditionDriver(device.Platform("ios"), roots...)
			lookup := NewElementLookup(driver, clock)
			lookup.RecordInteraction(test.interaction)
			dispatcher, compiled := compileBatch1Command(t, assertNotVisibleHandlerSpec(), command)
			evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
			if err != nil {
				t.Fatalf("evaluate(assertNotVisible) error = %v", err)
			}
			_, err = dispatcher.execute(context.Background(), &executionState{
				lookupFn: func() (*ElementLookup, error) { return lookup, nil },
			}, compiled, evaluated)
			var assertion *AssertionError
			if !errors.As(err, &assertion) || ClassifyOutcome(err, test.optional) != test.wantOutcome {
				t.Fatalf("adjusted assertNotVisible = %T %v outcome %q, want AssertionError/%q", err, err, ClassifyOutcome(err, test.optional), test.wantOutcome)
			}
			if !reflect.DeepEqual(clock.Waits(), test.wantWaits) {
				t.Fatalf("adjusted assertNotVisible waits = %#v, want %#v", clock.Waits(), test.wantWaits)
			}
		})
	}
}

func TestExtendedWaitUntilTimeoutPoliciesAndPredicateOrder(t *testing.T) {
	t.Parallel()

	t.Run("zero timeout checks visible once", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("Ready", "", int64Pointer(0))
		clock := newConditionClock(time.Unix(0, 0))
		driver := conditionDriver(device.Platform("ios"), conditionRoot("Ready"))
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, nil)
		if resultErr != nil || len(clock.Waits()) != 0 || conditionDriverCalls(driver, enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("zero timeout = error %v waits %#v actions %#v", resultErr, clock.Waits(), driver.Actions())
		}
	})

	t.Run("negative timeout checks missing visible once", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("Ready", "", int64Pointer(-1))
		clock := newConditionClock(time.Unix(0, 0))
		driver := conditionDriver(device.Platform("ios"), conditionRoot())
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, nil)
		var assertion *AssertionError
		if !errors.As(resultErr, &assertion) || len(clock.Waits()) != 0 || conditionDriverCalls(driver, enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("negative timeout = error %T %v waits %#v actions %#v", resultErr, resultErr, clock.Waits(), driver.Actions())
		}
	})

	t.Run("omitted timeout uses adjusted required budget", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("Ready", "", nil)
		clock := newConditionClock(time.Unix(20, 0))
		roots := make([]device.TreeNode, 21)
		for index := range roots {
			roots[index] = conditionRoot()
		}
		driver := conditionDriver(device.Platform("ios"), roots...)
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, func(lookup *ElementLookup) {
			lookup.RecordInteraction(time.Unix(5, 0))
		})
		var assertion *AssertionError
		if !errors.As(resultErr, &assertion) || clock.TotalWait() != 2*time.Second {
			t.Fatalf("omitted adjusted timeout = error %T %v total %v waits %#v", resultErr, resultErr, clock.TotalWait(), clock.Waits())
		}
	})

	t.Run("notVisible uses 500ms cadence", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("", "Gone", int64Pointer(1500))
		clock := newConditionClock(time.Unix(0, 0))
		driver := conditionDriver(
			device.Platform("ios"),
			conditionRoot("Gone"), conditionRoot("Gone"), conditionRoot(),
		)
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, nil)
		want := []time.Duration{500 * time.Millisecond, 500 * time.Millisecond}
		if resultErr != nil || !reflect.DeepEqual(clock.Waits(), want) {
			t.Fatalf("notVisible wait = error %v waits %#v want %#v", resultErr, clock.Waits(), want)
		}
	})

	t.Run("missing visible short-circuits notVisible", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("Ready", "Gone", int64Pointer(0))
		clock := newConditionClock(time.Unix(0, 0))
		driver := conditionDriver(device.Platform("ios"), conditionRoot("Gone"), conditionRoot())
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, nil)
		var assertion *AssertionError
		if !errors.As(resultErr, &assertion) || conditionDriverCalls(driver, enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("short circuit = error %T %v actions %#v", resultErr, resultErr, driver.Actions())
		}
	})

	t.Run("both predicates succeed in visible then notVisible order", func(t *testing.T) {
		t.Parallel()
		command := extendedWaitCommand("Ready", "Gone", int64Pointer(0))
		clock := newConditionClock(time.Unix(0, 0))
		driver := conditionDriver(
			device.Platform("ios"),
			conditionRoot("Ready", "Gone"), conditionRoot("Ready"),
		)
		resultErr := executeExtendedWaitCommand(t, command, driver, clock, nil)
		if resultErr != nil || conditionDriverCalls(driver, enginetest.MethodContentDescriptor) != 2 {
			t.Fatalf("both success = error %v actions %#v", resultErr, driver.Actions())
		}
	})
}

func TestAssertionWaitHandlersPreserveTerminalErrors(t *testing.T) {
	t.Parallel()

	connection := NewDeviceConnectionError("descriptor disconnected", errors.New("transport"))
	for _, test := range []struct {
		name    string
		spec    handlerSpec
		command model.Command
	}{
		{
			name: "assertNotVisible", spec: assertNotVisibleHandlerSpec(),
			command: selectorCommand(model.CommandAssertNotVisible, "Gone", false),
		},
		{
			name: "extendedWaitUntil", spec: extendedWaitUntilHandlerSpec(),
			command: extendedWaitCommand("Ready", "", int64Pointer(1000)),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clock := newConditionClock(time.Unix(0, 0))
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
					Platform: device.Platform("ios"), WidthGrid: 100, HeightGrid: 100,
				}}},
				ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: connection}},
			})
			lookup := NewElementLookup(driver, clock)
			dispatcher, compiled := compileBatch1Command(t, test.spec, test.command)
			evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
			if err != nil {
				t.Fatalf("evaluate() error = %v", err)
			}
			_, err = dispatcher.execute(context.Background(), &executionState{
				lookupFn: func() (*ElementLookup, error) { return lookup, nil },
			}, compiled, evaluated)
			if err != connection {
				t.Fatalf("terminal error identity = %T %v, want exact %p", err, err, connection)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executeAssertTrue(ctx, nil, evaluatedDispatch{
		command: model.Command{Kind: model.CommandAssertTrue}, value: assertTrueEvaluated{matched: true},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled assertTrue error = %v, want context.Canceled", err)
	}
}

func TestAssertionWaitCompiledCommandsRemainImmutableAcrossConcurrentEvaluation(t *testing.T) {
	t.Parallel()

	condition := "true"
	label := "stable label"
	source := model.Command{
		Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
		Arguments: map[string]any{"condition": condition, "label": label}, Label: &label,
	}
	snapshot := cloneCommand(source)
	dispatcher, compiled := compileBatch1Command(t, assertTrueHandlerSpec(), source)
	compiledSnapshot := cloneCommand(compiled.command)
	evaluation := evaluationContext{
		evaluateFn: func(context.Context, js.EvalRequest) (js.Result, error) {
			return js.Result{Value: true, Text: "true"}, nil
		},
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			return input, nil
		},
	}

	const executions = 32
	errorsByExecution := make(chan error, executions)
	for range executions {
		go func() {
			evaluated, err := dispatcher.evaluate(context.Background(), evaluation, compiled)
			if err == nil {
				_, err = dispatcher.execute(context.Background(), nil, compiled, evaluated)
			}
			if evaluated.command.Label != nil {
				*evaluated.command.Label = "caller mutation"
			}
			if fields, ok := evaluated.command.Arguments.(map[string]any); ok {
				fields["condition"] = "caller mutation"
			}
			errorsByExecution <- err
		}()
	}
	for range executions {
		if err := <-errorsByExecution; err != nil {
			t.Fatalf("concurrent assertion evaluation error = %v", err)
		}
	}
	if !reflect.DeepEqual(source, snapshot) || !reflect.DeepEqual(compiled.command, compiledSnapshot) {
		t.Fatalf("concurrent evaluation mutated source/compiled snapshots: source=%#v compiled=%#v", source, compiled.command)
	}
}

func TestAssertionWaitCompilersRejectInvalidShapesBeforeEffects(t *testing.T) {
	t.Parallel()

	label := "label"
	optional := true
	text := "Ready"
	point := "50%,50%"
	tests := []struct {
		name    string
		compile func(model.Command) (any, error)
		command model.Command
	}{
		{
			name: "assertNotVisible missing selector", compile: compileAssertNotVisible,
			command: model.Command{Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject},
		},
		{
			name: "assertNotVisible unsupported point", compile: compileAssertNotVisible,
			command: model.Command{
				Kind: model.CommandAssertNotVisible, Form: model.CommandFormObject,
				Selector: &model.ElementSelector{TextRegex: &text, Point: &point},
			},
		},
		{
			name: "assertTrue missing condition", compile: compileAssertTrue,
			command: model.Command{Kind: model.CommandAssertTrue, Form: model.CommandFormObject, Arguments: map[string]any{}},
		},
		{
			name: "assertTrue label mismatch", compile: compileAssertTrue,
			command: model.Command{
				Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
				Arguments: map[string]any{"condition": "true", "label": label},
			},
		},
		{
			name: "assertTrue optional mismatch", compile: compileAssertTrue,
			command: model.Command{
				Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
				Arguments: map[string]any{"condition": "true", "optional": optional},
			},
		},
		{
			name: "assertTrue typed condition metadata", compile: compileAssertTrue,
			command: model.Command{
				Kind: model.CommandAssertTrue, Form: model.CommandFormObject, Arguments: "true",
				Condition: &model.Condition{ScriptCondition: &text},
			},
		},
		{
			name: "extended wait neither predicate", compile: compileExtendedWaitUntil,
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{}, Condition: &model.Condition{},
			},
		},
		{
			name: "extended wait typed predicate mismatch", compile: compileExtendedWaitUntil,
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{}, Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: &text}},
			},
		},
		{
			name: "extended wait unsupported point", compile: compileExtendedWaitUntil,
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{"visible": map[string]any{"text": text, "point": point}},
				Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: &text, Point: &point}},
			},
		},
		{
			name: "extended wait timeout type", compile: compileExtendedWaitUntil,
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{"visible": text, "timeout": true},
				Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: &text}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.compile(test.command); err == nil {
				t.Fatalf("compiler accepted invalid command %#v", test.command)
			} else {
				var configuration *ConfigurationError
				if !errors.As(err, &configuration) {
					t.Fatalf("compiler error = %T %v, want *ConfigurationError", err, err)
				}
			}
		})
	}

	validObject := model.Command{
		Kind: model.CommandAssertTrue, Form: model.CommandFormObject,
		Arguments: map[string]any{"condition": "true", "label": label, "optional": optional},
		Label:     &label, Optional: &optional,
	}
	if _, err := compileAssertTrue(validObject); err != nil {
		t.Fatalf("compileAssertTrue(valid object) error = %v", err)
	}
}

func TestAssertionWaitHandlersFailClosedOnInvalidInternalPayloads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	evaluation := identityConditionEvaluation()
	foreign := struct{}{}
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "evaluate assertNotVisible", run: func() error {
			_, err := evaluateAssertNotVisible(ctx, evaluation, model.Command{}, foreign)
			return err
		}},
		{name: "evaluate assertTrue", run: func() error {
			_, err := evaluateAssertTrue(ctx, evaluation, model.Command{}, foreign)
			return err
		}},
		{name: "evaluate extendedWaitUntil", run: func() error {
			_, err := evaluateExtendedWaitUntil(ctx, evaluation, model.Command{}, foreign)
			return err
		}},
		{name: "execute assertNotVisible", run: func() error {
			_, err := executeAssertNotVisible(ctx, nil, evaluatedDispatch{value: foreign})
			return err
		}},
		{name: "execute assertTrue", run: func() error {
			_, err := executeAssertTrue(ctx, nil, evaluatedDispatch{value: foreign})
			return err
		}},
		{name: "execute extendedWaitUntil", run: func() error {
			_, err := executeExtendedWaitUntil(ctx, nil, evaluatedDispatch{value: foreign})
			return err
		}},
		{name: "assertNotVisible nil context", run: func() error {
			_, err := executeAssertNotVisible(nil, nil, evaluatedDispatch{value: assertNotVisibleEvaluated{}})
			return err
		}},
		{name: "assertNotVisible missing selector", run: func() error {
			_, err := executeAssertNotVisible(ctx, nil, evaluatedDispatch{value: assertNotVisibleEvaluated{}})
			return err
		}},
		{name: "assertTrue nil context", run: func() error {
			_, err := executeAssertTrue(nil, nil, evaluatedDispatch{value: assertTrueEvaluated{matched: true}})
			return err
		}},
		{name: "extendedWaitUntil nil context", run: func() error {
			_, err := executeExtendedWaitUntil(nil, nil, evaluatedDispatch{value: extendedWaitUntilEvaluated{}})
			return err
		}},
		{name: "extendedWaitUntil missing predicate", run: func() error {
			_, err := executeExtendedWaitUntil(ctx, nil, evaluatedDispatch{
				command: model.Command{Condition: &model.Condition{}}, value: extendedWaitUntilEvaluated{},
			})
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			var configuration *ConfigurationError
			if err := operation.run(); !errors.As(err, &configuration) {
				t.Fatalf("operation error = %T %v, want *ConfigurationError", err, err)
			}
		})
	}

	visible := "Ready"
	evaluated, err := evaluateExtendedWaitUntil(ctx, evaluation, model.Command{
		Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
		Arguments: map[string]any{"visible": visible},
	}, extendedWaitUntilCompiled{})
	if err == nil || evaluated.command.Kind != model.CommandExtendedWaitUntil {
		t.Fatalf("missing typed condition evaluation = %#v, %v", evaluated, err)
	}
}

func TestExtendedWaitUntilResolvesInterpolatedTimeout(t *testing.T) {
	t.Parallel()

	evaluation := newBatch1Evaluation(t, map[string]any{"TIMEOUT": "5000"})
	command := model.Command{
		Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
		Arguments: map[string]any{"visible": "Ready", "timeout": "${TIMEOUT}"},
		Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: pointer("Ready")}},
	}
	compiled, err := compileExtendedWaitUntil(command)
	if err != nil {
		t.Fatalf("compileExtendedWaitUntil() error = %v", err)
	}
	evaluated, err := evaluateExtendedWaitUntil(context.Background(), evaluation, command, compiled)
	if err != nil {
		t.Fatalf("evaluateExtendedWaitUntil() error = %v", err)
	}
	payload, ok := evaluated.value.(extendedWaitUntilEvaluated)
	if !ok || payload.timeout == nil || *payload.timeout != 5000*time.Millisecond {
		t.Fatalf("resolved timeout = %#v, want 5s", payload.timeout)
	}
}

func TestWaitForAnimationToEndResolvesInterpolatedTimeout(t *testing.T) {
	t.Parallel()

	evaluation := newBatch1Evaluation(t, map[string]any{"TIMEOUT": "8000"})
	command := model.Command{
		Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormObject,
		Arguments: map[string]any{"timeout": "${TIMEOUT}"},
	}
	compiled, err := compileAnimationWait(command)
	if err != nil {
		t.Fatalf("compileAnimationWait() error = %v", err)
	}
	evaluated, err := evaluateAnimationWait(context.Background(), evaluation, command, compiled)
	if err != nil {
		t.Fatalf("evaluateAnimationWait() error = %v", err)
	}
	payload, ok := evaluated.value.(animationWaitEvaluated)
	if !ok || payload.timeoutMillis != 8000 {
		t.Fatalf("resolved timeout millis = %#v, want 8000", evaluated.value)
	}
}

func newBatch1Evaluation(t *testing.T, environment map[string]any) evaluationContext {
	t.Helper()
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error = %v", err)
	}
	for name, value := range environment {
		if err := runtime.PutEnv(name, value); err != nil {
			_ = runtime.Close()
			t.Fatalf("runtime.PutEnv(%q) error = %v", name, err)
		}
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return evaluationContext{evaluateFn: runtime.Evaluate, interpolateFn: runtime.Interpolate}
}

func selectorCommand(keyword model.CommandKeyword, text string, optional bool) model.Command {
	command := model.Command{
		Kind: keyword, Form: model.CommandFormObject,
		Arguments: text, Selector: &model.ElementSelector{TextRegex: &text},
	}
	if optional {
		command.Optional = &optional
		command.Selector.Optional = &optional
		command.Arguments = map[string]any{"text": text, "optional": optional}
	}
	return command
}

func extendedWaitCommand(visible, notVisible string, timeout *int64) model.Command {
	arguments := make(map[string]any)
	condition := &model.Condition{}
	if visible != "" {
		arguments["visible"] = visible
		condition.Visible = &model.ElementSelector{TextRegex: &visible}
	}
	if notVisible != "" {
		arguments["notVisible"] = notVisible
		condition.NotVisible = &model.ElementSelector{TextRegex: &notVisible}
	}
	if timeout != nil {
		arguments["timeout"] = *timeout
	}
	return model.Command{
		Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
		Arguments: arguments, Condition: condition,
	}
}

func executeExtendedWaitCommand(
	t *testing.T,
	command model.Command,
	driver *enginetest.FakeDriver,
	clock *conditionClock,
	configure func(*ElementLookup),
) error {
	t.Helper()
	lookup := NewElementLookup(driver, clock)
	if configure != nil {
		configure(lookup)
	}
	dispatcher, compiled := compileBatch1Command(t, extendedWaitUntilHandlerSpec(), command)
	evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
	if err != nil {
		t.Fatalf("evaluate(extendedWaitUntil) error = %v", err)
	}
	_, err = dispatcher.execute(context.Background(), &executionState{
		lookupFn: func() (*ElementLookup, error) { return lookup, nil },
	}, compiled, evaluated)
	return err
}

type cancelAfterDescriptorDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
	root   device.TreeNode
}

func newCancelAfterDescriptorDriver(cancel context.CancelFunc, root device.TreeNode) *cancelAfterDescriptorDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 100, HeightGrid: 100},
	}}})
	return &cancelAfterDescriptorDriver{FakeDriver: driver, cancel: cancel, root: root}
}

func (driver *cancelAfterDescriptorDriver) ContentDescriptor(context.Context, device.ContentDescriptorRequest) (device.TreeNode, error) {
	driver.cancel()
	return driver.root, nil
}

type steppingNowClock struct {
	mu            sync.Mutex
	next          time.Time
	last          time.Time
	step          time.Duration
	waitDeadlines []time.Time
}

func newSteppingNowClock(start time.Time, step time.Duration) *steppingNowClock {
	return &steppingNowClock{next: start, step: step}
}

func (clock *steppingNowClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	current := clock.next
	clock.last = current
	clock.next = current.Add(clock.step)
	return current
}

func (clock *steppingNowClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	deadline := clock.last.Add(delay)
	clock.waitDeadlines = append(clock.waitDeadlines, deadline)
	clock.next = deadline
	clock.mu.Unlock()
	return ctx.Err()
}

func (clock *steppingNowClock) WaitDeadlines() []time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]time.Time(nil), clock.waitDeadlines...)
}

func stringPointer(value string) *string { return &value }

func int64Pointer(value int64) *int64 { return &value }

func compileBatch1Command(t *testing.T, spec handlerSpec, command model.Command) (dispatcher, compiledDispatch) {
	t.Helper()
	registry, err := newHandlerRegistry(spec)
	if err != nil {
		t.Fatalf("newHandlerRegistry(%s) error = %v", command.Kind, err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, command)
	if err != nil {
		t.Fatalf("compile(%s) error = %v", command.Kind, err)
	}
	return dispatcher, compiled
}
