package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestCompileRunFlowRequiresExactlyOnePrecompiledSource(t *testing.T) {
	t.Parallel()

	child := &compiledFlow{path: "/workspace/child.yaml"}
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: child.path,
	}
	tests := []struct {
		name             string
		command          model.Command
		wantError        bool
		wantRequireCalls int
		wantSource       runFlowSource
	}{
		{
			name: "scalar file",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: "child.yaml", Links: []model.FileLink{link},
			},
			wantRequireCalls: 1, wantSource: runFlowLinkedSource,
		},
		{
			name: "object file",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"file": "child.yaml"},
				Links:     []model.FileLink{link},
			},
			wantRequireCalls: 1, wantSource: runFlowLinkedSource,
		},
		{
			name: "inline commands",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": []any{}},
				Source:    model.SourceInfo{Path: "/workspace/root.yaml", Start: model.Position{Line: 7, Column: 3, Offset: 40}},
			},
			wantSource: runFlowInlineSource,
		},
		{
			name: "dual source",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"file": "child.yaml", "commands": []any{}},
				Links:     []model.FileLink{link},
			},
			wantError: true,
		},
		{
			name: "zero source",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"env": map[string]any{"ROLE": "reader"}},
			},
			wantError: true,
		},
		{
			name: "bare command",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormScalar,
			},
			wantError: true,
		},
		{
			name: "scalar file with forged typed condition",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: "child.yaml", Links: []model.FileLink{link},
				Condition: &model.Condition{},
			},
			wantError: true,
		},
		{
			name: "file link mismatch",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"file": "child.yaml"},
				Links:     []model.FileLink{{Kind: model.FileLinkFlow, Path: "other.yaml"}},
			},
			wantError: true,
		},
		{
			name: "inline foreign link",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": []any{}},
				Links:     []model.FileLink{link},
			},
			wantError: true,
		},
		{
			name: "inline typed sequence mismatch",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": []any{}},
				Children:  []model.Command{runFlowActionCommand("forged")},
			},
			wantError: true,
		},
		{
			name: "malformed when snapshot",
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": []any{}, "when": "not-an-object"},
				Condition: &model.Condition{},
			},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry, err := newHandlerRegistry(runFlowHandlerSpec())
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			requireCalls := 0
			compiled, compileErr := newDispatcher(registry).compile(
				context.Background(),
				compileContext{
					containingFlow: "/workspace/root.yaml",
					requireFlow: func(got model.FileLink) (*compiledFlow, error) {
						requireCalls++
						if got != link {
							return nil, errors.New("unexpected link")
						}
						return child, nil
					},
				},
				test.command,
			)
			if test.wantError {
				if compileErr == nil {
					t.Fatalf("compile() = %#v, nil; want configuration error", compiled)
				}
				var configuration *ConfigurationError
				if !errors.As(compileErr, &configuration) {
					t.Fatalf("compile() error = %T %v, want *ConfigurationError", compileErr, compileErr)
				}
			} else if compileErr != nil {
				t.Fatalf("compile() error = %v", compileErr)
			} else {
				payload, ok := compiled.value.(runFlowCompiled)
				if !ok || payload.source != test.wantSource {
					t.Fatalf("compiled payload = %#v, want source %d", compiled.value, test.wantSource)
				}
				if test.wantSource == runFlowLinkedSource && payload.linked != child {
					t.Fatalf("compiled linked child = %p, want retained %p", payload.linked, child)
				}
			}
			if requireCalls != test.wantRequireCalls {
				t.Fatalf("RequireFlow calls = %d, want %d", requireCalls, test.wantRequireCalls)
			}
		})
	}
}

