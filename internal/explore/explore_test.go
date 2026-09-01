package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScreenSignatureKeyIsSlugAndDigestBounded(t *testing.T) {
	signature := ScreenSignature{
		AppID:      "com.example.app",
		Salient:    []string{"Login  Screen!", ""},
		TreeDigest: "abcdef0123456789",
	}
	key := signature.Key()
	if key != "login-screen-abcdef01" {
		t.Fatalf("unexpected key %q", key)
	}
	if empty := (ScreenSignature{TreeDigest: "beef"}).Key(); empty != "beef" {
		t.Fatalf("digest-only key %q", empty)
	}
}

func TestPlanPendingOrdersByPriorityThenInsertion(t *testing.T) {
	plan := Plan{Scenarios: []Scenario{
		{Name: "low", Priority: PriorityLow},
		{Name: "crit", Priority: PriorityCritical},
		{Name: "done", Priority: PriorityCritical, Status: ScenarioPassed},
		{Name: "normal-a", Priority: PriorityNormal},
		{Name: "normal-b", Priority: PriorityNormal},
	}}
	pending := plan.Pending()
	got := make([]string, 0, len(pending))
	for _, s := range pending {
		got = append(got, s.Name)
	}
	want := "crit,normal-a,normal-b,low"
	if strings.Join(got, ",") != want {
		t.Fatalf("order %v, want %s", got, want)
	}
}

type scriptedLLM struct {
	replies []Message
	calls   int
	// seen keeps every request as sent, so a test can assert what the
	// second attempt of a retry actually carried.
	seen []ChatRequest
}

func (s *scriptedLLM) Chat(_ context.Context, request ChatRequest) (ChatResponse, error) {
	s.seen = append(s.seen, request)
	if s.calls >= len(s.replies) {
		return ChatResponse{}, errors.New("script exhausted")
	}
	reply := s.replies[s.calls]
	s.calls++
	return ChatResponse{Message: reply, Usage: Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func TestRunToolLoopExecutesToolsUntilPlainReply(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "ping", Arguments: json.RawMessage(`{}`)}}},
		{Role: RoleAssistant, Text: "done"},
	}}
	pinged := false
	box := ToolBox{
		Specs: []ToolSpec{{Name: "ping"}},
		Handlers: map[string]ToolHandler{
			"ping": func(context.Context, json.RawMessage) (string, error) {
				pinged = true
				return "pong", nil
			},
		},
	}
	result, err := RunToolLoop(context.Background(), llm, []Message{{Role: RoleUser, Text: "go"}}, box, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !pinged || result.Stopped || result.Exhausted {
		t.Fatalf("pinged=%v result=%+v", pinged, result)
	}
	if result.Usage.InputTokens != 2 {
		t.Fatalf("usage %+v", result.Usage)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Text != "done" {
		t.Fatalf("last message %+v", last)
	}
}

func TestRunToolLoopStopRequestAndBounds(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "halt"}}},
	}}
	box := ToolBox{Handlers: map[string]ToolHandler{
		"halt": func(context.Context, json.RawMessage) (string, error) {
			return "stopping", ErrStopRequested
		},
	}}
	result, err := RunToolLoop(context.Background(), llm, nil, box, 3)
	if err != nil || !result.Stopped {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if _, err := RunToolLoop(context.Background(), llm, nil, box, 0); err == nil {
		t.Fatal("zero bound accepted")
	}
}

type fakeCrew struct {
	state       *ScreenState
	plans       [][]Scenario
	planCall    int
	ran         []string
	outcomes    []OutcomeCheck
	requests    []PlanRequest
	failAfter   int
	failVerdict string
	failErr     error
	failEvery   bool
	reportFail  bool
	readyCalls  int
	// starts records the state each scenario was handed.
	starts []*ScreenState
	// reached records the key of every Reach call.
	reached      []string
	reachedState *ScreenState
	reachSteps   []StepRecord
	reachErr     error
}

