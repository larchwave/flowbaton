package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch4HConcurrentIndependentRootsRemainIsolated(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{}, batch4HRandomBare(model.CommandInputRandomEmail),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path      string
		appID     string
		rootRunID string
		value     string
	}{
		{path: "/workspace/batch4h-concurrent-a.yaml", appID: "com.example.batch4h.a", rootRunID: "batch4h-concurrent/root-run-000001", value: "a@example.test"},
		{path: "/workspace/batch4h-concurrent-b.yaml", appID: "com.example.batch4h.b", rootRunID: "batch4h-concurrent/root-run-000002", value: "b@example.test"},
	}
	type outcome struct {
		index     int
		result    FlowResult
		err       error
		generator *batch4HRecordingGenerator
		driver    *enginetest.FakeDriver
		events    []Event
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(tests))
	var group sync.WaitGroup
	for index, test := range tests {
		index, test := index, test
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
			driver := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}})
			generator := &batch4HRecordingGenerator{value: test.value}
			events := make([]Event, 0, 4)
			root := &compiledFlow{
				path: test.path, config: model.Config{AppID: test.appID}, body: []compiledDispatch{compiled},
			}
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch4h-concurrent", Driver: driver, Clock: newAdvancingClock(),
				InputGenerator: generator, JSFactory: tapJSFactory(t), Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, test.rootRunID)
			outcomes <- outcome{index: index, result: result, err: runErr, generator: generator, driver: driver, events: events}
		}()
	}
	close(start)
	group.Wait()
	close(outcomes)

	seen := make([]bool, len(tests))
	for actual := range outcomes {
		test := tests[actual.index]
		seen[actual.index] = true
		commands := actual.result.Commands()
		requests := batch4AInputRequests(actual.driver.Actions())
		if actual.err != nil || actual.result.Path() != test.path || actual.result.RootRunID() != test.rootRunID ||
			actual.result.Outcome() != Completed || len(commands) != 1 || commands[0].Sequence() != 1 || commands[0].Depth() != 0 ||
			commands[0].Command().Kind != model.CommandInputRandomEmail || commands[0].Outcome() != Completed ||
			actual.generator.calls != 1 || actual.generator.request != (InputRequest{Kind: InputEmail, Length: -1}) ||
			len(requests) != 1 || requests[0].Text != test.value || len(requests[0].AppIDs) != 1 || requests[0].AppIDs[0] != test.appID ||
			len(actual.events) != 4 || actual.events[0].RootRunID() != test.rootRunID || actual.events[3].Outcome() != Completed {
			t.Fatalf("concurrent root %d = result %#v commands %#v generator %#v requests %#v events %#v error %v", actual.index, actual.result, commands, actual.generator, requests, actual.events, actual.err)
		}
		evaluated, exists := commands[0].Metadata().EvaluatedCommand()
		if !exists || evaluated.Kind != model.CommandInputRandomEmail || evaluated.Form != model.CommandFormScalar || evaluated.Arguments != nil {
			t.Fatalf("concurrent root %d evaluated metadata = %#v present %t", actual.index, evaluated, exists)
		}
	}
	for index, present := range seen {
		if !present {
			t.Fatalf("concurrent root %d result is missing", index)
		}
	}
}

func TestInteractionBatch4BResultLedgerAuthenticatesCoreFinalizedOutcomes(t *testing.T) {
	t.Parallel()

	t.Run("completed", func(t *testing.T) {
		t.Parallel()
		core := batch4BNewCompletedCore(t, []string{"first log", "second log"}, batch4BArtifacts())
		result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandAction), 2)
		if runErr != nil || result.Outcome() != Completed || result.ProductError() != nil {
			t.Fatalf("completed result = outcome %q error %v", result.Outcome(), runErr)
		}
		batch4BAssertAuthentic(t, core.ledger, result)
	})

	t.Run("failed", func(t *testing.T) {
		t.Parallel()
		raw := errors.New("failed exact publication")
		core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(100, 0)), raw)
		result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
		if runErr != raw || result.Outcome() != Failed || result.ProductError() != raw {
			t.Fatalf("failed result = outcome %q product %T returned %T", result.Outcome(), result.ProductError(), runErr)
		}
		canonical := batch4BAssertAuthentic(t, core.ledger, result)
		batch4BAssertFourCarriers(t, canonical)
	})

	t.Run("warned", func(t *testing.T) {
		t.Parallel()
		raw := errors.New("optional exact publication")
		core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(200, 0)), raw)
		command := batch4BRichCommand(true, model.CommandPressKey)
		optional := true
		command.Optional = &optional
		result, runErr := core.execute(context.Background(), command, 2)
		if runErr != nil || result.Outcome() != Warned || result.ProductError() != raw {
			t.Fatalf("warned result = outcome %q product %T returned %T", result.Outcome(), result.ProductError(), runErr)
		}
		canonical := batch4BAssertAuthentic(t, core.ledger, result)
		if !batch4BIsPrivateErrorCarrier(canonical.productError) {
			t.Fatalf("warned stored product = %T, want private carrier", canonical.productError)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		core, err := newExecutorCoreForRootRun(
			coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(300, 0))),
			"batch4b/cancelled",
		)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, runErr := core.execute(ctx, batch4BRichCommand(false, model.CommandAction), 2)
		if runErr != context.Canceled || result.Outcome() != Cancelled || result.ProductError() != context.Canceled {
			t.Fatalf("cancelled result = outcome %q product %T returned %T", result.Outcome(), result.ProductError(), runErr)
		}
		canonical := batch4BAssertAuthentic(t, core.ledger, result)
		if !batch4BIsPrivateErrorCarrier(canonical.productError) {
			t.Fatalf("cancelled stored product = %T, want private carrier", canonical.productError)
		}
	})
}

func TestInteractionBatch4BResultLedgerRecordDoesNotMintErrorOrigin(t *testing.T) {
	t.Parallel()

	timeline, err := newTimeline(enginetest.NewFakeClock(time.Unix(400, 0)), "batch4b/manual")
	if err != nil {
		t.Fatal(err)
	}
	span, _, err := timeline.BeginCommand(batch4BRichCommand(true, model.CommandPressKey), 2)
	if err != nil {
		t.Fatal(err)
	}
	raw := errors.New("handcrafted raw slot")
	evaluated := batch4BRichCommand(true, model.CommandPressKey)
	result, _, err := span.FinishWithArtifacts(
		Failed, raw, NewCommandMetadata(2, &evaluated, []string{"raw"}, "raw", "raw"), batch4BArtifacts(),
	)
	if err != nil {
		t.Fatal(err)
	}
	result.retryClassification = &retryErrorClassification{
		classification: raw,
		publication:    &exactErrorDisposition{published: raw, classification: raw},
	}
	ledger := newCommandResultLedger()
	if err := ledger.record(result); err != nil {
		t.Fatal(err)
	}
	stored, exists := ledger.result(result.Sequence())
	if !exists || batch4BIsPrivateErrorCarrier(stored.productError) || stored.ProductError() != raw {
		t.Fatalf("manual record unexpectedly minted origin: exists=%t stored=%T", exists, stored.productError)
	}
	if _, ok := ledger.authenticate(stored); ok {
		t.Fatal("ledger authenticated raw slots that did not cross the core sealing boundary")
	}
}

