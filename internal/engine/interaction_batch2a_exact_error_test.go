package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch2AExactErrorCapabilityFailsClosed(t *testing.T) {
	t.Parallel()

	pressKey := pressKeyHandlerSpec()
	if pressKey.exactErrorPolicy != exactErrorPublicationPressKey {
		t.Fatalf("pressKey exact-error policy = %d, want pressKey-only publication", pressKey.exactErrorPolicy)
	}
	unauthorized := batch2ADirectHandlerSpec(model.CommandBack)
	unauthorized.exactErrorPolicy = exactErrorPublicationPressKey
	if _, err := newHandlerRegistry(unauthorized); !isConfigurationError(err) {
		t.Fatalf("unauthorized exact policy error = %T %v, want *ConfigurationError", err, err)
	}

	raw := errors.New("raw Driver failure")
	typedNil := (*batch2ACyclicDriverError)(nil)
	tests := []struct {
		name   string
		spec   handlerSpec
		effect commandEffect
		err    error
	}{
		{
			name: "unauthorized request",
			spec: batch2ADirectHandlerSpec(model.CommandBack),
			effect: commandEffect{effectClass: EffectDeviceMutation,
				exactErrorRequest: &exactErrorRequest{underlying: raw}},
		},
		{
			name: "nil underlying request",
			spec: pressKey,
			effect: commandEffect{effectClass: EffectDeviceMutation,
				exactErrorRequest: &exactErrorRequest{}},
		},
		{
			name: "typed nil underlying request",
			spec: pressKey,
			effect: commandEffect{effectClass: EffectDeviceMutation,
				exactErrorRequest: &exactErrorRequest{underlying: typedNil}},
		},
		{
			name: "incoherent mixed request and error channels",
			spec: pressKey,
			effect: commandEffect{effectClass: EffectDeviceMutation,
				exactErrorRequest: &exactErrorRequest{underlying: raw}},
			err: errors.New("unrelated returned error"),
		},
		{
			name: "retry reconstructed bare disposition",
			spec: retryHandlerSpec(),
			effect: commandEffect{effectClass: EffectComposite,
				exactErrorDisposition: &exactErrorDisposition{
					published: raw, classification: NewOperationError("forged", raw),
				}},
		},
		{
			name: "unauthorized propagation",
			spec: pressKey,
			effect: commandEffect{effectClass: EffectDeviceMutation,
				exactErrorPropagation: &exactErrorPropagationRequest{}},
		},
		{
			name: "mixed propagation and error channels",
			spec: retryHandlerSpec(),
			effect: commandEffect{effectClass: EffectComposite,
				exactErrorPropagation: &exactErrorPropagationRequest{}},
			err: raw,
		},
		{
			name: "nil propagation source",
			spec: retryHandlerSpec(),
			effect: commandEffect{effectClass: EffectComposite,
				exactErrorPropagation: &exactErrorPropagationRequest{source: nil}},
		},
		{
			name: "malformed propagation",
			spec: retryHandlerSpec(),
			effect: commandEffect{effectClass: EffectComposite,
				exactErrorPropagation: &exactErrorPropagationRequest{source: &CommandResult{
					sequence: 1, identity: &commandResultIdentity{marker: 1},
				}}},
		},
		{
			name: "mutually exclusive request channels",
			spec: retryHandlerSpec(),
			effect: commandEffect{effectClass: EffectComposite,
				exactErrorRequest:     &exactErrorRequest{underlying: raw},
				exactErrorPropagation: &exactErrorPropagationRequest{}},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return test.effect, test.err
			}
			compiled := compiledDispatch{command: model.Command{Kind: test.spec.keyword}, spec: test.spec}
			effect, err := newDispatcher(handlerRegistry{}).execute(
				context.Background(), &executionState{}, compiled,
				evaluatedDispatch{command: model.Command{Kind: test.spec.keyword}, value: struct{}{}},
			)
			if !isConfigurationError(err) || effect.exactErrorRequest != nil || effect.exactErrorPropagation != nil || effect.exactErrorDisposition != nil {
				t.Fatalf("dispatcher result = effect %#v error %T %v, want cleared exact state and *ConfigurationError", effect, err, err)
			}
		})
	}
}

