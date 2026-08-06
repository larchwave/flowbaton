package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestExecuteCompiledRootRunsLifecycleAndClosesOwnedRuntime(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var handlerOrder []string
	var eventKinds []EventKind
	flowFinishedSawClosed := false
	action := func(name string) compiledDispatch {
		command := model.Command{Kind: model.CommandAction}
		return compiledDispatch{
			command: command,
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAction, effectClass: EffectHostMutation,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					mu.Lock()
					handlerOrder = append(handlerOrder, name)
					mu.Unlock()
					return commandEffect{effectClass: EffectHostMutation}, nil
				},
			},
			value: name,
		}
	}
	root := &compiledFlow{
		path:       "/workspace/root.yaml",
		onStart:    []compiledDispatch{action("start")},
		body:       []compiledDispatch{action("body")},
		onComplete: []compiledDispatch{action("complete")},
	}
	runtime := &sessionRuntime{}
	factory := &sessionRuntimeFactory{runtime: runtime}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844},
	}}})
	dependencies := Dependencies{
		Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
		JSFactory: factory, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			mu.Lock()
			eventKinds = append(eventKinds, event.Kind())
			if event.Kind() == EventFlowFinished {
				_, _, closeCalls := runtime.Snapshot()
				flowFinishedSawClosed = closeCalls == 1
			}
			mu.Unlock()
			return nil
		})},
	}

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != nil {
		t.Fatalf("executeCompiledRoot() error: %v", err)
	}
	if result.Path() != root.path || result.Depth() != 0 || result.Outcome() != Completed {
		t.Fatalf("flow result = path %q depth %d outcome %q", result.Path(), result.Depth(), result.Outcome())
	}
	if got, want := handlerOrder, []string{"start", "body", "complete"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handler order = %#v, want %#v", got, want)
	}
	commands := result.Commands()
	if len(commands) != 3 {
		t.Fatalf("flow commands = %#v, want three", commands)
	}
	for index, command := range commands {
		if command.Sequence() != uint64(index+1) || command.Depth() != 0 || command.Outcome() != Completed {
			t.Fatalf("command %d = sequence %d depth %d outcome %q", index, command.Sequence(), command.Depth(), command.Outcome())
		}
	}
	wantEvents := []EventKind{
		EventFlowStarted,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventFlowFinished,
	}
	if !reflect.DeepEqual(eventKinds, wantEvents) {
		t.Fatalf("event kinds = %#v, want %#v", eventKinds, wantEvents)
	}
	if !flowFinishedSawClosed {
		t.Fatal("FlowFinished listener ran before root runtime close")
	}
	if got, want := runtime.EnvCalls(),
		append(flowEnvCalls("/workspace/root.yaml"), "pop"); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime env calls = %#v, want %#v", got, want)
	}
	_, _, closeCalls := runtime.Snapshot()
	if factory.Calls() != 1 || closeCalls != 1 {
		t.Fatalf("runtime ownership = factory %d close %d, want one/one", factory.Calls(), closeCalls)
	}
	actions := driver.Actions()
	if len(actions) != 1 || actions[0].Method != enginetest.MethodDeviceInfo {
		t.Fatalf("driver actions = %#v, want only DeviceInfo", actions)
	}
}

