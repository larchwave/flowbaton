package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch4HPrivateRandomHandlerTableIsComplete(t *testing.T) {
	t.Parallel()

	want := map[model.CommandKeyword]InputKind{
		model.CommandInputRandomText:        InputText,
		model.CommandInputRandomNumber:      InputNumber,
		model.CommandInputRandomEmail:       InputEmail,
		model.CommandInputRandomPersonName:  InputPersonName,
		model.CommandInputRandomCityName:    InputCityName,
		model.CommandInputRandomCountryName: InputCountryName,
		model.CommandInputRandomColorName:   InputColorName,
	}
	specs := randomInputHandlerSpecs()
	if len(specs) != len(want) {
		t.Fatalf("random handler specs = %d, want %d", len(specs), len(want))
	}
	for _, spec := range specs {
		kind, exists := want[spec.keyword]
		if !exists {
			t.Fatalf("unexpected random handler keyword %q", spec.keyword)
		}
		if spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle ||
			spec.settleRequest != nil || spec.requiredService != requiredServiceInputGenerator ||
			spec.compile == nil || spec.evaluate == nil || spec.execute == nil {
			t.Fatalf("%s/%s handler spec = %#v, want complete private device-mutation spec", spec.keyword, kind, spec)
		}
		delete(want, spec.keyword)
	}
	if len(want) != 0 {
		t.Fatalf("missing random handler specs: %#v", want)
	}
}

func TestInteractionBatch4HNamedSentinelReachesGenerator(t *testing.T) {
	t.Parallel()

	generator := &batch4HRecordingGenerator{value: "generated@example.com"}
	state := &executionState{dependencies: Dependencies{InputGenerator: generator}}
	request := InputRequest{Kind: InputEmail, Length: -1}
	generated, err := state.generateInput(context.Background(), request)
	if err != nil || generated != generator.value {
		t.Fatalf("generateInput(named sentinel) = %q, %T %v, want exact generated value", generated, err, err)
	}
	if generator.calls != 1 || generator.request != request {
		t.Fatalf("generator calls/request = %d/%#v, want 1/%#v", generator.calls, generator.request, request)
	}
}

