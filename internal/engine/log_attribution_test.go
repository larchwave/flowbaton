package engine

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestNestedCommandLogsBelongOnlyToActiveCommandAndHost(t *testing.T) {
	t.Parallel()
	assertNestedCommandLogs(t, false)
}

func TestNestedCommandLogAttributionRestoresParentOnChildFailure(t *testing.T) {
	t.Parallel()
	assertNestedCommandLogs(t, true)
}

func assertNestedCommandLogs(t *testing.T, failChild bool) {
	t.Helper()

	var hostLogs []string
	factory, err := js.NewFactory(js.Config{
		Random: rand.New(rand.NewSource(59)),
		LogSink: func(message string) {
			hostLogs = append(hostLogs, message)
		},
	})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	var finished []Event
	dependencies := coreDependencies(enginetest.NewFakeDriver(), newAdvancingClock())
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandFinished {
			finished = append(finished, event)
		}
		return nil
	})}
	core, err := newExecutorCore(
		dependencies,
		handlerSpec{
			keyword: model.CommandRunFlow, effectClass: EffectComposite,
			compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			evaluate: identityEvaluator,
			execute: func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
				active, runtimeErr := state.jsRuntime()
				if runtimeErr != nil {
					return commandEffect{}, runtimeErr
				}
				if _, evalErr := active.Evaluate(ctx, js.EvalRequest{Script: `console.log("parent-before")`}); evalErr != nil {
					return commandEffect{}, evalErr
				}
				if _, childErr := state.executeChildren(ctx, evaluated.command.Children); childErr != nil {
					return commandEffect{}, childErr
				}
				if _, evalErr := active.Evaluate(ctx, js.EvalRequest{Script: `console.log("parent-after")`}); evalErr != nil {
					return commandEffect{}, evalErr
				}
				return commandEffect{effectClass: EffectComposite}, nil
			},
		},
		handlerSpec{
			keyword: model.CommandRunScript, effectClass: EffectHostMutation,
			compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			evaluate: func(ctx context.Context, evaluation evaluationContext, command model.Command, compiled any) (evaluatedDispatch, error) {
				if failChild {
					if _, evalErr := evaluation.Evaluate(ctx, js.EvalRequest{Script: `console.log("child-evaluate")`}); evalErr != nil {
						return evaluatedDispatch{}, evalErr
					}
				}
				return identityEvaluator(ctx, evaluation, command, compiled)
			},
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				active, runtimeErr := state.jsRuntime()
				if runtimeErr != nil {
					return commandEffect{}, runtimeErr
				}
				message := "child"
				if failChild {
					message = "child-execute"
				}
				if _, evalErr := active.Evaluate(ctx, js.EvalRequest{Script: `console.log("` + message + `")`}); evalErr != nil {
					return commandEffect{}, evalErr
				}
				if failChild {
					return commandEffect{effectClass: EffectHostMutation}, NewOperationError("child failed", nil)
				}
				return commandEffect{effectClass: EffectHostMutation}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }

	parent, err := core.execute(context.Background(), model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Children: []model.Command{{Kind: model.CommandRunScript, Form: model.CommandFormObject}},
	}, 0)
	if failChild {
		var operation *OperationError
		if !errors.As(err, &operation) {
			t.Fatalf("execute(parent) error = %T %v, want *OperationError", err, err)
		}
	} else if err != nil {
		t.Fatalf("execute(parent) error = %v", err)
	}
	wantHost := []string{"parent-before", "child", "parent-after"}
	wantParent := []string{"parent-before", "parent-after"}
	wantChild := []string{"child"}
	if failChild {
		wantHost = []string{"parent-before", "child-evaluate", "child-execute"}
		wantParent = []string{"parent-before"}
		wantChild = []string{"child-evaluate", "child-execute"}
	}
	if got, want := hostLogs, wantHost; !reflect.DeepEqual(got, want) {
		t.Fatalf("host logs = %#v, want %#v", got, want)
	}
	if got, want := parent.Metadata().LogMessages(), wantParent; !reflect.DeepEqual(got, want) {
		t.Fatalf("parent metadata logs = %#v, want %#v", got, want)
	}
	if len(finished) != 2 {
		t.Fatalf("finished event count = %d, want 2", len(finished))
	}
	var childLogs []string
	for _, event := range finished {
		command, exists := event.Command()
		if exists && command.Kind == model.CommandRunScript {
			childLogs = event.Metadata().LogMessages()
		}
	}
	if want := wantChild; !reflect.DeepEqual(childLogs, want) {
		t.Fatalf("child metadata logs = %#v, want %#v", childLogs, want)
	}
}
