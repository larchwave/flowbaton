package run

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

func testConfig() explore.Config {
	return explore.Config{
		AppID:           "com.example.app",
		MaxStepsPerTest: 6,
		SessionName:     "otter-42",
		Clock:           func() time.Time { return time.Unix(100, 0) },
	}
}

func newTester(driver *fakeDriver, observer *fakeObserver, worker, manager explore.LLM, config explore.Config) *Tester {
	return &Tester{
		Driver:   driver,
		Observer: observer,
		Models:   explore.ModelSet{Worker: worker, Manager: manager},
		Config:   config,
		Sleep:    noSleep,
	}
}

func TestRunScenarioTapResolutionAndDeterministicPass(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	after := makeState("com.example.app", screen("Welcome", button("Logout", "logout_button", "[0,0][100,50]")))
	for name, tapArgs := range map[string]string{
		"by eidx": `{"eidx":0}`,
		"by text": `{"text":"Login"}`,
	} {
		t.Run(name, func(t *testing.T) {
			driver := &fakeDriver{}
			observer := &fakeObserver{states: []*explore.ScreenState{after}}
			worker := &scriptedLLM{replies: []explore.Message{
				toolCall("1", "tap", tapArgs),
				toolCall("2", "finish", `{"status":"passed","outcomes":[{"expected":"Welcome","met":true,"evidence":"greeting shown"}]}`),
			}}
			tester := newTester(driver, observer, worker, nil, testConfig())
			result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "log in", Expected: []string{"Welcome"}}, home)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != explore.TestPassed {
				t.Fatalf("status %s, verdict %q", result.Status, result.Verdict)
			}
			if len(driver.tapped) != 1 || driver.tapped[0].X != 50 || driver.tapped[0].Y != 25 {
				t.Fatalf("tapped %+v", driver.tapped)
			}
			if len(result.Steps) != 1 || result.Steps[0].Status != explore.StepOK {
				t.Fatalf("steps %+v", result.Steps)
			}
			if len(result.Outcomes) != 1 || !result.Outcomes[0].Met {
				t.Fatalf("outcomes %+v", result.Outcomes)
			}
		})
	}
}

func TestRunScenarioRecordsNoChangeSteps(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "tap", `{"eidx":0}`),
		toolCall("2", "finish", `{"status":"failed"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "noop"}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) != 1 || result.Steps[0].Status != explore.StepNoChange {
		t.Fatalf("steps %+v", result.Steps)
	}
	if result.Status != explore.TestFailed {
		t.Fatalf("status %s", result.Status)
	}
}

func TestRunScenarioInjectsStallWarningAfterThreeStalledSteps(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "tap", `{"eidx":0}`),
		toolCall("2", "tap", `{"eidx":0}`),
		toolCall("3", "tap", `{"eidx":0}`),
		toolCall("4", "finish", `{"status":"failed"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	if _, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "stall"}, home); err != nil {
		t.Fatal(err)
	}
	warned := 0
	for _, request := range worker.requests {
		for _, message := range request.Messages {
			if message.Role == explore.RoleUser && strings.Contains(message.Text, "made no progress") {
				warned++
				break
			}
		}
	}
	if warned != 1 {
		t.Fatalf("stall warning seen in %d requests, want 1", warned)
	}
	if len(worker.requests) != 4 {
		t.Fatalf("worker calls %d", len(worker.requests))
	}
}

