package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/flow"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestExecutorRequiresLateEvaluationAndCapturesLogsAndSnapshot(t *testing.T) {
	t.Parallel()

	if _, err := newHandlerRegistry(handlerSpec{
		keyword: model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) { return "compiled", nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	}); err == nil {
		t.Fatal("newHandlerRegistry() error = nil, want missing evaluator rejection")
	}

	tests := []struct {
		name       string
		executeErr error
		want       Outcome
	}{
		{name: "success", want: Completed},
		{name: "failure", executeErr: NewOperationError("execute failed", nil), want: Failed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
			if err != nil {
				t.Fatalf("js.NewFactory() error: %v", err)
			}
			createdRuntime, err := factory.NewRuntime()
			if err != nil {
				t.Fatalf("factory.NewRuntime() error: %v", err)
			}
			tracked := &trackingLogRuntime{Runtime: createdRuntime}
			defer tracked.Close()

			core, err := newExecutorCore(coreDependencies(enginetest.NewFakeDriver(), newAdvancingClock()), handlerSpec{
				keyword: model.CommandAction, effectClass: EffectObserved,
				compile: pureCompiler(func(model.Command) (any, error) { return "compiled", nil }),
				evaluate: func(ctx context.Context, evaluation evaluationContext, original model.Command, compiled any) (evaluatedDispatch, error) {
					if compiled != "compiled" {
						return evaluatedDispatch{}, NewConfigurationError("compiled payload changed", nil)
					}
					value, interpolationErr := evaluation.Interpolate(ctx, "${console.log('during evaluate') || 'evaluated'}", nil)
					evaluated := cloneCommand(original)
					evaluated.Arguments = value
					label := "evaluated label"
					evaluated.Label = &label
					return evaluatedDispatch{command: evaluated, value: "late"}, interpolationErr
				},
				execute: func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
					if evaluated.value != "late" {
						return commandEffect{}, NewConfigurationError("evaluated payload changed", nil)
					}
					runtime, runtimeErr := state.jsRuntime()
					if runtimeErr != nil {
						return commandEffect{}, runtimeErr
					}
					if _, evalErr := runtime.Evaluate(ctx, js.EvalRequest{Script: "console.log('during execute')"}); evalErr != nil {
						return commandEffect{}, evalErr
					}
					return commandEffect{effectClass: EffectObserved}, test.executeErr
				},
			})
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return tracked, nil }

			result, executeErr := core.execute(context.Background(), model.Command{
				Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "original",
			}, 0)
			if !errors.Is(executeErr, test.executeErr) {
				t.Fatalf("execute() error = %v, want %v", executeErr, test.executeErr)
			}
			if result.Outcome() != test.want {
				t.Fatalf("execute() outcome = %q, want %q", result.Outcome(), test.want)
			}
			evaluated, exists := result.Metadata().EvaluatedCommand()
			if !exists || evaluated.Arguments != "evaluated" || evaluated.Label == nil || *evaluated.Label != "evaluated label" {
				t.Fatalf("evaluated command = %#v, want synchronized late snapshot", evaluated)
			}
			if got, want := result.Metadata().LogMessages(), []string{"during evaluate", "during execute"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("log messages = %#v, want %#v", got, want)
			}
			if got, want := tracked.sinkStates, []bool{true, false}; !reflect.DeepEqual(got, want) {
				t.Fatalf("SetLogSink states = %#v, want %#v", got, want)
			}
		})
	}
}

