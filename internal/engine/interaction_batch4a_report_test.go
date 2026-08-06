package engine_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/report"
)

func TestInteractionBatch4AActualRootLifecycleResultAndReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, events, config, inputRequests, eraseRequests := engine.ExecuteBatch4ARootForReportTest(t)
	if !reflect.DeepEqual(inputRequests, []device.InputTextRequest{{
		Text: "hello 世界", AppIDs: []string{"com.example.batch4a.report"},
	}}) || !reflect.DeepEqual(eraseRequests, []device.EraseTextRequest{{
		CharactersToErase: 7, AppIDs: []string{"com.example.batch4a.report"},
	}}) {
		t.Fatalf("requests = input %#v erase %#v", inputRequests, eraseRequests)
	}
	if flowResult.Path() != "/workspace/batch4a-report.yaml" || flowResult.RootRunID() != "batch4a-report/root-run-000001" ||
		flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
		t.Fatalf("FlowResult = path %q root %q outcome %q error %v", flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
	}
	commands := flowResult.Commands()
	if len(commands) != 2 || commands[0].Sequence() != 1 || commands[1].Sequence() != 2 ||
		commands[0].Command().Kind != model.CommandInputText || commands[1].Command().Kind != model.CommandEraseText ||
		commands[0].Outcome() != engine.Completed || commands[1].Outcome() != engine.Completed {
		t.Fatalf("CommandResults = %#v", commands)
	}
	inputEvaluated, inputExists := commands[0].Metadata().EvaluatedCommand()
	eraseEvaluated, eraseExists := commands[1].Metadata().EvaluatedCommand()
	wantInputArguments := map[string]any{"text": "hello 世界", "label": "Typing profile"}
	if !inputExists || !reflect.DeepEqual(inputEvaluated.Arguments, wantInputArguments) || inputEvaluated.Label == nil || *inputEvaluated.Label != "Typing profile" ||
		!eraseExists || eraseEvaluated.Arguments != int64(7) {
		t.Fatalf("evaluated commands = input %#v erase %#v", inputEvaluated, eraseEvaluated)
	}
	wantEventKinds := []engine.EventKind{
		engine.EventFlowStarted,
		engine.EventCommandStarted, engine.EventCommandFinished,
		engine.EventCommandStarted, engine.EventCommandFinished,
		engine.EventFlowFinished,
	}
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch4a-report/root-run-000001" {
			t.Fatalf("event %d rootRunID = %q", index, events[index].RootRunID())
		}
	}
	if !reflect.DeepEqual(gotEventKinds, wantEventKinds) || events[2].Outcome() != engine.Completed || events[4].Outcome() != engine.Completed {
		t.Fatalf("events = %#v", events)
	}

	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	inputJSON, err := json.Marshal(inputEvaluated)
	if err != nil {
		t.Fatal(err)
	}
	eraseJSON, err := json.Marshal(eraseEvaluated)
	if err != nil {
		t.Fatal(err)
	}
	if reportResult.Name != "Batch 4A" || reportResult.Status != report.Completed || len(reportResult.Commands) != 2 ||
		reportResult.Commands[0].Keyword != "inputText" || reportResult.Commands[0].Description != "Typing profile" ||
		reportResult.Commands[0].Metadata["evaluatedCommand"] != string(inputJSON) ||
		reportResult.Commands[1].Keyword != "eraseText" || reportResult.Commands[1].Description != "eraseText" ||
		reportResult.Commands[1].Metadata["evaluatedCommand"] != string(eraseJSON) {
		t.Fatalf("report projection = %#v", reportResult)
	}
	reportResult.Commands[0].Metadata["evaluatedCommand"] = "MUTATED"
	freshReport, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("fresh report projection error = %v", err)
	}
	if freshReport.Commands[0].Metadata["evaluatedCommand"] != string(inputJSON) {
		t.Fatalf("report mutation escaped = %#v", freshReport.Commands[0])
	}

	inputEvaluated.Arguments.(map[string]any)["text"] = "MUTATED"
	*inputEvaluated.Label = "MUTATED"
	freshInput, freshExists := flowResult.Commands()[0].Metadata().EvaluatedCommand()
	if !freshExists || !reflect.DeepEqual(freshInput.Arguments, wantInputArguments) || freshInput.Label == nil || *freshInput.Label != "Typing profile" {
		t.Fatalf("evaluated metadata mutation escaped = %#v", freshInput)
	}
	eventCommand, eventExists := events[1].Command()
	if !eventExists {
		t.Fatal("command-start event command is absent")
	}
	wantAuthoredArguments := map[string]any{"text": "${TEXT}", "label": "${LABEL}"}
	if !reflect.DeepEqual(eventCommand.Arguments, wantAuthoredArguments) || eventCommand.Label == nil || *eventCommand.Label != "${LABEL}" {
		t.Fatalf("command-start event = %#v, want authored snapshot", eventCommand)
	}
	eventEvaluated, eventEvaluatedExists := events[2].Metadata().EvaluatedCommand()
	if !eventEvaluatedExists || !reflect.DeepEqual(eventEvaluated.Arguments, wantInputArguments) ||
		eventEvaluated.Label == nil || *eventEvaluated.Label != "Typing profile" {
		t.Fatalf("command-finished evaluated metadata = %#v, present %t", eventEvaluated, eventEvaluatedExists)
	}
	eventCommand.Arguments.(map[string]any)["text"] = "MUTATED"
	*eventCommand.Label = "MUTATED"
	freshEventCommand, freshEventExists := events[1].Command()
	if !freshEventExists || !reflect.DeepEqual(freshEventCommand.Arguments, wantAuthoredArguments) ||
		freshEventCommand.Label == nil || *freshEventCommand.Label != "${LABEL}" {
		t.Fatalf("event snapshot mutation escaped = %#v", freshEventCommand)
	}
}

func TestInteractionBatch4ADriverFailureReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, config, runErr := engine.ExecuteBatch4AFailureRootForReportTest(t)
	var operation *engine.OperationError
	if !errors.As(runErr, &operation) || flowResult.Outcome() != engine.Failed || flowResult.ProductError() != runErr ||
		len(flowResult.Commands()) != 1 || flowResult.Commands()[0].ProductError() != runErr {
		t.Fatalf("failure FlowResult = result %#v returned %T %v", flowResult, runErr, runErr)
	}
	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	if reportResult.Name != "Batch 4A failure" || reportResult.Status != report.Failed || reportResult.Failure == nil ||
		len(reportResult.Commands) != 1 || reportResult.Commands[0].Keyword != "inputText" ||
		reportResult.Commands[0].Description != "Typing failure" || reportResult.Commands[0].Status != report.Failed ||
		reportResult.Commands[0].Failure == nil {
		t.Fatalf("failure report projection = %#v", reportResult)
	}
}