func TestRetryCannotForgeExactErrorPropagation(t *testing.T) {
	raw := errors.New("forged public error")
	classification := NewOperationError("forged retry classification", raw)
	forged := CommandResult{
		identity: &commandResultIdentity{marker: 1}, rootRunID: "forged/root", sequence: 1, depth: 1,
		productError: raw,
		retryClassification: &retryErrorClassification{
			classification: classification,
			publication:    &exactErrorDisposition{published: raw, classification: classification},
		},
	}
	spec := retryHandlerSpec()
	spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
		return commandEffect{
			effectClass:           EffectComposite,
			exactErrorPropagation: &exactErrorPropagationRequest{source: &forged},
		}, nil
	}
	compiled := compiledDispatch{
		command: model.Command{Kind: model.CommandRetry},
		spec:    spec,
	}
	effect, err := newLedgerDispatcher(handlerRegistry{}, newCommandResultLedger()).execute(
		context.Background(),
		&executionState{},
		compiled,
		evaluatedDispatch{command: model.Command{Kind: model.CommandRetry}, value: struct{}{}},
	)
	if err == raw || effect.exactErrorDisposition != nil {
		t.Fatalf("retry-forged propagation was accepted: effect %#v error %T %v", effect, err, err)
	}
	if !isConfigurationError(err) {
		t.Fatalf("retry-forged propagation error = %T %v, want *ConfigurationError", err, err)
	}
}

func TestExactErrorSourceAuthentication(t *testing.T) {
	t.Parallel()

	canonical := errors.New("canonical recorded public error")
	classification := NewOperationError("canonical retry classification", canonical)
	source := CommandResult{
		identity: &commandResultIdentity{marker: 1}, rootRunID: "exact-error/root", sequence: 7, depth: 1,
		productError: canonical,
		retryClassification: &retryErrorClassification{
			classification: classification,
			publication:    &exactErrorDisposition{published: canonical, classification: classification},
		},
	}
	ledger := newCommandResultLedger()
	if err := ledger.record(source); err != nil {
		t.Fatal(err)
	}
	dispatch := func(t *testing.T, target *commandResultLedger, candidate *CommandResult) (commandEffect, error) {
		t.Helper()
		spec := retryHandlerSpec()
		spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{
				effectClass:           EffectComposite,
				exactErrorPropagation: &exactErrorPropagationRequest{source: candidate},
			}, nil
		}
		return newLedgerDispatcher(handlerRegistry{}, target).execute(
			context.Background(), &executionState{},
			compiledDispatch{command: model.Command{Kind: model.CommandRetry}, spec: spec},
			evaluatedDispatch{command: model.Command{Kind: model.CommandRetry}, value: struct{}{}},
		)
	}
	assertRejected := func(t *testing.T, target *commandResultLedger, candidate *CommandResult) {
		t.Helper()
		effect, err := dispatch(t, target, candidate)
		if !isConfigurationError(err) || effect.exactErrorPropagation != nil || effect.exactErrorDisposition != nil {
			t.Fatalf("source authentication = effect %#v error %T %v, want cleared exact state", effect, err, err)
		}
	}

	t.Run("nil source", func(t *testing.T) { assertRejected(t, ledger, nil) })
	t.Run("unrecorded source", func(t *testing.T) {
		unrecorded := cloneCommandResult(source)
		unrecorded.sequence++
		assertRejected(t, ledger, &unrecorded)
	})
	t.Run("foreign root", func(t *testing.T) {
		foreignRoot := cloneCommandResult(source)
		foreignRoot.rootRunID = "foreign/root"
		assertRejected(t, ledger, &foreignRoot)
	})
	t.Run("foreign ledger", func(t *testing.T) {
		foreignLedger := newCommandResultLedger()
		foreign := cloneCommandResult(source)
		foreign.identity = &commandResultIdentity{marker: 1}
		if err := foreignLedger.record(foreign); err != nil {
			t.Fatal(err)
		}
		assertRejected(t, foreignLedger, &source)
	})
	t.Run("copied source fields cannot replace canonical record", func(t *testing.T) {
		forgedPublic := errors.New("handler-forged replacement")
		copied := cloneCommandResult(source)
		copied.productError = forgedPublic
		copied.retryClassification = &retryErrorClassification{
			classification: NewConfigurationError("handler-forged classification", nil),
			publication: &exactErrorDisposition{
				published: forgedPublic, classification: NewConfigurationError("handler-forged publication", nil),
			},
		}
		assertRejected(t, ledger, &copied)
	})
}