func TestExecuteCompiledRootRunsNestedPrecompiledFlowInSharedSession(t *testing.T) {
	t.Parallel()

	var nestedResult FlowResult
	var rootRuntime any
	var rootLookup *ElementLookup
	var childFinishedCloseCalls int
	child := &compiledFlow{
		path: "/workspace/child.yaml",
		config: model.Config{
			AppID: "com.example.child",
			Env:   map[string]string{"CHILD": "config"},
		},
	}
	child.body = []compiledDispatch{{
		command: model.Command{Kind: model.CommandAction},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			execute: func(_ context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				runtime, err := state.jsRuntime()
				if err != nil {
					return commandEffect{}, err
				}
				lookup, err := state.elementLookup()
				if err != nil {
					return commandEffect{}, err
				}
				if runtime != rootRuntime || lookup != rootLookup {
					return commandEffect{}, NewConfigurationError("nested flow did not share root state", nil)
				}
				appID, err := state.activeAppID()
				if err != nil || appID != "com.example.child" {
					return commandEffect{}, NewConfigurationError("nested flow app configuration was not active", err)
				}
				return commandEffect{effectClass: EffectHostMutation}, nil
			},
		},
		value: "child",
	}}
	root := &compiledFlow{
		path:   "/workspace/root.yaml",
		config: model.Config{AppID: "com.example.root"},
	}
	root.body = []compiledDispatch{{
		command: model.Command{Kind: model.CommandRunFlow},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				var err error
				rootRuntime, err = state.jsRuntime()
				if err != nil {
					return commandEffect{}, err
				}
				rootLookup, err = state.elementLookup()
				if err != nil {
					return commandEffect{}, err
				}
				if appID, appErr := state.activeAppID(); appErr != nil || appID != "com.example.root" {
					return commandEffect{}, NewConfigurationError("root app configuration was not active", appErr)
				}
				nestedResult, err = state.executeFlow(ctx, child, map[string]string{"CHILD": "overlay"})
				if err != nil {
					return commandEffect{}, err
				}
				if appID, appErr := state.activeAppID(); appErr != nil || appID != "com.example.root" {
					return commandEffect{}, NewConfigurationError("root app configuration was not restored", appErr)
				}
				return commandEffect{effectClass: EffectComposite}, nil
			},
		},
		value: child,
	}}

	runtime := &sessionRuntime{}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 1080, HeightGrid: 1920},
	}}})
	dependencies := Dependencies{
		Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
		JSFactory: &sessionRuntimeFactory{runtime: runtime}, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventFlowFinished && event.FlowPath() == child.path {
				_, _, childFinishedCloseCalls = runtime.Snapshot()
			}
			return nil
		})},
	}

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != nil {
		t.Fatalf("executeCompiledRoot() error: %v", err)
	}
	if result.Outcome() != Completed || nestedResult.Outcome() != Completed {
		t.Fatalf("outcomes = root %q nested %q", result.Outcome(), nestedResult.Outcome())
	}
	commands := result.Commands()
	if len(commands) != 2 || commands[0].Sequence() != 1 || commands[0].Depth() != 0 ||
		commands[1].Sequence() != 2 || commands[1].Depth() != 1 {
		t.Fatalf("root command ledger = %#v, want parent sequence 1 before child sequence 2", commands)
	}
	nestedCommands := nestedResult.Commands()
	if len(nestedCommands) != 1 || nestedCommands[0].Sequence() != 2 || nestedCommands[0].Depth() != 1 {
		t.Fatalf("nested command ledger = %#v, want only child sequence 2", nestedCommands)
	}
	if childFinishedCloseCalls != 0 {
		t.Fatalf("nested flow closed root runtime %d time(s)", childFinishedCloseCalls)
	}
	if got, want := runtime.EnvCalls(), concatEnvCalls(
		flowEnvCalls("/workspace/root.yaml"),
		// The overlay goes on first and the flow's own env last — a flow's
		// `env:` wins, see env_precedence_test.go.
		flowEnvCalls("/workspace/child.yaml", "put:CHILD=overlay", "put:CHILD=config"),
		[]string{"pop", "pop"},
	); !reflect.DeepEqual(got, want) {
		t.Fatalf("nested env calls = %#v, want %#v", got, want)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("root runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecuteCompiledRootScopesExternalEnvironmentAtRootOnly(t *testing.T) {
	t.Parallel()

	runtime := &sessionRuntime{}
	// COLLIDE resolves to the flow's own value: a flow's `env:` wins over what
	// it was handed. See env_precedence_test.go.
	rootEnvironment := withFileName("/workspace/root-env.yaml", map[string]string{
		"COLLIDE":     "root",
		"EXTERNAL":    "yes",
		"ROOT_CONFIG": "yes",
	})
	childEnvironment := withFileName("/workspace/child-env.yaml", map[string]string{
		"CHILD":       "yes",
		"COLLIDE":     "child",
		"EXTERNAL":    "yes",
		"OVERLAY":     "yes",
		"ROOT_CONFIG": "yes",
	})
	assertEnvironment := func(want map[string]string) error {
		if got := runtime.CurrentEnvironment(); !reflect.DeepEqual(got, want) {
			return NewConfigurationError("unexpected runtime environment", fmt.Errorf("got %#v want %#v", got, want))
		}
		return nil
	}
	child := &compiledFlow{
		path: "/workspace/child-env.yaml",
		config: model.Config{Env: map[string]string{
			"CHILD": "yes", "COLLIDE": "child",
		}},
	}
	child.body = []compiledDispatch{{
		command: model.Command{Kind: model.CommandAction},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectObserved,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectObserved}, assertEnvironment(childEnvironment)
			},
		},
	}}
	root := &compiledFlow{
		path: "/workspace/root-env.yaml",
		config: model.Config{Env: map[string]string{
			"COLLIDE": "root", "ROOT_CONFIG": "yes",
		}},
	}
	root.body = []compiledDispatch{{
		command: model.Command{Kind: model.CommandRunFlow},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				if err := assertEnvironment(rootEnvironment); err != nil {
					return commandEffect{}, err
				}
				_, err := state.executeFlow(ctx, child, map[string]string{
					"COLLIDE": "overlay", "OVERLAY": "yes",
				})
				if err != nil {
					return commandEffect{}, err
				}
				return commandEffect{effectClass: EffectComposite}, assertEnvironment(rootEnvironment)
			},
		},
	}}
	dependencies := flowExecutorDependencies(runtime, nil)
	dependencies.ExternalEnvironment = map[string]string{
		"COLLIDE": "external", "EXTERNAL": "yes",
	}

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != nil || result.Outcome() != Completed {
		t.Fatalf("executeCompiledRoot() = outcome %q error %v", result.Outcome(), err)
	}
	wantCalls := concatEnvCalls(
		flowEnvCalls("/workspace/root-env.yaml",
			"put:COLLIDE=external", "put:EXTERNAL=yes",
			"put:COLLIDE=root", "put:ROOT_CONFIG=yes"),
		flowEnvCalls("/workspace/child-env.yaml",
			"put:COLLIDE=overlay", "put:OVERLAY=yes",
			"put:CHILD=yes", "put:COLLIDE=child"),
		[]string{"pop", "pop"},
	)
	if got := runtime.EnvCalls(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("environment calls = %#v, want root-only external injection %#v", got, wantCalls)
	}
}

