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
		context.Background(), llm, "the list is visible", &explore.ScreenState{})
	if !check.Met {
		t.Fatalf("check = %+v, want met", check)
	}
	if strings.Contains(check.Evidence, "unreadable") {
		t.Fatalf("evidence = %q, want the model's evidence", check.Evidence)
	}
}