func TestInteractionBatch4HCompileDefaultsBoundsAndClosedPayloads(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandInputRandomText, model.CommandInputRandomNumber} {
		keyword := keyword
		for _, test := range []struct {
			name      string
			command   model.Command
			want      int
			wantError bool
		}{
			{name: "bare default", command: batch4HRandomBare(keyword), want: 8},
			{name: "empty default", command: batch4HRandomObject(keyword, map[string]any{}), want: 8},
			{name: "zero", command: batch4HRandomObject(keyword, map[string]any{"length": int64(0)}), want: 0},
			{name: "one", command: batch4HRandomObject(keyword, map[string]any{"length": int64(1)}), want: 1},
			{name: "eight", command: batch4HRandomObject(keyword, map[string]any{"length": int64(8)}), want: 8},
			{name: "maximum", command: batch4HRandomObject(keyword, map[string]any{"length": int64(1024)}), want: 1024},
			{name: "negative", command: batch4HRandomObject(keyword, map[string]any{"length": int64(-1)}), wantError: true},
			{name: "oversized", command: batch4HRandomObject(keyword, map[string]any{"length": int64(1025)}), wantError: true},
			{name: "fraction", command: batch4HRandomObject(keyword, map[string]any{"length": 1.5}), wantError: true},
			{name: "string", command: batch4HRandomObject(keyword, map[string]any{"length": "eight"}), wantError: true},
			{name: "boolean", command: batch4HRandomObject(keyword, map[string]any{"length": true}), wantError: true},
			{name: "null", command: batch4HRandomObject(keyword, map[string]any{"length": nil}), wantError: true},
			{name: "unknown", command: batch4HRandomObject(keyword, map[string]any{"mystery": true}), wantError: true},
		} {
			test := test
			t.Run(string(keyword)+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				value, err := compileRandomInput(test.command)
				if test.wantError {
					if !isConfigurationError(err) {
						t.Fatalf("compileRandomInput() error = %T %v, want ConfigurationError", err, err)
					}
					return
				}
				plan, ok := value.(randomInputCompiled)
				definition, _ := randomInputDefinitionFor(keyword)
				if err != nil || !ok || plan.keyword != keyword || plan.kind != definition.kind || plan.length != test.want {
					t.Fatalf("compileRandomInput() = %#v, %v, want %s/%s/%d", value, err, keyword, definition.kind, test.want)
				}
			})
		}
	}

	for _, definition := range randomInputDefinitions()[2:] {
		definition := definition
		for _, command := range []model.Command{
			batch4HRandomBare(definition.keyword),
			batch4HRandomObject(definition.keyword, map[string]any{}),
		} {
			value, err := compileRandomInput(command)
			plan, ok := value.(randomInputCompiled)
			if err != nil || !ok || plan.kind != definition.kind || plan.length != inputNamedLengthSentinel {
				t.Fatalf("compileRandomInput(%s) = %#v, %v", definition.keyword, value, err)
			}
		}
		if _, err := compileRandomInput(batch4HRandomObject(definition.keyword, map[string]any{"length": int64(0)})); !isConfigurationError(err) {
			t.Fatalf("compileRandomInput(%s length) error = %T %v", definition.keyword, err, err)
		}
	}

	invalidMetadata := batch4HRandomBare(model.CommandInputRandomText)
	invalidMetadata.Children = []model.Command{{Kind: model.CommandBack}}
	if _, err := compileRandomInput(invalidMetadata); !isConfigurationError(err) {
		t.Fatalf("compileRandomInput(children) error = %T %v", err, err)
	}
	invalidMetadata = batch4HRandomBare(model.CommandInputRandomText)
	invalidMetadata.Condition = &model.Condition{}
	if _, err := compileRandomInput(invalidMetadata); !isConfigurationError(err) {
		t.Fatalf("compileRandomInput(condition) error = %T %v", err, err)
	}
	invalidMetadata = batch4HRandomBare(model.CommandInputRandomText)
	invalidMetadata.Links = []model.FileLink{{Path: "child.yaml"}}
	if _, err := compileRandomInput(invalidMetadata); !isConfigurationError(err) {
		t.Fatalf("compileRandomInput(link) error = %T %v", err, err)
	}
	invalidMetadata = batch4HRandomBare(model.CommandInputRandomText)
	invalidMetadata.Selector = &model.ElementSelector{}
	if _, err := compileRandomInput(invalidMetadata); !isConfigurationError(err) {
		t.Fatalf("compileRandomInput(selector) error = %T %v", err, err)
	}
	if _, err := compileRandomInput(model.Command{Kind: model.CommandInputText, Form: model.CommandFormScalar}); !isConfigurationError(err) {
		t.Fatalf("compileRandomInput(wrong keyword) error = %T %v", err, err)
	}
}

