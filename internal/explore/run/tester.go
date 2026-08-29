package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// stallThreshold is how many consecutive stalled steps (failed or screen
// unchanged) trigger a warning to the model.
const stallThreshold = 3

// cycleThreshold is how many consecutive steps bouncing between the same two
// screens trigger a warning. Three is the shortest run that shows a repeat:
// the there-and-back a scenario may legitimately need is two steps, and a
// third screen resets the count either way. A run gets few turns, so waiting
// longer spends the whole budget on the loop it is meant to break.
const cycleThreshold = 3

// Tester executes one scenario as a bounded tool loop on the worker model,
// optionally supervised by a pilot conversation on the manager model.
type Tester struct {
	Driver   device.Driver
	Observer explore.Observer
	Models   explore.ModelSet
	Config   explore.Config
	// Sleep is injected by tests; nil means real sleeping.
	Sleep func(context.Context, time.Duration) error
	// SettleTimeout bounds the post-action settle; zero means the default.
	SettleTimeout time.Duration
}

var _ explore.Tester = (*Tester)(nil)

// RunScenario drives the scenario until the model finishes, the pilot stops
// the run, the step budget runs out, or the context ends. A non-nil result
// is returned alongside any error so partial evidence is never lost.
func (t *Tester) RunScenario(ctx context.Context, scenario explore.Scenario, start *explore.ScreenState) (*explore.TestResult, error) {
	if t.Models.Worker == nil {
		return nil, explore.ErrNoAIProvider
	}
	if t.Config.MaxStepsPerTest <= 0 {
		return nil, errors.New("explore/run: Config.MaxStepsPerTest must be positive")
	}
	if start == nil {
		observed, err := t.Observer.Observe(ctx)
		if err != nil {
			return nil, fmt.Errorf("explore/run: initial observation: %w", err)
		}
		start = observed
	}
	deps := toolDeps{
		driver:        t.Driver,
		observer:      t.Observer,
		appID:         t.Config.AppID,
		sleep:         t.Sleep,
		now:           t.Config.Now,
		settleTimeout: t.SettleTimeout,
	}
	session, err := newToolSession(deps, start)
	if err != nil {
		return nil, err
	}
	box := session.box()
	result := &explore.TestResult{Scenario: scenario, Status: explore.TestStopped, Started: t.Config.Now()}
	conversation := []explore.Message{
		{Role: explore.RoleSystem, Text: testerSystemText()},
		{Role: explore.RoleUser, Text: scenarioText(scenario, t.Config.SessionName, t.Config.MaxStepsPerTest, session.current)},
	}
	var supervisor *pilot
	if t.Config.PilotEnabled && t.Models.Manager != nil {
		supervisor = newPilot(t.Models.Manager, scenario)
	}
	reviewed := 0
	warnedAt := 0
	pilotVerdict := ""
	stoppedByPilot := false

	for turn := 0; turn < t.Config.MaxStepsPerTest && session.finish == nil && !stoppedByPilot; turn++ {
		if remaining := t.Config.MaxStepsPerTest - turn; remaining == budgetWarningAt && turn > 0 {
			conversation = append(conversation, explore.Message{Role: explore.RoleUser, Text: budgetWarning(remaining)})
		}
		loop, loopErr := explore.RunToolLoop(ctx, t.Models.Worker, conversation, box, 1)
		conversation = pruneElementTables(loop.Messages)
		if loopErr != nil {
			t.fill(result, session)
			return result, fmt.Errorf("explore/run: tester turn %d: %w", turn+1, loopErr)
		}
		if loop.Stopped {
			break
		}
		if !loop.Exhausted {
			conversation = append(conversation, explore.Message{
				Role: explore.RoleUser,
				Text: "Act through tools only; call finish when the scenario is done.",
			})
		}
		if streak := trailingStalls(session.steps); streak >= stallThreshold && streak%stallThreshold == 0 && warnedAt != len(session.steps) {
			warnedAt = len(session.steps)
			conversation = append(conversation, explore.Message{Role: explore.RoleUser, Text: stallWarning(session.steps, streak)})
		} else if cycle := trailingCycle(session.steps); cycle >= cycleThreshold && cycle%cycleThreshold == 0 && warnedAt != len(session.steps) {
			warnedAt = len(session.steps)
			conversation = append(conversation, explore.Message{Role: explore.RoleUser, Text: cycleWarning(cycle)})
		}
		if supervisor != nil && len(session.steps)-reviewed >= pilotCadence {
			lines := stepLog(session.steps[reviewed:])
			reviewed = len(session.steps)
			order, reviewErr := supervisor.review(ctx, lines, false)
			if reviewErr != nil {
				t.fill(result, session)
				return result, fmt.Errorf("explore/run: %w", reviewErr)
			}
			if order.stop() {
				stoppedByPilot = true
				pilotVerdict = order.Verdict
			} else if order.Instruction != "" {
				conversation = append(conversation, explore.Message{Role: explore.RoleUser, Text: "supervisor: " + order.Instruction})
			}
		}
	}

	checks := evaluateOutcomes(ctx, t.Models.Worker, scenario.Expected, session.current, session.finish, session.checksOnFinalScreen(), typedTexts(session.steps))
	t.fill(result, session)
	result.Outcomes = append(append([]explore.OutcomeCheck(nil), checks...), session.checks...)
	switch {
	case stoppedByPilot:
		result.Status = explore.TestStopped
		result.Verdict = "pilot stop: " + pilotVerdict
	case session.finish == nil:
		result.Status = explore.TestStopped
		result.Notes = append(result.Notes, "step budget exhausted before finish")
		result.Verdict = "stopped: the step budget ran out before the model finished"
	case session.finish.Status == "passed" && allMet(checks):
		result.Status = explore.TestPassed
		result.Verdict = fmt.Sprintf("passed: all %d expected outcome(s) met", len(checks))
	case session.finish.Status == "passed":
		result.Status = explore.TestFailed
		result.Verdict = fmt.Sprintf("failed: %d of %d expected outcome(s) unmet", unmetCount(checks), len(checks))
	default:
		result.Status = explore.TestFailed
		result.Verdict = "failed: the model reported failure"
	}
	if supervisor != nil && !stoppedByPilot {
		order, reviewErr := supervisor.review(ctx, stepLog(session.steps[reviewed:]), true)
		if reviewErr != nil {
			return result, fmt.Errorf("explore/run: %w", reviewErr)
		}
		if order.Verdict != "" {
			result.Verdict += "; pilot: " + order.Verdict
		}
	}
	result.Finished = t.Config.Now()
	return result, nil
}

