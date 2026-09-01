package export_test

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/explore/export"
)

// A flow that only launches the app asserts nothing and passes on replay
// whatever the app does, so it is a green that means nothing -- and it goes
// on the same replay tally as a flow that tests something.
//
// Six of seventy exported flows are launch-only, three of them from mmx69,
// where one came from a scenario whose every device call failed on a dead
// iOS runner and was still called passed. The judge's verdict is a separate
// question; a flow with no action in it is one the exporter can refuse to
// write, and the caller already treats a refusal as a warning it prints and
// skips.
func TestExportRefusesAFlowWithNoAction(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{Name: "Switch to inbox from footer"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{
			// Every call failed against a dead runner.
			{Index: 0, Status: explore.StepFailed, Action: explore.Action{
				Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorID, Value: "inbox"}}},
			// A wait is dropped by the exporter anyway.
			{Index: 1, Status: explore.StepOK, Action: explore.Action{Kind: explore.ActionWait}},
		},
	}
	if _, err := (export.Exporter{}).ExportFlow(result, "com.example.app"); err == nil {
		t.Fatal("a flow with nothing but launchApp was written")
	} else if !strings.Contains(err.Error(), "no action") {
		t.Errorf("error = %v, want it to say the run has no action", err)
	}
}

// One real action is enough: the flow tests something.
func TestExportWritesAFlowWithOneAction(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{Name: "Open the add-new-item action"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{
			{Index: 0, Status: explore.StepOK, Action: explore.Action{
				Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorID, Value: "add-plus-button"}}},
		},
	}
	data, err := (export.Exporter{}).ExportFlow(result, "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "add-plus-button") {
		t.Errorf("flow = %s", data)
	}
}