func TestExecutorCommandLogCapturePreservesConfiguredHostSink(t *testing.T) {
	tests := []struct {
		name         string
		evaluatorErr error
		executorErr  error
		wantLogs     []string
	}{
		{name: "success", wantLogs: []string{"during evaluate", "during execute"}},
		{name: "evaluator failure", evaluatorErr: NewConfigurationError("evaluation failed", nil), wantLogs: []string{"during evaluate"}},
		{name: "executor failure", executorErr: NewOperationError("execution failed", nil), wantLogs: []string{"during evaluate", "during execute"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hostLogs := make([]string, 0)
			factory, err := js.NewFactory(js.Config{
				Random: deterministicRandom{},
				LogSink: func(message string) {
					hostLogs = append(hostLogs, message)
				},
			})
			if err != nil {
				t.Fatalf("js.NewFactory() error: %v", err)
			}
			runtime, err := factory.NewRuntime()
			if err != nil {
				t.Fatalf("factory.NewRuntime() error: %v", err)
			}
			defer runtime.Close()

			core, err := newExecutorCore(coreDependencies(enginetest.NewFakeDriver(), newAdvancingClock()), handlerSpec{
				keyword: model.CommandAction, effectClass: EffectObserved,
				compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
				evaluate: func(ctx context.Context, evaluation evaluationContext, command model.Command, _ any) (evaluatedDispatch, error) {
					if _, evalErr := evaluation.Evaluate(ctx, js.EvalRequest{Script: "console.log('during evaluate')"}); evalErr != nil {
						return evaluatedDispatch{}, evalErr
					}
					return evaluatedDispatch{command: cloneCommand(command), value: struct{}{}}, test.evaluatorErr
				},
				execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
					jsRuntime, runtimeErr := state.jsRuntime()
					if runtimeErr != nil {
						return commandEffect{}, runtimeErr
					}
					if _, evalErr := jsRuntime.Evaluate(ctx, js.EvalRequest{Script: "console.log('during execute')"}); evalErr != nil {
						return commandEffect{}, evalErr
					}
					return commandEffect{effectClass: EffectObserved}, test.executorErr
				},
			})
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }

			result, _ := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
			if got := result.Metadata().LogMessages(); !reflect.DeepEqual(got, test.wantLogs) {
				t.Fatalf("command metadata logs = %#v, want %#v", got, test.wantLogs)
			}
			if !reflect.DeepEqual(hostLogs, test.wantLogs) {
				t.Fatalf("configured host logs = %#v, want %#v", hostLogs, test.wantLogs)
			}

			if _, err := runtime.Evaluate(context.Background(), js.EvalRequest{Script: "console.log('after command')"}); err != nil {
				t.Fatalf("post-command Evaluate() error: %v", err)
			}
			wantHost := append(append([]string(nil), test.wantLogs...), "after command")
			if !reflect.DeepEqual(hostLogs, wantHost) {
				t.Fatalf("post-command host logs = %#v, want %#v", hostLogs, wantHost)
			}
		})
	}
}

func TestExecuteRejectsUnsupportedParsedSelectorSemanticsBeforeEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "tap start end", body: "- tapOn:\n    text: Continue\n    start: 10%,10%\n    end: 90%,90%\n"},
		{name: "assert point", body: "- assertVisible:\n    text: Ready\n    point: 50%,50%\n"},
		{name: "assert start end", body: "- assertVisible:\n    text: Ready\n    start: 10%,10%\n    end: 90%,90%\n"},
		{name: "assert repeat", body: "- assertVisible:\n    text: Ready\n    repeat: 2\n"},
		{name: "assert delay", body: "- assertVisible:\n    text: Ready\n    delay: 100\n"},
		{name: "assert retry", body: "- assertVisible:\n    text: Ready\n    retryTapIfNoChange: true\n"},
		{name: "assert wait until visible", body: "- assertVisible:\n    text: Ready\n    waitUntilVisible: true\n"},
		{name: "assert settle timeout", body: "- assertVisible:\n    text: Ready\n    waitToSettleTimeoutMs: 500\n"},
		// CSS selectors compile and are resolved by capable drivers. See
		// TestCSSAloneIsATargetPredicate.
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := "appId: com.example.unsupported\n---\n" + test.body
			parsed, err := flow.ParseBytes("/workspace/unsupported.yaml", []byte(contents))
			if err != nil {
				t.Fatalf("flow.ParseBytes() rejected valid fixture before engine compilation: %v", err)
			}
			program := singleCompileProgram(parsed)

			driver := enginetest.NewFakeDriver()
			factory := &countingRuntimeFactory{}
			results, executeErr := Execute(context.Background(), program, Dependencies{
				ExecutionID: "unsupported-selectors",
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
			})
			if factory.calls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("Execute() reached effects: runtime calls=%d driver actions=%#v error=%v", factory.calls, driver.Actions(), executeErr)
			}
			if len(results) != 0 {
				t.Fatalf("Execute() results = %#v, want none before effects", results)
			}
			var configuration *ConfigurationError
			if !errors.As(executeErr, &configuration) {
				t.Fatalf("Execute() error = %T, want *ConfigurationError: %v", executeErr, executeErr)
			}
		})
	}
}

