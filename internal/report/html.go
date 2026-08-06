package report

import (
	"bytes"
	"fmt"
	"html/template"
	"time"
)

// The HTML reports.
//
// specs/03-cli-tooling.md:34 names HTML and HTML-DETAILED and gives the summary
// they present — passed, suites of flows with {name, status, failure, duration,
// tags, steps}, passedCount, totalTests. The markup is an implementation detail;
// the summary fields are the stable surface.
//
// html/template rather than fmt: a flow name and a failure message are arbitrary
// text from a YAML file, and this document is opened in a browser. Every
// interpolation has to be escaped, and a template that escapes by default is the
// only version of that which stays true after the next edit.

// HTMLOptions configures one rendered report.
type HTMLOptions struct {
	// SuiteName titles the report. Blank falls back to a generic title.
	SuiteName string
	// Timestamp is the run's own notion of now. Taken from the caller for the
	// same reason JUnitOptions takes one: two identical runs must render
	// identical bytes.
	Timestamp time.Time
	// Detailed selects HTML-DETAILED, which lists each flow's steps. The step
	// list is the whole difference between the two formats.
	Detailed bool
}

// htmlSummary is what the template sees. Building it here rather than reaching
// into FlowResult from the template keeps the counting in Go, where it is
// testable, and out of template expressions, where an off-by-one is invisible.
type htmlSummary struct {
	Title     string
	Timestamp string
	Total     int
	Passed    int
	Failed    int
	AllPassed bool
	Detailed  bool
	Flows     []htmlFlow
}

type htmlFlow struct {
	Name           string
	Status         string
	Passed         bool
	DurationMillis int64
	FailureMessage string
	FailureDetails string
	Steps          []htmlStep
}

type htmlStep struct {
	Sequence       int64
	Depth          int
	Keyword        string
	Description    string
	Status         string
	Passed         bool
	FailureMessage string
}

// MarshalHTML renders the run as a self-contained document.
//
// Self-contained is a requirement, not a choice: a report is opened from a
// CI artifact bundle, offline, long after the run. Anything fetched over the
// network renders as a broken page at exactly the moment somebody needs it.
func MarshalHTML(options HTMLOptions, flows []FlowResult) ([]byte, error) {
	summary := buildHTMLSummary(options, flows)
	var rendered bytes.Buffer
	if err := htmlReportTemplate.Execute(&rendered, summary); err != nil {
		return nil, fmt.Errorf("report: rendering html: %w", err)
	}
	return rendered.Bytes(), nil
}

func buildHTMLSummary(options HTMLOptions, flows []FlowResult) htmlSummary {
	title := options.SuiteName
	if title == "" {
		title = "flowbaton"
	}
	summary := htmlSummary{
		Title:     title,
		Timestamp: options.Timestamp.UTC().Format(time.RFC3339),
		Total:     len(flows),
		Detailed:  options.Detailed,
		Flows:     make([]htmlFlow, 0, len(flows)),
	}
	for _, flow := range flows {
		converted := htmlFlow{
			Name:           flow.Name,
			Status:         string(flow.Status),
			Passed:         reportedAsPassing(flow.Status),
			DurationMillis: flow.DurationMillis,
		}
		if flow.Failure != nil {
			converted.FailureMessage = flow.Failure.Message
			converted.FailureDetails = flow.Failure.Details
		}
		if converted.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		if options.Detailed {
			converted.Steps = htmlSteps(flow.Commands)
		}
		summary.Flows = append(summary.Flows, converted)
	}
	summary.AllPassed = summary.Failed == 0
	return summary
}

func htmlSteps(commands []CommandResult) []htmlStep {
	steps := make([]htmlStep, 0, len(commands))
	for _, command := range commands {
		step := htmlStep{
			Sequence:    command.Sequence,
			Depth:       command.Depth,
			Keyword:     command.Keyword,
			Description: command.Description,
			Status:      string(command.Status),
			Passed:      reportedAsPassing(command.Status),
		}
		if command.Failure != nil {
			step.FailureMessage = command.Failure.Message
		}
		steps = append(steps, step)
	}
	return steps
}

