package explore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestScreenSignatureKeyIsSlugAndDigestBounded(t *testing.T) {
	signature := ScreenSignature{
		AppID:      "com.example.app",
		Salient:    []string{"Login  Screen!", ""},
		TreeDigest: "abcdef0123456789",
	}
	key := signature.Key()
	if key != "login-screen-abcdef01" {
		t.Fatalf("unexpected key %q", key)
	}
	if empty := (ScreenSignature{TreeDigest: "beef"}).Key(); empty != "beef" {
		t.Fatalf("digest-only key %q", empty)
	}
}

func TestPlanPendingOrdersByPriorityThenInsertion(t *testing.T) {
	plan := Plan{Scenarios: []Scenario{
		{Name: "low", Priority: PriorityLow},
		{Name: "crit", Priority: PriorityCritical},
		{Name: "done", Priority: PriorityCritical, Status: ScenarioPassed},
		{Name: "normal-a", Priority: PriorityNormal},
		{Name: "normal-b", Priority: PriorityNormal},
	}}
	pending := plan.Pending()
	got := make([]string, 0, len(pending))
	for _, s := range pending {
		got = append(got, s.Name)
	}
	want := "crit,normal-a,normal-b,low"
	if strings.Join(got, ",") != want {
		t.Fatalf("order %v, want %s", got, want)
	}
}

type scriptedLLM struct {
	replies []Message
	calls   int
}

func (s *scriptedLLM) Chat(_ context.Context, _ ChatRequest) (ChatResponse, error) {
	if s.calls >= len(s.replies) {
		return ChatResponse{}, errors.New("script exhausted")
	}
	reply := s.replies[s.calls]
	s.calls++
	return ChatResponse{Message: reply, Usage: Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func TestRunToolLoopExecutesToolsUntilPlainReply(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "ping", Arguments: json.RawMessage(`{}`)}}},
		{Role: RoleAssistant, Text: "done"},
	}}
	pinged := false
	box := ToolBox{
		Specs: []ToolSpec{{Name: "ping"}},
		Handlers: map[string]ToolHandler{
			"ping": func(context.Context, json.RawMessage) (string, error) {
				pinged = true
				return "pong", nil
			},
		},
	}
	result, err := RunToolLoop(context.Background(), llm, []Message{{Role: RoleUser, Text: "go"}}, box, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !pinged || result.Stopped || result.Exhausted {
		t.Fatalf("pinged=%v result=%+v", pinged, result)
	}
	if result.Usage.InputTokens != 2 {
		t.Fatalf("usage %+v", result.Usage)
	}
	last := result.Messages[len(result.Messages)-1]
	if last.Text != "done" {
		t.Fatalf("last message %+v", last)
	}
}

func TestRunToolLoopStopRequestAndBounds(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "halt"}}},
	}}
	box := ToolBox{Handlers: map[string]ToolHandler{
		"halt": func(context.Context, json.RawMessage) (string, error) {
			return "stopping", ErrStopRequested
		},
	}}
	result, err := RunToolLoop(context.Background(), llm, nil, box, 3)
	if err != nil || !result.Stopped {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if _, err := RunToolLoop(context.Background(), llm, nil, box, 0); err == nil {
		t.Fatal("zero bound accepted")
	}
}

type fakeCrew struct {
	state    *ScreenState
	plans    [][]Scenario
	planCall int
	ran      []string
}

func (f *fakeCrew) Observe(context.Context) (*ScreenState, error) { return f.state, nil }
func (f *fakeCrew) Research(_ context.Context, s *ScreenState) (*UIMap, error) {
	return &UIMap{Screen: s.Signature}, nil
}
func (f *fakeCrew) PlanNext(_ context.Context, _ PlanRequest) ([]Scenario, error) {
	if f.planCall >= len(f.plans) {
		return nil, nil
	}
	scenarios := f.plans[f.planCall]
	f.planCall++
	return scenarios, nil
}
func (f *fakeCrew) RunScenario(_ context.Context, s Scenario, _ *ScreenState) (*TestResult, error) {
	f.ran = append(f.ran, s.Name)
	return &TestResult{Scenario: s, Status: TestPassed}, nil
}
func (f *fakeCrew) EnsureReady(context.Context) (*ScreenState, error) { return f.state, nil }
func (f *fakeCrew) Reach(context.Context, string) (*ScreenState, error) {
	return f.state, nil
}
func (f *fakeCrew) Report(_ context.Context, r *SessionReport) (string, error) {
	return "report", nil
}

func TestRunSessionSpendsBudgetAndStopsOnDryRounds(t *testing.T) {
	fake := &fakeCrew{
		state: &ScreenState{Signature: ScreenSignature{AppID: "app", TreeDigest: "d1"}},
		plans: [][]Scenario{
			{{Name: "a", Priority: PriorityNormal}, {Name: "b", Priority: PriorityCritical}},
		},
	}
	crew := Crew{
		Observer: fake, Researcher: fake, Planner: fake,
		Tester: fake, Navigator: fake, Analyst: fake,
	}
	clock := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	config := Config{
		AppID: "app", MaxTests: 3, Styles: []string{"normal", "curious"},
		Clock: func() time.Time { return clock },
	}
	report, err := RunSession(context.Background(), config, crew)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fake.ran, ",") != "b,a" {
		t.Fatalf("ran %v", fake.ran)
	}
	if len(report.Results) != 2 || report.Markdown != "report" {
		t.Fatalf("report %+v", report)
	}
	if !report.Finished.Equal(clock) {
		t.Fatalf("finished %v", report.Finished)
	}
}

func TestRunSessionFailsClosed(t *testing.T) {
	if _, err := RunSession(context.Background(), Config{MaxTests: 0}, Crew{}); err == nil {
		t.Fatal("zero budget accepted")
	}
	if _, err := RunSession(context.Background(), Config{MaxTests: 1}, Crew{}); err == nil {
		t.Fatal("empty crew accepted")
	}
}
