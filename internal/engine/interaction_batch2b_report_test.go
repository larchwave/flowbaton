package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/report"
)

func TestInteractionBatch2BResultEventAndReportProjectionMatrix(t *testing.T) {
	t.Parallel()

	scenarios := engine.ExecuteBatch2BReportScenariosForTest(t)
	wantScenarios := []struct {
		name         string
		alias        string
		command      model.Command
		failureClass string
		failure      string
	}{
		{name: "success", alias: "back", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "back"}, failureClass: "none"},
		{name: "evaluator-failure", alias: "back", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "back"}, failureClass: "configuration", failure: "back requires an active appId"},
		{name: "executor-failure", alias: "scroll", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "scroll"}, failureClass: "retryable", failure: "command scroll failed: Batch 2B Driver failed"},
		{name: "evaluator-panic", alias: "hideKeyboard", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "hideKeyboard"}, failureClass: "configuration", failure: "handler evaluator panicked: Batch 2B evaluator panic"},
		{name: "executor-panic", alias: "pasteText", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "pasteText"}, failureClass: "configuration", failure: "handler executor panicked: Batch 2B executor panic"},
		{name: "typed-nil-executor-failure", alias: "back", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "back"}, failureClass: "configuration", failure: "command action failed: malformed error graph"},
		{name: "terminal-settle-failure", alias: "back", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "back"}, failureClass: "configuration", failure: "Batch 2B terminal settle failed"},
		{name: "post-call-cancellation", alias: "pasteText", command: model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: "pasteText"}, failureClass: "cancelled", failure: context.Canceled.Error()},
		{name: "session-integrity-failure", alias: "pasteText", command: model.Command{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: "poison report value"}, failureClass: "configuration", failure: "execution session integrity lost after copied text update failed"},
	}
	if got, want := len(scenarios), len(wantScenarios); got != want {
		t.Fatalf("scenarios = %d, want %d", got, want)
	}
	for index, scenario := range scenarios {
		want := wantScenarios[index]
		if scenario.Name != want.name || scenario.Alias != want.alias || scenario.FailureClass != want.failureClass ||
			len(scenario.ExpectedCommands) != 1 || !scenario.ExpectedCommands[0].Equivalent(want.command) {
			t.Fatalf("scenario %d = name %q alias %q, want name %q alias %q", index, scenario.Name, scenario.Alias, wantScenarios[index].name, wantScenarios[index].alias)
		}
		t.Run(scenario.Name, func(t *testing.T) {
			commands := scenario.Flow.Commands()
			if len(commands) != 1 || !commands[0].Command().Equivalent(want.command) || commands[0].Depth() != 0 ||
				commands[0].Sequence() != 1 || commands[0].RootRunID() != scenario.Flow.RootRunID() {
				t.Fatalf("engine projection = flow %#v commands %#v", scenario.Flow, commands)
			}
			wantOutcome := engine.Failed
			wantStatus := report.Failed
			if scenario.Name == "success" {
				wantOutcome = engine.Completed
				wantStatus = report.Completed
			} else if want.failureClass == "cancelled" {
				wantOutcome = engine.Cancelled
				wantStatus = report.Cancelled
			}
			if scenario.Flow.Outcome() != wantOutcome || commands[0].Outcome() != wantOutcome ||
				(scenario.Name == "success") != (scenario.Err == nil) {
				t.Fatalf("outcome = flow %s command %s error %T %v", scenario.Flow.Outcome(), commands[0].Outcome(), scenario.Err, scenario.Err)
			}
			if scenario.FailureClass != want.failureClass || (scenario.Err == nil) != (want.failure == "") ||
				(scenario.Err != nil && scenario.Err.Error() != want.failure) {
				t.Fatalf("primary failure = class %q error %T %v, want class %q message %q", scenario.FailureClass, scenario.Err, scenario.Err, want.failureClass, want.failure)
			}
			switch want.failureClass {
			case "configuration":
				var configuration *engine.ConfigurationError
				if !errors.As(scenario.Err, &configuration) || configuration == nil {
					t.Fatalf("configuration primary = %T %v", scenario.Err, scenario.Err)
				}
			case "cancelled":
				if !errors.Is(scenario.Err, context.Canceled) {
					t.Fatalf("cancelled primary = %T %v", scenario.Err, scenario.Err)
				}
			case "retryable":
				var operation *engine.OperationError
				if !errors.As(scenario.Err, &operation) || operation == nil {
					t.Fatalf("retryable primary = %T %v", scenario.Err, scenario.Err)
				}
			}
			assertBatch2BEventAgreement(t, scenario, commands[0], want.command)

			converted, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
			if err != nil {
				t.Fatalf("report.FromEngineFlowResult() error = %v", err)
			}
			if converted.Name != scenario.Config.Name || converted.Description != scenario.Flow.Path() ||
				converted.Status != wantStatus || converted.Metadata["rootRunId"] != scenario.Flow.RootRunID() ||
				len(converted.Commands) != 1 || converted.Commands[0].Keyword != string(want.command.Kind) ||
				converted.Commands[0].Description != string(want.command.Kind) || converted.Commands[0].Status != wantStatus ||
				converted.Commands[0].Sequence != int64(commands[0].Sequence()) || converted.Commands[0].Depth != 0 ||
				converted.Commands[0].Metadata["rootRunId"] != commands[0].RootRunID() {
				t.Fatalf("report projection = %#v", converted)
			}
			if scenario.Name == "success" {
				if converted.Failure != nil || converted.Commands[0].Failure != nil {
					t.Fatalf("success failure projection = %#v", converted)
				}
			} else if converted.Failure == nil || converted.Commands[0].Failure == nil ||
				converted.Failure.Message != want.failure || converted.Commands[0].Failure.Message != want.failure ||
				scenario.Flow.ProductError() != scenario.Err || commands[0].ProductError() != scenario.Err {
				t.Fatalf("failure projection = %#v engine error %p/%p/%p", converted, scenario.Err, scenario.Flow.ProductError(), commands[0].ProductError())
			}
			if scenario.Name == "typed-nil-executor-failure" {
				var configuration *engine.ConfigurationError
				if !errors.As(scenario.Err, &configuration) || configuration == nil ||
					scenario.Err.Error() != "command action failed: malformed error graph" {
					t.Fatalf("typed-nil report failure = %T %v, want sanitized non-nil ConfigurationError", scenario.Err, scenario.Err)
				}
			}
			if evaluated, exists := commands[0].Metadata().EvaluatedCommand(); exists {
				if evaluated.Kind != want.command.Kind || evaluated.Arguments != want.command.Arguments {
					t.Fatalf("evaluated engine metadata = %#v", evaluated)
				}
				encoded, encodeErr := json.Marshal(evaluated)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				if converted.Commands[0].Metadata["evaluatedCommand"] != string(encoded) {
					t.Fatalf("evaluated report metadata = %#v, want %s", converted.Commands[0].Metadata, encoded)
				}
			}
			if scenario.Name == "session-integrity-failure" {
				for _, command := range commands {
					if command.Command().Kind == model.CommandAction {
						t.Fatalf("session-integrity engine leaked later Action = %#v", commands)
					}
				}
				for _, event := range scenario.Events {
					if authored, exists := event.Command(); exists && authored.Kind == model.CommandAction {
						t.Fatalf("session-integrity events leaked later Action = %#v", scenario.Events)
					}
				}
				for _, command := range converted.Commands {
					if command.Keyword == string(model.CommandAction) {
						t.Fatalf("session-integrity report leaked later Action = %#v", converted)
					}
				}
			}
		})
	}
}

