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
