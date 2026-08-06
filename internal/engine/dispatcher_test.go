package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestExecutorCompilesEntireSequenceBeforeAnyHandlerEffect(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	clock := enginetest.NewFakeClock(time.Unix(100, 0))
	compileFailure := errors.New("second command does not compile")
	var order []string
	hostMutations := 0
	core, err := newExecutorCore(coreDependencies(driver, clock),
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile: pureCompiler(func(model.Command) (any, error) {
				order = append(order, "compile launch")
				return device.LaunchAppRequest{AppID: "com.example"}, nil
			}),
			execute: func(ctx context.Context, state *executionState, compiled evaluatedDispatch) (commandEffect, error) {
				order = append(order, "execute launch")
				hostMutations++
				err := state.dependencies.Driver.LaunchApp(ctx, compiled.value.(device.LaunchAppRequest))
				return commandEffect{effectClass: EffectDeviceMutation}, err
			},
		},
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandStopApp, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile: pureCompiler(func(model.Command) (any, error) {
				order = append(order, "compile stop")
				return nil, compileFailure
			}),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				order = append(order, "execute stop")
				hostMutations++
				return commandEffect{effectClass: EffectDeviceMutation}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}

	results, err := core.executeSequence(context.Background(), []model.Command{
		{Kind: model.CommandLaunchApp},
		{Kind: model.CommandStopApp},
	}, 0)
	if !errors.Is(err, compileFailure) {
		t.Fatalf("executeSequence() error = %v, want compile failure", err)
	}
	if len(results) != 0 {
		t.Fatalf("executeSequence() results = %#v, want none before compilation completes", results)
	}
	if got, want := order, []string{"compile launch", "compile stop"}; !equalStrings(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	if hostMutations != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("compilation failure allowed effects: host=%d driver=%v", hostMutations, driver.Actions())
	}
}

func TestExecutorCompilesInlineDescendantsBeforeParentEffects(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	clock := enginetest.NewFakeClock(time.Unix(200, 0))
	childFailure := errors.New("inline child does not compile")
	parentCompiles, firstChildCompiles, failingChildCompiles := 0, 0, 0
	parentEffects, childEffects, listenerEffects := 0, 0, 0
	dependencies := coreDependencies(driver, clock)
	dependencies.Listeners = []Listener{ListenerFunc(func(context.Context, Event) error {
		listenerEffects++
		return nil
	})}
	core, err := newExecutorCore(dependencies,
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			compile: pureCompiler(func(command model.Command) (any, error) {
				parentCompiles++
				return command.Children, nil
			}),
			execute: func(ctx context.Context, state *executionState, compiled evaluatedDispatch) (commandEffect, error) {
				parentEffects++
				if err := state.dependencies.Driver.LaunchApp(ctx, device.LaunchAppRequest{AppID: "must-not-launch"}); err != nil {
					return commandEffect{effectClass: EffectComposite}, err
				}
				_, err := state.executeChildren(ctx, compiled.value.([]model.Command))
				return commandEffect{effectClass: EffectComposite}, err
			},
		},
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			compile: pureCompiler(func(model.Command) (any, error) {
				firstChildCompiles++
				return struct{}{}, nil
			}),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				childEffects++
				return commandEffect{effectClass: EffectHostMutation}, nil
			},
		},
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandStopApp, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile: pureCompiler(func(model.Command) (any, error) {
				failingChildCompiles++
				return nil, childFailure
			}),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				childEffects++
				return commandEffect{effectClass: EffectDeviceMutation}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}

	result, err := core.execute(context.Background(), model.Command{
		Kind: model.CommandRunFlow,
		Children: []model.Command{
			{Kind: model.CommandAction},
			{Kind: model.CommandStopApp},
		},
	}, 0)
	if !errors.Is(err, childFailure) {
		t.Fatalf("execute() error = %v, want inline child compile failure", err)
	}
	if result.Sequence() != 0 {
		t.Fatalf("execute() result sequence = %d, want no Timeline span before full-tree compilation", result.Sequence())
	}
	if parentCompiles != 1 || firstChildCompiles != 1 || failingChildCompiles != 1 {
		t.Fatalf("compile counts = parent %d first %d failing %d, want 1/1/1", parentCompiles, firstChildCompiles, failingChildCompiles)
	}
	if parentEffects != 0 || childEffects != 0 || listenerEffects != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("descendant compile failure allowed effects: parent=%d child=%d listener=%d driver=%v", parentEffects, childEffects, listenerEffects, driver.Actions())
	}
}

