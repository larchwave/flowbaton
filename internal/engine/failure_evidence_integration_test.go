package engine_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/flow"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/report"
)

func TestFailureEvidencePublicExecuteAndReportAgree(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name           string
		optional       bool
		wantOutcome    engine.Outcome
		wantReport     report.Status
		wantSurfaceErr bool
	}{
		{name: "required", wantOutcome: engine.Failed, wantReport: report.Failed, wantSurfaceErr: true},
		{name: "optional", optional: true, wantOutcome: engine.Warned, wantReport: report.Warned},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join("/workspace", "batch4-"+test.name+".yaml")
			program, source := prepareFailureEvidenceProgram(t, path, test.optional)
			before, exists := program.Flow(path)
			if !exists {
				t.Fatalf("prepared flow %q missing", path)
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				TakeScreenshot: []enginetest.Result[[]byte]{{Value: enginetest.PNGFixture("public-png")}},
			})
			sink := &failureEvidenceIntegrationSink{result: engine.ArtifactWriteResult{
				Artifact: device.Artifact{
					Kind: "failure-screenshot", Path: "sink/" + test.name + ".png",
					Metadata: map[string]string{"provider": "public"},
				},
				BytesWritten: int64(len(enginetest.PNGFixture("public-png"))),
			}}
			factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
			if err != nil {
				t.Fatalf("js.NewFactory() error = %v", err)
			}
			events := make([]engine.Event, 0, 4)
			executionID := "batch4-public-" + test.name
			results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
				ExecutionID: executionID,
				Driver:      driver, Clock: &integrationClock{now: time.Unix(1_700_001_000, 0)}, JSFactory: factory, Controller: engine.NoopController{},
				ArtifactSink: sink,
				Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
					events = append(events, event)
					return nil
				})},
			})
			if len(results) != 1 {
				t.Fatalf("engine.Execute() results = %d error %v", len(results), executeErr)
			}
			root := results[0]
			commands := root.Commands()
			if len(commands) != 1 {
				t.Fatalf("flow commands = %#v, want one", commands)
			}
			command := commands[0]
			rootRunID := executionID + "/root-run-000001"
			if root.RootRunID() != rootRunID || command.RootRunID() != rootRunID || root.Outcome() != test.wantOutcome || command.Outcome() != test.wantOutcome {
				t.Fatalf("engine identity/outcome = root %q/%s command %q/%s", root.RootRunID(), root.Outcome(), command.RootRunID(), command.Outcome())
			}
			if command.ProductError() == nil {
				t.Fatal("failed/warned command product error = nil")
			}
			commandFinished, flowFinished := failureEvidenceFinishedEvents(t, events)
			if commandFinished.ProductError() != command.ProductError() || commandFinished.Outcome() != command.Outcome() {
				t.Fatalf("command event primary/outcome = %T %v/%s", commandFinished.ProductError(), commandFinished.ProductError(), commandFinished.Outcome())
			}
			if test.wantSurfaceErr {
				if executeErr != command.ProductError() || root.ProductError() != command.ProductError() || flowFinished.ProductError() != command.ProductError() {
					t.Fatalf("required primary identity = returned %T %v command %T %v flow %T %v event %T %v", executeErr, executeErr, command.ProductError(), command.ProductError(), root.ProductError(), root.ProductError(), flowFinished.ProductError(), flowFinished.ProductError())
				}
			} else if executeErr != nil || root.ProductError() != nil || flowFinished.ProductError() != nil {
				t.Fatalf("optional warning surfaced flow error = returned %T %v flow %T %v event %T %v", executeErr, executeErr, root.ProductError(), root.ProductError(), flowFinished.ProductError(), flowFinished.ProductError())
			}

			assertPublicFailureArtifacts(t, command.Artifacts(), "sink/"+test.name+".png")
			assertPublicFailureArtifacts(t, commandFinished.Artifacts(), "sink/"+test.name+".png")
			requests := sink.Requests()
			if len(requests) != 1 {
				t.Fatalf("sink requests = %#v, want one", requests)
			}
			wantRequestMetadata := map[string]string{
				"keyword": "assertVisible", "rootRunId": rootRunID, "sequence": "1", "depth": "0",
				"outcome": string(test.wantOutcome), "sourcePath": path,
			}
			if requests[0].Owner != rootRunID || requests[0].Kind != "failure-screenshot" || requests[0].SuggestedName != "failure-000001.png" || !bytes.Equal(requests[0].Data, enginetest.PNGFixture("public-png")) || !reflect.DeepEqual(requests[0].Metadata, wantRequestMetadata) {
				t.Fatalf("public failure write = %#v, want metadata %#v", requests[0], wantRequestMetadata)
			}
			screenshots := failureEvidenceScreenshotRequests(driver.Actions())
			if len(screenshots) != 1 || screenshots[0] != (device.ScreenshotRequest{Compressed: true}) {
				t.Fatalf("public screenshot requests = %#v", screenshots)
			}

			reportResult, reportErr := report.FromEngineFlowResult(root, source.Config)
			if reportErr != nil {
				t.Fatalf("report.FromEngineFlowResult() error = %v", reportErr)
			}
			if reportResult.Status != test.wantReport || len(reportResult.Commands) != 1 || reportResult.Commands[0].Status != test.wantReport {
				t.Fatalf("report outcomes = flow %s command %#v", reportResult.Status, reportResult.Commands)
			}
			wantReportArtifact := report.Artifact{Kind: "failure-screenshot", Path: "sink/" + test.name + ".png"}
			if !reflect.DeepEqual(reportResult.Artifacts, []report.Artifact{wantReportArtifact}) || !reflect.DeepEqual(reportResult.Commands[0].Artifacts, []report.Artifact{wantReportArtifact}) {
				t.Fatalf("report artifacts = flow %#v command %#v", reportResult.Artifacts, reportResult.Commands[0].Artifacts)
			}
			if reportResult.Commands[0].Failure == nil || reportResult.Commands[0].Failure.Message != fmt.Sprint(command.ProductError()) {
				t.Fatalf("report command failure = %#v, want %q", reportResult.Commands[0].Failure, command.ProductError())
			}
			if test.wantSurfaceErr && (reportResult.Failure == nil || reportResult.Failure.Message != fmt.Sprint(command.ProductError())) {
				t.Fatalf("required report flow failure = %#v", reportResult.Failure)
			}
			if !test.wantSurfaceErr && reportResult.Failure != nil {
				t.Fatalf("optional report flow failure = %#v, want nil", reportResult.Failure)
			}
			for _, forbidden := range []string{"keyword", "sequence", "depth", "outcome", "sourcePath"} {
				if _, exists := reportResult.Commands[0].Metadata[forbidden]; exists {
					t.Fatalf("diagnostic metadata leaked into command report: %s=%q", forbidden, reportResult.Commands[0].Metadata[forbidden])
				}
			}
			after, exists := program.Flow(path)
			if !exists || !reflect.DeepEqual(after, before) || !reflect.DeepEqual(source, before) {
				t.Fatalf("public execution changed prepared/source flow: source=%#v before=%#v after=%#v", source, before, after)
			}
		})
	}
}