func TestExecuteRejectsTargetlessParsedSelectorsBeforeEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "tap empty", body: "- tapOn: {}\n"},
		{name: "tap label only", body: "- tapOn:\n    label: Report label\n"},
		{name: "tap optional only", body: "- tapOn:\n    optional: true\n"},
		{name: "tap settle only", body: "- tapOn:\n    waitToSettleTimeoutMs: 500\n"},
		{name: "tap index only", body: "- tapOn:\n    index: 0\n"},
		{name: "tap tolerance only", body: "- tapOn:\n    tolerance: 5\n"},
		{name: "tap empty traits", body: "- tapOn:\n    traits: []\n"},
		{name: "tap empty descendants", body: "- tapOn:\n    containsDescendants: []\n"},
		{name: "tap combined modifiers", body: "- tapOn:\n    label: Report label\n    optional: true\n    index: 0\n    waitToSettleTimeoutMs: 500\n"},
		{name: "tap targetless relative link", body: "- tapOn:\n    below:\n      label: Link label\n"},
		{name: "assert empty", body: "- assertVisible: {}\n"},
		{name: "assert label only", body: "- assertVisible:\n    label: Report label\n"},
		{name: "assert optional only", body: "- assertVisible:\n    optional: true\n"},
		{name: "assert settle only", body: "- assertVisible:\n    waitToSettleTimeoutMs: 500\n"},
		{name: "assert index only", body: "- assertVisible:\n    index: 0\n"},
		{name: "assert tolerance only", body: "- assertVisible:\n    tolerance: 5\n"},
		{name: "assert empty traits", body: "- assertVisible:\n    traits: []\n"},
		{name: "assert empty descendants", body: "- assertVisible:\n    containsDescendants: []\n"},
		{name: "assert combined modifiers", body: "- assertVisible:\n    label: Report label\n    optional: true\n    index: 0\n"},
		{name: "assert targetless descendant", body: "- assertVisible:\n    containsDescendants:\n      - optional: true\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents := "appId: com.example.targetless\n---\n" + test.body
			parsed, err := flow.ParseBytes("/workspace/targetless.yaml", []byte(contents))
			if err != nil {
				t.Fatalf("flow.ParseBytes() rejected valid fixture before engine compilation: %v", err)
			}
			driver := enginetest.NewFakeDriver()
			factory := &countingRuntimeFactory{}
			results, executeErr := Execute(context.Background(), singleCompileProgram(parsed), Dependencies{
				ExecutionID: "targetless-selectors",
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
			})
			if factory.calls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("Execute() reached effects: runtime calls=%d driver actions=%#v error=%v", factory.calls, driver.Actions(), executeErr)
			}
			if len(results) != 0 {
				t.Fatalf("Execute() results = %#v, want none before effects", results)
			}
			var configuration *ConfigurationError
			if !errors.As(executeErr, &configuration) {
				t.Fatalf("Execute() error = %T, want *ConfigurationError: %v", executeErr, executeErr)
			}
		})
	}
}

func TestSelectorTargetValidationAcceptsImplementedPredicates(t *testing.T) {
	t.Parallel()

	text := "Continue"
	id := "submit"
	width := 100
	height := 40
	tolerance := 5
	enabled := true
	selected := false
	checked := true
	focused := false
	index := "0"
	tests := []struct {
		name     string
		selector *model.ElementSelector
	}{
		{name: "text", selector: &model.ElementSelector{TextRegex: &text}},
		{name: "id", selector: &model.ElementSelector{IDRegex: &id}},
		{name: "size", selector: &model.ElementSelector{Size: &model.SizeSelector{Width: &width}}},
		{name: "size with tolerance", selector: &model.ElementSelector{Size: &model.SizeSelector{Height: &height, Tolerance: &tolerance}}},
		{name: "trait", selector: &model.ElementSelector{Traits: []model.ElementTrait{model.ElementTraitText}}},
		{name: "enabled state", selector: &model.ElementSelector{Enabled: &enabled}},
		{name: "selected state", selector: &model.ElementSelector{Selected: &selected}},
		{name: "checked state", selector: &model.ElementSelector{Checked: &checked}},
		{name: "focused state", selector: &model.ElementSelector{Focused: &focused}},
		{name: "below", selector: &model.ElementSelector{Below: &model.ElementSelector{TextRegex: &text}}},
		{name: "above", selector: &model.ElementSelector{Above: &model.ElementSelector{TextRegex: &text}}},
		{name: "left of", selector: &model.ElementSelector{LeftOf: &model.ElementSelector{TextRegex: &text}}},
		{name: "right of", selector: &model.ElementSelector{RightOf: &model.ElementSelector{TextRegex: &text}}},
		{name: "contains child", selector: &model.ElementSelector{ContainsChild: &model.ElementSelector{IDRegex: &id}}},
		{name: "contains descendants", selector: &model.ElementSelector{ContainsDescendants: []model.ElementSelector{{IDRegex: &id}}}},
		{name: "child of", selector: &model.ElementSelector{ChildOf: &model.ElementSelector{IDRegex: &id}}},
		{name: "indexed target", selector: &model.ElementSelector{TextRegex: &text, Index: &index}},
	}
	compilers := []struct {
		keyword model.CommandKeyword
		compile func(model.Command) (any, error)
	}{
		{keyword: model.CommandTapOn, compile: compileTapOn},
		{keyword: model.CommandAssertVisible, compile: compileAssertVisible},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, compiler := range compilers {
				arguments, valid := canonicalTypedSelector(test.selector)
				if !valid {
					t.Fatal("test selector could not be canonicalized")
				}
				if _, err := compiler.compile(model.Command{
					Kind: compiler.keyword, Form: model.CommandFormObject, Arguments: arguments, Selector: cloneSelector(test.selector),
				}); err != nil {
					t.Fatalf("%s compiler rejected implemented predicate: %v", compiler.keyword, err)
				}
			}
		})
	}
}

