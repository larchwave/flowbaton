package planning

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

type scriptedLLM struct {
	replies  []string
	requests []explore.ChatRequest
}

func (s *scriptedLLM) Chat(_ context.Context, request explore.ChatRequest) (explore.ChatResponse, error) {
	s.requests = append(s.requests, request)
	if len(s.requests) > len(s.replies) {
		return explore.ChatResponse{}, errors.New("script exhausted")
	}
	return explore.ChatResponse{
		Message: explore.Message{Role: explore.RoleAssistant, Text: s.replies[len(s.requests)-1]},
	}, nil
}

type fakeKnowledge struct {
	hints []string
	err   error
}

func (f fakeKnowledge) Match(context.Context, explore.ScreenSignature) ([]string, error) {
	return f.hints, f.err
}

func testMap() *explore.UIMap {
	return &explore.UIMap{
		Screen: explore.ScreenSignature{
			AppID:      "com.example.app",
			Salient:    []string{"Login"},
			TreeDigest: "abcdef0123456789",
		},
		Markdown: "## Login\n| Sign in | button |\n| Email | field |",
	}
}

func scenarioJSON(entries ...string) string {
	return `{"scenarios":[` + strings.Join(entries, ",") + `]}`
}

func entry(name, priority string) string {
	return `{"name":"` + name + `","priority":"` + priority +
		`","steps":["tap Sign in"],"expected":["the home screen shows"]}`
}

