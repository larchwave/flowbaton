package cli

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// The tester has a note tool -- "Record a short free-text finding" -- and
// what it recorded reached explore.TestResult.Notes and stopped there: no
// report, no artifact, no consumer anywhere in the tree. The step log is
// where the tool calls are already kept, so the findings go there too.
func TestStepLogKeepsWhatTheTesterNoted(t *testing.T) {
	t.Parallel()

	report := &explore.SessionReport{Results: []explore.TestResult{{
		Scenario: explore.Scenario{Name: "Open search"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{{
			Index: 0, Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap},
		}},
		Notes: []string{"the row vanished after the second tap", "back is unsupported on this platform"},
	}}}
	log := stepLogMarkdown(report)
	for _, want := range []string{"the row vanished after the second tap", "back is unsupported"} {
		if !strings.Contains(log, want) {
			t.Fatalf("step log dropped a note:\n%s", log)
		}
	}
}

// A run that noted nothing gets no empty heading.
func TestStepLogSaysNothingWithoutNotes(t *testing.T) {
	t.Parallel()

	report := &explore.SessionReport{Results: []explore.TestResult{{
		Scenario: explore.Scenario{Name: "Open search"},
		Status:   explore.TestPassed,
	}}}
	if strings.Contains(stepLogMarkdown(report), "note") {
		t.Fatalf("step log mentions notes it does not have:\n%s", stepLogMarkdown(report))
	}
}
