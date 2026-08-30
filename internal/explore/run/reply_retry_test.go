package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// A reply cut off mid-object killed three of five live sessions on
// 2026-08-30. Supervision fails closed, so the pilot turns one truncated
// reply into an aborted scenario -- the run is thrown away over a cut
// cable rather than anything the app did.
func TestPilotAsksAgainWhenTheOrderIsCutOff(t *testing.T) {
	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"decision":"cont`),
		textReply(`{"decision":"continue","instruction":"try the other tab"}`),
	}}
	supervisor := newPilot(llm, explore.Scenario{Name: "Add a contact"})
	order, err := supervisor.review(context.Background(), []string{"1 tap ok"}, false)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if order.Decision != "continue" || order.Instruction != "try the other tab" {
		t.Fatalf("order = %+v", order)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want the truncated order asked again once", len(llm.requests))
	}
	// The pilot's own conversation must carry the exchange it kept, not the
	// reply it rejected; a stale half-object would poison every later turn.
	if len(supervisor.messages) != 3 {
		t.Fatalf("pilot kept %d messages, want the seed plus one exchange", len(supervisor.messages))
	}
	if got := supervisor.messages[2].Text; got != `{"decision":"continue","instruction":"try the other tab"}` {
		t.Fatalf("pilot kept the wrong reply: %q", got)
	}
}

// The judge writes its reason into the report, so a truncated reply does not
// abort the run -- it silently downgrades one outcome to unjudged, which
// reads in the report exactly like a model that had nothing to say.
func TestOutcomeJudgeAsksAgainWhenTheVerdictIsCutOff(t *testing.T) {
	llm := &scriptedLLM{replies: []explore.Message{
		textReply(`{"met":true,"evidence":"the co`),
		textReply(`{"met":true,"evidence":"the contact row is on screen"}`),
	}}
	check := askWorkerOutcome(context.Background(), llm, "the contact appears", judgeFacts{Final: screenOfLabels("Ada Lovelace")})
	if !check.Met {
		t.Fatalf("check = %+v, want the second verdict read", check)
	}
	if check.Evidence != "the contact row is on screen" {
		t.Fatalf("evidence = %q", check.Evidence)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("requests = %d, want the truncated verdict asked again once", len(llm.requests))
	}
}

// Two unreadable replies are the model's answer, and the report has to say
// which of the two ways the judge failed: a provider that is down sends an
// operator somewhere else entirely.
func TestOutcomeJudgeSeparatesAnUnreadableVerdictFromADeadProvider(t *testing.T) {
	unreadable := &scriptedLLM{replies: []explore.Message{textReply("no"), textReply("still no")}}
	check := askWorkerOutcome(context.Background(), unreadable, "the contact appears", judgeFacts{Final: screenOfLabels("Ada Lovelace")})
	if check.Met || check.Missed != explore.MissUnjudged {
		t.Fatalf("check = %+v", check)
	}
	if !strings.HasPrefix(check.Evidence, "unreadable outcome reply: ") {
		t.Fatalf("evidence = %q", check.Evidence)
	}
	dead := &scriptedLLM{}
	check = askWorkerOutcome(context.Background(), dead, "the contact appears", judgeFacts{Final: screenOfLabels("Ada Lovelace")})
	if !strings.HasPrefix(check.Evidence, "outcome check unavailable: ") {
		t.Fatalf("evidence = %q", check.Evidence)
	}
}