func TestInteractionBatch4HAllKindsGenerateThenInputUnchangedAndSettleOnce(t *testing.T) {
	t.Parallel()

	generatedValues := []string{"", " 007 ", "person 世界", "city\nline", "country\tname", "#00ffAA", "mail+tag@example.test"}
	definitions := randomInputDefinitions()
	for index, definition := range definitions {
		index, definition := index, definition
		t.Run(string(definition.keyword), func(t *testing.T) {
			t.Parallel()

			command := batch4HRandomBare(definition.keyword)
			wantLength := inputNamedLengthSentinel
			if definition.variableLength {
				wantLength = 8
				command = batch4HRandomObject(definition.keyword, map[string]any{"length": int64(wantLength)})
			}
			registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := newDispatcher(registry)
			compiled, err := dispatcher.compile(context.Background(), compileContext{containingFlow: "/workspace/batch4h.yaml"}, command)
			if err != nil {
				t.Fatal(err)
			}
			activeConfigCalls := 0
			interpolationCalls := 0
			evaluation := evaluationContext{
				activeConfigFn: func() (model.Config, error) {
					activeConfigCalls++
					return model.Config{AppID: "com.${APP}.batch4h"}, nil
				},
				interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
					interpolationCalls++
					if input != "com.${APP}.batch4h" {
						return "", fmt.Errorf("unexpected interpolation %q", input)
					}
					return "com.example.batch4h", nil
				},
			}
			evaluated, err := dispatcher.evaluate(context.Background(), evaluation, compiled)
			if err != nil {
				t.Fatal(err)
			}
			if activeConfigCalls != 1 || interpolationCalls != 1 {
				t.Fatalf("appId resolution/interpolation calls = %d/%d, want 1/1", activeConfigCalls, interpolationCalls)
			}

			trace := make([]string, 0, 2)
			generator := &batch4HRecordingGenerator{value: generatedValues[index], trace: &trace}
			base := batch4ADriver()
			driver := &batch4HTraceInputDriver{Driver: base, trace: &trace}
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			state := &executionState{
				dependencies: Dependencies{Driver: driver, Clock: clock, InputGenerator: generator},
				lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
			}
			effect, err := dispatcher.execute(context.Background(), state, compiled, evaluated)
			if err != nil || effect.effectClass != EffectDeviceMutation {
				t.Fatalf("execute() = effect %#v, error %T %v", effect, err, err)
			}
			wantInput := InputRequest{Kind: definition.kind, Length: wantLength}
			if generator.calls != 1 || generator.request != wantInput {
				t.Fatalf("generator calls/request = %d/%#v, want 1/%#v", generator.calls, generator.request, wantInput)
			}
			requests := batch4AInputRequests(base.Actions())
			wantDevice := []device.InputTextRequest{{Text: generatedValues[index], AppIDs: []string{"com.example.batch4h"}}}
			if !reflect.DeepEqual(requests, wantDevice) || len(settleRequests(base.Actions())) != 2 {
				t.Fatalf("Driver requests/settles = %#v/%#v, want %#v/one complete settle operation", requests, settleRequests(base.Actions()), wantDevice)
			}
			wantTrace := []string{"generator:" + string(definition.kind), "driver:" + generatedValues[index]}
			if !reflect.DeepEqual(trace, wantTrace) {
				t.Fatalf("call trace = %#v, want %#v", trace, wantTrace)
			}
		})
	}
}

func TestInteractionBatch4HServiceValidationIsExactPerKind(t *testing.T) {
	t.Parallel()

	valid := []InputRequest{
		{Kind: InputText, Length: 0}, {Kind: InputText, Length: 1024},
		{Kind: InputNumber, Length: 0}, {Kind: InputNumber, Length: 1024},
		{Kind: InputEmail, Length: -1}, {Kind: InputPersonName, Length: -1},
		{Kind: InputCityName, Length: -1}, {Kind: InputCountryName, Length: -1}, {Kind: InputColorName, Length: -1},
	}
	for _, request := range valid {
		generator := &batch4HRecordingGenerator{value: "generated"}
		state := &executionState{dependencies: Dependencies{InputGenerator: generator}}
		if generated, err := state.generateInput(context.Background(), request); err != nil || generated != "generated" || generator.calls != 1 {
			t.Fatalf("generateInput(%#v) = %q, %v, calls %d", request, generated, err, generator.calls)
		}
	}
	invalid := []InputRequest{
		{Kind: InputKind("future"), Length: 0},
		{Kind: InputText, Length: -1}, {Kind: InputText, Length: 1025},
		{Kind: InputNumber, Length: -1}, {Kind: InputNumber, Length: 1025},
		{Kind: InputEmail, Length: 0}, {Kind: InputPersonName, Length: 1},
		{Kind: InputCityName, Length: -2}, {Kind: InputCountryName, Length: 1024}, {Kind: InputColorName, Length: 8},
	}
	for _, request := range invalid {
		generator := &batch4HRecordingGenerator{value: "must-not-run"}
		state := &executionState{dependencies: Dependencies{InputGenerator: generator}}
		if generated, err := state.generateInput(context.Background(), request); !isConfigurationError(err) || generated != "" || generator.calls != 0 {
			t.Fatalf("generateInput(%#v) = %q, %T %v, calls %d", request, generated, err, err, generator.calls)
		}
	}
}