func TestInteractionBatch2AOpaqueExactErrorPublishesAndRetriesByOrigin(t *testing.T) {
	t.Parallel()

	t.Run("direct publication", func(t *testing.T) {
		t.Parallel()

		rawValue := batch2AOpaqueDriverError{"seed": 1}
		var raw error = rawValue
		driver := batch2ADriver("ios")
		driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: raw}}})
		compiled := compileBatch2AExactTestCommand(t, batch2APressKeyCommand("LOCK"), pressKeyHandlerSpec())
		events := make([]Event, 0, 4)
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: "batch2a-nonidentitySafe-direct", Driver: driver, Clock: newAdvancingClock(),
			JSFactory: tapJSFactory(t), Controller: NoopController{},
			Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})},
		}, batch2AExactTestRoot(compiled, "/workspace/batch2a-nonidentitySafe-direct.yaml"), "batch2a-nonidentitySafe-direct/root-run-000001")
		commands := result.Commands()
		assertBatch2AOpaqueAlias(t, runErr, rawValue, "returned", 2)
		assertBatch2AOpaqueAlias(t, result.ProductError(), rawValue, "flow", 3)
		if len(commands) != 1 {
			t.Fatalf("non-identitySafe direct commands = %#v, want one", commands)
		}
		assertBatch2AOpaqueAlias(t, commands[0].ProductError(), rawValue, "command", 4)
		finished := 0
		for _, event := range events {
			if event.ProductError() == nil {
				continue
			}
			finished++
			assertBatch2AOpaqueAlias(t, event.ProductError(), rawValue, "event", 4+finished)
		}
		if finished != 2 || result.Outcome() != Failed || commands[0].Outcome() != Failed {
			t.Fatalf("non-identitySafe direct lifecycle = result %#v commands %#v events %#v", result, commands, events)
		}
	})

	t.Run("actual retry", func(t *testing.T) {
		t.Parallel()

		rawValue := batch2AOpaqueDriverError{"seed": 10}
		var raw error = rawValue
		driver := batch2ADriver("ios")
		driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: raw}, {Err: raw}}})
		child := batch2APressKeyCommand("LOCK")
		retry := model.Command{
			Kind: model.CommandRetry, Form: model.CommandFormObject,
			Arguments: map[string]any{
				"maxRetries": int64(1),
				"commands":   []any{map[string]any{string(model.CommandPressKey): "LOCK"}},
			},
			Children: []model.Command{child},
		}
		compiled := compileBatch2AExactTestCommand(t, retry, retryHandlerSpec(), pressKeyHandlerSpec())
		events := make([]Event, 0, 10)
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: "batch2a-nonidentitySafe-retry", Driver: driver, Clock: newAdvancingClock(),
			JSFactory: tapJSFactory(t), Controller: NoopController{},
			Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})},
		}, batch2AExactTestRoot(compiled, "/workspace/batch2a-nonidentitySafe-retry.yaml"), "batch2a-nonidentitySafe-retry/root-run-000001")
		if len(batch2APressKeyRequests(driver.Actions())) != 2 {
			t.Fatalf("non-identitySafe PressKey attempts = %d, want two; actions %#v", len(batch2APressKeyRequests(driver.Actions())), driver.Actions())
		}
		pressResults := make([]CommandResult, 0, 2)
		for _, command := range result.Commands() {
			if command.Command().Kind == model.CommandPressKey {
				pressResults = append(pressResults, command)
			}
		}
		if len(pressResults) != 2 {
			t.Fatalf("non-identitySafe retry press results = %#v, want two", pressResults)
		}
		assertBatch2AOpaqueAlias(t, pressResults[0].ProductError(), rawValue, "attempt-one", 11)
		assertBatch2AOpaqueAlias(t, pressResults[1].ProductError(), rawValue, "attempt-two", 12)
		assertBatch2AOpaqueRetrySurfaces(t, result, runErr, events, rawValue, "inline", 40)
	})
}

