package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

const (
	batch4BRequiredServiceNone           = uint64(0)
	batch4BRequiredServiceInputGenerator = uint64(1)
)

func TestInteractionBatch4BCoreRequiredServiceDeclarationIsClosed(t *testing.T) {
	t.Parallel()

	base := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil)
	if _, err := newHandlerRegistry(base); err != nil {
		t.Fatalf("no-requirement registry error: %v", err)
	}
	required := batch4BObservedSpec(t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator, nil)
	if _, err := newHandlerRegistry(required); err != nil {
		t.Fatalf("input-generator registry error: %v", err)
	}

	for _, invalid := range []uint64{
		2,
		batch4BRequiredServiceInputGenerator | 2,
		255,
	} {
		spec := batch4BSetRequiredService(t, base, invalid)
		if _, err := newHandlerRegistry(spec); !batch4BIsConfigurationError(err) {
			t.Fatalf("registry accepted unknown required service %#x: %T %v", invalid, err, err)
		}
	}
}

func TestInteractionBatch4BCoreMissingGeneratorCutsOffBeforeCommandEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		generator InputGenerator
	}{
		{name: "nil"},
		{name: "typed nil", generator: (*batch4BInputGenerator)(nil)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var controllerCalls atomic.Int64
			var firstEvaluate atomic.Int64
			var requiredEvaluate atomic.Int64
			var laterEvaluate atomic.Int64
			var firstExecute atomic.Int64
			var requiredExecute atomic.Int64
			var laterExecute atomic.Int64
			var commandEvents atomic.Int64
			controller := batch4BController{calls: &controllerCalls}
			dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(100, 0)))
			dependencies.Controller = controller
			dependencies.InputGenerator = test.generator
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
					commandEvents.Add(1)
				}
				return nil
			})}

			first := batch4BCountingSpec(t, model.CommandAction, batch4BRequiredServiceNone, &firstEvaluate, &firstExecute, nil)
			required := batch4BCountingSpec(t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator, &requiredEvaluate, &requiredExecute, nil)
			later := batch4BCountingSpec(t, model.CommandInputRandomNumber, batch4BRequiredServiceNone, &laterEvaluate, &laterExecute, nil)
			core, err := newExecutorCore(dependencies, first, required, later)
			if err != nil {
				t.Fatal(err)
			}

			results, runErr := core.executeSequence(context.Background(), []model.Command{
				{Kind: model.CommandAction},
				{Kind: model.CommandInputRandomText},
				{Kind: model.CommandInputRandomNumber},
			}, 0)
			if !batch4BIsConfigurationError(runErr) {
				t.Fatalf("missing generator error = %T %v, want ConfigurationError", runErr, runErr)
			}
			if len(results) != 1 || results[0].Command().Kind != model.CommandAction || results[0].Outcome() != Completed {
				t.Fatalf("retained results = %#v, want only authenticated completed prefix", results)
			}
			if got := core.ledger.snapshot(); len(got) != 1 || got[0].Sequence() != results[0].Sequence() {
				t.Fatalf("ledger = %#v, want only completed prefix", got)
			}
			if core.timeline.Checkpoint() != 1 {
				t.Fatalf("timeline checkpoint = %d, want one completed command and no offender span", core.timeline.Checkpoint())
			}
			if controllerCalls.Load() != 1 || commandEvents.Load() != 2 {
				t.Fatalf("controller/events = %d/%d, want only first command effects", controllerCalls.Load(), commandEvents.Load())
			}
			if firstEvaluate.Load() != 1 || firstExecute.Load() != 1 ||
				requiredEvaluate.Load() != 0 || requiredExecute.Load() != 0 ||
				laterEvaluate.Load() != 0 || laterExecute.Load() != 0 {
				t.Fatalf("evaluation/execution counts = first %d/%d required %d/%d later %d/%d",
					firstEvaluate.Load(), firstExecute.Load(), requiredEvaluate.Load(), requiredExecute.Load(),
					laterEvaluate.Load(), laterExecute.Load())
			}
			if got := dependencies.Driver.(*enginetest.FakeDriver).Actions(); len(got) != 0 {
				t.Fatalf("Driver effects = %#v, want none", got)
			}
		})
	}
}

func TestInteractionBatch4BCoreCommandsWithoutRequirementIgnoreAbsentGenerator(t *testing.T) {
	t.Parallel()

	var executes atomic.Int64
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		batch4BCountingSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil, &executes, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
	if runErr != nil || result.Outcome() != Completed || executes.Load() != 1 {
		t.Fatalf("optional-service-free command = result %#v error %v executes %d", result, runErr, executes.Load())
	}
}

func TestInteractionBatch4BCorePresentGeneratorSatisfiesGateWithoutCallingGenerator(t *testing.T) {
	t.Parallel()

	generator := &batch4BInputGenerator{}
	dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.InputGenerator = generator
	core, err := newExecutorCore(
		dependencies,
		batch4BCountingSpec(t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator, nil, nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := core.execute(context.Background(), model.Command{Kind: model.CommandInputRandomText}, 0)
	if runErr != nil || result.Outcome() != Completed || generator.calls.Load() != 0 {
		t.Fatalf("present generator gate = outcome %q error %v Generate calls %d", result.Outcome(), runErr, generator.calls.Load())
	}
}

func TestInteractionBatch4BCoreSelectedRootMissingGeneratorHasZeroOffenderEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		generator InputGenerator
	}{
		{name: "nil"},
		{name: "typed nil", generator: (*batch4BInputGenerator)(nil)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var controllerCalls atomic.Int64
			var resolverCalls atomic.Int64
			var commandEvents atomic.Int64
			var executeCalls atomic.Int64
			sink := &batch4BArtifactSink{}
			dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
					commandEvents.Add(1)
				}
				return nil
			})})
			dependencies.Controller = batch4BController{calls: &controllerCalls}
			dependencies.InputGenerator = test.generator
			dependencies.ArtifactSink = sink
			dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				resolverCalls.Add(1)
				return FailureDecisionContinue
			})
			spec := batch4BCountingSpec(
				t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator, nil, &executeCalls, nil,
			)
			registry, err := newHandlerRegistry(spec)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := newDispatcher(registry).compile(
				context.Background(), compileContext{}, model.Command{Kind: model.CommandInputRandomText},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, runErr := executeCompiledRootForRun(context.Background(), dependencies, &compiledFlow{
				path: "/workspace/batch4b-selected-root.yaml", config: model.Config{AppID: "com.example.batch4b"},
				body: []compiledDispatch{compiled},
			}, "batch4b-selected-root/"+test.name)
			if !batch4BIsConfigurationError(runErr) || result.Outcome() != Failed || len(result.Commands()) != 0 {
				t.Fatalf("selected root cutoff = outcome %q commands %d error %T", result.Outcome(), len(result.Commands()), runErr)
			}
			if controllerCalls.Load() != 0 || resolverCalls.Load() != 0 || commandEvents.Load() != 0 ||
				executeCalls.Load() != 0 || sink.calls.Load() != 0 {
				t.Fatalf("selected root effects = controller %d resolver %d events %d execute %d artifacts %d",
					controllerCalls.Load(), resolverCalls.Load(), commandEvents.Load(), executeCalls.Load(), sink.calls.Load())
			}
			if generator, ok := test.generator.(*batch4BInputGenerator); ok && generator != nil && generator.calls.Load() != 0 {
				t.Fatalf("generator Generate calls = %d, want zero", generator.calls.Load())
			}
		})
	}
}

