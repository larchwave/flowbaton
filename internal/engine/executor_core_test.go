package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestNewExecutorCoreValidatesOnlyCoreDependencies(t *testing.T) {
	t.Parallel()

	valid := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	tests := []struct {
		name   string
		mutate func(*Dependencies)
	}{
		{name: "driver", mutate: func(dependencies *Dependencies) { dependencies.Driver = nil }},
		{name: "clock", mutate: func(dependencies *Dependencies) { dependencies.Clock = nil }},
		{name: "JavaScript factory", mutate: func(dependencies *Dependencies) { dependencies.JSFactory = nil }},
		{name: "controller", mutate: func(dependencies *Dependencies) { dependencies.Controller = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies := valid
			test.mutate(&dependencies)
			if _, err := newExecutorCore(dependencies, observedTestHandler(model.CommandLaunchApp)); err == nil {
				t.Fatal("newExecutorCore() error = nil, want missing core dependency rejection")
			}
		})
	}

	valid.ArtifactSink = nil
	valid.RecordingController = nil
	valid.ResourceReader = nil
	valid.InputGenerator = nil
	valid.ImageChecker = nil
	if _, err := newExecutorCore(valid, observedTestHandler(model.CommandLaunchApp)); err != nil {
		t.Fatalf("newExecutorCore() rejected unused optional services: %v", err)
	}
}

func TestExecutorCancellationDoesNotCompileWaitOrAct(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	compileCalls, executeCalls, controllerCalls := 0, 0, 0
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.Controller = ControllerFunc(func(context.Context) error {
		controllerCalls++
		return nil
	})
	core, err := newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile: pureCompiler(func(model.Command) (any, error) {
			compileCalls++
			return struct{}{}, nil
		}),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			executeCalls++
			return commandEffect{effectClass: EffectDeviceMutation}, nil
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := core.execute(ctx, model.Command{Kind: model.CommandLaunchApp}, 2)
	if !errors.Is(err, context.Canceled) || result.Outcome() != Cancelled {
		t.Fatalf("execute() = outcome %q error %v, want Cancelled/context.Canceled", result.Outcome(), err)
	}
	if result.Sequence() != 1 || result.Depth() != 2 {
		t.Fatalf("cancelled result sequence/depth = %d/%d", result.Sequence(), result.Depth())
	}
	if compileCalls != 0 || executeCalls != 0 || controllerCalls != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("cancelled execution acted: compile=%d execute=%d controller=%d driver=%v", compileCalls, executeCalls, controllerCalls, driver.Actions())
	}
}

func TestExecutorCancellationFromStartedListenerPreventsHandlerEffects(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listenerCalls, handlerEffects := 0, 0
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		listenerCalls++
		if event.Kind() == EventCommandStarted {
			cancel()
		}
		return nil
	})}
	core, err := newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile: pureCompiler(func(model.Command) (any, error) {
			return device.LaunchAppRequest{AppID: "must-not-launch"}, nil
		}),
		execute: func(ctx context.Context, state *executionState, compiled evaluatedDispatch) (commandEffect, error) {
			handlerEffects++
			err := state.dependencies.Driver.LaunchApp(ctx, compiled.value.(device.LaunchAppRequest))
			return commandEffect{effectClass: EffectDeviceMutation}, err
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}

	result, err := core.execute(ctx, model.Command{Kind: model.CommandLaunchApp}, 0)
	if !errors.Is(err, context.Canceled) || result.Outcome() != Cancelled {
		t.Fatalf("execute() = outcome %q error %v, want Cancelled/context.Canceled", result.Outcome(), err)
	}
	if result.Sequence() != 1 || listenerCalls != 2 {
		t.Fatalf("cancelled result/listeners = sequence %d calls %d, want 1/2", result.Sequence(), listenerCalls)
	}
	if handlerEffects != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("listener cancellation allowed effects: handler=%d driver=%v", handlerEffects, driver.Actions())
	}
}

