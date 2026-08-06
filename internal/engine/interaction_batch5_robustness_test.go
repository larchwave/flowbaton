package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch5RobustnessPrivateSpecsRemainClosed(t *testing.T) {
	t.Parallel()

	if got := len(clipboardHandlerSpecs()); got != 3 {
		t.Fatalf("clipboardHandlerSpecs() count = %d, want 3", got)
	}
}

func TestInteractionBatch5SetterPoisonIntegrationCloseAndLaterRootIsolation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		command     model.Command
		wantRuntime string
		driver      func() *enginetest.FakeDriver
	}{
		{
			name: "setClipboard", command: batch5SetCommand("poison-set"), wantRuntime: "poison-set",
			driver: batch5DeviceInfoOnlyDriver,
		},
		{
			name: "optional copyTextFrom", command: batch5CopyIDCommand("target", boolPointer(true), nil), wantRuntime: "copied-value",
			driver: func() *enginetest.FakeDriver { return batch5CopyPasteDriver(map[string]string{"text": "copied-value"}) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			rawSetter := errors.New("runtime mutated then failed")
			closeFailure := errors.New("runtime close failed")
			// The command writes copiedText, then the injected setter error surfaces.
			base := &sessionRuntime{closeErr: closeFailure}
			base.SetCopiedError(rawSetter)
			runtime := &batch5LiteralSessionRuntime{sessionRuntime: base}
			factory := &sessionRuntimeFactory{runtime: runtime}
			root, err := batch5CompileRoot(
				[]model.Command{test.command, batch5PasteCommand()},
				model.Config{AppID: "com.example.batch5.poison"},
			)
			if err != nil {
				t.Fatal(err)
			}
			events := make([]Event, 0, 4)
			resolverCalls := 0
			driver := test.driver()
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch5-poison", Driver: driver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
				FailureResolver: FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
					resolverCalls++
					return FailureDecisionContinue
				}),
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, "batch5-poison/root-run-000001")
			if runErr == nil {
				t.Fatal("poison error = nil")
			}
			assertBatch5CoreIntegrityPrimary(t, runErr, rawSetter)
			commands := result.Commands()
			if result.Outcome() != Failed || result.ProductError() != runErr || len(commands) != 1 ||
				commands[0].Outcome() != Failed || commands[0].ProductError() != runErr || resolverCalls != 0 {
				t.Fatalf("poison result = %#v commands %#v resolver calls %d error %p", result, commands, resolverCalls, runErr)
			}
			if got := batch5InputRequests(driver.Actions()); len(got) != 0 {
				t.Fatalf("poisoned root reached paste = %#v", got)
			}
			_, copiedValues, closeCalls := base.Snapshot()
			if closeCalls != 1 || !reflect.DeepEqual(copiedValues, []string{test.wantRuntime}) {
				t.Fatalf("runtime snapshot = copied %#v close calls %d", copiedValues, closeCalls)
			}
			wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
			gotKinds := make([]EventKind, len(events))
			for index, event := range events {
				gotKinds[index] = event.Kind()
				if event.Kind() == EventCommandFinished || event.Kind() == EventFlowFinished {
					if event.ProductError() != runErr || event.Outcome() != Failed {
						t.Fatalf("terminal event %d = outcome %q error %p", index, event.Outcome(), event.ProductError())
					}
				}
			}
			if !reflect.DeepEqual(gotKinds, wantKinds) {
				t.Fatalf("poison event kinds = %#v, want %#v", gotKinds, wantKinds)
			}

			laterRoot, err := batch5CompileRoot(
				[]model.Command{batch5PasteCommand()},
				model.Config{AppID: "com.example.batch5.later"},
			)
			if err != nil {
				t.Fatal(err)
			}
			laterDriver := batch5PasteDriver()
			laterResult, laterErr := executeCompiledRoot(context.Background(), Dependencies{
				Driver: laterDriver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			}, laterRoot)
			if laterErr != nil || laterResult.Outcome() != Completed ||
				!reflect.DeepEqual(batch5InputRequests(laterDriver.Actions()), []device.InputTextRequest{{
					Text: "", AppIDs: []string{"com.example.batch5.later"},
				}}) {
				t.Fatalf("later root = result %#v requests %#v error %v", laterResult, batch5InputRequests(laterDriver.Actions()), laterErr)
			}
		})
	}
}