func TestInteractionBatch4BCoreLaterRootRunsAfterPriorCompletedPrefixAndMissingService(t *testing.T) {
	t.Parallel()

	for _, service := range []struct {
		name      string
		generator InputGenerator
	}{
		{name: "nil"},
		{name: "typed nil", generator: (*batch4BInputGenerator)(nil)},
	} {
		service := service
		t.Run(service.name, func(t *testing.T) {
			t.Parallel()

			var earlierExecute atomic.Int64
			var prefixExecute atomic.Int64
			var offenderEvaluate atomic.Int64
			var offenderExecute atomic.Int64
			var laterEvaluate atomic.Int64
			var laterExecute atomic.Int64
			earlierSpec := batch4BCountingSpec(t, model.CommandBack, batch4BRequiredServiceNone, nil, &earlierExecute, nil)
			prefixSpec := batch4BCountingSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil, &prefixExecute, nil)
			offenderSpec := batch4BCountingSpec(
				t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator,
				&offenderEvaluate, &offenderExecute, nil,
			)
			laterSpec := batch4BCountingSpec(
				t, model.CommandInputRandomNumber, batch4BRequiredServiceNone,
				&laterEvaluate, &laterExecute, nil,
			)
			registry, err := newHandlerRegistry(earlierSpec, prefixSpec, offenderSpec, laterSpec)
			if err != nil {
				t.Fatal(err)
			}
			dispatcher := newDispatcher(registry)

			earlierDispatch, err := dispatcher.compile(
				context.Background(), compileContext{}, model.Command{Kind: model.CommandBack},
			)
			if err != nil {
				t.Fatal(err)
			}
			earlierDependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
			earlierDependencies.InputGenerator = service.generator
			earlier, earlierErr := executeCompiledRootForRun(context.Background(), earlierDependencies, &compiledFlow{
				path: "/workspace/batch4b-earlier-root.yaml", config: model.Config{AppID: "com.example.batch4b"},
				body: []compiledDispatch{earlierDispatch},
			}, "batch4b/earlier-root/"+service.name)
			earlierCommands := earlier.Commands()
			if earlierErr != nil || earlier.Outcome() != Completed || len(earlierCommands) != 1 ||
				earlierCommands[0].Command().Kind != model.CommandBack ||
				earlierCommands[0].RootRunID() != "batch4b/earlier-root/"+service.name {
				t.Fatalf("earlier selected root = outcome %q error %v commands %#v", earlier.Outcome(), earlierErr, earlierCommands)
			}

			laterBody, err := dispatcher.compileSequence(context.Background(), compileContext{}, []model.Command{
				{Kind: model.CommandAction},
				{Kind: model.CommandInputRandomText},
				{Kind: model.CommandInputRandomNumber},
			})
			if err != nil {
				t.Fatal(err)
			}
			var controllerCalls atomic.Int64
			var resolverCalls atomic.Int64
			var commandEvents atomic.Int64
			sink := &batch4BArtifactSink{}
			laterDependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
					commandEvents.Add(1)
				}
				return nil
			})})
			laterDependencies.Controller = batch4BController{calls: &controllerCalls}
			laterDependencies.InputGenerator = service.generator
			laterDependencies.ArtifactSink = sink
			laterDependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				resolverCalls.Add(1)
				return FailureDecisionContinue
			})
			driver := laterDependencies.Driver.(*enginetest.FakeDriver)
			session, err := newExecutionSessionForRootRun(context.Background(), laterDependencies, &compiledFlow{
				path: "/workspace/batch4b-later-root.yaml", config: model.Config{AppID: "com.example.batch4b"}, body: laterBody,
			}, "batch4b/later-root/"+service.name)
			if err != nil {
				t.Fatal(err)
			}
			driverExpected := driver.Actions()
			later, laterErr := session.run(context.Background())
			if !batch4BIsConfigurationError(laterErr) || later.Outcome() != Failed {
				t.Fatalf("later selected root = outcome %q error %T %v", later.Outcome(), laterErr, laterErr)
			}
			laterCommands := later.Commands()
			if len(laterCommands) != 1 || laterCommands[0].Command().Kind != model.CommandAction ||
				laterCommands[0].Outcome() != Completed || laterCommands[0].RootRunID() != "batch4b/later-root/"+service.name {
				t.Fatalf("later selected-root projection = %#v, want only completed prefix", laterCommands)
			}
			ledger := session.core.ledger.snapshot()
			if len(ledger) != 1 || ledger[0].Sequence() != laterCommands[0].Sequence() ||
				session.core.timeline.Checkpoint() != 1 {
				t.Fatalf("later selected-root timeline/ledger = checkpoint %d ledger %#v", session.core.timeline.Checkpoint(), ledger)
			}
			if earlierExecute.Load() != 1 || prefixExecute.Load() != 1 ||
				offenderEvaluate.Load() != 0 || offenderExecute.Load() != 0 ||
				laterEvaluate.Load() != 0 || laterExecute.Load() != 0 {
				t.Fatalf("selected-root execution counts = earlier %d prefix %d offender %d/%d later %d/%d",
					earlierExecute.Load(), prefixExecute.Load(), offenderEvaluate.Load(), offenderExecute.Load(),
					laterEvaluate.Load(), laterExecute.Load())
			}
			if controllerCalls.Load() != 1 || resolverCalls.Load() != 0 || commandEvents.Load() != 2 || sink.calls.Load() != 0 {
				t.Fatalf("later selected-root cutoffs = controller %d resolver %d events %d artifacts %d",
					controllerCalls.Load(), resolverCalls.Load(), commandEvents.Load(), sink.calls.Load())
			}
			if !isNilValue(service.generator) {
				t.Fatalf("later selected-root generator fixture = %T, want absent or typed nil", service.generator)
			}
			if !reflect.DeepEqual(driver.Actions(), driverExpected) {
				t.Fatalf("later selected-root Driver effects = %#v, expected %#v", driver.Actions(), driverExpected)
			}
		})
	}
}

