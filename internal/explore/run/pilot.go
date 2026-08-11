package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

// pilotCadence is how many executed steps pass between pilot check-ins.
const pilotCadence = 5

// pilot is the supervisor conversation on the manager model. It never sees
// raw screen trees, only the compact step log.
type pilot struct {
	llm      explore.LLM
	messages []explore.Message
}

func newPilot(llm explore.LLM, scenario explore.Scenario) *pilot {
	system := &strings.Builder{}
	system.WriteString("You supervise one mobile UI test run from its step log. ")
	system.WriteString("Decide whether the run should continue or stop, and give one short instruction when it should continue. ")
	system.WriteString("Reply with only a JSON object {\"decision\": \"continue\"|\"stop\", \"verdict\": \"one line, required for stop\", \"instruction\": \"one line, optional\"}.\n\n")
	fmt.Fprintf(system, "Scenario: %s\n", scenario.Name)
	for _, want := range scenario.Expected {
		fmt.Fprintf(system, "Expected: %s\n", want)
	}
	return &pilot{
		llm:      llm,
		messages: []explore.Message{{Role: explore.RoleSystem, Text: system.String()}},
	}
}

type pilotOrder struct {
	Decision    string `json:"decision"`
	Verdict     string `json:"verdict"`
	Instruction string `json:"instruction"`
}

func (o pilotOrder) stop() bool { return o.Decision == "stop" }

// review feeds the newest step log lines to the pilot and returns its
// order. Any model or protocol failure is an error: supervision fails
// closed, never silently continues.
func (p *pilot) review(ctx context.Context, logLines []string, final bool) (pilotOrder, error) {
	question := "The run continues. Continue or stop?"
	if final {
		question = "The run has ended. Give your closing verdict as a stop decision."
	}
	text := "Newest steps:\n" + strings.Join(logLines, "\n") + "\n\n" + question
	request := explore.ChatRequest{Messages: append(append([]explore.Message(nil), p.messages...), explore.Message{Role: explore.RoleUser, Text: text})}
	response, err := p.llm.Chat(ctx, request)
	if err != nil {
		return pilotOrder{}, fmt.Errorf("pilot review: %w", err)
	}
	order := pilotOrder{}
	if err := strictjson.Decode([]byte(explore.UnfencedJSON(response.Message.Text)), &order); err != nil {
		return pilotOrder{}, fmt.Errorf("pilot reply: %w", err)
	}
	if order.Decision != "continue" && order.Decision != "stop" {
		return pilotOrder{}, fmt.Errorf("pilot decision must be continue or stop, got %q", order.Decision)
	}
	p.messages = append(p.messages, explore.Message{Role: explore.RoleUser, Text: text}, response.Message)
	return order, nil
}

// stepLog renders records as the compact per-step lines the pilot reads.
func stepLog(steps []explore.StepRecord) []string {
	lines := make([]string, 0, len(steps))
	for _, step := range steps {
		line := fmt.Sprintf("step %d: %s", step.Index+1, step.Action.Kind)
		if step.Action.Text != "" {
			line += fmt.Sprintf(" %q", truncate(step.Action.Text, 40))
		}
		if step.Action.Direction != "" {
			line += " " + step.Action.Direction
		}
		line += fmt.Sprintf(" status=%s changed=%v", step.Status, !step.After.Same(step.Before))
		if step.Note != "" {
			line += " note=" + truncate(step.Note, 60)
		}
		if step.ErrText != "" {
			line += " error=" + truncate(step.ErrText, 60)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, "(no steps executed yet)")
	}
	return lines
}
