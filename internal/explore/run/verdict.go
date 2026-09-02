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
	// Verdict is the one word the prompt asks for. Three independent
	// booleans let a weak model contradict itself and it did: mmx75 and
	// mmx77 each wrote the routing reason into evidence and then set
	// met=false with both flags off, so a fact gap and a feature the app
	// never had were filed as defects. One word cannot disagree with
	// itself. The booleans stay for a model that answers in the old shape.
	Verdict  string `json:"verdict"`
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
// judgeSystemRule keeps a judge on the screen it was given. The strictness
// is the point -- a wrong value must never pass -- but mmx74 filed a High
// defect against an outcome its own evidence found: it wanted "Tuesday,
// September 1" and the composer said "Sep 1, 2026", which is the same date
// wearing the app's format. One date, one time, one number written two ways
// is one value; a different value stays a different value.
const judgeSystemRule = "You judge test outcomes strictly from the screen content and " +
	"the driver checks given to you. An outcome that names a specific value or text is met " +
	"only when that value or text is on the screen; a different value is not met. A date, a " +
	"time, a number or an amount written in another format is the same value, not a different " +
	"one: judge what it means, not how the app spells it."

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
			"{\"verdict\": \"met\"|\"not_met\"|\"inapplicable\"|\"undecidable\", "+
			"\"evidence\": \"one line\"}.\n"+
			"Choose exactly one verdict:\n"+
			"met -- the screen shows it.\n"+
			"not_met -- the app should have produced it and did not.\n"+
			"inapplicable -- this app has no such feature, or the screen cannot "+
			"express the expectation at all.\n"+
			"undecidable -- the facts above cannot answer the question.\n"+
			"If the reason you are about to write in evidence is that a fact is "+
			"missing, the verdict is undecidable; if it is that the app has no such "+
			"control, the verdict is inapplicable. Neither is not_met.",
		expected, elementTable(facts.Final),
		layoutLine(facts.Final)+driverCheckLines(facts.DriverChecks)+
			typedLines(facts.Typed)+visitedLines(facts.Visited)+actionLines(facts.Actions),
		sessionTagLine(facts.SessionTag))
	verdict := workerVerdict{}
	_, err := explore.ChatJSON(ctx, llm, explore.ChatRequest{Messages: []explore.Message{
		{Role: explore.RoleSystem, Text: judgeSystemRule},
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
	return readVerdict(verdict, expected)
}

// readVerdict maps one model reply onto a check. The word decides when it is
// one of the four; anything else, including an empty reply, falls back to the
// booleans, which keeps the safe direction -- an unmet outcome stays a defect.
func readVerdict(verdict workerVerdict, expected string) explore.OutcomeCheck {
	check := explore.OutcomeCheck{Expected: expected, Evidence: verdict.Evidence}
	switch verdict.Verdict {
	case "met":
		check.Met = true
		return check
	case "not_met":
		return check
	case "undecidable":
		check.Missed = explore.MissUnjudged
		return check
	case "inapplicable":
		check.Missed = explore.MissUnpromised
		return check
	}
	check.Met = verdict.Met
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
	Final *explore.ScreenState
	// Actions are the run's steps, so the judge can answer what it did and
	// not only where it ended.
	Actions      []explore.StepRecord
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
// actionLines names what the run did, in order. mmx85's judge asked for it:
// "The driver facts do not specify which button was tapped, so it cannot be
// confirmed whether the interface associated with that button is now open."
// It was being told the screens the run visited and the text it typed, and
// nothing about the actions between them.
//
// A failed step is left out. It did not happen, and listing it invites the
// judge to credit an action the device refused. The rendering is the step
// log's, which has named the resolved target since c14b63d -- "id
// inbox-button" rather than "e13", which is the shape the question wants.
func actionLines(steps []explore.StepRecord) string {
	done := make([]explore.StepRecord, 0, len(steps))
	for _, step := range steps {
		if step.Status == explore.StepFailed {
			continue
		}
		done = append(done, step)
	}
	if len(done) == 0 {
		return ""
	}
	builder := &strings.Builder{}
	builder.WriteString("\nWhat the run did, in order (driver facts):\n")
	for _, step := range done {
		fmt.Fprintf(builder, "- %s\n", explore.StepLine(step))
	}
	return builder.String()
}

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
// layoutRowTolerance is how far two elements' tops may differ and still count
// as the same row. A calendar's day cells do not share a pixel exactly.
const layoutRowTolerance = 12

// layoutLine describes how the screen's labelled elements are arranged. Three
// sessions asked the judge a question about layout -- a vertical split, an
// agenda beneath a grid, a list against a month grid -- and the element table
// answers none of them: it lists identifiers and drops the geometry it
// already holds. mmx79's judge said so itself, "the facts only list element
// identifiers and do not describe whether they form an agenda-style list or a
// month calendar grid".
//
// How many rows hold more than one element is the statistic that separates
// them, and the widest row is not: Calendar's month grid and its day timeline
// BOTH answer "up to 7 across", because the timeline carries a week strip.
// Measured on two live captures of this app:
//
//	month grid    17 rows, widths [1 1 1 1 1 1 2 2 2 2 3 4 4 5 7 7 7], 11 wide
//	day timeline  27 rows, widths [1 x20, 2 4 4 5 6 7],                 6 wide
//
// Raw bounds per row would answer the question too and would cost this
// table's length again, on every judged outcome.
func layoutLine(state *explore.ScreenState) string {
	if state == nil {
		return ""
	}
	tops := []int{}
	perRow := map[int]int{}
	for _, element := range state.Elements {
		bounds, ok := explore.ElementBounds(element.Node)
		if !ok {
			continue
		}
		row := -1
		for _, top := range tops {
			if bounds.Y-top <= layoutRowTolerance && top-bounds.Y <= layoutRowTolerance {
				row = top
				break
			}
		}
		if row < 0 {
			row = bounds.Y
			tops = append(tops, row)
		}
		perRow[row]++
	}
	if len(tops) == 0 {
		return ""
	}
	wide, widest := 0, 0
	for _, count := range perRow {
		if count > 1 {
			wide++
		}
		if count > widest {
			widest = count
		}
	}
	return fmt.Sprintf(
		"\nHow this final screen is arranged (driver fact): its elements sit in %d rows, "+
			"%d of which hold more than one element side by side, the widest %d across. "+
			"Mostly wide rows are a grid; mostly single rows are a list or a timeline.\n",
		len(tops), wide, widest)
}

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
