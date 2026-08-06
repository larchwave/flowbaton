package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

const batch5CoreIntegrityMessage = "execution session integrity lost after copied text update failed"

func TestInteractionBatch5CoreSessionPoisonIsStableAcrossAllEntryPoints(t *testing.T) {
	setterFailure := errors.New("setter mutated then failed")
	closeFailure := errors.New("runtime close failed")
	runtime := &sessionRuntime{closeErr: closeFailure}
	root := &compiledFlow{path: "/workspace/batch5-core-root.yaml", config: model.Config{AppID: "com.example.root"}}
	child := &compiledFlow{path: "/workspace/batch5-core-child.yaml", config: model.Config{AppID: "com.example.child"}}
	session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	rootScope, err := session.enterFlow(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("enter root flow: %v", err)
	}
	childScope, err := session.enterFlow(context.Background(), child, nil)
	if err != nil {
		t.Fatalf("enter child flow: %v", err)
	}
	if err := session.setCopiedText("retained"); err != nil {
		t.Fatalf("seed copied text: %v", err)
	}

	runtime.SetCopiedError(setterFailure)
	primary := session.setCopiedText("uncommitted")
	if primary == nil {
		t.Fatal("mutating setter failure returned nil")
	}
	assertBatch5CoreIntegrityPrimary(t, primary, setterFailure)

	_, copiedValues, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Errorf("runtime close calls immediately after poison = %d, want one", closeCalls)
	}
	if !reflect.DeepEqual(copiedValues, []string{"retained", "uncommitted"}) {
		t.Errorf("runtime copied values = %#v", copiedValues)
	}
	session.mu.Lock()
	retained := session.copiedText
	session.mu.Unlock()
	if retained != "retained" {
		t.Errorf("session copied text = %q, want retained prior value", retained)
	}

	assertSame := func(surface string, got error) {
		t.Helper()
		if got != primary {
			t.Errorf("%s error = %T %v, want exact primary %p", surface, got, got, primary)
		}
	}
	_, surfaceErr := session.jsRuntime()
	assertSame("jsRuntime", surfaceErr)
	_, surfaceErr = session.elementLookup()
	assertSame("elementLookup", surfaceErr)
	_, surfaceErr = session.currentConfig()
	assertSame("currentConfig", surfaceErr)
	_, surfaceErr = session.currentAppID()
	assertSame("currentAppID", surfaceErr)
	_, surfaceErr = session.copiedTextValue()
	assertSame("copiedTextValue", surfaceErr)
	assertSame("later setCopiedText", session.setCopiedText("must-not-reach-runtime"))
	laterScope, surfaceErr := session.enterFlow(context.Background(), child, nil)
	assertSame("later enterFlow", surfaceErr)
	if laterScope != nil {
		assertSame("unexpected later scope Close", laterScope.Close())
	}
	assertSame("child scope Close", childScope.Close())
	assertSame("child scope idempotent Close", childScope.Close())
	assertSame("root scope Close", rootScope.Close())
	assertSame("root scope idempotent Close", rootScope.Close())
	assertSame("session Close", session.Close())
	assertSame("session idempotent Close", session.Close())

	session.mu.Lock()
	remainingFrames := len(session.frames)
	session.mu.Unlock()
	if remainingFrames != 0 {
		t.Errorf("remaining flow frames = %d, want zero", remainingFrames)
	}
	if got := runtime.EnvCalls(); !reflect.DeepEqual(got, concatEnvCalls(
		flowEnvCalls("/workspace/batch5-core-root.yaml"),
		flowEnvCalls("/workspace/batch5-core-child.yaml"))) {
		t.Errorf("runtime environment calls after poison = %#v, want pushes without PopEnv", got)
	}
	_, copiedValues, closeCalls = runtime.Snapshot()
	if closeCalls != 1 {
		t.Errorf("runtime close calls after idempotent cleanup = %d, want one", closeCalls)
	}
	if !reflect.DeepEqual(copiedValues, []string{"retained", "uncommitted"}) {
		t.Errorf("later poisoned entry point reached runtime: %#v", copiedValues)
	}
}

