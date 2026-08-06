package engine_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/report"
)

func TestInteractionBatch4HRootLifecycleAndReportProjectionAgree(t *testing.T) {
	t.Parallel()

	flowResult, events, config, generatorRequests, inputRequests := engine.ExecuteBatch4HRootForReportTest(t)
	wantGenerator := []engine.InputRequest{
		{Kind: engine.InputText, Length: 0},
		{Kind: engine.InputNumber, Length: 1024},
		{Kind: engine.InputEmail, Length: -1},
		{Kind: engine.InputPersonName, Length: -1},
		{Kind: engine.InputCityName, Length: -1},
		{Kind: engine.InputCountryName, Length: -1},
		{Kind: engine.InputColorName, Length: -1},
	}
	wantInput := []device.InputTextRequest{
		{Text: "", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "007", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "generated@example.test", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "Ada Lovelace", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "Toronto", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "Canada", AppIDs: []string{"com.example.batch4h.report"}},
		{Text: "ultraviolet", AppIDs: []string{"com.example.batch4h.report"}},
	}
	if !reflect.DeepEqual(generatorRequests, wantGenerator) || !reflect.DeepEqual(inputRequests, wantInput) {
		t.Fatalf("requests = generator %#v input %#v", generatorRequests, inputRequests)
	}
	if flowResult.Path() != "/workspace/batch4h-report.yaml" || flowResult.RootRunID() != "batch4h-report/root-run-000001" ||
		flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
		t.Fatalf("FlowResult = path %q root %q outcome %q error %v", flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
	}
	wantKeywords := []model.CommandKeyword{
		model.CommandInputRandomText,
		model.CommandInputRandomNumber,
		model.CommandInputRandomEmail,
		model.CommandInputRandomPersonName,
		model.CommandInputRandomCityName,
		model.CommandInputRandomCountryName,
		model.CommandInputRandomColorName,
	}
	commands := flowResult.Commands()
	if len(commands) != len(wantKeywords) {
		t.Fatalf("CommandResults = %d, want %d", len(commands), len(wantKeywords))
	}
	for index, result := range commands {
		evaluated, exists := result.Metadata().EvaluatedCommand()
		if result.Sequence() != uint64(index+1) || result.Depth() != 0 || result.Command().Kind != wantKeywords[index] ||
			result.Outcome() != engine.Completed || result.ProductError() != nil || !exists || evaluated.Kind != wantKeywords[index] {
			t.Fatalf("command %d = result %#v evaluated %#v present %t", index, result, evaluated, exists)
		}
		if index == 0 && !reflect.DeepEqual(evaluated.Arguments, map[string]any{"length": int64(0)}) {
			t.Fatalf("text evaluated arguments = %#v", evaluated.Arguments)
		}
		if index == 1 && !reflect.DeepEqual(evaluated.Arguments, map[string]any{"length": int64(1024)}) {
			t.Fatalf("number evaluated arguments = %#v", evaluated.Arguments)
		}
	}

	wantEventKinds := make([]engine.EventKind, 0, len(commands)*2+2)
	wantEventKinds = append(wantEventKinds, engine.EventFlowStarted)
	for range commands {
		wantEventKinds = append(wantEventKinds, engine.EventCommandStarted, engine.EventCommandFinished)
	}
	wantEventKinds = append(wantEventKinds, engine.EventFlowFinished)
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch4h-report/root-run-000001" {
			t.Fatalf("event %d rootRunID = %q", index, events[index].RootRunID())
		}
	}
	if !reflect.DeepEqual(gotEventKinds, wantEventKinds) || events[len(events)-1].Outcome() != engine.Completed {
		t.Fatalf("events = %#v", events)
	}
	for index, result := range commands {
		started := events[1+index*2]
		finished := events[2+index*2]
		startedCommand, startedExists := started.Command()
		finishedCommand, finishedExists := finished.Command()
		resultEvaluated, resultEvaluatedExists := result.Metadata().EvaluatedCommand()
		finishedEvaluated, finishedEvaluatedExists := finished.Metadata().EvaluatedCommand()
		if !startedExists || !finishedExists || startedCommand.Kind != result.Command().Kind || finishedCommand.Kind != result.Command().Kind ||
			started.Sequence() != result.Sequence() || finished.Sequence() != result.Sequence() ||
			started.Depth() != result.Depth() || finished.Depth() != result.Depth() || started.Outcome() != "" ||
			started.ProductError() != nil || finished.Outcome() != result.Outcome() || finished.ProductError() != result.ProductError() ||
			!resultEvaluatedExists || !finishedEvaluatedExists || !reflect.DeepEqual(finishedEvaluated, resultEvaluated) {
			t.Fatalf("event/result agreement %d = started %#v finished %#v result %#v", index, started, finished, result)
		}
		if _, exists := started.Metadata().EvaluatedCommand(); exists {
			t.Fatalf("CommandStarted %d contains evaluated metadata", index)
		}
	}

	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	if reportResult.Name != "Batch 4B-H" || reportResult.Status != report.Completed || reportResult.Failure != nil ||
		len(reportResult.Commands) != len(wantKeywords) {
		t.Fatalf("report projection = %#v", reportResult)
	}
	for index, command := range reportResult.Commands {
		evaluated, _ := commands[index].Metadata().EvaluatedCommand()
		evaluatedJSON, marshalErr := json.Marshal(evaluated)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if command.Keyword != string(wantKeywords[index]) || command.Description != string(wantKeywords[index]) ||
			command.Status != report.Completed || command.Failure != nil || command.Metadata["evaluatedCommand"] != string(evaluatedJSON) {
			t.Fatalf("report command %d = %#v", index, command)
		}
	}

	reportResult.Commands[0].Metadata["evaluatedCommand"] = "MUTATED"
	freshReport, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil || freshReport.Commands[0].Metadata["evaluatedCommand"] == "MUTATED" {
		t.Fatalf("report ownership = %#v, %v", freshReport, err)
	}
	evaluated, _ := commands[0].Metadata().EvaluatedCommand()
	evaluated.Arguments.(map[string]any)["length"] = int64(99)
	freshEvaluated, exists := flowResult.Commands()[0].Metadata().EvaluatedCommand()
	if !exists || !reflect.DeepEqual(freshEvaluated.Arguments, map[string]any{"length": int64(0)}) {
		t.Fatalf("evaluated metadata ownership escaped = %#v", freshEvaluated)
	}
}

