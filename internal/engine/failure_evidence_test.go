package engine

import (
	"bytes"
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

func TestFailureEvidenceCapturesEligibleNormalizedFailures(t *testing.T) {
	t.Parallel()

	assertion := NewAssertionError("required assertion failed", nil)
	warned := NewAssertionError("optional assertion failed", nil)
	ordinary := errors.New("ordinary driver failure")
	wrapped := fmt.Errorf("wrapped assertion: %w", NewAssertionError("wrapped", nil))
	joined := errors.Join(NewAssertionError("joined assertion", nil), NewOperationError("joined operation", nil))
	customAs := &asOnlyError{target: asFlowBaton}
	tests := []struct {
		name         string
		primary      error
		optional     bool
		wantOutcome  Outcome
		wantIdentity bool
	}{
		{name: "required assertion", primary: assertion, wantOutcome: Failed, wantIdentity: true},
		{name: "optional assertion", primary: warned, optional: true, wantOutcome: Warned, wantIdentity: true},
		{name: "ordinary normalizes to operation", primary: ordinary, wantOutcome: Failed},
		{name: "wrapped retryable", primary: wrapped, wantOutcome: Failed, wantIdentity: true},
		{name: "joined retryable", primary: joined, wantOutcome: Failed, wantIdentity: true},
		{name: "custom As retryable", primary: customAs, wantOutcome: Failed, wantIdentity: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			trace := &failureEvidenceTrace{}
			driver := newFailureEvidenceDriver(enginetest.PNGFixture("png-data"), nil, trace)
			sink := &failureEvidenceSink{
				trace: trace,
				responses: []failureEvidenceSinkResponse{{result: ArtifactWriteResult{
					Artifact: device.Artifact{
						Kind: failureScreenshotArtifactKind, Path: "sink/final-failure.png",
						Metadata: map[string]string{"provider": "owned"},
					},
					BytesWritten: int64(len(enginetest.PNGFixture("png-data"))),
				}}},
			}
			var finished Event
			dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(100, 0)))
			dependencies.ArtifactSink = sink
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandFinished {
					finished = event
				}
				return nil
			})}
			core, err := newExecutorCoreForRootRun(
				dependencies,
				"root-run-batch4",
				failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, test.primary),
			)
			if err != nil {
				t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
			}
			command := failureEvidenceCommand(test.optional)
			result, executeErr := core.execute(context.Background(), command, 2)

			if result.Outcome() != test.wantOutcome || result.ProductError() == nil {
				t.Fatalf("result = outcome %s error %T %v", result.Outcome(), result.ProductError(), result.ProductError())
			}
			if test.wantOutcome == Warned {
				if executeErr != nil {
					t.Fatalf("optional warning surfaced %T %v", executeErr, executeErr)
				}
			} else if executeErr != result.ProductError() {
				t.Fatalf("returned/result primary identity = %s / %s", errorIdentity(executeErr), errorIdentity(result.ProductError()))
			}
			if test.wantIdentity && result.ProductError() != test.primary {
				t.Fatalf("normalized primary identity changed: got %s want %s", errorIdentity(result.ProductError()), errorIdentity(test.primary))
			}
			if !test.wantIdentity && !errors.Is(result.ProductError(), test.primary) {
				t.Fatalf("normalized ordinary failure lost cause: %T %v", result.ProductError(), result.ProductError())
			}
			if finished.ProductError() != result.ProductError() || finished.Outcome() != result.Outcome() {
				t.Fatalf("finished event primary/outcome = %s/%s", errorIdentity(finished.ProductError()), finished.Outcome())
			}
			requests := driver.Requests()
			if len(requests) != 1 || requests[0] != (device.ScreenshotRequest{Compressed: true}) {
				t.Fatalf("screenshot requests = %#v, want one compressed request", requests)
			}
			writes := sink.Requests()
			if len(writes) != 1 {
				t.Fatalf("sink writes = %#v, want one", writes)
			}
			wantMetadata := map[string]string{
				"keyword": "assertVisible", "rootRunId": "root-run-batch4", "sequence": "1",
				"depth": "2", "outcome": string(test.wantOutcome), "sourcePath": "/workspace/failure.yaml",
			}
			if writes[0].Owner != "root-run-batch4" || writes[0].Kind != failureScreenshotArtifactKind || writes[0].SuggestedName != "failure-000001.png" || !bytes.Equal(writes[0].Data, enginetest.PNGFixture("png-data")) || !reflect.DeepEqual(writes[0].Metadata, wantMetadata) {
				t.Fatalf("failure write = %#v, want exact deterministic request %#v", writes[0], wantMetadata)
			}
			assertFailureEvidenceArtifacts(t, result.Artifacts(), "sink/final-failure.png")
			assertFailureEvidenceArtifacts(t, finished.Artifacts(), "sink/final-failure.png")
			returned := result.Artifacts()
			returned[0].Path = "caller-mutated.png"
			returned[0].Metadata["provider"] = "caller-mutated"
			assertFailureEvidenceArtifacts(t, result.Artifacts(), "sink/final-failure.png")
		})
	}
}

