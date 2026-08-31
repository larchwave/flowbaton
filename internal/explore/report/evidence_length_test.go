package report

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// The judge is asked for one line and writes one: what it saw, then why it
// does not meet the outcome. The report cut that line at 160 characters,
// which lands in the middle of the reason. Session mmx56 printed
//
//	Evidence: The search screen was dismissed (toolbar-search-32d1546f was
//	visited before returning to contacts), but the expected outcome states
//	the search screen is dismiss…
//
// and an operator deciding whether that defect is real needs the half that
// was cut.
func TestJudgeEvidenceKeepsAWholeLine(t *testing.T) {
	t.Parallel()

	const evidence = "The search screen was dismissed (toolbar-search-32d1546f was visited " +
		"before returning to contacts), but the expected outcome states the search screen " +
		"is dismissed and the previous screen is displayed, which the final screen does not show."
	if len(evidence) <= 160 {
		t.Fatalf("the sample is %d characters, too short to test the bound", len(evidence))
	}
	got := judgeEvidence(&explore.OutcomeCheck{Evidence: evidence})
	if !strings.HasSuffix(got, "does not show.") {
		t.Fatalf("evidence was cut: %q", got)
	}
	if got != evidenceLine(explore.TestResult{}, &explore.OutcomeCheck{Evidence: evidence}) {
		t.Fatal("the two evidence lines of one report disagree on length")
	}
}

// A model that answers with a paragraph is still bounded: the line is one
// line of a report.
func TestJudgeEvidenceStillBoundsARunawayLine(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("the screen did not settle and then it did, ", 40)
	if got := judgeEvidence(&explore.OutcomeCheck{Evidence: long}); len(got) >= len(long) {
		t.Fatalf("a runaway line was not bounded: %d characters", len(got))
	}
}