func TestExecuteCompilesCompleteProgramBeforeRuntimeOrDriverEffects(t *testing.T) {
	t.Parallel()

	first := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/first.yaml",
		Config:        model.Config{AppID: "com.example.first"},
		Commands: []model.Command{{
			Kind: model.CommandLaunchApp,
			Form: model.CommandFormScalar,
		}},
	}
	second := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/second.yaml",
		Commands: []model.Command{{
			// applyConfiguration is a structural/meta command with no runtime
			// handler, so it is the durable stand-in for an unregistered command.
			// Every executable keyword is registered.
			Kind: model.CommandApplyConfiguration,
			Form: model.CommandFormScalar,
		}},
	}
	program := &Program{
		roots: []string{first.Path, second.Path},
		paths: []string{first.Path, second.Path},
		flows: map[string]model.Flow{first.Path: first, second.Path: second},
		aliases: map[string]string{
			first.Path: first.Path, second.Path: second.Path,
		},
		graph: capability.Report{
			Roots: []string{first.Path, second.Path},
			Nodes: []capability.GraphNode{{Path: first.Path}, {Path: second.Path}},
		},
	}
	driver := enginetest.NewFakeDriver()
	factory := &countingRuntimeFactory{}

	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "compile-before-effects",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want unregistered-command failure")
	}
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("Execute() error = %T, want *ConfigurationError: %v", err, err)
	}
	if len(results) != 0 {
		t.Fatalf("Execute() results = %#v, want none before complete compilation", results)
	}
	if factory.calls != 0 {
		t.Fatalf("runtime factory calls = %d, want 0", factory.calls)
	}
	if actions := driver.Actions(); len(actions) != 0 {
		t.Fatalf("driver actions = %#v, want none", actions)
	}
}

func TestExecuteRejectsMissingExecutionIDBeforeEffects(t *testing.T) {
	t.Parallel()

	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/missing-execution-id.yaml",
		Config:        model.Config{AppID: "com.example.execution"},
	}
	driver := enginetest.NewFakeDriver()
	factory := &countingRuntimeFactory{}
	results, err := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		Driver: driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("Execute() error = %T %v, want missing execution ID *ConfigurationError", err, err)
	}
	if len(results) != 0 || factory.calls != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("missing execution ID reached effects: results=%#v runtime=%d driver=%#v", results, factory.calls, driver.Actions())
	}
}

func TestExecuteRootCorrelationIsScopedByInjectedExecutionID(t *testing.T) {
	t.Parallel()

	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/correlation.yaml",
		Config:        model.Config{AppID: "com.example.correlation"},
	}
	program := singleCompileProgram(flow)
	program.roots = []string{flow.Path, flow.Path}
	program.graph.Roots = append([]string(nil), program.roots...)
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	allIDs := make(map[string]struct{})
	for _, executionID := range []string{"execution-alpha", "execution-beta"} {
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
		}})
		events := make([]Event, 0, 4)
		results, executeErr := Execute(context.Background(), program, Dependencies{
			ExecutionID: executionID,
			Driver:      driver,
			Clock:       newAdvancingClock(),
			JSFactory:   factory,
			Controller:  NoopController{},
			Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})},
		})
		if executeErr != nil {
			t.Fatalf("Execute(%q) error: %v", executionID, executeErr)
		}
		want := []string{
			executionID + "/root-run-000001",
			executionID + "/root-run-000002",
		}
		if len(results) != 2 || len(events) != 4 {
			t.Fatalf("Execute(%q) results/events = %d/%d, want 2/4", executionID, len(results), len(events))
		}
		for index, result := range results {
			if result.RootRunID() != want[index] {
				t.Fatalf("Execute(%q) result %d root ID = %q, want %q", executionID, index, result.RootRunID(), want[index])
			}
			if _, collision := allIDs[result.RootRunID()]; collision {
				t.Fatalf("root run ID collided across Execute calls: %q", result.RootRunID())
			}
			allIDs[result.RootRunID()] = struct{}{}
		}
		for index, event := range events {
			if event.RootRunID() != want[index/2] {
				t.Fatalf("Execute(%q) event %d root ID = %q, want %q", executionID, index, event.RootRunID(), want[index/2])
			}
		}
	}
}

