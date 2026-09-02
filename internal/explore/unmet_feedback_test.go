package explore

import (
	"context"
	"slices"
	"testing"
	"time"
)

// The planner is told which expectations earlier runs already looked for and
// did not find, so it stops rebuilding scenarios around the same one. The
// channel existed and was fed only by outcomes the JUDGE called inapplicable,
// which it never does: every session so far reports "0 with an expectation
// the app never promised" while filing defects of exactly that kind. mmx78
// planned the same invented day view three times in one session.
func TestAnUnmetOutcomeReachesTheNextPlanner(t *testing.T) {
	t.Parallel()

	invented := "The screen displays a day view titled \"September 2026\""
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal}},
			{{Name: "b", Priority: PriorityNormal}},
		},
		outcomes: []OutcomeCheck{
			{Expected: invented, Met: false},
			{Expected: "a met one", Met: true},
			{Expected: "a driver probe", Met: false, Driver: true},
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
	if len(fake.requests) < 2 {
		t.Fatalf("the planner was asked %d times, want at least 2", len(fake.requests))
	}
	if got := fake.requests[0].Unmet; len(got) != 0 {
		t.Fatalf("the first plan already carried %v", got)
	}
	got := fake.requests[1].Unmet
	if !slices.Contains(got, invented) {
		t.Fatalf("Unmet = %v, want the expectation the run did not find", got)
	}
	if slices.Contains(got, "a met one") {
		t.Fatalf("Unmet = %v, want no outcome the run did find", got)
	}
	if slices.Contains(got, "a driver probe") {
		t.Fatalf("Unmet = %v, want no driver probe; those are the run's evidence", got)
	}
}

// "Looked for and did not find" is a claim about a run that ran. Two kinds
// of result cannot support it, and both arrive with Met false like any other:
// an outcome the judge never reached a verdict on -- an undecidable, a model
// failure, an unreadable reply -- and every outcome of a run that stopped
// before it finished its work. mmx69 passed a scenario whose eighteen device
// calls all failed against a dead runner; banning what that run "did not
// find" would take the next plan's subject away on no evidence at all.
func TestAnUnjudgedOrStoppedRunBansNothing(t *testing.T) {
	t.Parallel()

	expectation := "The screen displays a day view"
	unjudged := TestResult{
		Status: TestFailed,
		Outcomes: []OutcomeCheck{
			{Expected: expectation, Met: false, Missed: MissUnjudged},
		},
	}
	if got := collectUnmet(nil, unjudged); len(got) != 0 {
		t.Fatalf("Unmet = %v, want nothing from an outcome nobody judged", got)
	}

	stopped := TestResult{
		Status: TestStopped,
		Outcomes: []OutcomeCheck{
			{Expected: expectation, Met: false},
		},
	}
	if got := collectUnmet(nil, stopped); len(got) != 0 {
		t.Fatalf("Unmet = %v, want nothing from a run that never finished", got)
	}

	// The case the feed exists for still passes through.
	ran := TestResult{
		Status: TestFailed,
		Outcomes: []OutcomeCheck{
			{Expected: expectation, Met: false},
		},
	}
	if got := collectUnmet(nil, ran); len(got) != 1 || got[0] != expectation {
		t.Fatalf("Unmet = %v, want the outcome a finished run did not find", got)
	}
}

// The Unmet feed reaches the planner between plans, and a plan is written all
// at once. mmx82 wrote eight scenarios in one call, five of them the same test
// with cosmetic variations -- "a day with multiple event bars", "with +6 more
// events", "with mixed color event bars", "with a single pink event bar" --
// and all five failed on one invented expectation, which the report then filed
// as a single High defect naming five tests. Dedup by name cannot see it: the
// names differ. The expectations do not.
func TestAScenarioIsSkippedWhenEveryOutcomeWasAlreadyMissed(t *testing.T) {
	t.Parallel()

	invented := "An inline image element labelled 'event image' is visible"
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{{
			{Name: "a", Priority: PriorityNormal, Expected: []string{invented}},
			{Name: "b", Priority: PriorityNormal, Expected: []string{invented}},
			{Name: "c", Priority: PriorityNormal, Expected: []string{"something else"}},
		}},
		outcomes: []OutcomeCheck{{Expected: invented, Met: false}},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	report, err := RunSession(context.Background(), Config{
		AppID: "app", MaxTests: 3, Styles: []string{"normal"},
		Clock: func() time.Time { return clock },
	}, crew)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fake.ran, "b") {
		t.Fatalf("ran %v, want b skipped: every outcome it checks was already missed", fake.ran)
	}
	if !slices.Contains(fake.ran, "c") {
		t.Fatalf("ran %v, want c to run: it checks something nothing has looked for", fake.ran)
	}
	if len(report.Results) != 2 {
		t.Fatalf("%d results, want the two scenarios that ran", len(report.Results))
	}
}

// A scenario that expects nothing has no outcome to have missed, and skipping
// it on an empty set would skip every such scenario forever.
func TestAScenarioExpectingNothingIsStillRun(t *testing.T) {
	t.Parallel()

	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{{
			{Name: "a", Priority: PriorityNormal, Expected: []string{"x"}},
			{Name: "b", Priority: PriorityNormal},
		}},
		outcomes: []OutcomeCheck{{Expected: "x", Met: false}},
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
	if !slices.Contains(fake.ran, "b") {
		t.Fatalf("ran %v, want the scenario with no expectations to run", fake.ran)
	}
}