func TestExecutorIsolatesControllerListenersAndArtifactFailure(t *testing.T) {
	t.Parallel()

	t.Run("controller blocks handler", func(t *testing.T) {
		t.Parallel()
		controllerFailure := errors.New("controller unavailable")
		executeCalls := 0
		dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
		dependencies.Controller = ControllerFunc(func(context.Context) error { return controllerFailure })
		core, err := newExecutorCore(dependencies, handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandLaunchApp, effectClass: EffectObserved,
			compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				executeCalls++
				return commandEffect{effectClass: EffectObserved}, nil
			},
		})
		if err != nil {
			t.Fatalf("newExecutorCore() error: %v", err)
		}
		result, err := core.execute(context.Background(), model.Command{Kind: model.CommandLaunchApp}, 0)
		if !errors.Is(err, controllerFailure) || result.Outcome() != Failed || executeCalls != 0 {
			t.Fatalf("controller result = outcome %q error %v calls %d", result.Outcome(), err, executeCalls)
		}
	})

	t.Run("product error wins", func(t *testing.T) {
		t.Parallel()
		productFailure := NewDeviceConnectionError("device disconnected", errors.New("transport"))
		artifactFailure := errors.New("artifact storage unavailable")
		sink := &failingArtifactSink{err: artifactFailure}
		listenerCalls := 0
		dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
		dependencies.ArtifactSink = sink
		dependencies.Listeners = []Listener{
			ListenerFunc(func(context.Context, Event) error {
				listenerCalls++
				return errors.New("listener failure")
			}),
			ListenerFunc(func(context.Context, Event) error {
				listenerCalls++
				panic("listener panic")
			}),
		}
		core, err := newExecutorCore(dependencies, handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandTakeScreenshot, effectClass: EffectArtifact,
			compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{
					effectClass:    EffectArtifact,
					artifactWrites: []ArtifactWriteRequest{{Owner: "command", Kind: "screenshot", Data: []byte("png")}},
				}, productFailure
			},
		})
		if err != nil {
			t.Fatalf("newExecutorCore() error: %v", err)
		}
		result, err := core.execute(context.Background(), model.Command{Kind: model.CommandTakeScreenshot}, 0)
		if err != productFailure || result.ProductError() != productFailure || result.Outcome() != Failed {
			t.Fatalf("product error was replaced: outcome=%q returned=%v recorded=%v", result.Outcome(), err, result.ProductError())
		}
		if sink.calls != 1 || listenerCalls != 4 {
			t.Fatalf("isolated callbacks = artifact %d listener %d, want 1/4", sink.calls, listenerCalls)
		}
	})
}

func TestExecutorAssociatesFinalizedArtifactsBeforeFinishedListenerAndLedgerSnapshot(t *testing.T) {
	t.Parallel()

	primaryFailure := NewDeviceConnectionError("handler failed", errors.New("transport"))
	artifactFailure := errors.New("second artifact failed")
	handlerArtifacts := []device.Artifact{{
		Kind: "recording", Path: "owned/run.mp4",
		Metadata: map[string]string{"source": "handler"},
	}}
	sink := &orderedArtifactSink{responses: []artifactSinkResponse{
		{result: ArtifactWriteResult{Artifact: device.Artifact{
			Kind: "screenshot", Path: "owned/first.png",
			Metadata: map[string]string{"source": "first"},
		}}},
		{result: ArtifactWriteResult{Artifact: device.Artifact{
			Kind: "screenshot", Path: "owned/second.png",
			Metadata: map[string]string{"source": "second"},
		}}, err: artifactFailure},
	}}

	var core *executorCore
	var finishedEvent Event
	var listenerLedger []CommandResult
	dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.ArtifactSink = sink
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandFinished {
			finishedEvent = event
			listenerLedger = core.ledger.snapshot()
		}
		return nil
	})}
	var err error
	core, err = newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandTakeScreenshot, effectClass: EffectArtifact,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{
				effectClass:        EffectArtifact,
				finalizedArtifacts: handlerArtifacts,
				artifactWrites: []ArtifactWriteRequest{
					{Owner: "command", Kind: "screenshot", SuggestedName: "first.png"},
					{Owner: "command", Kind: "screenshot", SuggestedName: "second.png"},
					{Owner: "command", Kind: "screenshot", SuggestedName: "must-not-run.png"},
				},
			}, primaryFailure
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore error: %v", err)
	}

	result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandTakeScreenshot}, 2)
	if executeErr != primaryFailure || result.ProductError() != primaryFailure || result.Outcome() != Failed {
		t.Fatalf("execute = outcome %q returned %v recorded %v, want primary handler failure", result.Outcome(), executeErr, result.ProductError())
	}
	if len(sink.requests) != 2 {
		t.Fatalf("sink requests = %d, want stop after first error", len(sink.requests))
	}
	wantPaths := []string{"owned/run.mp4", "owned/first.png", "owned/second.png"}
	assertArtifactPaths(t, result.Artifacts(), wantPaths)
	assertArtifactPaths(t, finishedEvent.Artifacts(), wantPaths)
	if len(listenerLedger) != 1 || listenerLedger[0].Sequence() != result.Sequence() {
		t.Fatalf("ledger visible to finished listener = %#v, want current result already recorded once", listenerLedger)
	}
	assertArtifactPaths(t, listenerLedger[0].Artifacts(), wantPaths)
	ledger := core.ledger.snapshot()
	if len(ledger) != 1 {
		t.Fatalf("ledger snapshot = %#v, want exactly one result", ledger)
	}
	assertArtifactPaths(t, ledger[0].Artifacts(), wantPaths)

	handlerArtifacts[0].Metadata["source"] = "handler-mutated"
	sink.responses[0].result.Artifact.Metadata["source"] = "sink-mutated"
	if result.Artifacts()[0].Metadata["source"] != "handler" || result.Artifacts()[1].Metadata["source"] != "first" {
		t.Fatalf("result artifacts alias producer storage: %#v", result.Artifacts())
	}
}

