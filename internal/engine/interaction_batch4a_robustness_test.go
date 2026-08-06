package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch4AExactLiteralEvaluationAndFailureOrder(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, test := range []struct {
		name    string
		command model.Command
		want    []string
	}{
		{name: "literal input and empty label", command: batch4AInputObject("literal", stringPointer("")), want: []string{"com.example.literal", "literal", ""}},
		{name: "literal erase string", command: batch4AErase("50"), want: []string{"com.example.literal", "50"}},
		{name: "integer erase", command: batch4AErase(int64(1)), want: []string{"com.example.literal"}},
		{name: "bare erase", command: batch4AEraseBare(), want: []string{"com.example.literal"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, test.command)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			trace := make([]string, 0, len(test.want))
			evaluation := evaluationContext{
				interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
					trace = append(trace, input)
					return input, nil
				},
				activeConfig: model.Config{AppID: "com.example.literal"}, hasActiveConfig: true,
			}
			if _, evaluateErr := dispatcher.evaluate(context.Background(), evaluation, compiled); evaluateErr != nil {
				t.Fatal(evaluateErr)
			}
			if !reflect.DeepEqual(trace, test.want) {
				t.Fatalf("interpolation trace = %#v, want %#v", trace, test.want)
			}
		})
	}

	input := batch4AInputObject("${TEXT}", stringPointer("${LABEL}"))
	for _, test := range []struct {
		name      string
		command   model.Command
		appID     string
		failInput string
		replace   map[string]string
		want      []string
	}{
		{name: "app interpolation failure", command: input, appID: "${APP}", failInput: "${APP}", want: []string{"${APP}"}},
		{name: "blank evaluated app", command: input, appID: "${APP}", replace: map[string]string{"${APP}": " "}, want: []string{"${APP}"}},
		{name: "text interpolation failure", command: input, appID: "com.example.order", failInput: "${TEXT}", want: []string{"com.example.order", "${TEXT}"}},
		{name: "label interpolation failure", command: input, appID: "com.example.order", failInput: "${LABEL}", replace: map[string]string{"${TEXT}": "text"}, want: []string{"com.example.order", "${TEXT}", "${LABEL}"}},
		{name: "erase interpolation failure", command: batch4AErase("${COUNT}"), appID: "com.example.order", failInput: "${COUNT}", want: []string{"com.example.order", "${COUNT}"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, test.command)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			primary := NewConfigurationError("selected interpolation failed", nil)
			trace := make([]string, 0, len(test.want))
			evaluation := evaluationContext{
				interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
					trace = append(trace, input)
					if input == test.failInput {
						return "", primary
					}
					if replacement, exists := test.replace[input]; exists {
						return replacement, nil
					}
					return input, nil
				},
				activeConfig: model.Config{AppID: test.appID}, hasActiveConfig: true,
			}
			_, evaluateErr := dispatcher.evaluate(context.Background(), evaluation, compiled)
			if !isConfigurationError(evaluateErr) || !reflect.DeepEqual(trace, test.want) {
				t.Fatalf("evaluation = trace %#v error %T %v, want trace %#v ConfigurationError", trace, evaluateErr, evaluateErr, test.want)
			}
		})
	}
}

