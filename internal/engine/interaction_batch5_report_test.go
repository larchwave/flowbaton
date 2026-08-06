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

func TestInteractionBatch5ResultEventAndReportProjectionMatrix(t *testing.T) {
	t.Parallel()

	scenarios := engine.ExecuteBatch5ReportScenariosForTest(t)
	if got, want := len(scenarios), 4; got != want {
		t.Fatalf("scenarios = %d, want %d", got, want)
	}
	wantStatus := map[string]report.Status{
		"success":  report.Completed,
		"optional": report.Warned,
		"driver":   report.Failed,
		"poison":   report.Failed,
	}
	for _, scenario := range scenarios {
		t.Run(scenario.Name, func(t *testing.T) {
			commands := scenario.Flow.Commands()
			assertBatch5EventAgreement(t, scenario, commands)
			converted, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
			if err != nil {
				t.Fatalf("report.FromEngineFlowResult() error = %v", err)
			}
			if converted.Name != scenario.Config.Name || converted.Description != scenario.Flow.Path() ||
				converted.Status != wantStatus[scenario.Name] || len(converted.Commands) != len(commands) ||
				converted.Metadata["rootRunId"] != scenario.Flow.RootRunID() {
				t.Fatalf("report projection = %#v", converted)
			}
			for index, command := range commands {
				evaluated, exists := command.Metadata().EvaluatedCommand()
				if !exists {
					t.Fatalf("command %d evaluated metadata absent", index)
				}
				encoded, encodeErr := json.Marshal(evaluated)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				if converted.Commands[index].Sequence != int64(command.Sequence()) ||
					converted.Commands[index].Depth != command.Depth() ||
					converted.Commands[index].Keyword != string(evaluated.Kind) ||
					converted.Commands[index].Metadata["evaluatedCommand"] != string(encoded) ||
					converted.Commands[index].Metadata["rootRunId"] != command.RootRunID() {
					t.Fatalf("report command %d = %#v evaluated %#v", index, converted.Commands[index], evaluated)
				}
			}
			if scenario.Name == "success" {
				assertBatch5SuccessProjection(t, scenario, converted)
			} else if scenario.Name == "optional" {
				if converted.Failure != nil || commands[len(commands)-1].ProductError() == nil ||
					converted.Commands[len(converted.Commands)-1].Failure == nil {
					t.Fatalf("warning projection = %#v commands %#v", converted, commands)
				}
			} else if converted.Failure == nil || commands[len(commands)-1].ProductError() == nil {
				t.Fatalf("failure projection = %#v commands %#v", converted, commands)
			}
		})
	}

	poison := scenarios[3]
	var configuration *engine.ConfigurationError
	if poison.Name != "poison" || poison.Err == nil || !errors.As(poison.Err, &configuration) || configuration == nil ||
		poison.Flow.ProductError() != poison.Err || len(poison.Requests) != 0 {
		t.Fatalf("poison identity = scenario %#v configuration %#v", poison, configuration)
	}
}

func TestInteractionBatch5ReportAndEventSnapshotsAreDefensive(t *testing.T) {
	t.Parallel()

	success := engine.ExecuteBatch5ReportScenariosForTest(t)[0]
	converted, err := report.FromEngineFlowResult(success.Flow, success.Config)
	if err != nil {
		t.Fatal(err)
	}
	originalEvaluated := converted.Commands[1].Metadata["evaluatedCommand"]
	converted.Metadata["rootRunId"] = "mutated"
	converted.Commands[1].Metadata["evaluatedCommand"] = "mutated"
	converted.Commands[1].Description = "mutated"
	fresh, err := report.FromEngineFlowResult(success.Flow, success.Config)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Metadata["rootRunId"] != success.Flow.RootRunID() ||
		fresh.Commands[1].Metadata["evaluatedCommand"] != originalEvaluated || fresh.Commands[1].Description != "copy target" {
		t.Fatalf("report mutation escaped = %#v", fresh)
	}

	commands := success.Flow.Commands()
	evaluated, exists := commands[1].Metadata().EvaluatedCommand()
	if !exists || evaluated.Selector == nil || evaluated.Selector.IDRegex == nil {
		t.Fatalf("copy evaluated metadata = %#v", evaluated)
	}
	*evaluated.Selector.IDRegex = "mutated"
	evaluated.Arguments.(map[string]any)["id"] = "mutated"
	freshEvaluated, exists := success.Flow.Commands()[1].Metadata().EvaluatedCommand()
	if !exists || freshEvaluated.Selector == nil || freshEvaluated.Selector.IDRegex == nil ||
		*freshEvaluated.Selector.IDRegex != "target" || freshEvaluated.Arguments.(map[string]any)["id"] != "target" {
		t.Fatalf("engine result mutation escaped = %#v", freshEvaluated)
	}

	started := success.Events[3]
	authored, exists := started.Command()
	if !exists || authored.Selector == nil || authored.Selector.IDRegex == nil {
		t.Fatalf("copy started event command = %#v", authored)
	}
	*authored.Selector.IDRegex = "mutated"
	authored.Arguments.(map[string]any)["id"] = "mutated"
	freshAuthored, exists := started.Command()
	if !exists || *freshAuthored.Selector.IDRegex != "target" || freshAuthored.Arguments.(map[string]any)["id"] != "target" {
		t.Fatalf("event command mutation escaped = %#v", freshAuthored)
	}

	success.Requests[0].AppIDs[0] = "mutated"
	success.Requests[0].Text = "mutated"
	freshScenario := engine.ExecuteBatch5ReportScenariosForTest(t)[0]
	if freshScenario.Requests[0].AppIDs[0] != "com.example.batch5.report" || freshScenario.Requests[0].Text != "copied 世界" {
		t.Fatalf("request snapshot mutation escaped = %#v", freshScenario.Requests)
	}
}