func TestInteractionBatch5CorePoisonCutsOffNestedAndSelectedLifecycle(t *testing.T) {
	setterFailure := errors.New("nested setter failed")
	closeFailure := errors.New("nested runtime close failed")
	runtime := &sessionRuntime{closeErr: closeFailure}
	order := make([]string, 0, 8)
	events := make([]Event, 0, 8)
	resolverCalls := 0
	optional := true

	poison := batch5CoreSetDispatch("poison", "mutated", runtime, setterFailure, &order)
	poison.command.Optional = &optional
	child := &compiledFlow{
		path:       "/workspace/batch5-core-child.yaml",
		body:       []compiledDispatch{poison, rootPolicyDispatch("child-sibling", nil, &order)},
		onComplete: []compiledDispatch{rootPolicyDispatch("child-completion", nil, &order)},
	}
	var childResult FlowResult
	outer := compiledDispatch{
		command: model.Command{Kind: model.CommandRunFlow, Arguments: "outer"},
		spec: handlerSpec{
			keyword: model.CommandRunFlow, effectClass: EffectComposite, evaluate: identityEvaluator,
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				order = append(order, "outer-start")
				var executeErr error
				childResult, executeErr = state.executeFlow(ctx, child, nil)
				order = append(order, "outer-return")
				return commandEffect{effectClass: EffectComposite}, executeErr
			},
		},
		value: child,
	}
	root := &compiledFlow{
		path:       "/workspace/batch5-core-root.yaml",
		body:       []compiledDispatch{outer, rootPolicyDispatch("root-sibling", nil, &order)},
		onComplete: []compiledDispatch{rootPolicyDispatch("root-completion", nil, &order)},
	}
	dependencies := flowExecutorDependencies(runtime, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})})
	dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
		resolverCalls++
		return FailureDecisionContinue
	})

	result, runErr := executeCompiledRoot(context.Background(), dependencies, root)
	if runErr == nil {
		t.Fatal("poisoned root error = nil")
	}
	assertBatch5CoreIntegrityPrimary(t, runErr, setterFailure)
	if result.ProductError() != runErr || childResult.ProductError() != runErr {
		t.Errorf("flow primary identity = root %p child %p returned %p", result.ProductError(), childResult.ProductError(), runErr)
	}
	if result.Outcome() != Failed || childResult.Outcome() != Failed {
		t.Errorf("flow outcomes = root %q child %q, want Failed", result.Outcome(), childResult.Outcome())
	}
	if resolverCalls != 0 {
		t.Errorf("failure resolver calls = %d, want zero", resolverCalls)
	}
	if want := []string{"outer-start", "poison", "outer-return"}; !reflect.DeepEqual(order, want) {
		t.Errorf("lifecycle order = %#v, want %#v", order, want)
	}

	commands := result.Commands()
	if len(commands) != 2 {
		t.Fatalf("root commands = %#v, want poison and open outer only", commands)
	}
	for _, command := range commands {
		if command.ProductError() != runErr || command.Outcome() != Failed {
			t.Errorf("command result = outcome %q error %p, want Failed exact primary", command.Outcome(), command.ProductError())
		}
	}
	childCommands := childResult.Commands()
	if len(childCommands) != 1 || childCommands[0].ProductError() != runErr || childCommands[0].Outcome() != Failed {
		t.Errorf("child commands = %#v, want one exact failed poison", childCommands)
	}

	wantKinds := []EventKind{
		EventFlowStarted,
		EventCommandStarted,
		EventFlowStarted,
		EventCommandStarted,
		EventCommandFinished,
		EventFlowFinished,
		EventCommandFinished,
		EventFlowFinished,
	}
	gotKinds := make([]EventKind, len(events))
	startedNames := make([]string, 0, 2)
	for index, event := range events {
		gotKinds[index] = event.Kind()
		if event.Kind() == EventCommandStarted {
			command, ok := event.Command()
			if !ok {
				t.Errorf("command-start event %d has no command", index)
				continue
			}
			startedNames = append(startedNames, command.Arguments.(string))
		}
		if event.Kind() == EventCommandFinished || event.Kind() == EventFlowFinished {
			if event.ProductError() != runErr || event.Outcome() != Failed {
				t.Errorf("terminal event %s = outcome %q error %p, want Failed exact primary", event.Kind(), event.Outcome(), event.ProductError())
			}
		}
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Errorf("event kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	if want := []string{"outer", "poison"}; !reflect.DeepEqual(startedNames, want) {
		t.Errorf("started command spans = %#v, want %#v", startedNames, want)
	}
	if got := runtime.EnvCalls(); !reflect.DeepEqual(got, concatEnvCalls(
		flowEnvCalls("/workspace/batch5-core-root.yaml"),
		flowEnvCalls("/workspace/batch5-core-child.yaml"))) {
		t.Errorf("poisoned environment calls = %#v", got)
	}
	_, copiedValues, closeCalls := runtime.Snapshot()
	if closeCalls != 1 || !reflect.DeepEqual(copiedValues, []string{"mutated"}) {
		t.Errorf("poisoned runtime = copied %#v close calls %d", copiedValues, closeCalls)
	}
}