func TestInteractionBatch4AActualRootLateInvalidCutoffAndCompletedPrefixOwnership(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compileSequence(
		context.Background(), compileContext{containingFlow: "/workspace/batch4a-prefix.yaml"},
		[]model.Command{batch4AInputObject("prefix", stringPointer("kept")), batch4AErase("${COUNT}"), batch4AInputScalar("later")},
	)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch4ADriver()
	events := make([]Event, 0, 6)
	root := &compiledFlow{
		path:   "/workspace/batch4a-prefix.yaml",
		config: model.Config{AppID: "com.example.batch4a.prefix", Env: map[string]string{"COUNT": "101"}},
		body:   compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-prefix", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch4a-prefix/root-run-000001")
	commands := result.Commands()
	if !isConfigurationError(runErr) || result.Outcome() != Failed || result.ProductError() != runErr || len(commands) != 2 ||
		commands[0].Outcome() != Completed || commands[1].Outcome() != Failed || commands[1].ProductError() != runErr {
		t.Fatalf("late-invalid prefix = result %#v commands %#v error %T %v", result, commands, runErr, runErr)
	}
	if got := batch4AInputRequests(driver.Actions()); !reflect.DeepEqual(got, []device.InputTextRequest{{Text: "prefix", AppIDs: []string{"com.example.batch4a.prefix"}}}) ||
		len(batch4AEraseRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 2 {
		t.Fatalf("late-invalid cutoff actions = %#v", driver.Actions())
	}
	firstEvaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || firstEvaluated.Label == nil || *firstEvaluated.Label != "kept" {
		t.Fatalf("completed prefix evaluated command = %#v", firstEvaluated)
	}
	firstEvaluated.Arguments.(map[string]any)["text"] = "MUTATED"
	*firstEvaluated.Label = "MUTATED"
	freshFirst, freshExists := result.Commands()[0].Metadata().EvaluatedCommand()
	if !freshExists || !reflect.DeepEqual(freshFirst.Arguments, map[string]any{"text": "prefix", "label": "kept"}) ||
		freshFirst.Label == nil || *freshFirst.Label != "kept" {
		t.Fatalf("completed prefix mutation escaped = %#v", freshFirst)
	}
	wantKinds := []EventKind{
		EventFlowStarted,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventFlowFinished,
	}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Completed || events[4].ProductError() != runErr {
		t.Fatalf("late-invalid prefix events = %#v", events)
	}
}

func TestInteractionBatch4AActualRootInputEvaluationCutoffs(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, test := range []struct {
		name      string
		command   model.Command
		config    model.Config
		failInput string
		wantTrace []string
	}{
		{
			name: "app failure", command: batch4AInputObject("${TEXT}", stringPointer("${LABEL}")),
			config: model.Config{AppID: "${APP}"}, failInput: "${APP}", wantTrace: []string{"${APP}"},
		},
		{
			name: "blank app", command: batch4AInputObject("${TEXT}", stringPointer("${LABEL}")),
			config: model.Config{AppID: "${APP}", Env: map[string]string{"APP": " "}}, wantTrace: []string{"${APP}"},
		},
		{
			name: "text failure", command: batch4AInputObject("${TEXT}", stringPointer("${LABEL}")),
			config: model.Config{AppID: "com.example.batch4a.cutoff"}, failInput: "${TEXT}",
			wantTrace: []string{"com.example.batch4a.cutoff", "${TEXT}"},
		},
		{
			name: "label failure", command: batch4AInputObject("literal", stringPointer("${LABEL}")),
			config: model.Config{AppID: "com.example.batch4a.cutoff"}, failInput: "${LABEL}",
			wantTrace: []string{"com.example.batch4a.cutoff", "literal", "${LABEL}"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, test.command)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			primary := NewConfigurationError("Batch 4A interpolation failed", nil)
			factory := &batch4ASelectiveInterpolationFactory{
				base: tapJSFactory(t), failInput: test.failInput, failure: primary,
			}
			driver := batch4ADriver()
			root := &compiledFlow{path: "/workspace/batch4a-input-cutoff.yaml", config: test.config, body: []compiledDispatch{compiled}}
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch4a-input-cutoff", Driver: driver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
			}, root, "batch4a-input-cutoff/root-run-000001")
			if !isConfigurationError(runErr) || result.Outcome() != Failed || len(result.Commands()) != 1 ||
				!reflect.DeepEqual(factory.Trace(), test.wantTrace) {
				t.Fatalf("input cutoff = result %#v error %T %v trace %#v, want %#v", result, runErr, runErr, factory.Trace(), test.wantTrace)
			}
			if got := batch4AMethods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{enginetest.MethodDeviceInfo}) {
				t.Fatalf("input cutoff Driver actions = %#v", driver.Actions())
			}
		})
	}
}