func TestExecutorClassifiesOptionalRetryableSkippedAndCancelledOutcomes(t *testing.T) {
	t.Parallel()

	retryable := NewOperationError("element missing", errors.New("lookup"))
	tests := []struct {
		name       string
		optional   bool
		executeErr error
		want       Outcome
		wantErr    bool
	}{
		{name: "completed", want: Completed},
		{name: "optional warning", optional: true, executeErr: retryable, want: Warned},
		{name: "retryable failure", executeErr: retryable, want: Failed, wantErr: true},
		{name: "skipped", executeErr: NewCommandSkippedError("condition false", nil), want: Skipped},
		{name: "cancelled", executeErr: context.Canceled, want: Cancelled, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			core, err := newExecutorCore(
				coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
				handlerSpec{
					evaluate: identityEvaluator,
					keyword:  model.CommandAssertVisible, effectClass: EffectObserved,
					compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
					execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
						return commandEffect{effectClass: EffectObserved}, test.executeErr
					},
				},
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			command := model.Command{Kind: model.CommandAssertVisible}
			if test.optional {
				command.Optional = pointer(true)
			}
			result, executeErr := core.execute(context.Background(), command, 0)
			if result.Outcome() != test.want {
				t.Fatalf("execute() outcome = %q, want %q", result.Outcome(), test.want)
			}
			if (executeErr != nil) != test.wantErr {
				t.Fatalf("execute() error = %v, wantErr=%t", executeErr, test.wantErr)
			}
			if test.name == "retryable failure" && !IsRetryable(result.ProductError()) {
				t.Fatalf("retryable product error lost classification: %v", result.ProductError())
			}
		})
	}
}

func TestExecutorNormalizesTerminalHandlerErrorsExactlyOnce(t *testing.T) {
	t.Parallel()

	configuration := NewConfigurationError("invalid handler configuration", nil)
	connection := NewDeviceConnectionError("device disconnected", nil)
	ordinary := errors.New("driver rejected request")
	tests := []struct {
		name          string
		input         error
		wantSame      bool
		wantFlowBaton int
	}{
		{name: "context cancellation", input: context.Canceled, wantSame: true},
		{name: "context deadline", input: context.DeadlineExceeded, wantSame: true},
		{name: "configuration", input: configuration, wantSame: true},
		{name: "device connection", input: connection, wantSame: true},
		{name: "ordinary failure", input: ordinary, wantFlowBaton: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			core, err := newExecutorCore(
				coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
				handlerSpec{
					evaluate: identityEvaluator,
					keyword:  model.CommandAction, effectClass: EffectObserved,
					compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
					execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
						return commandEffect{effectClass: EffectObserved}, test.input
					},
				},
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			result, _ := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
			got := result.ProductError()
			if test.wantSame && got != test.input {
				t.Fatalf("ProductError() = %T %v, want original %T %v", got, got, test.input, test.input)
			}
			count := 0
			for current := got; current != nil; current = errors.Unwrap(current) {
				if _, ok := current.(*OperationError); ok {
					count++
				}
			}
			if count != test.wantFlowBaton {
				t.Fatalf("OperationError wrappers = %d, want %d: %T %v", count, test.wantFlowBaton, got, got)
			}
			if test.wantFlowBaton == 1 && !errors.Is(got, ordinary) {
				t.Fatalf("normalized error lost original cause: %v", got)
			}
		})
	}
}

