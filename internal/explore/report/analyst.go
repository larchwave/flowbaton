// Package report renders a finished exploration session into a markdown
// summary and aggregates token spend across the session.
package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Analyst renders one exploration session into a markdown report. The
// aggregation, clustering, and section layout are deterministic; a single
// manager-model call adds a short prose headline. Any model failure other
// than a context error falls back to the deterministic headline so
// rendering always succeeds.
type Analyst struct {
	// Manager writes the headline prose. Nil keeps the deterministic
	// headline without any model call.
	Manager explore.LLM
}

var _ explore.Analyst = Analyst{}

// Report renders the session markdown.
func (a Analyst) Report(ctx context.Context, session *explore.SessionReport) (string, error) {
	if session == nil {
		return "", errors.New("report: nil session")
	}
	agg := aggregate(session)
	headline := deterministicHeadline(session, agg)
	if a.Manager != nil {
		prose, err := a.headlineFromModel(ctx, session, agg)
		if err != nil {
			return "", err
		}
		if prose != "" {
			headline = prose
		}
	}
	return render(headline, session, agg), nil
}

const headlineSystemPrompt = "You summarize the product state of a mobile app " +
	"after an automated exploration session. Reply with one or two sentences " +
	"about how the app behaves for its users, grounded only in the supplied " +
	"findings. Do not mention scenario counts, tooling, or the session itself."

// headlineFromModel asks the manager model for the headline. Context errors
// propagate; every other failure (provider error, empty reply) returns an
// empty headline so the caller keeps the deterministic one.
func (a Analyst) headlineFromModel(ctx context.Context, session *explore.SessionReport, agg aggregation) (string, error) {
	response, err := a.Manager.Chat(ctx, explore.ChatRequest{
		Messages: []explore.Message{
			{Role: explore.RoleSystem, Text: headlineSystemPrompt},
			{Role: explore.RoleUser, Text: headlineDigest(session, agg)},
		},
		MaxTokens: 300,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", nil
	}
	return strings.TrimSpace(response.Message.Text), nil
}

func headlineDigest(session *explore.SessionReport, agg aggregation) string {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "App: %s (%s)\n", session.AppID, session.Platform)
	if len(agg.passed) > 0 {
		builder.WriteString("Working flows:\n")
		for _, name := range agg.passed {
			fmt.Fprintf(builder, "- %s\n", name)
		}
	}
	if len(agg.findings) > 0 {
		builder.WriteString("Defects:\n")
		for _, f := range agg.findings {
			fmt.Fprintf(builder, "- [%s] %s\n", f.severity, f.title)
		}
	}
	if len(agg.unsure) > 0 {
		builder.WriteString("Expectations this app never promised (scenario wording, not defects):\n")
		for _, item := range agg.unsure {
			fmt.Fprintf(builder, "- %s\n", item.expected)
		}
	}
	if len(agg.issues) > 0 {
		fmt.Fprintf(builder, "Automation problems kept %d runs from a verdict.\n", len(agg.issues))
	}
	return builder.String()
}

func deterministicHeadline(session *explore.SessionReport, agg aggregation) string {
	if agg.total == 0 {
		return fmt.Sprintf("No scenarios ran against %s.", session.AppID)
	}
	headline := fmt.Sprintf(
		"%s on %s: %d of %d scenarios passed, %s, %s with execution problems",
		session.AppID, session.Platform, len(agg.passed), agg.total,
		count(len(agg.findings), "defect finding"), count(len(agg.issues), "run"),
	)
	if len(agg.unsure) > 0 {
		headline += fmt.Sprintf(", %d with an expectation the app never promised", len(agg.unsure))
	}
	return headline + "."
}

func render(headline string, session *explore.SessionReport, agg aggregation) string {
	builder := &strings.Builder{}
	builder.WriteString(headline)
	builder.WriteString("\n")
	builder.WriteString("\n## Coverage\n\n")
	fmt.Fprintf(builder,
		"%s against %s on %s: %d passed, %d failed, %d with execution problems, "+
			"%d with an expectation the app never promised.\n",
		count(agg.total, "scenario run"), session.AppID, session.Platform,
		len(agg.passed), len(agg.failed), len(agg.issues), len(agg.unsure))
	if len(agg.passed) > 0 {
		builder.WriteString("\n## What works\n\n")
		for _, name := range agg.passed {
			fmt.Fprintf(builder, "- `%s`\n", name)
		}
	}
	if len(agg.findings) > 0 {
		builder.WriteString("\n## Defects\n\n")
		for _, f := range agg.findings {
			fmt.Fprintf(builder, "- [%s] %s (tests: %s)\n", f.severity, f.title, backticked(f.tests))
			if len(f.repro) > 0 {
				builder.WriteString("  Reproduce:\n")
				for i, line := range f.repro {
					fmt.Fprintf(builder, "  %d. %s\n", i+1, line)
				}
			}
			fmt.Fprintf(builder, "  Evidence: %s\n", f.evidence)
		}
	}
	if len(agg.unsure) > 0 {
		builder.WriteString("\n## Unconfirmed expectations\n\n")
		builder.WriteString("The app never promised these, so they are scenario wording to fix, not defects.\n\n")
		for _, item := range agg.unsure {
			fmt.Fprintf(builder, "- %s (tests: %s)\n  Evidence: %s\n",
				item.expected, backticked(item.tests), item.evidence)
		}
	}
	if len(agg.issues) > 0 {
		builder.WriteString("\n## Execution issues\n\n")
		for _, issue := range agg.issues {
			fmt.Fprintf(builder, "- `%s`: %s\n", issue.test, issue.reason)
		}
	}
	return builder.String()
}

// count words a tally with its noun, pluralizing the regular way. The
// report's counts are read by people, and "1 runs" reads as a bug in the
// tool that wrote it.
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func backticked(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, "`"+name+"`")
	}
	return strings.Join(quoted, ", ")
}