func TestInteractionBatch4AExecutionDependenciesFailClosed(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, batch4AInputScalar("never"))
	if err != nil {
		t.Fatal(err)
	}
	root := &compiledFlow{
		path: "/workspace/batch4a-dependencies.yaml", config: model.Config{AppID: "com.example.batch4a.dependencies"},
		body: []compiledDispatch{compiled},
	}

	validDriver := batch4ADriver()
	var nilContext context.Context
	if result, runErr := executeCompiledRootForRun(nilContext, Dependencies{
		ExecutionID: "batch4a-nil-context", Driver: validDriver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch4a-nil-context/root-run-000001"); !isConfigurationError(runErr) || result.Path() != "" || len(validDriver.Actions()) != 0 {
		t.Fatalf("nil context = result %#v error %T %v actions %#v", result, runErr, runErr, validDriver.Actions())
	}

	for _, test := range []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{name: "nil Driver", mutate: func(dependencies *Dependencies) { dependencies.Driver = nil }},
		{name: "typed-nil Driver", mutate: func(dependencies *Dependencies) { var value *enginetest.FakeDriver; dependencies.Driver = value }},
		{name: "nil Clock", mutate: func(dependencies *Dependencies) { dependencies.Clock = nil }},
		{name: "typed-nil Clock", mutate: func(dependencies *Dependencies) { var value *batch1ATraceClock; dependencies.Clock = value }},
		{name: "nil runtime factory", mutate: func(dependencies *Dependencies) { dependencies.JSFactory = nil }},
		{name: "typed-nil runtime factory", mutate: func(dependencies *Dependencies) { var value *sessionRuntimeFactory; dependencies.JSFactory = value }},
		{name: "nil runtime", mutate: func(dependencies *Dependencies) { dependencies.JSFactory = &sessionRuntimeFactory{} }},
		{name: "typed-nil runtime", mutate: func(dependencies *Dependencies) {
			var value *sessionRuntime
			dependencies.JSFactory = &sessionRuntimeFactory{runtime: value}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch4ADriver()
			dependencies := Dependencies{
				ExecutionID: "batch4a-dependencies", Driver: driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
			}
			test.mutate(&dependencies)
			result, runErr := executeCompiledRootForRun(
				context.Background(), dependencies, root, "batch4a-dependencies/root-run-000001",
			)
			if !isConfigurationError(runErr) || result.Path() != "" || len(driver.Actions()) != 0 {
				t.Fatalf("dependency cutoff = result %#v error %T %v actions %#v", result, runErr, runErr, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch4AHandlerOwnedSettleAttemptPolicy(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, command := range []model.Command{batch4AInputScalar("settle"), batch4AErase(int64(1))} {
		command := command
		t.Run(string(command.Kind), func(t *testing.T) {
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, command)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			driver := batch4ADriver()
			root := &compiledFlow{
				path: "/workspace/batch4a-settle.yaml", config: model.Config{AppID: "com.example.batch4a.settle"},
				body: []compiledDispatch{compiled},
			}
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch4a-settle", Driver: driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
			}, root, "batch4a-settle/root-run-000001")
			settles := settleRequests(driver.Actions())
			if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 1 || result.Commands()[0].Depth() != 0 ||
				batch4APhysicalCount(driver.Actions()) != 1 || len(settles) != 2 {
				t.Fatalf("stable settle = result %#v error %v actions %#v", result, runErr, driver.Actions())
			}
			for _, request := range settles {
				if request.AppID != "com.example.batch4a.settle" || request.TimeoutMillis != nil {
					t.Fatalf("stable settle request = %#v", request)
				}
			}

			exhausted := make([]enginetest.Result[*device.ViewHierarchy], HierarchySettleAttempts)
			driver = batch4ADriverWithSettle(exhausted)
			clock := &batch1ATraceClock{now: time.Unix(2300, 0).UTC()}
			if _, _, executeErr := executeBatch4AForTest(context.Background(), command, nil, driver, clock); executeErr != nil {
				t.Fatalf("inconclusive settle error = %v, want ignored", executeErr)
			}
			if len(settleRequests(driver.Actions())) != HierarchySettleAttempts || len(clock.waits) != HierarchySettleAttempts ||
				sumBatch1ADurations(clock.waits) != time.Duration(HierarchySettleAttempts)*HierarchySettlePollInterval {
				t.Fatalf("inconclusive settle = waits %#v actions %#v", clock.waits, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch4ACopiedTextEvaluationAndSourceOwnership(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	source := batch4AInputObject("${flowbaton.copiedText}-${VALUE}", stringPointer("copied ${VALUE}"))
	authored := cloneCommand(source)
	compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatal(err)
	}
	seedCommand := model.Command{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: "copied"}
	seed := compiledDispatch{
		command: seedCommand, value: "copied",
		spec: handlerSpec{
			keyword: model.CommandSetClipboard, effectClass: EffectHostMutation, postAction: postActionNoSettle,
			evaluate: identityEvaluator,
			execute: func(_ context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectHostMutation}, state.setCopiedText("copied")
			},
		},
	}
	driver := batch4ADriver()
	root := &compiledFlow{
		path:   "/workspace/batch4a-copied-text.yaml",
		config: model.Config{AppID: "com.example.batch4a.copied", Env: map[string]string{"VALUE": "env"}},
		body:   []compiledDispatch{seed, compiled},
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-copied-text", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch4a-copied-text/root-run-000001")
	requests := batch4AInputRequests(driver.Actions())
	commands := result.Commands()
	if runErr != nil || result.Outcome() != Completed || len(commands) != 2 || len(requests) != 1 ||
		requests[0].Text != "copied-env" || !reflect.DeepEqual(requests[0].AppIDs, []string{"com.example.batch4a.copied"}) {
		t.Fatalf("copied-text execution = result %#v error %v requests %#v", result, runErr, requests)
	}
	evaluated, exists := commands[1].Metadata().EvaluatedCommand()
	if !exists || !reflect.DeepEqual(evaluated.Arguments, map[string]any{"text": "copied-env", "label": "copied env"}) ||
		evaluated.Label == nil || *evaluated.Label != "copied env" || !reflect.DeepEqual(source, authored) {
		t.Fatalf("copied-text metadata/source = evaluated %#v source %#v authored %#v", evaluated, source, authored)
	}
}

func TestInteractionBatch4ARepeatedDuplicateAndConcurrentRootIsolation(t *testing.T) {
	t.Parallel()

	path := "/workspace/batch4a-reuse.yaml"
	label := "owned ${SUFFIX}"
	source := batch4AInputObject("text-${SUFFIX}", &label)
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.batch4a.reuse", Env: map[string]string{"SUFFIX": "original"}},
		Commands: []model.Command{source},
	}
	program := singleCompileProgram(flow)
	program.roots = []string{path, path}
	program.graph.Roots = []string{path, path}
	registry, err := newHandlerRegistry(inputTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiledProgram, err := compileProgram(context.Background(), program, registry)
	if err != nil {
		t.Fatal(err)
	}
	root, exists := compiledProgram.Flow(path)
	if !exists || !reflect.DeepEqual(compiledProgram.Roots(), []string{path, path}) {
		t.Fatalf("compiled duplicate roots = %#v, exists %t", compiledProgram.Roots(), exists)
	}
	source.Arguments.(map[string]any)["text"] = "MUTATED"
	*source.Label = "MUTATED"
	prepared := program.flows[path]
	prepared.Commands[0].Arguments.(map[string]any)["text"] = "MUTATED-PREPARED"
	prepared.Config.Env["SUFFIX"] = "mutated"
	program.flows[path] = prepared

	run := func(index int, factory js.Factory) (FlowResult, []Event, []device.InputTextRequest, error) {
		base := batch4ADriver()
		driver := &batch4AMutatingRequestDriver{Driver: base}
		events := make([]Event, 0, 4)
		dependencies := Dependencies{
			ExecutionID: "batch4a-reuse", Driver: driver, Clock: newAdvancingClock(),
			JSFactory: factory, Controller: NoopController{},
			Listeners: []Listener{
				nil,
				ListenerFunc(nil),
				ListenerFunc(func(_ context.Context, event Event) error {
					if command, commandExists := event.Command(); commandExists {
						if arguments, ok := command.Arguments.(map[string]any); ok {
							arguments["text"] = "listener-mutated"
						}
						if command.Label != nil {
							*command.Label = "listener-mutated"
						}
					}
					if evaluated, evaluatedExists := event.Metadata().EvaluatedCommand(); evaluatedExists {
						if arguments, ok := evaluated.Arguments.(map[string]any); ok {
							arguments["text"] = "listener-mutated"
						}
					}
					return errors.New("ignored listener error")
				}),
				ListenerFunc(func(context.Context, Event) error { panic("ignored listener panic") }),
				ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				}),
			},
		}
		result, runErr := executeCompiledRootForRun(
			context.Background(), dependencies, root, fmt.Sprintf("batch4a-reuse/root-run-%06d", index+1),
		)
		return result, events, batch4AInputRequests(base.Actions()), runErr
	}

	for index := range compiledProgram.Roots() {
		result, events, requests, runErr := run(index, tapJSFactory(t))
		if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 1 || len(events) != 4 ||
			!reflect.DeepEqual(requests, []device.InputTextRequest{{Text: "text-original", AppIDs: []string{"com.example.batch4a.reuse"}}}) {
			t.Fatalf("duplicate run %d = result %#v events %#v requests %#v error %v", index, result, events, requests, runErr)
		}
	}

	const runs = 16
	type observation struct {
		result   FlowResult
		events   []Event
		requests []device.InputTextRequest
		err      error
	}
	observations := make([]observation, runs)
	factories := make([]js.Factory, runs)
	for index := range factories {
		factories[index] = tapJSFactory(t)
	}
	var group sync.WaitGroup
	for index := range runs {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			result, events, requests, runErr := run(index+runs, factories[index])
			observations[index] = observation{result: result, events: events, requests: requests, err: runErr}
		}()
	}
	group.Wait()
	for index := range observations {
		observation := &observations[index]
		if observation.err != nil || observation.result.Outcome() != Completed || len(observation.result.Commands()) != 1 ||
			len(observation.events) != 4 || !reflect.DeepEqual(observation.requests, []device.InputTextRequest{{
			Text: "text-original", AppIDs: []string{"com.example.batch4a.reuse"},
		}}) {
			t.Fatalf("concurrent run %d = %#v", index, observation)
		}
		evaluated, evaluatedExists := observation.result.Commands()[0].Metadata().EvaluatedCommand()
		if !evaluatedExists || !reflect.DeepEqual(evaluated.Arguments, map[string]any{"text": "text-original", "label": "owned original"}) ||
			evaluated.Label == nil || *evaluated.Label != "owned original" {
			t.Fatalf("concurrent run %d evaluated command = %#v", index, evaluated)
		}
	}
	first, _ := observations[0].result.Commands()[0].Metadata().EvaluatedCommand()
	first.Arguments.(map[string]any)["text"] = "caller-mutated"
	*first.Label = "caller-mutated"
	other, otherExists := observations[1].result.Commands()[0].Metadata().EvaluatedCommand()
	if !otherExists || other.Arguments.(map[string]any)["text"] != "text-original" || other.Label == nil || *other.Label != "owned original" {
		t.Fatalf("concurrent result alias escaped = %#v", other)
	}
	if compiledText := root.body[0].command.Arguments.(map[string]any)["text"]; compiledText != "text-${SUFFIX}" {
		t.Fatalf("compiled root mutated after reuse: %v", compiledText)
	}
}

func TestInteractionBatch4AConcurrentEraseRequestOwnership(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, batch4AErase("${COUNT}"))
	if err != nil {
		t.Fatal(err)
	}
	const executions = 48
	var group sync.WaitGroup
	errs := make(chan error, executions)
	for index := range executions {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			appID := fmt.Sprintf("com.example.batch4a.erase.%02d", index)
			count := fmt.Sprintf("%d", index%101)
			evaluated, evaluateErr := dispatcher.evaluate(
				context.Background(), batch2AEvaluation(map[string]string{"COUNT": count}, appID), compiled,
			)
			if evaluateErr != nil {
				errs <- evaluateErr
				return
			}
			base := batch4ADriver()
			driver := &batch4AMutatingRequestDriver{Driver: base}
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
			if _, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated); executeErr != nil {
				errs <- executeErr
				return
			}
			requests := batch4AEraseRequests(base.Actions())
			if len(requests) != 1 || requests[0].CharactersToErase != uint32(index%101) ||
				!reflect.DeepEqual(requests[0].AppIDs, []string{appID}) || evaluated.command.Arguments != count {
				errs <- fmt.Errorf("erase ownership escaped for %d: request %#v command %#v", index, requests, evaluated.command)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

type batch4ASelectiveInterpolationFactory struct {
	base      js.Factory
	failInput string
	failure   error
	mu        sync.Mutex
	trace     []string
}

func (factory *batch4ASelectiveInterpolationFactory) NewRuntime() (js.Runtime, error) {
	runtime, err := factory.base.NewRuntime()
	if err != nil {
		return nil, err
	}
	return &batch4ASelectiveInterpolationRuntime{Runtime: runtime, owner: factory}, nil
}

func (factory *batch4ASelectiveInterpolationFactory) Trace() []string {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return append([]string(nil), factory.trace...)
}

type batch4ASelectiveInterpolationRuntime struct {
	js.Runtime
	owner *batch4ASelectiveInterpolationFactory
}

func (runtime *batch4ASelectiveInterpolationRuntime) Interpolate(
	ctx context.Context,
	input string,
	env map[string]any,
) (string, error) {
	runtime.owner.mu.Lock()
	runtime.owner.trace = append(runtime.owner.trace, input)
	runtime.owner.mu.Unlock()
	if input == runtime.owner.failInput {
		return "", runtime.owner.failure
	}
	return runtime.Runtime.Interpolate(ctx, input, env)
}
