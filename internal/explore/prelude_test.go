package explore

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The walk to a scenario's start screen is the flow's missing prefix, and the
// crew is the only place that knows which run it belongs to: the navigator
// walks, the tester runs, and nothing joined the two until the result carried
// both.
func TestTheWalkToTheStartScreenReachesTheResult(t *testing.T) {
	t.Parallel()

	walked := []StepRecord{{
		Index: 1, Status: StepOK,
		Action: Action{Kind: ActionTap, Target: &Locator{Kind: LocatorID, Value: "today-button"}},
	}}
	fake := &fakeCrew{
		state:      &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		reachSteps: walked,
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal, StartScreen: "elsewhere-12345678"}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("ran %d scenarios, want 1", len(report.Results))
	}
	prelude := report.Results[0].Prelude
	if len(prelude) != 1 || prelude[0].Action.Target.Value != "today-button" {
		t.Fatalf("prelude = %+v, want the step the navigator walked", prelude)
	}
}

// A scenario already standing on its start screen is not walked to, so its
// flow gets no prefix it did not need.
func TestNoWalkIsRecordedWhenTheScenarioAlreadyStandsThere(t *testing.T) {
	t.Parallel()

	here := ScreenSignature{AppID: "app", TreeDigest: "d1", Salient: []string{"Today"}}
	fake := &fakeCrew{
		state:      &ScreenState{Signature: here},
		reachSteps: []StepRecord{{Index: 1, Status: StepOK}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal, StartScreen: here.Key()}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.reached) != 0 {
		t.Fatalf("the navigator was sent to %v for a screen it already stood on", fake.reached)
	}
	if walk := report.Results[0].Prelude; len(walk) != 0 {
		t.Fatalf("prelude = %+v, want nothing to prepend", walk)
	}
}

// Two sessions in a row reported no failed reach, which reads as "the
// navigator works" and could equally mean "no scenario ever asked". A reach
// that succeeds has to say so, or its absence proves nothing.
func TestASuccessfulWalkIsOnTheRecord(t *testing.T) {
	t.Parallel()

	fake := &fakeCrew{
		state:      &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		reachSteps: []StepRecord{{Index: 1, Status: StepOK}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal, StartScreen: "elsewhere-12345678"}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(report.Results[0].Notes, "\n")
	if !strings.Contains(notes, "elsewhere-12345678") {
		t.Fatalf("notes = %q, want the start screen the navigator walked to", notes)
	}
}

// 16c91cd filled an absent StartScreen from the screen the plan was made
// against, and mmx80 shows the cost: every one of its scenarios inherited the
// same key, a SEARCH screen an earlier scenario had left open, and each was
// dragged there before it ran. Six walks to one key, two scenarios passed,
// five ended in execution problems -- the worst session of the day.
//
// The planner's key is per scenario; the map's screen is per plan, and using
// one for the other says every scenario begins where the planner happened to
// be looking. A scenario with no start screen goes back to running from
// wherever the relaunch left it, which is what it did before.
func TestAScenarioWithNoStartScreenIsNotWalkedAnywhere(t *testing.T) {
	t.Parallel()

	planned := ScreenSignature{AppID: "app", TreeDigest: "planned", Salient: []string{"September"}}
	elsewhere := ScreenSignature{AppID: "app", TreeDigest: "elsewhere", Salient: []string{"Years"}}
	fake := &fakeCrew{
		state:        &ScreenState{Signature: planned},
		readyState:   &ScreenState{Signature: elsewhere},
		reachedState: &ScreenState{Signature: planned},
		reachSteps:   []StepRecord{{Index: 1, Status: StepOK}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityNormal}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	if _, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 2, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew); err != nil {
		t.Fatal(err)
	}
	if len(fake.reached) != 0 {
		t.Fatalf("reached %v, want no walk for a scenario that named no screen", fake.reached)
	}
}

// A reach that took no step walked nowhere, and mmx80 wrote "walked to the
// start screen ... in 0 steps" for one. The note is evidence a reader counts
// walks from.
func TestNoNoteForAWalkThatTookNoStep(t *testing.T) {
	t.Parallel()

	here := ScreenSignature{AppID: "app", TreeDigest: "here", Salient: []string{"September"}}
	elsewhere := ScreenSignature{AppID: "app", TreeDigest: "elsewhere", Salient: []string{"Years"}}
	fake := &fakeCrew{
		state:        &ScreenState{Signature: elsewhere},
		reachedState: &ScreenState{Signature: here},
		reachSteps:   nil,
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal, StartScreen: here.Key()}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 1, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.reached) != 1 {
		t.Fatalf("reached %v, want the walk to have been attempted", fake.reached)
	}
	for _, note := range report.Results[0].Notes {
		if strings.Contains(note, "walked to the start screen") {
			t.Fatalf("note = %q, want no claim of a walk that took no step", note)
		}
	}
}