func TestFailureEvidenceOmitsOnlyBlankSourcePath(t *testing.T) {
	t.Parallel()

	primary := NewAssertionError("missing", nil)
	driver := newFailureEvidenceDriver([]byte("png"), nil, nil)
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("blank-source.png", 3)}}}
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(150, 0)))
	dependencies.ArtifactSink = sink
	core, err := newExecutorCoreForRootRun(dependencies, "root-blank-source", failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, primary))
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
	}
	command := failureEvidenceCommand(false)
	command.Source.Path = " \t "
	result, executeErr := core.execute(context.Background(), command, 4)
	requests := sink.Requests()
	if executeErr != primary || result.ProductError() != primary || len(result.Artifacts()) != 1 || len(requests) != 1 {
		t.Fatalf("blank source result = error %T %v artifacts %#v requests %#v", executeErr, executeErr, result.Artifacts(), requests)
	}
	if _, exists := requests[0].Metadata["sourcePath"]; exists {
		t.Fatalf("blank sourcePath was retained: %#v", requests[0].Metadata)
	}
	wantMetadata := map[string]string{
		"keyword": "assertVisible", "rootRunId": "root-blank-source", "sequence": "1", "depth": "4", "outcome": "Failed",
	}
	if !reflect.DeepEqual(requests[0].Metadata, wantMetadata) {
		t.Fatalf("blank source metadata = %#v, want %#v", requests[0].Metadata, wantMetadata)
	}
}