func TestInteractionBatch5CoreResolverCutoffAndMarkerPredicateAreExact(t *testing.T) {
	ordinary := NewConfigurationError("continued ordinary configuration failure", nil)
	setterFailure := errors.New("setter failure after continued ordinary error")
	runtime := &sessionRuntime{}
	order := make([]string, 0, 4)
	root := &compiledFlow{
		path: "/workspace/batch5-core-resolver-cutoff.yaml",
		body: []compiledDispatch{
			rootPolicyDispatch("ordinary", ordinary, &order),
			batch5CoreSetDispatch("poison", "poison", runtime, setterFailure, &order),
			rootPolicyDispatch("must-not-run", nil, &order),
		},
		onComplete: []compiledDispatch{rootPolicyDispatch("must-not-complete", nil, &order)},
	}
	resolverCalls := 0
	dependencies := flowExecutorDependencies(runtime, nil)
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
		resolverCalls++
		if result.ProductError() != ordinary {
			t.Errorf("resolver received %T %v, want ordinary configuration error", result.ProductError(), result.ProductError())
		}
		return FailureDecisionContinue
	})

	result, runErr := executeCompiledRoot(context.Background(), dependencies, root)
	if runErr == nil {
		t.Fatal("poison after ordinary failure returned nil")
	}
	assertBatch5CoreIntegrityPrimary(t, runErr, setterFailure)
	if resolverCalls != 1 || !reflect.DeepEqual(order, []string{"ordinary", "poison"}) {
		t.Errorf("resolver cutoff = calls %d order %#v", resolverCalls, order)
	}
	if result.ProductError() != runErr || result.Outcome() != Failed {
		t.Errorf("promoted root primary = outcome %q product %p returned %p", result.Outcome(), result.ProductError(), runErr)
	}
	commands := result.Commands()
	if len(commands) != 2 || commands[0].ProductError() != ordinary || commands[1].ProductError() != runErr {
		t.Errorf("chronological command evidence = %#v", commands)
	}
	if !isSessionIntegrityError(runErr) || !failureResolverBypassed(runErr) {
		t.Errorf("exact marker was not recognized/bypassed: %T", runErr)
	}
	if isSessionIntegrityError(ordinary) || failureResolverBypassed(ordinary) {
		t.Errorf("ordinary ConfigurationError was treated as session-integrity poison")
	}
	wrapped := NewOperationError("external-looking causal wrapper", runErr)
	if isSessionIntegrityError(wrapped) {
		t.Errorf("causal wrapper forged exact session-integrity identity")
	}
	marker, ok := runErr.(*sessionIntegrityError)
	if !ok {
		t.Fatalf("root primary type = %T, want private session-integrity marker", runErr)
	}
	if marker.As(nil) {
		t.Error("marker.As(nil) = true")
	}
	var deviceTarget *DeviceConnectionError
	if marker.As(&deviceTarget) || deviceTarget != nil {
		t.Errorf("marker matched wrong As target: %#v", deviceTarget)
	}
	var nilConfigurationTarget **ConfigurationError
	if marker.As(nilConfigurationTarget) {
		t.Error("marker matched typed-nil As target")
	}
}