func TestInteractionBatch4BResultLedgerRejectsEveryErrorSlotOriginMutation(t *testing.T) {
	t.Parallel()

	core := batch4BNewExactCore(
		t,
		enginetest.NewFakeClock(time.Unix(500, 0)),
		errors.New("first exact publication"),
		errors.New("foreign exact publication"),
	)
	first, firstErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	if firstErr == nil {
		t.Fatal("first exact execution error = nil")
	}
	foreign, foreignErr := core.execute(context.Background(), batch4BRichCommand(false, model.CommandPressKey), 2)
	if foreignErr == nil {
		t.Fatal("foreign exact execution error = nil")
	}
	first = batch4BAssertAuthentic(t, core.ledger, first)
	foreign = batch4BAssertAuthentic(t, core.ledger, foreign)
	batch4BAssertFourCarriers(t, first)
	batch4BAssertFourCarriers(t, foreign)

	tests := []struct {
		name   string
		mutate func(*CommandResult)
	}{
		{name: "product raw", mutate: func(candidate *CommandResult) { candidate.productError = batch4BMapError{"raw": 1} }},
		{name: "product foreign", mutate: func(candidate *CommandResult) { candidate.productError = foreign.productError }},
		{name: "product swapped", mutate: func(candidate *CommandResult) { candidate.productError = candidate.retryClassification.classification }},
		{name: "product missing", mutate: func(candidate *CommandResult) { candidate.productError = nil }},
		{name: "retry classification raw", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.classification = batch4BMapError{"raw": 1}
		}},
		{name: "retry classification foreign", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.classification = foreign.retryClassification.classification
		}},
		{name: "retry classification swapped", mutate: func(candidate *CommandResult) { candidate.retryClassification.classification = candidate.productError }},
		{name: "retry classification missing", mutate: func(candidate *CommandResult) { candidate.retryClassification.classification = nil }},
		{name: "publication published raw", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.published = batch4BMapError{"raw": 1}
		}},
		{name: "publication published foreign", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.published = foreign.retryClassification.publication.published
		}},
		{name: "publication published swapped", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.published = candidate.retryClassification.publication.classification
		}},
		{name: "publication published missing", mutate: func(candidate *CommandResult) { candidate.retryClassification.publication.published = nil }},
		{name: "publication classification raw", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.classification = batch4BMapError{"raw": 1}
		}},
		{name: "publication classification foreign", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.classification = foreign.retryClassification.publication.classification
		}},
		{name: "publication classification swapped", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.classification = candidate.retryClassification.publication.published
		}},
		{name: "publication classification missing", mutate: func(candidate *CommandResult) { candidate.retryClassification.publication.classification = nil }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneCommandResult(first)
			test.mutate(&candidate)
			if _, ok := core.ledger.authenticate(candidate); ok {
				t.Fatal("mutated error origin authenticated")
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerRedemptionRequiresCompleteAuthentication(t *testing.T) {
	t.Parallel()

	state := &batch4BAttackerState{}
	attacker := batch4BAttackerError{state: state, bytes: []byte{1, 2, 3}}
	core := batch4BNewExactCore(t, &batch4BArmingClock{now: time.Unix(550, 0), state: state}, attacker)
	result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	if runErr == nil || state.calls.Load() != 0 || !state.armed.Load() {
		t.Fatalf("attacker boundary = returned %T calls %d armed %t", runErr, state.calls.Load(), state.armed.Load())
	}
	canonical := batch4BAssertAuthentic(t, core.ledger, result)
	batch4BAssertFourCarriers(t, canonical)
	expectedCalls := state.calls.Load()
	disposition, ok := core.ledger.redeemExactError(&canonical)
	if !ok {
		t.Fatal("genuine authenticated exact error did not redeem")
	}
	if _, ok := disposition.published.(batch4BAttackerError); !ok {
		t.Fatalf("redeemed publication type = %T, want batch4BAttackerError", disposition.published)
	}
	if state.calls.Load() != expectedCalls {
		t.Fatalf("genuine redemption invoked hostile raw methods: expected %d now %d", expectedCalls, state.calls.Load())
	}

	tests := []struct {
		name   string
		mutate func(*CommandResult)
	}{
		{name: "payload", mutate: func(candidate *CommandResult) { candidate.metadata.insight += "mutated" }},
		{name: "product error", mutate: func(candidate *CommandResult) { candidate.productError = attacker }},
		{name: "retry classification", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.classification = attacker
		}},
		{name: "publication published", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.published = attacker
		}},
		{name: "publication classification", mutate: func(candidate *CommandResult) {
			candidate.retryClassification.publication.classification = attacker
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneCommandResult(canonical)
			test.mutate(&candidate)
			if _, ok := core.ledger.redeemExactError(&candidate); ok {
				t.Fatal("mutated exact-error candidate redeemed")
			}
			if state.calls.Load() != expectedCalls {
				t.Fatalf("redemption inspected hostile raw methods: expected %d now %d", expectedCalls, state.calls.Load())
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerRejectsIndependentImmutablePayloadMutations(t *testing.T) {
	t.Parallel()

	raw := errors.New("payload expected")
	core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(600, 0)), raw)
	result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	if runErr != raw {
		t.Fatalf("expected execution error = %T, want exact raw", runErr)
	}
	result = batch4BAssertAuthentic(t, core.ledger, result)

	mutations := []struct {
		name   string
		mutate func(*CommandResult)
	}{
		{name: "identity", mutate: func(candidate *CommandResult) { candidate.identity = &commandResultIdentity{marker: 1} }},
		{name: "root", mutate: func(candidate *CommandResult) { candidate.rootRunID = "foreign/root" }},
		{name: "sequence", mutate: func(candidate *CommandResult) { candidate.sequence++ }},
		{name: "depth", mutate: func(candidate *CommandResult) { candidate.depth++ }},
		{name: "outcome", mutate: func(candidate *CommandResult) { candidate.outcome = Cancelled }},
		{name: "started timestamp", mutate: func(candidate *CommandResult) { candidate.startedAt = candidate.startedAt.Add(time.Nanosecond) }},
		{name: "finished timestamp", mutate: func(candidate *CommandResult) { candidate.finishedAt = candidate.finishedAt.Add(time.Nanosecond) }},
		{name: "duration", mutate: func(candidate *CommandResult) { candidate.duration++ }},
		{name: "retry presence", mutate: func(candidate *CommandResult) { candidate.retryClassification = nil }},
		{name: "publication presence", mutate: func(candidate *CommandResult) { candidate.retryClassification.publication = nil }},
		{name: "metadata runs", mutate: func(candidate *CommandResult) { candidate.metadata.numberOfRuns++ }},
		{name: "metadata runs presence", mutate: func(candidate *CommandResult) { candidate.metadata.numberOfRunsSet = false }},
		{name: "metadata evaluated presence", mutate: func(candidate *CommandResult) { candidate.metadata.evaluatedCommand = nil }},
		{name: "metadata log nil shape", mutate: func(candidate *CommandResult) { candidate.metadata.logMessages = nil }},
		{name: "metadata log empty shape", mutate: func(candidate *CommandResult) { candidate.metadata.logMessages = []string{} }},
		{name: "metadata log order", mutate: func(candidate *CommandResult) {
			candidate.metadata.logMessages[0], candidate.metadata.logMessages[1] = candidate.metadata.logMessages[1], candidate.metadata.logMessages[0]
		}},
		{name: "metadata log bytes", mutate: func(candidate *CommandResult) { candidate.metadata.logMessages[0] += "!" }},
		{name: "metadata insight", mutate: func(candidate *CommandResult) { candidate.metadata.insight += "!" }},
		{name: "metadata AI reasoning", mutate: func(candidate *CommandResult) { candidate.metadata.aiReasoning += "!" }},
		{name: "artifact nil shape", mutate: func(candidate *CommandResult) { candidate.artifacts = nil }},
		{name: "artifact empty shape", mutate: func(candidate *CommandResult) { candidate.artifacts = []device.Artifact{} }},
		{name: "artifact order", mutate: func(candidate *CommandResult) {
			candidate.artifacts[0], candidate.artifacts[1] = candidate.artifacts[1], candidate.artifacts[0]
		}},
		{name: "artifact kind", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Kind += "!" }},
		{name: "artifact path", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Path += "!" }},
		{name: "artifact metadata nil shape", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata = nil }},
		{name: "artifact metadata empty shape", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata = map[string]string{} }},
		{name: "artifact metadata cardinality", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata["extra"] = "value" }},
		{name: "artifact metadata key", mutate: func(candidate *CommandResult) {
			value := candidate.artifacts[0].Metadata["owner"]
			delete(candidate.artifacts[0].Metadata, "owner")
			candidate.artifacts[0].Metadata["owner2"] = value
		}},
		{name: "artifact metadata value", mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata["owner"] += "!" }},
		{name: "authored command semantic", mutate: func(candidate *CommandResult) { candidate.command.Arguments.(*model.Config).AppID += ".mutated" }},
		{name: "evaluated command semantic", mutate: func(candidate *CommandResult) {
			candidate.metadata.evaluatedCommand.Arguments.(*model.Config).AppID += ".mutated"
		}},
	}
	for _, mutation := range mutations {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			t.Parallel()
			candidate := cloneCommandResult(result)
			mutation.mutate(&candidate)
			if _, ok := core.ledger.authenticate(candidate); ok {
				t.Fatal("immutable payload mutation authenticated")
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerRejectsNilEmptyShapes(t *testing.T) {
	t.Parallel()

	for _, shape := range []struct {
		name          string
		logs          []string
		artifacts     []device.Artifact
		wantNil       bool
		mutateLogs    func(*CommandResult)
		mutateResults func(*CommandResult)
	}{
		{
			name: "nil to empty", wantNil: true,
			mutateLogs:    func(candidate *CommandResult) { candidate.metadata.logMessages = []string{} },
			mutateResults: func(candidate *CommandResult) { candidate.artifacts = []device.Artifact{} },
		},
		{
			name: "empty to nil", logs: []string{}, artifacts: []device.Artifact{},
			mutateLogs:    func(candidate *CommandResult) { candidate.metadata.logMessages = nil },
			mutateResults: func(candidate *CommandResult) { candidate.artifacts = nil },
		},
	} {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			core := batch4BNewCompletedCore(t, shape.logs, shape.artifacts)
			result, runErr := core.execute(context.Background(), batch4BRichCommand(false, model.CommandAction), 0)
			if runErr != nil {
				t.Fatal(runErr)
			}
			result = batch4BAssertAuthentic(t, core.ledger, result)
			if (result.metadata.logMessages == nil) != shape.wantNil || (result.artifacts == nil) != shape.wantNil {
				t.Fatalf("core did not preserve %s expected shape", shape.name)
			}
			for _, mutation := range []struct {
				name   string
				mutate func(*CommandResult)
			}{
				{name: "logs", mutate: shape.mutateLogs},
				{name: "artifacts", mutate: shape.mutateResults},
			} {
				mutation := mutation
				t.Run(mutation.name, func(t *testing.T) {
					candidate := cloneCommandResult(result)
					mutation.mutate(&candidate)
					if _, ok := core.ledger.authenticate(candidate); ok {
						t.Fatal("equal-cardinality nil/empty shape mutation authenticated")
					}
				})
			}
		})
	}

	for _, shape := range []struct {
		name     string
		metadata map[string]string
		wantNil  bool
		mutate   func(*CommandResult)
	}{
		{name: "artifact metadata nil to empty", wantNil: true, mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata = map[string]string{} }},
		{name: "artifact metadata empty to nil", metadata: map[string]string{}, mutate: func(candidate *CommandResult) { candidate.artifacts[0].Metadata = nil }},
	} {
		shape := shape
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			artifacts := []device.Artifact{{Kind: "trace", Path: "shape.txt", Metadata: shape.metadata}}
			core := batch4BNewCompletedCore(t, nil, artifacts)
			result, runErr := core.execute(context.Background(), batch4BRichCommand(false, model.CommandAction), 0)
			if runErr != nil {
				t.Fatal(runErr)
			}
			result = batch4BAssertAuthentic(t, core.ledger, result)
			if (result.artifacts[0].Metadata == nil) != shape.wantNil {
				t.Fatalf("core did not preserve %s expected shape", shape.name)
			}
			candidate := cloneCommandResult(result)
			shape.mutate(&candidate)
			if _, ok := core.ledger.authenticate(candidate); ok {
				t.Fatal("equal-cardinality artifact metadata shape mutation authenticated")
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerRejectsFieldSourceNilEmptyShapesBothDirections(t *testing.T) {
	t.Parallel()

	for _, pointerConfig := range []bool{false, true} {
		pointerConfig := pointerConfig
		configShape := "Config"
		if pointerConfig {
			configShape = "*Config"
		}
		for _, expectedNil := range []bool{false, true} {
			expectedNil := expectedNil
			direction := "empty-to-nil"
			if expectedNil {
				direction = "nil-to-empty"
			}
			t.Run(configShape+"/"+direction, func(t *testing.T) {
				t.Parallel()
				command := batch4BRichCommand(pointerConfig, model.CommandAction)
				for _, target := range batch4BFieldSourceTargets() {
					batch4BSetFieldSourceShape(&command, target, expectedNil)
				}
				core := batch4BNewCompletedCore(t, nil, nil)
				result, runErr := core.execute(context.Background(), command, 0)
				if runErr != nil {
					t.Fatal(runErr)
				}
				result = batch4BAssertAuthentic(t, core.ledger, result)
				for _, snapshot := range []string{"authored", "evaluated"} {
					snapshot := snapshot
					for _, target := range batch4BFieldSourceTargets() {
						target := target
						t.Run(snapshot+"/"+target, func(t *testing.T) {
							candidate := cloneCommandResult(result)
							if snapshot == "authored" {
								batch4BSetFieldSourceShape(&candidate.command, target, !expectedNil)
							} else {
								batch4BSetFieldSourceShape(candidate.metadata.evaluatedCommand, target, !expectedNil)
							}
							if _, ok := core.ledger.authenticate(candidate); ok {
								t.Fatalf("%s %s FieldSources %s mutation authenticated", snapshot, target, direction)
							}
						})
					}
				}
			})
		}
	}
}

func TestInteractionBatch4BResultLedgerRejectsRecursiveFieldSourceContentMutations(t *testing.T) {
	t.Parallel()

	for _, pointerConfig := range []bool{false, true} {
		pointerConfig := pointerConfig
		configShape := "Config"
		if pointerConfig {
			configShape = "*Config"
		}
		t.Run(configShape, func(t *testing.T) {
			t.Parallel()
			core := batch4BNewCompletedCore(t, nil, nil)
			result, runErr := core.execute(context.Background(), batch4BRichCommand(pointerConfig, model.CommandAction), 0)
			if runErr != nil {
				t.Fatal(runErr)
			}
			result = batch4BAssertAuthentic(t, core.ledger, result)
			for _, snapshot := range []string{"authored", "evaluated"} {
				snapshot := snapshot
				for _, target := range batch4BFieldSourceTargets() {
					target := target
					for _, mutation := range []string{"cardinality", "key", "value"} {
						mutation := mutation
						t.Run(snapshot+"/"+target+"/"+mutation, func(t *testing.T) {
							candidate := cloneCommandResult(result)
							command := &candidate.command
							expected := &result.command
							if snapshot == "evaluated" {
								command = candidate.metadata.evaluatedCommand
								expected = result.metadata.evaluatedCommand
							}
							sources := batch4BFieldSources(command, target)
							if len(sources) != 1 {
								t.Fatalf("%s %s expected FieldSources cardinality = %d, want one", snapshot, target, len(sources))
							}
							var key string
							var value model.SourceInfo
							for key, value = range sources {
							}
							switch mutation {
							case "cardinality":
								sources["extra"] = batch4BSource("field-source-extra.yaml", 91)
							case "key":
								delete(sources, key)
								sources[key+"-mutated"] = value
							case "value":
								sources[key] = batch4BSource("field-source-mutated.yaml", 92)
							default:
								t.Fatal("unknown FieldSources mutation")
							}
							if !expected.Equivalent(*command) {
								t.Fatalf("%s %s %s mutation changed semantic equivalence", snapshot, target, mutation)
							}
							if _, ok := core.ledger.authenticate(candidate); ok {
								t.Fatalf("%s %s FieldSources %s mutation authenticated", snapshot, target, mutation)
							}
						})
					}
				}
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerAuthenticatesGenuineNilErrorAndRejectsUnexpectedPresence(t *testing.T) {
	t.Parallel()

	timeline, err := newTimeline(enginetest.NewFakeClock(time.Unix(650, 0)), "batch4b/completed-span")
	if err != nil {
		t.Fatal(err)
	}
	command := batch4BRichCommand(false, model.CommandAction)
	span, _, err := timeline.BeginCommand(command, 0)
	if err != nil {
		t.Fatal(err)
	}
	completed, _, err := span.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	ledger := newCommandResultLedger()
	if err := ledger.record(completed); err != nil {
		t.Fatal(err)
	}
	completed = batch4BAssertAuthentic(t, ledger, completed)

	withProduct := cloneCommandResult(completed)
	withProduct.productError = errors.New("unexpected")
	if _, ok := ledger.authenticate(withProduct); ok {
		t.Fatal("unexpected completed product error authenticated")
	}
	withEvaluated := cloneCommandResult(completed)
	evaluated := cloneCommand(command)
	withEvaluated.metadata.evaluatedCommand = &evaluated
	if _, ok := ledger.authenticate(withEvaluated); ok {
		t.Fatal("unexpected evaluated-command presence authenticated")
	}
	withRetry := cloneCommandResult(completed)
	withRetry.retryClassification = &retryErrorClassification{
		classification: errors.New("unexpected"),
		publication:    &exactErrorDisposition{published: errors.New("unexpected"), classification: errors.New("unexpected")},
	}
	if _, ok := ledger.authenticate(withRetry); ok {
		t.Fatal("unexpected retry/publication presence authenticated")
	}
}

func TestInteractionBatch4BResultLedgerRejectsZeroForgedForeignAndUnrecordedCandidates(t *testing.T) {
	t.Parallel()

	raw := errors.New("candidate expected")
	core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(700, 0)), raw)
	result, _ := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	result = batch4BAssertAuthentic(t, core.ledger, result)
	foreignCore := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(800, 0)), errors.New("foreign"))
	foreign, _ := foreignCore.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	foreign = batch4BAssertAuthentic(t, foreignCore.ledger, foreign)

	tests := []struct {
		name      string
		ledger    *commandResultLedger
		candidate CommandResult
	}{
		{name: "zero", ledger: core.ledger, candidate: CommandResult{}},
		{name: "forged nonzero", ledger: core.ledger, candidate: CommandResult{identity: &commandResultIdentity{marker: 1}, sequence: result.sequence, rootRunID: result.rootRunID}},
		{name: "foreign identity", ledger: core.ledger, candidate: func() CommandResult {
			candidate := cloneCommandResult(result)
			candidate.identity = foreign.identity
			return candidate
		}()},
		{name: "foreign root", ledger: core.ledger, candidate: func() CommandResult {
			candidate := cloneCommandResult(result)
			candidate.rootRunID = "foreign/root"
			return candidate
		}()},
		{name: "foreign ledger", ledger: foreignCore.ledger, candidate: result},
		{name: "unrecorded sequence", ledger: core.ledger, candidate: func() CommandResult { candidate := cloneCommandResult(result); candidate.sequence++; return candidate }()},
		{name: "nil error", ledger: core.ledger, candidate: func() CommandResult {
			candidate := cloneCommandResult(result)
			candidate.productError = nil
			return candidate
		}()},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := test.ledger.authenticate(test.candidate); ok {
				t.Fatal("forged or unrecorded candidate authenticated")
			}
		})
	}
}

func TestInteractionBatch4BResultLedgerNeverInspectsRawErrorsAndPreservesOpaqueIdentity(t *testing.T) {
	t.Parallel()

	state := &batch4BAttackerState{}
	attacker := batch4BAttackerError{state: state, bytes: []byte{1, 2, 3}}
	clock := &batch4BArmingClock{now: time.Unix(900, 0), state: state}
	core := batch4BNewExactCore(t, clock, attacker)
	result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 2)
	if runErr == nil || state.calls.Load() != 0 || !state.armed.Load() {
		t.Fatalf("attacker boundary = returned %T calls %d armed %t", runErr, state.calls.Load(), state.armed.Load())
	}
	expectedCalls := state.calls.Load()
	canonical := batch4BAssertAuthentic(t, core.ledger, result)
	if state.calls.Load() != expectedCalls {
		t.Fatalf("cloning/authentication invoked armed raw methods: expected %d now %d", expectedCalls, state.calls.Load())
	}
	batch4BAssertErrorOnlyCarrier(t, "armed attacker stored product", canonical.productError)
	if state.calls.Load() != expectedCalls {
		t.Fatalf("carrier diagnostic invoked raw methods: expected %d now %d", expectedCalls, state.calls.Load())
	}
	rawAttacker, ok := canonical.ProductError().(batch4BAttackerError)
	if !ok {
		t.Fatalf("ProductError type = %T, want batch4BAttackerError", canonical.ProductError())
	}
	rawAttacker.bytes[0] = 9
	if attacker.bytes[0] != 9 || state.calls.Load() != expectedCalls {
		t.Fatalf("attacker identity/calls = bytes %#v calls %d", attacker.bytes, state.calls.Load())
	}

	for _, mutate := range []func(*CommandResult){
		func(candidate *CommandResult) { candidate.productError = attacker },
		func(candidate *CommandResult) { candidate.retryClassification.classification = attacker },
		func(candidate *CommandResult) { candidate.retryClassification.publication.published = attacker },
		func(candidate *CommandResult) { candidate.retryClassification.publication.classification = attacker },
	} {
		candidate := cloneCommandResult(canonical)
		mutate(&candidate)
		if _, ok := core.ledger.authenticate(candidate); ok {
			t.Fatal("raw attacker substitution authenticated")
		}
		if state.calls.Load() != expectedCalls {
			t.Fatalf("raw attacker substitution invoked methods: expected %d now %d", expectedCalls, state.calls.Load())
		}
	}

	mapRaw := batch4BMapError{"seed": 1}
	wrapperRaw := batch4BValueWrapperError{values: []int{2, 3}}
	opaqueCore := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(1000, 0)), mapRaw, wrapperRaw)
	mapResult, _ := opaqueCore.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 1)
	wrapperResult, _ := opaqueCore.execute(context.Background(), batch4BRichCommand(false, model.CommandPressKey), 1)
	mapResult = batch4BAssertAuthentic(t, opaqueCore.ledger, mapResult)
	wrapperResult = batch4BAssertAuthentic(t, opaqueCore.ledger, wrapperResult)
	mapAlias, ok := mapResult.ProductError().(batch4BMapError)
	if !ok {
		t.Fatalf("map-backed ProductError type = %T", mapResult.ProductError())
	}
	mapAlias["alias"] = 4
	if mapRaw["alias"] != 4 {
		t.Fatal("map-backed ProductError lost exact raw identity")
	}
	wrapperAlias, ok := wrapperResult.ProductError().(batch4BValueWrapperError)
	if !ok {
		t.Fatalf("value-wrapper ProductError type = %T", wrapperResult.ProductError())
	}
	wrapperAlias.values[0] = 8
	if wrapperRaw.values[0] != 8 {
		t.Fatal("non-identitySafe value wrapper lost exact raw identity")
	}
	disposition, ok := opaqueCore.ledger.redeemExactError(&wrapperResult)
	if !ok {
		t.Fatal("value-wrapper exact publication did not redeem")
	}
	redeemed, ok := disposition.published.(batch4BValueWrapperError)
	if !ok {
		t.Fatalf("redeemed publication type = %T", disposition.published)
	}
	redeemed.values[1] = 11
	if wrapperRaw.values[1] != 11 {
		t.Fatal("redeemed publication lost exact raw identity")
	}
}