func TestRunFlowConditionReevaluatesBeforeOpeningInlineScope(t *testing.T) {
	t.Parallel()

	conditionSource := "${READY}"
	label := "conditional child"
	optional := false
	command := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"commands": []any{},
			"when":     map[string]any{"true": conditionSource},
			"label":    label,
			"optional": optional,
		},
		Condition: &model.Condition{ScriptCondition: &conditionSource},
		Label:     &label, Optional: &optional,
		Source: model.SourceInfo{
			Path: "/workspace/root.yaml", Start: model.Position{Line: 9, Column: 3, Offset: 72},
		},
	}
	runtime := conditionRuntime(t, false)
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		runFlowHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
	core.state.currentConfigFn = func() (model.Config, error) {
		return model.Config{AppID: "com.example.parent", Env: map[string]string{"PARENT": "kept"}}, nil
	}
	var calls int
	var capturedFlow *compiledFlow
	var capturedOverlay map[string]string
	core.state.executeCompiledFlow = func(
		_ context.Context,
		flow *compiledFlow,
		overlay map[string]string,
		depth int,
	) (FlowResult, error) {
		calls++
		capturedFlow = flow
		capturedOverlay = cloneStringMap(overlay)
		if depth != 1 {
			return FlowResult{}, NewConfigurationError("unexpected inline depth", nil)
		}
		return FlowResult{}, nil
	}
	compiled, err := core.dispatcher.compile(
		context.Background(), compileContext{containingFlow: "/workspace/root.yaml"}, command,
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}

	first, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || first.Outcome() != Skipped || calls != 0 {
		t.Fatalf("false runFlow = outcome %q error %v nested calls %d", first.Outcome(), err, calls)
	}
	firstEvaluated, exists := first.Metadata().EvaluatedCommand()
	if !exists || firstEvaluated.Condition == nil || firstEvaluated.Condition.ScriptCondition == nil ||
		*firstEvaluated.Condition.ScriptCondition != "false" {
		t.Fatalf("false evaluated command = %#v, want evaluated condition snapshot", firstEvaluated)
	}
	if err := runtime.PutEnv("READY", "false"); err != nil {
		t.Fatalf("runtime.PutEnv(false string) error = %v", err)
	}
	second, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || second.Outcome() != Completed || calls != 1 {
		t.Fatalf("nonempty false-string runFlow = outcome %q error %v nested calls %d", second.Outcome(), err, calls)
	}
	secondEvaluated, exists := second.Metadata().EvaluatedCommand()
	if !exists || secondEvaluated.Condition == nil || secondEvaluated.Condition.ScriptCondition == nil ||
		*secondEvaluated.Condition.ScriptCondition != "false" {
		t.Fatalf("false-string evaluated command = %#v, want truthy string snapshot", secondEvaluated)
	}
	if err := runtime.PutEnv("READY", true); err != nil {
		t.Fatalf("runtime.PutEnv(true) error = %v", err)
	}
	third, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || third.Outcome() != Completed || calls != 2 {
		t.Fatalf("true runFlow = outcome %q error %v nested calls %d", third.Outcome(), err, calls)
	}
	if capturedFlow == nil || capturedFlow.path != "/workspace/root.yaml#runFlow:inline:9:3:72" ||
		capturedFlow.config.AppID != "com.example.parent" || capturedFlow.config.Env != nil ||
		capturedFlow.config.OnFlowStart != nil || capturedFlow.config.OnFlowComplete != nil {
		t.Fatalf("inline flow = %#v, want deterministic isolated parent-config clone", capturedFlow)
	}
	if !reflect.DeepEqual(capturedOverlay, map[string]string(nil)) {
		t.Fatalf("inline overlay = %#v, want nil", capturedOverlay)
	}
	if *command.Condition.ScriptCondition != conditionSource || command.Arguments.(map[string]any)["label"] != label {
		t.Fatalf("source command mutated = %#v", command)
	}
}

func TestRunFlowBigIntConditionTruthiness(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		script     string
		wantCalls  int
		wantResult Outcome
	}{
		{name: "direct zero", script: "0n", wantResult: Skipped},
		{name: "direct one", script: "1n", wantCalls: 1, wantResult: Completed},
		{name: "exact zero", script: "${0n}", wantResult: Skipped},
		{name: "exact one", script: "${1n}", wantCalls: 1, wantResult: Completed},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := test.script
			command := model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"commands": []any{},
					"when":     map[string]any{"true": script},
				},
				Condition: &model.Condition{ScriptCondition: &script},
				Source:    model.SourceInfo{Path: "/workspace/bigint-run-flow.yaml"},
			}
			runtime := conditionRuntime(t, true)
			core, err := newExecutorCore(
				coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
				runFlowHandlerSpec(),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
			core.state.currentConfigFn = func() (model.Config, error) {
				return model.Config{AppID: "com.example.bigint"}, nil
			}
			calls := 0
			core.state.executeCompiledFlow = func(context.Context, *compiledFlow, map[string]string, int) (FlowResult, error) {
				calls++
				return FlowResult{}, nil
			}
			compiled, err := core.dispatcher.compile(
				context.Background(),
				compileContext{containingFlow: command.Source.Path},
				command,
			)
			if err != nil {
				t.Fatalf("compile(runFlow) error = %v", err)
			}
			result, err := core.executeCompiled(context.Background(), compiled, 0)
			if err != nil || result.Outcome() != test.wantResult || calls != test.wantCalls {
				t.Fatalf("runFlow %q = outcome %q calls %d error %v, want %q/%d", script, result.Outcome(), calls, err, test.wantResult, test.wantCalls)
			}
		})
	}
}