func TestInteractionBatch4BCoreNestedCallerShapesRetainOnlyAuthenticatedResults(t *testing.T) {
	t.Parallel()

	children := []model.Command{
		{Kind: model.CommandAction, Form: model.CommandFormScalar},
		{Kind: model.CommandInputRandomText, Form: model.CommandFormScalar},
		{Kind: model.CommandInputRandomNumber, Form: model.CommandFormScalar},
	}
	rawChildren := []any{
		string(model.CommandAction),
		string(model.CommandInputRandomText),
		string(model.CommandInputRandomNumber),
	}
	tests := []struct {
		name    string
		spec    handlerSpec
		command model.Command
	}{
		{
			name: "runFlow",
			spec: runFlowHandlerSpec(),
			command: model.Command{
				Kind: model.CommandRunFlow, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": rawChildren},
				Children:  children,
				Source:    batch4BSource("nested-run-flow.yaml", 10),
			},
		},
		{
			name: "repeat",
			spec: repeatHandlerSpec(),
			command: model.Command{
				Kind: model.CommandRepeat, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"times": int64(1), "commands": rawChildren,
				},
				Children: children,
			},
		},
		{
			name: "retry",
			spec: retryHandlerSpec(),
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"maxRetries": int64(0), "commands": rawChildren,
				},
				Children: children,
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			for _, service := range []struct {
				name      string
				generator InputGenerator
			}{
				{name: "nil"},
				{name: "typed nil", generator: (*batch4BInputGenerator)(nil)},
			} {
				service := service
				t.Run(service.name, func(t *testing.T) {
					t.Parallel()

					var prefixEvaluate atomic.Int64
					var prefixExecute atomic.Int64
					var offenderEvaluate atomic.Int64
					var offenderExecute atomic.Int64
					var laterEvaluate atomic.Int64
					var laterExecute atomic.Int64
					var commandEvents atomic.Int64
					var controllerCalls atomic.Int64
					var resolverCalls atomic.Int64
					var resolved CommandResult
					sink := &batch4BArtifactSink{}
					dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
						if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
							commandEvents.Add(1)
						}
						return nil
					})})
					dependencies.Controller = batch4BController{calls: &controllerCalls}
					dependencies.InputGenerator = service.generator
					dependencies.ArtifactSink = sink
					dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, candidate CommandResult) FailureDecision {
						resolverCalls.Add(1)
						resolved = candidate
						return FailureDecisionFail
					})
					prefix := batch4BCountingSpec(
						t, model.CommandAction, batch4BRequiredServiceNone,
						&prefixEvaluate, &prefixExecute, nil,
					)
					offender := batch4BCountingSpec(
						t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator,
						&offenderEvaluate, &offenderExecute, nil,
					)
					later := batch4BCountingSpec(
						t, model.CommandInputRandomNumber, batch4BRequiredServiceNone,
						&laterEvaluate, &laterExecute, nil,
					)
					registry, err := newHandlerRegistry(test.spec, prefix, offender, later)
					if err != nil {
						t.Fatal(err)
					}
					dispatcher := newDispatcher(registry)
					compiled, err := dispatcher.compile(
						context.Background(), compileContext{containingFlow: "/workspace/batch4b-root.yaml"}, test.command,
					)
					if err != nil {
						t.Fatal(err)
					}
					laterRoot, err := dispatcher.compile(
						context.Background(), compileContext{}, model.Command{Kind: model.CommandInputRandomNumber},
					)
					if err != nil {
						t.Fatal(err)
					}
					driver := dependencies.Driver.(*enginetest.FakeDriver)
					session, err := newExecutionSessionForRootRun(context.Background(), dependencies, &compiledFlow{
						path: "/workspace/batch4b-root.yaml", config: model.Config{AppID: "com.example.batch4b"},
						body: []compiledDispatch{compiled, laterRoot},
					}, "batch4b-nested/"+test.name+"/"+service.name)
					if err != nil {
						t.Fatal(err)
					}
					driverExpected := driver.Actions()
					result, runErr := session.run(context.Background())
					if !batch4BIsConfigurationError(runErr) || result.Outcome() != Failed {
						t.Fatalf("nested %s/%s = outcome %q error %T", test.name, service.name, result.Outcome(), runErr)
					}
					commands := result.Commands()
					if len(commands) != 2 || commands[0].Command().Kind != test.command.Kind || commands[0].Outcome() != Failed ||
						commands[0].Depth() != 0 || commands[1].Command().Kind != model.CommandAction ||
						commands[1].Outcome() != Completed || commands[1].Depth() != 1 {
						t.Fatalf("nested %s/%s projection = %#v, want failed parent plus completed child prefix", test.name, service.name, commands)
					}
					ledger := session.core.ledger.snapshot()
					if len(ledger) != 2 || ledger[0].Sequence() != commands[0].Sequence() ||
						ledger[1].Sequence() != commands[1].Sequence() || session.core.timeline.Checkpoint() != 2 {
						t.Fatalf("nested %s/%s timeline/ledger = checkpoint %d ledger %#v",
							test.name, service.name, session.core.timeline.Checkpoint(), ledger)
					}
					if prefixEvaluate.Load() != 1 || prefixExecute.Load() != 1 ||
						offenderEvaluate.Load() != 0 || offenderExecute.Load() != 0 ||
						laterEvaluate.Load() != 0 || laterExecute.Load() != 0 {
						t.Fatalf("nested %s/%s execution = prefix %d/%d offender %d/%d later %d/%d",
							test.name, service.name, prefixEvaluate.Load(), prefixExecute.Load(),
							offenderEvaluate.Load(), offenderExecute.Load(), laterEvaluate.Load(), laterExecute.Load())
					}
					if controllerCalls.Load() != 2 || commandEvents.Load() != 4 || resolverCalls.Load() != 1 || sink.calls.Load() != 0 {
						t.Fatalf("nested %s/%s cutoffs = controller %d events %d resolver %d artifacts %d",
							test.name, service.name, controllerCalls.Load(), commandEvents.Load(), resolverCalls.Load(), sink.calls.Load())
					}
					if !isNilValue(service.generator) {
						t.Fatalf("nested %s/%s generator fixture = %T, want absent or typed nil", test.name, service.name, service.generator)
					}
					if resolved.Command().Kind != test.command.Kind || resolved.Depth() != 0 ||
						!batch4BIsConfigurationError(resolved.ProductError()) || batch4BIsPrivateErrorCarrier(resolved.ProductError()) {
						t.Fatalf("nested %s/%s resolver surface = %#v", test.name, service.name, resolved)
					}
					if !reflect.DeepEqual(driver.Actions(), driverExpected) {
						t.Fatalf("nested %s/%s Driver effects = %#v, expected %#v",
							test.name, service.name, driver.Actions(), driverExpected)
					}
				})
			}
		})
	}
}

