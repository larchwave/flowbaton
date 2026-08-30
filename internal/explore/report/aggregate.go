package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
)

// finding is one clustered product defect: several runs failing on the
// same unmet outcome collapse into a single entry.
type finding struct {
	title    string
	severity string
	tests    []string
	repro    []string
	evidence string
}

type execIssue struct {
	test   string
	reason string
}

// unconfirmed clusters runs whose only unmet outcomes the app never
// promised. They are feedback for planning, not defects anyone can fix in
// the app.
type unconfirmed struct {
	expected string
	tests    []string
	evidence string
}

type aggregation struct {
	total    int
	passed   []string
	failed   []string
	findings []finding
	issues   []execIssue
	unsure   []unconfirmed
}

// aggregate walks every result once. Runs that ended stopped or with a
// driver error, and failed runs without any unmet outcome, are execution
// problems, never product defects.
func aggregate(session *explore.SessionReport) aggregation {
	agg := aggregation{total: len(session.Results)}
	byKey := map[string]int{}
	byUnsureKey := map[string]int{}
	for _, result := range session.Results {
		name := result.Scenario.Name
		if result.Status == explore.TestPassed {
			agg.passed = append(agg.passed, name)
			continue
		}
		if result.Status == explore.TestStopped {
			agg.issues = append(agg.issues, execIssue{test: name, reason: stoppedReason(result)})
			continue
		}
		if reason, ended := endedOnAFailedStep(result); ended {
			agg.issues = append(agg.issues, execIssue{test: name, reason: reason})
			continue
		}
		unmet := firstUnmet(result, explore.MissDefect)
		if unmet == nil {
			// A missing verdict outranks an unpromised expectation: the run
			// was not judged, so its remaining classification is a guess.
			if unjudged := firstUnmet(result, explore.MissUnjudged); unjudged != nil {
				agg.issues = append(agg.issues, execIssue{
					test:   name,
					reason: "the judge reached no verdict: " + judgeEvidence(unjudged),
				})
				continue
			}
			if skipped := firstUnmet(result, explore.MissUnpromised); skipped != nil {
				key := normalizeOutcome(skipped.Expected)
				if index, seen := byUnsureKey[key]; seen {
					agg.unsure[index].tests = append(agg.unsure[index].tests, name)
					continue
				}
				byUnsureKey[key] = len(agg.unsure)
				agg.unsure = append(agg.unsure, unconfirmed{
					expected: findingTitle(skipped.Expected),
					tests:    []string{name},
					evidence: judgeEvidence(skipped),
				})
				continue
			}
			agg.issues = append(agg.issues, execIssue{
				test:   name,
				reason: "ended without a product verdict",
			})
			continue
		}
		agg.failed = append(agg.failed, name)
		key := normalizeOutcome(unmet.Expected)
		sev := severity(result.Scenario.Priority)
		if index, seen := byKey[key]; seen {
			existing := &agg.findings[index]
			existing.tests = append(existing.tests, name)
			if severityRank(sev) < severityRank(existing.severity) {
				existing.severity = sev
			}
			continue
		}
		byKey[key] = len(agg.findings)
		agg.findings = append(agg.findings, finding{
			title:    findingTitle(unmet.Expected),
			severity: sev,
			tests:    []string{name},
			repro:    reproSteps(result),
			evidence: evidenceLine(result, unmet),
		})
	}
	sort.SliceStable(agg.findings, func(i, j int) bool {
		return severityRank(agg.findings[i].severity) < severityRank(agg.findings[j].severity)
	})
	return agg
}

func stoppedReason(result explore.TestResult) string {
	if result.Verdict != "" {
		return "run stopped: " + result.Verdict
	}
	return "run stopped before a verdict"
}

// endedOnAFailedStep reports a run whose final step died on an error rather
// than on a failed product check, and names the reason honestly.
//
// The two cases read very differently to whoever triages the report. A device
// or driver error is broken equipment. A missed target is the agent aiming at
// an element the screen does not have: the device answered, and the unmet
// outcome behind it says as much about the agent's route as about the app, so
// it stays out of the defect list rather than becoming a defect nobody can
// reproduce.
func endedOnAFailedStep(result explore.TestResult) (string, bool) {
	if len(result.Steps) == 0 {
		return "", false
	}
	last := result.Steps[len(result.Steps)-1]
	if last.Status != explore.StepFailed || last.ErrText == "" || last.Action.Kind == explore.ActionVerify {
		return "", false
	}
	if last.TargetMiss {
		return "the run ended aiming at something the screen does not have: " +
			truncate(last.ErrText, 120), true
	}
	return "driver error: " + truncate(last.ErrText, 120), true
}