// fill copies the session evidence into the result so every exit path
// carries steps, notes, checks, and a finish time.
func (t *Tester) fill(result *explore.TestResult, session *toolSession) {
	result.Steps = append([]explore.StepRecord(nil), session.steps...)
	result.Notes = append([]string(nil), session.notes...)
	if result.Outcomes == nil {
		result.Outcomes = append([]explore.OutcomeCheck(nil), session.checks...)
	}
	result.Finished = t.Config.Now()
}

func trailingStalls(steps []explore.StepRecord) int {
	count := 0
	for index := len(steps) - 1; index >= 0; index-- {
		status := steps[index].Status
		if status != explore.StepFailed && status != explore.StepNoChange {
			break
		}
		count++
	}
	return count
}

// trailingCycle counts the trailing steps that only move between two screens.
// Each one changes the screen, so the stall count never rises, yet the run is
// as stuck as a screen that never moves.
func trailingCycle(steps []explore.StepRecord) int {
	seen := []explore.ScreenSignature{}
	known := func(sig explore.ScreenSignature) bool {
		for _, other := range seen {
			if other.Same(sig) {
				return true
			}
		}
		if len(seen) == 2 {
			return false
		}
		seen = append(seen, sig)
		return true
	}
	count := 0
	for index := len(steps) - 1; index >= 0; index-- {
		step := steps[index]
		if step.Status != explore.StepOK || !known(step.After) || !known(step.Before) {
			break
		}
		count++
	}
	if len(seen) < 2 {
		return 0
	}
	return count
}

// budgetWarningAt is how many steps are left when the model is reminded:
// enough for one check_visible and the finish call, no more.
const budgetWarningAt = 2

func budgetWarning(remaining int) string {
	return fmt.Sprintf(
		"Budget: %d steps left. A run that ends without finish is stopped, not passed. "+
			"Use them to check_visible what you can and call finish with the outcomes as they stand.",
		remaining)
}

func cycleWarning(cycle int) string {
	return fmt.Sprintf(
		"Warning: the last %d steps only moved back and forth between two screens. "+
			"Repeating that pair proves nothing: pick an element neither screen has "+
			"used yet, or finish the scenario with what you have.",
		cycle)
}

func stallWarning(steps []explore.StepRecord, streak int) string {
	failed := 0
	unchanged := 0
	for _, step := range steps[len(steps)-streak:] {
		if step.Status == explore.StepFailed {
			failed++
		} else {
			unchanged++
		}
	}
	return fmt.Sprintf(
		"Warning: the last %d steps made no progress (%d failed, %d left the screen unchanged). "+
			"Change the approach: pick a different element, scroll to reveal more, or go back.",
		streak, failed, unchanged)
}

func unmetCount(checks []explore.OutcomeCheck) int {
	count := 0
	for _, check := range checks {
		if !check.Met {
			count++
		}
	}
	return count
}

func testerSystemText() string {
	return "You run one exploratory test scenario on a mobile app, acting only through the given tools. " +
		"Ground every action in the newest element table; older tables are stale. " +
		"Each action's result tells you whether the screen changed; a step that changed nothing is not a success. " +
		"Never repeat an action that just failed or changed nothing. " +
		"Use check_visible to gather evidence for expected outcomes. " +
		"When the scenario goal is done, or clearly cannot be done, call finish with a status and one entry per expected outcome, " +
		"citing what you saw as evidence. Keep notes short."
}

func scenarioText(scenario explore.Scenario, sessionName string, budget int, start *explore.ScreenState) string {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "Scenario: %s\n", scenario.Name)
	fmt.Fprintf(builder, "Budget: %d steps (every tool call counts); finish must be one of them.\n", budget)
	if len(scenario.Steps) > 0 {
		builder.WriteString("Suggested steps (guidance, adapt to the live app):\n")
		for _, step := range scenario.Steps {
			fmt.Fprintf(builder, "- %s\n", step)
		}
	}
	if len(scenario.Expected) > 0 {
		builder.WriteString("Expected outcomes (verify each, never weaken them):\n")
		for _, want := range scenario.Expected {
			fmt.Fprintf(builder, "- %s\n", want)
		}
	}
	if sessionName != "" {
		fmt.Fprintf(builder, "Tag any data you create with %q so it can be identified later.\n", sessionName)
	}
	builder.WriteString("\n")
	builder.WriteString(elementTable(start))
	return builder.String()
}
