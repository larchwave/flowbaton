package run

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

// treeTexts collects the case-folded text-bearing attribute values of the
// final tree for deterministic outcome checks.
// visibleTexts collects the words of the final screen the way the engine
// would find them: pruned to the viewport. Reading the raw tree passed an
// expectation on text nobody could see -- a row scrolled off, a hidden view
// -- which is a false pass, the one wrong answer a testing tool must not
// give.
//
// A tree that will not normalize yields no texts rather than the raw ones.
// The outcome then falls through to the worker, which is the conservative
// path: asking beats auto-passing on evidence that could not be checked.
func visibleTexts(state *explore.ScreenState) []string {
	if state == nil {
		return nil
	}
	root, err := state.VisibleTree()
	if err != nil {
		return nil
	}
	texts := []string{}
	var walk func(element *hierarchy.Element)
	walk = func(element *hierarchy.Element) {
		for _, key := range []string{"text", "label", "name"} {
			if value := strings.TrimSpace(element.Node.Attributes[key]); value != "" {
				texts = append(texts, strings.ToLower(value))
			}
		}
		for _, child := range element.Children {
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
			return fmt.Sprintf("text %q is on the final screen", explore.Truncate(text, 60)), true
		}
	}
	return "", false
}

type workerVerdict struct {
	Met      bool   `json:"met"`
	Evidence string `json:"evidence"`
	// Inapplicable is set only when the model says the expectation does
	// not apply to this app at all. A reply that omits it leaves an unmet
	// outcome a defect, which is the safe direction.
	Inapplicable bool `json:"inapplicable"`
	// Undecidable is set when the facts given cannot answer the question at
	// all -- an outcome about colour, shape, or highlighting, none of which
	// a text table carries. Without it the judge answered anyway: two of the
	// three defects in the mmx57 calendar report turn on a "red
	// filled-circle selection highlight" and were filed as [High] not
	// observed.
	Undecidable bool `json:"undecidable"`
}

// askWorkerOutcome poses one expected outcome to the worker model against
// the final element table and the driver's own check_visible results from
// the run. Those results are facts the driver measured, not claims the
// tester made, so the judge gets them next to the table. Any model failure
// counts as not met.
func askWorkerOutcome(ctx context.Context, llm explore.LLM, expected string, facts judgeFacts) explore.OutcomeCheck {
	check := explore.OutcomeCheck{Expected: expected}
	if llm == nil {
		check.Missed = explore.MissUnjudged
		check.Evidence = "no model available for this check"
		return check
	}
	prompt := fmt.Sprintf(
		"Judge one expected outcome of a mobile UI test against the final screen.\n"+
			"Expected outcome: %s\n\n%s%s%s\n"+
			"The facts above are text: roles, labels, identifiers and the driver's "+
			"own checks. They do not carry colour, shape, size, or highlighting. When "+
			"the outcome turns on something these facts cannot express, set "+
			"undecidable true and say which fact was missing; do not report it as "+
			"not met.\n"+
			"Reply with only a JSON object "+
			"{\"met\": true|false, \"inapplicable\": true|false, "+
			"\"undecidable\": true|false, \"evidence\": \"one line\"}.\n"+
			"Set inapplicable only when this app has no such feature or the screen "+
			"cannot express the expectation at all; an outcome the app should have "+
			"produced and did not is applicable.",
		expected, elementTable(facts.Final),
		driverCheckLines(facts.DriverChecks)+typedLines(facts.Typed)+visitedLines(facts.Visited),
		sessionTagLine(facts.SessionTag))
	verdict := workerVerdict{}
	_, err := explore.ChatJSON(ctx, llm, explore.ChatRequest{Messages: []explore.Message{
		{Role: explore.RoleSystem, Text: "You judge test outcomes strictly from the screen content and the driver checks given to you. An outcome that names a specific value or text is met only when exactly that value or text is on the screen; a different value is not met."},
		{Role: explore.RoleUser, Text: prompt},
	}}, &verdict)
	if err != nil {
		check.Missed = explore.MissUnjudged
		// The report is all an operator gets, and a model that answered
		// nonsense sends them somewhere different from one that never
		// answered at all.
		if errors.Is(err, explore.ErrUnreadableReply) {
			check.Evidence = "unreadable outcome reply: " + err.Error()
		} else {
			check.Evidence = "outcome check unavailable: " + err.Error()
		}
		return check
	}
	check.Met = verdict.Met
	check.Evidence = verdict.Evidence
	switch {
	case !verdict.Met && verdict.Undecidable:
		// A fact gap outranks a judgement about the app: the judge says it
		// could not look, which is not the same as the app never promising
		// the outcome.
		check.Missed = explore.MissUnjudged
	case !verdict.Met && verdict.Inapplicable:
		check.Missed = explore.MissUnpromised
	}
	return check
}