func TestFailureEvidencePublicDiagnosticFailureHasNoProductSurface(t *testing.T) {
	t.Parallel()

	path := "/workspace/batch4-diagnostic-failure.yaml"
	program, source := prepareFailureEvidenceProgram(t, path, false)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: enginetest.PNGFixture("diagnostic-png")}},
	})
	sinkFailure := errors.New("diagnostic sink unavailable")
	sink := &failureEvidenceIntegrationSink{
		result: engine.ArtifactWriteResult{
			Artifact:     device.Artifact{Kind: "failure-screenshot", Path: "sink/partial.png", Metadata: map[string]string{"partial": "true"}},
			BytesWritten: int64(len(enginetest.PNGFixture("diagnostic-png"))),
		},
		err: sinkFailure,
	}
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	events := make([]engine.Event, 0, 4)
	results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "batch4-public-diagnostic-failure",
		Driver:      driver, Clock: &integrationClock{now: time.Unix(1_700_002_000, 0)}, JSFactory: factory, Controller: engine.NoopController{},
		ArtifactSink: sink,
		Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
			events = append(events, event)
			return nil
		})},
	})
	if len(results) != 1 || executeErr == nil {
		t.Fatalf("engine.Execute() = results %d error %T %v", len(results), executeErr, executeErr)
	}
	root := results[0]
	commands := root.Commands()
	if len(commands) != 1 {
		t.Fatalf("commands = %#v, want one", commands)
	}
	command := commands[0]
	commandFinished, flowFinished := failureEvidenceFinishedEvents(t, events)
	if command.ProductError() != executeErr || root.ProductError() != executeErr || commandFinished.ProductError() != executeErr || flowFinished.ProductError() != executeErr {
		t.Fatalf("diagnostic sink replaced primary = returned %T %v command %T %v flow %T %v", executeErr, executeErr, command.ProductError(), command.ProductError(), root.ProductError(), root.ProductError())
	}
	if len(command.Artifacts()) != 0 || len(commandFinished.Artifacts()) != 0 || len(sink.Requests()) != 1 || len(failureEvidenceScreenshotRequests(driver.Actions())) != 1 {
		t.Fatalf("diagnostic failure surface = command %#v event %#v sink %d screens %d", command.Artifacts(), commandFinished.Artifacts(), len(sink.Requests()), len(failureEvidenceScreenshotRequests(driver.Actions())))
	}
	if len(command.Metadata().LogMessages()) != 0 {
		t.Fatalf("diagnostic failure added command logs = %#v", command.Metadata().LogMessages())
	}
	reportResult, reportErr := report.FromEngineFlowResult(root, source.Config)
	if reportErr != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", reportErr)
	}
	if len(reportResult.Artifacts) != 0 || len(reportResult.Commands) != 1 || len(reportResult.Commands[0].Artifacts) != 0 || reportResult.Commands[0].Failure == nil || reportResult.Failure == nil {
		t.Fatalf("diagnostic failure leaked to report = %#v", reportResult)
	}
	if reportResult.Commands[0].Failure.Message != fmt.Sprint(executeErr) || reportResult.Failure.Message != fmt.Sprint(executeErr) {
		t.Fatalf("report primary changed = flow %#v command %#v want %q", reportResult.Failure, reportResult.Commands[0].Failure, executeErr)
	}
}