func (f *fakeCrew) Observe(context.Context) (*ScreenState, error) { return f.state, nil }
func (f *fakeCrew) Research(_ context.Context, s *ScreenState) (*UIMap, error) {
	return &UIMap{Screen: s.Signature}, nil
}
func (f *fakeCrew) PlanNext(_ context.Context, request PlanRequest) ([]Scenario, error) {
	f.requests = append(f.requests, request)
	if f.planCall >= len(f.plans) {
		return nil, nil
	}
	scenarios := f.plans[f.planCall]
	f.planCall++
	return scenarios, nil
}
func (f *fakeCrew) RunScenario(_ context.Context, s Scenario, start *ScreenState) (*TestResult, error) {
	f.ran = append(f.ran, s.Name)
	f.starts = append(f.starts, start)
	if f.failEvery || (f.failAfter > 0 && len(f.ran) > f.failAfter) {
		err := error(errors.New("device unreachable"))
		if f.failErr != nil {
			err = f.failErr
		}
		return &TestResult{Scenario: s, Status: TestStopped, Verdict: f.failVerdict}, err
	}
	return &TestResult{Scenario: s, Status: TestPassed, Outcomes: f.outcomes}, nil
}
func (f *fakeCrew) EnsureReady(context.Context) (*ScreenState, error) {
	f.readyCalls++
	return f.state, nil
}
func (f *fakeCrew) Reach(_ context.Context, key string) (*ScreenState, []StepRecord, error) {
	f.reached = append(f.reached, key)
	if f.reachErr != nil {
		return nil, nil, f.reachErr
	}
	if f.reachedState != nil {
		return f.reachedState, f.reachSteps, nil
	}
	return f.state, f.reachSteps, nil
}
func (f *fakeCrew) Report(_ context.Context, r *SessionReport) (string, error) {
	if f.reportFail {
		return "", errors.New("no model")
	}
	return "report", nil
}

func TestRunSessionSpendsBudgetAndStopsOnDryRounds(t *testing.T) {
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityCritical}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	config := Config{
		AppID: "app", MaxTests: 3, Styles: []string{"normal", "curious"},
		Clock: func() time.Time { return clock },
	}
	report, err := RunSession(context.Background(), config, crew)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fake.ran, ",") != "b,a" {
		t.Fatalf("ran %v", fake.ran)
	}
	if len(report.Results) != 2 || report.Markdown != "report" {
		t.Fatalf("report %+v", report)
	}
	if !report.Finished.Equal(clock) {
		t.Fatalf("finished %v", report.Finished)
	}
}

func TestRunSessionFailsClosed(t *testing.T) {
	if _, err := RunSession(context.Background(), Config{MaxTests: 0}, Crew{}); err == nil {
		t.Fatal("zero budget accepted")
	}
	if _, err := RunSession(context.Background(), Config{MaxTests: 1}, Crew{}); err == nil {
		t.Fatal("empty crew accepted")
	}
}

// A planner that writes an expectation the app never offers writes it
// again next round unless the session says so. The judge's verdict is the
// only place that knowledge exists, so the loop carries it back.
func TestRunSessionCarriesUnpromisedExpectationsBackToThePlanner(t *testing.T) {
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal}},
			{{Name: "b", Priority: PriorityNormal}},
		},
		outcomes: []OutcomeCheck{
			{Expected: "the Completed tile is selected", Missed: MissUnpromised},
			{Expected: "the list is empty"},
			{Expected: `visible: text "Completed"`, Missed: MissUnpromised, Driver: true},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}}
	if _, err := RunSession(context.Background(), config, crew); err != nil {
		t.Fatal(err)
	}
	if len(fake.requests) < 2 {
		t.Fatalf("want at least two plan requests, got %d", len(fake.requests))
	}
	if got := fake.requests[0].Unpromised; len(got) != 0 {
		t.Fatalf("first request already carries %v", got)
	}
	got := fake.requests[1].Unpromised
	if len(got) != 1 || got[0] != "the Completed tile is selected" {
		t.Fatalf("Unpromised = %v, want only the inapplicable scenario outcome", got)
	}
}

// mmx22 (2026-08-30, live): the first scenario passed, the simulator's
// runner then died in the second, and the whole session came back with an
// error and no report -- the passed run's evidence went with it.
func TestRunSessionReportsWhatRanWhenAScenarioAborts(t *testing.T) {
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{{
			{Name: "create a reminder", Priority: PriorityNormal},
			{Name: "rename a list", Priority: PriorityNormal},
		}},
		failAfter: 1,
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}}
	report, err := RunSession(context.Background(), config, crew)
	if err == nil {
		t.Fatal("want the device failure surfaced")
	}
	if report == nil {
		t.Fatal("want the partial report")
	}
	if len(report.Results) != 2 || report.Results[0].Status != TestPassed {
		t.Fatalf("results = %+v, want the passed run and the aborted one", report.Results)
	}
	if report.Markdown != "report" {
		t.Fatalf("markdown = %q, want the analyst's report over what ran", report.Markdown)
	}
}

// A report the analyst cannot write must never replace the failure that
// ended the session.
func TestAnAnalystFailureDoesNotMaskTheSessionFailure(t *testing.T) {
	fake := &fakeCrew{
		state:      &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans:      [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
		failAfter:  1,
		reportFail: true,
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}}
	report, err := RunSession(context.Background(), config, crew)
	if err == nil || !strings.Contains(err.Error(), "device unreachable") {
		t.Fatalf("err = %v, want the device failure", err)
	}
	if report == nil || len(report.Results) != 2 {
		t.Fatalf("want the partial report anyway, got %+v", report)
	}
}

