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
		context.Background(), llm, "the list is visible", &explore.ScreenState{}, nil)
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
	askWorkerOutcome(context.Background(), llm, "the reminder is listed", &explore.ScreenState{}, checks)
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
