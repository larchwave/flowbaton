package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestFailureDecisionValuesAreStable(t *testing.T) {
	t.Parallel()

	if got := string(FailureDecisionFail); got != "FAIL" {
		t.Fatalf("FailureDecisionFail = %q, want FAIL", got)
	}
	if got := string(FailureDecisionContinue); got != "CONTINUE" {
		t.Fatalf("FailureDecisionContinue = %q, want CONTINUE", got)
	}
}

func TestRootFailureResolverDecisionsFailClosedExceptContinue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resolver     func(*int) FailureResolver
		wantContinue bool
		wantCalls    int
	}{
		{name: "nil resolver", resolver: func(*int) FailureResolver { return nil }},
		{name: "typed nil resolver", resolver: func(*int) FailureResolver {
			var resolver *nilContinuingFailureResolver
			return resolver
		}},
		{name: "FAIL", resolver: func(calls *int) FailureResolver {
			return FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				*calls++
				return FailureDecisionFail
			})
		}, wantCalls: 1},
		{name: "CONTINUE", resolver: func(calls *int) FailureResolver {
			return FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				*calls++
				return FailureDecisionContinue
			})
		}, wantContinue: true, wantCalls: 1},
		{name: "zero decision", resolver: func(calls *int) FailureResolver {
			return FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				*calls++
				return FailureDecision("")
			})
		}, wantCalls: 1},
		{name: "invalid decision", resolver: func(calls *int) FailureResolver {
			return FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				*calls++
				return FailureDecision("RETRY")
			})
		}, wantCalls: 1},
		{name: "resolver panic", resolver: func(calls *int) FailureResolver {
			return FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				*calls++
				panic("resolver panic")
			})
		}, wantCalls: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := NewConfigurationError("first command failed", nil)
			order := make([]string, 0, 2)
			root := &compiledFlow{
				path: "/workspace/failure-decision.yaml",
				body: []compiledDispatch{
					rootPolicyDispatch("first", failure, &order),
					rootPolicyDispatch("second", nil, &order),
				},
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
			dependencies.FailureResolver = test.resolver(&resolverCalls)

			result, err := executeCompiledRoot(context.Background(), dependencies, root)
			if err != failure || result.ProductError() != failure || result.Outcome() != Failed {
				t.Fatalf("root result = outcome %q product %T returned %T, want original failure", result.Outcome(), result.ProductError(), err)
			}
			wantOrder := []string{"first"}
			wantCommands := 1
			if test.wantContinue {
				wantOrder = append(wantOrder, "second")
				wantCommands = 2
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("handler order = %#v, want %#v", order, wantOrder)
			}
			if resolverCalls != test.wantCalls {
				t.Fatalf("resolver calls = %d, want %d", resolverCalls, test.wantCalls)
			}
			commands := result.Commands()
			if len(commands) != wantCommands || commands[0].Outcome() != Failed || commands[0].ProductError() != failure {
				t.Fatalf("commands = %#v, want %d with original first failure", commands, wantCommands)
			}
			if test.wantContinue && commands[1].Outcome() != Completed {
				t.Fatalf("continued command outcome = %q, want Completed", commands[1].Outcome())
			}
		})
	}
}