func TestFailureEvidenceExcludesEveryIneligibleOutcomeAndTerminalClass(t *testing.T) {
	t.Parallel()

	causalCycle := &graphCausalError{}
	causalCycle.cause = causalCycle
	aggregateCycle := &graphAggregateError{}
	aggregateCycle.branches = []error{aggregateCycle}
	var typedNil *OperationError
	deep := error(NewAssertionError("deep leaf", nil))
	for range maxTerminalErrorTraversalDepth {
		deep = &graphCausalError{cause: deep}
	}
	ordinaryAggregate := errors.Join(errors.New("ordinary one"), errors.New("ordinary two"))
	tests := []struct {
		name         string
		primary      error
		wantOutcome  Outcome
		wantIdentity bool
	}{
		{name: "completed", primary: nil, wantOutcome: Completed, wantIdentity: true},
		{name: "skipped", primary: NewCommandSkippedError("skip", nil), wantOutcome: Skipped, wantIdentity: true},
		{name: "cancelled", primary: context.Canceled, wantOutcome: Cancelled, wantIdentity: true},
		{name: "configuration", primary: NewConfigurationError("invalid", nil), wantOutcome: Failed, wantIdentity: true},
		{name: "device connection", primary: NewDeviceConnectionError("disconnected", nil), wantOutcome: Failed, wantIdentity: true},
		{name: "ordinary nonretryable aggregate", primary: ordinaryAggregate, wantOutcome: Failed, wantIdentity: true},
		{name: "joined retryable and cancellation", primary: errors.Join(NewAssertionError("retryable", nil), context.Canceled), wantOutcome: Cancelled, wantIdentity: true},
		{name: "joined retryable and configuration", primary: errors.Join(NewAssertionError("retryable", nil), NewConfigurationError("invalid", nil)), wantOutcome: Failed, wantIdentity: true},
		{name: "joined retryable and device", primary: errors.Join(NewAssertionError("retryable", nil), NewDeviceConnectionError("disconnected", nil)), wantOutcome: Failed, wantIdentity: true},
		{name: "custom As configuration", primary: &asOnlyError{target: asConfiguration}, wantOutcome: Failed, wantIdentity: true},
		{name: "custom As device", primary: &asOnlyError{target: asDeviceConnection}, wantOutcome: Failed, wantIdentity: true},
		{name: "malformed causal cycle", primary: causalCycle, wantOutcome: Failed},
		{name: "malformed aggregate cycle", primary: aggregateCycle, wantOutcome: Failed},
		{name: "malformed typed nil", primary: error(typedNil), wantOutcome: Failed},
		{name: "malformed panicking Is", primary: &panickingIsError{}, wantOutcome: Failed},
		{name: "malformed panicking As", primary: &panickingAsError{}, wantOutcome: Failed},
		{name: "malformed panicking causal unwrap", primary: &graphCausalError{panicUnwrap: true}, wantOutcome: Failed},
		{name: "malformed panicking aggregate unwrap", primary: &graphAggregateError{panicUnwrap: true}, wantOutcome: Failed},
		{name: "malformed deep graph", primary: deep, wantOutcome: Failed},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			driver := newFailureEvidenceDriver([]byte("must-not-capture"), nil, nil)
			sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("must-not-exist.png", 16)}}}
			dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(200, 0)))
			dependencies.ArtifactSink = sink
			core, err := newExecutorCore(dependencies, failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, test.primary))
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			result, executeErr := core.execute(context.Background(), failureEvidenceCommand(false), 0)
			if result.Outcome() != test.wantOutcome || len(result.Artifacts()) != 0 || len(driver.Requests()) != 0 || len(sink.Requests()) != 0 {
				t.Fatalf("excluded result = outcome %s artifacts %#v screens %d sinks %d", result.Outcome(), result.Artifacts(), len(driver.Requests()), len(sink.Requests()))
			}
			if test.primary == nil {
				if executeErr != nil || result.ProductError() != nil {
					t.Fatalf("completed result error = %T %v / %T %v", executeErr, executeErr, result.ProductError(), result.ProductError())
				}
				return
			}
			if test.wantOutcome == Skipped {
				if executeErr != nil {
					t.Fatalf("skipped result surfaced %T %v", executeErr, executeErr)
				}
			} else if executeErr != result.ProductError() {
				t.Fatalf("excluded primary identity differs between result/return: %s / %s", errorIdentity(result.ProductError()), errorIdentity(executeErr))
			}
			if test.wantIdentity && result.ProductError() != test.primary {
				t.Fatalf("excluded primary changed: got %s want %s", errorIdentity(result.ProductError()), errorIdentity(test.primary))
			}
			if !test.wantIdentity && !isConfigurationError(result.ProductError()) {
				t.Fatalf("malformed primary normalized to %T %v, want configuration", result.ProductError(), result.ProductError())
			}
		})
	}
}

func TestFailureEvidenceRequiresLiveNonTypedNilSinkBeforeScreenshot(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		sink ArtifactSink
	}{
		{name: "nil", sink: nil},
		{name: "typed nil", sink: (*failureEvidenceSink)(nil)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			primary := NewAssertionError("missing", nil)
			driver := newFailureEvidenceDriver([]byte("must-not-capture"), nil, nil)
			dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(300, 0)))
			dependencies.ArtifactSink = test.sink
			core, err := newExecutorCore(dependencies, failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, primary))
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			result, executeErr := core.execute(context.Background(), failureEvidenceCommand(false), 0)
			if executeErr != primary || result.ProductError() != primary || result.Outcome() != Failed || len(result.Artifacts()) != 0 || len(driver.Requests()) != 0 {
				t.Fatalf("nil sink = returned %s result %s outcome %s artifacts %#v screens %d", errorIdentity(executeErr), errorIdentity(result.ProductError()), result.Outcome(), result.Artifacts(), len(driver.Requests()))
			}
		})
	}
}

func TestFailureEvidencePreCancelledExecutionDoesNotCapture(t *testing.T) {
	t.Parallel()

	driver := newFailureEvidenceDriver([]byte("must-not-capture"), nil, nil)
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("must-not-exist.png", 16)}}}
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(350, 0)))
	dependencies.ArtifactSink = sink
	core, err := newExecutorCore(dependencies, failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, NewAssertionError("must-not-run", nil)))
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, executeErr := core.execute(ctx, failureEvidenceCommand(false), 0)
	if executeErr != context.Canceled || result.ProductError() != context.Canceled || result.Outcome() != Cancelled || len(result.Artifacts()) != 0 || len(driver.Requests()) != 0 || len(sink.Requests()) != 0 {
		t.Fatalf("pre-cancelled = returned %T %v result %T %v outcome %s artifacts %#v screens %d sinks %d", executeErr, executeErr, result.ProductError(), result.ProductError(), result.Outcome(), result.Artifacts(), len(driver.Requests()), len(sink.Requests()))
	}
}