func TestInteractionBatch4BCorePreCancelledSequenceReturnsCanonicalResult(t *testing.T) {
	t.Parallel()

	var controllerCalls atomic.Int64
	var resolverCalls atomic.Int64
	var evaluateCalls atomic.Int64
	var executeCalls atomic.Int64
	var commandEvents atomic.Int64
	driver := enginetest.NewFakeDriver()
	generator := &batch4BInputGenerator{}
	sink := &batch4BArtifactSink{}
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	dependencies.Controller = batch4BController{calls: &controllerCalls}
	dependencies.InputGenerator = generator
	dependencies.ArtifactSink = sink
	dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
		resolverCalls.Add(1)
		return FailureDecisionContinue
	})
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
			commandEvents.Add(1)
		}
		return nil
	})}
	core, err := newExecutorCoreForRootRun(
		dependencies,
		"batch4b/pre-cancelled/genuine",
		batch4BCountingSpec(
			t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator,
			&evaluateCalls, &executeCalls, nil,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancellation := ctx.Err()

	results, runErr := core.executeSequence(ctx, []model.Command{
		{Kind: model.CommandInputRandomText},
		{Kind: model.CommandAction},
	}, 3)
	if runErr != cancellation || len(results) != 1 {
		t.Fatalf("pre-cancelled genuine return = results %#v error %T %v, want one canonical result/exact cancellation",
			results, runErr, runErr)
	}
	result := results[0]
	if result.Command().Kind != model.CommandInputRandomText || result.Depth() != 3 ||
		result.Outcome() != Cancelled || result.ProductError() != cancellation {
		t.Fatalf("pre-cancelled genuine result = %#v, want cancelled first command at depth 3", result)
	}
	canonical, authenticated := core.ledger.authenticate(result)
	if !authenticated || canonical.Sequence() != result.Sequence() ||
		canonical.ProductError() != cancellation || canonical.productError != result.productError {
		t.Fatalf("pre-cancelled canonical admission = %#v ok=%t, want ledger-owned clone of returned result",
			canonical, authenticated)
	}
	ledger := core.ledger.snapshot()
	if len(ledger) != 1 || ledger[0].Sequence() != result.Sequence() ||
		ledger[0].ProductError() != cancellation {
		t.Fatalf("pre-cancelled genuine ledger = %#v, want one exact cancelled row", ledger)
	}
	results[0].metadata.insight = "caller mutation"
	stored, ok := core.ledger.result(result.Sequence())
	if !ok || stored.Metadata().Insight() == "caller mutation" || stored.ProductError() != cancellation {
		t.Fatalf("pre-cancelled canonical ownership = %#v ok=%t, caller mutation reached ledger", stored, ok)
	}
	if core.timeline.Checkpoint() != 1 || commandEvents.Load() != 2 ||
		controllerCalls.Load() != 0 || resolverCalls.Load() != 0 ||
		evaluateCalls.Load() != 0 || executeCalls.Load() != 0 ||
		generator.calls.Load() != 0 || sink.calls.Load() != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("pre-cancelled genuine effects = checkpoint %d events %d controller %d resolver %d evaluate %d execute %d generator %d artifacts %d driver %#v",
			core.timeline.Checkpoint(), commandEvents.Load(), controllerCalls.Load(), resolverCalls.Load(),
			evaluateCalls.Load(), executeCalls.Load(), generator.calls.Load(), sink.calls.Load(), driver.Actions())
	}
}

func TestInteractionBatch4BCorePreCancelledSequenceRejectsPostRecordForgery(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		t.Run(category, func(t *testing.T) {
			t.Parallel()

			var controllerCalls atomic.Int64
			var resolverCalls atomic.Int64
			var evaluateCalls atomic.Int64
			var executeCalls atomic.Int64
			var commandEvents atomic.Int64
			driver := enginetest.NewFakeDriver()
			generator := &batch4BInputGenerator{}
			sink := &batch4BArtifactSink{}
			dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
			dependencies.Controller = batch4BController{calls: &controllerCalls}
			dependencies.InputGenerator = generator
			dependencies.ArtifactSink = sink
			dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
				resolverCalls.Add(1)
				return FailureDecisionContinue
			})
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandStarted || event.Kind() == EventCommandFinished {
					commandEvents.Add(1)
				}
				return nil
			})}
			core, err := newExecutorCoreForRootRun(
				dependencies,
				"batch4b/pre-cancelled/rejected/"+category,
				batch4BCountingSpec(
					t, model.CommandInputRandomText, batch4BRequiredServiceInputGenerator,
					&evaluateCalls, &executeCalls, nil,
				),
			)
			if err != nil {
				t.Fatal(err)
			}
			foreign := batch4BForeignAdmissionResult(
				t, model.CommandInputRandomText, context.Canceled, "pre-cancelled/"+category,
			)
			batch4BInstallPostRecordMutation(core, model.CommandInputRandomText, category, foreign)
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			cancellation := ctx.Err()

			results, runErr := core.executeSequence(ctx, []model.Command{
				{Kind: model.CommandInputRandomText},
				{Kind: model.CommandAction},
			}, 3)
			if results != nil || runErr != cancellation {
				t.Fatalf("%s pre-cancelled rejection = result count %d error %T %v, want nil/exact cancellation",
					category, len(results), runErr, runErr)
			}
			if got := core.ledger.snapshot(); len(got) != 0 {
				t.Fatalf("%s pre-cancelled projection = %#v, want no unauthenticated rows", category, got)
			}
			if _, ok := core.ledger.result(1); ok {
				t.Fatalf("%s pre-cancelled corrupted row gained a canonical result", category)
			}
			if core.timeline.Checkpoint() != 1 || commandEvents.Load() != 2 ||
				controllerCalls.Load() != 0 || resolverCalls.Load() != 0 ||
				evaluateCalls.Load() != 0 || executeCalls.Load() != 0 ||
				generator.calls.Load() != 0 || sink.calls.Load() != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("%s pre-cancelled rejection effects = checkpoint %d events %d controller %d resolver %d evaluate %d execute %d generator %d artifacts %d driver %#v",
					category, core.timeline.Checkpoint(), commandEvents.Load(), controllerCalls.Load(),
					resolverCalls.Load(), evaluateCalls.Load(), executeCalls.Load(), generator.calls.Load(),
					sink.calls.Load(), driver.Actions())
			}
		})
	}
}

func TestInteractionBatch4BCoreSequenceRejectsPostRecordForgery(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		for _, returned := range []struct {
			name       string
			productErr error
		}{
			{name: "returned nil"},
			{name: "returned error", productErr: NewConfigurationError("authentic handler failure", nil)},
		} {
			returned := returned
			t.Run(category+"/"+returned.name, func(t *testing.T) {
				t.Parallel()

				prefix := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil)
				target := batch4BObservedSpec(t, model.CommandInputText, batch4BRequiredServiceNone, returned.productErr)
				core, err := newExecutorCoreForRootRun(
					coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
					"batch4b/sequence/"+category+"/"+returned.name, prefix, target,
				)
				if err != nil {
					t.Fatal(err)
				}
				foreign := batch4BForeignAdmissionResult(t, model.CommandInputText, returned.productErr, category+"/"+returned.name)
				batch4BInstallPostRecordMutation(core, model.CommandInputText, category, foreign)

				results, runErr := core.executeSequence(context.Background(), []model.Command{
					{Kind: model.CommandAction},
					{Kind: model.CommandInputText},
				}, 0)
				if len(results) != 1 || results[0].Command().Kind != model.CommandAction || results[0].Outcome() != Completed {
					kinds := make([]model.CommandKeyword, len(results))
					for index := range results {
						kinds[index] = results[index].Command().Kind
					}
					t.Fatalf("sequence admitted %s candidate with %s: len=%d kinds=%v", category, returned.name, len(results), kinds)
				}
				if returned.productErr != nil {
					if runErr != returned.productErr {
						t.Fatalf("%s/%s return = %T %v, want original %T %v",
							category, returned.name, runErr, runErr, returned.productErr, returned.productErr)
					}
				} else if !batch4BIsConfigurationError(runErr) {
					t.Fatalf("%s/%s return = %T %v, want ConfigurationError", category, returned.name, runErr, runErr)
				}
			})
		}
	}
}

