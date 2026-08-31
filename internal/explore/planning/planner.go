// Package planning turns researched UI maps into prioritized, deduplicated
// test scenarios through one model conversation per iteration.
package planning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Planner implements explore.Planner on top of one chat model.
type Planner struct {
	LLM explore.LLM
	// Knowledge optionally supplies operator hints matched to the
	// screen; nil means no hints.
	Knowledge explore.KnowledgeStore
}

var _ explore.Planner = (*Planner)(nil)

type plannedScenario struct {
	Name     string `json:"name"`
	Priority string `json:"priority"`
	Steps    lines  `json:"steps"`
	Expected lines  `json:"expected"`
}

// lines is a string list that also accepts one bare string: models asked
// for ["outcome"] sometimes answer "outcome" for a single-item list, and
// one item is a valid list, not a reason to retry the whole plan.
type lines []string

func (l *lines) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = lines{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*l = lines(many)
	return nil
}

type planReply struct {
	Scenarios []plannedScenario `json:"scenarios"`
}

// PlanNext asks the model for up to request.Budget scenarios grounded in
// the researched map, validates the reply, drops rejected and repeated
// entries, and stamps the survivors with style, start screen, and pending
// status.
func (p *Planner) PlanNext(ctx context.Context, request explore.PlanRequest) ([]explore.Scenario, error) {
	if p.LLM == nil {
		return nil, errors.New("planning: nil llm")
	}
	if request.Map == nil {
		return nil, errors.New("planning: nil ui map")
	}
	if request.Budget <= 0 {
		return nil, errors.New("planning: budget must be positive")
	}
	style, known := LookupStyle(request.Style)
	hints, err := p.matchHints(ctx, request.Map.Screen)
	if err != nil {
		return nil, fmt.Errorf("planning: match hints: %w", err)
	}
	messages := []explore.Message{
		{Role: explore.RoleSystem, Text: plannerPersona},
		{Role: explore.RoleUser, Text: buildPrompt(request, style, known, hints)},
	}
	reply, err := p.converse(ctx, messages)
	if err != nil {
		return nil, err
	}
	return filterScenarios(reply.Scenarios, request, style.Name), nil
}

func (p *Planner) matchHints(ctx context.Context, screen explore.ScreenSignature) ([]string, error) {
	if p.Knowledge == nil {
		return nil, nil
	}
	return p.Knowledge.Match(ctx, screen)
}

// converse invokes the model and decodes the reply, continuing the same
// conversation once with the decode complaint before giving up.
func (p *Planner) converse(ctx context.Context, messages []explore.Message) (planReply, error) {
	// This loop reads like explore.ChatJSON and is deliberately not it: the
	// retry here also covers an empty scenarios list, which decodes fine.
	// A planning error aborts the whole session (crew.go), so that second
	// chance is what keeps an empty plan from ending the run.
	response, err := p.LLM.Chat(ctx, explore.ChatRequest{Messages: messages})
	if err != nil {
		return planReply{}, fmt.Errorf("planning: chat: %w", err)
	}
	reply, decodeErr := decodeReply(response.Message.Text)
	if decodeErr == nil {
		return reply, nil
	}
	retry := append(append([]explore.Message(nil), messages...),
		response.Message,
		explore.Message{
			Role: explore.RoleUser,
			Text: fmt.Sprintf("That reply did not decode: %v. Answer again with only the JSON object in the required shape.", decodeErr),
		})
	response, err = p.LLM.Chat(ctx, explore.ChatRequest{Messages: retry})
	if err != nil {
		return planReply{}, fmt.Errorf("planning: retry chat: %w", err)
	}
	reply, decodeErr = decodeReply(response.Message.Text)
	if decodeErr != nil {
		return planReply{}, fmt.Errorf("planning: decode scenarios: %w", decodeErr)
	}
	return reply, nil
}

func decodeReply(text string) (planReply, error) {
	var reply planReply
	if err := explore.DecodeReply(text, &reply); err != nil {
		return planReply{}, err
	}
	if len(reply.Scenarios) == 0 {
		return planReply{}, errors.New("the scenarios list is empty")
	}
	return reply, nil
}