func TestInteractionBatch4HActualSpecsRequireGeneratorBeforeCommandEffects(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	clock := enginetest.NewFakeClock(time.Unix(2300, 0).UTC())
	dependencies := coreDependencies(driver, clock)
	var commandEvents atomic.Int64
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
			commandEvents.Add(1)
		}
		return nil
	})}
	core, err := newExecutorCore(dependencies, randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	results, runErr := core.executeSequence(context.Background(), []model.Command{
		batch4HRandomBare(model.CommandInputRandomEmail),
	}, 0)
	if !isConfigurationError(runErr) || len(results) != 0 || commandEvents.Load() != 0 ||
		len(driver.Actions()) != 0 || core.timeline.Checkpoint() != 0 || len(core.ledger.snapshot()) != 0 {
		t.Fatalf(
			"missing-generator boundary = error %T %v results %#v events %d actions %#v timeline %d ledger %#v",
			runErr, runErr, results, commandEvents.Load(), driver.Actions(), core.timeline.Checkpoint(), core.ledger.snapshot(),
		)
	}
}

func TestInteractionBatch4HGeneratorDriverCancellationAndSettleBoundaries(t *testing.T) {
	t.Parallel()

	command := batch4HRandomBare(model.CommandInputRandomEmail)
	dispatcher, compiled, evaluated := batch4HPreparedDispatch(t, command, "com.example.batch4h.boundaries")

	t.Run("generator partial error is discarded", func(t *testing.T) {
		primary := errors.New("generator failed")
		generator := &batch4HRecordingGenerator{value: "partial-value", err: primary}
		state := &executionState{dependencies: Dependencies{InputGenerator: generator}}
		generated, err := state.generateInput(context.Background(), InputRequest{Kind: InputEmail, Length: -1})
		if generated != "" || err != primary || generator.calls != 1 {
			t.Fatalf("generateInput() = %q, %T %v, calls %d", generated, err, err, generator.calls)
		}

		base := batch4ADriver()
		clock := newAdvancingClock()
		lookup := NewElementLookup(base, clock)
		state = &executionState{
			dependencies: Dependencies{Driver: base, Clock: clock, InputGenerator: generator},
			lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		}
		_, err = dispatcher.execute(context.Background(), state, compiled, evaluated)
		if !errors.Is(err, primary) || generator.calls != 2 || len(batch4AInputRequests(base.Actions())) != 0 || len(settleRequests(base.Actions())) != 0 {
			t.Fatalf("generator failure = %T %v calls %d actions %#v", err, err, generator.calls, base.Actions())
		}
	})

	t.Run("pre-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		generator := &batch4HRecordingGenerator{value: "must-not-run"}
		base := batch4ADriver()
		state := batch4HExecutionState(base, newAdvancingClock(), generator)
		_, err := dispatcher.execute(ctx, state, compiled, evaluated)
		if !errors.Is(err, context.Canceled) || generator.calls != 0 || len(base.Actions()) != 0 {
			t.Fatalf("pre-cancel = %T %v calls %d actions %#v", err, err, generator.calls, base.Actions())
		}
	})

	t.Run("context-ignoring generator cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		generator := &batch4HCancellingGenerator{cancel: cancel, value: "late-value"}
		base := batch4ADriver()
		state := batch4HExecutionState(base, newAdvancingClock(), generator)
		_, err := dispatcher.execute(ctx, state, compiled, evaluated)
		if !errors.Is(err, context.Canceled) || generator.calls.Load() != 1 || len(batch4AInputRequests(base.Actions())) != 0 {
			t.Fatalf("post-generator cancellation = %T %v calls %d actions %#v", err, err, generator.calls.Load(), base.Actions())
		}
	})

	t.Run("generator panic is contained", func(t *testing.T) {
		base := batch4ADriver()
		state := batch4HExecutionState(base, newAdvancingClock(), batch4HPanicGenerator{})
		_, err := dispatcher.execute(context.Background(), state, compiled, evaluated)
		if !isConfigurationError(err) || len(batch4AInputRequests(base.Actions())) != 0 || len(settleRequests(base.Actions())) != 0 {
			t.Fatalf("generator panic = %T %v actions %#v", err, err, base.Actions())
		}
	})

	t.Run("Driver failure has no watermark or settle", func(t *testing.T) {
		primary := errors.New("input adapter failed")
		base := batch4ADriver()
		base.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{{Err: primary}}})
		generator := &batch4HRecordingGenerator{value: "generated"}
		clock := &batch1ATraceClock{now: time.Unix(2400, 0).UTC()}
		lookup := NewElementLookup(base, clock)
		state := &executionState{
			dependencies: Dependencies{Driver: base, Clock: clock, InputGenerator: generator},
			lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		}
		_, err := dispatcher.execute(context.Background(), state, compiled, evaluated)
		clock.now = clock.now.Add(time.Second)
		if !errors.Is(err, primary) || generator.calls != 1 || len(batch4AInputRequests(base.Actions())) != 1 ||
			len(settleRequests(base.Actions())) != 0 || lookup.AdjustedTimeout(LookupOptions{}) != LookupTimeout {
			t.Fatalf("Driver failure = %T %v calls %d actions %#v timeout %v", err, err, generator.calls, base.Actions(), lookup.AdjustedTimeout(LookupOptions{}))
		}
	})

	t.Run("post-Driver cancellation watermarks once and skips settle", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch4ADriver()
		driver := &batch4ACancelAfterInputDriver{Driver: base, cancel: cancel}
		generator := &batch4HRecordingGenerator{value: "generated"}
		clock := &batch1ATraceClock{now: time.Unix(2500, 0).UTC()}
		lookup := NewElementLookup(driver, clock)
		state := &executionState{
			dependencies: Dependencies{Driver: driver, Clock: clock, InputGenerator: generator},
			lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		}
		_, err := dispatcher.execute(ctx, state, compiled, evaluated)
		clock.now = clock.now.Add(time.Second)
		if !errors.Is(err, context.Canceled) || len(batch4AInputRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 0 ||
			lookup.AdjustedTimeout(LookupOptions{}) != LookupTimeout-time.Second {
			t.Fatalf("post-Driver cancellation = %T %v actions %#v timeout %v", err, err, base.Actions(), lookup.AdjustedTimeout(LookupOptions{}))
		}
	})

	t.Run("ordinary settle error is best effort", func(t *testing.T) {
		base := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: errors.New("not settled")}})
		state := batch4HExecutionState(base, newAdvancingClock(), &batch4HRecordingGenerator{value: "generated"})
		if _, err := dispatcher.execute(context.Background(), state, compiled, evaluated); err != nil {
			t.Fatalf("ordinary settle error propagated: %T %v", err, err)
		}
		if len(batch4AInputRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 1 {
			t.Fatalf("ordinary settle actions = %#v", base.Actions())
		}
	})

	t.Run("terminal settle error propagates", func(t *testing.T) {
		terminal := NewConfigurationError("settle configuration failed", nil)
		base := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: terminal}})
		state := batch4HExecutionState(base, newAdvancingClock(), &batch4HRecordingGenerator{value: "generated"})
		_, err := dispatcher.execute(context.Background(), state, compiled, evaluated)
		if classifyTerminalError(err) != terminalErrorConfiguration || len(batch4AInputRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 1 {
			t.Fatalf("terminal settle = %T %v actions %#v", err, err, base.Actions())
		}
	})
}