func TestInteractionBatch4BResultLedgerPreservesIdentitySafeAndSliceBackedRawIdentity(t *testing.T) {
	t.Parallel()

	identitySafe := batch4BIdentitySafeValueError{code: 17}
	sliceBacked := batch4BSliceError{1, 2, 3}
	core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(1100, 0)), identitySafe, sliceBacked)
	identitySafeResult, identitySafeErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 1)
	if identitySafeErr != identitySafe || identitySafeResult.ProductError() != identitySafe {
		t.Fatalf("identitySafe value identity = returned %T product %T", identitySafeErr, identitySafeResult.ProductError())
	}
	batch4BAssertAuthentic(t, core.ledger, identitySafeResult)

	sliceResult, sliceErr := core.execute(context.Background(), batch4BRichCommand(false, model.CommandPressKey), 1)
	returnedSlice, ok := sliceErr.(batch4BSliceError)
	if !ok {
		t.Fatalf("slice-backed returned error = %T", sliceErr)
	}
	productSlice, ok := sliceResult.ProductError().(batch4BSliceError)
	if !ok {
		t.Fatalf("slice-backed ProductError = %T", sliceResult.ProductError())
	}
	batch4BAssertAuthentic(t, core.ledger, sliceResult)
	returnedSlice[0] = 8
	productSlice[1] = 9
	if sliceBacked[0] != 8 || sliceBacked[1] != 9 {
		t.Fatal("slice-backed raw error lost exact interface payload identity")
	}
}

