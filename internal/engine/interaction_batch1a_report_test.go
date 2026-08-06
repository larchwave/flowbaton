package engine_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/report"
)

func TestInteractionBatch1AActualReportProjection(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
		t.Run(string(keyword), func(t *testing.T) {
			label := "Batch 1A " + string(keyword)
			flowResult, events, config := engine.ExecuteBatch1ARootForReportTest(t, keyword, label)
			if flowResult.Path() != "/workspace/batch1a.yaml" || flowResult.RootRunID() != "batch1a-root/root-run-000001" ||
				flowResult.Outcome() != engine.Completed || flowResult.ProductError() != nil {
				t.Fatalf("FlowResult = path %q rootRunID %q outcome %q error %v",
					flowResult.Path(), flowResult.RootRunID(), flowResult.Outcome(), flowResult.ProductError())
			}

			commands := flowResult.Commands()
			if len(commands) != 1 {
				t.Fatalf("FlowResult command count = %d, want exactly one", len(commands))
			}
			command := commands[0]
			if command.Command().Kind != keyword || command.Outcome() != engine.Completed || command.Sequence() != 1 ||
				command.Depth() != 0 || command.RootRunID() != "batch1a-root/root-run-000001" || command.ProductError() != nil {
				t.Fatalf("CommandResult = kind %q outcome %q sequence %d depth %d rootRunID %q error %v",
					command.Command().Kind, command.Outcome(), command.Sequence(), command.Depth(), command.RootRunID(), command.ProductError())
			}
			evaluated, exists := command.Metadata().EvaluatedCommand()
			if !exists || evaluated.Kind != keyword || evaluated.Label == nil || *evaluated.Label != label {
				t.Fatalf("evaluated command = %#v, want %s labeled %q", evaluated, keyword, label)
			}
			evaluatedJSON, err := json.Marshal(evaluated)
			if err != nil {
				t.Fatalf("json.Marshal(evaluated) error = %v", err)
			}

			wantEventKinds := []engine.EventKind{
				engine.EventFlowStarted,
				engine.EventCommandStarted,
				engine.EventCommandFinished,
				engine.EventFlowFinished,
			}
			gotEventKinds := make([]engine.EventKind, len(events))
			for index := range events {
				gotEventKinds[index] = events[index].Kind()
				if events[index].RootRunID() != "batch1a-root/root-run-000001" {
					t.Fatalf("event %d rootRunID = %q", index, events[index].RootRunID())
				}
			}
			if !reflect.DeepEqual(gotEventKinds, wantEventKinds) {
				t.Fatalf("event lifecycle = %#v, want %#v", gotEventKinds, wantEventKinds)
			}
			startedCommand, startedOK := events[1].Command()
			finishedCommand, finishedOK := events[2].Command()
			if !startedOK || !finishedOK || startedCommand.Kind != keyword || finishedCommand.Kind != keyword ||
				startedCommand.Label == nil || *startedCommand.Label != label || finishedCommand.Label == nil || *finishedCommand.Label != label ||
				events[1].Sequence() != 1 || events[2].Sequence() != 1 || events[2].Outcome() != engine.Completed ||
				events[3].FlowPath() != "/workspace/batch1a.yaml" || events[3].Outcome() != engine.Completed {
				t.Fatalf("listener events disagree with Batch 1A result: %#v", events)
			}

			reportResult, err := report.FromEngineFlowResult(flowResult, config)
			if err != nil {
				t.Fatalf("report.FromEngineFlowResult() error = %v", err)
			}
			if reportResult.Name != "Batch 1A" || reportResult.Description != "/workspace/batch1a.yaml" ||
				reportResult.Status != report.Completed || reportResult.Metadata["rootRunId"] != "batch1a-root/root-run-000001" ||
				len(reportResult.Commands) != 1 {
				t.Fatalf("report flow projection = %#v", reportResult)
			}
			reportCommand := reportResult.Commands[0]
			if reportCommand.Keyword != string(keyword) || reportCommand.Description != label || reportCommand.Status != report.Completed ||
				reportCommand.Sequence != 1 || reportCommand.Depth != 0 ||
				reportCommand.Metadata["rootRunId"] != "batch1a-root/root-run-000001" ||
				reportCommand.Metadata["evaluatedCommand"] != string(evaluatedJSON) {
				t.Fatalf("actual report command projection = %#v, evaluated=%s", reportCommand, evaluatedJSON)
			}
		})
	}
}