func TestFailureEvidenceDiagnosticFailuresStaySilentAndKeepExactPrimary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		optional        bool
		screenshot      []byte
		screenshotErr   error
		cancelScreen    bool
		panicScreen     bool
		sinkResponse    ArtifactWriteResult
		sinkErr         error
		cancelSink      bool
		panicSink       bool
		wantSinkCalls   int
		wantScreenCalls int
	}{
		{name: "nil screenshot", screenshot: nil, wantScreenCalls: 1},
		{name: "empty screenshot", screenshot: []byte{}, wantScreenCalls: 1},
		{name: "screenshot bytes and error", screenshot: []byte("partial"), screenshotErr: errors.New("screenshot failed"), wantScreenCalls: 1},
		{name: "context ignoring screenshot cancels", screenshot: []byte("png"), cancelScreen: true, wantScreenCalls: 1},
		{name: "optional screenshot cancellation", optional: true, screenshot: []byte("png"), cancelScreen: true, wantScreenCalls: 1},
		{name: "screenshot provider panic", screenshot: []byte("png"), panicScreen: true, wantScreenCalls: 1},
		{name: "sink partial and error", screenshot: []byte("png"), sinkResponse: validFailureEvidenceResult("partial.png", 3), sinkErr: errors.New("sink failed"), wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "optional sink failure", optional: true, screenshot: []byte("png"), sinkResponse: validFailureEvidenceResult("optional-partial.png", 3), sinkErr: errors.New("sink failed"), wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "context ignoring sink cancels", screenshot: []byte("png"), sinkResponse: validFailureEvidenceResult("cancelled.png", 3), cancelSink: true, wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "zero sink success", screenshot: []byte("png"), sinkResponse: ArtifactWriteResult{}, wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "wrong sink kind", screenshot: []byte("png"), sinkResponse: ArtifactWriteResult{Artifact: device.Artifact{Kind: "other", Path: "wrong.png"}, BytesWritten: 3}, wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "blank sink path", screenshot: []byte("png"), sinkResponse: ArtifactWriteResult{Artifact: device.Artifact{Kind: failureScreenshotArtifactKind, Path: "  "}, BytesWritten: 3}, wantScreenCalls: 1, wantSinkCalls: 1},
		{name: "sink provider panic", screenshot: []byte("png"), panicSink: true, wantScreenCalls: 1, wantSinkCalls: 1},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			primary := NewAssertionError("primary must survive", nil)
			driver := newFailureEvidenceDriver(test.screenshot, test.screenshotErr, nil)
			driver.panicCall = test.panicScreen
			if test.cancelScreen {
				driver.cancel = cancel
			}
			sink := &failureEvidenceSink{
				responses: []failureEvidenceSinkResponse{{result: test.sinkResponse, err: test.sinkErr}},
				panicCall: test.panicSink,
			}
			if test.cancelSink {
				sink.cancel = cancel
			}
			events := make([]Event, 0, 2)
			dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(400, 0)))
			dependencies.ArtifactSink = sink
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})}
			core, err := newExecutorCore(dependencies, failureEvidenceHandler(commandEffect{
				effectClass: EffectObserved, logMessages: []string{"handler-log"},
			}, primary))
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			command := failureEvidenceCommand(test.optional)
			before := cloneCommand(command)
			result, executeErr := core.execute(ctx, command, 0)
			cancel()
			wantOutcome := Failed
			if test.optional {
				wantOutcome = Warned
			}
			primaryPreserved := executeErr == primary
			if test.optional {
				primaryPreserved = executeErr == nil
			}
			if !primaryPreserved || result.ProductError() != primary || result.Outcome() != wantOutcome {
				t.Fatalf("diagnostic replaced primary = returned %s result %s outcome %s", errorIdentity(executeErr), errorIdentity(result.ProductError()), result.Outcome())
			}
			if len(result.Artifacts()) != 0 || len(driver.Requests()) != test.wantScreenCalls || len(sink.Requests()) != test.wantSinkCalls {
				t.Fatalf("diagnostic leaked = artifacts %#v screens %d/%d sinks %d/%d", result.Artifacts(), len(driver.Requests()), test.wantScreenCalls, len(sink.Requests()), test.wantSinkCalls)
			}
			if len(events) != 2 || events[0].Kind() != EventCommandStarted || events[1].Kind() != EventCommandFinished || events[1].ProductError() != primary || len(events[1].Artifacts()) != 0 {
				t.Fatalf("diagnostic event surface = %#v", events)
			}
			metadata := result.Metadata()
			if !reflect.DeepEqual(metadata.LogMessages(), []string{"handler-log"}) || metadata.NumberOfRuns() != 1 || !metadata.HasNumberOfRuns() {
				t.Fatalf("diagnostic changed metadata = runs %d/%t logs %#v", metadata.NumberOfRuns(), metadata.HasNumberOfRuns(), metadata.LogMessages())
			}
			if !reflect.DeepEqual(command, before) {
				t.Fatalf("diagnostic changed source command: got %#v want %#v", command, before)
			}
		})
	}
}