func TestInteractionBatch2BProjectionSnapshotsAreDefensive(t *testing.T) {
	t.Parallel()

	success := engine.ExecuteBatch2BReportScenariosForTest(t)[0]
	converted, err := report.FromEngineFlowResult(success.Flow, success.Config)
	if err != nil {
		t.Fatal(err)
	}
	originalEvaluated := converted.Commands[0].Metadata["evaluatedCommand"]
	converted.Metadata["rootRunId"] = "mutated"
	converted.Commands[0].Metadata["evaluatedCommand"] = "mutated"
	converted.Commands[0].Description = "mutated"
	fresh, err := report.FromEngineFlowResult(success.Flow, success.Config)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Metadata["rootRunId"] != success.Flow.RootRunID() ||
		fresh.Commands[0].Metadata["evaluatedCommand"] != originalEvaluated ||
		fresh.Commands[0].Description != string(model.CommandAction) {
		t.Fatalf("report mutation escaped = %#v", fresh)
	}

	commands := success.Flow.Commands()
	evaluated, exists := commands[0].Metadata().EvaluatedCommand()
	if !exists || evaluated.Arguments != "back" {
		t.Fatalf("Action evaluated metadata = %#v", evaluated)
	}
	evaluated.Arguments = "mutated"
	freshEvaluated, exists := success.Flow.Commands()[0].Metadata().EvaluatedCommand()
	if !exists || freshEvaluated.Kind != model.CommandAction || freshEvaluated.Arguments != "back" {
		t.Fatalf("engine result mutation escaped = %#v", freshEvaluated)
	}

	started := success.Events[1]
	authored, exists := started.Command()
	if !exists || authored.Kind != model.CommandAction || authored.Arguments != "back" {
		t.Fatalf("started event command = %#v", authored)
	}
	authored.Arguments = "mutated"
	freshAuthored, exists := started.Command()
	if !exists || freshAuthored.Kind != model.CommandAction || freshAuthored.Arguments != "back" {
		t.Fatalf("event mutation escaped = %#v", freshAuthored)
	}
}

