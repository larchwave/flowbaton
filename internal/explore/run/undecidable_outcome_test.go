package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// The facts the judge gets are text: roles, labels, identifiers, driver
// checks. An outcome about colour or shape cannot be answered from them, and
// the judge answered anyway: two of the three defects in the mmx57 calendar
// report turn on a "red filled-circle selection highlight" the table cannot
// carry, filed as [High] not observed.
//
// Measured against MiniMax-M3 on the mmx57 facts, four calls per cell:
// without the paragraph the visual outcome came back not met 4/4; with it,
// undecidable 4/4, while an outcome the table confirms stayed met 4/4 and
// one it contradicts stayed not met 4/4.
func TestAskWorkerOutcomeTellsTheJudgeWhatTheFactsCannotShow(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met": false, "undecidable": true, "evidence": "the facts carry no colour"}`),
	}}
	check := askWorkerOutcome(context.Background(), llm,
		"the day cell shows the red filled-circle selection highlight",
		judgeFacts{Final: &explore.ScreenState{}})
	if check.Met {
		t.Fatal("an undecidable outcome must not read as met")
	}
	if check.Missed != explore.MissUnjudged {
		t.Fatalf("missed = %q, want %q", check.Missed, explore.MissUnjudged)
	}
	prompt := llm.requests[0].Messages[1].Text
	for _, want := range []string{"do not carry colour", "undecidable"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

// A fact gap outranks a judgement about the app: a reply that sets both says
// it could not look, which is not the same as the app not promising it.
func TestAskWorkerOutcomePrefersUndecidableOverUnpromised(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met": false, "inapplicable": true, "undecidable": true, "evidence": "no colour in the facts"}`),
	}}
	check := askWorkerOutcome(context.Background(), llm, "the row is green",
		judgeFacts{Final: &explore.ScreenState{}})
	if check.Missed != explore.MissUnjudged {
		t.Fatalf("missed = %q, want %q", check.Missed, explore.MissUnjudged)
	}
}