func TestInteractionBatch5CoreExactPrimaryOrdering(t *testing.T) {
	t.Run("poison before late cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		runtime := &sessionRuntime{afterCopiedValue: "late-cancel", afterCopied: cancel}
		setterFailure := errors.New("setter failed before cancellation observation")
		root := &compiledFlow{
			path: "/workspace/batch5-core-late-cancel.yaml",
			body: []compiledDispatch{batch5CoreSetDispatch("late-cancel", "late-cancel", runtime, setterFailure, nil)},
		}
		result, runErr := executeCompiledRoot(ctx, flowExecutorDependencies(runtime, nil), root)
		if runErr == nil {
			t.Fatal("late-cancel poison error = nil")
		}
		assertBatch5CoreIntegrityPrimary(t, runErr, setterFailure)
		if errors.Is(runErr, context.Canceled) || result.Outcome() != Failed || result.ProductError() != runErr {
			t.Errorf("late-cancel result = outcome %q product %p returned %T %v", result.Outcome(), result.ProductError(), runErr, runErr)
		}
		commands := result.Commands()
		if len(commands) != 1 || commands[0].ProductError() != runErr || commands[0].Outcome() != Failed {
			t.Errorf("late-cancel commands = %#v", commands)
		}
	})

	t.Run("ordinary configuration still yields to late cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		ordinary := NewConfigurationError("ordinary handler failure", nil)
		runtime := &sessionRuntime{}
		dispatch := compiledDispatch{
			command: model.Command{Kind: model.CommandAction, Arguments: "ordinary"},
			spec: handlerSpec{
				keyword: model.CommandAction, effectClass: EffectHostMutation, evaluate: identityEvaluator,
				execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
					cancel()
					return commandEffect{effectClass: EffectHostMutation}, ordinary
				},
			},
			value: "ordinary",
		}
		result, runErr := executeCompiledRoot(ctx, flowExecutorDependencies(runtime, nil), &compiledFlow{
			path: "/workspace/batch5-core-ordinary-late-cancel.yaml", body: []compiledDispatch{dispatch},
		})
		if runErr != context.Canceled || result.ProductError() != context.Canceled || result.Outcome() != Cancelled {
			t.Errorf("ordinary late cancellation = outcome %q product %T returned %T", result.Outcome(), result.ProductError(), runErr)
		}
	})

	t.Run("pre-cancellation prevents runtime construction and setter", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		runtime := &sessionRuntime{}
		result, runErr := executeCompiledRoot(ctx, flowExecutorDependencies(runtime, nil), &compiledFlow{
			path: "/workspace/batch5-core-pre-cancel.yaml",
			body: []compiledDispatch{batch5CoreSetDispatch("must-not-run", "must-not-run", runtime, errors.New("unused"), nil)},
		})
		_, copiedValues, closeCalls := runtime.Snapshot()
		if runErr != context.Canceled || result.ProductError() != nil || len(copiedValues) != 0 || closeCalls != 0 {
			t.Errorf("pre-cancel = result %#v error %T copied %#v closes %d", result, runErr, copiedValues, closeCalls)
		}
	})

	t.Run("device primary prevents setter", func(t *testing.T) {
		deviceFailure := NewDeviceConnectionError("device unavailable", nil)
		runtime := &sessionRuntime{}
		dependencies := flowExecutorDependencies(runtime, nil)
		dependencies.Controller = ControllerFunc(func(context.Context) error { return deviceFailure })
		result, runErr := executeCompiledRoot(context.Background(), dependencies, &compiledFlow{
			path: "/workspace/batch5-core-device-primary.yaml",
			body: []compiledDispatch{batch5CoreSetDispatch("must-not-run", "must-not-run", runtime, errors.New("unused"), nil)},
		})
		var classified *DeviceConnectionError
		_, copiedValues, closeCalls := runtime.Snapshot()
		if !errors.As(runErr, &classified) || classified != deviceFailure || result.ProductError() != runErr ||
			len(copiedValues) != 0 || closeCalls != 1 {
			t.Errorf("device primary = result %#v error %T copied %#v closes %d", result, runErr, copiedValues, closeCalls)
		}
	})
}