// TestPublicExecuteRegistersActionAndClearKeychain ensures both commands cross
// the production registry boundary.
func TestPublicExecuteRegistersActionAndClearKeychain(t *testing.T) {
	t.Parallel()

	observations := engine.ObserveBatch2BPublicBoundaryForTest(t)
	want := []model.CommandKeyword{model.CommandAction, model.CommandClearKeychain}
	if len(observations) != len(want) {
		t.Fatalf("observations = %#v", observations)
	}
	for index, observation := range observations {
		if observation.Keyword != want[index] || !observation.PreparePassed {
			t.Fatalf("public observation %d = %#v", index, observation)
		}
		// Runtime creation confirms that the command crossed the registry boundary.
		// Device and platform outcomes are outside this test.
		if observation.RuntimeCalls == 0 {
			t.Fatalf("%s was not admitted: %#v", observation.Keyword, observation)
		}
		if observation.Results == 0 {
			t.Fatalf("%s produced no flow result, so it never executed: %#v", observation.Keyword, observation)
		}
	}
}

func assertBatch2BEventAgreement(
	t testing.TB,
	scenario engine.Batch2BReportScenario,
	command engine.CommandResult,
	want model.Command,
) {
	t.Helper()
	wantKinds := []engine.EventKind{
		engine.EventFlowStarted, engine.EventCommandStarted, engine.EventCommandFinished, engine.EventFlowFinished,
	}
	if len(scenario.Events) != len(wantKinds) {
		t.Fatalf("events = %#v, want four", scenario.Events)
	}
	gotKinds := make([]engine.EventKind, len(scenario.Events))
	for index, event := range scenario.Events {
		gotKinds[index] = event.Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("event kinds = %#v, want %#v", gotKinds, wantKinds)
	}
	started, finished := scenario.Events[1], scenario.Events[2]
	if started.RootRunID() != command.RootRunID() || finished.RootRunID() != command.RootRunID() ||
		started.Sequence() != command.Sequence() || finished.Sequence() != command.Sequence() ||
		started.Depth() != 0 || finished.Depth() != 0 || finished.Outcome() != command.Outcome() ||
		finished.ProductError() != command.ProductError() || !reflect.DeepEqual(finished.Metadata(), command.Metadata()) {
		t.Fatalf("event/command agreement = started %#v finished %#v command %#v", started, finished, command)
	}
	authored, exists := started.Command()
	if !exists || !authored.Equivalent(want) || !authored.Equivalent(command.Command()) {
		t.Fatalf("authored command = %#v command %#v want %#v", authored, command.Command(), want)
	}
	flowFinished := scenario.Events[3]
	if flowFinished.RootRunID() != scenario.Flow.RootRunID() ||
		flowFinished.Outcome() != scenario.Flow.Outcome() || flowFinished.ProductError() != scenario.Flow.ProductError() {
		t.Fatalf("flow-finished agreement = event %#v flow %#v", flowFinished, scenario.Flow)
	}
}