func TestInteractionBatch4BCoreRootPolicyAuthenticatesBeforeFailureResolver(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		for _, returned := range []struct {
			name       string
			productErr error
		}{
			{name: "returned nil"},
			{name: "returned error", productErr: NewConfigurationError("selected root failed", nil)},
		} {
			returned := returned
			t.Run(category+"/"+returned.name, func(t *testing.T) {
				t.Parallel()

				var resolverCalls atomic.Int64
				dependencies := coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
				dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
					resolverCalls.Add(1)
					return FailureDecisionContinue
				})
				core, err := newExecutorCoreForRootRun(dependencies, "batch4b/root-policy/"+category+"/"+returned.name)
				if err != nil {
					t.Fatal(err)
				}
				foreign := batch4BForeignAdmissionResult(t, model.CommandAction, returned.productErr, category+"/"+returned.name)
				batch4BInstallPostRecordMutation(core, model.CommandAction, category, foreign)
				spec := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, returned.productErr)
				policy := core.executeRootSequenceWithPolicy(context.Background(), []compiledDispatch{{
					command: model.Command{Kind: model.CommandAction}, spec: spec, value: struct{}{},
				}}, 0)
				if !policy.halted || resolverCalls.Load() != 0 {
					t.Fatalf("%s/%s root policy = %#v resolver calls %d, want halt/no resolver",
						category, returned.name, policy, resolverCalls.Load())
				}
				if returned.productErr != nil {
					if policy.failure != returned.productErr {
						t.Fatalf("%s/%s failure = %T %v, want original %T %v",
							category, returned.name, policy.failure, policy.failure, returned.productErr, returned.productErr)
					}
				} else if !batch4BIsConfigurationError(policy.failure) {
					t.Fatalf("%s/%s failure = %T %v, want ConfigurationError",
						category, returned.name, policy.failure, policy.failure)
				}
			})
		}
	}
}

func TestInteractionBatch4BCoreSelectedRootProjectionRejectsPostRecordForgery(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		for _, returned := range []struct {
			name       string
			productErr error
		}{
			{name: "returned nil"},
			{name: "returned error", productErr: NewConfigurationError("selected root projection failed", nil)},
		} {
			returned := returned
			t.Run(category+"/"+returned.name, func(t *testing.T) {
				t.Parallel()

				var resolverCalls atomic.Int64
				dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
				dependencies.FailureResolver = FailureResolverFunc(func(context.Context, CommandResult) FailureDecision {
					resolverCalls.Add(1)
					return FailureDecisionContinue
				})
				prefix := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil)
				offender := batch4BObservedSpec(t, model.CommandInputText, batch4BRequiredServiceNone, returned.productErr)
				session, err := newExecutionSessionForRootRun(context.Background(), dependencies, &compiledFlow{
					path:   "/workspace/batch4b-projection-" + category + ".yaml",
					config: model.Config{Name: "Batch 4B projection", AppID: "com.example.batch4b"},
					body: []compiledDispatch{
						{command: model.Command{Kind: model.CommandAction}, spec: prefix, value: struct{}{}},
						{command: model.Command{Kind: model.CommandInputText}, spec: offender, value: struct{}{}},
					},
				}, "batch4b/projection/"+category+"/"+returned.name)
				if err != nil {
					t.Fatal(err)
				}
				foreign := batch4BForeignAdmissionResult(
					t, model.CommandInputText, returned.productErr, "projection/"+category+"/"+returned.name,
				)
				batch4BInstallPostRecordMutation(session.core, model.CommandInputText, category, foreign)

				flow, runErr := session.run(context.Background())
				commands := flow.Commands()
				if len(commands) != 1 || commands[0].Command().Kind != model.CommandAction ||
					commands[0].Outcome() != Completed || commands[0].Depth() != 0 {
					kinds := make([]model.CommandKeyword, len(commands))
					for index := range commands {
						kinds[index] = commands[index].Command().Kind
					}
					t.Fatalf("%s/%s FlowResult command kinds = %v, want only [action]",
						category, returned.name, kinds)
				}
				if returned.productErr != nil {
					if runErr != returned.productErr || flow.ProductError() != returned.productErr {
						t.Fatalf("%s/%s errors = returned %T %v flow %T %v, want original %T %v",
							category, returned.name, runErr, runErr, flow.ProductError(), flow.ProductError(),
							returned.productErr, returned.productErr)
					}
				} else if !batch4BIsConfigurationError(runErr) || flow.ProductError() != runErr {
					t.Fatalf("%s/%s errors = returned %T %v flow %T %v, want shared ConfigurationError",
						category, returned.name, runErr, runErr, flow.ProductError(), flow.ProductError())
				}
				if flow.Outcome() != Failed || resolverCalls.Load() != 0 {
					t.Fatalf("%s/%s outcome/resolver = %q/%d, want Failed/0",
						category, returned.name, flow.Outcome(), resolverCalls.Load())
				}
				offenderSequence := commands[0].Sequence() + 1
				if session.core.timeline.Checkpoint() != offenderSequence {
					t.Fatalf("%s/%s timeline checkpoint = %d, want offender sequence %d",
						category, returned.name, session.core.timeline.Checkpoint(), offenderSequence)
				}
				if _, ok := session.core.ledger.result(offenderSequence); ok {
					t.Fatalf("%s/%s corrupted offender sequence %d gained a canonical result",
						category, returned.name, offenderSequence)
				}
				ledger := session.core.ledger.snapshot()
				if len(ledger) != 1 || ledger[0].Sequence() != commands[0].Sequence() ||
					ledger[0].Command().Kind != model.CommandAction {
					t.Fatalf("%s/%s ledger projection = %#v, want only authenticated prefix",
						category, returned.name, ledger)
				}
				if canonical, ok := session.core.ledger.authenticate(commands[0]); !ok ||
					canonical.Command().Kind != model.CommandAction {
					t.Fatalf("%s/%s prefix authentication = %#v ok=%t", category, returned.name, canonical, ok)
				}
			})
		}
	}

	t.Run("extra row", func(t *testing.T) {
		t.Parallel()

		prefix := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, nil)
		offender := batch4BObservedSpec(t, model.CommandInputText, batch4BRequiredServiceNone, nil)
		session, err := newExecutionSessionForRootRun(
			context.Background(), flowExecutorDependencies(&sessionRuntime{}, nil),
			&compiledFlow{
				path:   "/workspace/batch4b-projection-extra.yaml",
				config: model.Config{Name: "Batch 4B extra-row projection", AppID: "com.example.batch4b"},
				body: []compiledDispatch{
					{command: model.Command{Kind: model.CommandAction}, spec: prefix, value: struct{}{}},
					{command: model.Command{Kind: model.CommandInputText}, spec: offender, value: struct{}{}},
				},
			}, "batch4b/projection/extra",
		)
		if err != nil {
			t.Fatal(err)
		}
		foreign := batch4BForeignAdmissionResult(t, model.CommandInputText, nil, "projection/extra")
		var extraSequence uint64
		session.core.state.dependencies.Listeners = append(
			session.core.state.dependencies.Listeners,
			ListenerFunc(func(_ context.Context, event Event) error {
				command, ok := event.Command()
				if event.Kind() != EventCommandFinished || !ok || command.Kind != model.CommandInputText {
					return nil
				}
				extraSequence = event.Sequence() + 1
				session.core.ledger.mu.Lock()
				session.core.ledger.bySequence[extraSequence] = cloneCommandResult(foreign)
				session.core.ledger.mu.Unlock()
				return nil
			}),
		)

		flow, runErr := session.run(context.Background())
		commands := flow.Commands()
		if runErr != nil || flow.Outcome() != Completed || len(commands) != 2 ||
			commands[0].Command().Kind != model.CommandAction ||
			commands[1].Command().Kind != model.CommandInputText {
			t.Fatalf("extra-row FlowResult = outcome %q commands %v error %T %v, want genuine pair",
				flow.Outcome(), commandSequences(commands), runErr, runErr)
		}
		ledger := session.core.ledger.snapshot()
		if len(ledger) != 2 || ledger[0].Sequence() != commands[0].Sequence() ||
			ledger[1].Sequence() != commands[1].Sequence() {
			t.Fatalf("extra-row ledger projection = %#v, want genuine pair", ledger)
		}
		if extraSequence == 0 {
			t.Fatal("extra-row listener did not run")
		}
		if _, ok := session.core.ledger.result(extraSequence); ok {
			t.Fatalf("extra mutable row %d gained a canonical result", extraSequence)
		}
	})
}