func TestFailureEvidencePreparedProgramRepeatedConcurrentOwnership(t *testing.T) {
	t.Parallel()

	path := "/workspace/batch4-concurrent.yaml"
	program, source := prepareFailureEvidenceProgram(t, path, true)
	before, exists := program.Flow(path)
	if !exists {
		t.Fatalf("prepared flow %q missing", path)
	}

	const workers = 12
	var wait sync.WaitGroup
	errCh := make(chan error, workers)
	for index := range workers {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{TakeScreenshot: []enginetest.Result[[]byte]{{Value: enginetest.PNGFixture("concurrent-png")}}})
			sink := &failureEvidenceIntegrationSink{
				mutateInput: true,
				result: engine.ArtifactWriteResult{
					Artifact:     device.Artifact{Kind: "failure-screenshot", Path: fmt.Sprintf("sink/concurrent-%02d.png", index)},
					BytesWritten: int64(len(enginetest.PNGFixture("concurrent-png"))),
				},
			}
			factory, factoryErr := js.NewFactory(js.Config{Random: integrationRandom{}})
			if factoryErr != nil {
				errCh <- fmt.Errorf("worker %d factory: %w", index, factoryErr)
				return
			}
			executionID := fmt.Sprintf("batch4-concurrent-%02d", index)
			results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
				ExecutionID: executionID,
				Driver:      driver, Clock: &integrationClock{now: time.Unix(int64(1_700_003_000+index), 0)}, JSFactory: factory, Controller: engine.NoopController{},
				ArtifactSink: sink,
			})
			if executeErr != nil || len(results) != 1 {
				errCh <- fmt.Errorf("worker %d Execute: results %d error %w", index, len(results), executeErr)
				return
			}
			root := results[0]
			commands := root.Commands()
			requests := sink.Requests()
			if root.Outcome() != engine.Warned || len(commands) != 1 || commands[0].Outcome() != engine.Warned || len(commands[0].Artifacts()) != 1 || len(requests) != 1 || len(failureEvidenceScreenshotRequests(driver.Actions())) != 1 {
				errCh <- fmt.Errorf("worker %d surfaces: flow %s commands %d artifacts %d sink %d screens %d", index, root.Outcome(), len(commands), len(commands[0].Artifacts()), len(requests), len(failureEvidenceScreenshotRequests(driver.Actions())))
				return
			}
			rootRunID := executionID + "/root-run-000001"
			if requests[0].Owner != rootRunID || requests[0].Metadata["rootRunId"] != rootRunID || requests[0].SuggestedName != "failure-000001.png" || !bytes.Equal(requests[0].Data, enginetest.PNGFixture("concurrent-png")) {
				errCh <- fmt.Errorf("worker %d request = %#v", index, requests[0])
				return
			}
			reportResult, reportErr := report.FromEngineFlowResult(root, source.Config)
			if reportErr != nil || reportResult.Status != report.Warned || len(reportResult.Artifacts) != 1 {
				errCh <- fmt.Errorf("worker %d report = status %s artifacts %d error %v", index, reportResult.Status, len(reportResult.Artifacts), reportErr)
			}
		}()
	}
	wait.Wait()
	close(errCh)
	for workerErr := range errCh {
		t.Error(workerErr)
	}
	after, exists := program.Flow(path)
	if !exists || !reflect.DeepEqual(before, after) || !reflect.DeepEqual(source, before) {
		t.Fatalf("concurrent Execute changed prepared/source flow: source=%#v before=%#v after=%#v", source, before, after)
	}
}