func TestRunScenarioOutcomeMergeNeverUpgradesUnmetChecks(t *testing.T) {
	t.Run("worker check overrides a met claim", func(t *testing.T) {
		home := makeState("com.example.app", screen("Home", button("Save", "save_button", "[0,0][100,50]")))
		driver := &fakeDriver{}
		observer := &fakeObserver{states: []*explore.ScreenState{home}}
		worker := &scriptedLLM{replies: []explore.Message{
			toolCall("1", "finish", `{"status":"passed","outcomes":[{"expected":"Receipt saved","met":true,"evidence":"claimed"}]}`),
			textReply(`{"met": false, "evidence": "no receipt on the final screen"}`),
		}}
		tester := newTester(driver, observer, worker, nil, testConfig())
		result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "save", Expected: []string{"Receipt saved"}}, home)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != explore.TestFailed {
			t.Fatalf("status %s, verdict %q", result.Status, result.Verdict)
		}
		if len(result.Outcomes) != 1 || result.Outcomes[0].Met {
			t.Fatalf("outcomes %+v", result.Outcomes)
		}
	})
	t.Run("a not-met claim sticks even with tree evidence", func(t *testing.T) {
		home := makeState("com.example.app", screen("Welcome", button("Next", "next_button", "[0,0][100,50]")))
		driver := &fakeDriver{}
		observer := &fakeObserver{states: []*explore.ScreenState{home}}
		worker := &scriptedLLM{replies: []explore.Message{
			toolCall("1", "finish", `{"status":"passed","outcomes":[{"expected":"Welcome","met":false,"evidence":"wrong account name"}]}`),
		}}
		tester := newTester(driver, observer, worker, nil, testConfig())
		result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "greet", Expected: []string{"Welcome"}}, home)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != explore.TestFailed {
			t.Fatalf("status %s", result.Status)
		}
		if result.Outcomes[0].Met || !strings.Contains(result.Outcomes[0].Evidence, "wrong account name") {
			t.Fatalf("outcome %+v", result.Outcomes[0])
		}
	})
}

func TestRunScenarioStopsWhenStepBudgetRunsOut(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "note", `{"text":"looking around"}`),
		toolCall("2", "note", `{"text":"still looking"}`),
	}}
	config := testConfig()
	config.MaxStepsPerTest = 2
	tester := newTester(driver, observer, worker, nil, config)
	result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "wander"}, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != explore.TestStopped {
		t.Fatalf("status %s", result.Status)
	}
	found := false
	for _, note := range result.Notes {
		if strings.Contains(note, "step budget exhausted") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes %v", result.Notes)
	}
}

// A model that does not know its budget spends it on the goal and never
// calls finish, so a scenario it may well have completed ends as "stopped"
// with no verdict. It hears the budget up front and again two steps from
// the end, when the remaining steps are exactly enough to check and finish.
func TestRunScenarioTellsTheModelTheBudgetAndWarnsTwoStepsFromTheEnd(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "note", `{"text":"one"}`),
		toolCall("2", "note", `{"text":"two"}`),
		toolCall("3", "note", `{"text":"three"}`),
		toolCall("4", "note", `{"text":"four"}`),
	}}
	config := testConfig()
	config.MaxStepsPerTest = 4
	tester := newTester(driver, observer, worker, nil, config)
	if _, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "wander"}, home); err != nil {
		t.Fatal(err)
	}
	if len(worker.requests) != 4 {
		t.Fatalf("worker calls %d", len(worker.requests))
	}
	if opening := worker.requests[0].Messages[1].Text; !strings.Contains(opening, "4 replies") {
		t.Fatalf("the scenario text does not state the budget: %q", opening)
	}
	var warnedAt []int
	for index, request := range worker.requests {
		last := request.Messages[len(request.Messages)-1]
		if last.Role == explore.RoleUser && strings.Contains(last.Text, "2 replies left") {
			warnedAt = append(warnedAt, index)
		}
	}
	if !reflect.DeepEqual(warnedAt, []int{2}) {
		t.Fatalf("budget warning seen before requests %v, want only the third", warnedAt)
	}
}

func TestRunScenarioPilotStopOrderEndsTheRun(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	fiveTaps := explore.Message{Role: explore.RoleAssistant}
	for id := 0; id < 5; id++ {
		fiveTaps.ToolCalls = append(fiveTaps.ToolCalls, explore.ToolCall{
			ID: string(rune('a' + id)), Name: "tap", Arguments: []byte(`{"eidx":0}`),
		})
	}
	worker := &scriptedLLM{replies: []explore.Message{fiveTaps}}
	manager := &scriptedLLM{replies: []explore.Message{
		textReply(`{"decision":"stop","verdict":"looping without progress","instruction":""}`),
	}}
	config := testConfig()
	config.PilotEnabled = true
	tester := newTester(driver, observer, worker, manager, config)
	result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "loopy"}, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != explore.TestStopped {
		t.Fatalf("status %s", result.Status)
	}
	if !strings.Contains(result.Verdict, "looping without progress") {
		t.Fatalf("verdict %q", result.Verdict)
	}
	if len(manager.requests) != 1 {
		t.Fatalf("pilot calls %d", len(manager.requests))
	}
	for _, message := range manager.requests[0].Messages {
		if strings.Contains(message.Text, elementTableHeading) {
			t.Fatal("pilot saw a raw element table")
		}
	}
}