func TestFailureEvidenceAppendsAfterHandlerArtifactsAndOwnsEveryProjection(t *testing.T) {
	t.Parallel()

	trace := &failureEvidenceTrace{}
	diagnosticBytes := enginetest.PNGFixture("diagnostic-png")
	driver := newFailureEvidenceDriver(diagnosticBytes, nil, trace)
	handlerWriteData := []byte("handler-png")
	handlerFinalized := []device.Artifact{{
		Kind: "recording", Path: "owned/handler-recording.mp4", Metadata: map[string]string{"owner": "handler-finalized"},
	}}
	sink := &failureEvidenceSink{
		trace:       trace,
		mutateInput: true,
		responses: []failureEvidenceSinkResponse{
			{result: ArtifactWriteResult{Artifact: device.Artifact{Kind: "handler-screenshot", Path: "owned/handler-write.png", Metadata: map[string]string{"owner": "handler-write"}}, BytesWritten: int64(len(handlerWriteData))}},
			{result: ArtifactWriteResult{Artifact: device.Artifact{Kind: failureScreenshotArtifactKind, Path: "owned/failure.png", Metadata: map[string]string{"owner": "diagnostic"}}, BytesWritten: int64(len(diagnosticBytes))}},
		},
	}
	primary := NewAssertionError("primary assertion", nil)
	effect := commandEffect{
		effectClass:        EffectObserved,
		logMessages:        []string{"handler-log"},
		finalizedArtifacts: handlerFinalized,
		artifactWrites: []ArtifactWriteRequest{{
			Owner: "handler", Kind: "handler-screenshot", SuggestedName: "handler.png",
			Data: handlerWriteData, Metadata: map[string]string{"stage": "handler"},
		}},
	}
	command := failureEvidenceCommand(false)
	before := cloneCommand(command)
	var finished Event
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(500, 0)))
	dependencies.ArtifactSink = sink
	dependencies.Listeners = []Listener{
		ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() != EventCommandFinished {
				return nil
			}
			trace.Add("finished")
			finished = event
			artifacts := event.Artifacts()
			artifacts[0].Path = "listener-mutated.mp4"
			artifacts[0].Metadata["owner"] = "listener-mutated"
			listenerCommand, exists := event.Command()
			if exists {
				listenerCommand.Source.Path = "listener-mutated.yaml"
			}
			return errors.New("listener failure")
		}),
		ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventCommandFinished {
				trace.Add("listener-panic")
				panic("listener panic")
			}
			return nil
		}),
		ListenerFunc(func(_ context.Context, event Event) error {
			if event.Kind() == EventCommandFinished {
				trace.Add("after-listener-panic")
			}
			return nil
		}),
	}
	core, err := newExecutorCoreForRootRun(dependencies, "root-artifact-order", failureEvidenceHandler(effect, primary))
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
	}
	result, executeErr := core.execute(context.Background(), command, 3)
	if executeErr != primary || result.ProductError() != primary || result.Outcome() != Failed {
		t.Fatalf("execution primary = returned %s result %s outcome %s", errorIdentity(executeErr), errorIdentity(result.ProductError()), result.Outcome())
	}
	wantTrace := []string{"sink:handler-screenshot", "screenshot", "sink:failure-screenshot", "finished", "listener-panic", "after-listener-panic"}
	if got := trace.Snapshot(); !reflect.DeepEqual(got, wantTrace) {
		t.Fatalf("artifact/finalization trace = %#v, want %#v", got, wantTrace)
	}
	wantPaths := []string{"owned/handler-recording.mp4", "owned/handler-write.png", "owned/failure.png"}
	assertFailureEvidenceArtifactOrder(t, result.Artifacts(), wantPaths)
	assertFailureEvidenceArtifactOrder(t, finished.Artifacts(), wantPaths)
	ledger := core.ledger.snapshot()
	if len(ledger) != 1 {
		t.Fatalf("ledger = %#v, want one result", ledger)
	}
	assertFailureEvidenceArtifactOrder(t, ledger[0].Artifacts(), wantPaths)

	writes := sink.Requests()
	if len(writes) != 2 || writes[0].Kind != "handler-screenshot" || writes[1].Kind != failureScreenshotArtifactKind {
		t.Fatalf("sink request order = %#v", writes)
	}
	wantMetadata := map[string]string{
		"keyword": "assertVisible", "rootRunId": "root-artifact-order", "sequence": "1",
		"depth": "3", "outcome": "Failed", "sourcePath": "/workspace/failure.yaml",
	}
	if writes[1].Owner != "root-artifact-order" || writes[1].SuggestedName != "failure-000001.png" || !bytes.Equal(writes[1].Data, enginetest.PNGFixture("diagnostic-png")) || !reflect.DeepEqual(writes[1].Metadata, wantMetadata) {
		t.Fatalf("diagnostic sink request = %#v", writes[1])
	}
	if !bytes.Equal(diagnosticBytes, enginetest.PNGFixture("diagnostic-png")) || string(handlerWriteData) != "handler-png" || !reflect.DeepEqual(command, before) {
		t.Fatalf("provider mutation escaped ownership: diagnostic=%q handler=%q command=%#v", diagnosticBytes, handlerWriteData, command)
	}

	sink.MutateResponseMetadata("provider-mutated")
	diagnosticBytes[0] = 'X'
	handlerWriteData[0] = 'X'
	handlerFinalized[0].Path = "producer-mutated.mp4"
	handlerFinalized[0].Metadata["owner"] = "producer-mutated"
	assertFailureEvidenceArtifactOrder(t, result.Artifacts(), wantPaths)
	if result.Artifacts()[2].Metadata["owner"] != "diagnostic" || result.Artifacts()[0].Metadata["owner"] != "handler-finalized" {
		t.Fatalf("result aliases producer/sink metadata: %#v", result.Artifacts())
	}
	if !reflect.DeepEqual(result.Metadata().LogMessages(), []string{"handler-log"}) {
		t.Fatalf("listener changed result metadata: %#v", result.Metadata().LogMessages())
	}
}