func TestRunFlowEnvironmentInterpolationUsesOnlyRuntime(t *testing.T) {
	t.Parallel()

	runtime := conditionRuntime(t, true)
	if err := runtime.PutEnv("PARENT", "inherited"); err != nil {
		t.Fatalf("runtime.PutEnv(PARENT) error = %v", err)
	}
	child := &compiledFlow{path: "/workspace/child.yaml"}
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: child.path,
	}
	command := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"file": "child.yaml",
			"env":  map[string]any{"VALUE": "${PARENT}"},
		},
		Links: []model.FileLink{link},
	}
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		runFlowHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	compiled, err := core.dispatcher.compile(context.Background(), compileContext{
		containingFlow: "/workspace/root.yaml",
		requireFlow: func(model.FileLink) (*compiledFlow, error) {
			return child, nil
		},
	}, command)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	runtimeCalls := 0
	core.state.runtimeFn = func() (js.Runtime, error) {
		runtimeCalls++
		return runtime, nil
	}
	unrelatedConfigFailure := errors.New("current config must not be acquired")
	configCalls := 0
	core.state.currentConfigFn = func() (model.Config, error) {
		configCalls++
		return model.Config{}, unrelatedConfigFailure
	}
	var overlay map[string]string
	core.state.executeCompiledFlow = func(
		_ context.Context,
		got *compiledFlow,
		gotOverlay map[string]string,
		depth int,
	) (FlowResult, error) {
		if got != child || depth != 1 {
			return FlowResult{}, NewConfigurationError("linked flow identity or depth changed", nil)
		}
		overlay = cloneStringMap(gotOverlay)
		return FlowResult{}, nil
	}

	result, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || result.Outcome() != Completed {
		t.Fatalf("executeCompiled() = outcome %q error %v", result.Outcome(), err)
	}
	if !reflect.DeepEqual(overlay, map[string]string{"VALUE": "inherited"}) {
		t.Fatalf("linked overlay = %#v", overlay)
	}
	if runtimeCalls != 1 || configCalls != 0 {
		t.Fatalf("service calls = runtime %d config %d, want 1/0", runtimeCalls, configCalls)
	}
	evaluated, exists := result.Metadata().EvaluatedCommand()
	if !exists || evaluated.Arguments.(map[string]any)["env"].(map[string]any)["VALUE"] != "inherited" {
		t.Fatalf("evaluated environment snapshot = %#v", evaluated)
	}
	if command.Arguments.(map[string]any)["env"].(map[string]any)["VALUE"] != "${PARENT}" {
		t.Fatalf("source environment mutated = %#v", command.Arguments)
	}
}

func TestRunFlowPlatformMismatchSkipsRuntimeConfigAndNestedScope(t *testing.T) {
	t.Parallel()

	wantPlatform := model.PlatformIOS
	command := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"commands": []any{},
			"when":     map[string]any{"platform": string(wantPlatform)},
		},
		Condition: &model.Condition{Platform: &wantPlatform},
	}
	driver := conditionDriver("android")
	core, err := newExecutorCore(
		coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0))),
		runFlowHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	runtimeCalls := 0
	configCalls := 0
	nestedCalls := 0
	core.state.runtimeFn = func() (js.Runtime, error) {
		runtimeCalls++
		return nil, errors.New("runtime must be skipped")
	}
	core.state.currentConfigFn = func() (model.Config, error) {
		configCalls++
		return model.Config{}, errors.New("config must be skipped")
	}
	lookup := NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	core.state.lookupFn = func() (*ElementLookup, error) { return lookup, nil }
	core.state.executeCompiledFlow = func(context.Context, *compiledFlow, map[string]string, int) (FlowResult, error) {
		nestedCalls++
		return FlowResult{}, nil
	}

	result, err := core.execute(context.Background(), command, 0)
	if err != nil || result.Outcome() != Skipped {
		t.Fatalf("platform mismatch = outcome %q error %v", result.Outcome(), err)
	}
	if runtimeCalls != 0 || configCalls != 0 || nestedCalls != 0 {
		t.Fatalf("short-circuit calls = runtime %d config %d nested %d, want 0/0/0", runtimeCalls, configCalls, nestedCalls)
	}
}

func TestHandlerEvaluatedSnapshotRejectsKeywordMutationOnSuccess(t *testing.T) {
	t.Parallel()

	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		handlerSpec{
			keyword: model.CommandAction, effectClass: EffectHostMutation,
			compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			evaluate: identityEvaluator,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				changed := model.Command{Kind: model.CommandBack}
				return commandEffect{
					effectClass: EffectHostMutation, evaluatedCommand: &changed,
				}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	result, err := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) || result.ProductError() != err {
		t.Fatalf("keyword mutation = result error %T %v returned %T %v", result.ProductError(), result.ProductError(), err, err)
	}
	evaluated, exists := result.Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandAction {
		t.Fatalf("keyword mutation metadata = %#v, want evaluator fallback", evaluated)
	}
}