func TestInteractionBatch4BCoreSelectedRootRejectedResultKeepsEarliestFailure(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		for _, returned := range []struct {
			name       string
			productErr error
		}{
			{name: "returned nil"},
			{name: "returned error", productErr: NewConfigurationError("later rejected failure", nil)},
		} {
			returned := returned
			t.Run(category+"/"+returned.name, func(t *testing.T) {
				first := NewConfigurationError("earliest resolver-continued failure", nil)
				flow, runErr, resolverCalls := batch4BRunSelectedRootWithRejectedResult(
					t, category, returned.name, first, returned.productErr,
				)
				if runErr != first || flow.ProductError() != first {
					t.Fatalf("%s/%s top-level identity = returned %T %v flow %T %v, want exact earliest failure",
						category, returned.name, runErr, runErr, flow.ProductError(), flow.ProductError())
				}
				commands := flow.Commands()
				if flow.Outcome() != Failed || len(commands) != 1 ||
					commands[0].Command().Kind != model.CommandAction ||
					commands[0].ProductError() != first || commands[0].Outcome() != Failed {
					t.Fatalf("%s/%s genuine projection = outcome %q commands %#v",
						category, returned.name, flow.Outcome(), commands)
				}
				if resolverCalls != 1 {
					t.Fatalf("%s/%s resolver calls = %d, want only the genuine first failure",
						category, returned.name, resolverCalls)
				}
				wantReportFailure := fmt.Sprintf("%T:%v", first, first)
				flowReportFailure := fmt.Sprintf("%T:%v", flow.ProductError(), flow.ProductError())
				commandReportFailure := fmt.Sprintf("%T:%v", commands[0].ProductError(), commands[0].ProductError())
				if flowReportFailure != wantReportFailure || commandReportFailure != wantReportFailure {
					t.Fatalf("%s/%s report-facing failures = %q/%q, want %q",
						category, returned.name, flowReportFailure, commandReportFailure, wantReportFailure)
				}
			})
		}
	}
}

func TestInteractionBatch4BCoreSelectedRootRejectedResultRetainsTerminalPrecedence(t *testing.T) {
	t.Parallel()

	for _, category := range batch4BAdmissionCategories() {
		category := category
		for _, terminal := range []struct {
			name       string
			productErr error
		}{
			{name: "cancellation", productErr: context.Canceled},
			{name: "device", productErr: NewDeviceConnectionError("later device disconnection", nil)},
		} {
			terminal := terminal
			t.Run(category+"/"+terminal.name, func(t *testing.T) {
				first := NewConfigurationError("earliest resolver-continued failure", nil)
				flow, runErr, resolverCalls := batch4BRunSelectedRootWithRejectedResult(
					t, category, terminal.name, first, terminal.productErr,
				)
				if runErr != terminal.productErr || flow.ProductError() != terminal.productErr ||
					flow.Outcome() != ClassifyOutcome(terminal.productErr, false) {
					t.Fatalf("%s/%s terminal identity = returned %T flow %T outcome %q, want %T/%q",
						category, terminal.name, runErr, flow.ProductError(), flow.Outcome(),
						terminal.productErr, ClassifyOutcome(terminal.productErr, false))
				}
				commands := flow.Commands()
				if len(commands) != 1 || commands[0].Command().Kind != model.CommandAction ||
					commands[0].ProductError() != first || resolverCalls != 1 {
					t.Fatalf("%s/%s terminal projection = commands %#v resolver %d, want genuine first only/1",
						category, terminal.name, commands, resolverCalls)
				}
				wantReportFailure := fmt.Sprintf("%T:%v", terminal.productErr, terminal.productErr)
				if got := fmt.Sprintf("%T:%v", flow.ProductError(), flow.ProductError()); got != wantReportFailure {
					t.Fatalf("%s/%s report-facing terminal failure = %q, want %q",
						category, terminal.name, got, wantReportFailure)
				}
			})
		}
	}
}

func TestInteractionBatch4BCoreGenuineErroredResultRemainsRepresentable(t *testing.T) {
	t.Parallel()

	failure := NewConfigurationError("genuine recorded failure", nil)
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, failure),
	)
	if err != nil {
		t.Fatal(err)
	}
	results, runErr := core.executeSequence(context.Background(), []model.Command{{Kind: model.CommandAction}}, 0)
	if runErr != failure || len(results) != 1 || results[0].ProductError() != failure || results[0].Outcome() != Failed {
		t.Fatalf("genuine errored sequence = results %#v error %T %v", results, runErr, runErr)
	}
	canonical, ok := core.ledger.authenticate(results[0])
	if !ok || canonical.ProductError() != failure {
		t.Fatalf("genuine errored authentication = %#v ok=%t", canonical, ok)
	}
}

