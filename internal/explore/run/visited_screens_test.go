package run

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func signature(salient string, digest string) explore.ScreenSignature {
	return explore.ScreenSignature{AppID: "app", Salient: []string{salient}, TreeDigest: digest}
}

// A scenario whose expectation names a state the run PASSES THROUGH was
// judged against the final screen alone and filed as a product defect.
// Live mmx51 (Contacts, 2026-08-31): the run tapped Add, typed into First
// name and Last name, then tapped Done -- and the report says "[High] not
// observed: A new contact creation screen is shown" three lines above a
// reproduce list that taps "First name". The screens the run visited are
// driver facts and belong with the other facts the judge is handed.
func TestVisitedScreensCollapseRepeatsAndKeepOrder(t *testing.T) {
	t.Parallel()

	list := signature("Contacts", "aaaa1111")
	create := signature("New Contact", "bbbb2222")
	steps := []explore.StepRecord{
		{Before: list, After: create},
		{Before: create, After: create},
		{Before: create, After: list},
	}
	got := visitedScreens(steps)
	want := []string{list.Key(), create.Key(), list.Key()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("visitedScreens = %q, want %q", got, want)
	}
}

// A step whose screen was never captured carries a zero signature; naming
// it would tell the judge the run visited a screen with no digest at all.
func TestVisitedScreensSkipUncapturedScreens(t *testing.T) {
	t.Parallel()

	list := signature("Contacts", "aaaa1111")
	steps := []explore.StepRecord{
		{Before: list, After: explore.ScreenSignature{}},
		{Before: explore.ScreenSignature{}, After: list},
	}
	if got := visitedScreens(steps); !reflect.DeepEqual(got, []string{list.Key()}) {
		t.Fatalf("visitedScreens = %q, want just the captured screen", got)
	}
}

func TestAskWorkerOutcomeHandsVisitedScreensToTheJudge(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{textReply(`{"met": true, "evidence": "ok"}`)}}
	askWorkerOutcome(context.Background(), llm, "a new contact creation screen is shown", judgeFacts{
		Final:   &explore.ScreenState{},
		Visited: []string{"contacts-aaaa", "new-contact-bbbb", "contacts-aaaa"},
	})
	prompt := llm.requests[0].Messages[1].Text
	for _, want := range []string{
		"Screens the run visited, in order (driver facts)",
		"1. contacts-aaaa",
		"2. new-contact-bbbb",
		"3. contacts-aaaa",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(strings.Split(prompt, "Screens the run visited")[0], "Reply with only") {
		t.Fatalf("visited screens come after the reply instruction:\n%s", prompt)
	}
}

// A run that never left one screen tells the judge nothing it does not
// already see, so the block stays out of the prompt entirely.
func TestAskWorkerOutcomeOmitsVisitedScreensWhenTheRunStayedPut(t *testing.T) {
	t.Parallel()

	llm := &scriptedLLM{replies: []explore.Message{textReply(`{"met": true, "evidence": "ok"}`)}}
	askWorkerOutcome(context.Background(), llm, "the list is visible", judgeFacts{
		Final:   &explore.ScreenState{},
		Visited: []string{"contacts-aaaa"},
	})
	if prompt := llm.requests[0].Messages[1].Text; strings.Contains(prompt, "Screens the run visited") {
		t.Fatalf("prompt names a single screen as a journey:\n%s", prompt)
	}
}