func TestInteractionBatch4BResultLedgerClonesPreserveCarrierCapabilities(t *testing.T) {
	t.Parallel()

	raw := errors.New("carrier clone expected")
	core := batch4BNewExactCore(t, enginetest.NewFakeClock(time.Unix(1200, 0)), raw)
	result, _ := core.execute(context.Background(), batch4BRichCommand(true, model.CommandPressKey), 1)
	canonical := batch4BAssertAuthentic(t, core.ledger, result)
	cloned := cloneCommandResult(canonical)
	reauthenticated := batch4BAssertAuthentic(t, core.ledger, cloned)
	canonicalSlots := batch4BErrorSlots(canonical)
	clonedSlots := batch4BErrorSlots(cloned)
	reauthenticatedSlots := batch4BErrorSlots(reauthenticated)
	for index := range canonicalSlots {
		if !batch4BIsPrivateErrorCarrier(canonicalSlots[index]) ||
			canonicalSlots[index] != clonedSlots[index] || canonicalSlots[index] != reauthenticatedSlots[index] {
			t.Fatalf("carrier capability %d was not preserved across owned clones", index)
		}
	}
}

func TestInteractionBatch4BResultLedgerRejectsAuthoredAndEvaluatedSourceOnlyMutations(t *testing.T) {
	t.Parallel()

	mutations := batch4BOriginMutations()
	for _, pointerConfig := range []bool{false, true} {
		pointerConfig := pointerConfig
		configShape := "Config"
		if pointerConfig {
			configShape = "*Config"
		}
		for _, target := range []string{"authored", "evaluated"} {
			target := target
			for _, mutation := range mutations {
				mutation := mutation
				t.Run(configShape+"/"+target+"/"+mutation.name, func(t *testing.T) {
					t.Parallel()
					core := batch4BNewCompletedCore(t, []string{"origin"}, batch4BArtifacts())
					result, runErr := core.execute(context.Background(), batch4BRichCommand(pointerConfig, model.CommandAction), 2)
					if runErr != nil {
						t.Fatal(runErr)
					}
					stored := batch4BAssertAuthentic(t, core.ledger, result)
					candidate := cloneCommandResult(stored)
					if target == "authored" {
						mutation.mutate(&candidate.command)
						if !stored.command.Equivalent(candidate.command) {
							t.Fatal("source-only authored mutation changed semantic equivalence")
						}
					} else {
						mutation.mutate(candidate.metadata.evaluatedCommand)
						if !stored.metadata.evaluatedCommand.Equivalent(*candidate.metadata.evaluatedCommand) {
							t.Fatal("source-only evaluated mutation changed semantic equivalence")
						}
					}
					if _, ok := core.ledger.authenticate(candidate); ok {
						t.Fatal("source-only mutation authenticated")
					}
				})
			}
		}
	}
}