func TestInteractionBatch4BCoreErrorCarrierNeverEscapesOutwardSurfaces(t *testing.T) {
	t.Parallel()

	t.Run("actual root and report-facing projection", func(t *testing.T) {
		raw := batch4BMapError{"seed": 1}
		var published error = raw
		events := make([]Event, 0, 4)
		var resolverCalls atomic.Int64
		var resolved CommandResult
		dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})})
		dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, candidate CommandResult) FailureDecision {
			resolverCalls.Add(1)
			resolved = candidate
			return FailureDecisionFail
		})
		spec := batch4BExactPublicationSpec(published)
		session, err := newExecutionSessionForRootRun(context.Background(), dependencies, &compiledFlow{
			path:   "/workspace/batch4b-carrier-root.yaml",
			config: model.Config{Name: "carrier report root", AppID: "com.example.batch4b"},
			body: []compiledDispatch{{
				command: model.Command{Kind: model.CommandPressKey}, spec: spec, value: struct{}{},
			}},
		}, "batch4b/carrier-root")
		if err != nil {
			t.Fatal(err)
		}
		flow, runErr := session.run(context.Background())
		batch4BAssertRawMapError(t, "actual-root returned error", runErr, raw, "returned", 2)
		batch4BAssertRawMapError(t, "actual-root FlowResult.ProductError", flow.ProductError(), raw, "flow", 3)
		commands := flow.Commands()
		if len(commands) != 1 || commands[0].Command().Kind != model.CommandPressKey {
			t.Fatalf("actual-root commands = %#v, want one pressKey", commands)
		}
		batch4BAssertRawMapError(t, "actual-root FlowResult.Commands ProductError", commands[0].ProductError(), raw, "flow-command", 4)
		if resolverCalls.Load() != 1 || resolved.Command().Kind != model.CommandPressKey {
			t.Fatalf("actual-root resolver = calls %d result %#v", resolverCalls.Load(), resolved)
		}
		batch4BAssertRawMapError(t, "actual-root FailureResolver ProductError", resolved.ProductError(), raw, "resolver", 5)
		for index, event := range events {
			if event.ProductError() == nil {
				continue
			}
			batch4BAssertRawMapError(t, "actual-root listener Event", event.ProductError(), raw, fmt.Sprintf("event-%d", index), 10+index)
			if batch4BIsPrivateErrorCarrier(event.ProductError()) {
				t.Fatalf("carrier escaped through event %s", event.Kind())
			}
		}
		ledger := session.core.ledger.snapshot()
		if len(ledger) != 1 {
			t.Fatalf("actual-root ledger = %#v, want one result", ledger)
		}
		batch4BAssertFourCarriers(t, ledger[0])
		for index, carrier := range batch4BErrorSlots(ledger[0]) {
			batch4BAssertErrorOnlyCarrier(t, fmt.Sprintf("actual-root slot %d", index), carrier)
		}
		wantReportMessage := raw.Error()
		if flowMessage, commandMessage := fmt.Sprint(flow.ProductError()), fmt.Sprint(commands[0].ProductError()); flowMessage != wantReportMessage || commandMessage != wantReportMessage {
			t.Fatalf("report-facing failure messages = %q/%q, want %q", flowMessage, commandMessage, wantReportMessage)
		}
	})

	t.Run("actual retry propagation", func(t *testing.T) {
		raw := NewOperationError("retry-visible exact failure", errors.New("retry cause"))
		child := model.Command{Kind: model.CommandPressKey, Form: model.CommandFormScalar}
		retryCommand := model.Command{
			Kind: model.CommandRetry, Form: model.CommandFormObject,
			Arguments: map[string]any{
				"maxRetries": int64(0), "commands": []any{string(model.CommandPressKey)},
			},
			Children: []model.Command{child},
		}
		registry, err := newHandlerRegistry(retryHandlerSpec(), batch4BExactPublicationSpec(raw))
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, retryCommand)
		if err != nil {
			t.Fatal(err)
		}
		var resolverCalls atomic.Int64
		var resolved CommandResult
		events := make([]Event, 0, 6)
		dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})})
		dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, candidate CommandResult) FailureDecision {
			resolverCalls.Add(1)
			resolved = candidate
			return FailureDecisionFail
		})
		session, err := newExecutionSessionForRootRun(context.Background(), dependencies, &compiledFlow{
			path:   "/workspace/batch4b-retry-carrier-root.yaml",
			config: model.Config{Name: "retry carrier report root", AppID: "com.example.batch4b"},
			body:   []compiledDispatch{compiled},
		}, "batch4b/retry-carrier-root")
		if err != nil {
			t.Fatal(err)
		}
		flow, runErr := session.run(context.Background())
		commands := flow.Commands()
		if runErr != raw || flow.ProductError() != raw || len(commands) != 2 ||
			commands[0].Command().Kind != model.CommandRetry || commands[0].ProductError() != raw ||
			commands[1].Command().Kind != model.CommandPressKey || commands[1].ProductError() != raw {
			t.Fatalf("actual retry outward identity = returned %T flow %T commands %#v", runErr, flow.ProductError(), commands)
		}
		if resolverCalls.Load() != 1 || resolved.ProductError() != raw || batch4BIsPrivateErrorCarrier(resolved.ProductError()) {
			t.Fatalf("actual retry resolver = calls %d result %#v", resolverCalls.Load(), resolved)
		}
		classification := retryClassificationError(retryInlineSource, commands, errors.New("fallback"))
		if classification != raw || !IsRetryable(classification) || batch4BIsPrivateErrorCarrier(classification) {
			t.Fatalf("trusted retry classification = %T %v, want exact raw retryable error", classification, classification)
		}
		for _, event := range events {
			if event.ProductError() != nil && batch4BIsPrivateErrorCarrier(event.ProductError()) {
				t.Fatalf("carrier escaped through retry event %s", event.Kind())
			}
		}
		ledger := session.core.ledger.snapshot()
		if len(ledger) != 2 {
			t.Fatalf("actual retry ledger = %#v, want outer and child", ledger)
		}
		for resultIndex, stored := range ledger {
			batch4BAssertFourCarriers(t, stored)
			for slotIndex, carrier := range batch4BErrorSlots(stored) {
				batch4BAssertErrorOnlyCarrier(t, fmt.Sprintf("retry result %d slot %d", resultIndex, slotIndex), carrier)
			}
		}
		wantReportMessage := raw.Error()
		if flowMessage, outerMessage, childMessage := fmt.Sprint(flow.ProductError()), fmt.Sprint(commands[0].ProductError()), fmt.Sprint(commands[1].ProductError()); flowMessage != wantReportMessage || outerMessage != wantReportMessage || childMessage != wantReportMessage {
			t.Fatalf("retry report-facing failure messages = %q/%q/%q, want %q",
				flowMessage, outerMessage, childMessage, wantReportMessage)
		}
	})
}

func batch4BAdmissionCategories() []string {
	return []string{"zero", "identity-forged", "payload-mutated", "foreign", "unrecorded"}
}

func batch4BExactPublicationSpec(published error) handlerSpec {
	return handlerSpec{
		keyword: model.CommandPressKey, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle, exactErrorPolicy: exactErrorPublicationPressKey,
		compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{
				effectClass:       EffectDeviceMutation,
				exactErrorRequest: &exactErrorRequest{underlying: published},
			}, nil
		},
	}
}

func batch4BForeignAdmissionResult(
	t *testing.T,
	keyword model.CommandKeyword,
	productErr error,
	suffix string,
) CommandResult {
	t.Helper()
	core, err := newExecutorCoreForRootRun(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(777, 0))),
		"batch4b/foreign/"+suffix,
		batch4BObservedSpec(t, keyword, batch4BRequiredServiceNone, productErr),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := core.execute(context.Background(), model.Command{Kind: keyword}, 0)
	if productErr == nil {
		if runErr != nil {
			t.Fatalf("foreign admission fixture error = %v", runErr)
		}
	} else if runErr != productErr {
		t.Fatalf("foreign admission fixture error = %T %v, want %T %v", runErr, runErr, productErr, productErr)
	}
	return result
}