// mmx23 (2026-08-30, live): the runner died mid-scenario and the report
// filed the run as "run stopped before a verdict", which is what a spent
// step budget also says. The operator cannot tell a dead device from a
// slow model unless the report says so.
func TestAnAbortedScenarioCarriesItsCauseIntoTheReport(t *testing.T) {
	fake := &fakeCrew{
		state:     &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans:     [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
		failAfter: 1,
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}}
	report, err := RunSession(context.Background(), config, crew)
	if err == nil {
		t.Fatal("want the failure surfaced")
	}
	aborted := report.Results[len(report.Results)-1]
	if !strings.Contains(aborted.Verdict, "device unreachable") {
		t.Fatalf("verdict = %q, want the cause that ended the run", aborted.Verdict)
	}
}

// A run that recorded its own verdict keeps it: the tester knows better
// than the loop why it stopped.
func TestAnAbortedScenarioKeepsAVerdictItAlreadyHas(t *testing.T) {
	fake := &fakeCrew{
		state:       &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans:       [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
		failAfter:   1,
		failVerdict: "pilot stop: the run left the app",
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}}
	report, _ := RunSession(context.Background(), config, crew)
	aborted := report.Results[len(report.Results)-1]
	if aborted.Verdict != "pilot stop: the run left the app" {
		t.Fatalf("verdict = %q, want the tester's own", aborted.Verdict)
	}
}

// Scenarios run back to back, so before this each one inherited the process
// the last one left. The planner writes them against the UI map of the
// screen the session opened on, and an exported flow records the actions
// without the screen they began on -- measured on mmx36, only the first
// scenario's flow replayed standalone. Every scenario now begins from a
// relaunch, and runs against the state that relaunch returned.
//
// That is what this pins: the navigator is asked, and its answer is what
// the scenario gets. Whether the app comes back on the same SCREEN is the
// app's business and not testable here -- one that restores its last screen
// comes back on it (run/navigator.go).
func TestRunSessionStartsEveryScenarioFromARelaunch(t *testing.T) {
	start := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "start"}}
	fake := &fakeCrew{
		state: start,
		plans: [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake, Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	config := Config{AppID: "app", MaxTests: 2, Styles: []string{"normal"}, Clock: func() time.Time { return clock }}
	if _, err := RunSession(context.Background(), config, crew); err != nil {
		t.Fatal(err)
	}
	if len(fake.starts) != 2 {
		t.Fatalf("ran %d scenarios, want 2", len(fake.starts))
	}
	for index, state := range fake.starts {
		if state == nil || !state.Signature.Same(start.Signature) {
			t.Fatalf("scenario %d started from %+v, want the state the relaunch returned", index+1, state)
		}
	}
	// Once at the session's start and once between the two scenarios. Not a
	// third time: with the budget spent there is nothing left to prepare
	// for, and a reset that failed there would fail a session whose work is
	// already done and whose flows are already exportable.
	if fake.readyCalls != 2 {
		t.Fatalf("EnsureReady ran %d times, want one per scenario start and none after the last", fake.readyCalls)
	}
}

// The planner writes a scenario against the UI map of one screen and records
// that screen's key. EnsureReady cannot put the app back on it: it kills and
// relaunches, and an app that restores its last view relaunches on that
// view. Nine of thirty-four replayed flows failed on their first action for
// that reason -- on 2026-09-01 six of them were Calendar, which had restored
// into its years view where not one recorded start screen exists.
//
// Reach is what navigates, and it was implemented, documented and never
// called. It is called now, and only when the relaunch landed somewhere
// else: a scenario already on its screen costs nothing.
func TestRunSessionReachesTheScenarioStartScreen(t *testing.T) {
	here := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "here"}}
	there := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "there"}}
	fake := &fakeCrew{
		state:        here,
		reachedState: there,
		plans: [][]Scenario{{
			{Name: "away", Priority: PriorityNormal, StartScreen: there.Signature.Key()},
			{Name: "home", Priority: PriorityNormal, StartScreen: here.Signature.Key()},
		}},
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if _, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 2, Clock: func() time.Time { return clock },
	}, crew); err != nil {
		t.Fatalf("session: %v", err)
	}
	if want := []string{there.Signature.Key()}; !slices.Equal(fake.reached, want) {
		t.Fatalf("reached = %q, want only the scenario whose screen differs", fake.reached)
	}
	if len(fake.starts) != 2 || fake.starts[0] != there {
		t.Errorf("the first scenario began on %+v, want the reached screen", fake.starts[0])
	}
}

