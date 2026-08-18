package explore

import "fmt"

// StepLine renders one executed step as a single line: the action, its text or
// direction, the status, whether the screen changed, and any note or error.
//
// The supervisor prompt and the written session log share this rendering, so
// an operator reading the artifact sees the run exactly as the supervisor did.
func StepLine(step StepRecord) string {
	line := fmt.Sprintf("step %d: %s", step.Index+1, step.Action.Kind)
	if step.Action.Text != "" {
		line += fmt.Sprintf(" %q", truncateText(step.Action.Text, 40))
	}
	if step.Action.Direction != "" {
		line += " " + step.Action.Direction
	}
	line += fmt.Sprintf(" status=%s changed=%v", step.Status, !step.After.Same(step.Before))
	if step.Note != "" {
		line += " note=" + truncateText(step.Note, 60)
	}
	if step.ErrText != "" {
		line += " error=" + truncateText(step.ErrText, 60)
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

func truncateText(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}
