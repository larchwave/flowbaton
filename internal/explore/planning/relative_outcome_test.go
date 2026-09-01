package planning

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// The rules tell the planner not to write an outcome relative to an earlier
// screen, because the judge sees the final screen alone. It wrote them
// anyway, and both cost a scenario: mmx57 filed "[Medium] not observed: the
// toggle action opens the alternate view, leaving the date strip … no longer
// the primary visible surface", and mmx60 "[Medium] not observed: The view
// mode indicator changes from the timeline view" -- whose own evidence reads
// "has changed from timeline to list view … but the expected outcome
// specifies a change, which is not demonstrated". A rule nothing enforces is
// a rule the model may ignore.
func TestFilterDropsAnOutcomeAboutTheScreenBefore(t *testing.T) {
	t.Parallel()

	raw := []plannedScenario{{
		Name:     "Toggle the day list view",
		Priority: "high",
		Steps:    []string{"Tap the toggle"},
		Expected: []string{
			"The view mode indicator changes from the timeline view",
			"A list of the day's events is shown",
		},
	}}
	got := filterScenarios(raw, explore.PlanRequest{Map: &explore.UIMap{}, Budget: 4}, "normal")
	if len(got) != 1 {
		t.Fatalf("scenarios = %d, want 1", len(got))
	}
	if want := []string{"A list of the day's events is shown"}; !reflect.DeepEqual(got[0].Expected, want) {
		t.Fatalf("expected = %q, want %q", got[0].Expected, want)
	}
}

// A scenario whose every outcome looks back has nothing left to judge, so it
// is not worth a run.
func TestFilterDropsAScenarioWithOnlyRelativeOutcomes(t *testing.T) {
	t.Parallel()

	raw := []plannedScenario{{
		Name:     "Toggle twice",
		Priority: "high",
		Steps:    []string{"Tap the toggle"},
		Expected: []string{"The badge increments", "The row is no longer shown"},
	}}
	if got := filterScenarios(raw, explore.PlanRequest{Map: &explore.UIMap{}, Budget: 4}, "normal"); len(got) != 0 {
		t.Fatalf("scenarios = %d, want none", len(got))
	}
}

// An outcome that names what must be on the screen survives, including one
// that happens to contain the word change as part of the app's own text.
func TestFilterKeepsOutcomesAboutTheFinalScreen(t *testing.T) {
	t.Parallel()

	raw := []plannedScenario{{
		Name:     "Open settings",
		Priority: "high",
		Steps:    []string{"Tap settings"},
		Expected: []string{"A row labelled 'Change password' is shown", "The title reads 'Settings'"},
	}}
	got := filterScenarios(raw, explore.PlanRequest{Map: &explore.UIMap{}, Budget: 4}, "normal")
	if len(got) != 1 || len(got[0].Expected) != 2 {
		t.Fatalf("expected = %q, want both kept", got)
	}
}

// Three more wordings the guard let through, each taken verbatim from a
// session that ran WITH the guard in place. Every phrase below was checked
// against all forty outcomes the sessions have filed and catches only
// backward-looking ones.
//
//	mmx61  The Tuesday - Sep 1, 2026 day timeline view is restored
//	mmx66  ... indicated by the absence of the 2026 year header ...,
//	       replaced by inbox content
//	mmx67  No new row is added to the 'My Lists' section; ...
//
// "restored", "replaced", "absence" and "new" all name the screen before as
// plainly as "no longer" does: the judge is handed the final screen and
// cannot tell a row that was just added from one that was always there.
// mmx67's own tester note and the judge's evidence disagreed about exactly
// that -- the tester wrote "the mmx67 row was pre-existing test data" and
// the judge wrote "the mmx67 tag added by the tester".
func TestFilterDropsTheWordingsThatSurvivedTheFirstList(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{
		"The Tuesday - Sep 1, 2026 day timeline view is restored",
		"The Contacts screen is replaced by the previous screen.",
		"The inbox screen is displayed, indicated by the absence of the 2026 year header",
		"No new row is added to the 'My Lists' section",
	} {
		raw := []plannedScenario{{
			Name:     "A scenario",
			Priority: "high",
			Steps:    []string{"Tap something"},
			Expected: []string{outcome, "The title reads 'Reminders'"},
		}}
		got := filterScenarios(raw, explore.PlanRequest{Map: &explore.UIMap{}, Budget: 4}, "normal")
		if len(got) != 1 {
			t.Fatalf("%q: scenarios = %d, want 1", outcome, len(got))
		}
		if want := []string{"The title reads 'Reminders'"}; !reflect.DeepEqual(got[0].Expected, want) {
			t.Errorf("%q survived: expected = %q", outcome, got[0].Expected)
		}
	}
}