// A reach that fails must not fail the session: the scenario runs from
// wherever the relaunch left the app, which is what it did before Reach was
// wired at all. The note is what tells the report's reader why it began
// somewhere else.
func TestRunSessionNotesAFailedReachAndRunsAnyway(t *testing.T) {
	here := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "here"}}
	fake := &fakeCrew{
		state:    here,
		reachErr: errors.New("no recipe worked"),
		plans: [][]Scenario{{
			{Name: "away", Priority: PriorityNormal, StartScreen: "somewhere-else-00000000"},
		}},
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatalf("a failed reach ended the session: %v", err)
	}
	if len(fake.ran) != 1 {
		t.Fatalf("scenarios run = %v, want the scenario to run anyway", fake.ran)
	}
	if len(report.Results) != 1 || len(report.Results[0].Notes) == 0 {
		t.Fatalf("no note records the failed reach: %+v", report.Results)
	}
	if !strings.Contains(report.Results[0].Notes[0], "no recipe worked") {
		t.Errorf("note = %q, want the reach error in it", report.Results[0].Notes[0])
	}
}

// mmx70 lost five of its six scenarios to one. Its first swipe ran and the
// observation after it answered "none of com.apple.mobilecal is in the
// foreground". The tester loop ends on that by design -- it has no tool that
// restores an app -- and the crew turned the scenario's error into the
// session's, so five scenarios that had not started were never attempted.
//
// Why the app left is not established. A direction-only swipe up starts at
// the centre of the screen, and replaying one against Calendar leaves the
// app in front, so the swipe is not the cause it first looked like.
//
// A relaunch is exactly what fixes an app that left the foreground, and the
// end of the scenario loop already relaunches. A device that is unreachable
// still ends the session: no relaunch reaches a dead runner.
func TestRunSessionCarriesOnWhenTheAppLeavesTheForeground(t *testing.T) {
	state := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}}
	fake := &fakeCrew{
		state:       state,
		failAfter:   0,
		plans:       [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
		failEvery:   true,
		failErr:     fmt.Errorf("tester turn 1: %w: observe after swipe", ErrScreenUnobservable),
		failVerdict: "stopped",
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 2, Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatalf("the session ended on a scenario's lost foreground: %v", err)
	}
	if !slices.Equal(fake.ran, []string{"a", "b"}) {
		t.Fatalf("ran %q, want both scenarios attempted", fake.ran)
	}
	if len(report.Results) != 2 {
		t.Fatalf("results %d, want both kept", len(report.Results))
	}
	if len(report.Results[0].Notes) == 0 ||
		!strings.Contains(report.Results[0].Notes[0], "foreground") {
		t.Errorf("no note says the app left the foreground: %+v", report.Results[0].Notes)
	}
}

// A dead runner still ends the session: no relaunch reaches it.
func TestRunSessionStillStopsWhenTheDeviceIsUnreachable(t *testing.T) {
	state := &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}}
	fake := &fakeCrew{
		state:     state,
		plans:     [][]Scenario{{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}}},
		failEvery: true,
		failErr:   fmt.Errorf("tester turn 1: %w", ErrDeviceUnreachable),
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if _, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 2, Clock: func() time.Time { return clock },
	}, crew); err == nil {
		t.Fatal("an unreachable device did not end the session")
	}
	if len(fake.ran) != 1 {
		t.Errorf("ran %q, want the session to stop after the first", fake.ran)
	}
}

// The crew must ask the signature the same question the navigator asks on
// arrival. It checked exact key equality, so a scenario whose start
// screen was in front under a different digest -- the common case, since the
// digest covers the whole tree -- still cost a reach: a kill, a launch and an
// observation, all for a check the navigator answers on its first line.
func TestRunSessionSkipsAReachWhenTheLabelsAlreadyMatch(t *testing.T) {
	here := &ScreenState{Signature: ScreenSignature{
		AppID: "app", Salient: []string{"Search", "Add"}, TreeDigest: "cbc05d8a11112222"}}
	fake := &fakeCrew{
		state: here,
		plans: [][]Scenario{{{Name: "a", Priority: PriorityNormal, StartScreen: "search-add-4d3ffed3"}}},
	}
	crew := Crew{Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if _, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Clock: func() time.Time { return clock },
	}, crew); err != nil {
		t.Fatal(err)
	}
	if len(fake.reached) != 0 {
		t.Errorf("reached %q, want none: the screen in front already carries those labels", fake.reached)
	}
}