// firstUnmet returns the first unmet scenario outcome that went unmet for
// the given reason. Callers ask for a defect first, so a real one stays
// visible even when an unpromised or unjudged outcome precedes it;
// skipping the run's own check_visible probes keeps a failed probe from
// becoming the finding once that scan walks past the scenario outcomes.
func firstUnmet(result explore.TestResult, reason explore.MissReason) *explore.OutcomeCheck {
	for i := range result.Outcomes {
		if result.Outcomes[i].Driver {
			continue
		}
		if !result.Outcomes[i].Met && result.Outcomes[i].Missed == reason {
			check := result.Outcomes[i]
			return &check
		}
	}
	return nil
}

// normalizeOutcome folds an expected-outcome text into a cluster key:
// lowercase, collapsed whitespace, trailing punctuation trimmed.
func normalizeOutcome(text string) string {
	folded := strings.ToLower(strings.Join(strings.Fields(text), " "))
	return strings.TrimRight(folded, ".!?")
}

// findingTitle words a defect as the miss it is. Printing the expectation on
// its own made the report contradict itself: mmx34 filed "the row no longer
// appears in the list" one line above evidence saying the row was still
// there. The expectation is what was asked for, never a report of what the
// app did.
func findingTitle(expected string) string {
	title := strings.Join(strings.Fields(expected), " ")
	if title == "" {
		return "an expected outcome was not observed"
	}
	return "not observed: " + title
}

func severity(priority explore.Priority) string {
	switch priority {
	case explore.PriorityCritical, explore.PriorityImportant:
		return "High"
	case explore.PriorityHigh:
		return "Medium"
	default:
		return "Low"
	}
}

func severityRank(severity string) int {
	switch severity {
	case "High":
		return 0
	case "Medium":
		return 1
	default:
		return 2
	}
}

// reproSteps renders the step log as imperative lines.
func reproSteps(result explore.TestResult) []string {
	lines := make([]string, 0, len(result.Steps))
	for _, step := range result.Steps {
		line := describeAction(step.Action)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func describeAction(action explore.Action) string {
	target := describeTarget(action.Target)
	switch action.Kind {
	case explore.ActionTap:
		return "Tap " + target
	case explore.ActionLongPress:
		return "Long-press " + target
	case explore.ActionInput:
		return fmt.Sprintf("Type %q", action.Text)
	case explore.ActionErase:
		return "Erase the field text"
	case explore.ActionSwipe:
		return strings.TrimSpace("Swipe " + strings.ToLower(action.Direction))
	case explore.ActionScroll:
		return strings.TrimSpace("Scroll " + strings.ToLower(action.Direction))
	case explore.ActionBack:
		return "Go back"
	case explore.ActionPressKey:
		return "Press " + action.Text
	case explore.ActionHideKeys:
		return "Hide the keyboard"
	case explore.ActionOpenLink:
		return "Open " + action.Text
	case explore.ActionLaunch:
		return "Launch the app"
	case explore.ActionStop:
		return "Stop the app"
	case explore.ActionVerify:
		if action.Target != nil {
			return "Check " + target
		}
		return fmt.Sprintf("Check that %q is shown", action.Text)
	case explore.ActionWait:
		return ""
	}
	return string(action.Kind)
}

func describeTarget(locator *explore.Locator) string {
	if locator == nil {
		return "the screen"
	}
	switch locator.Kind {
	case explore.LocatorID:
		return fmt.Sprintf("the element with id %q", locator.Value)
	case explore.LocatorText:
		return fmt.Sprintf("%q", locator.Value)
	case explore.LocatorPoint:
		return "the point " + locator.Value
	default:
		return "the element at path " + locator.Value
	}
}

// judgeEvidence quotes only what the judge said. An unconfirmed
// expectation must not borrow a driver error from the step log: that
// sentence reads as a defect under a heading that says there is none.
func judgeEvidence(check *explore.OutcomeCheck) string {
	if check.Evidence == "" {
		return "the judge recorded no evidence"
	}
	return truncate(check.Evidence, 160)
}

func evidenceLine(result explore.TestResult, unmet *explore.OutcomeCheck) string {
	if unmet.Evidence != "" {
		return truncate(unmet.Evidence, 160)
	}
	for i := len(result.Steps) - 1; i >= 0; i-- {
		step := result.Steps[i]
		if step.Status != explore.StepFailed {
			continue
		}
		if step.Note != "" {
			return truncate(step.Note, 160)
		}
		if step.ErrText != "" {
			return truncate(step.ErrText, 160)
		}
	}
	return "no evidence recorded"
}

func truncate(text string, max int) string {
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