func TestHandlerEvaluatedSnapshotCannotReplaceExistingProductError(t *testing.T) {
	t.Parallel()

	productFailure := NewOperationError("original product failure", nil)
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		handlerSpec{
			keyword: model.CommandAction, effectClass: EffectHostMutation,
			compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			evaluate: identityEvaluator,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				changed := model.Command{Kind: model.CommandBack}
				return commandEffect{
					effectClass: EffectHostMutation, evaluatedCommand: &changed,
				}, productFailure
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	result, err := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
	if err != productFailure || result.ProductError() != productFailure {
		t.Fatalf("mismatched snapshot error = returned %T %v product %T %v, want original identity", err, err, result.ProductError(), result.ProductError())
	}
	evaluated, exists := result.Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandAction {
		t.Fatalf("mismatched snapshot metadata = %#v, want evaluator fallback", evaluated)
	}
}

func TestRunFlowLinkedExecutesPrecompiledLifecycleAndRestoresParent(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/root.yaml"
	childPath := "/workspace/child.yaml"
	childLink := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: childPath,
	}
	label := "linked child"
	optional := false
	runChild := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"file": "child.yaml",
			"env": map[string]any{
				"COLLIDE": "overlay", "OVERLAY": "explicit",
			},
			"label": label, "optional": optional,
		},
		Links:    []model.FileLink{childLink},
		Label:    &label,
		Optional: &optional,
		Source: model.SourceInfo{
			Path: rootPath, Start: model.Position{Line: 8, Column: 3, Offset: 80},
		},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config: model.Config{
			AppID:          "com.example.root",
			Env:            map[string]string{"COLLIDE": "root", "ROOT": "yes"},
			OnFlowStart:    []model.Command{runFlowActionCommand("parent-start")},
			OnFlowComplete: []model.Command{runFlowActionCommand("parent-complete")},
		},
		Commands: []model.Command{runChild, runFlowActionCommand("parent-after")},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config: model.Config{
			AppID:          "com.example.child",
			Env:            map[string]string{"CHILD": "yes", "COLLIDE": "child"},
			OnFlowStart:    []model.Command{runFlowActionCommand("child-start")},
			OnFlowComplete: []model.Command{runFlowActionCommand("child-complete")},
		},
		Commands: []model.Command{runFlowActionCommand("child-body")},
	}
	program := runFlowLinkedProgram(rootFlow, childFlow, childLink)

	type observation struct {
		name  string
		depth int
		appID string
		env   map[string]string
	}
	observations := make([]observation, 0, 6)
	registry, err := newHandlerRegistry(
		runFlowHandlerSpec(),
		runFlowActionHandlerSpec(func(name string, state *executionState) error {
			config, err := state.activeConfig()
			if err != nil {
				return err
			}
			runtime, err := state.jsRuntime()
			if err != nil {
				return err
			}
			session, ok := runtime.(interface{ CurrentEnvironment() map[string]string })
			if !ok {
				return NewConfigurationError("unexpected runFlow test runtime", nil)
			}
			observations = append(observations, observation{
				name: name, depth: state.depth, appID: config.AppID, env: session.CurrentEnvironment(),
			})
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(context.Background(), program, registry)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, exists := compiled.Flow(rootPath)
	if !exists {
		t.Fatal("compiled root is missing")
	}
	child, exists := compiled.Flow(childPath)
	if !exists {
		t.Fatal("compiled child is missing")
	}
	payload, ok := root.body[0].value.(runFlowCompiled)
	if !ok || payload.linked != child {
		t.Fatalf("linked payload = %#v, want retained precompiled child %p", root.body[0].value, child)
	}

	var flowEvents []string
	runtimeState := &sessionRuntime{}
	runtime := &runFlowPassthroughRuntime{sessionRuntime: runtimeState}
	dependencies := flowExecutorDependencies(runtimeState, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		switch event.Kind() {
		case EventFlowStarted, EventFlowFinished:
			flowEvents = append(flowEvents, string(event.Kind())+":"+event.FlowPath())
		}
		return nil
	})})
	dependencies.JSFactory = &sessionRuntimeFactory{runtime: runtime}
	dependencies.ExternalEnvironment = map[string]string{"COLLIDE": "external", "EXTERNAL": "yes"}
	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != nil || result.Outcome() != Completed {
		t.Fatalf("executeCompiledRoot() = outcome %q error %v", result.Outcome(), err)
	}

	wantObservations := []observation{
		// COLLIDE resolves to each flow's OWN value — a flow's `env:` wins over
		// the operator's -e and over a parent's runFlow env alike.
		{name: "parent-start", depth: 0, appID: "com.example.root", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"})},
		{name: "child-start", depth: 1, appID: "com.example.child", env: withFileName(childPath, map[string]string{"CHILD": "yes", "COLLIDE": "child", "EXTERNAL": "yes", "OVERLAY": "explicit", "ROOT": "yes"})},
		{name: "child-body", depth: 1, appID: "com.example.child", env: withFileName(childPath, map[string]string{"CHILD": "yes", "COLLIDE": "child", "EXTERNAL": "yes", "OVERLAY": "explicit", "ROOT": "yes"})},
		{name: "child-complete", depth: 1, appID: "com.example.child", env: withFileName(childPath, map[string]string{"CHILD": "yes", "COLLIDE": "child", "EXTERNAL": "yes", "OVERLAY": "explicit", "ROOT": "yes"})},
		{name: "parent-after", depth: 0, appID: "com.example.root", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"})},
		{name: "parent-complete", depth: 0, appID: "com.example.root", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"})},
	}
	if !reflect.DeepEqual(observations, wantObservations) {
		t.Fatalf("lifecycle observations = %#v, want %#v", observations, wantObservations)
	}
	wantFlowEvents := []string{
		"FlowStarted:" + rootPath,
		"FlowStarted:" + childPath,
		"FlowFinished:" + childPath,
		"FlowFinished:" + rootPath,
	}
	if !reflect.DeepEqual(flowEvents, wantFlowEvents) {
		t.Fatalf("flow events = %#v, want %#v", flowEvents, wantFlowEvents)
	}
	wantEnvCalls := concatEnvCalls(
		flowEnvCalls(rootPath, "put:COLLIDE=external", "put:EXTERNAL=yes",
			"put:COLLIDE=root", "put:ROOT=yes"),
		flowEnvCalls(childPath, "put:COLLIDE=overlay", "put:OVERLAY=explicit",
			"put:CHILD=yes", "put:COLLIDE=child"),
		[]string{"pop", "pop"},
	)
	if got := runtimeState.EnvCalls(); !reflect.DeepEqual(got, wantEnvCalls) {
		t.Fatalf("environment calls = %#v, want %#v", got, wantEnvCalls)
	}
	commands := result.Commands()
	wantDepths := []int{0, 0, 1, 1, 1, 0, 0}
	if len(commands) != len(wantDepths) {
		t.Fatalf("ledger commands = %#v, want %d", commands, len(wantDepths))
	}
	for index, wantDepth := range wantDepths {
		if commands[index].Sequence() != uint64(index+1) || commands[index].Depth() != wantDepth || commands[index].Outcome() != Completed {
			t.Fatalf("ledger command %d = seq %d depth %d outcome %q", index, commands[index].Sequence(), commands[index].Depth(), commands[index].Outcome())
		}
	}
	outerEvaluated, exists := commands[1].Metadata().EvaluatedCommand()
	if !exists || outerEvaluated.Label == nil || *outerEvaluated.Label != label || outerEvaluated.Optional == nil || *outerEvaluated.Optional {
		t.Fatalf("outer evaluated metadata = %#v", outerEvaluated)
	}
	if runChild.Arguments.(map[string]any)["env"].(map[string]any)["COLLIDE"] != "overlay" || runChild.Label == nil || *runChild.Label != label {
		t.Fatalf("source command mutated = %#v", runChild)
	}
}

func TestRunFlowInlineUsesSyntheticScopeWithoutReplayingParentHooks(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/inline-root.yaml"
	label := "inline child"
	optional := true
	inline := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"commands": []any{
				map[string]any{"action": "inline-first"},
				map[string]any{"action": "inline-second"},
			},
			"env":   map[string]any{"COLLIDE": "inline", "INLINE": "yes"},
			"label": label, "optional": optional,
		},
		Children: []model.Command{runFlowActionCommand("inline-first"), runFlowActionCommand("inline-second")},
		Label:    &label,
		Optional: &optional,
		Source: model.SourceInfo{
			Path: rootPath, Start: model.Position{Line: 12, Column: 3, Offset: 140},
		},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config: model.Config{
			Name: "parent", AppID: "com.example.inline", Tags: []string{"smoke"},
			Env:            map[string]string{"COLLIDE": "root", "ROOT": "yes"},
			OnFlowStart:    []model.Command{runFlowActionCommand("parent-start")},
			OnFlowComplete: []model.Command{runFlowActionCommand("parent-complete")},
			Properties:     map[string]string{"owner": "parent"},
		},
		Commands: []model.Command{inline, runFlowActionCommand("parent-after")},
	}

	type observation struct {
		name          string
		depth         int
		appID         string
		env           map[string]string
		configEnv     map[string]string
		startHooks    int
		completeHooks int
	}
	observations := make([]observation, 0, 5)
	registry, err := newHandlerRegistry(
		runFlowHandlerSpec(),
		runFlowActionHandlerSpec(func(name string, state *executionState) error {
			config, err := state.activeConfig()
			if err != nil {
				return err
			}
			runtime, err := state.jsRuntime()
			if err != nil {
				return err
			}
			session := runtime.(interface{ CurrentEnvironment() map[string]string })
			observations = append(observations, observation{
				name: name, depth: state.depth, appID: config.AppID,
				env: session.CurrentEnvironment(), configEnv: cloneStringMap(config.Env),
				startHooks: len(config.OnFlowStart), completeHooks: len(config.OnFlowComplete),
			})
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(context.Background(), singleCompileProgram(rootFlow), registry)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, _ := compiled.Flow(rootPath)
	payload, ok := root.body[0].value.(runFlowCompiled)
	wantInlinePath := rootPath + "#runFlow:inline:12:3:140"
	if !ok || payload.source != runFlowInlineSource || payload.inlinePath != wantInlinePath || len(root.body[0].children) != 2 {
		t.Fatalf("inline compiled payload = %#v children %d", root.body[0].value, len(root.body[0].children))
	}

	var flowEvents []string
	runtimeState := &sessionRuntime{}
	runtime := &runFlowPassthroughRuntime{sessionRuntime: runtimeState}
	dependencies := flowExecutorDependencies(runtimeState, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventFlowStarted || event.Kind() == EventFlowFinished {
			flowEvents = append(flowEvents, string(event.Kind())+":"+event.FlowPath())
		}
		return nil
	})})
	dependencies.JSFactory = &sessionRuntimeFactory{runtime: runtime}
	dependencies.ExternalEnvironment = map[string]string{"COLLIDE": "external", "EXTERNAL": "yes"}
	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != nil || result.Outcome() != Completed {
		t.Fatalf("executeCompiledRoot() = outcome %q error %v", result.Outcome(), err)
	}

	wantObservations := []observation{
		// COLLIDE resolves to the flow's own value, see env_precedence_test.go.
		{name: "parent-start", depth: 0, appID: "com.example.inline", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"}), configEnv: map[string]string{"COLLIDE": "root", "ROOT": "yes"}, startHooks: 1, completeHooks: 1},
		{name: "inline-first", depth: 1, appID: "com.example.inline", env: withFileName(rootPath, map[string]string{"COLLIDE": "inline", "EXTERNAL": "yes", "INLINE": "yes", "ROOT": "yes"})},
		{name: "inline-second", depth: 1, appID: "com.example.inline", env: withFileName(rootPath, map[string]string{"COLLIDE": "inline", "EXTERNAL": "yes", "INLINE": "yes", "ROOT": "yes"})},
		{name: "parent-after", depth: 0, appID: "com.example.inline", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"}), configEnv: map[string]string{"COLLIDE": "root", "ROOT": "yes"}, startHooks: 1, completeHooks: 1},
		{name: "parent-complete", depth: 0, appID: "com.example.inline", env: withFileName(rootPath, map[string]string{"COLLIDE": "root", "EXTERNAL": "yes", "ROOT": "yes"}), configEnv: map[string]string{"COLLIDE": "root", "ROOT": "yes"}, startHooks: 1, completeHooks: 1},
	}
	if !reflect.DeepEqual(observations, wantObservations) {
		t.Fatalf("inline observations = %#v, want %#v", observations, wantObservations)
	}
	wantFlowEvents := []string{
		"FlowStarted:" + rootPath,
		"FlowStarted:" + wantInlinePath,
		"FlowFinished:" + wantInlinePath,
		"FlowFinished:" + rootPath,
	}
	if !reflect.DeepEqual(flowEvents, wantFlowEvents) {
		t.Fatalf("inline flow events = %#v, want %#v", flowEvents, wantFlowEvents)
	}
	// An inline scope has no filename, so it retains its parent's
	// FLOWBATON_FILENAME value.
	wantEnvCalls := concatEnvCalls(
		flowEnvCalls(rootPath, "put:COLLIDE=external", "put:EXTERNAL=yes",
			"put:COLLIDE=root", "put:ROOT=yes"),
		flowEnvCalls(wantInlinePath, "put:COLLIDE=inline", "put:INLINE=yes"),
		[]string{"pop", "pop"},
	)
	if got := runtimeState.EnvCalls(); !reflect.DeepEqual(got, wantEnvCalls) {
		t.Fatalf("inline environment calls = %#v, want %#v", got, wantEnvCalls)
	}
	commands := result.Commands()
	wantDepths := []int{0, 0, 1, 1, 0, 0}
	if len(commands) != len(wantDepths) {
		t.Fatalf("inline ledger = %#v", commands)
	}
	for index, wantDepth := range wantDepths {
		if commands[index].Sequence() != uint64(index+1) || commands[index].Depth() != wantDepth || commands[index].Outcome() != Completed {
			t.Fatalf("inline ledger command %d = seq %d depth %d outcome %q", index, commands[index].Sequence(), commands[index].Depth(), commands[index].Outcome())
		}
	}
	outerEvaluated, exists := commands[1].Metadata().EvaluatedCommand()
	if !exists || outerEvaluated.Label == nil || *outerEvaluated.Label != label || outerEvaluated.Optional == nil || !*outerEvaluated.Optional {
		t.Fatalf("inline outer evaluated metadata = %#v", outerEvaluated)
	}
	if inline.Children[0].Arguments != "inline-first" || inline.Arguments.(map[string]any)["env"].(map[string]any)["COLLIDE"] != "inline" {
		t.Fatalf("inline source mutated = %#v", inline)
	}
}

func TestRunFlowNestedFailureStaysRawAndRootResolvesOuterOnce(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/failure-root.yaml"
	childPath := "/workspace/failure-child.yaml"
	link := model.FileLink{Kind: model.FileLinkFlow, Path: "failure-child.yaml", ResolvedPath: childPath}
	runChild := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{"file": link.Path},
		Links:     []model.FileLink{link},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config:   model.Config{AppID: "com.example.failure"},
		Commands: []model.Command{runChild, runFlowActionCommand("root-after")},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config: model.Config{
			AppID:          "com.example.failure-child",
			OnFlowStart:    []model.Command{runFlowActionCommand("child-start")},
			OnFlowComplete: []model.Command{runFlowActionCommand("child-complete")},
		},
		Commands: []model.Command{
			runFlowActionCommand("child-fail"),
			runFlowActionCommand("child-must-not-run"),
		},
	}
	failure := NewOperationError("nested child failed", nil)
	order := make([]string, 0, 5)
	registry, err := newHandlerRegistry(
		runFlowHandlerSpec(),
		runFlowActionHandlerSpec(func(name string, _ *executionState) error {
			order = append(order, name)
			if name == "child-fail" {
				return failure
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, _ := compiled.Flow(rootPath)
	runtime := &runFlowPassthroughRuntime{sessionRuntime: &sessionRuntime{}}
	dependencies := flowExecutorDependencies(runtime.sessionRuntime, nil)
	dependencies.JSFactory = &sessionRuntimeFactory{runtime: runtime}
	resolverCalls := 0
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if result.Command().Kind != model.CommandRunFlow || result.Depth() != 0 || result.ProductError() != failure {
			t.Errorf("resolver received non-outer result = kind %q depth %d error %T %v", result.Command().Kind, result.Depth(), result.ProductError(), result.ProductError())
		}
		return FailureDecisionContinue
	})

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != failure || result.ProductError() != failure || result.Outcome() != Failed {
		t.Fatalf("failure result = outcome %q returned %T %v product %T %v", result.Outcome(), err, err, result.ProductError(), result.ProductError())
	}
	if resolverCalls != 1 {
		t.Fatalf("FailureResolver calls = %d, want outer exactly once", resolverCalls)
	}
	if want := []string{"child-start", "child-fail", "child-complete", "root-after"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("failure lifecycle order = %#v, want %#v", order, want)
	}
	commands := result.Commands()
	if len(commands) != 5 {
		t.Fatalf("failure ledger = %#v, want outer/start/fail/complete/root-after", commands)
	}
	wantKinds := []model.CommandKeyword{
		model.CommandRunFlow, model.CommandAction, model.CommandAction, model.CommandAction, model.CommandAction,
	}
	wantDepths := []int{0, 1, 1, 1, 0}
	wantOutcomes := []Outcome{Failed, Completed, Failed, Completed, Completed}
	for index := range commands {
		if commands[index].Sequence() != uint64(index+1) || commands[index].Command().Kind != wantKinds[index] ||
			commands[index].Depth() != wantDepths[index] || commands[index].Outcome() != wantOutcomes[index] {
			t.Fatalf("failure ledger command %d = seq %d kind %q depth %d outcome %q", index, commands[index].Sequence(), commands[index].Command().Kind, commands[index].Depth(), commands[index].Outcome())
		}
	}
	if commands[0].ProductError() != failure || commands[2].ProductError() != failure {
		t.Fatalf("failure identity changed = outer %T %v child %T %v", commands[0].ProductError(), commands[0].ProductError(), commands[2].ProductError(), commands[2].ProductError())
	}
}

func TestRunFlowPropagatesCancellationAndDeviceIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		failure           error
		cancelDuringChild bool
		wantResolverCalls int
		wantCompletionRun bool
	}{
		{name: "cancellation", failure: context.Canceled, cancelDuringChild: true},
		{name: "device disconnect", failure: NewDeviceConnectionError("device disconnected", nil), wantCompletionRun: true},
		{name: "infrastructure", failure: NewConfigurationError("host infrastructure failed", nil), wantResolverCalls: 1, wantCompletionRun: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rootPath := "/workspace/identity-root.yaml"
			childPath := "/workspace/identity-child.yaml"
			link := model.FileLink{Kind: model.FileLinkFlow, Path: "identity-child.yaml", ResolvedPath: childPath}
			rootFlow := model.Flow{
				SchemaVersion: model.ASTVersionV0, Path: rootPath,
				Config: model.Config{AppID: "com.example.identity"},
				Commands: []model.Command{
					{
						Kind: model.CommandRunFlow, Form: model.CommandFormObject,
						Arguments: map[string]any{"file": link.Path}, Links: []model.FileLink{link},
					},
					runFlowActionCommand("root-must-not-run"),
				},
			}
			childFlow := model.Flow{
				SchemaVersion: model.ASTVersionV0, Path: childPath,
				Config: model.Config{
					AppID:          "com.example.identity-child",
					OnFlowComplete: []model.Command{runFlowActionCommand("child-complete")},
				},
				Commands: []model.Command{runFlowActionCommand("child-fail")},
			}
			ctx := context.Background()
			cancel := func() {}
			if test.cancelDuringChild {
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
			}
			order := make([]string, 0, 3)
			registry, err := newHandlerRegistry(
				runFlowHandlerSpec(),
				runFlowActionHandlerSpec(func(name string, _ *executionState) error {
					order = append(order, name)
					if name == "child-fail" {
						if test.cancelDuringChild {
							cancel()
						}
						return test.failure
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			compiled, err := compileProgram(ctx, runFlowLinkedProgram(rootFlow, childFlow, link), registry)
			if err != nil {
				t.Fatalf("compileProgram() error = %v", err)
			}
			root, _ := compiled.Flow(rootPath)
			runtimeState := &sessionRuntime{}
			runtime := &runFlowPassthroughRuntime{sessionRuntime: runtimeState}
			var finishedFlowErrors []error
			dependencies := flowExecutorDependencies(runtimeState, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventFlowFinished {
					finishedFlowErrors = append(finishedFlowErrors, event.ProductError())
				}
				return nil
			})})
			dependencies.JSFactory = &sessionRuntimeFactory{runtime: runtime}
			resolverCalls := 0
			dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
				resolverCalls++
				if result.Command().Kind != model.CommandRunFlow || result.ProductError() != test.failure {
					t.Errorf("resolver result = kind %q error %T %v", result.Command().Kind, result.ProductError(), result.ProductError())
				}
				return FailureDecisionFail
			})

			result, err := executeCompiledRoot(ctx, dependencies, root)
			if err != test.failure || result.ProductError() != test.failure {
				t.Fatalf("identity = returned %T %v product %T %v, want %T identity", err, err, result.ProductError(), result.ProductError(), test.failure)
			}
			if resolverCalls != test.wantResolverCalls {
				t.Fatalf("FailureResolver calls = %d, want %d", resolverCalls, test.wantResolverCalls)
			}
			wantOrder := []string{"child-fail"}
			if test.wantCompletionRun {
				wantOrder = append(wantOrder, "child-complete")
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("terminal lifecycle order = %#v, want %#v", order, wantOrder)
			}
			commands := result.Commands()
			completionError := error(nil)
			if test.cancelDuringChild {
				completionError = test.failure
			}
			if len(commands) != 3 || commands[0].Command().Kind != model.CommandRunFlow || commands[0].ProductError() != test.failure ||
				commands[1].ProductError() != test.failure || commands[2].ProductError() != completionError {
				t.Fatalf("terminal ledger = %#v, want outer/child identity and completion error %T %v", commands, completionError, completionError)
			}
			if len(finishedFlowErrors) != 2 || finishedFlowErrors[0] != test.failure || finishedFlowErrors[1] != test.failure {
				t.Fatalf("finished flow errors = %#v, want child/root identity", finishedFlowErrors)
			}
			if got, want := runtimeState.EnvCalls(), concatEnvCalls(
				flowEnvCalls(rootPath), flowEnvCalls(childPath),
				[]string{"pop", "pop"}); !reflect.DeepEqual(got, want) {
				t.Fatalf("terminal environment cleanup = %#v, want %#v", got, want)
			}
		})
	}
}