func TestExecuteOwnsAndSanitizesExternalEnvironmentBeforeRootExecution(t *testing.T) {
	t.Parallel()

	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/external-environment.yaml",
		Config: model.Config{Env: map[string]string{
			"COLLIDE":               "root",
			"FLOWBATON_SHARD_ID":    "root-id",
			"FLOWBATON_SHARD_INDEX": "root-index",
			"ROOT_ONLY":             "root",
		}},
	}
	external := map[string]string{
		"COLLIDE":               "external",
		"EXT_A":                 "external-a",
		"EXT_Z":                 "external-z",
		"FLOWBATON_SHARD_ID":    "external-id",
		"FLOWBATON_SHARD_INDEX": "external-index",
	}
	wantCaller := cloneStringMap(external)
	runtime := &sessionRuntime{}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
	}}})

	results, err := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID:         "external-environment",
		ExternalEnvironment: external,
		Driver:              driver,
		Clock:               newAdvancingClock(),
		JSFactory:           &sessionRuntimeFactory{runtime: runtime},
		Controller:          NoopController{},
	})
	if err != nil || len(results) != 1 || results[0].Outcome() != Completed {
		t.Fatalf("Execute() = results %#v error %v", results, err)
	}
	if !reflect.DeepEqual(external, wantCaller) {
		t.Fatalf("Execute() mutated caller environment: got %#v want %#v", external, wantCaller)
	}
	wantCalls := append(flowEnvCalls("/workspace/external-environment.yaml",
		"put:COLLIDE=external", "put:EXT_A=external-a", "put:EXT_Z=external-z",
		"put:COLLIDE=root", "put:FLOWBATON_SHARD_ID=root-id", "put:FLOWBATON_SHARD_INDEX=root-index", "put:ROOT_ONLY=root",
	), "pop")
	if got := runtime.EnvCalls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("environment calls = %#v, want %#v", got, wantCalls)
	}
}

func TestExecuteDuplicateRootsKeepOneExternalEnvironmentSnapshot(t *testing.T) {
	t.Parallel()

	flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/duplicate-environment.yaml"}
	program := singleCompileProgram(flow)
	program.roots = []string{flow.Path, flow.Path}
	program.graph.Roots = append([]string(nil), program.roots...)
	external := map[string]string{
		"A_FIRST":               "snapshot-a",
		"Z_LAST":                "snapshot-z",
		"FLOWBATON_SHARD_INDEX": "drop-me",
	}
	runtimes := []*sessionRuntime{{}, {}}
	factory := &queuedRuntimeFactory{runtimes: []js.Runtime{runtimes[0], runtimes[1]}}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{
		{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
		{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
	}})
	rootStarts := 0

	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID:         "duplicate-environment",
		ExternalEnvironment: external,
		Driver:              driver,
		Clock:               newAdvancingClock(),
		JSFactory:           factory,
		Controller:          NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventFlowStarted {
				rootStarts++
				if rootStarts == 1 {
					external["A_FIRST"] = "caller-mutated"
					external["LATE"] = "must-not-appear"
				}
			}
			return nil
		})},
	})
	if err != nil || len(results) != 2 {
		t.Fatalf("Execute() = results %d error %v", len(results), err)
	}
	wantCalls := append(flowEnvCalls("/workspace/duplicate-environment.yaml",
		"put:A_FIRST=snapshot-a", "put:Z_LAST=snapshot-z"), "pop")
	for index, runtime := range runtimes {
		if got := runtime.EnvCalls(); !reflect.DeepEqual(got, wantCalls) {
			t.Fatalf("root %d environment calls = %#v, want snapshot %#v", index+1, got, wantCalls)
		}
	}
	if external["A_FIRST"] != "caller-mutated" || external["LATE"] != "must-not-appear" {
		t.Fatalf("listener caller mutation did not occur: %#v", external)
	}
}

func TestExecuteOptionalAssertVisibleAbsenceWarnsWithAssertionError(t *testing.T) {
	t.Parallel()

	optional := true
	text := "${MISSING_TEXT}"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/optional.yaml",
		Config: model.Config{
			AppID: "com.example.optional",
			Env:   map[string]string{"MISSING_TEXT": "Missing"},
		},
		Commands: []model.Command{{
			Kind: model.CommandAssertVisible, Form: model.CommandFormObject,
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
	}}})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	results, err := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "optional-assertion",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome() != Warned {
		t.Fatalf("Execute() results = %#v, want one warned flow", results)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Warned {
		t.Fatalf("command results = %#v, want one warned command", commands)
	}
	var assertion *AssertionError
	if !errors.As(commands[0].ProductError(), &assertion) {
		t.Fatalf("command error = %T, want *AssertionError: %v", commands[0].ProductError(), commands[0].ProductError())
	}
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Selector == nil || evaluated.Selector.TextRegex == nil || *evaluated.Selector.TextRegex != "Missing" {
		t.Fatalf("evaluated selector = %#v, want interpolated missing target", evaluated.Selector)
	}
}

