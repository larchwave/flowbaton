package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// mmx85's judge asked for something the run was holding: "The driver facts
// do not specify which button was tapped, so it cannot be confirmed whether
// the interface associated with that button is now open." It gets the screens
// the run visited and the text it typed, and nothing about what it DID.
//
// The step log has named the resolved target since c14b63d, which is the
// shape the question wants: not "e13" but "id inbox-button".
func TestTheJudgeIsToldWhatTheRunDid(t *testing.T) {
	t.Parallel()

	lines := actionLines([]explore.StepRecord{
		{Index: 0, Status: explore.StepOK, Action: explore.Action{
			Kind:   explore.ActionTap,
			Target: &explore.Locator{Kind: explore.LocatorID, Value: "inbox-button"},
		}},
		{Index: 1, Status: explore.StepOK, Action: explore.Action{
			Kind: explore.ActionScroll, Direction: "down",
		}},
	})
	if !strings.Contains(lines, "inbox-button") {
		t.Fatalf("actions = %q, want the element the run tapped", lines)
	}
	if !strings.Contains(lines, "scroll") {
		t.Fatalf("actions = %q, want the screen-level action too", lines)
	}
}

// A failed step did not happen. Listing it as something the run did would
// invite the judge to credit an action the device refused.
func TestAFailedStepIsNotSomethingTheRunDid(t *testing.T) {
	t.Parallel()

	lines := actionLines([]explore.StepRecord{
		{Index: 0, Status: explore.StepFailed, Action: explore.Action{
			Kind:   explore.ActionTap,
			Target: &explore.Locator{Kind: explore.LocatorID, Value: "never-tapped"},
		}},
	})
	if strings.Contains(lines, "never-tapped") {
		t.Fatalf("actions = %q, want a refused step left out", lines)
	}
}

// A run that did nothing has no action block, rather than an empty heading.
func TestNoActionBlockForARunThatDidNothing(t *testing.T) {
	t.Parallel()

	if lines := actionLines(nil); lines != "" {
		t.Fatalf("actions = %q, want nothing", lines)
	}
}
