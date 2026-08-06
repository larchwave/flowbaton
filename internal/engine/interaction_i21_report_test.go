package engine_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/report"
)

func TestInteractionI21ReportProjectionCompletedWarnedFailedCancelled(t *testing.T) {
	scenarios := engine.ExecuteInteractionI21ReportScenariosForTest(t)
	if len(scenarios) != 5 {
		t.Fatalf("I21 report scenarios = %d, want five", len(scenarios))
	}
	wantStatuses := map[string]report.Status{
		"happy":     report.Completed,
		"completed": report.Completed,
		"warned":    report.Warned,
		"failed":    report.Failed,
		"cancelled": report.Cancelled,
	}
	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario.Name, func(t *testing.T) {
			commands := scenario.Flow.Commands()
			wantEvents := 2 + 2*len(commands)
			if len(scenario.Events) != wantEvents || scenario.Events[0].Kind() != engine.EventFlowStarted || scenario.Events[len(scenario.Events)-1].Kind() != engine.EventFlowFinished {
				t.Fatalf("scenario events = %#v, want %d bounded events", scenario.Events, wantEvents)
			}
			for index, command := range commands {
				started := scenario.Events[1+2*index]
				finished := scenario.Events[2+2*index]
				if started.Kind() != engine.EventCommandStarted || finished.Kind() != engine.EventCommandFinished || started.Sequence() != command.Sequence() || finished.Sequence() != command.Sequence() || finished.Outcome() != command.Outcome() || started.RootRunID() != command.RootRunID() || finished.RootRunID() != command.RootRunID() {
					t.Fatalf("scenario %s event/command %d disagreement: started %#v finished %#v command %#v", scenario.Name, index, started, finished, command)
				}
			}
			converted, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
			if err != nil {
				t.Fatalf("report.FromEngineFlowResult(%s) error = %v", scenario.Name, err)
			}
			if converted.Status != wantStatuses[scenario.Name] || converted.Name != scenario.Config.Name || converted.Description != scenario.Flow.Path() || len(converted.Commands) != len(commands) || converted.Metadata["rootRunId"] != scenario.Flow.RootRunID() {
				t.Fatalf("scenario %s report = %#v", scenario.Name, converted)
			}
			for index, command := range commands {
				evaluated, ok := command.Metadata().EvaluatedCommand()
				if !ok || converted.Commands[index].Keyword != string(evaluated.Kind) || converted.Commands[index].Sequence != int64(command.Sequence()) || converted.Commands[index].Depth != command.Depth() || converted.Commands[index].Metadata["rootRunId"] != command.RootRunID() {
					t.Fatalf("scenario %s report command %d = %#v evaluated %#v", scenario.Name, index, converted.Commands[index], evaluated)
				}
			}
			if scenario.Name == "happy" {
				wantKinds := []string{"doubleTapOn", "longPressOn", "back", "hideKeyboard", "pasteText", "scroll", "scrollUntilVisible", "inputText", "inputRandomText", "inputRandomNumber", "inputRandomEmail", "inputRandomPersonName", "inputRandomCityName", "inputRandomCountryName", "inputRandomColorName", "swipe", "pressKey", "eraseText", "action", "copyTextFrom", "setClipboard"}
				gotKinds := make([]string, len(converted.Commands))
				for index := range converted.Commands {
					gotKinds[index] = converted.Commands[index].Keyword
				}
				if !reflect.DeepEqual(gotKinds, wantKinds) {
					t.Fatalf("happy report exact-21 order = %#v, want %#v", gotKinds, wantKinds)
				}
				wantGenerated := []engine.InputRequest{
					{Kind: engine.InputText, Length: 3}, {Kind: engine.InputNumber, Length: 4},
					{Kind: engine.InputEmail, Length: -1}, {Kind: engine.InputPersonName, Length: -1},
					{Kind: engine.InputCityName, Length: -1}, {Kind: engine.InputCountryName, Length: -1},
					{Kind: engine.InputColorName, Length: -1},
				}
				if !reflect.DeepEqual(scenario.GeneratorRequests, wantGenerated) {
					t.Fatalf("happy same-run generator/report agreement = %#v, want %#v", scenario.GeneratorRequests, wantGenerated)
				}
				methodCounts := map[enginetest.Method]int{}
				for _, action := range scenario.DriverActions {
					methodCounts[action.Method]++
				}
				for method, want := range map[enginetest.Method]int{
					enginetest.MethodTap: 2, enginetest.MethodLongPress: 1, enginetest.MethodBackPress: 2,
					enginetest.MethodHideKeyboard: 1, enginetest.MethodScrollVertical: 1, enginetest.MethodSwipe: 1,
					enginetest.MethodPressKey: 1, enginetest.MethodEraseText: 1, enginetest.MethodInputText: 9,
				} {
					if methodCounts[method] != want {
						t.Fatalf("happy same-run device/report %s count = %d, want %d; actions %#v", method, methodCounts[method], want, scenario.DriverActions)
					}
				}
			}
			switch scenario.Name {
			case "warned":
				want := []engine.Outcome{engine.Completed, engine.Warned, engine.Completed, engine.Completed}
				got := make([]engine.Outcome, len(commands))
				for index := range commands {
					got[index] = commands[index].Outcome()
				}
				if !reflect.DeepEqual(got, want) || commands[1].ProductError() == nil || len(commands[1].Artifacts()) != 1 || converted.Commands[1].Failure == nil || len(converted.Commands[1].Artifacts) != 1 {
					t.Fatalf("warned continuation/evidence = outcomes %#v flow %#v report %#v", got, scenario.Flow, converted)
				}
				var pasted []device.InputTextRequest
				for _, action := range scenario.DriverActions {
					if action.Method == enginetest.MethodInputText {
						pasted = append(pasted, action.Request.(device.InputTextRequest))
					}
				}
				wantPasted := []device.InputTextRequest{{Text: "retained-before-warning", AppIDs: []string{"com.example.g003.i21.warned"}}}
				if !reflect.DeepEqual(pasted, wantPasted) {
					t.Fatalf("warned retained copiedText/later paste = %#v, want %#v; actions %#v", pasted, wantPasted, scenario.DriverActions)
				}
			case "failed":
				if len(commands) != 2 || commands[0].Outcome() != engine.Completed || commands[1].Outcome() != engine.Failed || scenario.Err == nil || scenario.Flow.ProductError() != scenario.Err || commands[1].ProductError() != scenario.Err || len(commands[1].Artifacts()) != 1 || len(converted.Commands) != 2 || converted.Failure == nil || converted.Failure.Message != scenario.Err.Error() {
					t.Fatalf("failed cutoff/identity/evidence = error %p flow %#v report %#v", scenario.Err, scenario.Flow, converted)
				}
			case "cancelled":
				if len(commands) != 2 || commands[0].Outcome() != engine.Completed || commands[1].Outcome() != engine.Cancelled || !errors.Is(scenario.Err, context.Canceled) || scenario.Flow.ProductError() != scenario.Err || commands[1].ProductError() != scenario.Err || len(commands[1].Artifacts()) != 0 || len(converted.Commands) != 2 {
					t.Fatalf("cancelled cutoff/identity = error %p flow %#v report %#v", scenario.Err, scenario.Flow, converted)
				}
				methods := make([]enginetest.Method, len(scenario.DriverActions))
				for index, action := range scenario.DriverActions {
					methods[index] = action.Method
				}
				wantMethods := []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodBackPress}
				for range 10 {
					wantMethods = append(wantMethods, enginetest.MethodWaitForAppToSettle)
				}
				wantMethods = append(wantMethods, enginetest.MethodContentDescriptor, enginetest.MethodScrollVertical)
				if !reflect.DeepEqual(methods, wantMethods) {
					t.Fatalf("cancelled exact Driver cutoff = %#v, want %#v", methods, wantMethods)
				}
			}
			if scenario.Name == "happy" || scenario.Name == "completed" || scenario.Name == "warned" {
				if scenario.Err != nil {
					t.Fatalf("scenario %s error = %v", scenario.Name, scenario.Err)
				}
			} else if scenario.Err == nil {
				t.Fatalf("scenario %s error = nil", scenario.Name)
			}
			if scenario.Name == "cancelled" && !errors.Is(scenario.Err, context.Canceled) {
				t.Fatalf("cancelled error = %T %v", scenario.Err, scenario.Err)
			}
		})
	}
}