func TestRootFailurePolicyContinuesRemainingLifecycleAndKeepsEarliestError(t *testing.T) {
	t.Parallel()

	t.Run("continued onStart reaches remaining start body and completion", func(t *testing.T) {
		startFailure := NewConfigurationError("start failed", nil)
		order := make([]string, 0, 5)
		root := &compiledFlow{
			path: "/workspace/continue-start.yaml",
			onStart: []compiledDispatch{
				rootPolicyDispatch("start-failed", startFailure, &order),
				rootPolicyDispatch("start-remaining", nil, &order),
			},
			body:       []compiledDispatch{rootPolicyDispatch("body", nil, &order)},
			onComplete: []compiledDispatch{rootPolicyDispatch("completion", nil, &order)},
		}
		resolverCalls := 0
		dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
		dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
			resolverCalls++
			return FailureDecisionContinue
		})

		result, err := executeCompiledRoot(context.Background(), dependencies, root)
		if err != startFailure || result.ProductError() != startFailure || result.Outcome() != Failed {
			t.Fatalf("root result = outcome %q product %T returned %T, want first start failure", result.Outcome(), result.ProductError(), err)
		}
		wantOrder := []string{"start-failed", "start-remaining", "body", "completion"}
		if !reflect.DeepEqual(order, wantOrder) || resolverCalls != 1 || len(result.Commands()) != 4 {
			t.Fatalf("continued lifecycle = order %#v calls %d commands %d", order, resolverCalls, len(result.Commands()))
		}
	})

	t.Run("FAIL skips normal work but completion and cleanup run", func(t *testing.T) {
		startFailure := NewConfigurationError("start failed", nil)
		order := make([]string, 0, 2)
		runtime := &sessionRuntime{}
		root := &compiledFlow{
			path: "/workspace/fail-start.yaml",
			onStart: []compiledDispatch{
				rootPolicyDispatch("start-failed", startFailure, &order),
				rootPolicyDispatch("start-skipped", nil, &order),
			},
			body:       []compiledDispatch{rootPolicyDispatch("body-skipped", nil, &order)},
			onComplete: []compiledDispatch{rootPolicyDispatch("completion", nil, &order)},
		}
		resolverCalls := 0
		dependencies := flowExecutorDependencies(runtime, nil)
		dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
			resolverCalls++
			return FailureDecisionFail
		})

		result, err := executeCompiledRoot(context.Background(), dependencies, root)
		wantOrder := []string{"start-failed", "completion"}
		_, _, closeCalls := runtime.Snapshot()
		if err != startFailure || result.ProductError() != startFailure || !reflect.DeepEqual(order, wantOrder) ||
			resolverCalls != 1 || len(result.Commands()) != 2 || closeCalls != 1 {
			t.Fatalf("failed lifecycle = error %T order %#v calls %d commands %d close %d", err, order, resolverCalls, len(result.Commands()), closeCalls)
		}
	})

	t.Run("continued body and completion failures retain chronological first", func(t *testing.T) {
		first := NewConfigurationError("first start failure", nil)
		second := NewConfigurationError("second body failure", nil)
		third := NewConfigurationError("third completion failure", nil)
		cleanup := NewConfigurationError("cleanup failure", nil)
		order := make([]string, 0, 5)
		runtime := &sessionRuntime{closeErr: cleanup}
		root := &compiledFlow{
			path:    "/workspace/continue-all-phases.yaml",
			onStart: []compiledDispatch{rootPolicyDispatch("start-failed", first, &order)},
			body: []compiledDispatch{
				rootPolicyDispatch("body-failed", second, &order),
				rootPolicyDispatch("body-remaining", nil, &order),
			},
			onComplete: []compiledDispatch{rootPolicyDispatch("completion-failed", third, &order), rootPolicyDispatch("completion-remaining", nil, &order)},
		}
		resolverCalls := 0
		dependencies := flowExecutorDependencies(runtime, nil)
		dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
			resolverCalls++
			return FailureDecisionContinue
		})

		result, err := executeCompiledRoot(context.Background(), dependencies, root)
		wantOrder := []string{"start-failed", "body-failed", "body-remaining", "completion-failed", "completion-remaining"}
		commands := result.Commands()
		if err != first || result.ProductError() != first || !reflect.DeepEqual(order, wantOrder) || resolverCalls != 3 || len(commands) != 5 {
			t.Fatalf("continued failures = error %T product %T order %#v calls %d commands %d", err, result.ProductError(), order, resolverCalls, len(commands))
		}
		if commands[0].ProductError() != first || commands[1].ProductError() != second || commands[2].Outcome() != Completed ||
			commands[3].ProductError() != third || commands[4].Outcome() != Completed {
			t.Fatalf("chronological command evidence = %#v", commands)
		}
	})
}