func TestFailureEvidenceRepeatedAndConcurrentExecutionsOwnInputs(t *testing.T) {
	t.Parallel()

	command := failureEvidenceCommand(false)
	command.Arguments.(map[string]any)["nested"] = map[string]any{"value": "source"}
	before := cloneCommand(command)
	sharedScreenshot := enginetest.PNGFixture("shared-screenshot")

	driver := newFailureEvidenceDriver(sharedScreenshot, nil, nil)
	sink := &failureEvidenceSink{mutateInput: true, responses: []failureEvidenceSinkResponse{
		{result: validFailureEvidenceResult("repeat-1.png", int64(len(sharedScreenshot)))},
		{result: validFailureEvidenceResult("repeat-2.png", int64(len(sharedScreenshot)))},
	}}
	primary := NewAssertionError("repeat primary", nil)
	dependencies := coreDependencies(driver, enginetest.NewFakeClock(time.Unix(600, 0)))
	dependencies.ArtifactSink = sink
	core, err := newExecutorCoreForRootRun(dependencies, "root-repeat", failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, primary))
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
	}
	for index := range 2 {
		result, executeErr := core.execute(context.Background(), command, 1)
		if executeErr != primary || result.ProductError() != primary || len(result.Artifacts()) != 1 || result.Sequence() != uint64(index+1) {
			t.Fatalf("repeat %d = sequence %d error %s artifacts %#v", index, result.Sequence(), errorIdentity(executeErr), result.Artifacts())
		}
	}
	writes := sink.Requests()
	if len(writes) != 2 || writes[0].SuggestedName != "failure-000001.png" || writes[1].SuggestedName != "failure-000002.png" || writes[0].Metadata["sequence"] != "1" || writes[1].Metadata["sequence"] != "2" {
		t.Fatalf("repeated deterministic requests = %#v", writes)
	}
	if !reflect.DeepEqual(command, before) || !bytes.Equal(sharedScreenshot, enginetest.PNGFixture("shared-screenshot")) {
		t.Fatalf("repeated execution changed source/screenshot: command=%#v screenshot=%q", command, sharedScreenshot)
	}

	const workers = 16
	errCh := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range workers {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			workerDriver := newFailureEvidenceDriver(sharedScreenshot, nil, nil)
			workerSink := &failureEvidenceSink{mutateInput: true, responses: []failureEvidenceSinkResponse{{
				result: validFailureEvidenceResult(fmt.Sprintf("concurrent-%02d.png", index), int64(len(sharedScreenshot))),
			}}}
			workerPrimary := NewAssertionError(fmt.Sprintf("worker-%02d", index), nil)
			workerDependencies := coreDependencies(workerDriver, enginetest.NewFakeClock(time.Unix(int64(700+index), 0)))
			workerDependencies.ArtifactSink = workerSink
			workerCore, workerErr := newExecutorCoreForRootRun(
				workerDependencies,
				fmt.Sprintf("root-concurrent-%02d", index),
				failureEvidenceHandler(commandEffect{effectClass: EffectObserved}, workerPrimary),
			)
			if workerErr != nil {
				errCh <- fmt.Errorf("worker %d core: %w", index, workerErr)
				return
			}
			result, executeErr := workerCore.execute(context.Background(), command, 2)
			workerWrites := workerSink.Requests()
			if executeErr != workerPrimary || result.ProductError() != workerPrimary || result.Outcome() != Failed || len(result.Artifacts()) != 1 || len(workerDriver.Requests()) != 1 || len(workerWrites) != 1 {
				errCh <- fmt.Errorf("worker %d result: err %T outcome %s artifacts %d screens %d writes %d", index, executeErr, result.Outcome(), len(result.Artifacts()), len(workerDriver.Requests()), len(workerWrites))
				return
			}
			wantRoot := fmt.Sprintf("root-concurrent-%02d", index)
			if workerWrites[0].Owner != wantRoot || workerWrites[0].Metadata["rootRunId"] != wantRoot || workerWrites[0].SuggestedName != "failure-000001.png" || !bytes.Equal(workerWrites[0].Data, enginetest.PNGFixture("shared-screenshot")) {
				errCh <- fmt.Errorf("worker %d request = %#v", index, workerWrites[0])
			}
		}()
	}
	wait.Wait()
	close(errCh)
	for workerErr := range errCh {
		t.Error(workerErr)
	}
	if !reflect.DeepEqual(command, before) || !bytes.Equal(sharedScreenshot, enginetest.PNGFixture("shared-screenshot")) {
		t.Fatalf("concurrent execution changed source/screenshot: command=%#v screenshot=%q", command, sharedScreenshot)
	}
}