func TestInteractionBatch4HCompiledOwnershipAndConcurrentAllKindReuse(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	definitions := randomInputDefinitions()
	compiled := make([]compiledDispatch, len(definitions))
	for index, definition := range definitions {
		command := batch4HRandomBare(definition.keyword)
		if definition.variableLength {
			arguments := map[string]any{"length": int64(17)}
			command = batch4HRandomObject(definition.keyword, arguments)
			compiled[index], err = dispatcher.compile(context.Background(), compileContext{}, command)
			arguments["length"] = int64(1025)
		} else {
			compiled[index], err = dispatcher.compile(context.Background(), compileContext{}, command)
		}
		if err != nil {
			t.Fatal(err)
		}
	}

	const executions = 56
	var group sync.WaitGroup
	errs := make(chan error, executions)
	for index := range executions {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			definitionIndex := index % len(definitions)
			definition := definitions[definitionIndex]
			appID := fmt.Sprintf("com.example.batch4h.%02d", index)
			value := fmt.Sprintf("generated-%s-%02d", definition.kind, index)
			evaluated, evaluateErr := dispatcher.evaluate(context.Background(), batch2AEvaluation(nil, appID), compiled[definitionIndex])
			if evaluateErr != nil {
				errs <- evaluateErr
				return
			}
			generator := &batch4HSafeGenerator{value: value}
			base := batch4ADriver()
			driver := &batch4AMutatingRequestDriver{Driver: base}
			state := batch4HExecutionState(driver, newAdvancingClock(), generator)
			if _, executeErr := dispatcher.execute(context.Background(), state, compiled[definitionIndex], evaluated); executeErr != nil {
				errs <- executeErr
				return
			}
			requests := batch4AInputRequests(base.Actions())
			generatorRequests := generator.Requests()
			wantLength := inputNamedLengthSentinel
			if definition.variableLength {
				wantLength = 17
			}
			if len(requests) != 1 || requests[0].Text != value || !reflect.DeepEqual(requests[0].AppIDs, []string{appID}) ||
				len(generatorRequests) != 1 || generatorRequests[0] != (InputRequest{Kind: definition.kind, Length: wantLength}) {
				errs <- fmt.Errorf("execution %d ownership mismatch: input %#v generator %#v", index, requests, generatorRequests)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestInteractionBatch4HNestedAppIDPrecedenceAndRestoration(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/batch4h-root.yaml"
	childPath := "/workspace/batch4h-child.yaml"
	link := model.FileLink{Kind: model.FileLinkFlow, Path: "batch4h-child.yaml", ResolvedPath: childPath}
	runChild := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{"file": "batch4h-child.yaml"},
		Links:     []model.FileLink{link},
		Source:    model.SourceInfo{Path: rootPath},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config:   model.Config{AppID: "com.example.batch4h.root"},
		Commands: []model.Command{runChild, batch4HRandomBare(model.CommandInputRandomEmail)},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config:   model.Config{AppID: "com.example.batch4h.child"},
		Commands: []model.Command{batch4HRandomBare(model.CommandInputRandomText)},
	}
	specs := append([]handlerSpec{runFlowHandlerSpec()}, randomInputHandlerSpecs()...)
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry)
	if err != nil {
		t.Fatal(err)
	}
	root, exists := compiled.Flow(rootPath)
	if !exists {
		t.Fatal("compiled Batch 4H root is missing")
	}
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	base := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{
		{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
	})
	generator := &batch4HSequenceGenerator{values: []string{"child-generated", "root@example.test"}}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4h-nested", Driver: base, Clock: newAdvancingClock(), InputGenerator: generator,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch4h-nested/root-run-000001")
	wantDevice := []device.InputTextRequest{
		{Text: "child-generated", AppIDs: []string{"com.example.batch4h.child"}},
		{Text: "root@example.test", AppIDs: []string{"com.example.batch4h.root"}},
	}
	wantGenerator := []InputRequest{
		{Kind: InputText, Length: 8},
		{Kind: InputEmail, Length: -1},
	}
	if runErr != nil || result.Outcome() != Completed || !reflect.DeepEqual(batch4AInputRequests(base.Actions()), wantDevice) ||
		!reflect.DeepEqual(generator.Requests(), wantGenerator) {
		t.Fatalf("nested execution = outcome %s error %v device %#v generator %#v", result.Outcome(), runErr, batch4AInputRequests(base.Actions()), generator.Requests())
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].Command().Kind != model.CommandRunFlow ||
		commands[1].Command().Kind != model.CommandInputRandomText || commands[1].Depth() != 1 ||
		commands[2].Command().Kind != model.CommandInputRandomEmail || commands[2].Depth() != 0 {
		t.Fatalf("nested result ledger = %#v", commands)
	}
}

