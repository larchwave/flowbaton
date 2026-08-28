package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// treeTexts collects the case-folded text-bearing attribute values of the
// final tree for deterministic outcome checks.
func treeTexts(root device.TreeNode) []string {
	texts := []string{}
	var walk func(node device.TreeNode)
	walk = func(node device.TreeNode) {
		for _, key := range []string{"text", "label", "name"} {
			if value := strings.TrimSpace(node.Attributes[key]); value != "" {
				texts = append(texts, strings.ToLower(value))
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return texts
}

func foldedTreeMatch(texts []string, expected string) (string, bool) {
	needle := strings.ToLower(strings.TrimSpace(expected))
	if needle == "" {
		return "", false
	}
	for _, text := range texts {
		if strings.Contains(text, needle) {
			return fmt.Sprintf("text %q is on the final screen", truncate(text, 60)), true
		}
	}
	return "", false
}

type workerVerdict struct {
	Met      bool   `json:"met"`
	Evidence string `json:"evidence"`
}

// askWorkerOutcome poses one expected outcome to the worker model against
// the final element table and the driver's own check_visible results from
// the run. Those results are facts the driver measured, not claims the
// tester made, so the judge gets them next to the table. Any model failure
// counts as not met.
func askWorkerOutcome(ctx context.Context, llm explore.LLM, expected string, final *explore.ScreenState, driverChecks []explore.OutcomeCheck) explore.OutcomeCheck {
	check := explore.OutcomeCheck{Expected: expected}
	if llm == nil {
		check.Evidence = "no model available for this check"
		return check
	}
	prompt := fmt.Sprintf(
		"Judge one expected outcome of a mobile UI test against the final screen.\n"+
			"Expected outcome: %s\n\n%s%s\n"+
			"Reply with only a JSON object {\"met\": true|false, \"evidence\": \"one line\"}.",
		expected, elementTable(final), driverCheckLines(driverChecks))
	response, err := llm.Chat(ctx, explore.ChatRequest{Messages: []explore.Message{
		{Role: explore.RoleSystem, Text: "You judge test outcomes strictly from the screen content and the driver checks given to you."},
		{Role: explore.RoleUser, Text: prompt},
	}})
	if err != nil {
		check.Evidence = "outcome check unavailable: " + err.Error()
		return check
	}
	verdict := workerVerdict{}
	if err := explore.DecodeReply(response.Message.Text, &verdict); err != nil {
		check.Evidence = "unreadable outcome reply: " + err.Error()
		return check
	}
	check.Met = verdict.Met
	check.Evidence = verdict.Evidence
	return check
}

// driverCheckLines renders check_visible results for the judge; blank when
// the run made none.
func driverCheckLines(checks []explore.OutcomeCheck) string {
	if len(checks) == 0 {
		return ""
	}
	builder := &strings.Builder{}
	builder.WriteString("\nDriver checks measured on this final screen (facts, not model claims):\n")
	for _, check := range checks {
		fmt.Fprintf(builder, "- %s: %t (%s)\n", check.Expected, check.Met, check.Evidence)
	}
	return builder.String()
}

// evaluateOutcomes checks every expected outcome: deterministic tree match
// first, then one worker call, then a conservative merge with the model's
// own finish outcomes. The model can never turn an unmet check into a met
// one; a model claim of not-met always sticks.
func evaluateOutcomes(ctx context.Context, llm explore.LLM, expected []string, final *explore.ScreenState, finish *finishArgs, driverChecks []explore.OutcomeCheck) []explore.OutcomeCheck {
	claims := map[string]finishOutcome{}
	if finish != nil {
		for _, outcome := range finish.Outcomes {
			claims[strings.ToLower(strings.TrimSpace(outcome.Expected))] = outcome
		}
	}
	texts := treeTexts(final.Hierarchy)
	checks := make([]explore.OutcomeCheck, 0, len(expected))
	for _, want := range expected {
		check := explore.OutcomeCheck{Expected: want}
		if evidence, met := foldedTreeMatch(texts, want); met {
			check.Met = true
			check.Evidence = evidence
		} else {
			check = askWorkerOutcome(ctx, llm, want, final, driverChecks)
		}
		if claim, ok := claims[strings.ToLower(strings.TrimSpace(want))]; ok && !claim.Met {
			check.Met = false
			if claim.Evidence != "" {
				check.Evidence = "model: " + claim.Evidence
			}
		}
		checks = append(checks, check)
	}
	return checks
}

func allMet(checks []explore.OutcomeCheck) bool {
	for _, check := range checks {
		if !check.Met {
			return false
		}
	}
	return true
}