func TestExecutorFinalizationNormalizesEvaluatorErrorsBeforeStorage(t *testing.T) {
	t.Parallel()

	ordinary := errors.New("evaluator lookup failed")
	joined := errors.Join(
		NewCommandSkippedError("skipped", nil),
		NewDeviceConnectionError("device disconnected", nil),
	)
	tests := []struct {
		name          string
		err           func() error
		optional      bool
		wantOutcome   Outcome
		wantSame      error
		wantSafe      bool
		wantFlowBaton int
	}{
		{
			name: "ordinary error gets one product wrapper", err: func() error { return ordinary },
			wantOutcome: Failed, wantFlowBaton: 1,
		},
		{
			name: "valid joined aggregate keeps identity", err: func() error { return joined }, optional: true,
			wantOutcome: Failed, wantSame: joined,
		},
		{
			name: "cancellation keeps identity", err: func() error { return context.Canceled }, optional: true,
			wantOutcome: Cancelled, wantSame: context.Canceled,
		},
		{
			name: "causal cycle fails closed", err: func() error {
				cycle := &graphCausalError{}
				cycle.cause = cycle
				return cycle
			},
			wantOutcome: Failed, wantSafe: true,
		},
		{
			name: "panicking graph fails closed", err: func() error { return &panickingAsError{} },
			wantOutcome: Failed, wantSafe: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := test.err()
			var finished Event
			dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandFinished {
					finished = event
				}
				return nil
			})}
			core, err := newExecutorCore(dependencies, handlerSpec{
				keyword: model.CommandAssertVisible, effectClass: EffectObserved,
				compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
				evaluate: func(_ context.Context, _ evaluationContext, command model.Command, _ any) (evaluatedDispatch, error) {
					return evaluatedDispatch{command: command, value: struct{}{}}, input
				},
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					t.Fatal("executor ran after evaluator failure")
					return commandEffect{}, nil
				},
			})
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}
			command := model.Command{Kind: model.CommandAssertVisible}
			if test.optional {
				command.Optional = pointer(true)
			}
			result, executeErr := core.execute(context.Background(), command, 0)
			if result.Outcome() != test.wantOutcome {
				t.Fatalf("result outcome = %q, want %q", result.Outcome(), test.wantOutcome)
			}
			if executeErr == nil || result.ProductError() != executeErr || finished.ProductError() != executeErr {
				t.Fatalf("terminal error identity = returned %T result %T event %T", executeErr, result.ProductError(), finished.ProductError())
			}
			if test.wantSafe {
				assertSafeConfigurationError(t, executeErr)
			}
			if test.wantSame != nil && executeErr != test.wantSame {
				t.Fatalf("execute error type = %T, want original %T", executeErr, test.wantSame)
			}
			count := 0
			for current := executeErr; current != nil; current = errors.Unwrap(current) {
				if _, ok := current.(*OperationError); ok {
					count++
				}
			}
			if count != test.wantFlowBaton {
				t.Fatalf("OperationError wrappers = %d, want %d", count, test.wantFlowBaton)
			}
			if test.wantFlowBaton == 1 && !errors.Is(executeErr, ordinary) {
				t.Fatal("one-layer normalization lost ordinary evaluator cause")
			}
		})
	}
}

func TestExecutorFinalizationSanitizesMalformedArtifactFailure(t *testing.T) {
	t.Parallel()

	var malformed *OperationError
	sink := &failingArtifactSink{err: malformed}
	var finished Event
	dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.ArtifactSink = sink
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandFinished {
			finished = event
		}
		return nil
	})}
	core, err := newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandTakeScreenshot, effectClass: EffectArtifact,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{
				effectClass:    EffectArtifact,
				artifactWrites: []ArtifactWriteRequest{{Owner: "command", Kind: "screenshot", Data: []byte("png")}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}

	result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandTakeScreenshot}, 0)
	assertSafeConfigurationError(t, executeErr)
	if sink.calls != 1 || result.Outcome() != Failed || result.ProductError() != executeErr || finished.ProductError() != executeErr {
		t.Fatalf("artifact failure = calls %d outcome %q result %T event %T", sink.calls, result.Outcome(), result.ProductError(), finished.ProductError())
	}
}