// Declaring backPress is not a promise the press lands: a driver that offers
// the tool may still refuse one call, and the handler answers with a note
// rather than a tool failure the model would spend its budget retrying.
func TestRunScenarioReportsBackAsUnsupported(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{backErr: device.ErrUnsupported, features: map[string]bool{"backPress": true}}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "back", `{}`),
		toolCall("2", "finish", `{"status":"failed"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "go back"}, home)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, note := range result.Notes {
		if strings.Contains(note, "unsupported on this platform") {
			found = true
		}
	}
	if !found {
		t.Fatalf("notes %v", result.Notes)
	}
	if len(result.Steps) != 0 {
		t.Fatalf("steps %+v", result.Steps)
	}
}

func TestRunScenarioReturnsPartialResultOnCancellation(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &scriptedLLM{
		replies: []explore.Message{toolCall("1", "tap", `{"eidx":0}`)},
		onCall:  func(int) { cancel() },
	}
	tester := newTester(driver, observer, worker, nil, testConfig())
	result, err := tester.RunScenario(ctx, explore.Scenario{Name: "cut short"}, home)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
	if result == nil || result.Status != explore.TestStopped {
		t.Fatalf("result %+v", result)
	}
}

func TestRunScenarioRefusesNonPositiveStepBudget(t *testing.T) {
	config := testConfig()
	config.MaxStepsPerTest = 0
	tester := newTester(&fakeDriver{}, &fakeObserver{}, &scriptedLLM{}, nil, config)
	if _, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "x"}, nil); err == nil {
		t.Fatal("zero step budget accepted")
	}
}

func TestRunScenarioMarksAMissedTargetOnTheStep(t *testing.T) {
	// The report separates equipment failures from an agent aiming at nothing,
	// and the step record is where that distinction is made.
	home := makeState("com.example.app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "tap", `{"text":"Filter"}`),
		toolCall("2", "finish", `{"status":"failed"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	result, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "missing control"}, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps %+v", result.Steps)
	}
	step := result.Steps[0]
	if step.Status != explore.StepFailed || !step.TargetMiss {
		t.Fatalf("step = %+v, want a failed step marked as a missed target", step)
	}
	if !strings.Contains(step.ErrText, `no element matched text "Filter"`) {
		t.Fatalf("error text %q, want the message the agent reads", step.ErrText)
	}
}

func TestRunScenarioWarnsWhenTheRunBouncesBetweenTwoScreens(t *testing.T) {
	// Every step here changes the screen, so the stall count stays at zero
	// while the run makes no headway: forward, back, forward, back until the
	// budget is gone.
	home := makeState("com.example.app", screen("Home", button("Next", "next_button", "[0,0][100,50]")))
	detail := makeState("com.example.app", screen("Detail", button("Back", "back_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{detail, home, detail, home, detail}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "tap", `{"eidx":0}`),
		toolCall("2", "tap", `{"eidx":0}`),
		toolCall("3", "tap", `{"eidx":0}`),
		toolCall("4", "tap", `{"eidx":0}`),
		toolCall("5", "finish", `{"status":"failed"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	if _, err := tester.RunScenario(context.Background(), explore.Scenario{Name: "bounce"}, home); err != nil {
		t.Fatal(err)
	}
	// The warning stays in the conversation once injected, so the honest count
	// is how many times it was injected: occurrences in the final request.
	warned := 0
	last := worker.requests[len(worker.requests)-1]
	for _, message := range last.Messages {
		if message.Role == explore.RoleUser && strings.Contains(message.Text, "between two screens") {
			warned++
		}
	}
	if warned != 1 {
		t.Fatalf("cycle warning injected %d times, want 1", warned)
	}
}