func TestPlanNextValidatesAndStamps(t *testing.T) {
	llm := &scriptedLLM{replies: []string{
		scenarioJSON(entry("Sign in with valid data", "Critical"), entry("Reject a blank email", "high")),
	}}
	planner := &Planner{LLM: llm}
	scenarios, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 2 {
		t.Fatalf("got %d scenarios", len(scenarios))
	}
	first := scenarios[0]
	if first.Name != "Sign in with valid data" || first.Priority != explore.PriorityCritical {
		t.Fatalf("first scenario %+v", first)
	}
	if first.Style != "normal" || first.Status != explore.ScenarioPending {
		t.Fatalf("stamps %+v", first)
	}
	if first.StartScreen != "login-abcdef01" {
		t.Fatalf("start screen %q", first.StartScreen)
	}
	if scenarios[1].Priority != explore.PriorityHigh {
		t.Fatalf("second priority %q", scenarios[1].Priority)
	}
	prompt := llm.requests[0].Messages[1].Text
	for _, want := range []string{"## Login", "Emit at most 5 scenarios", "never invent routes"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestPlanNextFiltersReply(t *testing.T) {
	tests := []struct {
		name     string
		reply    string
		existing []string
		budget   int
		want     []string
	}{
		{
			name:   "existing names dropped after fold and space collapse",
			reply:  scenarioJSON(entry("Create  USER", "normal"), entry("Delete user", "normal")),
			budget: 5, existing: []string{"create user"},
			want: []string{"Delete user"},
		},
		{
			name:   "later in-reply repeat dropped",
			reply:  scenarioJSON(entry("Open settings", "normal"), entry("open  Settings", "high")),
			budget: 5,
			want:   []string{"Open settings"},
		},
		{
			name:   "unknown priority rejects only that scenario",
			reply:  scenarioJSON(entry("Good one", "low"), entry("Bad one", "urgent")),
			budget: 5,
			want:   []string{"Good one"},
		},
		{
			name: "blank name and empty steps rejected",
			reply: scenarioJSON(
				entry("  ", "normal"),
				`{"name":"No steps","priority":"normal","steps":["  "],"expected":["x"]}`,
				entry("Kept", "normal"),
			),
			budget: 5,
			want:   []string{"Kept"},
		},
		{
			name:   "budget clamps the accepted list",
			reply:  scenarioJSON(entry("One", "critical"), entry("Two", "normal"), entry("Three", "low")),
			budget: 2,
			want:   []string{"One", "Two"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			planner := &Planner{LLM: &scriptedLLM{replies: []string{test.reply}}}
			scenarios, err := planner.PlanNext(context.Background(), explore.PlanRequest{
				Map: testMap(), Style: "normal", Existing: test.existing, Budget: test.budget,
			})
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(scenarios))
			for _, s := range scenarios {
				got = append(got, s.Name)
			}
			if strings.Join(got, "|") != strings.Join(test.want, "|") {
				t.Fatalf("got %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlanNextRetriesOnceOnDecodeFailure(t *testing.T) {
	llm := &scriptedLLM{replies: []string{
		"sorry, here you go: not json",
		"```json\n" + scenarioJSON(entry("Sign in", "critical")) + "\n```",
	}}
	planner := &Planner{LLM: llm}
	scenarios, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].Name != "Sign in" {
		t.Fatalf("scenarios %+v", scenarios)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("chat calls %d", len(llm.requests))
	}
	retryMessages := llm.requests[1].Messages
	if len(retryMessages) != 4 {
		t.Fatalf("retry conversation length %d", len(retryMessages))
	}
	complaint := retryMessages[3]
	if complaint.Role != explore.RoleUser || !strings.Contains(complaint.Text, "did not decode") {
		t.Fatalf("complaint %+v", complaint)
	}
}

func TestPlanNextFailsAfterSecondDecodeFailure(t *testing.T) {
	tests := []struct {
		name    string
		replies []string
	}{
		{name: "garbage twice", replies: []string{"nope", "still nope"}},
		{name: "empty list twice", replies: []string{`{"scenarios":[]}`, `{"scenarios":[]}`}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			planner := &Planner{LLM: &scriptedLLM{replies: test.replies}}
			_, err := planner.PlanNext(context.Background(), explore.PlanRequest{
				Map: testMap(), Style: "normal", Budget: 3,
			})
			if err == nil || !strings.Contains(err.Error(), "decode scenarios") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestPlanNextUnknownStyleFallsBackToNormal(t *testing.T) {
	llm := &scriptedLLM{replies: []string{scenarioJSON(entry("Sign in", "critical"))}}
	planner := &Planner{LLM: llm}
	scenarios, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "wild", Budget: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scenarios[0].Style != "normal" {
		t.Fatalf("style stamp %q", scenarios[0].Style)
	}
	prompt := llm.requests[0].Messages[1].Text
	normal, _ := LookupStyle("normal")
	if !strings.Contains(prompt, normal.Directive) {
		t.Fatalf("prompt missing normal directive:\n%s", prompt)
	}
	if !strings.Contains(prompt, `Requested style "wild" is not known`) {
		t.Fatalf("prompt missing fallback note:\n%s", prompt)
	}
}

func TestPlanNextInjectsKnowledgeHints(t *testing.T) {
	llm := &scriptedLLM{replies: []string{scenarioJSON(entry("Sign in", "critical"))}}
	planner := &Planner{
		LLM:       llm,
		Knowledge: fakeKnowledge{hints: []string{"demo account: demo@example.com"}},
	}
	if _, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 3,
	}); err != nil {
		t.Fatal(err)
	}
	prompt := llm.requests[0].Messages[1].Text
	if !strings.Contains(prompt, "demo account: demo@example.com") {
		t.Fatalf("prompt missing hint:\n%s", prompt)
	}
	broken := &Planner{LLM: llm, Knowledge: fakeKnowledge{err: errors.New("disk gone")}}
	if _, err := broken.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 3,
	}); err == nil || !strings.Contains(err.Error(), "match hints") {
		t.Fatalf("err = %v", err)
	}
}

func TestPlanNextFailsClosed(t *testing.T) {
	llm := &scriptedLLM{}
	tests := []struct {
		name    string
		planner *Planner
		request explore.PlanRequest
	}{
		{name: "nil llm", planner: &Planner{}, request: explore.PlanRequest{Map: testMap(), Budget: 1}},
		{name: "nil map", planner: &Planner{LLM: llm}, request: explore.PlanRequest{Budget: 1}},
		{name: "zero budget", planner: &Planner{LLM: llm}, request: explore.PlanRequest{Map: testMap()}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.planner.PlanNext(context.Background(), test.request); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	if len(llm.requests) != 0 {
		t.Fatalf("model called %d times on invalid requests", len(llm.requests))
	}
}

func TestStylesRegistry(t *testing.T) {
	styles := Styles()
	if len(styles) != 3 {
		t.Fatalf("styles %v", styles)
	}
	names := []string{styles[0].Name, styles[1].Name, styles[2].Name}
	if strings.Join(names, ",") != "normal,curious,edge" {
		t.Fatalf("names %v", names)
	}
	for _, s := range styles {
		if s.Directive == "" {
			t.Fatalf("style %q has no directive", s.Name)
		}
	}
	if style, known := LookupStyle("edge"); !known || style.Name != "edge" {
		t.Fatalf("lookup edge = %+v known=%v", style, known)
	}
	if style, known := LookupStyle("nope"); known || style.Name != "normal" {
		t.Fatalf("lookup nope = %+v known=%v", style, known)
	}
}

func TestPlanNextAcceptsABareStringForASingleItemList(t *testing.T) {
	// A model answering "outcome" where ["outcome"] was asked still gave a
	// usable one-item list; only a non-string, non-list value is a failure.
	var reply planReply
	err := explore.DecodeReply(`{"scenarios":[{"name":"n","priority":"normal","steps":"tap add","expected":["a","b"]}]}`, &reply)
	if err != nil || len(reply.Scenarios) != 1 || len(reply.Scenarios[0].Steps) != 1 || reply.Scenarios[0].Steps[0] != "tap add" || len(reply.Scenarios[0].Expected) != 2 {
		t.Fatalf("decode: %v %+v", err, reply)
	}
	if err := explore.DecodeReply(`{"scenarios":[{"name":"n","priority":"normal","steps":7,"expected":["a"]}]}`, &reply); err == nil {
		t.Fatal("a number decoded as a step list")
	}
}

// Live sessions (2026-08-28) planned "the count badge increments" and "the
// placeholder is no longer shown", which no single final screen can show,
// so the judge rightly failed runs whose goal was reached. The rule that
// outcomes are judged on the final screen alone must reach the model.
func TestPlanPromptForbidsOutcomesRelativeToAnEarlierScreen(t *testing.T) {
	style, known := LookupStyle("normal")
	prompt := buildPrompt(explore.PlanRequest{Map: &explore.UIMap{}, Budget: 1}, style, known, nil)
	for _, want := range []string{"final screen alone", `"increments"`, `"is no longer shown"`, "Never copy a count"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
}

// Expectations the judge ruled this app never offers must reach the
// planner, or the next round writes them again.
func TestPlanNextNamesUnpromisedExpectations(t *testing.T) {
	llm := &scriptedLLM{replies: []string{scenarioJSON(entry("Sign in", "critical"))}}
	planner := &Planner{LLM: llm}
	if _, err := planner.PlanNext(context.Background(), explore.PlanRequest{
		Map: testMap(), Style: "normal", Budget: 3,
		Unpromised: []string{"the Completed tile is selected"},
	}); err != nil {
		t.Fatal(err)
	}
	prompt := llm.requests[0].Messages[1].Text
	if !strings.Contains(prompt, "the Completed tile is selected") {
		t.Fatalf("prompt missing the unpromised expectation:\n%s", prompt)
	}
}

// The planner wrote outcomes nothing can check: mmx57's calendar run spent
// two of six scenarios on a "red filled-circle selection highlight", and the
// judge has no colour to look at. Telling the planner what the checker sees
// is upstream of that.
//
// Measured against MiniMax-M3 on a cached calendar map, four plans per
// variant, budget six: without this rule 24 outcomes carried 4 about colour
// or highlighting; with it, 24 outcomes carried 0, and the outcomes stayed
// as specific ("The timeline header reads 'Monday - Aug 31, 2026'").
func TestPromptTellsThePlannerTheScreenArrivesAsText(t *testing.T) {
	t.Parallel()

	prompt := buildPrompt(explore.PlanRequest{Map: &explore.UIMap{}, Budget: 1}, builtinStyles[0], true, nil)
	for _, want := range []string{"roles, labels, identifiers", "cannot be checked"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q", want)
		}
	}
}
