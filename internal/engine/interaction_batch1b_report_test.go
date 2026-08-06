package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/report"
)

func TestInteractionBatch1BActualRootLifecycleResultAndReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, events, config := engine.ExecuteBatch1BRootForReportTest(t)
	if flowResult.Path() != "/workspace/batch1b.yaml" || flowResult.RootRunID() != "batch1b-root/root-run-000001" ||
		flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
		t.Fatalf("FlowResult = path %q rootRunID %q outcome %q error %v", flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
	}
	commands := flowResult.Commands()
	if len(commands) != 1 || commands[0].Command().Kind != model.CommandSwipe || commands[0].Outcome() != engine.Completed ||
		commands[0].Sequence() != 1 || commands[0].Depth() != 0 || commands[0].RootRunID() != "batch1b-root/root-run-000001" {
		t.Fatalf("CommandResult = %#v", commands)
	}
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandSwipe || evaluated.Arguments.(map[string]any)["direction"] != "RIGHT" {
		t.Fatalf("evaluated command = %#v", evaluated)
	}
	wantEventKinds := []engine.EventKind{engine.EventFlowStarted, engine.EventCommandStarted, engine.EventCommandFinished, engine.EventFlowFinished}
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch1b-root/root-run-000001" {
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
	if reportResult.Name != "Batch 1B" || reportResult.Status != report.Completed || len(reportResult.Commands) != 1 ||
		reportResult.Commands[0].Keyword != "swipe" || reportResult.Commands[0].Description != "swipe" ||
		reportResult.Commands[0].Status != report.Completed || reportResult.Commands[0].Metadata["evaluatedCommand"] != string(evaluatedJSON) {
		t.Fatalf("report projection = %#v", reportResult)
	}

	commandSnapshot := commands[0].Command()
	commandSnapshot.Arguments.(map[string]any)["direction"] = "MUTATED"
	freshCommand := flowResult.Commands()[0].Command()
	if freshCommand.Arguments.(map[string]any)["direction"] != "RIGHT" {
		t.Fatalf("CommandResult accessor mutation escaped = %#v", freshCommand)
	}
	evaluated.Arguments.(map[string]any)["direction"] = "MUTATED"
	freshEvaluated, freshExists := flowResult.Commands()[0].Metadata().EvaluatedCommand()
	if !freshExists || freshEvaluated.Arguments.(map[string]any)["direction"] != "RIGHT" {
		t.Fatalf("metadata evaluated snapshot mutation escaped = %#v", freshEvaluated)
	}
	eventCommand, eventExists := events[1].Command()
	if !eventExists {
		t.Fatal("command-start event command is absent")
	}
	eventCommand.Arguments.(map[string]any)["direction"] = "MUTATED"
	freshEventCommand, freshEventExists := events[1].Command()
	if !freshEventExists || freshEventCommand.Arguments.(map[string]any)["direction"] != "RIGHT" {
		t.Fatalf("lifecycle event snapshot mutation escaped = %#v", freshEventCommand)
	}
	reportResult.Metadata["callerMutation"] = "escaped"
	reportResult.Commands[0].Metadata["callerMutation"] = "escaped"
	freshReport, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil || freshReport.Metadata["callerMutation"] != "" || freshReport.Commands[0].Metadata["callerMutation"] != "" ||
		freshReport.Commands[0].Metadata["evaluatedCommand"] != string(evaluatedJSON) {
		t.Fatalf("report snapshot mutation escaped = %#v error %v", freshReport, err)
	}
}
