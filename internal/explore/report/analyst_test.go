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