// filterScenarios validates each raw scenario, folds its priority, drops
// entries whose normalized name is already planned or appeared earlier in
// the same reply, and clamps the result to the budget.
func filterScenarios(raw []plannedScenario, request explore.PlanRequest, styleName string) []explore.Scenario {
	seen := make(map[string]bool, len(request.Existing)+len(raw))
	for _, name := range request.Existing {
		seen[normalizeName(name)] = true
	}
	startScreen := request.Map.Screen.Key()
	out := make([]explore.Scenario, 0, request.Budget)
	for _, item := range raw {
		if len(out) == request.Budget {
			break
		}
		name := strings.TrimSpace(item.Name)
		priority, ok := foldPriority(item.Priority)
		steps := cleanLines(item.Steps)
		expected := cleanLines(item.Expected)
		if name == "" || !ok || len(steps) == 0 || len(expected) == 0 {
			continue
		}
		key := normalizeName(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, explore.Scenario{
			Name:        name,
			Priority:    priority,
			Style:       styleName,
			StartScreen: startScreen,
			Steps:       steps,
			Expected:    expected,
			Status:      explore.ScenarioPending,
		})
	}
	return out
}

func foldPriority(raw string) (explore.Priority, bool) {
	priority := explore.Priority(strings.ToLower(strings.TrimSpace(raw)))
	switch priority {
	case explore.PriorityCritical, explore.PriorityImportant, explore.PriorityHigh,
		explore.PriorityNormal, explore.PriorityLow:
		return priority, true
	}
	return "", false
}

// normalizeName case-folds a scenario name and collapses runs of
// whitespace so cosmetic variants match.
func normalizeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(name)), " ")
}

func cleanLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

const plannerPersona = "You are a senior QA engineer doing exploratory testing " +
	"of a mobile app. You plan business-focused test scenarios grounded " +
	"strictly in what is visible on the screen."

func buildPrompt(request explore.PlanRequest, style Style, known bool, hints []string) string {
	var b strings.Builder
	b.WriteString("Plan test scenarios for the screen mapped below.\n\nScreen map:\n")
	b.WriteString(request.Map.Markdown)
	b.WriteString("\n")
	if len(hints) > 0 {
		b.WriteString("\nOperator hints about this app:\n")
		for _, hint := range hints {
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\nPlanning approach (%s):\n%s\n", style.Name, style.Directive)
	if !known {
		fmt.Fprintf(&b, "(Requested style %q is not known; the normal approach applies.)\n", request.Style)
	}
	if request.Focus != "" {
		fmt.Fprintf(&b, "\nFocus every scenario on: %s\n", request.Focus)
	}
	if len(request.Unpromised) > 0 {
		b.WriteString("\nOutcomes this app was already found not to offer. " +
			"Do not expect any of these again:\n")
		for _, expected := range request.Unpromised {
			b.WriteString("- ")
			b.WriteString(expected)
			b.WriteString("\n")
		}
	}
	if len(request.Existing) > 0 {
		b.WriteString("\nScenarios already planned, do not propose these again:\n")
		for _, name := range request.Existing {
			b.WriteString("- ")
			b.WriteString(name)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "\nEmit at most %d scenarios.\n", request.Budget)
	b.WriteString(promptRules)
	return b.String()
}

const promptRules = `
Reply with only a JSON object of this exact shape:
{"scenarios":[{"name":"short goal","priority":"critical|important|high|normal|low","steps":["atomic action"],"expected":["atomic on-screen outcome"]}]}

Rules:
- Ground every scenario only in elements present in the map; never invent routes, texts, or data.
- Each scenario pursues one meaningful user goal from start to finish.
- Steps are atomic actions meant as guidance, not a rigid script.
- Each expected outcome is a single result that can be checked on the screen.
- Outcomes are judged on the final screen alone, with no memory of earlier screens: name the exact text or element that must be there (or must be absent), never a change relative to before -- no "increments", "updates", "is no longer shown", "changes to".
- Never copy a count, badge, or timestamp from the map into an outcome the scenario itself will change; expect what the goal produces (the row with the title the step typed, the screen the tap opens), not a number.
- The screen reaches the checker as text: roles, labels, identifiers. Colour, shape, size, highlighting, and position cannot be checked -- name the text or the element that must be there instead.
- Balance positive and negative cases.
- Priority meanings: critical = the screen's core purpose; important = key flows; high = secondary features; normal = supporting behavior; low = cosmetic or boundary detail.
`