func TestFailureEvidenceCoexistsWithProductionRegistry(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	if len(registry.byKeyword) != len(productionKeywords()) {
		t.Fatalf("production registry size = %d, want the complete set", len(registry.byKeyword))
	}
	for _, keyword := range []model.CommandKeyword{
		model.CommandAssertNotVisible, model.CommandAssertTrue, model.CommandExtendedWaitUntil,
	} {
		if _, exists := registry.lookup(keyword); !exists {
			t.Fatalf("Batch 5 registry missing public keyword %q", keyword)
		}
	}
}

func failureEvidenceHandler(effect commandEffect, productError error) handlerSpec {
	return handlerSpec{
		keyword: model.CommandAssertVisible, effectClass: EffectObserved,
		compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		evaluate: identityEvaluator,
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return effect, productError
		},
	}
}

func failureEvidenceCommand(optional bool) model.Command {
	return model.Command{
		Kind: model.CommandAssertVisible, Form: model.CommandFormObject,
		Arguments: map[string]any{"text": "Missing"},
		Optional:  pointer(optional),
		Source: model.SourceInfo{
			Path: "/workspace/failure.yaml", Start: model.Position{Line: 4, Column: 3, Offset: 42},
		},
	}
}

func validFailureEvidenceResult(path string, bytesWritten int64) ArtifactWriteResult {
	return ArtifactWriteResult{
		Artifact:     device.Artifact{Kind: failureScreenshotArtifactKind, Path: path, Metadata: map[string]string{"provider": "owned"}},
		BytesWritten: bytesWritten,
	}
}