func TestRootFailurePolicyBypassesResolverForCancellationAndDeviceFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input error
	}{
		{name: "cancellation", input: context.Canceled},
		{name: "deadline", input: context.DeadlineExceeded},
		{name: "wrapped cancellation", input: fmt.Errorf("wrapped cancellation: %w", context.Canceled)},
		{name: "joined deadline", input: errors.Join(NewConfigurationError("configuration", nil), context.DeadlineExceeded)},
		{name: "device connection", input: NewDeviceConnectionError("device disconnected", nil)},
		{name: "custom As device", input: NewOperationError("retry wrapper", &asOnlyError{target: asDeviceConnection})},
		{name: "joined device", input: errors.Join(NewConfigurationError("configuration", nil), NewDeviceConnectionError("device disconnected", nil))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			order := make([]string, 0, 2)
			root := &compiledFlow{
				path: "/workspace/bypass-policy.yaml",
				body: []compiledDispatch{
					rootPolicyDispatch("terminal", test.input, &order),
					rootPolicyDispatch("must-not-run", nil, &order),
				},
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
			dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				resolverCalls++
				return FailureDecisionContinue
			})

			result, err := executeCompiledRoot(context.Background(), dependencies, root)
			if err != test.input || result.ProductError() != test.input || result.Outcome() != ClassifyOutcome(test.input, false) {
				t.Fatalf("terminal identity = returned %T product %T outcome %q", err, result.ProductError(), result.Outcome())
			}
			if resolverCalls != 0 || !reflect.DeepEqual(order, []string{"terminal"}) || len(result.Commands()) != 1 {
				t.Fatalf("bypass = resolver calls %d order %#v commands %d", resolverCalls, order, len(result.Commands()))
			}
		})
	}
}

func TestRootFailurePolicyPromotesLaterTerminalFailureOverContinuedOrdinaryFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal error
	}{
		{name: "cancellation", terminal: context.Canceled},
		{name: "deadline", terminal: context.DeadlineExceeded},
		{name: "wrapped deadline", terminal: fmt.Errorf("wrapped deadline: %w", context.DeadlineExceeded)},
		{name: "device connection", terminal: NewDeviceConnectionError("device disconnected", nil)},
		{name: "custom As device", terminal: NewOperationError("device wrapper", &asOnlyError{target: asDeviceConnection})},
		{name: "joined device", terminal: errors.Join(NewConfigurationError("configuration", nil), NewDeviceConnectionError("device disconnected", nil))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ordinary := NewConfigurationError("continued ordinary failure", nil)
			order := make([]string, 0, 3)
			runtime := &sessionRuntime{}
			root := &compiledFlow{
				path: "/workspace/ordinary-then-terminal.yaml",
				body: []compiledDispatch{
					rootPolicyDispatch("ordinary", ordinary, &order),
					rootPolicyDispatch("terminal", test.terminal, &order),
					rootPolicyDispatch("must-not-run", nil, &order),
				},
				onComplete: []compiledDispatch{rootPolicyDispatch("completion", nil, &order)},
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(runtime, nil)
			dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
				resolverCalls++
				if result.ProductError() != ordinary {
					t.Errorf("resolver received terminal error %T", result.ProductError())
				}
				return FailureDecisionContinue
			})

			result, err := executeCompiledRoot(context.Background(), dependencies, root)
			if err != test.terminal || result.ProductError() != test.terminal || result.Outcome() != ClassifyOutcome(test.terminal, false) {
				t.Fatalf("terminal publication = returned %T product %T outcome %q, want %T/%q", err, result.ProductError(), result.Outcome(), test.terminal, ClassifyOutcome(test.terminal, false))
			}
			wantOrder := []string{"ordinary", "terminal", "completion"}
			commands := result.Commands()
			if resolverCalls != 1 || !reflect.DeepEqual(order, wantOrder) || len(commands) != 3 ||
				commands[0].ProductError() != ordinary || commands[1].ProductError() != test.terminal || commands[2].Outcome() != Completed {
				t.Fatalf("terminal lifecycle = resolver %d order %#v commands %#v", resolverCalls, order, commands)
			}
			if got := runtime.EnvCalls(); !reflect.DeepEqual(
				got, append(flowEnvCalls("/workspace/ordinary-then-terminal.yaml"), "pop")) {
				t.Fatalf("environment cleanup = %#v", got)
			}
			_, _, closeCalls := runtime.Snapshot()
			if closeCalls != 1 {
				t.Fatalf("runtime Close calls = %d, want one", closeCalls)
			}
		})
	}
}