func TestInteractionI21ReportProjectionIsDefensive(t *testing.T) {
	scenarios := engine.ExecuteInteractionI21ReportScenariosForTest(t)
	scenario := scenarios[1]
	first, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
	if err != nil {
		t.Fatal(err)
	}
	original := first.Commands[0].Metadata["evaluatedCommand"]
	first.Metadata["rootRunId"] = "mutated"
	first.Commands[0].Metadata["evaluatedCommand"] = "mutated"
	first.Commands[0].Description = "mutated"
	second, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
	if err != nil {
		t.Fatal(err)
	}
	if second.Metadata["rootRunId"] != scenario.Flow.RootRunID() || second.Commands[0].Metadata["evaluatedCommand"] != original || second.Commands[0].Description == "mutated" {
		t.Fatalf("report mutation escaped = %#v", second)
	}

	commands := scenario.Flow.Commands()
	evaluated, ok := commands[0].Metadata().EvaluatedCommand()
	if !ok || evaluated.Selector == nil || evaluated.Selector.IDRegex == nil {
		t.Fatalf("copy evaluated command = %#v", evaluated)
	}
	*evaluated.Selector.IDRegex = "mutated"
	freshEvaluated, ok := scenario.Flow.Commands()[0].Metadata().EvaluatedCommand()
	if !ok || freshEvaluated.Selector == nil || freshEvaluated.Selector.IDRegex == nil || *freshEvaluated.Selector.IDRegex != "copy-target" {
		t.Fatalf("engine result mutation escaped = %#v", freshEvaluated)
	}

	fresh := engine.ExecuteInteractionI21ReportScenariosForTest(t)[1]
	if !reflect.DeepEqual(fresh.Flow.Commands()[0].Command(), scenario.Flow.Commands()[0].Command()) {
		t.Fatal("scenario source mutation escaped into fresh execution")
	}

	artifactScenario := scenarios[2]
	artifactCommands := artifactScenario.Flow.Commands()
	if len(artifactCommands) != 4 || len(artifactScenario.Events) != 10 {
		t.Fatalf("warned artifact scenario shape = commands %d events %d", len(artifactCommands), len(artifactScenario.Events))
	}
	artifactCommand := artifactCommands[1]
	finishedEvent := artifactScenario.Events[4]
	commandArtifacts := artifactCommand.Artifacts()
	eventArtifacts := finishedEvent.Artifacts()
	wantArtifact := device.Artifact{Kind: "failure-screenshot", Path: "i21-warned-failure.png", Metadata: map[string]string{"provider": "owned"}}
	if finishedEvent.Kind() != engine.EventCommandFinished || finishedEvent.Sequence() != artifactCommand.Sequence() || len(commandArtifacts) != 1 || len(eventArtifacts) != 1 || !reflect.DeepEqual(commandArtifacts[0], wantArtifact) || !reflect.DeepEqual(eventArtifacts[0], wantArtifact) {
		t.Fatalf("warned artifact surfaces = command %#v event %#v", commandArtifacts, eventArtifacts)
	}
	artifactReport, artifactReportErr := report.FromEngineFlowResult(artifactScenario.Flow, artifactScenario.Config)
	wantReportArtifact := report.Artifact{Kind: "failure-screenshot", Path: "i21-warned-failure.png"}
	if artifactReportErr != nil || len(artifactReport.Commands) != 4 || len(artifactReport.Commands[1].Artifacts) != 1 || len(artifactReport.Artifacts) != 1 || artifactReport.Commands[1].Artifacts[0] != wantReportArtifact || artifactReport.Artifacts[0] != wantReportArtifact {
		t.Fatalf("warned report artifact surfaces = %#v error %v", artifactReport, artifactReportErr)
	}

	commandArtifacts[0].Path = "caller-command-mutated.png"
	commandArtifacts[0].Metadata["provider"] = "caller-command-mutated"
	eventArtifacts[0].Path = "caller-event-mutated.png"
	eventArtifacts[0].Metadata["provider"] = "caller-event-mutated"
	artifactReport.Commands[1].Artifacts[0].Path = "caller-report-command-mutated.png"
	artifactReport.Artifacts[0].Path = "caller-report-flow-mutated.png"
	if freshCommandArtifacts := artifactCommand.Artifacts(); len(freshCommandArtifacts) != 1 || !reflect.DeepEqual(freshCommandArtifacts[0], wantArtifact) {
		t.Fatalf("engine result artifact ownership escaped = %#v", freshCommandArtifacts)
	}
	if freshEventArtifacts := finishedEvent.Artifacts(); len(freshEventArtifacts) != 1 || !reflect.DeepEqual(freshEventArtifacts[0], wantArtifact) {
		t.Fatalf("engine event artifact ownership escaped = %#v", freshEventArtifacts)
	}
	freshArtifactReport, freshArtifactReportErr := report.FromEngineFlowResult(artifactScenario.Flow, artifactScenario.Config)
	if freshArtifactReportErr != nil || len(freshArtifactReport.Commands) != 4 || len(freshArtifactReport.Commands[1].Artifacts) != 1 || len(freshArtifactReport.Artifacts) != 1 || freshArtifactReport.Commands[1].Artifacts[0] != wantReportArtifact || freshArtifactReport.Artifacts[0] != wantReportArtifact {
		t.Fatalf("report artifact ownership escaped = %#v error %v", freshArtifactReport, freshArtifactReportErr)
	}
}