func TestExecuteOptionalTapDisconnectRemainsFailedAndSurfaced(t *testing.T) {
	t.Parallel()

	optional := true
	text := "Continue"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/disconnect.yaml",
		Config:        model.Config{AppID: "com.example.disconnect"},
		Commands: []model.Command{{
			Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: map[string]any{"text": text, "optional": optional},
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	disconnect := NewDeviceConnectionError("driver disconnected", nil)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: disconnect}},
	})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "optional-tap-disconnect",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	var connection *DeviceConnectionError
	if !errors.As(executeErr, &connection) {
		t.Fatalf("Execute() error = %T, want surfaced *DeviceConnectionError: %v", executeErr, executeErr)
	}
	if len(results) != 1 || results[0].Outcome() != Failed {
		t.Fatalf("Execute() results = %#v, want one failed flow", results)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Failed {
		t.Fatalf("command results = %#v, want optional disconnect to remain failed", commands)
	}
	if !errors.As(commands[0].ProductError(), &connection) {
		t.Fatalf("command error = %T, want *DeviceConnectionError", commands[0].ProductError())
	}
}

func TestExecuteOptionalTapMixedDisconnectRemainsFailedAndSurfaced(t *testing.T) {
	t.Parallel()

	optional := true
	text := "Continue"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/mixed-disconnect.yaml",
		Config:        model.Config{AppID: "com.example.mixed-disconnect"},
		Commands: []model.Command{{
			Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: map[string]any{"text": text, "optional": optional},
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	disconnect := NewDeviceConnectionError("driver disconnected", nil)
	mixed := NewOperationError("retry wrapper", disconnect)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: mixed}},
	})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "optional-tap-mixed-disconnect",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if executeErr != mixed {
		t.Fatalf("Execute() error = %T %v, want original mixed chain", executeErr, executeErr)
	}
	var connection *DeviceConnectionError
	var operation *OperationError
	if !errors.As(executeErr, &connection) || !errors.As(executeErr, &operation) {
		t.Fatalf("Execute() error lost mixed diagnostics: %T %v", executeErr, executeErr)
	}
	if IsRetryable(executeErr) || CanWarnWhenOptional(executeErr) {
		t.Fatalf("mixed disconnect remained retryable/warnable: %v", executeErr)
	}
	if len(results) != 1 || results[0].Outcome() != Failed || results[0].ProductError() != mixed {
		t.Fatalf("Execute() flow result = %#v, want one Failed flow with original chain", results)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Failed || commands[0].ProductError() != mixed {
		t.Fatalf("command results = %#v, want optional mixed disconnect to remain Failed", commands)
	}
}

func TestExecuteOptionalTapJoinedSkipAndDisconnectRemainsFailedAndSurfaced(t *testing.T) {
	t.Parallel()

	optional := true
	text := "Continue"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/joined-skip-disconnect.yaml",
		Config:        model.Config{AppID: "com.example.joined-skip-disconnect"},
		Commands: []model.Command{{
			Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: map[string]any{"text": text, "optional": optional},
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	skipped := NewCommandSkippedError("condition false", nil)
	disconnect := NewDeviceConnectionError("driver disconnected", nil)
	aggregate := errors.Join(skipped, disconnect)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: aggregate}},
	})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "optional-tap-joined-skip-disconnect",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if executeErr != aggregate {
		t.Fatalf("Execute() error = %T %v, want original aggregate", executeErr, executeErr)
	}
	var connection *DeviceConnectionError
	var commandSkipped *CommandSkippedError
	if !errors.As(executeErr, &connection) || !errors.As(executeErr, &commandSkipped) {
		t.Fatalf("Execute() error lost aggregate branches: %T %v", executeErr, executeErr)
	}
	if IsRetryable(executeErr) || CanWarnWhenOptional(executeErr) || IsCommandSkipped(executeErr) {
		t.Fatalf("joined skip/disconnect was downgraded: %v", executeErr)
	}
	if len(results) != 1 || results[0].Outcome() != Failed || results[0].ProductError() != aggregate {
		t.Fatalf("Execute() flow result = %#v, want one Failed flow with original aggregate", results)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Failed || commands[0].ProductError() != aggregate {
		t.Fatalf("command results = %#v, want optional joined disconnect to remain Failed", commands)
	}
}

func TestExecuteOptionalTapAsOnlyDisconnectRemainsFailedAndSurfaced(t *testing.T) {
	t.Parallel()

	driverError := &asOnlyError{target: asDeviceConnection}
	results, executeErr := executeOptionalTapDriverError(t, "optional-tap-as-only-disconnect", driverError)
	if executeErr != driverError {
		t.Fatalf("Execute() error type = %T, want original As-only device error", executeErr)
	}
	assertFailedExecutionError(t, results, executeErr)
}

func TestExecuteOptionalTapTypedNilDriverErrorFailsClosed(t *testing.T) {
	t.Parallel()

	var driverError *DeviceConnectionError
	results, executeErr := executeOptionalTapDriverError(t, "optional-tap-typed-nil", driverError)
	assertSafeConfigurationError(t, executeErr)
	assertFailedExecutionError(t, results, executeErr)
}