func TestRootFailurePolicyTerminalFailureSurvivesCompletionAndCleanupErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal error
	}{
		{name: "cancellation", terminal: context.Canceled},
		{name: "device connection", terminal: NewDeviceConnectionError("device disconnected", nil)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ordinary := NewConfigurationError("continued body failure", nil)
			completion := NewConfigurationError("ordinary completion failure", nil)
			popFailure := NewConfigurationError("scope cleanup failure", nil)
			closeFailure := NewConfigurationError("session cleanup failure", nil)
			order := make([]string, 0, 3)
			runtime := &sessionRuntime{popErr: popFailure, closeErr: closeFailure}
			root := &compiledFlow{
				path: "/workspace/terminal-cleanup-precedence.yaml",
				body: []compiledDispatch{
					rootPolicyDispatch("ordinary", ordinary, &order),
					rootPolicyDispatch("terminal", test.terminal, &order),
				},
				onComplete: []compiledDispatch{rootPolicyDispatch("completion", completion, &order)},
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(runtime, nil)
			dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
				resolverCalls++
				if result.ProductError() != ordinary && result.ProductError() != completion {
					t.Errorf("resolver received terminal/cleanup error %T", result.ProductError())
				}
				return FailureDecisionContinue
			})

			result, err := executeCompiledRoot(context.Background(), dependencies, root)
			if err != test.terminal || result.ProductError() != test.terminal || result.Outcome() != ClassifyOutcome(test.terminal, false) {
				t.Fatalf("terminal was masked = returned %T product %T outcome %q", err, result.ProductError(), result.Outcome())
			}
			if resolverCalls != 2 || !reflect.DeepEqual(order, []string{"ordinary", "terminal", "completion"}) {
				t.Fatalf("lifecycle = resolver %d order %#v", resolverCalls, order)
			}
			if got := runtime.EnvCalls(); !reflect.DeepEqual(
				got, append(flowEnvCalls("/workspace/terminal-cleanup-precedence.yaml"), "pop")) {
				t.Fatalf("environment cleanup = %#v", got)
			}
			_, _, closeCalls := runtime.Snapshot()
			if closeCalls != 1 {
				t.Fatalf("runtime Close calls = %d, want one", closeCalls)
			}
		})
	}
}