func TestInteractionBatch2AOpaqueExactErrorRetriesLinkedFlowByOrigin(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/batch2a-linked-root.yaml"
	childPath := "/workspace/batch2a-linked-child.yaml"
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "batch2a-linked-child.yaml", ResolvedPath: childPath,
	}
	rawValue := batch2AOpaqueDriverError{"seed": 30}
	var raw error = rawValue
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          rootPath,
		Config:        model.Config{AppID: "com.example.batch2a.root"},
		Commands:      []model.Command{retryFileCommand(int64(1), link)},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          childPath,
		Config:        model.Config{AppID: "com.example.batch2a.child"},
		Commands:      []model.Command{batch2APressKeyCommand("LOCK")},
	}
	registry, err := newHandlerRegistry(retryHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := compiled.Flow(rootPath)
	if !ok {
		t.Fatal("compiled linked retry root missing")
	}
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{
		PressKey: []enginetest.Result[struct{}]{{Err: raw}, {Err: raw}},
	})
	events := make([]Event, 0, 14)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-linked-retry", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch2a-linked-retry/root-run-000001")
	if got := len(batch2APressKeyRequests(driver.Actions())); got != 2 {
		t.Fatalf("linked PressKey attempts = %d, want two; result %#v error %T %v", got, result, runErr, runErr)
	}
	pressResults := make([]CommandResult, 0, 2)
	for _, command := range result.Commands() {
		if command.Command().Kind == model.CommandPressKey {
			pressResults = append(pressResults, command)
		}
	}
	if len(pressResults) != 2 {
		t.Fatalf("linked PressKey results = %#v, want two", pressResults)
	}
	assertBatch2AOpaqueAlias(t, pressResults[0].ProductError(), rawValue, "linked-attempt-one", 31)
	assertBatch2AOpaqueAlias(t, pressResults[1].ProductError(), rawValue, "linked-attempt-two", 32)
	assertBatch2AOpaqueRetrySurfaces(t, result, runErr, events, rawValue, "linked", 50)
}

func TestInteractionBatch2AIdentitySafeExactErrorRetriesLinkedFlowAndPublishesExactIdentity(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/batch2a-identitySafe-linked-root.yaml"
	childPath := "/workspace/batch2a-identitySafe-linked-child.yaml"
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "batch2a-identitySafe-linked-child.yaml", ResolvedPath: childPath,
	}
	raw := errors.New("identitySafe linked Driver failure")
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          rootPath,
		Config:        model.Config{AppID: "com.example.batch2a.root"},
		Commands:      []model.Command{retryFileCommand(int64(1), link)},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          childPath,
		Config:        model.Config{AppID: "com.example.batch2a.child"},
		Commands:      []model.Command{batch2APressKeyCommand("LOCK")},
	}
	registry, err := newHandlerRegistry(retryHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	root, ok := compiled.Flow(rootPath)
	if !ok {
		t.Fatal("compiled identitySafe linked retry root missing")
	}
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{
		PressKey: []enginetest.Result[struct{}]{{Err: raw}, {Err: raw}},
	})
	events := make([]Event, 0, 14)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-identitySafe-linked-retry", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch2a-identitySafe-linked-retry/root-run-000001")
	if got := len(batch2APressKeyRequests(driver.Actions())); got != 2 {
		t.Fatalf("identitySafe linked PressKey attempts = %d, want two; result %#v error %T %v", got, result, runErr, runErr)
	}
	assertBatch2AIdentitySafeRetrySurfaces(t, result, runErr, events, raw)
}