func TestExecuteTypedNilEvaluatorErrorIsSanitizedBeforeEveryTerminalConsumer(t *testing.T) {
	t.Parallel()

	var malformed *OperationError
	factory := newRuntimeFailureFactory(t, runtimeFailureConfig{interpolateErr: malformed})
	optional := true
	text := "Continue"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/typed-nil-evaluator.yaml",
		Config:        model.Config{AppID: "com.example.typed-nil-evaluator"},
		Commands: []model.Command{{
			Kind: model.CommandAssertVisible, Form: model.CommandFormObject, Arguments: text,
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
	}}})
	events := make([]Event, 0, 4)

	results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "typed-nil-evaluator",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	})
	safe := assertSafeConfigurationError(t, executeErr)
	assertPublicTerminalErrorIdentity(t, results, events, safe, 1)
}

func TestExecuteSanitizesMalformedSessionBootstrapError(t *testing.T) {
	t.Parallel()

	var malformed *OperationError
	factory := &runtimeFailureFactory{newRuntimeErr: malformed}
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/typed-nil-bootstrap.yaml",
		Config:        model.Config{AppID: "com.example.typed-nil-bootstrap"},
	}
	results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "typed-nil-bootstrap",
		Driver:      enginetest.NewFakeDriver(), Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	assertSafeConfigurationError(t, executeErr)
	if len(results) != 0 {
		t.Fatalf("Execute() results = %d, want none before session bootstrap", len(results))
	}
}

func TestExecuteSanitizesMalformedFlowCleanupErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config func(error) runtimeFailureConfig
	}{
		{name: "PopEnv", config: func(err error) runtimeFailureConfig { return runtimeFailureConfig{popEnvErr: err} }},
		{name: "runtime Close", config: func(err error) runtimeFailureConfig { return runtimeFailureConfig{closeErr: err} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var malformed *OperationError
			factory := newRuntimeFailureFactory(t, test.config(malformed))
			flow := model.Flow{
				SchemaVersion: model.ASTVersionV0,
				Path:          "/workspace/typed-nil-cleanup.yaml",
				Config:        model.Config{AppID: "com.example.typed-nil-cleanup"},
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
				Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
			}}})
			events := make([]Event, 0, 2)
			results, executeErr := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
				ExecutionID: "typed-nil-cleanup-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			})
			safe := assertSafeConfigurationError(t, executeErr)
			assertPublicTerminalErrorIdentity(t, results, events, safe, 0)
		})
	}
}

func assertPublicTerminalErrorIdentity(
	t *testing.T,
	results []FlowResult,
	events []Event,
	want error,
	wantCommands int,
) {
	t.Helper()

	if len(results) != 1 || results[0].Outcome() != Failed || results[0].ProductError() != want {
		t.Fatalf("flow terminal state = count %d outcome %v error %T", len(results), flowOutcomes(results), flowProductErrorType(results))
	}
	commands := results[0].Commands()
	if len(commands) != wantCommands {
		t.Fatalf("command results = %d, want %d", len(commands), wantCommands)
	}
	for _, command := range commands {
		if command.Outcome() != Failed || command.ProductError() != want {
			t.Fatalf("command terminal state = outcome %q error %T", command.Outcome(), command.ProductError())
		}
	}
	for _, event := range events {
		switch event.Kind() {
		case EventCommandStarted, EventFlowStarted:
			if event.ProductError() != nil {
				t.Fatalf("started event %q exposed terminal error type %T", event.Kind(), event.ProductError())
			}
		case EventCommandFinished, EventFlowFinished:
			if event.ProductError() != want {
				t.Fatalf("finished event %q error type = %T, want shared safe error", event.Kind(), event.ProductError())
			}
		}
	}
}

func flowProductErrorType(results []FlowResult) any {
	if len(results) == 0 {
		return nil
	}
	return results[0].ProductError()
}

func executeOptionalTapDriverError(t *testing.T, executionID string, driverError error) ([]FlowResult, error) {
	t.Helper()

	optional := true
	text := "Continue"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/" + executionID + ".yaml",
		Config:        model.Config{AppID: "com.example." + executionID},
		Commands: []model.Command{{
			Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: map[string]any{"text": text, "optional": optional},
			Selector: &model.ElementSelector{TextRegex: &text, Optional: &optional},
			Optional: &optional,
		}},
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: driverError}},
	})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	return Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: executionID,
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
}