func TestRootFailurePolicyUsesTerminalPrecedenceAcrossBodyAndCompletion(t *testing.T) {
	t.Parallel()

	ordinary := NewConfigurationError("continued ordinary failure", nil)
	deviceFailure := NewDeviceConnectionError("device disconnected", nil)
	order := make([]string, 0, 3)
	runtime := &sessionRuntime{
		popErr:   NewConfigurationError("scope cleanup failure", nil),
		closeErr: NewConfigurationError("session cleanup failure", nil),
	}
	root := &compiledFlow{
		path: "/workspace/terminal-cross-phase-precedence.yaml",
		body: []compiledDispatch{
			rootPolicyDispatch("ordinary", ordinary, &order),
			rootPolicyDispatch("device", deviceFailure, &order),
		},
		onComplete: []compiledDispatch{
			rootPolicyDispatch("completion-cancelled", context.Canceled, &order),
			rootPolicyDispatch("completion-must-not-run", nil, &order),
		},
	}
	resolverCalls := 0
	dependencies := flowExecutorDependencies(runtime, nil)
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if result.ProductError() != ordinary {
			t.Errorf("resolver received terminal error %T", result.ProductError())
		}
		return FailureDecisionContinue
	})

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != context.Canceled || result.ProductError() != context.Canceled || result.Outcome() != Cancelled {
		t.Fatalf("cross-phase precedence = returned %T product %T outcome %q, want cancellation", err, result.ProductError(), result.Outcome())
	}
	if resolverCalls != 1 || !reflect.DeepEqual(order, []string{"ordinary", "device", "completion-cancelled"}) {
		t.Fatalf("cross-phase lifecycle = resolver %d order %#v", resolverCalls, order)
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].ProductError() != ordinary || commands[1].ProductError() != deviceFailure || commands[2].ProductError() != context.Canceled {
		t.Fatalf("cross-phase command evidence = %#v", commands)
	}
	if got := runtime.EnvCalls(); !reflect.DeepEqual(
		got, append(flowEnvCalls("/workspace/terminal-cross-phase-precedence.yaml"), "pop")) {
		t.Fatalf("environment cleanup = %#v", got)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestFailureResolverObservesFinishedLedgerRecordAndCannotMutateOutput(t *testing.T) {
	t.Parallel()

	failure := NewConfigurationError("finalized command failed", nil)
	label := "evaluated label"
	root := &compiledFlow{
		path: "/workspace/finalized-resolver.yaml",
		body: []compiledDispatch{{
			command: model.Command{Kind: model.CommandAction, Arguments: map[string]any{"stage": "source"}},
			spec: handlerSpec{
				keyword: model.CommandAction, effectClass: EffectObserved,
				evaluate: func(_ context.Context, _ evaluationContext, command model.Command, _ any) (evaluatedDispatch, error) {
					evaluated := cloneCommand(command)
					evaluated.Arguments = map[string]any{"stage": "evaluated"}
					evaluated.Label = &label
					return evaluatedDispatch{command: evaluated, value: struct{}{}}, nil
				},
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					return commandEffect{effectClass: EffectObserved, logMessages: []string{"final-log"}}, failure
				},
			},
		}},
	}
	finishedSeen := false
	var finishedEvent Event
	var session *executionSession
	resolverCalls := 0
	violations := make([]string, 0)
	dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandFinished {
			finishedSeen = true
			finishedEvent = event
		}
		return nil
	})})
	dependencies.Clock = newAdvancingClock()
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if !finishedSeen {
			violations = append(violations, "CommandFinished was not dispatched")
		}
		ledger := session.core.ledger.snapshot()
		if len(ledger) != 1 || ledger[0].Sequence() != result.Sequence() || ledger[0].ProductError() != failure {
			violations = append(violations, fmt.Sprintf("ledger snapshot %#v did not contain resolver result", ledger))
		}
		metadata := result.Metadata()
		evaluated, exists := metadata.EvaluatedCommand()
		if result.Outcome() != Failed || result.ProductError() != failure || !exists ||
			evaluated.Arguments.(map[string]any)["stage"] != "evaluated" ||
			!reflect.DeepEqual(metadata.LogMessages(), []string{"final-log"}) ||
			result.StartedAt().IsZero() || result.FinishedAt().Before(result.StartedAt()) ||
			result.Duration() != result.FinishedAt().Sub(result.StartedAt()) {
			violations = append(violations, fmt.Sprintf("resolver result was not finalized: %#v", result))
		}

		result.command.Arguments.(map[string]any)["stage"] = "resolver-mutated"
		result.metadata.evaluatedCommand.Arguments.(map[string]any)["stage"] = "resolver-mutated"
		result.metadata.logMessages[0] = "resolver-mutated"
		return FailureDecisionContinue
	})

	var err error
	session, err = newExecutionSession(context.Background(), dependencies, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	result, runErr := session.run(context.Background())
	if runErr != failure || result.ProductError() != failure || resolverCalls != 1 || len(violations) != 0 {
		t.Fatalf("resolver finalization = returned %T product %T calls %d violations %#v", runErr, result.ProductError(), resolverCalls, violations)
	}
	commands := result.Commands()
	if len(commands) != 1 || commands[0].Command().Arguments.(map[string]any)["stage"] != "source" {
		t.Fatalf("resolver mutation changed command output: %#v", commands)
	}
	metadata := commands[0].Metadata()
	evaluated, exists := metadata.EvaluatedCommand()
	if !exists || evaluated.Arguments.(map[string]any)["stage"] != "evaluated" || !reflect.DeepEqual(metadata.LogMessages(), []string{"final-log"}) {
		t.Fatalf("resolver mutation changed metadata output: %#v", metadata)
	}
	if finishedEvent.ProductError() != failure || finishedEvent.Metadata().LogMessages()[0] != "final-log" {
		t.Fatalf("finished event = error %T metadata %#v", finishedEvent.ProductError(), finishedEvent.Metadata())
	}
}