func TestExecuteCompiledRootRunsCompletionAfterStartFailureAndKeepsPrimaryError(t *testing.T) {
	t.Parallel()

	startCause := errors.New("start failed")
	completionCause := errors.New("completion failed")
	var handlerOrder []string
	command := func(name string, executeErr error) compiledDispatch {
		return compiledDispatch{
			command: model.Command{Kind: model.CommandAction},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAction, effectClass: EffectHostMutation,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					handlerOrder = append(handlerOrder, name)
					return commandEffect{effectClass: EffectHostMutation}, executeErr
				},
			},
			value: name,
		}
	}
	root := &compiledFlow{
		path:       "/workspace/root.yaml",
		onStart:    []compiledDispatch{command("start", NewDeviceConnectionError("start", startCause))},
		body:       []compiledDispatch{command("body", nil)},
		onComplete: []compiledDispatch{command("complete", NewConfigurationError("complete", completionCause))},
	}
	runtime := &sessionRuntime{}

	result, err := executeCompiledRoot(context.Background(), flowExecutorDependencies(runtime, nil), root)
	if !errors.Is(err, startCause) {
		t.Fatalf("executeCompiledRoot() error = %v, want start failure", err)
	}
	if result.Outcome() != Failed || !errors.Is(result.ProductError(), startCause) {
		t.Fatalf("flow result = outcome %q error %v, want primary start failure", result.Outcome(), result.ProductError())
	}
	if got, want := handlerOrder, []string{"start", "complete"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("handler order = %#v, want %#v", got, want)
	}
	commands := result.Commands()
	if len(commands) != 2 || commands[0].Outcome() != Failed || !errors.Is(commands[0].ProductError(), startCause) ||
		commands[1].Outcome() != Failed || !errors.Is(commands[1].ProductError(), completionCause) {
		t.Fatalf("flow commands = %#v, want failed start and completion evidence", commands)
	}
	if got, want := runtime.EnvCalls(),
		append(flowEnvCalls("/workspace/root.yaml"), "pop"); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime env calls = %#v, want %#v", got, want)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecuteCompiledRootRecordsCancelledCompletionWithoutRunningItsHandler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var completionHandlerCalls int
	root := &compiledFlow{path: "/workspace/root.yaml"}
	root.body = []compiledDispatch{{
		command: model.Command{Kind: model.CommandAction},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				cancel()
				return commandEffect{effectClass: EffectHostMutation}, nil
			},
		},
	}}
	root.onComplete = []compiledDispatch{{
		command: model.Command{Kind: model.CommandAction},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				completionHandlerCalls++
				return commandEffect{effectClass: EffectHostMutation}, nil
			},
		},
	}}
	runtime := &sessionRuntime{}

	result, err := executeCompiledRoot(ctx, flowExecutorDependencies(runtime, nil), root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeCompiledRoot() error = %v, want context cancellation", err)
	}
	if result.Outcome() != Cancelled || !errors.Is(result.ProductError(), context.Canceled) {
		t.Fatalf("flow result = outcome %q error %v, want cancellation", result.Outcome(), result.ProductError())
	}
	if completionHandlerCalls != 0 {
		t.Fatalf("completion handler calls = %d, want zero under cancelled context", completionHandlerCalls)
	}
	commands := result.Commands()
	if len(commands) != 2 || commands[0].Outcome() != Cancelled || commands[1].Outcome() != Cancelled {
		t.Fatalf("flow commands = %#v, want body and completion cancellation evidence", commands)
	}
	if got, want := runtime.EnvCalls(),
		append(flowEnvCalls("/workspace/root.yaml"), "pop"); !reflect.DeepEqual(got, want) {
		t.Fatalf("runtime env calls = %#v, want %#v", got, want)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecuteCompiledRootListenerCancellationPreventsFlowEffects(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var handlerCalls int
	var eventKinds []EventKind
	root := &compiledFlow{
		path: "/workspace/root.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandAction},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAction, effectClass: EffectHostMutation,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					handlerCalls++
					return commandEffect{effectClass: EffectHostMutation}, nil
				},
			},
		}},
	}
	runtime := &sessionRuntime{}
	listener := ListenerFunc(func(_ context.Context, event Event) error {
		eventKinds = append(eventKinds, event.Kind())
		if event.Kind() == EventFlowStarted {
			cancel()
		}
		return nil
	})

	result, err := executeCompiledRoot(ctx, flowExecutorDependencies(runtime, []Listener{listener}), root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("executeCompiledRoot() error = %v, want context cancellation", err)
	}
	if result.Outcome() != Cancelled || len(result.Commands()) != 0 {
		t.Fatalf("flow result = outcome %q commands %#v, want cancelled without commands", result.Outcome(), result.Commands())
	}
	if handlerCalls != 0 {
		t.Fatalf("handler calls = %d, want zero", handlerCalls)
	}
	if got, want := eventKinds, []EventKind{EventFlowStarted, EventFlowFinished}; !reflect.DeepEqual(got, want) {
		t.Fatalf("event kinds = %#v, want %#v", got, want)
	}
	if got := runtime.EnvCalls(); len(got) != 0 {
		t.Fatalf("runtime env calls = %#v, want none", got)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecuteCompiledRootReportsCloseFailureAndPreservesEarlierFailure(t *testing.T) {
	t.Parallel()

	t.Run("close failure is the root failure", func(t *testing.T) {
		closeCause := errors.New("runtime close failed")
		runtime := &sessionRuntime{closeErr: NewConfigurationError("close runtime", closeCause)}
		var finished Event
		listener := ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventFlowFinished {
				finished = event
			}
			return nil
		})

		result, err := executeCompiledRoot(
			context.Background(),
			flowExecutorDependencies(runtime, []Listener{listener}),
			&compiledFlow{path: "/workspace/root.yaml"},
		)
		if !errors.Is(err, closeCause) {
			t.Fatalf("executeCompiledRoot() error = %v, want close failure", err)
		}
		if result.Outcome() != Failed || !errors.Is(result.ProductError(), closeCause) {
			t.Fatalf("flow result = outcome %q error %v, want close failure", result.Outcome(), result.ProductError())
		}
		if finished.Kind() != EventFlowFinished || finished.Outcome() != Failed || !errors.Is(finished.ProductError(), closeCause) {
			t.Fatalf("FlowFinished = kind %q outcome %q error %v, want close failure", finished.Kind(), finished.Outcome(), finished.ProductError())
		}
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Fatalf("runtime Close calls = %d, want one", closeCalls)
		}
	})

	t.Run("command failure wins over close failure", func(t *testing.T) {
		commandCause := errors.New("command failed")
		closeCause := errors.New("runtime close failed")
		runtime := &sessionRuntime{closeErr: NewConfigurationError("close runtime", closeCause)}
		root := &compiledFlow{
			path: "/workspace/root.yaml",
			body: []compiledDispatch{{
				command: model.Command{Kind: model.CommandAction},
				spec: handlerSpec{
					evaluate: identityEvaluator,
					keyword:  model.CommandAction, effectClass: EffectHostMutation,
					execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
						return commandEffect{}, NewDeviceConnectionError("command", commandCause)
					},
				},
			}},
		}

		result, err := executeCompiledRoot(context.Background(), flowExecutorDependencies(runtime, nil), root)
		if !errors.Is(err, commandCause) || errors.Is(err, closeCause) {
			t.Fatalf("executeCompiledRoot() error = %v, want command failure only", err)
		}
		if result.Outcome() != Failed || !errors.Is(result.ProductError(), commandCause) {
			t.Fatalf("flow result = outcome %q error %v, want command failure", result.Outcome(), result.ProductError())
		}
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Fatalf("runtime Close calls = %d, want one despite earlier failure", closeCalls)
		}
	})
}

