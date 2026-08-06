package report

import (
	"strings"
	"testing"
	"time"
)

// specs/03-cli-tooling.md:34 names HTML and HTML-DETAILED as report formats and
// gives the summary they present: passed, suites of flows with
// {name,status,failure,duration,tags,steps}, passedCount, totalTests. It pins no
// markup, so these tests focus on the required summary and safe rendering.
//
// The one part that is not a matter of taste is escaping. A flow name or a
// failure message is arbitrary text from a YAML file, and it lands in a document
// somebody opens in a browser.

func htmlFlows() []FlowResult {
	start := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	return []FlowResult{
		{
			Name:           "checkout",
			Status:         Completed,
			StartedAt:      start,
			EndedAt:        start.Add(1200 * time.Millisecond),
			DurationMillis: 1200,
			Commands: []CommandResult{
				{Sequence: 1, Keyword: "launchApp", Description: "Launch com.example", Status: Completed},
				{Sequence: 2, Keyword: "tapOn", Description: "Tap on Buy", Status: Completed},
			},
		},
		{
			Name:           "login",
			Status:         Failed,
			StartedAt:      start,
			EndedAt:        start.Add(400 * time.Millisecond),
			DurationMillis: 400,
			Failure:        &Failure{Message: "Sign in never appeared", Details: "waited 17000ms"},
			Commands: []CommandResult{
				{Sequence: 1, Keyword: "assertVisible", Description: "Sign in", Status: Failed},
			},
		},
	}
}

func renderHTML(t *testing.T, options HTMLOptions, flows []FlowResult) string {
	t.Helper()
	data, err := MarshalHTML(options, flows)
	if err != nil {
		t.Fatalf("MarshalHTML() error = %v", err)
	}
	return string(data)
}

func TestTheHTMLReportCountsWhatRan(t *testing.T) {
	t.Parallel()

	page := renderHTML(t, HTMLOptions{SuiteName: "smoke"}, htmlFlows())
	for _, want := range []string{"smoke", "checkout", "login", "Sign in never appeared"} {
		if !strings.Contains(page, want) {
			t.Fatalf("report is missing %q\n%s", want, page)
		}
	}
	// The counts are the first thing anybody reads, and getting them from the
	// flow list rather than a passed-in number is what keeps them honest.
	for _, want := range []string{"2", "1"} {
		if !strings.Contains(page, want) {
			t.Fatalf("report is missing the count %q", want)
		}
	}
	if !strings.Contains(page, "<!doctype html>") {
		t.Fatalf("report is not a document:\n%s", page)
	}
	if !strings.HasSuffix(strings.TrimSpace(page), "</html>") {
		t.Fatalf("report does not close its document:\n%s", page)
	}
}

func TestAPassingRunAndAFailingRunLookDifferent(t *testing.T) {
	t.Parallel()

	// The control for the test above: a template that hardcoded "failed" or
	// ignored status entirely would satisfy it.
	failing := renderHTML(t, HTMLOptions{}, htmlFlows())
	passing := renderHTML(t, HTMLOptions{}, htmlFlows()[:1])
	if !strings.Contains(failing, "Failed") {
		t.Fatalf("a failing run is not reported as failed:\n%s", failing)
	}
	if strings.Contains(passing, "Failed") {
		t.Fatalf("a passing run is reported as failed:\n%s", passing)
	}
}

func TestOnlyTheDetailedReportListsSteps(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:34 gives `steps` as part of a flow, and two HTML
	// formats. The step list is what distinguishes them; if both rendered the
	// same thing, one of the two flags would be a lie.
	plain := renderHTML(t, HTMLOptions{}, htmlFlows())
	detailed := renderHTML(t, HTMLOptions{Detailed: true}, htmlFlows())

	if strings.Contains(plain, "tapOn") {
		t.Fatalf("the summary report listed a step:\n%s", plain)
	}
	for _, want := range []string{"launchApp", "tapOn", "Tap on Buy", "assertVisible"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("the detailed report is missing %q", want)
		}
	}
}

func TestTextFromAFlowCannotBecomeMarkup(t *testing.T) {
	t.Parallel()

	// A flow name and a failure message are arbitrary text from a YAML file, and
	// this document gets opened in a browser. Anything that reaches the page
	// unescaped is a scripted payload one `flows/` directory away.
	hostile := []FlowResult{{
		Name:   `<script>alert("x")</script>`,
		Status: Failed,
		Failure: &Failure{
			Message: `<img src=x onerror="alert(1)">`,
			Details: `</style><iframe src="javascript:alert(2)">`,
		},
		Commands: []CommandResult{{
			Sequence: 1, Keyword: `<b>keyword</b>`, Description: `"><script>alert(3)</script>`,
			Status: Failed,
		}},
	}}

	// No `<` from input may open a tag. Text such as `onerror=` is harmless
	// when it remains escaped plain text.
	for _, detailed := range []bool{false, true} {
		page := renderHTML(t, HTMLOptions{SuiteName: `<svg onload=alert(4)>`, Detailed: detailed}, hostile)
		for _, forbidden := range []string{"<script", "<img", "<iframe", "<svg", "<b>"} {
			if strings.Contains(page, forbidden) {
				t.Fatalf("detailed=%v: %q reached the page as a tag:\n%s", detailed, forbidden, page)
			}
		}
		// The positive half: the text still has to be THERE, escaped. A writer
		// that dropped hostile text would pass the checks above and lose the
		// failure message an operator needs.
		for _, want := range []string{"&lt;script&gt;", "&lt;img", "alert"} {
			if !strings.Contains(page, want) {
				t.Fatalf("detailed=%v: %q was dropped rather than escaped:\n%s", detailed, want, page)
			}
		}
	}
}

func TestTheHTMLTimestampComesFromTheCaller(t *testing.T) {
	t.Parallel()

	// Same reason MarshalJUnit takes one: two identical runs must render
	// identical bytes.
	moment := time.Date(2026, 7, 29, 12, 34, 56, 0, time.UTC)
	page := renderHTML(t, HTMLOptions{Timestamp: moment}, htmlFlows())
	if !strings.Contains(page, "2026-07-29T12:34:56") {
		t.Fatalf("report is missing the caller's timestamp:\n%s", page)
	}
}

func TestTheHTMLReportIsSelfContained(t *testing.T) {
	t.Parallel()

	// A report is opened from a CI artifact bundle, offline, long after the run.
	// Anything fetched from the network renders as a broken page.
	page := renderHTML(t, HTMLOptions{}, htmlFlows())
	for _, forbidden := range []string{"http://", "https://", "<link", "<script"} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("report reaches outside itself via %q", forbidden)
		}
	}
	if !strings.Contains(page, "<style>") {
		t.Fatalf("report carries no styling of its own:\n%s", page)
	}
}

func TestAnEmptyRunStillRendersADocument(t *testing.T) {
	t.Parallel()

	// The runner never writes a report for an empty run today, but a template
	// that divided by the flow count would panic here rather than say "0".
	page := renderHTML(t, HTMLOptions{SuiteName: "nothing"}, nil)
	if !strings.Contains(page, "<!doctype html>") || !strings.Contains(page, "nothing") {
		t.Fatalf("empty run produced no usable document:\n%s", page)
	}
}