func TestInteractionBatch5RepeatedAndConcurrentRootIsolationStress(t *testing.T) {
	factory := &batch5FreshRuntimeFactory{delegate: tapJSFactory(t)}

	for iteration := 0; iteration < 3; iteration++ {
		value := fmt.Sprintf("repeat-root-%02d", iteration)
		appID := fmt.Sprintf("com.example.batch5.repeat.%02d", iteration)
		rootRunID := fmt.Sprintf("batch5-repeat/root-run-%06d", iteration+1)
		root, err := batch5CompileRoot(
			[]model.Command{batch5SetCommand(value), batch5PasteCommand()},
			model.Config{AppID: appID},
		)
		if err != nil {
			t.Fatal(err)
		}
		driver := batch5PasteDriver()
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: "batch5-repeat", Driver: driver, Clock: newAdvancingClock(),
			JSFactory: factory, Controller: NoopController{},
		}, root, rootRunID)
		requests := batch5InputRequests(driver.Actions())
		commands := result.Commands()
		if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID || len(commands) != 2 ||
			commands[0].RootRunID() != rootRunID || commands[1].RootRunID() != rootRunID ||
			commands[0].Command().Kind != model.CommandSetClipboard || commands[1].Command().Kind != model.CommandPasteText ||
			!reflect.DeepEqual(requests, []device.InputTextRequest{{Text: value, AppIDs: []string{appID}}}) {
			t.Fatalf("repeat %d = result %#v requests %#v error %v", iteration, result, requests, runErr)
		}
	}

	const roots = 12
	type observation struct {
		index     int
		value     string
		appID     string
		rootRunID string
		request   device.InputTextRequest
		result    FlowResult
		err       error
	}
	compiledRoots := make([]*compiledFlow, roots)
	for index := 0; index < roots; index++ {
		value := fmt.Sprintf("concurrent-root-%02d", index)
		appID := fmt.Sprintf("com.example.batch5.concurrent.%02d", index)
		compiled, err := batch5CompileRoot(
			[]model.Command{batch5SetCommand(value), batch5PasteCommand()},
			model.Config{AppID: appID},
		)
		if err != nil {
			t.Fatal(err)
		}
		compiledRoots[index] = compiled
	}
	observations := make(chan observation, roots)
	var group sync.WaitGroup
	for index := 0; index < roots; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			value := fmt.Sprintf("concurrent-root-%02d", index)
			appID := fmt.Sprintf("com.example.batch5.concurrent.%02d", index)
			rootRunID := fmt.Sprintf("batch5-concurrent/root-run-%06d", index+1)
			driver := batch5PasteDriver()
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: fmt.Sprintf("batch5-concurrent-%02d", index), Driver: driver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
			}, compiledRoots[index], rootRunID)
			requests := batch5InputRequests(driver.Actions())
			observation := observation{
				index: index, value: value, appID: appID, rootRunID: rootRunID, result: result, err: runErr,
			}
			if len(requests) == 1 {
				observation.request = requests[0]
			}
			observations <- observation
		}(index)
	}
	group.Wait()
	close(observations)
	seen := make(map[int]bool, roots)
	seenRootRuns := make(map[string]bool, roots)
	for observation := range observations {
		seen[observation.index] = true
		commands := observation.result.Commands()
		if observation.err != nil || observation.result.Outcome() != Completed ||
			observation.result.RootRunID() != observation.rootRunID || len(commands) != 2 ||
			commands[0].RootRunID() != observation.rootRunID || commands[1].RootRunID() != observation.rootRunID ||
			commands[0].Command().Kind != model.CommandSetClipboard || commands[1].Command().Kind != model.CommandPasteText ||
			observation.request.Text != observation.value || !reflect.DeepEqual(observation.request.AppIDs, []string{observation.appID}) {
			t.Fatalf("concurrent observation = %#v", observation)
		}
		if seenRootRuns[observation.rootRunID] {
			t.Fatalf("duplicate concurrent root run ID %q", observation.rootRunID)
		}
		seenRootRuns[observation.rootRunID] = true
	}
	if len(seen) != roots || len(seenRootRuns) != roots {
		t.Fatalf("concurrent roots observed = indexes %d root runs %d, want %d", len(seen), len(seenRootRuns), roots)
	}
	if count, unique := factory.RuntimeSnapshot(); count != 3+roots || !unique {
		t.Fatalf("root runtimes = count %d unique %t, want %d distinct instances", count, unique, 3+roots)
	}
}