func TestInteractionBatch4HDuplicateRootsReuseOneCompiledProgramIndependently(t *testing.T) {
	t.Parallel()

	path := "/workspace/batch4h-duplicate.yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          path,
		Config:        model.Config{AppID: "com.example.batch4h.duplicate"},
		Commands:      []model.Command{batch4HRandomBare(model.CommandInputRandomColorName)},
	}
	program := singleCompileProgram(flow)
	program.roots = []string{path, path}
	program.graph.Roots = []string{path, path}
	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(context.Background(), program, registry)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(compiled.Roots(), []string{path, path}) {
		t.Fatalf("compiled roots = %#v", compiled.Roots())
	}
	root, exists := compiled.Flow(path)
	if !exists {
		t.Fatal("compiled duplicate root is missing")
	}
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	base := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{
		{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
	})
	generator := &batch4HSequenceGenerator{values: []string{"blue", "green"}}
	dependencies := Dependencies{
		ExecutionID: "batch4h-duplicate", Driver: base, Clock: newAdvancingClock(), InputGenerator: generator,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	for index := range compiled.Roots() {
		rootRunID := fmt.Sprintf("batch4h-duplicate/root-run-%06d", index+1)
		result, runErr := executeCompiledRootForRun(context.Background(), dependencies, root, rootRunID)
		if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID ||
			len(result.Commands()) != 1 || result.Commands()[0].Sequence() != 1 {
			t.Fatalf("duplicate root %d = result %#v error %v", index, result, runErr)
		}
	}
	wantDevice := []device.InputTextRequest{
		{Text: "blue", AppIDs: []string{"com.example.batch4h.duplicate"}},
		{Text: "green", AppIDs: []string{"com.example.batch4h.duplicate"}},
	}
	if !reflect.DeepEqual(batch4AInputRequests(base.Actions()), wantDevice) ||
		!reflect.DeepEqual(generator.Requests(), []InputRequest{{Kind: InputColorName, Length: -1}, {Kind: InputColorName, Length: -1}}) {
		t.Fatalf("duplicate-root requests = device %#v generator %#v", batch4AInputRequests(base.Actions()), generator.Requests())
	}
}