func TestExecutorFinalizationSanitizesMalformedControllerFailure(t *testing.T) {
	t.Parallel()

	var malformed *OperationError
	dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.Controller = ControllerFunc(func(context.Context) error { return malformed })
	core, err := newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAssertVisible, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			t.Fatal("handler ran after controller failure")
			return commandEffect{}, nil
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}

	result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandAssertVisible}, 0)
	assertSafeConfigurationError(t, executeErr)
	if result.Outcome() != Failed || result.ProductError() != executeErr {
		t.Fatalf("controller failure = outcome %q result error %T returned %T", result.Outcome(), result.ProductError(), executeErr)
	}
}

func TestExecutorCoreOwnsDeclaredDeviceMutationSettling(t *testing.T) {
	t.Parallel()

	clock := newAdvancingClock()
	driver := enginetest.NewFakeDriver()
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
	driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
		{Value: ready}, {Value: ready},
	}})
	timeout := int64(500)
	core, err := newExecutorCore(coreDependencies(driver, clock), handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
		postAction: postActionSettle,
		settleRequest: func(evaluated evaluatedDispatch) (device.SettleRequest, error) {
			request := evaluated.value.(device.LaunchAppRequest)
			return device.SettleRequest{AppID: request.AppID, TimeoutMillis: &timeout}, nil
		},
		compile: pureCompiler(func(model.Command) (any, error) {
			return device.LaunchAppRequest{AppID: "com.example.core-settle"}, nil
		}),
		execute: func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
			request := evaluated.value.(device.LaunchAppRequest)
			return commandEffect{effectClass: EffectDeviceMutation}, state.dependencies.Driver.LaunchApp(ctx, request)
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}
	lookup := NewElementLookup(driver, clock)
	core.state.lookupFn = func() (*ElementLookup, error) { return lookup, nil }

	result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandLaunchApp}, 0)
	if executeErr != nil || result.Outcome() != Completed {
		t.Fatalf("execute() = outcome %q error %v, want Completed", result.Outcome(), executeErr)
	}
	actions := driver.Actions()
	if len(actions) != 3 || actions[0].Method != enginetest.MethodLaunchApp ||
		actions[1].Method != enginetest.MethodWaitForAppToSettle ||
		actions[2].Method != enginetest.MethodWaitForAppToSettle {
		t.Fatalf("core-owned post-action methods = %#v", actions)
	}
	for _, action := range actions[1:] {
		request := action.Request.(device.SettleRequest)
		if request.AppID != "com.example.core-settle" || request.TimeoutMillis == nil || *request.TimeoutMillis != timeout {
			t.Fatalf("settle request = %#v", request)
		}
	}
	if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-2*HierarchySettlePollInterval; got != want {
		t.Fatalf("interaction watermark after core settle = %v, want %v", got, want)
	}
}

func TestExecutorCorePreservesDeviceDisconnectDuringDeclaredSettle(t *testing.T) {
	t.Parallel()

	clock := newAdvancingClock()
	disconnect := NewDeviceConnectionError("device disconnected during settle", nil)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
		{Err: disconnect},
	}})
	core, err := newExecutorCore(coreDependencies(driver, clock), handlerSpec{
		evaluate:    identityEvaluator,
		keyword:     model.CommandLaunchApp,
		effectClass: EffectDeviceMutation,
		postAction:  postActionSettle,
		settleRequest: func(evaluatedDispatch) (device.SettleRequest, error) {
			return device.SettleRequest{AppID: "com.example.disconnect"}, nil
		},
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectDeviceMutation}, nil
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}
	lookup := NewElementLookup(driver, clock)
	core.state.lookupFn = func() (*ElementLookup, error) { return lookup, nil }
	optional := true
	result, executeErr := core.execute(context.Background(), model.Command{
		Kind: model.CommandLaunchApp, Optional: &optional,
	}, 0)
	var connection *DeviceConnectionError
	if !errors.As(executeErr, &connection) || executeErr != disconnect {
		t.Fatalf("execute() error = %T %v, want original settle disconnect", executeErr, executeErr)
	}
	if result.Outcome() != Failed || result.ProductError() != disconnect {
		t.Fatalf("optional settle disconnect = outcome %q error %v, want Failed/original", result.Outcome(), result.ProductError())
	}
	actions := driver.Actions()
	if len(actions) != 1 || actions[0].Method != enginetest.MethodWaitForAppToSettle {
		t.Fatalf("settle disconnect actions = %#v, want one settle attempt", actions)
	}
}

