package explore

import (
	"fmt"
	"unicode/utf8"
)

// StepLine renders one executed step as a single line: the action, its text or
// direction, the status, whether the screen changed, and any note or error.
//
// The supervisor prompt and the written session log share this rendering, so
// an operator reading the artifact sees the run exactly as the supervisor did.
func StepLine(step StepRecord) string {
	line := fmt.Sprintf("step %d: %s", step.Index+1, step.Action.Kind)
	if step.Action.Text != "" {
		line += fmt.Sprintf(" %q", Truncate(step.Action.Text, 40))
	}
	if step.Action.Direction != "" {
		line += " " + step.Action.Direction
	}
	line += fmt.Sprintf(" status=%s changed=%v", step.Status, !step.After.Same(step.Before))
	if step.Note != "" {
		line += " note=" + Truncate(step.Note, 60)
	}
	if step.ErrText != "" {
		line += " error=" + Truncate(step.ErrText, 60)
	}
	return line
}

// StepLines renders every step of a run. An empty run says so rather than
// answering with nothing, because a caller that prints the lines would
// otherwise show a heading with no body.
func StepLines(steps []StepRecord) []string {
	lines := make([]string, 0, len(steps))
	for _, step := range steps {
		lines = append(lines, StepLine(step))
	}
	if len(lines) == 0 {
		lines = append(lines, "(no steps executed yet)")
	}
	return lines
}

// Truncate shortens a value to max characters, marking the cut with an
// ellipsis. It counts runes rather than bytes: a byte offset lands inside a
// multi-byte character for any label the device shows in a non-Latin script,
// and %q then escapes the broken tail into \xNN, so the corruption reaches
// the artifact looking like data.
//
// Every consumer of device text shortens it somewhere -- the element table
// the model reads, the step log, the session report, a failure cause -- and
// each one grew its own copy of this until they were four. One is enough.
func Truncate(value string, max int) string {
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	return string([]rune(value)[:max]) + "…"
}