func TestInteractionBatch4HFailClosedInternalPayloadsAndBlankAppID(t *testing.T) {
	t.Parallel()

	command := batch4HRandomObject(model.CommandInputRandomText, map[string]any{"length": int64(8)})
	compiledValue, err := compileRandomInput(command)
	if err != nil {
		t.Fatal(err)
	}
	forged := compiledValue.(randomInputCompiled)
	forged.length = 9
	if _, err := evaluateRandomInput(context.Background(), batch2AEvaluation(nil, "com.example.batch4h"), command, forged); !isConfigurationError(err) {
		t.Fatalf("forged compiled payload error = %T %v", err, err)
	}
	if _, err := evaluateRandomInput(context.Background(), batch2AEvaluation(nil, " "), command, compiledValue); !isConfigurationError(err) {
		t.Fatalf("blank appId error = %T %v", err, err)
	}
	if _, err := evaluateRandomInput(context.Background(), batch2AEvaluation(nil, "com.example.batch4h"), command, struct{}{}); !isConfigurationError(err) {
		t.Fatalf("invalid compiled type error = %T %v", err, err)
	}

	base := batch4ADriver()
	generator := &batch4HRecordingGenerator{value: "must-not-run"}
	clock := newAdvancingClock()
	state := batch4HExecutionState(base, clock, generator)
	validCommand := batch4HRandomBare(model.CommandInputRandomEmail)
	validPlan := randomInputEvaluated{
		keyword: model.CommandInputRandomEmail,
		kind:    InputEmail,
		length:  -1,
		appID:   "com.example.batch4h",
	}
	invalidPlans := []evaluatedDispatch{
		{command: validCommand, value: struct{}{}},
		{command: validCommand, value: randomInputEvaluated{keyword: model.CommandInputRandomText, kind: InputEmail, length: -1, appID: "com.example.batch4h"}},
		{command: validCommand, value: randomInputEvaluated{keyword: model.CommandInputRandomEmail, kind: InputText, length: -1, appID: "com.example.batch4h"}},
		{command: validCommand, value: randomInputEvaluated{keyword: model.CommandInputRandomEmail, kind: InputEmail, length: 0, appID: "com.example.batch4h"}},
		{command: validCommand, value: randomInputEvaluated{keyword: model.CommandInputRandomEmail, kind: InputEmail, length: -1, appID: " "}},
	}
	for index, evaluated := range invalidPlans {
		if _, err := executeRandomInput(context.Background(), state, evaluated); !isConfigurationError(err) {
			t.Fatalf("invalid evaluated payload %d error = %T %v", index, err, err)
		}
	}
	var nilContext context.Context
	if _, err := executeRandomInput(nilContext, state, evaluatedDispatch{command: validCommand, value: validPlan}); !isConfigurationError(err) {
		t.Fatalf("nil context error = %T %v", err, err)
	}
	if _, err := executeRandomInput(context.Background(), nil, evaluatedDispatch{command: validCommand, value: validPlan}); !isConfigurationError(err) {
		t.Fatalf("nil state error = %T %v", err, err)
	}
	var typedNilDriver *enginetest.FakeDriver
	typedNilState := &executionState{dependencies: Dependencies{Driver: typedNilDriver, Clock: clock, InputGenerator: generator}}
	if _, err := executeRandomInput(context.Background(), typedNilState, evaluatedDispatch{command: validCommand, value: validPlan}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Driver error = %T %v", err, err)
	}
	var typedNilClock *batch1ATraceClock
	typedNilState = &executionState{dependencies: Dependencies{Driver: base, Clock: typedNilClock, InputGenerator: generator}}
	if _, err := executeRandomInput(context.Background(), typedNilState, evaluatedDispatch{command: validCommand, value: validPlan}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Clock error = %T %v", err, err)
	}
	if generator.calls != 0 || len(base.Actions()) != 0 {
		t.Fatalf("invalid payload effects = generator %d Driver %#v", generator.calls, base.Actions())
	}
}