// judgeFacts carries what the driver observed during one run. Every one of
// these reached the judge as its own parameter until the list outgrew the
// call; they are all answers to "what actually happened", so they travel
// together.
type judgeFacts struct {
	Final        *explore.ScreenState
	DriverChecks []explore.OutcomeCheck
	Typed        []string
	// SessionTag is the label the tester was told to stamp on anything it
	// creates (scenarioText). Blank when the session has no name.
	SessionTag string
	// Visited names the screens the run passed through, in order. Without
	// it the judge answers only about the last screen, so an expectation
	// about a state the run REACHED and then left reads as never produced:
	// live mmx51 filed "[High] not observed: A new contact creation screen
	// is shown" against a run whose own reproduce list taps "First name".
	Visited []string
}

// sessionTagLine warns the judge about the harness's own fingerprint. The
// tester is instructed to tag the data it creates with the session name, so
// a row the scenario named will carry a suffix the scenario never mentions.
// Without this the judge applies its exact-text rule to the harness's tag
// and the report files a defect against the app for it (session mmx24).
func sessionTagLine(tag string) string {
	if tag == "" {
		return ""
	}
	return fmt.Sprintf(
		"\nThe tester was told to tag any data it creates with %q. A screen value "+
			"that matches the expectation apart from that tag is still the expected "+
			"one; do not count the tag against it.\n", tag)
}

// visitedScreens names the screens a run passed through, in order, with
// consecutive repeats collapsed: the judge needs the journey, not one line
// per step. A step whose screen was never captured carries a zero
// signature and is skipped -- naming it would assert a screen with no tree
// behind it.
func visitedScreens(steps []explore.StepRecord) []string {
	visited := []string{}
	for _, step := range steps {
		for _, signature := range []explore.ScreenSignature{step.Before, step.After} {
			if signature.TreeDigest == "" {
				continue
			}
			key := signature.Key()
			if len(visited) > 0 && visited[len(visited)-1] == key {
				continue
			}
			visited = append(visited, key)
		}
	}
	return visited
}

// visitedLines renders the journey for the judge. One screen is not a
// journey: it says nothing the final table does not already show, so it
// stays out of the prompt rather than inviting the model to reason about a
// list of length one.
func visitedLines(visited []string) string {
	if len(visited) < 2 {
		return ""
	}
	builder := &strings.Builder{}
	builder.WriteString("\nScreens the run visited, in order (driver facts):\n")
	for index, screen := range visited {
		fmt.Fprintf(builder, "%d. %s\n", index+1, screen)
	}
	builder.WriteString(
		"These are screen names, not screen content. An outcome naming a state " +
			"the app should REACH is met when the run visited it, even though the " +
			"run continued past it; an outcome naming a specific value or text is " +
			"still met only when that value is on the final screen above.\n")
	return builder.String()
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

// typedLines renders the text the run typed, so an outcome phrased as
// "the title that was entered" can be matched against the screen. Masked
// inputs are not among them.
func typedLines(typed []string) string {
	if len(typed) == 0 {
		return ""
	}
	builder := &strings.Builder{}
	builder.WriteString("\nText typed during the run (driver facts):\n")
	for _, text := range typed {
		fmt.Fprintf(builder, "- %q\n", text)
	}
	return builder.String()
}

// typedTexts collects the unmasked input text of a run's steps in order.
func typedTexts(steps []explore.StepRecord) []string {
	var typed []string
	for _, step := range steps {
		if step.Action.Kind == explore.ActionInput && !step.Action.Masked && step.Action.Text != "" {
			typed = append(typed, step.Action.Text)
		}
	}
	return typed
}

// evaluateOutcomes checks every expected outcome: deterministic tree match
// first, then one worker call, then a conservative merge with the model's
// own finish outcomes. The model can never turn an unmet check into a met
// one; a model claim of not-met always sticks.
func evaluateOutcomes(ctx context.Context, llm explore.LLM, expected []string, finish *finishArgs, facts judgeFacts) []explore.OutcomeCheck {
	claims := map[string]finishOutcome{}
	if finish != nil {
		for _, outcome := range finish.Outcomes {
			claims[strings.ToLower(strings.TrimSpace(outcome.Expected))] = outcome
		}
	}
	texts := visibleTexts(facts.Final)
	checks := make([]explore.OutcomeCheck, 0, len(expected))
	for _, want := range expected {
		check := explore.OutcomeCheck{Expected: want}
		if evidence, met := foldedTreeMatch(texts, want); met {
			check.Met = true
			check.Evidence = evidence
		} else {
			check = askWorkerOutcome(ctx, llm, want, facts)
		}
		if claim, ok := claims[strings.ToLower(strings.TrimSpace(want))]; ok && !claim.Met {
			check.Met = false
			// The tester claims the app failed to do it, which is a product
			// claim: it outranks the judge's "this app never offers that"
			// and its silence alike.
			check.Missed = explore.MissDefect
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