func TestExecuteCompiledRootWarnsForOptionalNestedFailureAtParentDepth(t *testing.T) {
	t.Parallel()

	childCause := errors.New("child assertion failed")
	var childResult FlowResult
	child := &compiledFlow{
		path: "/workspace/child.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandAssertVisible},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAssertVisible, effectClass: EffectObserved,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{}, NewAssertionError("child assertion", childCause)
				},
			},
		}},
	}
	root := &compiledFlow{
		path: "/workspace/root.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandRunFlow, Optional: pointer(true)},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandRunFlow, effectClass: EffectComposite,
				execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
					var err error
					childResult, err = state.executeFlow(ctx, child, nil)
					return commandEffect{effectClass: EffectComposite}, err
				},
			},
		}},
	}

	result, err := executeCompiledRoot(context.Background(), flowExecutorDependencies(&sessionRuntime{}, nil), root)
	if err != nil {
		t.Fatalf("executeCompiledRoot() error: %v", err)
	}
	if childResult.Outcome() != Failed || !errors.Is(childResult.ProductError(), childCause) {
		t.Fatalf("child result = outcome %q error %v, want failed child", childResult.Outcome(), childResult.ProductError())
	}
	if result.Outcome() != Warned || result.ProductError() != nil {
		t.Fatalf("root result = outcome %q error %v, want warned without surfaced error", result.Outcome(), result.ProductError())
	}
	commands := result.Commands()
	if len(commands) != 2 || commands[0].Depth() != 0 || commands[0].Outcome() != Warned ||
		commands[1].Depth() != 1 || commands[1].Outcome() != Failed {
		t.Fatalf("root commands = %#v, want warned parent and failed child evidence", commands)
	}
}

