package explore

import (
	"strings"
	"testing"
)

// A step log that says a tap went to "e13" names an index into a table
// nobody kept. Three sessions lost the app from the foreground right after
// an index tap and the log could not say which element the index was, so
// the question stayed open. The record already holds the resolved locator.
func TestAStepLineNamesTheElementItActedOn(t *testing.T) {
	t.Parallel()

	line := StepLine(StepRecord{
		Index:  0,
		Status: StepOK,
		Action: Action{Kind: ActionTap, Text: "e13", Target: &Locator{
			Kind: LocatorID, Value: "add-plus-button",
		}},
	})
	if !strings.Contains(line, "add-plus-button") {
		t.Fatalf("StepLine() = %q, want the element the tap resolved to", line)
	}
}

// A point locator says where and never what, which is why it carries a
// label. Rendering the coordinate without it names nothing.
func TestAPointStepLineCarriesItsLabel(t *testing.T) {
	t.Parallel()

	line := StepLine(StepRecord{
		Index:  0,
		Status: StepOK,
		Action: Action{Kind: ActionTap, Text: "e50", Target: &Locator{
			Kind: LocatorPoint, Value: "200,860", Label: "Dictate",
		}},
	})
	if !strings.Contains(line, "Dictate") {
		t.Fatalf("StepLine() = %q, want the label of the point it tapped", line)
	}
	if !strings.Contains(line, "200,860") {
		t.Fatalf("StepLine() = %q, want the point it tapped", line)
	}
}

// A screen-level action has no target and must not grow an empty one.
func TestAStepLineWithoutATargetSaysNothingAboutOne(t *testing.T) {
	t.Parallel()

	line := StepLine(StepRecord{
		Index: 0, Status: StepOK,
		Action: Action{Kind: ActionSwipe, Direction: "up"},
	})
	if strings.Contains(line, "->") {
		t.Fatalf("StepLine() = %q, want no target on a screen-level action", line)
	}
}