func TestInteractionBatch5CoreIndependentRootIsolation(t *testing.T) {
	type rootRun struct {
		observed string
		result   FlowResult
		err      error
	}
	run := func(runtime *sessionRuntime, value string, setterFailure error) rootRun {
		observed := ""
		dispatch := batch5CoreSetDispatch(value, value, runtime, setterFailure, nil)
		originalExecute := dispatch.spec.execute
		dispatch.spec.execute = func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
			var readErr error
			observed, readErr = state.copiedTextValue()
			if readErr != nil {
				return commandEffect{effectClass: EffectHostMutation}, readErr
			}
			return originalExecute(ctx, state, evaluated)
		}
		result, err := executeCompiledRoot(context.Background(), flowExecutorDependencies(runtime, nil), &compiledFlow{
			path: "/workspace/batch5-core-" + value + ".yaml", body: []compiledDispatch{dispatch},
		})
		return rootRun{observed: observed, result: result, err: err}
	}

	completedRuntime := &sessionRuntime{}
	completed := run(completedRuntime, "completed-before", nil)
	if completed.err != nil || completed.result.Outcome() != Completed || completed.observed != "" {
		t.Fatalf("completed root = %#v", completed)
	}
	poisonRuntime := &sessionRuntime{}
	setterFailure := errors.New("isolated poison")
	poisoned := run(poisonRuntime, "poisoned", setterFailure)
	if poisoned.err == nil || poisoned.observed != "" {
		t.Fatalf("poisoned root = %#v", poisoned)
	}
	assertBatch5CoreIntegrityPrimary(t, poisoned.err, setterFailure)
	laterRuntime := &sessionRuntime{}
	later := run(laterRuntime, "later-independent", nil)
	if later.err != nil || later.result.Outcome() != Completed || later.observed != "" {
		t.Fatalf("later independent root = %#v", later)
	}

	concurrentRuntimes := []*sessionRuntime{{}, {}}
	concurrentFailures := []error{errors.New("concurrent poison"), nil}
	concurrent := make([]rootRun, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := range concurrent {
		index := index
		go func() {
			defer wait.Done()
			concurrent[index] = run(concurrentRuntimes[index], []string{"concurrent-poison", "concurrent-healthy"}[index], concurrentFailures[index])
		}()
	}
	wait.Wait()
	if concurrent[0].err == nil || concurrent[0].observed != "" {
		t.Errorf("concurrent poisoned root = %#v", concurrent[0])
	} else {
		assertBatch5CoreIntegrityPrimary(t, concurrent[0].err, concurrentFailures[0])
	}
	if concurrent[1].err != nil || concurrent[1].result.Outcome() != Completed || concurrent[1].observed != "" {
		t.Errorf("concurrent healthy root = %#v", concurrent[1])
	}
	for index, runtime := range append([]*sessionRuntime{completedRuntime, poisonRuntime, laterRuntime}, concurrentRuntimes...) {
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Errorf("root runtime %d close calls = %d, want one", index, closeCalls)
		}
	}
}