func TestDispatcherPassesCompileContextAndCompilesInlineChildrenOnce(t *testing.T) {
	t.Parallel()

	var paths []string
	var compilerContexts []context.Context
	compileCalls := 0
	registry, err := newHandlerRegistry(handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectObserved,
		compile: func(ctx context.Context, compileCtx compileContext, _ model.Command) (any, error) {
			compileCalls++
			compilerContexts = append(compilerContexts, ctx)
			paths = append(paths, compileCtx.FlowPath())
			return struct{}{}, nil
		},
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	})
	if err != nil {
		t.Fatalf("newHandlerRegistry() error: %v", err)
	}
	ctx := context.WithValue(context.Background(), struct{}{}, "compiler marker")
	compiled, err := newDispatcher(registry).compile(
		ctx,
		compileContext{containingFlow: "/workspace/root.yaml"},
		model.Command{Kind: model.CommandAction, Children: []model.Command{{Kind: model.CommandAction}}},
	)
	if err != nil {
		t.Fatalf("compile() error: %v", err)
	}
	if compileCalls != 2 || len(compiled.children) != 1 {
		t.Fatalf("inline compilation = calls %d children %d, want 2/1", compileCalls, len(compiled.children))
	}
	if len(compilerContexts) != 2 || compilerContexts[0] != ctx || compilerContexts[1] != ctx {
		t.Fatalf("compiler contexts = %#v, want the dispatcher context twice", compilerContexts)
	}
	if got, want := paths, []string{"/workspace/root.yaml", "/workspace/root.yaml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("compiler flow paths = %#v, want %#v", got, want)
	}
}

func TestDispatcherChecksCancellationBeforeAndAfterCompiler(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	compileCalls := 0
	registry, err := newHandlerRegistry(handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) {
			compileCalls++
			return struct{}{}, nil
		}),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	})
	if err != nil {
		t.Fatalf("newHandlerRegistry() error: %v", err)
	}
	if _, err := newDispatcher(registry).compile(ctx, compileContext{}, model.Command{Kind: model.CommandAction}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled compile() error = %v, want context.Canceled", err)
	}
	if compileCalls != 0 {
		t.Fatalf("pre-cancelled compiler calls = %d, want zero", compileCalls)
	}

	ctx, cancel = context.WithCancel(context.Background())
	compileCalls = 0
	registry, err = newHandlerRegistry(handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) {
			compileCalls++
			cancel()
			return struct{}{}, nil
		}),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	})
	if err != nil {
		t.Fatalf("newHandlerRegistry() error: %v", err)
	}
	command := model.Command{Kind: model.CommandAction, Children: []model.Command{{Kind: model.CommandAction}}}
	if _, err := newDispatcher(registry).compile(ctx, compileContext{}, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("compiler-cancelled compile() error = %v, want context.Canceled", err)
	}
	if compileCalls != 1 {
		t.Fatalf("compiler-cancelled compiler calls = %d, want one and no child compile", compileCalls)
	}

	if _, err := newDispatcher(registry).compile(nil, compileContext{}, model.Command{Kind: model.CommandAction}); err == nil {
		t.Fatal("compile(nil) error = nil, want configuration error")
	} else {
		var configurationError *ConfigurationError
		if !errors.As(err, &configurationError) {
			t.Fatalf("compile(nil) error = %T %v, want *ConfigurationError", err, err)
		}
	}
}

func TestDispatcherRejectsMissingAndSilentSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      model.Command
		spec         handlerSpec
		wantNoResult bool
	}{
		{
			name: "missing handler", command: model.Command{Kind: model.CommandStopApp},
			spec: observedTestHandler(model.CommandLaunchApp), wantNoResult: true,
		},
		{
			name: "nil compiled value", command: model.Command{Kind: model.CommandLaunchApp}, wantNoResult: true,
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandLaunchApp, effectClass: EffectObserved,
				compile: pureCompiler(func(model.Command) (any, error) { return nil, nil }),
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{effectClass: EffectObserved}, nil
				},
			},
		},
		{
			name: "returned none", command: model.Command{Kind: model.CommandLaunchApp},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandLaunchApp, effectClass: EffectObserved,
				compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{}, nil
				},
			},
		},
		{
			name: "mismatched effect", command: model.Command{Kind: model.CommandLaunchApp},
			spec: handlerSpec{
				evaluate: identityEvaluator,
				keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
				postAction: postActionNoSettle,
				compile:    pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{effectClass: EffectObserved}, nil
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			driver := enginetest.NewFakeDriver()
			core, err := newExecutorCore(coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0))), test.spec)
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			result, err := core.execute(context.Background(), test.command, 0)
			var configurationError *ConfigurationError
			if !errors.As(err, &configurationError) {
				t.Fatalf("execute() error = %T %v, want *ConfigurationError", err, err)
			}
			if test.wantNoResult {
				if result.Sequence() != 0 {
					t.Fatalf("execute() result = %#v, want zero result", result)
				}
			} else if result.Outcome() != Failed || result.Sequence() == 0 {
				t.Fatalf("execute() result = outcome %q sequence %d, want recorded failure", result.Outcome(), result.Sequence())
			}
			if len(driver.Actions()) != 0 {
				t.Fatalf("rejected dispatch mutated driver: %v", driver.Actions())
			}
		})
	}
}

func observedTestHandler(keyword model.CommandKeyword) handlerSpec {
	return handlerSpec{
		evaluate: identityEvaluator,
		keyword:  keyword, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