type batch4HRecordingGenerator struct {
	calls   int
	request InputRequest
	value   string
	err     error
	trace   *[]string
}

func (generator *batch4HRecordingGenerator) Generate(_ context.Context, request InputRequest) (string, error) {
	generator.calls++
	generator.request = request
	if generator.trace != nil {
		*generator.trace = append(*generator.trace, "generator:"+string(request.Kind))
	}
	return generator.value, generator.err
}

type batch4HTraceInputDriver struct {
	device.Driver
	trace *[]string
}

func (driver *batch4HTraceInputDriver) InputText(ctx context.Context, request device.InputTextRequest) error {
	*driver.trace = append(*driver.trace, "driver:"+request.Text)
	return driver.Driver.InputText(ctx, request)
}

func batch4HRandomBare(keyword model.CommandKeyword) model.Command {
	return model.Command{Kind: keyword, Form: model.CommandFormScalar}
}

func batch4HRandomObject(keyword model.CommandKeyword, fields map[string]any) model.Command {
	return model.Command{Kind: keyword, Form: model.CommandFormObject, Arguments: fields}
}

func batch4HPreparedDispatch(
	t *testing.T,
	command model.Command,
	appID string,
) (dispatcher, compiledDispatch, evaluatedDispatch) {
	t.Helper()
	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, command)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := dispatcher.evaluate(context.Background(), batch2AEvaluation(nil, appID), compiled)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcher, compiled, evaluated
}

func batch4HExecutionState(driver device.Driver, clock Clock, generator InputGenerator) *executionState {
	lookup := NewElementLookup(driver, clock)
	return &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock, InputGenerator: generator},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
	}
}

type batch4HCancellingGenerator struct {
	cancel context.CancelFunc
	value  string
	calls  atomic.Int64
}

func (generator *batch4HCancellingGenerator) Generate(context.Context, InputRequest) (string, error) {
	generator.calls.Add(1)
	generator.cancel()
	return generator.value, nil
}

type batch4HPanicGenerator struct{}

func (batch4HPanicGenerator) Generate(context.Context, InputRequest) (string, error) {
	panic("Batch 4H generator panic")
}

type batch4HSafeGenerator struct {
	mu       sync.Mutex
	value    string
	requests []InputRequest
}

func (generator *batch4HSafeGenerator) Generate(_ context.Context, request InputRequest) (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.requests = append(generator.requests, request)
	return generator.value, nil
}

func (generator *batch4HSafeGenerator) Requests() []InputRequest {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return append([]InputRequest(nil), generator.requests...)
}

type batch4HSequenceGenerator struct {
	mu       sync.Mutex
	values   []string
	requests []InputRequest
}

func (generator *batch4HSequenceGenerator) Generate(_ context.Context, request InputRequest) (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	index := len(generator.requests)
	generator.requests = append(generator.requests, request)
	if index >= len(generator.values) {
		return "", errors.New("Batch 4H generator script exhausted")
	}
	return generator.values[index], nil
}

func (generator *batch4HSequenceGenerator) Requests() []InputRequest {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	return append([]InputRequest(nil), generator.requests...)
}