// reportedAsPassing is an allow-list, matching the runner's exit-code rule: a
// status nobody has classified yet reads as a failure rather than silently
// counting as a pass.
func reportedAsPassing(status Status) bool {
	switch status {
	case Completed, Skipped, Warned:
		return true
	default:
		return false
	}
}

// Tags are part of the summary specs/03-cli-tooling.md:34 describes, and are
// deliberately absent here: nothing carries a flow's tags into FlowResult yet,
// and a column that is always blank is decoration that reads as data.

var htmlReportTemplate = template.Must(template.New("report").Parse(
	`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>{{.Title}} — flowbaton</title>
<style>
:root { color-scheme: light dark; }
body { font: 15px/1.5 system-ui, sans-serif; margin: 0; padding: 2rem; }
h1 { font-size: 1.4rem; margin: 0 0 .25rem; }
.meta { color: #6b7280; font-size: .85rem; margin-bottom: 1.5rem; }
.totals { display: flex; gap: 1.5rem; margin-bottom: 1.5rem; }
.totals div { border: 1px solid #d1d5db; border-radius: .5rem; padding: .5rem 1rem; }
.totals span { display: block; font-size: 1.6rem; font-weight: 600; }
.flow { border: 1px solid #d1d5db; border-radius: .5rem; margin-bottom: 1rem; }
.flow > header { display: flex; align-items: flex-end; gap: .75rem;
  padding: .75rem 1rem; border-bottom: 1px solid #e5e7eb; }
.flow > header h2 { font-size: 1rem; margin: 0; }
.badge { border-radius: 1rem; font-size: .75rem; font-weight: 600;
  padding: .15rem .6rem; }
.pass { background: #dcfce7; color: #166534; }
.fail { background: #fee2e2; color: #991b1b; }
.duration { color: #6b7280; font-size: .8rem; }
.failure { margin: 0; padding: .75rem 1rem; background: #fef2f2; color: #991b1b; }
.failure pre { margin: .35rem 0 0; white-space: pre-wrap; font-size: .8rem; }
table { border-collapse: collapse; width: 100%; }
td, th { border-top: 1px solid #e5e7eb; padding: .35rem 1rem; text-align: left;
  font-size: .85rem; vertical-align: top; }
th { color: #6b7280; font-weight: 500; }
.empty { color: #6b7280; }
</style>
</head>
<body>
<h1>{{.Title}}</h1>
<p class="meta">{{if .Timestamp}}{{.Timestamp}}{{end}}{{if .Detailed}} · detailed{{end}}</p>
<div class="totals">
<div>flows<span>{{.Total}}</span></div>
<div>passed<span>{{.Passed}}</span></div>
<div>failed<span>{{.Failed}}</span></div>
</div>
{{if not .Flows}}<p class="empty">No flows ran.</p>{{end}}
{{range .Flows}}
<section class="flow">
<header>
<span class="badge {{if .Passed}}pass{{else}}fail{{end}}">{{.Status}}</span>
<h2>{{.Name}}</h2>
<span class="duration">{{.DurationMillis}} ms</span>
</header>
{{if .FailureMessage}}<p class="failure">{{.FailureMessage}}{{if .FailureDetails}}<pre>{{.FailureDetails}}</pre>{{end}}</p>{{end}}
{{if .Steps}}
<table>
<tr><th>#</th><th>step</th><th>status</th></tr>
{{range .Steps}}
<tr>
<td>{{.Sequence}}</td>
<td>{{.Keyword}}{{if .Description}} — {{.Description}}{{end}}{{if .FailureMessage}}<pre>{{.FailureMessage}}</pre>{{end}}</td>
<td>{{.Status}}</td>
</tr>
{{end}}
</table>
{{end}}
</section>
{{end}}
</body>
</html>
`))