func TestInteractionBatch5CoreConcurrentPoisonedEntryPointsKeepOnePrimary(t *testing.T) {
	runtime := &sessionRuntime{}
	session, err := newTestExecutionSession(t, context.Background(), runtime)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	if err := session.setCopiedText("retained"); err != nil {
		t.Fatalf("seed copied text: %v", err)
	}
	setterFailure := errors.New("concurrent setter failure")
	runtime.SetCopiedError(setterFailure)
	setterErrors := make(chan error, 64)
	start := make(chan struct{})
	var setters sync.WaitGroup
	setters.Add(64)
	for index := 0; index < 64; index++ {
		go func() {
			defer setters.Done()
			<-start
			setterErrors <- session.setCopiedText("uncommitted")
		}()
	}
	close(start)
	setters.Wait()
	close(setterErrors)
	var primary error
	for seen := range setterErrors {
		if seen == nil {
			t.Error("concurrent setter returned nil")
			continue
		}
		if primary == nil {
			primary = seen
			continue
		}
		if seen != primary {
			t.Errorf("concurrent setter primary = %p, want %p", seen, primary)
		}
	}
	if primary == nil {
		t.Fatal("concurrent poison primary = nil")
	}
	assertBatch5CoreIntegrityPrimary(t, primary, setterFailure)

	errorsSeen := make(chan error, 256)
	var wait sync.WaitGroup
	for index := 0; index < 256; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			switch index % 8 {
			case 0:
				errorsSeen <- session.setCopiedText("later")
			case 1:
				_, err := session.copiedTextValue()
				errorsSeen <- err
			case 2:
				_, err := session.jsRuntime()
				errorsSeen <- err
			case 3:
				_, err := session.elementLookup()
				errorsSeen <- err
			case 4:
				_, err := session.currentConfig()
				errorsSeen <- err
			case 5:
				_, err := session.currentAppID()
				errorsSeen <- err
			case 6:
				_, err := session.enterFlow(context.Background(), session.root, nil)
				errorsSeen <- err
			default:
				errorsSeen <- session.Close()
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for seen := range errorsSeen {
		if seen != primary {
			t.Errorf("concurrent entry point error = %T %v, want exact primary", seen, seen)
		}
	}
	session.mu.Lock()
	retained := session.copiedText
	session.mu.Unlock()
	_, copiedValues, closeCalls := runtime.Snapshot()
	if retained != "retained" || closeCalls != 1 || !reflect.DeepEqual(copiedValues, []string{"retained", "uncommitted"}) {
		t.Errorf("concurrent poison = retained %q copied %#v closes %d", retained, copiedValues, closeCalls)
	}
}

func TestInteractionBatch5CoreSetupAndMalformedControls(t *testing.T) {
	t.Run("session setup never touches the setter", func(t *testing.T) {
		// `flowbaton.copiedText` remains undefined until a command copies text,
		// so a poisoned setter cannot break session construction.
		runtime := &sessionRuntime{copiedErr: errors.New("setter must not be reached during setup")}
		session, err := newTestExecutionSession(t, context.Background(), runtime)
		_, copiedValues, closeCalls := runtime.Snapshot()
		if session == nil || err != nil || len(copiedValues) != 0 || closeCalls != 0 {
			t.Errorf("setup = session %#v error %v copied %#v closes %d", session, err, copiedValues, closeCalls)
		}
	})

	t.Run("typed nil runtime remains rejected", func(t *testing.T) {
		var runtime *sessionRuntime
		session, err := newTestExecutionSession(t, context.Background(), runtime)
		var configuration *ConfigurationError
		if session != nil || !errors.As(err, &configuration) {
			t.Errorf("typed-nil runtime = session %#v error %T %v", session, err, err)
		}
	})

	for _, test := range []struct {
		name string
		err  func() error
	}{
		{name: "panicking As diagnostic", err: func() error { return &panickingAsError{} }},
		{name: "typed nil diagnostic", err: func() error {
			var malformed *OperationError
			return malformed
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := &sessionRuntime{}
			session, err := newTestExecutionSession(t, context.Background(), runtime)
			if err != nil {
				t.Fatalf("newExecutionSession() error: %v", err)
			}
			raw := test.err()
			runtime.SetCopiedError(raw)
			primary := session.setCopiedText("malformed")
			if primary == nil {
				t.Fatal("malformed setter diagnostic returned nil")
			}
			matched, configuration, panicked := batch5CoreConfigurationMatch(primary)
			if panicked || !matched || configuration == nil || errors.Unwrap(configuration) != nil {
				t.Errorf("marker classification = matched %t configuration %#v panicked %t", matched, configuration, panicked)
			}
			if primary == raw || primary.Error() != batch5CoreIntegrityMessage {
				t.Errorf("malformed diagnostic escaped = primary %T %v raw %T", primary, primary, raw)
			}
			if err := session.Close(); err != primary {
				t.Errorf("session Close() = %T %v, want exact primary", err, err)
			}
			_, _, closeCalls := runtime.Snapshot()
			if closeCalls != 1 {
				t.Errorf("runtime close calls = %d, want one", closeCalls)
			}
		})
	}
}

func batch5CoreSetDispatch(
	name string,
	value string,
	runtime *sessionRuntime,
	setterFailure error,
	order *[]string,
) compiledDispatch {
	return compiledDispatch{
		command: model.Command{Kind: model.CommandAction, Arguments: name},
		spec: handlerSpec{
			keyword: model.CommandAction, effectClass: EffectHostMutation, evaluate: identityEvaluator,
			execute: func(_ context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				if order != nil {
					*order = append(*order, name)
				}
				runtime.SetCopiedError(setterFailure)
				return commandEffect{effectClass: EffectHostMutation}, state.setCopiedText(value)
			},
		},
		value: value,
	}
}

func assertBatch5CoreIntegrityPrimary(t *testing.T, primary error, raw error) {
	t.Helper()
	matched, configuration, panicked := batch5CoreConfigurationMatch(primary)
	if panicked || !matched || configuration == nil {
		t.Errorf("primary %T did not safely expose ConfigurationError: matched %t panicked %t", primary, matched, panicked)
	} else if errors.Unwrap(configuration) != nil {
		t.Errorf("direct ConfigurationError cause = %T, want nil", errors.Unwrap(configuration))
	}
	if primary == raw || errors.Is(primary, raw) {
		t.Errorf("primary %T exposes raw setter diagnostic %T", primary, raw)
	}
	if primary.Error() != batch5CoreIntegrityMessage {
		t.Errorf("primary message = %q, want %q", primary.Error(), batch5CoreIntegrityMessage)
	}
	if _, causal := primary.(interface{ Unwrap() error }); causal {
		t.Errorf("primary %T exposes causal Unwrap", primary)
	}
	if ClassifyOutcome(primary, true) != Failed {
		t.Errorf("optional primary outcome = %q, want Failed", ClassifyOutcome(primary, true))
	}
}

func batch5CoreConfigurationMatch(err error) (matched bool, configuration *ConfigurationError, panicked bool) {
	defer func() {
		if recover() != nil {
			matched = false
			configuration = nil
			panicked = true
		}
	}()
	matched = errors.As(err, &configuration)
	return matched, configuration, false
}