func TestRootFailurePolicyResolvesCompositeOuterOnceAndLeavesChildrenRaw(t *testing.T) {
	t.Parallel()

	failure := NewConfigurationError("synthetic child failed", nil)
	order := make([]string, 0, 5)
	childCommands := []model.Command{
		{Kind: model.CommandAction, Arguments: "child-failed"},
		{Kind: model.CommandAction, Arguments: "child-must-not-run"},
	}
	childDispatches := []compiledDispatch{
		rootPolicyDispatch("child-failed", failure, &order),
		rootPolicyDispatch("child-must-not-run", nil, &order),
	}
	var childResults []CommandResult
	outer := compiledDispatch{
		command:  model.Command{Kind: model.CommandRunFlow, Arguments: "outer", Children: childCommands},
		children: childDispatches,
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			execute: func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
				order = append(order, "outer")
				var err error
				childResults, err = state.executeChildren(ctx, evaluated.command.Children)
				order = append(order, "outer-return")
				return commandEffect{effectClass: EffectComposite}, err
			},
		},
		value: childCommands,
	}
	root := &compiledFlow{
		path: "/workspace/composite-policy.yaml",
		body: []compiledDispatch{
			outer,
			rootPolicyDispatch("root-next", nil, &order),
		},
	}
	resolverCalls := 0
	resolverViolations := make([]string, 0)
	dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if result.Command().Kind != model.CommandRunFlow || result.Depth() != 0 || result.ProductError() != failure {
			resolverViolations = append(resolverViolations, fmt.Sprintf("resolved non-outer result %#v", result))
		}
		return FailureDecisionContinue
	})

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != failure || result.ProductError() != failure || resolverCalls != 1 || len(resolverViolations) != 0 {
		t.Fatalf("composite policy = returned %T product %T calls %d violations %#v", err, result.ProductError(), resolverCalls, resolverViolations)
	}
	wantOrder := []string{"outer", "child-failed", "outer-return", "root-next"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("composite order = %#v, want %#v", order, wantOrder)
	}
	if len(childResults) != 1 || childResults[0].Depth() != 1 || childResults[0].ProductError() != failure {
		t.Fatalf("raw child results = %#v", childResults)
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].Command().Kind != model.CommandRunFlow || commands[0].Depth() != 0 ||
		commands[1].Depth() != 1 || commands[1].ProductError() != failure ||
		commands[2].Depth() != 0 || commands[2].Outcome() != Completed {
		t.Fatalf("composite ledger = %#v", commands)
	}
}

