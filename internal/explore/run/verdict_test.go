package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Real models wrap a JSON-only reply in a markdown fence even when told not
// to (seen live with gpt-4o on 2026-08-11); the verdict must still parse.
func TestAskWorkerOutcomeReadsAFencedReply(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{
		textReply("```json\n{\"met\": true, \"evidence\": \"the list is visible\"}\n```"),
	}}
	check := askWorkerOutcome(
		context.Background(), llm, "the list is visible", &explore.ScreenState{}, nil, nil)
	if !check.Met {
		t.Fatalf("check = %+v, want met", check)
	}
	if strings.Contains(check.Evidence, "unreadable") {
		t.Fatalf("evidence = %q, want the model's evidence", check.Evidence)
	}
}

// The judge sees only the final table, and a weaker model contradicted that
// table on a live session (2026-08-28). The tester's check_visible results
// are driver facts, so they go to the judge as well.
func TestAskWorkerOutcomeHandsDriverChecksToTheJudge(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met": true, "evidence": "ok"}`),
	}}
	checks := []explore.OutcomeCheck{
		{Expected: `visible: text "mmx7 morning reminder"`, Met: true, Evidence: `matched 1 element(s), first: "mmx7 morning reminder"`},
		{Expected: `visible: text "No Reminders"`, Evidence: "no matching element in the current tree"},
	}
	askWorkerOutcome(context.Background(), llm, "the reminder is listed", &explore.ScreenState{}, checks, nil)
	prompt := llm.requests[0].Messages[1].Text
	for _, want := range []string{
		"Driver checks measured on this final screen",
		`- visible: text "mmx7 morning reminder": true (matched 1 element(s), first: "mmx7 morning reminder")`,
		`- visible: text "No Reminders": false (no matching element in the current tree)`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(strings.Split(prompt, "Driver checks")[0], "Reply with only") {
		t.Fatalf("checks come after the reply instruction:\n%s", prompt)
	}
}

// A planned outcome may say "the title that was entered" (live, 2026-08-28);
// a judge that does not know what was typed denied a row that carried it.
// The run's unmasked input text goes to the judge; masked text never does.
func TestAskWorkerOutcomeHandsTypedTextToTheJudge(t *testing.T) {
	t.Parallel()
	steps := []explore.StepRecord{
		{Action: explore.Action{Kind: explore.ActionTap}},
		{Action: explore.Action{Kind: explore.ActionInput, Text: "mmx14 Test Reminder"}},
		{Action: explore.Action{Kind: explore.ActionInput, Text: explore.MaskedText, Masked: true}},
	}
	typed := typedTexts(steps)
	if len(typed) != 1 || typed[0] != "mmx14 Test Reminder" {
		t.Fatalf("typed = %q", typed)
	}
	llm := &scriptedLLM{replies: []explore.Message{textReply(`{"met": true, "evidence": "ok"}`)}}
	askWorkerOutcome(context.Background(), llm, "a row bearing the title that was entered", &explore.ScreenState{}, nil, typed)
	prompt := llm.requests[0].Messages[1].Text
	if !strings.Contains(prompt, "Text typed during the run") || !strings.Contains(prompt, `- "mmx14 Test Reminder"`) {
		t.Fatalf("prompt lacks the typed text:\n%s", prompt)
	}
}

// A weak planner writes outcomes the app never promises ("Completed tile
// visible and selected" on iOS tiles, session mmx21). The judge says so with
// an explicit flag; a reply without the flag stays a defect.
func TestAskWorkerOutcomeMarksAnInapplicableExpectation(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met": false, "inapplicable": true, "evidence": "no tile carries a selected state"}`),
		textReply(`{"met": false, "evidence": "the row still says Not Done"}`),
	}}
	marked := askWorkerOutcome(
		context.Background(), llm, "the Completed tile is selected", &explore.ScreenState{}, nil, nil)
	if marked.Met || !marked.Inapplicable {
		t.Fatalf("check = %+v, want unmet and inapplicable", marked)
	}
	plain := askWorkerOutcome(
		context.Background(), llm, "the row reads Done", &explore.ScreenState{}, nil, nil)
	if plain.Met || plain.Inapplicable {
		t.Fatalf("check = %+v, want unmet and applicable", plain)
	}
	if !strings.Contains(llm.requests[0].Messages[1].Text, "inapplicable") {
		t.Fatalf("prompt never asks for the flag:\n%s", llm.requests[0].Messages[1].Text)
	}
}