func prepareFailureEvidenceProgram(t *testing.T, path string, optional bool) (*engine.Program, model.Flow) {
	t.Helper()
	body := fmt.Sprintf("appId: com.example.batch4\nname: Batch 4\n---\n- assertVisible:\n    text: Missing\n    optional: %t\n", optional)
	parsed, err := flow.ParseBytes(path, []byte(body))
	if err != nil {
		t.Fatalf("flow.ParseBytes() error = %v", err)
	}
	loader := failureEvidenceFlowLoader{path: filepath.Clean(path), flow: parsed}
	program, err := engine.Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{path}}, loader)
	if err != nil {
		t.Fatalf("engine.Prepare() error = %v", err)
	}
	return program, parsed
}

type failureEvidenceFlowLoader struct {
	path string
	flow model.Flow
}

var _ capability.FlowLoader = failureEvidenceFlowLoader{}

func (loader failureEvidenceFlowLoader) Canonical(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if filepath.Clean(path) != loader.path {
		return "", fmt.Errorf("unexpected path %q", path)
	}
	return loader.path, nil
}

func (loader failureEvidenceFlowLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	if err := ctx.Err(); err != nil {
		return model.Flow{}, err
	}
	if filepath.Clean(path) != loader.path {
		return model.Flow{}, fmt.Errorf("unexpected canonical path %q", path)
	}
	return loader.flow, nil
}

type failureEvidenceIntegrationSink struct {
	mu          sync.Mutex
	requests    []engine.ArtifactWriteRequest
	result      engine.ArtifactWriteResult
	err         error
	mutateInput bool
}

func (sink *failureEvidenceIntegrationSink) Write(_ context.Context, request engine.ArtifactWriteRequest) (engine.ArtifactWriteResult, error) {
	sink.mu.Lock()
	sink.requests = append(sink.requests, cloneFailureEvidenceIntegrationRequest(request))
	sink.mu.Unlock()
	if sink.mutateInput {
		if len(request.Data) > 0 {
			request.Data[0] = 'X'
		}
		request.Metadata["providerMutation"] = "true"
	}
	return sink.result, sink.err
}

func (sink *failureEvidenceIntegrationSink) Requests() []engine.ArtifactWriteRequest {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	requests := make([]engine.ArtifactWriteRequest, len(sink.requests))
	for index, request := range sink.requests {
		requests[index] = cloneFailureEvidenceIntegrationRequest(request)
	}
	return requests
}

func cloneFailureEvidenceIntegrationRequest(request engine.ArtifactWriteRequest) engine.ArtifactWriteRequest {
	request.Data = append([]byte(nil), request.Data...)
	if request.Metadata != nil {
		metadata := make(map[string]string, len(request.Metadata))
		for key, value := range request.Metadata {
			metadata[key] = value
		}
		request.Metadata = metadata
	}
	return request
}

func failureEvidenceScreenshotRequests(actions []enginetest.Action) []device.ScreenshotRequest {
	requests := make([]device.ScreenshotRequest, 0, 1)
	for _, action := range actions {
		if action.Method != enginetest.MethodTakeScreenshot {
			continue
		}
		request, ok := action.Request.(device.ScreenshotRequest)
		if ok {
			requests = append(requests, request)
		}
	}
	return requests
}

func failureEvidenceFinishedEvents(t *testing.T, events []engine.Event) (engine.Event, engine.Event) {
	t.Helper()
	var commandFinished engine.Event
	var flowFinished engine.Event
	for _, event := range events {
		switch event.Kind() {
		case engine.EventCommandFinished:
			commandFinished = event
		case engine.EventFlowFinished:
			flowFinished = event
		}
	}
	if commandFinished.Kind() != engine.EventCommandFinished || flowFinished.Kind() != engine.EventFlowFinished {
		t.Fatalf("finished events missing from %#v", events)
	}
	return commandFinished, flowFinished
}

func assertPublicFailureArtifacts(t *testing.T, artifacts []device.Artifact, path string) {
	t.Helper()
	if len(artifacts) != 1 || artifacts[0].Kind != "failure-screenshot" || artifacts[0].Path != path || artifacts[0].Metadata["provider"] != "public" {
		t.Fatalf("public artifacts = %#v, want one owned %q", artifacts, path)
	}
}