func TestInteractionBatch5SourceHierarchyRequestAndReturnedDataOwnershipStress(t *testing.T) {
	t.Parallel()

	source := batch5CopyIDCommand("target", nil, stringPointer("owned label"))
	authored := cloneCommand(source)
	registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatal(err)
	}
	arguments := source.Arguments.(map[string]any)
	arguments["id"] = "mutated-after-compile"
	*source.Selector.IDRegex = "mutated-after-compile"
	*source.Label = "mutated-after-compile"
	if compiled.command.Arguments.(map[string]any)["id"] != "target" || *compiled.command.Selector.IDRegex != "target" ||
		*compiled.command.Label != "owned label" {
		t.Fatalf("compiled command aliased source = %#v", compiled.command)
	}
	if authored.Arguments.(map[string]any)["id"] != "target" {
		t.Fatalf("authored snapshot unexpectedly changed = %#v", authored)
	}

	base := batch5PasteDriver()
	mutatingDriver := &batch4AMutatingRequestDriver{Driver: base}
	clock := newAdvancingClock()
	lookup := NewElementLookup(mutatingDriver, clock)
	effect, evaluated, err := executeBatch5ForTest(
		context.Background(), batch5PasteCommand(), nil, mutatingDriver, clock, lookup,
		"owned text", nil,
	)
	requests := batch5InputRequests(base.Actions())
	if err != nil || effect.effectClass != EffectDeviceMutation ||
		!reflect.DeepEqual(requests, []device.InputTextRequest{{Text: "owned text", AppIDs: []string{"com.example.batch5"}}}) {
		t.Fatalf("mutating Driver = effect %#v evaluated %#v requests %#v error %v", effect, evaluated, requests, err)
	}
	plan := evaluated.value.(pasteTextEvaluated)
	if plan.appID != "com.example.batch5" {
		t.Fatalf("mutating Driver escaped into plan = %#v", plan)
	}
	requests[0].AppIDs[0] = "mutated-by-test"
	fresh := batch5InputRequests(base.Actions())
	if fresh[0].AppIDs[0] != "com.example.batch5" || fresh[0].Text != "owned text" {
		t.Fatalf("returned request mutation escaped = %#v", fresh)
	}

	initial := batch5Element(map[string]string{"text": "initial", "bounds": "[0,0][10,10]"})
	accepted := batch5Element(map[string]string{"text": "accepted", "bounds": "[0,0][10,10]"})
	value, err := copyTextFromAcceptedElement(initial, accepted)
	accepted.Node.Attributes["text"] = "mutated-after-extraction"
	if err != nil || value != "accepted" {
		t.Fatalf("owned extraction = %q error %v", value, err)
	}
}

func TestInteractionBatch5FailClosedInternalPayloadsAndEvaluationCutoffs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var nilContext context.Context
	evaluation := evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) { return input, nil },
		activeConfig:  model.Config{AppID: "com.example.batch5"}, hasActiveConfig: true,
	}
	for name, call := range map[string]func() error{
		"set evaluator": func() error {
			_, err := evaluateSetClipboard(ctx, evaluation, batch5SetCommand("x"), struct{}{})
			return err
		},
		"copy evaluator": func() error {
			_, err := evaluateCopyTextFrom(ctx, evaluation, batch5CopyIDCommand("target", nil, nil), struct{}{})
			return err
		},
		"paste evaluator": func() error {
			_, err := evaluatePasteText(ctx, evaluation, batch5PasteCommand(), struct{}{})
			return err
		},
		"set executor payload": func() error {
			_, err := executeSetClipboard(ctx, &executionState{}, evaluatedDispatch{value: struct{}{}})
			return err
		},
		"copy executor payload": func() error {
			_, err := executeCopyTextFrom(ctx, &executionState{}, evaluatedDispatch{value: struct{}{}})
			return err
		},
		"paste executor payload": func() error {
			_, err := executePasteText(ctx, &executionState{}, evaluatedDispatch{value: struct{}{}})
			return err
		},
		"set nil context": func() error {
			_, err := executeSetClipboard(nilContext, &executionState{}, evaluatedDispatch{value: setClipboardEvaluated{}})
			return err
		},
		"copy nil context": func() error {
			_, err := executeCopyTextFrom(nilContext, &executionState{}, evaluatedDispatch{value: copyTextFromEvaluated{selector: &model.ElementSelector{IDRegex: stringPointer("target")}}})
			return err
		},
		"paste nil context": func() error {
			_, err := executePasteText(nilContext, &executionState{}, evaluatedDispatch{value: pasteTextEvaluated{appID: "app"}})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !isConfigurationError(err) {
				t.Fatalf("error = %T %v", err, err)
			}
		})
	}

	interpolationFailure := NewConfigurationError("interpolation failed", nil)
	registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, batch5SetCommand("${VALUE}"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.evaluate(ctx, evaluationContext{
		interpolateFn: func(context.Context, string, map[string]any) (string, error) { return "", interpolationFailure },
	}, compiled); !errors.Is(err, interpolationFailure) {
		t.Fatalf("set interpolation error = %T %v", err, err)
	}
}

type batch5LiteralSessionRuntime struct {
	*sessionRuntime
}

type batch5FreshRuntimeFactory struct {
	mu       sync.Mutex
	delegate js.Factory
	runtimes []js.Runtime
}

func (factory *batch5FreshRuntimeFactory) NewRuntime() (js.Runtime, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	runtime, err := factory.delegate.NewRuntime()
	if err == nil {
		factory.runtimes = append(factory.runtimes, runtime)
	}
	return runtime, err
}

func (factory *batch5FreshRuntimeFactory) RuntimeSnapshot() (int, bool) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	identities := make(map[uintptr]struct{}, len(factory.runtimes))
	for _, runtime := range factory.runtimes {
		value := reflect.ValueOf(runtime)
		if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
			return len(factory.runtimes), false
		}
		identity := value.Pointer()
		if _, duplicate := identities[identity]; duplicate {
			return len(factory.runtimes), false
		}
		identities[identity] = struct{}{}
	}
	return len(factory.runtimes), true
}

func (runtime *batch5LiteralSessionRuntime) Interpolate(
	_ context.Context,
	input string,
	_ map[string]any,
) (string, error) {
	return input, nil
}

func batch5DeviceInfoOnlyDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400},
	}}})
	return driver
}