func TestRootFailurePolicyDoesNotResolveNestedFlowCommands(t *testing.T) {
	t.Parallel()

	failure := NewConfigurationError("nested command failed", nil)
	order := make([]string, 0, 5)
	child := &compiledFlow{
		path: "/workspace/nested-policy-child.yaml",
		body: []compiledDispatch{
			rootPolicyDispatch("nested-failed", failure, &order),
			rootPolicyDispatch("nested-must-not-run", nil, &order),
		},
	}
	var childResult FlowResult
	outer := compiledDispatch{
		command: model.Command{Kind: model.CommandRunFlow, Arguments: "nested"},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				order = append(order, "outer")
				var err error
				childResult, err = state.executeFlow(ctx, child, nil)
				order = append(order, "outer-return")
				return commandEffect{effectClass: EffectComposite}, err
			},
		},
		value: child,
	}
	root := &compiledFlow{
		path: "/workspace/nested-policy-root.yaml",
		body: []compiledDispatch{outer, rootPolicyDispatch("root-next", nil, &order)},
	}
	resolverCalls := 0
	dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if result.Depth() != 0 || result.Command().Kind != model.CommandRunFlow {
			t.Errorf("resolver received nested command: depth %d kind %q", result.Depth(), result.Command().Kind)
		}
		return FailureDecisionContinue
	})

	result, err := executeCompiledRoot(context.Background(), dependencies, root)
	if err != failure || result.ProductError() != failure || resolverCalls != 1 {
		t.Fatalf("nested policy = returned %T product %T resolver calls %d", err, result.ProductError(), resolverCalls)
	}
	wantOrder := []string{"outer", "nested-failed", "outer-return", "root-next"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("nested order = %#v, want %#v", order, wantOrder)
	}
	if childResult.ProductError() != failure || len(childResult.Commands()) != 1 || len(result.Commands()) != 3 {
		t.Fatalf("nested/raw results = child %#v root %#v", childResult, result)
	}
}

// A failed root ends only a declared execution sequence. This test declares
// that sequence and verifies the resolver, flow-start, and runtime counts.
func TestExecuteStopsBeforeDuplicateRootAfterContinuedRootFailure(t *testing.T) {
	t.Parallel()

	text := "Missing"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/continued-root-failure.yaml",
		Config:        model.Config{AppID: "com.example.continued-root"},
		Commands: []model.Command{{
			Kind: model.CommandAssertVisible, Form: model.CommandFormObject,
			Selector: &model.ElementSelector{TextRegex: &text},
		}},
	}
	program := singleCompileProgram(flow)
	program.roots = []string{flow.Path, flow.Path}
	program.graph.Roots = append([]string(nil), program.roots...)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
		},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: device.TreeNode{}}, {Value: device.TreeNode{}}},
	})
	baseFactory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	factory := &countingDelegatingRuntimeFactory{delegate: baseFactory}
	rootStarts := 0
	resolverCalls := 0

	results, executeErr := Execute(context.Background(), program, Dependencies{
		ExecutionID:    "continued-root-failure",
		SequencedRoots: 2,
		Driver:         driver,
		Clock:          newAdvancingClock(),
		JSFactory:      factory,
		Controller:     NoopController{},
		FailureResolver: FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
			resolverCalls++
			return FailureDecisionContinue
		}),
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventFlowStarted {
				rootStarts++
			}
			return nil
		})},
	})
	if executeErr == nil || len(results) != 1 || results[0].Outcome() != Failed {
		t.Fatalf("Execute() = results %#v error %v, want first failed root only", results, executeErr)
	}
	if resolverCalls != 1 || rootStarts != 1 || factory.calls != 1 {
		t.Fatalf("duplicate progression = resolver %d root starts %d runtimes %d", resolverCalls, rootStarts, factory.calls)
	}
}

type nilContinuingFailureResolver struct{}

func (*nilContinuingFailureResolver) ResolveFailure(context.Context, CommandResult) FailureDecision {
	return FailureDecisionContinue
}

func rootPolicyDispatch(name string, productError error, order *[]string) compiledDispatch {
	return compiledDispatch{
		command: model.Command{Kind: model.CommandAction, Arguments: name},
		spec: handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				*order = append(*order, name)
				return commandEffect{effectClass: EffectHostMutation}, productError
			},
		},
		value: name,
	}
}

type countingDelegatingRuntimeFactory struct {
	delegate js.Factory
	calls    int
}

func (factory *countingDelegatingRuntimeFactory) NewRuntime() (js.Runtime, error) {
	factory.calls++
	return factory.delegate.NewRuntime()
}