func TestInteractionI21ActionFailureCancellationSessionAndReportClosure(t *testing.T) {
	scenarios := engine.ExecuteBatch2BReportScenariosForTest(t)
	wantNames := []string{
		"success", "evaluator-failure", "executor-failure", "evaluator-panic", "executor-panic",
		"typed-nil-executor-failure", "terminal-settle-failure", "post-call-cancellation", "session-integrity-failure",
	}
	if len(scenarios) != len(wantNames) {
		t.Fatalf("Action closure scenarios = %d, want %d", len(scenarios), len(wantNames))
	}
	for index, scenario := range scenarios {
		if scenario.Name != wantNames[index] || len(scenario.Flow.Commands()) != 1 || len(scenario.Events) != 4 {
			t.Fatalf("Action closure scenario %d = %#v", index, scenario)
		}
		converted, err := report.FromEngineFlowResult(scenario.Flow, scenario.Config)
		if err != nil {
			t.Fatalf("Action closure report %s: %v", scenario.Name, err)
		}
		wantStatus := report.Failed
		if scenario.Name == "success" {
			wantStatus = report.Completed
		} else if scenario.Name == "post-call-cancellation" {
			wantStatus = report.Cancelled
		}
		if converted.Status != wantStatus || converted.Commands[0].Status != wantStatus || converted.Metadata["rootRunId"] != scenario.Flow.RootRunID() {
			t.Fatalf("Action closure report %s = %#v", scenario.Name, converted)
		}
		if scenario.Name == "success" {
			if scenario.Err != nil || converted.Failure != nil {
				t.Fatalf("Action success failure = %v/%#v", scenario.Err, converted.Failure)
			}
		} else if scenario.Err == nil || converted.Failure == nil || converted.Failure.Message != scenario.Err.Error() || scenario.Flow.ProductError() != scenario.Err {
			t.Fatalf("Action primary/report failure %s = %p/%p %#v", scenario.Name, scenario.Err, scenario.Flow.ProductError(), converted.Failure)
		}
	}
}