func TestInteractionBatch4HGeneratorFailureResultAndReportAgree(t *testing.T) {
	t.Parallel()

	flowResult, events, config, runErr := engine.ExecuteBatch4HFailureRootForReportTest(t)
	var operation *engine.OperationError
	commands := flowResult.Commands()
	if !errors.As(runErr, &operation) || flowResult.Outcome() != engine.Failed || flowResult.ProductError() != runErr ||
		len(commands) != 1 || commands[0].Sequence() != 1 || commands[0].Depth() != 0 ||
		commands[0].Command().Kind != model.CommandInputRandomEmail || commands[0].Outcome() != engine.Failed ||
		commands[0].ProductError() != runErr {
		t.Fatalf("failure FlowResult = result %#v commands %#v returned %T %v", flowResult, commands, runErr, runErr)
	}
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandInputRandomEmail || evaluated.Form != model.CommandFormScalar || evaluated.Arguments != nil {
		t.Fatalf("failure evaluated metadata = %#v present %t", evaluated, exists)
	}
	wantEventKinds := []engine.EventKind{
		engine.EventFlowStarted, engine.EventCommandStarted, engine.EventCommandFinished, engine.EventFlowFinished,
	}
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch4h-report-failure/root-run-000001" {
			t.Fatalf("failure event %d rootRunID = %q", index, events[index].RootRunID())
		}
	}
	if len(events) != len(wantEventKinds) {
		t.Fatalf("failure events = %#v, want four lifecycle events", events)
	}
	startedCommand, startedExists := events[1].Command()
	finishedCommand, finishedExists := events[2].Command()
	finishedEvaluated, finishedEvaluatedExists := events[2].Metadata().EvaluatedCommand()
	if !reflect.DeepEqual(gotEventKinds, wantEventKinds) || !startedExists || !finishedExists ||
		startedCommand.Kind != model.CommandInputRandomEmail || finishedCommand.Kind != model.CommandInputRandomEmail ||
		events[1].Sequence() != commands[0].Sequence() || events[2].Sequence() != commands[0].Sequence() ||
		events[1].Depth() != commands[0].Depth() || events[2].Depth() != commands[0].Depth() ||
		events[1].Outcome() != "" || events[1].ProductError() != nil || events[2].Outcome() != commands[0].Outcome() ||
		events[2].ProductError() != commands[0].ProductError() || !finishedEvaluatedExists || !reflect.DeepEqual(finishedEvaluated, evaluated) ||
		events[3].Outcome() != flowResult.Outcome() || events[3].ProductError() != flowResult.ProductError() {
		t.Fatalf("failure event/result agreement = events %#v result %#v commands %#v", events, flowResult, commands)
	}
	if _, exists := events[1].Metadata().EvaluatedCommand(); exists {
		t.Fatal("failure CommandStarted contains evaluated metadata")
	}
	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	if reportResult.Name != "Batch 4B-H failure" || reportResult.Status != report.Failed || reportResult.Failure == nil ||
		len(reportResult.Commands) != 1 || reportResult.Commands[0].Keyword != "inputRandomEmail" ||
		reportResult.Commands[0].Description != "inputRandomEmail" || reportResult.Commands[0].Status != report.Failed ||
		reportResult.Commands[0].Failure == nil {
		t.Fatalf("failure report projection = %#v", reportResult)
	}
}