func TestSettleRequestFactoryIsPanicSafeAndContractValidated(t *testing.T) {
	t.Parallel()

	negative := int64(-1)
	tests := []struct {
		name    string
		factory settleRequestFactory
		wantErr bool
	}{
		{name: "missing factory", wantErr: true},
		{name: "panic", factory: func(evaluatedDispatch) (device.SettleRequest, error) {
			panic("factory panic")
		}, wantErr: true},
		{name: "raw factory error", factory: func(evaluatedDispatch) (device.SettleRequest, error) {
			return device.SettleRequest{}, errors.New("factory failed")
		}, wantErr: true},
		{name: "blank app ID", factory: func(evaluatedDispatch) (device.SettleRequest, error) {
			return device.SettleRequest{}, nil
		}, wantErr: true},
		{name: "negative timeout", factory: func(evaluatedDispatch) (device.SettleRequest, error) {
			return device.SettleRequest{AppID: "com.example", TimeoutMillis: &negative}, nil
		}, wantErr: true},
		{name: "valid", factory: func(evaluatedDispatch) (device.SettleRequest, error) {
			return device.SettleRequest{AppID: "com.example"}, nil
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := invokeSettleRequestFactory(test.factory, evaluatedDispatch{})
			if (err != nil) != test.wantErr {
				t.Fatalf("invokeSettleRequestFactory() = %#v %v, wantErr=%t", request, err, test.wantErr)
			}
			if !test.wantErr {
				return
			}
			var configuration *ConfigurationError
			if !errors.As(err, &configuration) || CanWarnWhenOptional(err) {
				t.Fatalf("factory contract error = %T %v, want non-warnable *ConfigurationError", err, err)
			}
		})
	}
}