func TestInteractionBatch4BResultLedgerConcurrentAuthenticationReturnsOwnedCanonicalClones(t *testing.T) {
	core := batch4BNewCompletedCore(t, []string{"first log", "second log"}, batch4BArtifacts())
	result, runErr := core.execute(context.Background(), batch4BRichCommand(true, model.CommandAction), 2)
	if runErr != nil {
		t.Fatal(runErr)
	}
	result = batch4BAssertAuthentic(t, core.ledger, result)

	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			candidate := cloneCommandResult(result)
			canonical, ok := core.ledger.authenticate(candidate)
			if !ok {
				t.Errorf("authentication %d rejected genuine candidate", index)
				return
			}
			canonical.command.Source.Path = "mutated"
			canonical.metadata.logMessages[0] = "mutated"
			canonical.artifacts[0].Metadata["owner"] = "mutated"
		}()
	}
	group.Wait()
	canonical, ok := core.ledger.authenticate(result)
	if !ok || canonical.command.Source.Path == "mutated" || canonical.metadata.logMessages[0] == "mutated" || canonical.artifacts[0].Metadata["owner"] == "mutated" {
		t.Fatalf("concurrent canonical mutation escaped into ledger: ok=%t", ok)
	}
}

type batch4BExactHarness struct {
	raw  []error
	next int
}