func TestInteractionBatch2ARetryClassificationOriginRejectsOuterErrors(t *testing.T) {
	t.Parallel()

	rawValue := batch2AOpaqueDriverError{"seed": 20}
	var raw error = rawValue
	classification := NewOperationError("classified exact Driver failure", raw)
	exactResult := CommandResult{
		productError: raw,
		retryClassification: &retryErrorClassification{
			classification: classification,
		},
	}
	outer := NewConfigurationError("unrelated linked-flow failure", nil)
	if got := retryClassificationError(retryLinkedSource, []CommandResult{exactResult}, outer); got != outer {
		t.Fatalf("linked outer error inherited exact child classification: %T %v", got, got)
	}
	linkedState := &executionState{
		executeCompiledFlow: func(context.Context, *compiledFlow, map[string]string, int) (FlowResult, error) {
			child := exactResult
			child.depth = 1
			return FlowResult{
				depth: 1, outcome: Failed, productError: outer,
				commands: []CommandResult{child},
			}, outer
		},
	}
	linkedAttempt := executeRetryAttempt(
		context.Background(), linkedState, model.Command{Kind: model.CommandRetry},
		retryEvaluated{source: retryLinkedSource, linked: &compiledFlow{}},
	)
	if linkedAttempt.productError != outer || linkedAttempt.retryClassification != nil {
		t.Fatalf("linked attempt inherited marked child across unrelated outer error: %#v", linkedAttempt)
	}
	if got := retryClassificationError(retryInlineSource, []CommandResult{
		exactResult,
		{productError: outer},
	}, outer); got != outer {
		t.Fatalf("later unrelated inline error inherited earlier exact classification: %T %v", got, got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	resets := 0
	state := &executionState{
		compiledChildren: []compiledDispatch{{command: batch2APressKeyCommand("LOCK")}},
		executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
			attempts++
			cancel()
			return []CommandResult{exactResult}, raw
		},
		metadataUpdatedFn: func(context.Context, CommandMetadata) error { return nil },
		commandResetFn: func(context.Context, CommandResult) error {
			resets++
			return nil
		},
	}
	effect, err := executeRetry(ctx, state, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRetry, Children: []model.Command{batch2APressKeyCommand("LOCK")}},
		value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
	})
	if err != context.Canceled || attempts != 1 || resets != 0 || effect.numberOfRuns != 1 {
		t.Fatalf("cancellation inherited retry classification = effect %#v error %T %v attempts/resets %d/%d", effect, err, err, attempts, resets)
	}
}

func TestInteractionBatch2AEarlyNonRetryableExactErrorDoesNotPropagateBeforeExhaustion(t *testing.T) {
	t.Parallel()

	published := NewConfigurationError("authenticated early terminal Driver failure", nil)
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: published}}})
	child := batch2APressKeyCommand("LOCK")
	retry := model.Command{
		Kind: model.CommandRetry, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"maxRetries": int64(2),
			"commands":   []any{map[string]any{string(model.CommandPressKey): "LOCK"}},
		},
		Children: []model.Command{child},
	}
	compiled := compileBatch2AExactTestCommand(t, retry, retryHandlerSpec(), pressKeyHandlerSpec())
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-early-nonretryable", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, batch2AExactTestRoot(compiled, "/workspace/batch2a-early-nonretryable.yaml"), "batch2a-early-nonretryable/root-run-000001")
	if runErr != published || result.ProductError() != published || len(batch2APressKeyRequests(driver.Actions())) != 1 {
		t.Fatalf("early non-retryable terminal = result %#v error %T %v actions %#v", result, runErr, runErr, driver.Actions())
	}
	var retryResult *CommandResult
	for _, command := range result.Commands() {
		if command.Command().Kind == model.CommandRetry {
			command := command
			retryResult = &command
			break
		}
	}
	if retryResult == nil || retryResult.ProductError() != published || retryResult.Metadata().NumberOfRuns() != 1 {
		t.Fatalf("early non-retryable retry result = %#v, want one run and exact published Driver error", retryResult)
	}
}

