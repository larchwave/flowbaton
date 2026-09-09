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
				{Sequence: 3, Keyword: "stopLogCapture", Status: Completed, Artifacts: []Artifact{{
					Kind: "device-log", Path: "/run/fixture-console.ndjson",
					Metadata: map[string]string{"source": "unified-log", "scope": "app", "appId": "com.example", "bytes": "8192"},
				}}},
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
	for _, want := range []string{"launchApp", "tapOn", "Tap on Buy", "assertVisible",
		// A step's artifact is listed with what it is, not just where it is,
		// and the where is a link a reader can open.
		"device-log", `<a href="/run/fixture-console.ndjson">`, "/run/fixture-console.ndjson",
		"unified-log", "scope app com.example", "8192 bytes"} {
		if !strings.Contains(detailed, want) {
			t.Fatalf("the detailed report is missing %q", want)
		}
	}
	if strings.Contains(plain, "fixture-console") {
		t.Fatalf("the summary report listed an artifact:\n%s", plain)
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
			// An artifact path is a file name a flow chose; it lands in an href.
			Artifacts: []Artifact{{Kind: "screenshot", Path: `"><script>alert(5)</script>.txt`}},
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

func TestArtifactLinksAreRelativeToTheReportDirectory(t *testing.T) {
	t.Parallel()

	// The report is written next to nothing in particular: --output names a
	// file while the run directory holds the artifacts. A link that works is
	// one relative to the file the browser opened, and a screenshot is shown
	// inline so a before/after pair reads without leaving the page.
	root := t.TempDir()
	flows := []FlowResult{{
		Name: "shots", Status: Completed,
		Commands: []CommandResult{{
			Sequence: 1, Keyword: "takeScreenshot", Status: Completed,
			Artifacts: []Artifact{
				{Kind: "screenshot", Path: root + "/run/before.png"},
				{Kind: "recording", Path: root + "/run/clip.mp4"},
			},
		}},
	}}
	page := renderHTML(t, HTMLOptions{Detailed: true, Directory: root + "/reports"}, flows)
	for _, want := range []string{
		`<a href="../run/before.png"><code>`,
		`<img class="thumb" src="../run/before.png"`,
		`<a href="../run/clip.mp4"><code>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("report is missing %q:\n%s", want, page)
		}
	}
	if strings.Count(page, "<img") != 1 {
		t.Fatalf("want exactly one inline image (the recording is a link, not an image):\n%s", page)
	}

	// A hostile image name is escaped inside the src, never a tag of its own.
	hostile := []FlowResult{{
		Name: "x", Status: Completed,
		Commands: []CommandResult{{Sequence: 1, Keyword: "takeScreenshot", Status: Completed,
			Artifacts: []Artifact{{Kind: "screenshot", Path: `javascript:alert(6)//.png`}}}},
	}}
	page = renderHTML(t, HTMLOptions{Detailed: true}, hostile)
	for _, forbidden := range []string{`href="javascript`, `src="javascript`} {
		if strings.Contains(page, forbidden) {
			t.Fatalf("a javascript: path reached an attribute:\n%s", page)
		}
	}
	if !strings.Contains(page, "#ZgotmplZ") {
		t.Fatalf("the unsafe link was not neutralised by the template:\n%s", page)
	}
	if strings.Count(page, "<img") != 1 {
		t.Fatalf("want the hostile image escaped, not dropped:\n%s", page)
	}
}