func batch4BNewExactCore(t *testing.T, clock Clock, raw ...error) *executorCore {
	t.Helper()
	harness := &batch4BExactHarness{raw: append([]error(nil), raw...)}
	spec := handlerSpec{
		keyword: model.CommandPressKey, effectClass: EffectObserved,
		exactErrorPolicy: exactErrorPublicationPressKey,
		compile:          pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate:         identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			if harness.next >= len(harness.raw) || isNilValue(harness.raw[harness.next]) {
				return commandEffect{}, NewConfigurationError("batch4b exact fixture exhausted", nil)
			}
			published := harness.raw[harness.next]
			harness.next++
			return commandEffect{
				effectClass:       EffectObserved,
				exactErrorRequest: &exactErrorRequest{underlying: published},
				numberOfRuns:      2, numberOfRunsSet: true,
				logMessages: []string{"first log", "second log"},
				insight:     "insight", aiReasoning: "AI reasoning",
				finalizedArtifacts: batch4BArtifacts(),
			}, nil
		},
	}
	core, err := newExecutorCoreForRootRun(
		coreDependencies(enginetest.NewFakeDriver(), clock), "batch4b/root-run-000001", spec, batch4BRichChildSpec(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func batch4BNewCompletedCore(t *testing.T, logs []string, artifacts []device.Artifact) *executorCore {
	t.Helper()
	spec := handlerSpec{
		keyword: model.CommandAction, effectClass: EffectObserved,
		compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{
				effectClass:  EffectObserved,
				numberOfRuns: 2, numberOfRunsSet: true,
				logMessages: batch4BCloneStrings(logs),
				insight:     "insight", aiReasoning: "AI reasoning",
				finalizedArtifacts: cloneDeviceArtifacts(artifacts),
			}, nil
		},
	}
	core, err := newExecutorCoreForRootRun(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(50, 0))),
		"batch4b/root-run-000001", spec, batch4BRichChildSpec(),
	)
	if err != nil {
		t.Fatal(err)
	}
	return core
}

func batch4BRichChildSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandTapOn, effectClass: EffectObserved,
		compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	}
}

func batch4BCloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func batch4BFieldSourceTargets() []string {
	return []string{
		"selector",
		"selector/below",
		"selector/above",
		"selector/leftOf",
		"selector/rightOf",
		"selector/containsChild",
		"selector/containsDescendants",
		"selector/childOf",
		"condition",
		"condition/visible",
		"condition/notVisible",
		"config",
		"config/onFlowStart/child/selector",
		"config/onFlowStart/child/condition",
		"config/onFlowStart/child/condition/visible",
		"config/onFlowComplete/selector",
		"child/selector",
		"child/condition",
		"child/condition/visible",
	}
}

func batch4BSetFieldSourceShape(command *model.Command, target string, nilShape bool) {
	var values map[string]model.SourceInfo
	if !nilShape {
		values = map[string]model.SourceInfo{}
	}
	switch target {
	case "selector":
		command.Selector.FieldSources = values
	case "selector/below":
		command.Selector.Below.FieldSources = values
	case "selector/above":
		command.Selector.Above.FieldSources = values
	case "selector/leftOf":
		command.Selector.LeftOf.FieldSources = values
	case "selector/rightOf":
		command.Selector.RightOf.FieldSources = values
	case "selector/containsChild":
		command.Selector.ContainsChild.FieldSources = values
	case "selector/containsDescendants":
		command.Selector.ContainsDescendants[0].FieldSources = values
	case "selector/childOf":
		command.Selector.ChildOf.FieldSources = values
	case "condition":
		command.Condition.FieldSources = values
	case "condition/visible":
		command.Condition.Visible.FieldSources = values
	case "condition/notVisible":
		command.Condition.NotVisible.FieldSources = values
	case "config":
		batch4BMutateConfig(command, func(config *model.Config) { config.FieldSources = values })
	case "config/onFlowStart/child/selector":
		batch4BMutateConfig(command, func(config *model.Config) {
			config.OnFlowStart[0].Children[0].Selector.FieldSources = values
		})
	case "config/onFlowStart/child/condition":
		batch4BMutateConfig(command, func(config *model.Config) {
			config.OnFlowStart[0].Children[0].Condition.FieldSources = values
		})
	case "config/onFlowStart/child/condition/visible":
		batch4BMutateConfig(command, func(config *model.Config) {
			config.OnFlowStart[0].Children[0].Condition.Visible.FieldSources = values
		})
	case "config/onFlowComplete/selector":
		batch4BMutateConfig(command, func(config *model.Config) {
			config.OnFlowComplete[0].Selector.FieldSources = values
		})
	case "child/selector":
		command.Children[0].Selector.FieldSources = values
	case "child/condition":
		command.Children[0].Condition.FieldSources = values
	case "child/condition/visible":
		command.Children[0].Condition.Visible.FieldSources = values
	default:
		panic("unexpected FieldSources target")
	}
}

func batch4BFieldSources(command *model.Command, target string) map[string]model.SourceInfo {
	switch target {
	case "selector":
		return command.Selector.FieldSources
	case "selector/below":
		return command.Selector.Below.FieldSources
	case "selector/above":
		return command.Selector.Above.FieldSources
	case "selector/leftOf":
		return command.Selector.LeftOf.FieldSources
	case "selector/rightOf":
		return command.Selector.RightOf.FieldSources
	case "selector/containsChild":
		return command.Selector.ContainsChild.FieldSources
	case "selector/containsDescendants":
		return command.Selector.ContainsDescendants[0].FieldSources
	case "selector/childOf":
		return command.Selector.ChildOf.FieldSources
	case "condition":
		return command.Condition.FieldSources
	case "condition/visible":
		return command.Condition.Visible.FieldSources
	case "condition/notVisible":
		return command.Condition.NotVisible.FieldSources
	case "child/selector":
		return command.Children[0].Selector.FieldSources
	case "child/condition":
		return command.Children[0].Condition.FieldSources
	case "child/condition/visible":
		return command.Children[0].Condition.Visible.FieldSources
	}
	var config model.Config
	switch value := command.Arguments.(type) {
	case model.Config:
		config = value
	case *model.Config:
		config = *value
	default:
		panic("unexpected config shape")
	}
	switch target {
	case "config":
		return config.FieldSources
	case "config/onFlowStart/child/selector":
		return config.OnFlowStart[0].Children[0].Selector.FieldSources
	case "config/onFlowStart/child/condition":
		return config.OnFlowStart[0].Children[0].Condition.FieldSources
	case "config/onFlowStart/child/condition/visible":
		return config.OnFlowStart[0].Children[0].Condition.Visible.FieldSources
	case "config/onFlowComplete/selector":
		return config.OnFlowComplete[0].Selector.FieldSources
	default:
		panic("unexpected FieldSources target")
	}
}

func batch4BMutateConfig(command *model.Command, mutate func(*model.Config)) {
	switch config := command.Arguments.(type) {
	case model.Config:
		mutate(&config)
		command.Arguments = config
	case *model.Config:
		mutate(config)
	default:
		panic("unexpected config shape")
	}
}

func batch4BAssertAuthentic(t *testing.T, ledger *commandResultLedger, candidate CommandResult) CommandResult {
	t.Helper()
	canonical, ok := ledger.authenticate(candidate)
	if !ok {
		t.Fatal("core-finalized result did not authenticate")
	}
	return canonical
}

func batch4BAssertFourCarriers(t *testing.T, result CommandResult) {
	t.Helper()
	if result.retryClassification == nil || result.retryClassification.publication == nil {
		t.Fatal("exact-error result lacks retry/publication origin")
	}
	carriers := batch4BErrorSlots(result)
	for index, carrier := range carriers {
		if !batch4BIsPrivateErrorCarrier(carrier) {
			t.Fatalf("error slot %d = %T, want private carrier", index, carrier)
		}
		for prior := 0; prior < index; prior++ {
			if carrier == carriers[prior] {
				t.Fatalf("error slots %d and %d share one carrier", prior, index)
			}
		}
	}
}

func batch4BErrorSlots(result CommandResult) []error {
	return []error{
		result.productError,
		result.retryClassification.classification,
		result.retryClassification.publication.published,
		result.retryClassification.publication.classification,
	}
}

func batch4BArtifacts() []device.Artifact {
	return []device.Artifact{
		{Kind: "trace", Path: "owned/one.txt", Metadata: map[string]string{"owner": "one", "shape": "full"}},
		{Kind: "screenshot", Path: "owned/two.png", Metadata: map[string]string{}},
	}
}

