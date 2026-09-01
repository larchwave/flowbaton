package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// mmx69 reported "Switch to inbox from footer" as passed. Its eighteen
// device calls all failed against a dead iOS runner, and it exported a flow
// whose one command was launchApp. Two more scenarios in the same session
// passed having executed no step at all.
//
// The judge is handed the FINAL screen, and it was right about it: the
// screen it read did satisfy the outcome. It cannot tell a screen the run
// produced from one that was already there, which is the same blindness the
// backward-looking outcome list exists for. Whether a run did anything is
// not a question for the judge -- the step log answers it.
//
// A no-change step counts as having run: the action landed and the screen
// stood still, which is exactly the evidence a scenario like "an empty name
// is refused" is looking for.
func TestNothingRanReadsTheStepLog(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		steps []explore.StepRecord
		want  bool
	}{
		{"no steps at all", nil, true},
		{"every call failed", []explore.StepRecord{
			{Status: explore.StepFailed}, {Status: explore.StepFailed}}, true},
		{"one landed", []explore.StepRecord{
			{Status: explore.StepFailed}, {Status: explore.StepOK}}, false},
		{"one landed and changed nothing", []explore.StepRecord{
			{Status: explore.StepFailed}, {Status: explore.StepNoChange}}, false},
		{"one recovered", []explore.StepRecord{{Status: explore.StepRecov}}, false},
	} {
		if got := nothingRan(test.steps); got != test.want {
			t.Errorf("%s: nothingRan = %v, want %v", test.name, got, test.want)
		}
	}
}

// The status the rule actually changes: a model that finishes "passed" on a
// screen it never touched.
func TestRunScenarioWillNotPassARunThatDidNothing(t *testing.T) {
	home := makeState("com.example.app", screen("Inbox", button("Close", "close_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	worker := &scriptedLLM{replies: []explore.Message{
		toolCall("1", "finish", `{"status":"passed","outcomes":[{"expected":"The inbox is shown","met":true,"evidence":"Inbox title"}]}`),
		textReply(`{"met": true, "evidence": "the Inbox title is on the final screen"}`),
	}}
	tester := newTester(driver, observer, worker, nil, testConfig())
	result, err := tester.RunScenario(context.Background(),
		explore.Scenario{Name: "Switch to inbox", Expected: []string{"The inbox is shown"}}, home)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != explore.TestStopped {
		t.Fatalf("status %s, verdict %q", result.Status, result.Verdict)
	}
	if !strings.Contains(result.Verdict, "no action ran") {
		t.Errorf("verdict %q", result.Verdict)
	}
}