type runFlowPassthroughRuntime struct {
	*sessionRuntime
}

func (runtime *runFlowPassthroughRuntime) Interpolate(
	_ context.Context,
	input string,
	_ map[string]any,
) (string, error) {
	return input, nil
}

func runFlowActionCommand(name string) model.Command {
	return model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: name}
}

func runFlowActionHandlerSpec(execute func(string, *executionState) error) handlerSpec {
	return handlerSpec{
		keyword: model.CommandAction, effectClass: EffectHostMutation,
		compile: pureCompiler(func(command model.Command) (any, error) {
			return decodeString(command)
		}),
		evaluate: identityEvaluator,
		execute: func(_ context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
			name, ok := evaluated.value.(string)
			if !ok {
				return commandEffect{}, NewConfigurationError("runFlow test action payload is invalid", nil)
			}
			return commandEffect{effectClass: EffectHostMutation}, execute(name, state)
		},
	}
}

func runFlowLinkedProgram(
	root model.Flow,
	child model.Flow,
	link model.FileLink,
) *Program {
	return &Program{
		roots: []string{root.Path},
		paths: []string{root.Path, child.Path},
		flows: map[string]model.Flow{root.Path: cloneFlow(root), child.Path: cloneFlow(child)},
		aliases: map[string]string{
			root.Path: root.Path, child.Path: child.Path, link.ResolvedPath: child.Path,
		},
		graph: capability.Report{
			Roots: []string{root.Path},
			Nodes: []capability.GraphNode{{Path: root.Path}, {Path: child.Path}},
			Edges: []capability.GraphEdge{{
				From: root.Path, To: child.Path, Kind: model.FileLinkFlow,
			}},
		},
	}
}