func batch4BRunSelectedRootWithRejectedResult(
	t *testing.T,
	category string,
	suffix string,
	first error,
	later error,
) (FlowResult, error, int64) {
	t.Helper()

	var resolverCalls atomic.Int64
	dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
	dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, candidate CommandResult) FailureDecision {
		resolverCalls.Add(1)
		if candidate.Command().Kind != model.CommandAction || candidate.ProductError() != first {
			t.Errorf("resolver candidate = kind %q error %T %v, want genuine first failure",
				candidate.Command().Kind, candidate.ProductError(), candidate.ProductError())
		}
		return FailureDecisionContinue
	})
	prefix := batch4BObservedSpec(t, model.CommandAction, batch4BRequiredServiceNone, first)
	offender := batch4BObservedSpec(t, model.CommandInputText, batch4BRequiredServiceNone, later)
	session, err := newExecutionSessionForRootRun(context.Background(), dependencies, &compiledFlow{
		path:   "/workspace/batch4b-first-failure-" + category + "-" + suffix + ".yaml",
		config: model.Config{Name: "Batch 4B first failure", AppID: "com.example.batch4b"},
		body: []compiledDispatch{
			{command: model.Command{Kind: model.CommandAction}, spec: prefix, value: struct{}{}},
			{command: model.Command{Kind: model.CommandInputText}, spec: offender, value: struct{}{}},
		},
	}, "batch4b/first-failure/"+category+"/"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	foreign := batch4BForeignAdmissionResult(t, model.CommandInputText, later, "first-failure/"+category+"/"+suffix)
	batch4BInstallPostRecordMutation(session.core, model.CommandInputText, category, foreign)
	flow, runErr := session.run(context.Background())
	return flow, runErr, resolverCalls.Load()
}

func batch4BInstallPostRecordMutation(
	core *executorCore,
	keyword model.CommandKeyword,
	category string,
	foreign CommandResult,
) {
	core.state.dependencies.Listeners = append(core.state.dependencies.Listeners, ListenerFunc(func(_ context.Context, event Event) error {
		command, ok := event.Command()
		if event.Kind() != EventCommandFinished || !ok || command.Kind != keyword {
			return nil
		}
		core.ledger.mu.Lock()
		defer core.ledger.mu.Unlock()
		switch category {
		case "zero":
			core.ledger.bySequence[event.Sequence()] = CommandResult{}
		case "identity-forged":
			candidate := cloneCommandResult(core.ledger.bySequence[event.Sequence()])
			candidate.identity = &commandResultIdentity{marker: 1}
			core.ledger.bySequence[event.Sequence()] = candidate
		case "payload-mutated":
			candidate := cloneCommandResult(core.ledger.bySequence[event.Sequence()])
			candidate.metadata.insight += "post-record mutation"
			core.ledger.bySequence[event.Sequence()] = candidate
		case "foreign":
			core.ledger.bySequence[event.Sequence()] = cloneCommandResult(foreign)
		case "unrecorded":
			delete(core.ledger.bySequence, event.Sequence())
		default:
			panic("unknown Batch 4B admission category")
		}
		return nil
	}))
}

type batch4BController struct {
	calls *atomic.Int64
}

func (controller batch4BController) WaitIfPaused(context.Context) error {
	controller.calls.Add(1)
	return nil
}

type batch4BInputGenerator struct {
	calls atomic.Int64
}

type batch4BArtifactSink struct {
	calls atomic.Int64
}

func (sink *batch4BArtifactSink) Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.calls.Add(1)
	return ArtifactWriteResult{}, errors.New("unexpected artifact write")
}

func (generator *batch4BInputGenerator) Generate(context.Context, InputRequest) (string, error) {
	generator.calls.Add(1)
	return "must-not-run", nil
}

type batch4BMapError map[string]int

func (batch4BMapError) Error() string { return "batch4b map-backed error" }

func batch4BObservedSpec(t *testing.T, keyword model.CommandKeyword, required uint64, productErr error) handlerSpec {
	t.Helper()
	return batch4BCountingSpec(t, keyword, required, nil, nil, productErr)
}

func batch4BCountingSpec(
	t *testing.T,
	keyword model.CommandKeyword,
	required uint64,
	evaluateCalls *atomic.Int64,
	executeCalls *atomic.Int64,
	productErr error,
) handlerSpec {
	t.Helper()
	spec := handlerSpec{
		keyword: keyword, effectClass: EffectHostMutation,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: func(_ context.Context, _ evaluationContext, command model.Command, value any) (evaluatedDispatch, error) {
			if evaluateCalls != nil {
				evaluateCalls.Add(1)
			}
			return evaluatedDispatch{command: cloneCommand(command), value: value}, nil
		},
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			if executeCalls != nil {
				executeCalls.Add(1)
			}
			return commandEffect{effectClass: EffectHostMutation}, productErr
		},
	}
	return batch4BSetRequiredService(t, spec, required)
}

func batch4BSetRequiredService(t *testing.T, spec handlerSpec, required uint64) handlerSpec {
	t.Helper()
	spec.requiredService = requiredService(required)
	return spec
}

func batch4BIsPrivateErrorCarrier(err error) bool {
	if err == nil {
		return false
	}
	errorType := reflect.TypeOf(err)
	if errorType.Kind() != reflect.Pointer || errorType.Elem().PkgPath() != reflect.TypeOf(CommandResult{}).PkgPath() {
		return false
	}
	name := errorType.Elem().Name()
	return name != "" && name[0] >= 'a' && name[0] <= 'z' &&
		errorType.NumMethod() == 1 && errorType.Method(0).Name == "Error"
}

func batch4BAssertErrorOnlyCarrier(t *testing.T, surface string, carrier error) {
	t.Helper()
	if !batch4BIsPrivateErrorCarrier(carrier) {
		t.Fatalf("%s = %T, want private opaque pointer carrier", surface, carrier)
	}
	if _, ok := carrier.(interface{ Unwrap() error }); ok {
		t.Fatalf("%s exposes Unwrap", surface)
	}
	if _, ok := carrier.(interface{ Is(error) bool }); ok {
		t.Fatalf("%s exposes Is", surface)
	}
	if _, ok := carrier.(interface{ As(any) bool }); ok {
		t.Fatalf("%s exposes As", surface)
	}
	if carrier.Error() == "" {
		t.Fatalf("%s fixed diagnostic is blank", surface)
	}
}

func batch4BIsConfigurationError(err error) bool {
	var configuration *ConfigurationError
	return errors.As(err, &configuration)
}

func batch4BAssertRawMapError(
	t *testing.T,
	surface string,
	published error,
	raw batch4BMapError,
	key string,
	value int,
) {
	t.Helper()
	alias, ok := published.(batch4BMapError)
	if !ok {
		t.Fatalf("%s type = %T, want batch4BMapError", surface, published)
	}
	alias[key] = value
	if raw[key] != value {
		t.Fatalf("%s did not preserve raw map identity", surface)
	}
}
