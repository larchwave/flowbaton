package report

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

type scriptedLLM struct {
	reply    explore.ChatResponse
	err      error
	requests []explore.ChatRequest
}

func (s *scriptedLLM) Chat(_ context.Context, request explore.ChatRequest) (explore.ChatResponse, error) {
	s.requests = append(s.requests, request)
	if s.err != nil {
		return explore.ChatResponse{}, s.err
	}
	return s.reply, nil
}

func failedResult(name string, priority explore.Priority, expected string) explore.TestResult {
	return explore.TestResult{
		Scenario: explore.Scenario{Name: name, Priority: priority},
		Status:   explore.TestFailed,
		Steps: []explore.StepRecord{
			{Index: 0, Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorText, Value: "Save"}}, Status: explore.StepOK},
			{Index: 1, Action: explore.Action{Kind: explore.ActionVerify, Text: expected}, Status: explore.StepFailed, Note: "the toast never appeared"},
		},
		Outcomes: []explore.OutcomeCheck{{Expected: expected, Met: false, Evidence: "screen kept the old value"}},
	}
}

func passedResult(name string) explore.TestResult {
	return explore.TestResult{
		Scenario: explore.Scenario{Name: name, Priority: explore.PriorityNormal},
		Status:   explore.TestPassed,
		Outcomes: []explore.OutcomeCheck{{Expected: "it works", Met: true}},
	}
}

func TestClusteringGroupsFailuresOnTheSameControl(t *testing.T) {
	session := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "android",
		Results: []explore.TestResult{
			failedResult("save from list", explore.PriorityNormal, "the Save toast appears"),
			failedResult("save from detail", explore.PriorityCritical, "The  save toast appears."),
			failedResult("save after edit", explore.PriorityHigh, "the save toast APPEARS"),
		},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if count := strings.Count(markdown, "- ["); count != 1 {
		t.Fatalf("want one clustered finding, got %d:\n%s", count, markdown)
	}
	for _, name := range []string{"save from list", "save from detail", "save after edit"} {
		if !strings.Contains(markdown, "`"+name+"`") {
			t.Errorf("finding is missing test %q:\n%s", name, markdown)
		}
	}
	// Highest priority in the cluster is critical, so the tag is High.
	if !strings.Contains(markdown, "- [High] the Save toast appears") {
		t.Errorf("want a single High finding titled by the first unmet outcome:\n%s", markdown)
	}
	if !strings.Contains(markdown, "Evidence: screen kept the old value") {
		t.Errorf("want the unmet outcome's evidence line:\n%s", markdown)
	}
	if !strings.Contains(markdown, "1. Tap \"Save\"") {
		t.Errorf("want repro steps from the step log:\n%s", markdown)
	}
}

func TestSeverityFollowsScenarioPriority(t *testing.T) {
	cases := []struct {
		priority explore.Priority
		want     string
	}{
		{explore.PriorityCritical, "High"},
		{explore.PriorityImportant, "High"},
		{explore.PriorityHigh, "Medium"},
		{explore.PriorityNormal, "Low"},
		{explore.PriorityLow, "Low"},
		{explore.Priority("odd"), "Low"},
	}
	for _, c := range cases {
		if got := severity(c.priority); got != c.want {
			t.Errorf("severity(%q) = %q, want %q", c.priority, got, c.want)
		}
	}
}

func TestEmptySectionsAreOmitted(t *testing.T) {
	passesOnly := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "ios",
		Results:  []explore.TestResult{passedResult("login")},
	}
	markdown, err := Analyst{}.Report(context.Background(), passesOnly)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !strings.Contains(markdown, "## What works") {
		t.Errorf("want a What works section:\n%s", markdown)
	}
	for _, absent := range []string{"## Defects", "## Execution issues"} {
		if strings.Contains(markdown, absent) {
			t.Errorf("section %q must be omitted when empty:\n%s", absent, markdown)
		}
	}

	failuresOnly := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "ios",
		Results:  []explore.TestResult{failedResult("save", explore.PriorityNormal, "saved state shows")},
	}
	markdown, err = Analyst{}.Report(context.Background(), failuresOnly)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(markdown, "## What works") {
		t.Errorf("What works must be omitted with no passing runs:\n%s", markdown)
	}
	if !strings.Contains(markdown, "## Defects") {
		t.Errorf("want a Defects section:\n%s", markdown)
	}
}