type failureEvidenceTrace struct {
	mu      sync.Mutex
	entries []string
}

func (trace *failureEvidenceTrace) Add(entry string) {
	if trace == nil {
		return
	}
	trace.mu.Lock()
	trace.entries = append(trace.entries, entry)
	trace.mu.Unlock()
}

func (trace *failureEvidenceTrace) Snapshot() []string {
	if trace == nil {
		return nil
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.entries...)
}

type failureEvidenceDriver struct {
	device.Driver

	mu        sync.Mutex
	data      []byte
	err       error
	cancel    context.CancelFunc
	panicCall bool
	requests  []device.ScreenshotRequest
	trace     *failureEvidenceTrace
}

func newFailureEvidenceDriver(data []byte, err error, trace *failureEvidenceTrace) *failureEvidenceDriver {
	return &failureEvidenceDriver{Driver: enginetest.NewFakeDriver(), data: data, err: err, trace: trace}
}

func (driver *failureEvidenceDriver) TakeScreenshot(_ context.Context, request device.ScreenshotRequest) ([]byte, error) {
	driver.mu.Lock()
	driver.requests = append(driver.requests, request)
	driver.mu.Unlock()
	driver.trace.Add("screenshot")
	if driver.cancel != nil {
		driver.cancel()
	}
	if driver.panicCall {
		panic("screenshot provider panic")
	}
	return driver.data, driver.err
}

func (driver *failureEvidenceDriver) Requests() []device.ScreenshotRequest {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]device.ScreenshotRequest(nil), driver.requests...)
}

type failureEvidenceSinkResponse struct {
	result ArtifactWriteResult
	err    error
}

type failureEvidenceSink struct {
	mu          sync.Mutex
	requests    []ArtifactWriteRequest
	responses   []failureEvidenceSinkResponse
	cancel      context.CancelFunc
	panicCall   bool
	mutateInput bool
	trace       *failureEvidenceTrace
}

func (sink *failureEvidenceSink) Write(_ context.Context, request ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.mu.Lock()
	call := len(sink.requests)
	sink.requests = append(sink.requests, cloneFailureEvidenceRequest(request))
	response := failureEvidenceSinkResponse{}
	if call < len(sink.responses) {
		response = sink.responses[call]
	}
	sink.mu.Unlock()
	sink.trace.Add("sink:" + request.Kind)
	if sink.mutateInput {
		if len(request.Data) > 0 {
			request.Data[0] = 'X'
		}
		if request.Metadata != nil {
			request.Metadata["providerMutation"] = "mutated"
		}
	}
	if sink.cancel != nil {
		sink.cancel()
	}
	if sink.panicCall {
		panic("artifact sink panic")
	}
	return response.result, response.err
}

func (sink *failureEvidenceSink) Requests() []ArtifactWriteRequest {
	if sink == nil {
		return nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	requests := make([]ArtifactWriteRequest, len(sink.requests))
	for index, request := range sink.requests {
		requests[index] = cloneFailureEvidenceRequest(request)
	}
	return requests
}

func (sink *failureEvidenceSink) MutateResponseMetadata(value string) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for index := range sink.responses {
		if sink.responses[index].result.Artifact.Metadata != nil {
			sink.responses[index].result.Artifact.Metadata["owner"] = value
		}
	}
}

func cloneFailureEvidenceRequest(request ArtifactWriteRequest) ArtifactWriteRequest {
	request.Data = append([]byte(nil), request.Data...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func assertFailureEvidenceArtifacts(t *testing.T, artifacts []device.Artifact, path string) {
	t.Helper()
	if len(artifacts) != 1 || artifacts[0].Kind != failureScreenshotArtifactKind || artifacts[0].Path != path || artifacts[0].Metadata["provider"] != "owned" {
		t.Fatalf("failure artifacts = %#v, want one owned %s", artifacts, path)
	}
}

func assertFailureEvidenceArtifactOrder(t *testing.T, artifacts []device.Artifact, paths []string) {
	t.Helper()
	if len(artifacts) != len(paths) {
		t.Fatalf("artifact count = %d, want %d: %#v", len(artifacts), len(paths), artifacts)
	}
	for index, path := range paths {
		if artifacts[index].Path != path {
			t.Fatalf("artifact %d path = %q, want %q: %#v", index, artifacts[index].Path, path, artifacts)
		}
	}
}

func errorIdentity(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%v", err, err)
}
