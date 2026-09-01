package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// mmx74 spent a scenario toggling between the month view and the list view
// with a dead scroll between each pair, and neither detector saw it: the
// stall count broke on the toggles, which changed the screen, and the cycle
// count broke on the scrolls, which did not. A step that fails or changes
// nothing is not a way out of a cycle, so it must not end the count.
func TestACycleSurvivesTheDeadStepsInsideIt(t *testing.T) {
	t.Parallel()

	month := explore.ScreenSignature{AppID: "cal", TreeDigest: "month"}
	list := explore.ScreenSignature{AppID: "cal", TreeDigest: "list"}
	// toggle -> list, toggle -> month, scroll changed nothing, twice over.
	steps := []explore.StepRecord{
		{Status: explore.StepOK, Before: month, After: list},
		{Status: explore.StepOK, Before: list, After: month},
		{Status: explore.StepNoChange, Before: month, After: month},
		{Status: explore.StepOK, Before: month, After: list},
		{Status: explore.StepOK, Before: list, After: month},
		{Status: explore.StepNoChange, Before: month, After: month},
	}
	if cycle := trailingCycle(steps); cycle != len(steps) {
		t.Fatalf("trailingCycle() = %d, want all %d steps of the loop", cycle, len(steps))
	}
}

// Dead steps alone are a stall, which the stall detector owns. The cycle
// detector must not claim them: no pair of screens is being walked.
func TestDeadStepsAloneAreNotACycle(t *testing.T) {
	t.Parallel()

	here := explore.ScreenSignature{AppID: "cal", TreeDigest: "month"}
	steps := []explore.StepRecord{
		{Status: explore.StepNoChange, Before: here, After: here},
		{Status: explore.StepFailed, Before: here, After: here},
	}
	if cycle := trailingCycle(steps); cycle != 0 {
		t.Fatalf("trailingCycle() = %d, want 0 for a stall with no pair walked", cycle)
	}
}

// A run that reached a third screen is making progress, and the count stops
// at the step that left the pair.
func TestACycleStopsAtTheStepThatLeftIt(t *testing.T) {
	t.Parallel()

	month := explore.ScreenSignature{AppID: "cal", TreeDigest: "month"}
	list := explore.ScreenSignature{AppID: "cal", TreeDigest: "list"}
	detail := explore.ScreenSignature{AppID: "cal", TreeDigest: "detail"}
	steps := []explore.StepRecord{
		{Status: explore.StepOK, Before: detail, After: month},
		{Status: explore.StepOK, Before: month, After: list},
		{Status: explore.StepOK, Before: list, After: month},
	}
	if cycle := trailingCycle(steps); cycle != 2 {
		t.Fatalf("trailingCycle() = %d, want only the 2 steps inside the pair", cycle)
	}
}