func TestExecutorSequenceProducesMonotonicImmutableMetadata(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(500, 0))
	driver := enginetest.NewFakeDriver()
	var events []Event
	listeners := []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})}
	dependencies := coreDependencies(driver, clock)
	dependencies.Listeners = listeners
	var emittedLogs [][]string
	var evaluatedCommands []*model.Command
	core, err := newExecutorCore(dependencies, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectHostMutation,
		compile: pureCompiler(func(command model.Command) (any, error) {
			arguments := command.Arguments.(map[string]any)
			return arguments["name"].(string), nil
		}),
		execute: func(_ context.Context, _ *executionState, compiled evaluatedDispatch) (commandEffect, error) {
			clock.Advance(10 * time.Millisecond)
			logs := []string{"executed " + compiled.value.(string)}
			evaluated := model.Command{Kind: model.CommandAction, Arguments: map[string]any{"name": compiled.value}}
			emittedLogs = append(emittedLogs, logs)
			evaluatedCommands = append(evaluatedCommands, &evaluated)
			return commandEffect{
				effectClass:      EffectHostMutation,
				numberOfRuns:     1,
				evaluatedCommand: &evaluated,
				logMessages:      logs,
				insight:          "host action",
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}
	listeners[0] = ListenerFunc(func(context.Context, Event) error { panic("caller mutation") })
	commands := []model.Command{
		{Kind: model.CommandAction, Arguments: map[string]any{"name": "first"}},
		{Kind: model.CommandAction, Arguments: map[string]any{"name": "second"}},
	}
	results, err := core.executeSequence(context.Background(), commands, 3)
	if err != nil {
		t.Fatalf("executeSequence() error: %v", err)
	}
	if len(results) != 2 || results[0].Sequence() != 1 || results[1].Sequence() != 2 {
		t.Fatalf("sequence results = %#v", results)
	}
	for index, result := range results {
		if result.Depth() != 3 || result.Outcome() != Completed || result.Duration() != 10*time.Millisecond {
			t.Errorf("result %d = depth %d outcome %q duration %v", index, result.Depth(), result.Outcome(), result.Duration())
		}
		metadata := result.Metadata()
		if metadata.NumberOfRuns() != 1 || metadata.Insight() != "host action" || len(metadata.LogMessages()) != 1 {
			t.Errorf("result %d metadata = %#v", index, metadata)
		}
	}
	if len(events) != 4 {
		t.Fatalf("listener events = %d, want start/finish for two commands", len(events))
	}

	commands[0].Arguments.(map[string]any)["name"] = "caller mutation"
	emittedLogs[0][0] = "handler mutation"
	evaluatedCommands[0].Arguments.(map[string]any)["name"] = "handler mutation"
	firstCommand := results[0].Command()
	firstEvaluated, ok := results[0].Metadata().EvaluatedCommand()
	if !ok {
		t.Fatal("first result has no evaluated command")
	}
	if firstCommand.Arguments.(map[string]any)["name"] != "first" ||
		firstEvaluated.Arguments.(map[string]any)["name"] != "first" ||
		results[0].Metadata().LogMessages()[0] != "executed first" {
		t.Fatalf("result aliases mutable inputs: command=%#v evaluated=%#v logs=%v", firstCommand, firstEvaluated, results[0].Metadata().LogMessages())
	}
}

func TestCompositeHandlerCanExecuteChildSequenceAtNextDepth(t *testing.T) {
	t.Parallel()

	var childResults []CommandResult
	childCompiles := 0
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			compile: pureCompiler(func(command model.Command) (any, error) {
				return command.Children, nil
			}),
			execute: func(ctx context.Context, state *executionState, compiled evaluatedDispatch) (commandEffect, error) {
				var executeErr error
				childResults, executeErr = state.executeChildren(ctx, compiled.value.([]model.Command))
				return commandEffect{effectClass: EffectComposite}, executeErr
			},
		},
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectObserved,
			compile: pureCompiler(func(model.Command) (any, error) {
				childCompiles++
				return struct{}{}, nil
			}),
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectObserved}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error: %v", err)
	}
	parent, err := core.execute(context.Background(), model.Command{
		Kind:     model.CommandRunFlow,
		Children: []model.Command{{Kind: model.CommandAction}},
	}, 4)
	if err != nil {
		t.Fatalf("execute() error: %v", err)
	}
	if parent.Sequence() != 1 || parent.Depth() != 4 || parent.Outcome() != Completed {
		t.Fatalf("parent result = sequence %d depth %d outcome %q", parent.Sequence(), parent.Depth(), parent.Outcome())
	}
	if len(childResults) != 1 || childResults[0].Sequence() != 2 || childResults[0].Depth() != 5 || childResults[0].Outcome() != Completed {
		t.Fatalf("child results = %#v", childResults)
	}
	if childCompiles != 1 {
		t.Fatalf("child compiler called %d times, want cached exactly-once compilation", childCompiles)
	}
}

type coreJSFactory struct{}

func (coreJSFactory) NewRuntime() (js.Runtime, error) { return nil, nil }

func coreDependencies(driver device.Driver, clock Clock) Dependencies {
	return Dependencies{
		Driver: driver, Clock: clock, JSFactory: coreJSFactory{}, Controller: NoopController{},
	}
}

type failingArtifactSink struct {
	calls int
	err   error
}

func (sink *failingArtifactSink) Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.calls++
	return ArtifactWriteResult{}, sink.err
}

type artifactSinkResponse struct {
	result ArtifactWriteResult
	err    error
}

type orderedArtifactSink struct {
	requests  []ArtifactWriteRequest
	responses []artifactSinkResponse
}

func (sink *orderedArtifactSink) Write(_ context.Context, request ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.requests = append(sink.requests, request)
	response := sink.responses[len(sink.requests)-1]
	return response.result, response.err
}

func assertArtifactPaths(t *testing.T, artifacts []device.Artifact, want []string) {
	t.Helper()
	if len(artifacts) != len(want) {
		t.Fatalf("artifact count = %d, want %d: %#v", len(artifacts), len(want), artifacts)
	}
	for index, path := range want {
		if artifacts[index].Path != path {
			t.Fatalf("artifact %d path = %q, want %q: %#v", index, artifacts[index].Path, path, artifacts)
		}
	}
}