// TestPublicExecuteRegistersAllClipboardCommands ensures the production
// registry sends every clipboard command to runtime.
func TestPublicExecuteRegistersAllClipboardCommands(t *testing.T) {
	t.Parallel()

	observations := engine.ObserveBatch5PublicBoundaryForTest(t)
	want := []model.CommandKeyword{
		model.CommandCopyTextFrom,
		model.CommandSetClipboard,
		model.CommandPasteText,
	}
	if len(observations) != len(want) {
		t.Fatalf("observations = %#v", observations)
	}
	for index, observation := range observations {
		if observation.Keyword != want[index] || !observation.PreparePassed {
			t.Fatalf("public observation %d = %#v", index, observation)
		}
		// Device outcomes are outside this registry-boundary test.
		if observation.RuntimeCalls == 0 {
			t.Fatalf("clipboard keyword %s did not reach runtime: %#v", observation.Keyword, observation)
		}
		var configuration *engine.ConfigurationError
		if errors.As(observation.ExecuteErr, &configuration) && configuration != nil {
			t.Fatalf("clipboard keyword %s still fails closed at the registry: %v", observation.Keyword, observation.ExecuteErr)
		}
	}
}

func assertBatch5EventAgreement(t testing.TB, scenario engine.Batch5ReportScenario, commands []engine.CommandResult) {
	t.Helper()
	wantEventCount := 2 + 2*len(commands)
	if len(scenario.Events) != wantEventCount {
		t.Fatalf("events = %d, want %d", len(scenario.Events), wantEventCount)
	}
	if scenario.Events[0].Kind() != engine.EventFlowStarted ||
		scenario.Events[len(scenario.Events)-1].Kind() != engine.EventFlowFinished {
		t.Fatalf("flow event boundaries = %#v", scenario.Events)
	}
	for index, command := range commands {
		started := scenario.Events[1+2*index]
		finished := scenario.Events[2+2*index]
		if started.Kind() != engine.EventCommandStarted || finished.Kind() != engine.EventCommandFinished ||
			started.RootRunID() != command.RootRunID() || finished.RootRunID() != command.RootRunID() ||
			started.Sequence() != command.Sequence() || finished.Sequence() != command.Sequence() ||
			started.Depth() != command.Depth() || finished.Depth() != command.Depth() ||
			finished.Outcome() != command.Outcome() || finished.ProductError() != command.ProductError() ||
			!reflect.DeepEqual(finished.Metadata(), command.Metadata()) {
			t.Fatalf("event/command agreement %d = started %#v finished %#v command %#v", index, started, finished, command)
		}
		authored, exists := started.Command()
		if !exists || !authored.Equivalent(command.Command()) {
			t.Fatalf("started authored command %d = %#v, command %#v", index, authored, command.Command())
		}
	}
	finished := scenario.Events[len(scenario.Events)-1]
	if finished.RootRunID() != scenario.Flow.RootRunID() || finished.Outcome() != scenario.Flow.Outcome() ||
		finished.ProductError() != scenario.Flow.ProductError() {
		t.Fatalf("flow-finished agreement = event %#v flow %#v", finished, scenario.Flow)
	}
}

func assertBatch5SuccessProjection(t testing.TB, scenario engine.Batch5ReportScenario, converted report.FlowResult) {
	t.Helper()
	if scenario.Err != nil || scenario.Flow.ProductError() != nil ||
		!reflect.DeepEqual(scenario.Requests, []device.InputTextRequest{{
			Text: "copied 世界", AppIDs: []string{"com.example.batch5.report"},
		}}) {
		t.Fatalf("success scenario = %#v", scenario)
	}
	commands := scenario.Flow.Commands()
	wantKinds := []model.CommandKeyword{model.CommandSetClipboard, model.CommandCopyTextFrom, model.CommandPasteText}
	wantDescriptions := []string{"setClipboard", "copy target", "pasteText"}
	for index := range wantKinds {
		if commands[index].Command().Kind != wantKinds[index] || commands[index].Outcome() != engine.Completed ||
			converted.Commands[index].Keyword != string(wantKinds[index]) ||
			converted.Commands[index].Description != wantDescriptions[index] || converted.Commands[index].Status != report.Completed {
			t.Fatalf("success command %d = engine %#v report %#v", index, commands[index], converted.Commands[index])
		}
	}
	setEvaluated, _ := commands[0].Metadata().EvaluatedCommand()
	copyEvaluated, _ := commands[1].Metadata().EvaluatedCommand()
	if setEvaluated.Arguments != "seed 世界" || copyEvaluated.Selector == nil || copyEvaluated.Selector.IDRegex == nil ||
		*copyEvaluated.Selector.IDRegex != "target" || copyEvaluated.Label == nil || *copyEvaluated.Label != "copy target" {
		t.Fatalf("success evaluated metadata = set %#v copy %#v", setEvaluated, copyEvaluated)
	}
}
