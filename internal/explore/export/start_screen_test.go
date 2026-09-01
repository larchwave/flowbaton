package export

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/flow"
)

// A flow replays from wherever launchApp leaves the app, which is not
// necessarily the screen the scenario was planned against: replaying the
// flows of three sessions on the simulator, two of thirteen failed on their
// first action -- mmx56 flow-01 taps text a previous scenario had typed,
// mmx58 flow-01 taps a toolbar button that only exists in the day view. The
// flow should at least say where it was recorded, so the failure reads as a
// starting point rather than a missing element.
func TestFlowNamesTheScreenItWasRecordedOn(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{
			Name:        "Switch to single-day agenda",
			StartScreen: "august-single-day-07328620",
		},
		Status: explore.TestPassed,
		Steps: []explore.StepRecord{{
			Index:  1,
			Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{
				Kind: explore.LocatorID, Value: "toggle-day-list-view",
			}},
		}},
	}
	data, err := Exporter{}.ExportFlow(result, "com.apple.mobilecal")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "august-single-day-07328620") {
		t.Fatalf("flow does not name its start screen:\n%s", data)
	}
	if _, err := flow.ParseBytes("exported.yaml", data); err != nil {
		t.Fatalf("the flow no longer parses: %v\n%s", err, data)
	}
}

// A result with no start screen recorded emits no line about one.
func TestFlowSaysNothingWithoutAStartScreen(t *testing.T) {
	t.Parallel()

	result := &explore.TestResult{
		Scenario: explore.Scenario{Name: "Anything"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{{
			Index: 1, Status: explore.StepOK,
			Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{
				Kind: explore.LocatorID, Value: "x",
			}},
		}},
	}
	data, err := Exporter{}.ExportFlow(result, "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "recorded on") {
		t.Fatalf("flow mentions a start screen it does not have:\n%s", data)
	}
}
