package cli

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// A passing verdict left no trace anywhere: the report prints evidence only
// for findings, so nobody could ask what a pass was granted for. The step log
// is where a run is audited, so the verdict belongs beside the steps.
func TestStepLogRecordsWhatEachVerdictWasGrantedFor(t *testing.T) {
	report := &explore.SessionReport{Results: []explore.TestResult{{
		Scenario: explore.Scenario{Name: "Open the inbox"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{{
			Index:  0,
			Action: explore.Action{Kind: explore.ActionTap, Text: "e3"},
			Status: explore.StepOK,
		}},
		Outcomes: []explore.OutcomeCheck{
			{
				Expected: "the inbox screen opens",
				Met:      true,
				Evidence: "the final screen shows the Inbox title and two invitation rows",
			},
			{
				Expected: "a badge count shows",
				Evidence: "no element carries a count",
			},
			{
				Expected: "check_visible inbox-button",
				Met:      true,
				Driver:   true,
				Evidence: "present",
			},
		},
	}}}
	markdown := stepLogMarkdown(report)
	for _, want := range []string{
		`- met "the inbox screen opens": the final screen shows the Inbox title`,
		`- not met "a badge count shows": no element carries a count`,
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("step log missing %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, "check_visible") {
		t.Fatalf("step log filed a driver probe as a verdict:\n%s", markdown)
	}
}
