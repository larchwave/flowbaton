package export

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/flow"
)

// Naming the start screen tells a reader where the recording began; walking
// there makes the flow run. The navigator already walks it before every
// scenario whose start screen the relaunch does not produce, and the steps
// it took are the prefix the flow was missing.
func TestFlowWalksToTheScreenItWasRecordedOn(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{
			Name:        "Switch to single-day agenda",
			StartScreen: "august-single-day-07328620",
		},
		Status: explore.TestPassed,
		Prelude: []explore.StepRecord{{
			Index: 1, Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{
				Kind: explore.LocatorID, Value: "today-button",
			}},
		}},
		Steps: []explore.StepRecord{{
			Index: 1, Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{
				Kind: explore.LocatorID, Value: "toggle-day-list-view",
			}},
		}},
	}
	data, err := Exporter{}.ExportFlow(result, "com.apple.mobilecal")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	walked := strings.Index(body, "today-button")
	ran := strings.Index(body, "toggle-day-list-view")
	if walked < 0 {
		t.Fatalf("the flow does not walk to its start screen:\n%s", body)
	}
	if walked > ran {
		t.Fatalf("the walk comes after the run it was meant to set up:\n%s", body)
	}
	// The comment told a human to set the screen up by hand. The flow now
	// does it, so the instruction would be stale advice.
	if strings.Contains(body, "launchApp does not navigate there") {
		t.Fatalf("the flow still asks the reader to navigate for it:\n%s", body)
	}
	if _, err := flow.ParseBytes("exported.yaml", data); err != nil {
		t.Fatalf("the flow no longer parses: %v\n%s", err, data)
	}
}

// A walk is not the run's work. A flow whose only actions came from reaching
// the start screen asserts nothing about the scenario and must be refused
// the same as a launch-only flow.
func TestAWalkAloneIsNotAFlow(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{Name: "Anything", StartScreen: "somewhere-1234abcd"},
		Status:   explore.TestPassed,
		Prelude: []explore.StepRecord{{
			Index: 1, Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{
				Kind: explore.LocatorID, Value: "today-button",
			}},
		}},
	}
	if _, err := (Exporter{}).ExportFlow(result, "com.example.app"); err == nil {
		t.Fatal("ExportFlow() = nil error, want a refusal for a flow with no action of its own")
	}
}
