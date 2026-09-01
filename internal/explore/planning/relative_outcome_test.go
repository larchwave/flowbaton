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