func TestStoppedAndDriverErrorRunsAreExecutionIssues(t *testing.T) {
	session := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "android",
		Results: []explore.TestResult{
			{
				Scenario: explore.Scenario{Name: "stopped run"},
				Status:   explore.TestStopped,
				Verdict:  "scenario does not apply here",
			},
			{
				Scenario: explore.Scenario{Name: "driver broke"},
				Status:   explore.TestFailed,
				Steps: []explore.StepRecord{{
					Index:   0,
					Action:  explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorText, Value: "Go"}},
					Status:  explore.StepFailed,
					ErrText: "device connection lost",
				}},
				Outcomes: []explore.OutcomeCheck{{Expected: "next screen shows", Met: false}},
			},
			{
				Scenario: explore.Scenario{Name: "gave up"},
				Status:   explore.TestFailed,
			},
		},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(markdown, "## Defects") {
		t.Errorf("execution problems must not become defects:\n%s", markdown)
	}
	if !strings.Contains(markdown, "## Execution issues") {
		t.Fatalf("want an Execution issues section:\n%s", markdown)
	}
	for _, want := range []string{
		"- `stopped run`: run stopped: scenario does not apply here",
		"- `driver broke`: driver error: device connection lost",
		"- `gave up`: ended without a product verdict",
	} {
		if !strings.Contains(markdown, want) {
			t.Errorf("missing execution issue line %q:\n%s", want, markdown)
		}
	}
}

func TestModelHeadlineLeadsTheReport(t *testing.T) {
	llm := &scriptedLLM{reply: explore.ChatResponse{
		Message: explore.Message{Role: explore.RoleAssistant, Text: "  Saving notes silently loses the edit.  "},
	}}
	session := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "android",
		Results:  []explore.TestResult{failedResult("save", explore.PriorityCritical, "the edit persists")},
	}
	markdown, err := Analyst{Manager: llm}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !strings.HasPrefix(markdown, "Saving notes silently loses the edit.\n") {
		t.Errorf("want the trimmed model headline first:\n%s", markdown)
	}
	if len(llm.requests) != 1 {
		t.Fatalf("want one model call, got %d", len(llm.requests))
	}
}

func TestModelFailureFallsBackToDeterministicMarkdown(t *testing.T) {
	llm := &scriptedLLM{err: errors.New("provider unavailable")}
	session := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "android",
		Results:  []explore.TestResult{passedResult("login")},
	}
	markdown, err := Analyst{Manager: llm}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report must survive a model failure: %v", err)
	}
	if !strings.HasPrefix(markdown, "com.example.app on android: 1 of 1 scenarios passed") {
		t.Errorf("want the deterministic headline:\n%s", markdown)
	}
}

func TestContextErrorsPropagate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	llm := &scriptedLLM{err: ctx.Err()}
	session := &explore.SessionReport{AppID: "com.example.app", Results: []explore.TestResult{passedResult("login")}}
	if _, err := (Analyst{Manager: llm}).Report(ctx, session); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestNilSessionIsAnError(t *testing.T) {
	if _, err := (Analyst{}).Report(context.Background(), nil); err == nil {
		t.Fatal("want an error for a nil session")
	}
}

func TestARunEndingOnAMissedTargetIsNeitherADefectNorADriverError(t *testing.T) {
	// A target that matches nothing is the agent aiming at an element the
	// screen does not have. Calling it a driver error blames the equipment;
	// promoting its unmet outcome to a defect invents a product bug out of a
	// wrong route. It is an execution issue, named for what happened.
	session := &explore.SessionReport{
		AppID:    "com.example.app",
		Platform: "ios",
		Results: []explore.TestResult{{
			Scenario: explore.Scenario{Name: "filter results", Priority: explore.PriorityNormal},
			Status:   explore.TestFailed,
			Steps: []explore.StepRecord{{
				Index:      0,
				Action:     explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorText, Value: "Filter"}},
				Status:     explore.StepFailed,
				ErrText:    `no element matched text "Filter" on the current screen`,
				TargetMiss: true,
			}},
			Outcomes: []explore.OutcomeCheck{{Expected: "results are filtered", Met: false}},
		}},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(markdown, "## Defects") {
		t.Errorf("a missed target must not become a product defect:\n%s", markdown)
	}
	if !strings.Contains(markdown, "aiming at something the screen does not have") {
		t.Errorf("want the execution issue named for what happened:\n%s", markdown)
	}
	if strings.Contains(markdown, "driver error") {
		t.Errorf("a missed target must not be blamed on the driver:\n%s", markdown)
	}
}

// An expectation the app never promised is a planning artifact, not a
// product defect: session mmx21 filed "Completed tile visible and selected"
// as [High] although iOS tiles carry no selected state.
func TestInapplicableOutcomeIsNotADefect(t *testing.T) {
	result := failedResult("complete a reminder", explore.PriorityCritical, "the Completed tile is selected")
	result.Outcomes[0].Inapplicable = true
	result.Outcomes[0].Evidence = "no tile carries a selected state"
	session := &explore.SessionReport{
		AppID:    "com.apple.reminders",
		Platform: "ios",
		Results:  []explore.TestResult{result},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(markdown, "## Defects") {
		t.Fatalf("inapplicable outcome filed as a defect:\n%s", markdown)
	}
	if !strings.Contains(markdown, "## Unconfirmed expectations") {
		t.Fatalf("want an unconfirmed-expectations section:\n%s", markdown)
	}
	for _, want := range []string{"complete a reminder", "the Completed tile is selected", "no tile carries a selected state"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("unconfirmed entry lacks %q:\n%s", want, markdown)
		}
	}
	if !strings.Contains(markdown, "0 defect findings") {
		t.Errorf("headline still counts a defect:\n%s", markdown)
	}
}