type batch4BAttackerState struct {
	armed atomic.Bool
	calls atomic.Int64
}

type batch4BAttackerError struct {
	state *batch4BAttackerState
	bytes []byte
}

func (err batch4BAttackerError) attack() {
	if err.state != nil && err.state.armed.Load() {
		err.state.calls.Add(1)
		panic("armed attacker error method called")
	}
}

func (err batch4BAttackerError) Error() string {
	err.attack()
	return "batch4b attacker"
}

func (err batch4BAttackerError) Is(error) bool {
	err.attack()
	return false
}

func (err batch4BAttackerError) As(any) bool {
	err.attack()
	return false
}

func (err batch4BAttackerError) Unwrap() error {
	err.attack()
	return nil
}

type batch4BArmingClock struct {
	now   time.Time
	calls atomic.Int64
	state *batch4BAttackerState
}

func (clock *batch4BArmingClock) Now() time.Time {
	if clock.calls.Add(1) == 2 && clock.state != nil {
		clock.state.armed.Store(true)
	}
	return clock.now
}

func (clock *batch4BArmingClock) Wait(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

type batch4BValueWrapperError struct {
	values []int
}

func (batch4BValueWrapperError) Error() string { return "batch4b non-identitySafe value wrapper" }

type batch4BIdentitySafeValueError struct {
	code int
}

func (err batch4BIdentitySafeValueError) Error() string { return "batch4b identitySafe value error" }

type batch4BSliceError []byte

func (batch4BSliceError) Error() string { return "batch4b slice-backed error" }

func batch4BRichCommand(pointerConfig bool, keyword model.CommandKeyword) model.Command {
	rootSource := batch4BSource("root.yaml", 1)
	selectorSource := batch4BSource("selector.yaml", 10)
	conditionSource := batch4BSource("condition.yaml", 20)
	configSource := batch4BSource("config.yaml", 30)
	childSource := batch4BSource("child.yaml", 40)
	linkSource := batch4BSource("link.yaml", 50)
	text := "target"
	visible := "visible"
	selector := &model.ElementSelector{
		TextRegex:           &text,
		Source:              selectorSource,
		FieldSources:        map[string]model.SourceInfo{"text": selectorSource},
		Below:               batch4BSelectorLeaf("below", batch4BSource("below.yaml", 11)),
		Above:               batch4BSelectorLeaf("above", batch4BSource("above.yaml", 12)),
		LeftOf:              batch4BSelectorLeaf("left", batch4BSource("left.yaml", 13)),
		RightOf:             batch4BSelectorLeaf("right", batch4BSource("right.yaml", 14)),
		ContainsChild:       batch4BSelectorLeaf("contains-child", batch4BSource("contains-child.yaml", 15)),
		ContainsDescendants: []model.ElementSelector{*batch4BSelectorLeaf("descendant", batch4BSource("descendant.yaml", 16))},
		ChildOf:             batch4BSelectorLeaf("child-of", batch4BSource("child-of.yaml", 17)),
	}
	condition := &model.Condition{
		Visible:      batch4BSelectorLeaf(visible, batch4BSource("condition-visible.yaml", 21)),
		NotVisible:   batch4BSelectorLeaf("not-visible", batch4BSource("condition-not-visible.yaml", 22)),
		Source:       conditionSource,
		FieldSources: map[string]model.SourceInfo{"visible": conditionSource},
	}
	config := model.Config{
		Name: "rich config", AppID: "com.example.batch4b",
		Source:       configSource,
		FieldSources: map[string]model.SourceInfo{"appId": configSource},
		OnFlowStart: []model.Command{{
			Kind: model.CommandLaunchApp, Source: batch4BSource("hook-start.yaml", 31),
			Children: []model.Command{{
				Kind: model.CommandBack, Source: batch4BSource("hook-start-child.yaml", 34),
				Selector: batch4BSelectorLeaf(
					"hook-start-child-selector", batch4BSource("hook-start-child-selector.yaml", 35),
				),
				Condition: &model.Condition{
					Visible: batch4BSelectorLeaf(
						"hook-start-child-visible", batch4BSource("hook-start-child-visible.yaml", 36),
					),
					Source:       batch4BSource("hook-start-child-condition.yaml", 37),
					FieldSources: map[string]model.SourceInfo{"visible": batch4BSource("hook-start-child-condition.yaml", 37)},
				},
			}},
		}},
		OnFlowComplete: []model.Command{{Kind: model.CommandAssertVisible, Selector: batch4BSelectorLeaf("hook-complete", batch4BSource("hook-complete-selector.yaml", 32)), Source: batch4BSource("hook-complete.yaml", 33)}},
	}
	var arguments any = config
	if pointerConfig {
		arguments = &config
	}
	return model.Command{
		Kind: keyword, Form: model.CommandFormObject,
		Arguments: arguments,
		Selector:  selector,
		Condition: condition,
		Children: []model.Command{{
			Kind: model.CommandTapOn, Source: childSource,
			Selector: batch4BSelectorLeaf("child-selector", batch4BSource("child-selector.yaml", 41)),
			Condition: &model.Condition{
				Visible:      batch4BSelectorLeaf("child-visible", batch4BSource("child-visible.yaml", 42)),
				Source:       batch4BSource("child-condition.yaml", 43),
				FieldSources: map[string]model.SourceInfo{"visible": batch4BSource("child-condition.yaml", 43)},
			},
		}},
		Links: []model.FileLink{{
			Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: "/workspace/child.yaml", Source: linkSource,
		}},
		Source: rootSource,
	}
}

func batch4BSource(path string, line int) model.SourceInfo {
	return model.SourceInfo{
		Path:  path,
		Start: model.Position{Line: line, Column: 2, Offset: line * 10},
		End:   model.Position{Line: line, Column: 8, Offset: line*10 + 6},
	}
}

func batch4BSelectorLeaf(text string, source model.SourceInfo) *model.ElementSelector {
	return &model.ElementSelector{
		TextRegex:    &text,
		Source:       source,
		FieldSources: map[string]model.SourceInfo{"text": source},
	}
}

type batch4BOriginMutation struct {
	name   string
	mutate func(*model.Command)
}

func batch4BOriginMutations() []batch4BOriginMutation {
	mutated := batch4BSource("mutated.yaml", 99)
	mutateConfig := func(command *model.Command, mutate func(*model.Config)) {
		switch value := command.Arguments.(type) {
		case model.Config:
			mutate(&value)
			command.Arguments = value
		case *model.Config:
			mutate(value)
		default:
			panic("unexpected config shape")
		}
	}
	return []batch4BOriginMutation{
		{name: "command source", mutate: func(command *model.Command) { command.Source = mutated }},
		{name: "command source start", mutate: func(command *model.Command) { command.Source.Start.Line++ }},
		{name: "command source end", mutate: func(command *model.Command) { command.Source.End.Offset++ }},
		{name: "selector source", mutate: func(command *model.Command) { command.Selector.Source = mutated }},
		{name: "selector source position", mutate: func(command *model.Command) { command.Selector.Source.Start.Column++ }},
		{name: "selector fields nil", mutate: func(command *model.Command) { command.Selector.FieldSources = nil }},
		{name: "selector fields empty", mutate: func(command *model.Command) { command.Selector.FieldSources = map[string]model.SourceInfo{} }},
		{name: "selector field value", mutate: func(command *model.Command) { command.Selector.FieldSources["text"] = mutated }},
		{name: "selector field position", mutate: func(command *model.Command) {
			source := command.Selector.FieldSources["text"]
			source.End.Column++
			command.Selector.FieldSources["text"] = source
		}},
		{name: "selector below", mutate: func(command *model.Command) { command.Selector.Below.Source = mutated }},
		{name: "selector below position", mutate: func(command *model.Command) { command.Selector.Below.Source.Start.Offset++ }},
		{name: "selector above", mutate: func(command *model.Command) { command.Selector.Above.Source = mutated }},
		{name: "selector above position", mutate: func(command *model.Command) { command.Selector.Above.Source.End.Column++ }},
		{name: "selector leftOf", mutate: func(command *model.Command) { command.Selector.LeftOf.Source = mutated }},
		{name: "selector leftOf position", mutate: func(command *model.Command) { command.Selector.LeftOf.Source.Start.Column++ }},
		{name: "selector rightOf", mutate: func(command *model.Command) { command.Selector.RightOf.Source = mutated }},
		{name: "selector rightOf position", mutate: func(command *model.Command) { command.Selector.RightOf.Source.End.Offset++ }},
		{name: "selector containsChild", mutate: func(command *model.Command) { command.Selector.ContainsChild.Source = mutated }},
		{name: "selector containsChild position", mutate: func(command *model.Command) { command.Selector.ContainsChild.Source.Start.Line++ }},
		{name: "selector containsDescendants", mutate: func(command *model.Command) { command.Selector.ContainsDescendants[0].Source = mutated }},
		{name: "selector containsDescendants position", mutate: func(command *model.Command) {
			command.Selector.ContainsDescendants[0].Source.End.Line++
		}},
		{name: "selector childOf", mutate: func(command *model.Command) { command.Selector.ChildOf.Source = mutated }},
		{name: "selector childOf position", mutate: func(command *model.Command) { command.Selector.ChildOf.Source.Start.Offset++ }},
		{name: "condition source", mutate: func(command *model.Command) { command.Condition.Source = mutated }},
		{name: "condition source position", mutate: func(command *model.Command) { command.Condition.Source.End.Line++ }},
		{name: "condition fields nil", mutate: func(command *model.Command) { command.Condition.FieldSources = nil }},
		{name: "condition fields empty", mutate: func(command *model.Command) { command.Condition.FieldSources = map[string]model.SourceInfo{} }},
		{name: "condition field value", mutate: func(command *model.Command) { command.Condition.FieldSources["visible"] = mutated }},
		{name: "condition field position", mutate: func(command *model.Command) {
			source := command.Condition.FieldSources["visible"]
			source.Start.Column++
			command.Condition.FieldSources["visible"] = source
		}},
		{name: "condition visible", mutate: func(command *model.Command) { command.Condition.Visible.Source = mutated }},
		{name: "condition visible position", mutate: func(command *model.Command) { command.Condition.Visible.Source.End.Offset++ }},
		{name: "condition notVisible", mutate: func(command *model.Command) { command.Condition.NotVisible.Source = mutated }},
		{name: "condition notVisible position", mutate: func(command *model.Command) { command.Condition.NotVisible.Source.Start.Line++ }},
		{name: "config source", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.Source = mutated })
		}},
		{name: "config source position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.Source.Start.Offset++ })
		}},
		{name: "config fields nil", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.FieldSources = nil })
		}},
		{name: "config fields empty", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.FieldSources = map[string]model.SourceInfo{} })
		}},
		{name: "config field value", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.FieldSources["appId"] = mutated })
		}},
		{name: "config field position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				source := config.FieldSources["appId"]
				source.End.Column++
				config.FieldSources["appId"] = source
			})
		}},
		{name: "config onFlowStart", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowStart[0].Source = mutated })
		}},
		{name: "config onFlowStart position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowStart[0].Source.Start.Offset++ })
		}},
		{name: "config onFlowComplete", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowComplete[0].Source = mutated })
		}},
		{name: "config onFlowComplete position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowComplete[0].Source.End.Offset++ })
		}},
		{name: "config hook selector", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowComplete[0].Selector.Source = mutated })
		}},
		{name: "config hook selector position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowComplete[0].Selector.Source.Start.Column++ })
		}},
		{name: "config hook selector fields", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowComplete[0].Selector.FieldSources["text"] = mutated })
		}},
		{name: "config hook selector field position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				source := config.OnFlowComplete[0].Selector.FieldSources["text"]
				source.End.Line++
				config.OnFlowComplete[0].Selector.FieldSources["text"] = source
			})
		}},
		{name: "config hook nested child", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowStart[0].Children[0].Source = mutated })
		}},
		{name: "config hook nested child position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) { config.OnFlowStart[0].Children[0].Source.End.Column++ })
		}},
		{name: "config hook nested child selector position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				config.OnFlowStart[0].Children[0].Selector.Source.Start.Offset++
			})
		}},
		{name: "config hook nested child selector field position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				source := config.OnFlowStart[0].Children[0].Selector.FieldSources["text"]
				source.End.Offset++
				config.OnFlowStart[0].Children[0].Selector.FieldSources["text"] = source
			})
		}},
		{name: "config hook nested child condition position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				config.OnFlowStart[0].Children[0].Condition.Source.Start.Line++
			})
		}},
		{name: "config hook nested child condition field position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				source := config.OnFlowStart[0].Children[0].Condition.FieldSources["visible"]
				source.Start.Offset++
				config.OnFlowStart[0].Children[0].Condition.FieldSources["visible"] = source
			})
		}},
		{name: "config hook nested child condition selector position", mutate: func(command *model.Command) {
			mutateConfig(command, func(config *model.Config) {
				config.OnFlowStart[0].Children[0].Condition.Visible.Source.End.Line++
			})
		}},
		{name: "child source", mutate: func(command *model.Command) { command.Children[0].Source = mutated }},
		{name: "child source position", mutate: func(command *model.Command) { command.Children[0].Source.Start.Column++ }},
		{name: "child selector source", mutate: func(command *model.Command) { command.Children[0].Selector.Source = mutated }},
		{name: "child selector source position", mutate: func(command *model.Command) { command.Children[0].Selector.Source.End.Offset++ }},
		{name: "child selector fields", mutate: func(command *model.Command) { command.Children[0].Selector.FieldSources["text"] = mutated }},
		{name: "child selector field position", mutate: func(command *model.Command) {
			source := command.Children[0].Selector.FieldSources["text"]
			source.Start.Line++
			command.Children[0].Selector.FieldSources["text"] = source
		}},
		{name: "child condition source", mutate: func(command *model.Command) { command.Children[0].Condition.Source = mutated }},
		{name: "child condition source position", mutate: func(command *model.Command) { command.Children[0].Condition.Source.End.Column++ }},
		{name: "child condition fields", mutate: func(command *model.Command) { command.Children[0].Condition.FieldSources["visible"] = mutated }},
		{name: "child condition field position", mutate: func(command *model.Command) {
			source := command.Children[0].Condition.FieldSources["visible"]
			source.End.Offset++
			command.Children[0].Condition.FieldSources["visible"] = source
		}},
		{name: "child condition selector", mutate: func(command *model.Command) { command.Children[0].Condition.Visible.Source = mutated }},
		{name: "child condition selector position", mutate: func(command *model.Command) {
			command.Children[0].Condition.Visible.Source.Start.Offset++
		}},
		{name: "link source", mutate: func(command *model.Command) { command.Links[0].Source = mutated }},
		{name: "link source position", mutate: func(command *model.Command) { command.Links[0].Source.Start.Offset++ }},
		{name: "link resolved path", mutate: func(command *model.Command) { command.Links[0].ResolvedPath += ".mutated" }},
	}
}