func TestInteractionBatch2APostDispatchCancellationWinsOverExactDriverError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	raw := &batch2ACancelOnInspectionError{cancel: cancel}
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: raw}}})
	compiled := compileBatch2AExactTestCommand(t, batch2APressKeyCommand("LOCK"), pressKeyHandlerSpec())
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(ctx, Dependencies{
		ExecutionID: "batch2a-cancel-inspection", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, batch2AExactTestRoot(compiled, "/workspace/batch2a-cancel-inspection.yaml"), "batch2a-cancel-inspection/root-run-000001")
	commands := result.Commands()
	if runErr != context.Canceled || result.ProductError() != context.Canceled || len(commands) != 1 || commands[0].ProductError() != context.Canceled {
		t.Fatalf("cancellation result = run %T %v flow %T %v commands %#v", runErr, runErr, result.ProductError(), result.ProductError(), commands)
	}
	if result.Outcome() != Cancelled || commands[0].Outcome() != Cancelled || len(batch2APressKeyRequests(driver.Actions())) != 1 {
		t.Fatalf("cancellation lifecycle = result %#v commands %#v actions %#v", result, commands, driver.Actions())
	}
	for _, event := range events {
		if event.ProductError() != nil && event.ProductError() != context.Canceled {
			t.Fatalf("%s event published %T %v, want exact cancellation", event.Kind(), event.ProductError(), event.ProductError())
		}
	}
}

func TestInteractionBatch2AExactDriverErrorRetriesAndPublishesExactIdentity(t *testing.T) {
	t.Parallel()

	raw := errors.New("plain retryable Driver failure")
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: raw}, {Err: raw}}})
	child := batch2APressKeyCommand("LOCK")
	retry := model.Command{
		Kind: model.CommandRetry, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"maxRetries": int64(1),
			"commands":   []any{map[string]any{string(model.CommandPressKey): "LOCK"}},
		},
		Children: []model.Command{child},
	}
	compiled := compileBatch2AExactTestCommand(t, retry, retryHandlerSpec(), pressKeyHandlerSpec())
	events := make([]Event, 0, 10)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-exact-retry", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, batch2AExactTestRoot(compiled, "/workspace/batch2a-exact-retry.yaml"), "batch2a-exact-retry/root-run-000001")
	if len(batch2APressKeyRequests(driver.Actions())) != 2 {
		t.Fatalf("PressKey attempts = %d, want two; actions %#v", len(batch2APressKeyRequests(driver.Actions())), driver.Actions())
	}
	commands := result.Commands()
	pressResults := make([]CommandResult, 0, 2)
	for _, command := range commands {
		if command.Command().Kind == model.CommandPressKey {
			pressResults = append(pressResults, command)
		}
	}
	if len(pressResults) != 2 || pressResults[0].ProductError() != raw || pressResults[1].ProductError() != raw {
		t.Fatalf("pressKey result identities = %#v, want exact raw error on both exhausted attempts", pressResults)
	}
	for _, event := range events {
		command, ok := event.Command()
		if ok && command.Kind == model.CommandPressKey && event.Kind() == EventCommandFinished && event.ProductError() != raw {
			t.Fatalf("pressKey finished event error = %T %v, want exact raw", event.ProductError(), event.ProductError())
		}
	}
	assertBatch2AIdentitySafeRetrySurfaces(t, result, runErr, events, raw)
}

