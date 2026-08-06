package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/report"
)

func TestInteractionBatch2AActualRootLifecycleResultAndReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, events, config, request := engine.ExecuteBatch2ARootForReportTest(t)
	if request.Code != "VOLUME_DOWN" || !reflect.DeepEqual(request.AppIDs, []string{"com.example.batch2a.report"}) {
		t.Fatalf("PressKey request = %#v", request)
	}
	if flowResult.Path() != "/workspace/batch2a.yaml" || flowResult.RootRunID() != "batch2a-root/root-run-000001" ||
		flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
		t.Fatalf("FlowResult = path %q rootRunID %q outcome %q error %v", flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
	}
	commands := flowResult.Commands()
	if len(commands) != 1 || commands[0].Command().Kind != model.CommandPressKey || commands[0].Outcome() != engine.Completed ||
		commands[0].Sequence() != 1 || commands[0].Depth() != 0 || commands[0].RootRunID() != "batch2a-root/root-run-000001" {
		t.Fatalf("CommandResult = %#v", commands)
	}
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandPressKey || evaluated.Arguments != "volume down" {
		t.Fatalf("evaluated command = %#v", evaluated)
	}
	wantEventKinds := []engine.EventKind{engine.EventFlowStarted, engine.EventCommandStarted, engine.EventCommandFinished, engine.EventFlowFinished}
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch2a-root/root-run-000001" {
			t.Fatalf("event %d rootRunID = %q", index, events[index].RootRunID())
		}
	}
	if !reflect.DeepEqual(gotEventKinds, wantEventKinds) || events[1].Sequence() != 1 || events[2].Sequence() != 1 || events[2].Outcome() != engine.Completed {
		t.Fatalf("listener lifecycle = %#v", events)
	}
	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	evaluatedJSON, err := json.Marshal(evaluated)
	if err != nil {
		t.Fatal(err)
	}
	if reportResult.Name != "Batch 2A" || reportResult.Status != report.Completed || len(reportResult.Commands) != 1 ||
		reportResult.Commands[0].Keyword != "pressKey" || reportResult.Commands[0].Description != "pressKey" ||
		reportResult.Commands[0].Status != report.Completed || reportResult.Commands[0].Metadata["evaluatedCommand"] != string(evaluatedJSON) {
		t.Fatalf("report projection = %#v", reportResult)
	}

	evaluated.Arguments = "MUTATED"
	freshEvaluated, freshExists := flowResult.Commands()[0].Metadata().EvaluatedCommand()
	if !freshExists || freshEvaluated.Arguments != "volume down" {
		t.Fatalf("metadata evaluated snapshot mutation escaped = %#v", freshEvaluated)
	}
	eventCommand, eventExists := events[1].Command()
	if !eventExists {
		t.Fatal("command-start event command is absent")
	}
	eventCommand.Arguments = "MUTATED"
	freshEventCommand, freshEventExists := events[1].Command()
	if !freshEventExists || freshEventCommand.Arguments != "volume down" {
		t.Fatalf("lifecycle event snapshot mutation escaped = %#v", freshEventCommand)
	}
}