func assertFailedExecutionError(t *testing.T, results []FlowResult, executeErr error) {
	t.Helper()

	if executeErr == nil {
		t.Fatal("Execute() error = nil, want surfaced failure")
	}
	if len(results) != 1 || results[0].Outcome() != Failed || results[0].ProductError() != executeErr {
		t.Fatalf("Execute() flow result count/outcome = %d/%v, want one Failed flow", len(results), flowOutcomes(results))
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Failed || commands[0].ProductError() != executeErr {
		t.Fatalf("command result count = %d, want one Failed command with surfaced error", len(commands))
	}
}

func flowOutcomes(results []FlowResult) []Outcome {
	outcomes := make([]Outcome, len(results))
	for index := range results {
		outcomes[index] = results[index].Outcome()
	}
	return outcomes
}

func TestNormalizeTerminalErrorPreservesControlAndTypedFailures(t *testing.T) {
	t.Parallel()

	configuration := NewConfigurationError("invalid driver request", nil)
	connection := NewDeviceConnectionError("driver disconnected", nil)
	tests := []struct {
		name          string
		input         error
		wantSame      bool
		wantFlowBaton bool
	}{
		{name: "context canceled", input: context.Canceled, wantSame: true},
		{name: "context deadline", input: context.DeadlineExceeded, wantSame: true},
		{name: "configuration", input: configuration, wantSame: true},
		{name: "device connection", input: connection, wantSame: true},
		{name: "ordinary driver failure", input: errors.New("driver rejected request"), wantFlowBaton: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeTerminalError("tapOn failed", test.input)
			if test.wantSame && got != test.input {
				t.Fatalf("normalizeTerminalError() = %T %v, want original %T %v", got, got, test.input, test.input)
			}
			var operation *OperationError
			if errors.As(got, &operation) != test.wantFlowBaton {
				t.Fatalf("normalizeTerminalError() OperationError = %v, want %v: %T %v", operation != nil, test.wantFlowBaton, got, got)
			}
			if test.wantFlowBaton && !errors.Is(got, test.input) {
				t.Fatalf("normalizeTerminalError() lost original cause: %v", got)
			}
		})
	}
}

type countingRuntimeFactory struct {
	calls int
}

func (factory *countingRuntimeFactory) NewRuntime() (js.Runtime, error) {
	factory.calls++
	return nil, errors.New("runtime creation must not be reached")
}

type runtimeFailureConfig struct {
	interpolateErr error
	popEnvErr      error
	closeErr       error
}

type queuedRuntimeFactory struct {
	runtimes []js.Runtime
	calls    int
}

func (factory *queuedRuntimeFactory) NewRuntime() (js.Runtime, error) {
	if factory.calls >= len(factory.runtimes) {
		return nil, errors.New("runtime queue exhausted")
	}
	runtime := factory.runtimes[factory.calls]
	factory.calls++
	return runtime, nil
}

type runtimeFailureFactory struct {
	base          js.Factory
	config        runtimeFailureConfig
	newRuntimeErr error
}

func newRuntimeFailureFactory(t *testing.T, config runtimeFailureConfig) *runtimeFailureFactory {
	t.Helper()
	base, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	return &runtimeFailureFactory{base: base, config: config}
}

func (factory *runtimeFailureFactory) NewRuntime() (js.Runtime, error) {
	if factory.newRuntimeErr != nil {
		return nil, factory.newRuntimeErr
	}
	runtime, err := factory.base.NewRuntime()
	if err != nil {
		return nil, err
	}
	return &runtimeFailureRuntime{Runtime: runtime, config: factory.config}, nil
}

type runtimeFailureRuntime struct {
	js.Runtime
	config runtimeFailureConfig
}

func (runtime *runtimeFailureRuntime) Interpolate(
	ctx context.Context,
	input string,
	env map[string]any,
) (string, error) {
	if runtime.config.interpolateErr != nil {
		return "", runtime.config.interpolateErr
	}
	return runtime.Runtime.Interpolate(ctx, input, env)
}

func (runtime *runtimeFailureRuntime) PopEnv() error {
	if err := runtime.Runtime.PopEnv(); err != nil {
		return err
	}
	return runtime.config.popEnvErr
}

func (runtime *runtimeFailureRuntime) Close() error {
	closeErr := runtime.Runtime.Close()
	if closeErr != nil {
		return closeErr
	}
	return runtime.config.closeErr
}

type advancingClock struct {
	now time.Time
}

func newAdvancingClock() *advancingClock {
	return &advancingClock{now: time.Unix(1_700_000_000, 0).UTC()}
}

func (clock *advancingClock) Now() time.Time {
	return clock.now
}

func (clock *advancingClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		clock.now = clock.now.Add(delay)
	}
	return nil
}

type deterministicRandom struct{}

func (deterministicRandom) Intn(int) int { return 0 }

func (deterministicRandom) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}

type trackingLogRuntime struct {
	js.Runtime
	sinkStates []bool
}

func (runtime *trackingLogRuntime) SetLogSink(sink func(string)) {
	runtime.sinkStates = append(runtime.sinkStates, sink != nil)
	runtime.Runtime.SetLogSink(sink)
}

func (runtime *trackingLogRuntime) PushLogSink(sink func(string)) func() {
	runtime.sinkStates = append(runtime.sinkStates, sink != nil)
	restore := runtime.Runtime.PushLogSink(sink)
	return func() {
		runtime.sinkStates = append(runtime.sinkStates, false)
		restore()
	}
}