func TestInteractionBatch2AExactDriverErrorRetainsFailureEvidenceAndIdentity(t *testing.T) {
	t.Parallel()

	raw := errors.New("plain evidence-eligible Driver failure")
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{
		PressKey:       []enginetest.Result[struct{}]{{Err: raw}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: []byte("exact-png")}},
	})
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{
		result: validFailureEvidenceResult("exact/failure.png", int64(len("exact-png"))),
	}}}
	compiled := compileBatch2AExactTestCommand(t, batch2APressKeyCommand("LOCK"), pressKeyHandlerSpec())
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-exact-evidence", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{}, ArtifactSink: sink,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, batch2AExactTestRoot(compiled, "/workspace/batch2a-exact-evidence.yaml"), "batch2a-exact-evidence/root-run-000001")
	commands := result.Commands()
	if runErr != raw || result.ProductError() != raw || len(commands) != 1 || commands[0].ProductError() != raw {
		t.Fatalf("exact evidence identity = run %T %v flow %T %v commands %#v", runErr, runErr, result.ProductError(), result.ProductError(), commands)
	}
	if len(commands[0].Artifacts()) != 1 || commands[0].Artifacts()[0].Path != "exact/failure.png" || len(sink.Requests()) != 1 {
		t.Fatalf("exact failure evidence = artifacts %#v writes %#v", commands[0].Artifacts(), sink.Requests())
	}
	for _, event := range events {
		if event.ProductError() != nil && event.ProductError() != raw {
			t.Fatalf("%s event identity = %T %v, want exact raw", event.Kind(), event.ProductError(), event.ProductError())
		}
	}
}

func TestInteractionBatch2AMalformedExactErrorCannotGainRetryOrEvidence(t *testing.T) {
	t.Parallel()

	malformed := &batch2ACyclicDriverError{}
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{
		PressKey:       []enginetest.Result[struct{}]{{Err: malformed}, {Err: malformed}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: []byte("must-not-capture")}},
	})
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{
		result: validFailureEvidenceResult("unexpected.png", int64(len("must-not-capture"))),
	}}}
	child := batch2APressKeyCommand("LOCK")
	retry := model.Command{
		Kind: model.CommandRetry, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"maxRetries": int64(1),
			"commands":   []any{map[string]any{string(model.CommandPressKey): "LOCK"}},
		},
		Children: []model.Command{child},
	}
	compiled := compileBatch2AExactTestCommand(t, retry, retryHandlerSpec(), pressKeyHandlerSpec())
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-malformed-exact", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{}, ArtifactSink: sink,
	}, batch2AExactTestRoot(compiled, "/workspace/batch2a-malformed-exact.yaml"), "batch2a-malformed-exact/root-run-000001")
	var configuration *ConfigurationError
	if !errors.As(runErr, &configuration) || result.Outcome() != Failed || len(batch2APressKeyRequests(driver.Actions())) != 1 {
		t.Fatalf("malformed exact terminal = result %#v error %T %v actions %#v", result, runErr, runErr, driver.Actions())
	}
	if countBatch2AMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 0 || len(sink.Requests()) != 0 {
		t.Fatalf("malformed exact gained evidence = screenshots %d writes %#v", countBatch2AMethod(driver.Actions(), enginetest.MethodTakeScreenshot), sink.Requests())
	}
}

func TestInteractionBatch2AUnmarkedHandlerRetainsNormalizedTaxonomy(t *testing.T) {
	t.Parallel()

	raw := errors.New("ordinary unmarked handler failure")
	spec := handlerSpec{
		keyword: model.CommandAssertVisible, effectClass: EffectObserved,
		compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, raw
		},
	}
	core, err := newExecutorCoreForRootRun(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		"batch2a-unmarked/root-run-000001", spec,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandAssertVisible}, 0)
	var operation *OperationError
	if !errors.As(executeErr, &operation) || !errors.Is(executeErr, raw) || executeErr == raw || result.ProductError() != executeErr || !IsRetryable(executeErr) {
		t.Fatalf("unmarked taxonomy = result %#v error %T %v", result, executeErr, executeErr)
	}
}

