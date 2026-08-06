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

func TestInteractionBatch3ActualRootLifecycleResultAndReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, events, config, requests := engine.ExecuteBatch3RootForReportTest(t)
	if len(requests) != 1 || requests[0].Direction != "UP" || requests[0].Amount != 0.4 ||
		requests[0].ElementPoint == nil || *requests[0].ElementPoint != (device.Point{X: 50, Y: 90}) {
		t.Fatalf("centering requests = %#v", requests)
	}
	if flowResult.Path() != "/workspace/batch3-report.yaml" || flowResult.RootRunID() != "batch3-report/root-run-000001" ||
		flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
		t.Fatalf("FlowResult = path %q rootRunID %q outcome %q error %v", flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
	}
	commands := flowResult.Commands()
	if len(commands) != 1 || commands[0].Command().Kind != model.CommandScrollUntilVisible || commands[0].Outcome() != engine.Completed ||
		commands[0].Sequence() != 1 || commands[0].Depth() != 0 || commands[0].RootRunID() != "batch3-report/root-run-000001" {
		t.Fatalf("CommandResult = %#v", commands)
	}
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Kind != model.CommandScrollUntilVisible || evaluated.Selector == nil || evaluated.Selector.TextRegex == nil || *evaluated.Selector.TextRegex != "Ready" {
		t.Fatalf("evaluated command = %#v", evaluated)
	}
	wantEventKinds := []engine.EventKind{engine.EventFlowStarted, engine.EventCommandStarted, engine.EventCommandFinished, engine.EventFlowFinished}
	gotEventKinds := make([]engine.EventKind, len(events))
	for index := range events {
		gotEventKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "batch3-report/root-run-000001" {
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
	if reportResult.Name != "Batch 3" || reportResult.Status != report.Completed || len(reportResult.Commands) != 1 ||
		reportResult.Commands[0].Keyword != "scrollUntilVisible" || reportResult.Commands[0].Description != "scrollUntilVisible" ||
		reportResult.Commands[0].Status != report.Completed || reportResult.Commands[0].Metadata["evaluatedCommand"] != string(evaluatedJSON) {
		t.Fatalf("report projection = %#v", reportResult)
	}

	evaluated.Arguments.(map[string]any)["centerElement"] = false
	*evaluated.Selector.TextRegex = "MUTATED"
	freshEvaluated, freshExists := flowResult.Commands()[0].Metadata().EvaluatedCommand()
	if !freshExists || freshEvaluated.Arguments.(map[string]any)["centerElement"] != true || *freshEvaluated.Selector.TextRegex != "Ready" {
		t.Fatalf("metadata evaluated snapshot mutation escaped = %#v", freshEvaluated)
	}
}

func TestInteractionBatch3RequiredExhaustionReportProjection(t *testing.T) {
	t.Parallel()

	flowResult, config, runErr := engine.ExecuteBatch3FailureRootForReportTest(t)
	var assertion *engine.AssertionError
	if !errors.As(runErr, &assertion) || flowResult.Outcome() != engine.Failed || flowResult.ProductError() != runErr {
		t.Fatalf("failure FlowResult = outcome %q product %T %v returned %T %v", flowResult.Outcome(), flowResult.ProductError(), flowResult.ProductError(), runErr, runErr)
	}
	reportResult, err := report.FromEngineFlowResult(flowResult, config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error = %v", err)
	}
	if reportResult.Status != report.Failed || reportResult.Failure == nil || len(reportResult.Commands) != 1 ||
		reportResult.Commands[0].Keyword != "scrollUntilVisible" || reportResult.Commands[0].Status != report.Failed ||
		reportResult.Commands[0].Failure == nil {
		t.Fatalf("failure report projection = %#v", reportResult)
	}
}
