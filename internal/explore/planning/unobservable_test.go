package planning

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// The prompt has told the planner since mmx57 that the screen arrives as
// text and that colour, shape and highlighting cannot be checked. mmx84 lost
// three of twelve scenarios to outcomes about colour anyway -- the judge
// correctly answered "cannot confirm the red selected indicator colour" three
// times -- so the rule needs something enforcing it, the same way
// backwardLooking enforces the rule about the screen before the step.
//
// Every phrase below was checked against all 72 outcomes these sessions have
// filed. Six matched, all six unobservable, none a false positive.
func TestOutcomesAboutAppearanceAreDropped(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{
		"The 2027 year header appears in the accent color, and its month buttons",
		"the day cell shows the red filled-circle selection highlight",
		"The 'Continue' label appears as a blue filled button at the bottom",
		"Day cells for Tuesday, September 8 each display a dot indicator",
	} {
		if kept := checkableOutcomes([]string{outcome}); len(kept) != 0 {
			t.Fatalf("kept %q, want it dropped as unobservable", outcome)
		}
	}
}

// A bare colour word is NOT enough. An app may label a calendar "Red", and
// that label is text the checker can read.
func TestABareColourWordIsNotAnAppearanceClaim(t *testing.T) {
	t.Parallel()

	for _, outcome := range []string{
		"The calendar named Red is listed in the calendars sheet",
		"A row labelled 'Blue' appears under Subscribed",
	} {
		if kept := checkableOutcomes([]string{outcome}); len(kept) != 1 {
			t.Fatalf("dropped %q, want a label the checker can read to survive", outcome)
		}
	}
}

// The backward-looking rule still applies through the same filter.
func TestTheBackwardRuleStillApplies(t *testing.T) {
	t.Parallel()

	if kept := checkableOutcomes([]string{"the counter no longer shows 3"}); len(kept) != 0 {
		t.Fatalf("kept %v, want the backward outcome dropped", kept)
	}
	if kept := checkableOutcomes([]string{"the header reads 'Monday'"}); len(kept) != 1 {
		t.Fatalf("dropped a checkable outcome: %v", kept)
	}
}

// The dropped words must stay out of the prompt's own rule line, or the
// planner reads an example of what to write.
func TestThePromptStillTellsThePlannerWhatTheCheckerSees(t *testing.T) {
	t.Parallel()

	prompt := buildPrompt(explore.PlanRequest{Map: &explore.UIMap{}, Budget: 1}, builtinStyles[0], true, nil)
	if !strings.Contains(prompt, "cannot be checked") {
		t.Fatalf("prompt no longer states the rule:\n%s", prompt)
	}
}