func compileBatch2AExactTestCommand(t *testing.T, command model.Command, specs ...handlerSpec) compiledDispatch {
	t.Helper()
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch2a-exact.yaml"}, command,
	)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func batch2AExactTestRoot(compiled compiledDispatch, path string) *compiledFlow {
	return &compiledFlow{
		path:   path,
		config: model.Config{AppID: "com.example.batch2a"},
		body:   []compiledDispatch{compiled},
	}
}

type batch2ACancelOnInspectionError struct {
	once   sync.Once
	cancel context.CancelFunc
}

type batch2AOpaqueDriverError map[string]int

func (batch2AOpaqueDriverError) Error() string { return "valid non-identitySafe Driver error" }

func assertBatch2AOpaqueAlias(
	t *testing.T,
	published error,
	raw batch2AOpaqueDriverError,
	key string,
	value int,
) {
	t.Helper()
	alias, ok := published.(batch2AOpaqueDriverError)
	if !ok {
		t.Fatalf("%s non-identitySafe publication = %T %v, want batch2AOpaqueDriverError", key, published, published)
	}
	alias[key] = value
	if raw[key] != value {
		t.Fatalf("%s non-identitySafe publication did not retain shared map identity: raw %#v", key, raw)
	}
}

func assertBatch2AIdentitySafeRetrySurfaces(
	t *testing.T,
	result FlowResult,
	runErr error,
	events []Event,
	raw error,
) {
	t.Helper()
	if runErr != raw || result.ProductError() != raw || result.Outcome() != Failed {
		t.Fatalf("identitySafe retry terminal = returned %T %v flow %T %v outcome %s, want exact Driver object and Failed", runErr, runErr, result.ProductError(), result.ProductError(), result.Outcome())
	}
	assertBatch2ARetryCommandAndEvents(t, result, events, func(surface string, published error) {
		t.Helper()
		if published != raw {
			t.Fatalf("%s error = %T %v, want exact Driver object", surface, published, published)
		}
	})
}

func assertBatch2AOpaqueRetrySurfaces(
	t *testing.T,
	result FlowResult,
	runErr error,
	events []Event,
	raw batch2AOpaqueDriverError,
	prefix string,
	seed int,
) {
	t.Helper()
	if result.Outcome() != Failed {
		t.Fatalf("%s non-identitySafe retry result = %#v, want Failed", prefix, result)
	}
	assertBatch2AOpaqueAlias(t, runErr, raw, prefix+"-returned", seed)
	assertBatch2AOpaqueAlias(t, result.ProductError(), raw, prefix+"-flow", seed+1)
	next := seed + 2
	assertBatch2ARetryCommandAndEvents(t, result, events, func(surface string, published error) {
		assertBatch2AOpaqueAlias(t, published, raw, prefix+"-"+surface, next)
		next++
	})
}

func assertBatch2ARetryCommandAndEvents(
	t *testing.T,
	result FlowResult,
	events []Event,
	assertError func(string, error),
) {
	t.Helper()
	retryResults := 0
	for _, command := range result.Commands() {
		if command.Command().Kind == model.CommandRetry {
			retryResults++
			assertError("retry-command", command.ProductError())
		}
	}
	retryEvents := 0
	flowEvents := 0
	for _, event := range events {
		command, hasCommand := event.Command()
		if event.Kind() == EventCommandFinished && hasCommand && command.Kind == model.CommandRetry {
			retryEvents++
			assertError("retry-event", event.ProductError())
		}
		if event.Kind() == EventFlowFinished && event.Depth() == result.Depth() {
			flowEvents++
			assertError("flow-event", event.ProductError())
		}
	}
	if retryResults != 1 || retryEvents != 1 || flowEvents != 1 {
		t.Fatalf("outer retry surfaces = results %d retry events %d flow events %d; commands %#v events %#v", retryResults, retryEvents, flowEvents, result.Commands(), events)
	}
}

func (*batch2ACancelOnInspectionError) Error() string { return "cancel during dispatcher inspection" }

func (err *batch2ACancelOnInspectionError) Unwrap() error {
	err.once.Do(err.cancel)
	return nil
}