// One inapplicable outcome must not hide a real defect recorded after it.
func TestARealDefectSurvivesAnInapplicableOutcomeBeforeIt(t *testing.T) {
	result := failedResult("save a reminder", explore.PriorityCritical, "the Completed tile is selected")
	result.Outcomes[0].Inapplicable = true
	result.Outcomes = append(result.Outcomes, explore.OutcomeCheck{
		Expected: "the new reminder is listed",
		Evidence: "the list is unchanged",
	})
	session := &explore.SessionReport{
		AppID:    "com.apple.reminders",
		Platform: "ios",
		Results:  []explore.TestResult{result},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !strings.Contains(markdown, "- [High] the new reminder is listed") {
		t.Fatalf("want the applicable unmet outcome as the finding:\n%s", markdown)
	}
	if strings.Contains(markdown, "## Unconfirmed expectations") {
		t.Fatalf("a run with a real defect should not also be listed as unconfirmed:\n%s", markdown)
	}
}

// The run's own check_visible probes ride along in Outcomes. Scanning past
// an inapplicable scenario outcome must not land on a failed probe and file
// it as the defect -- that reopens the false [High] this section closed.
func TestADriverProbeIsNeverTheDefect(t *testing.T) {
	result := failedResult("complete a reminder", explore.PriorityCritical, "the Completed tile is selected")
	result.Outcomes[0].Inapplicable = true
	result.Outcomes = append(result.Outcomes, explore.OutcomeCheck{
		Expected: `visible: text "Completed"`,
		Evidence: "no matching element in the current tree",
		Driver:   true,
	})
	session := &explore.SessionReport{
		AppID:    "com.apple.reminders",
		Platform: "ios",
		Results:  []explore.TestResult{result},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if strings.Contains(markdown, "## Defects") {
		t.Fatalf("a driver probe became the defect:\n%s", markdown)
	}
	if !strings.Contains(markdown, "## Unconfirmed expectations") {
		t.Fatalf("want the run under unconfirmed expectations:\n%s", markdown)
	}
}

// Several scenarios carrying the same unpromised expectation are one line,
// the way defects on the same outcome cluster.
func TestUnconfirmedExpectationsClusterOnTheOutcome(t *testing.T) {
	session := &explore.SessionReport{AppID: "com.apple.reminders", Platform: "ios"}
	for _, name := range []string{"complete from list", "complete from detail"} {
		result := failedResult(name, explore.PriorityNormal, "the Completed tile is selected")
		result.Outcomes[0].Inapplicable = true
		session.Results = append(session.Results, result)
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if count := strings.Count(markdown, "the Completed tile is selected"); count != 1 {
		t.Fatalf("want one clustered entry, got %d:\n%s", count, markdown)
	}
	for _, name := range []string{"complete from list", "complete from detail"} {
		if !strings.Contains(markdown, "`"+name+"`") {
			t.Errorf("entry is missing test %q:\n%s", name, markdown)
		}
	}
}

// The headline is the only line many readers see; a run that is neither a
// pass, a defect, nor an execution problem must still be counted there, and
// the manager model must see it in its digest.
func TestUnconfirmedRunsReachTheHeadlineAndTheDigest(t *testing.T) {
	result := failedResult("complete a reminder", explore.PriorityNormal, "the Completed tile is selected")
	result.Outcomes[0].Inapplicable = true
	session := &explore.SessionReport{
		AppID:    "com.apple.reminders",
		Platform: "ios",
		Results:  []explore.TestResult{result, passedResult("open the list")},
	}
	markdown, err := Analyst{}.Report(context.Background(), session)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if !strings.Contains(markdown, "1 with an expectation the app never promised") {
		t.Errorf("headline drops the unconfirmed run:\n%s", markdown)
	}
	llm := &scriptedLLM{reply: explore.ChatResponse{Message: explore.Message{Text: "prose"}}}
	if _, err := (Analyst{Manager: llm}).Report(context.Background(), session); err != nil {
		t.Fatalf("Report: %v", err)
	}
	digest := llm.requests[0].Messages[1].Text
	if !strings.Contains(digest, "the Completed tile is selected") {
		t.Errorf("digest never mentions the unconfirmed expectation:\n%s", digest)
	}
}