func TestExecutionSessionRejectsConcurrentRootReentry(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	root := &compiledFlow{
		path: "/workspace/root.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandAction},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAction, effectClass: EffectHostMutation,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					close(entered)
					<-release
					return commandEffect{effectClass: EffectHostMutation}, nil
				},
			},
		}},
	}
	session, err := newExecutionSession(context.Background(), flowExecutorDependencies(&sessionRuntime{}, nil), root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	type runResult struct {
		result FlowResult
		err    error
	}
	firstDone := make(chan runResult, 1)
	go func() {
		result, runErr := session.run(context.Background())
		firstDone <- runResult{result: result, err: runErr}
	}()
	<-entered

	if _, secondErr := session.run(context.Background()); secondErr == nil {
		t.Fatal("concurrent session.run() error = nil")
	} else {
		var configuration *ConfigurationError
		if !errors.As(secondErr, &configuration) {
			t.Fatalf("concurrent session.run() error = %T %v, want ConfigurationError", secondErr, secondErr)
		}
	}
	close(release)
	first := <-firstDone
	if first.err != nil || first.result.Outcome() != Completed {
		t.Fatalf("first session.run() = outcome %q error %v", first.result.Outcome(), first.err)
	}
	if _, repeatedErr := session.run(context.Background()); repeatedErr == nil {
		t.Fatal("repeated session.run() error = nil")
	}
}

func TestExecuteCompiledRootUsesFreshStateForEachRootSession(t *testing.T) {
	t.Parallel()

	root := &compiledFlow{
		path: "/workspace/root.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandAction},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandAction, effectClass: EffectHostMutation,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{effectClass: EffectHostMutation}, nil
				},
			},
		}},
	}
	runtimes := []*sessionRuntime{{}, {}}
	for index, runtime := range runtimes {
		result, err := executeCompiledRoot(context.Background(), flowExecutorDependencies(runtime, nil), root)
		if err != nil {
			t.Fatalf("root session %d error: %v", index, err)
		}
		commands := result.Commands()
		if len(commands) != 1 || commands[0].Sequence() != 1 || commands[0].Depth() != 0 {
			t.Fatalf("root session %d commands = %#v, want fresh sequence one", index, commands)
		}
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Fatalf("root session %d Close calls = %d, want one", index, closeCalls)
		}
	}
}

func flowExecutorDependencies(runtime *sessionRuntime, listeners []Listener) Dependencies {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844},
	}}})
	return Dependencies{
		Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
		JSFactory: &sessionRuntimeFactory{runtime: runtime}, Controller: NoopController{},
		Listeners: listeners,
	}
}
