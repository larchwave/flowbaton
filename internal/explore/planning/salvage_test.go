package planning

import (
	"context"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// brokenReply is the shape MiniMax-M3 produced live on 2026-09-02: two
// well-formed scenarios, then a last one that closes its expected array with
// "}" before "]". The object is undecodable from that byte on, so every
// correct scenario before it died with it and the session ended at planning.
func brokenReply() string {
	return `{"scenarios":[` +
		entry("Open search", "high") + `,` +
		entry("Open the calendar list", "normal") + `,` +
		`{"name":"Open the inbox","priority":"high","steps":["Tap the button labeled inbox."],` +
		`"expected":["The inbox screen is displayed."}]}`
}

func TestPlanNextKeepsTheScenariosABrokenReplyDidCarry(t *testing.T) {
	llm := &scriptedLLM{replies: []string{brokenReply(), brokenReply()}}
	planner := &Planner{LLM: llm}
	scenarios, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("got %d scenarios, want the two the reply carried whole", len(scenarios))
	}
	if scenarios[0].Name != "Open search" || scenarios[1].Name != "Open the calendar list" {
		t.Fatalf("names %q and %q", scenarios[0].Name, scenarios[1].Name)
	}
}

func TestPlanNextStillFailsWhenNothingSurvives(t *testing.T) {
	garbage := `{"scenarios":[{"name":`
	llm := &scriptedLLM{replies: []string{garbage, garbage}}
	planner := &Planner{LLM: llm}
	if _, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 5,
	}); err == nil {
		t.Fatal("want an error when the reply carries no whole scenario")
	}
}
