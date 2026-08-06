package cli

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Every accepted --format value must produce its documented file.

func TestJUnitReportIsWrittenWhereOutputSaysAndDescribesTheRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"),
		"appId: com.example.a\n---\n- launchApp\n")
	output := filepath.Join(t.TempDir(), "results", "junit.xml")

	stdout, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--format", "JUNIT", "--output", output, "--test-suite-name", "smoke",
		filepath.Join(dir, "passing.yaml"),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	suite := readSuite(t, output)
	if suite.Name != "smoke" {
		t.Fatalf("suite name = %q, want the --test-suite-name value", suite.Name)
	}
	if suite.Tests != 1 || suite.Failures != 0 {
		t.Fatalf("tests = %d failures = %d, want 1 and 0", suite.Tests, suite.Failures)
	}
	if len(suite.Cases) != 1 || suite.Cases[0].Failure != nil {
		t.Fatalf("cases = %#v, want one passing case", suite.Cases)
	}
}

func TestAFailingFlowIsRecordedAsAFailureInTheReport(t *testing.T) {
	t.Parallel()

	// The control for the test above. A reporter that always writes zero
	// failures would satisfy it and be useless.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "failing.yaml"),
		"appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")
	output := filepath.Join(t.TempDir(), "junit.xml")

	_, _, code := runSessionWithArgs(t, emptyScreenDriver(), []string{
		"--format", "JUNIT", "--output", output, filepath.Join(dir, "failing.yaml"),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}

	suite := readSuite(t, output)
	if suite.Failures != 1 {
		t.Fatalf("failures = %d, want 1", suite.Failures)
	}
	if len(suite.Cases) != 1 || suite.Cases[0].Failure == nil {
		t.Fatalf("cases = %#v, want the failure recorded", suite.Cases)
	}
}

func TestTheReportIsWrittenEvenThoughTheRunFailed(t *testing.T) {
	t.Parallel()

	// A report that only appears when everything passed is a report nobody
	// needs. The failing case is the one a CI job opens.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "failing.yaml"),
		"appId: com.example.a\n---\n- assertVisible: NothingIsEverHere\n")
	output := filepath.Join(t.TempDir(), "junit.xml")

	_, _, _ = runSessionWithArgs(t, emptyScreenDriver(), []string{
		"--format", "JUNIT", "--output", output, filepath.Join(dir, "failing.yaml"),
	})
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("no report after a failing run: %v", err)
	}
}

func TestNoopWritesNothingAtAll(t *testing.T) {
	t.Parallel()

	// NOOP is the default, per specs/03-cli-tooling.md:20. It must not create
	// a file just because --output was also given.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	output := filepath.Join(t.TempDir(), "junit.xml")

	_, _, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--output", output, filepath.Join(dir, "passing.yaml"),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(output); err == nil {
		t.Fatal("NOOP wrote a report")
	}
}

// --format without --output writes report.xml into the WORKING DIRECTORY, not
// into the run's artifacts directory:
//
//	$ flowbaton test --format JUNIT .     → ./report.xml
//	$ flowbaton test -p android …       → ~/.flowbaton/tests/<ts>/junit.xml
//
// This keeps the default report path usable by CI publishers.
func TestJUnitWithoutAnOutputLandsInTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	working := t.TempDir()
	t.Chdir(working)

	code := fakeRunner(permissiveDriver(), dir).Run(
		context.Background(),
		[]string{"--format", "JUNIT", filepath.Join(dir, "passing.yaml")},
		discard{}, discard{})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(working, defaultJUnitFileName)); err != nil {
		t.Fatalf("no %s in the working directory: %v", defaultJUnitFileName, err)
	}
}

// --test-output-dir moves run artifacts but leaves the report in the working
// directory.
func TestTheArtifactsDirectoryDoesNotMoveTheReport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	artifacts := t.TempDir()
	working := t.TempDir()
	t.Chdir(working)

	code := fakeRunner(permissiveDriver(), dir).Run(
		context.Background(),
		[]string{"--format", "JUNIT", "--test-output-dir", artifacts,
			filepath.Join(dir, "passing.yaml")},
		discard{}, discard{})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(working, defaultJUnitFileName)); err != nil {
		t.Fatalf("the report followed the artifacts out of the working directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(artifacts, defaultJUnitFileName)); err == nil {
		t.Fatal("the report was also written into the artifacts directory")
	}
}

// A nested flow reports its path relative to the run root. This layer owns that
// path calculation before handing the value to the report package.
func TestANestedFlowReportsItsPathRelativeToTheRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub", "nested.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	writeFile(t, filepath.Join(dir, "config.yaml"), "flows:\n  - \"**/*.yaml\"\n")
	output := filepath.Join(t.TempDir(), "report.xml")

	if _, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--format", "JUNIT", "--output", output, dir,
	}); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	suite := readSuite(t, output)
	if len(suite.Cases) != 1 || suite.Cases[0].File != "sub/nested.yaml" {
		t.Fatalf("case file = %#v, want the path relative to the root", suite.Cases)
	}
}

// CI publishers use the default file name directly.
func TestDefaultJUnitFileName(t *testing.T) {
	t.Parallel()

	if defaultJUnitFileName != "report.xml" {
		t.Fatalf("defaultJUnitFileName = %q, want report.xml", defaultJUnitFileName)
	}
}

func TestBothHTMLFormatsWriteADocument(t *testing.T) {
	t.Parallel()

	// Both HTML formats have concrete writers.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	for _, format := range []string{"HTML", "HTML-DETAILED"} {
		output := filepath.Join(t.TempDir(), "report.html")
		_, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
			"--format", format, "--output", output,
			filepath.Join(dir, "passing.yaml"),
		})
		if code != ExitOK {
			t.Fatalf("--format %s exited %d\nstderr: %s", format, code, stderr)
		}
		page, err := os.ReadFile(output)
		if err != nil {
			t.Fatalf("--format %s wrote no report: %v", format, err)
		}
		if !strings.Contains(string(page), "<!doctype html>") {
			t.Fatalf("--format %s did not write a document:\n%s", format, page)
		}
		if !strings.Contains(string(page), "passing") {
			t.Fatalf("--format %s report does not name the flow:\n%s", format, page)
		}
		// The detailed variant is the one that lists steps; if both rendered the
		// same thing, one of the two flags would be a lie.
		listsSteps := strings.Contains(string(page), "launchApp")
		if listsSteps != (format == "HTML-DETAILED") {
			t.Fatalf("--format %s listsSteps = %v, want %v",
				format, listsSteps, format == "HTML-DETAILED")
		}
	}
}

func TestHTMLWithoutAnOutputLandsInTheWorkingDirectory(t *testing.T) {
	// Each format needs its own default name, or asking for HTML would overwrite
	// a JUnit report — or worse, write XML into a file called .html. The
	// HTML writes ./report.html here, using the same working-directory rule as
	// the JUnit one above.
	dir := t.TempDir()
	working := t.TempDir()
	t.Chdir(working)
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	code := fakeRunner(permissiveDriver(), dir).Run(
		context.Background(),
		[]string{"--format", "HTML", filepath.Join(dir, "passing.yaml")},
		discard{}, discard{})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(filepath.Join(working, defaultHTMLFileName)); err != nil {
		t.Fatalf("no %s in the working directory: %v", defaultHTMLFileName, err)
	}
	if _, err := os.Stat(filepath.Join(working, defaultJUnitFileName)); err == nil {
		t.Fatal("--format HTML also wrote a junit report")
	}
}

func TestAFormatWithNoWriterIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	// Unreachable through the CLI — ParseTestOptions gates --format against the
	// four names the spec gives — so this calls writeReport directly. It is the
	// fail-closed backstop for someone adding a fifth name to that list and
	// forgetting the switch, which would silently write nothing.
	err := writeReport(
		TestOptions{Format: "PDF", Output: filepath.Join(t.TempDir(), "out.pdf")},
		nil, time.Now())
	if err == nil {
		t.Fatal("writeReport() accepted a format it cannot write")
	}
	if !strings.Contains(err.Error(), "PDF") {
		t.Fatalf("error = %q, want it to name the format", err)
	}
}

func TestAnUnwritableReportPathFailsTheRun(t *testing.T) {
	t.Parallel()

	// A run whose report could not be written did not produce what was asked
	// for. Exiting 0 would tell CI it has a report it does not have.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	writeFile(t, blocked, "occupied")

	_, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--format", "JUNIT", "--output", filepath.Join(blocked, "junit.xml"),
		filepath.Join(dir, "passing.yaml"),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d when the report cannot be written", code, ExitFailure)
	}
	if !strings.Contains(stderr, "report") {
		t.Fatalf("stderr = %q, want it to name the report as the reason", stderr)
	}
}

func TestTheReportComesFromTheRunClockNotTheWallClock(t *testing.T) {
	t.Parallel()

	// MarshalJUnit never reads the wall clock. Two runs on the same fixed session
	// clock must produce identical bytes.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	render := func() []byte {
		output := filepath.Join(t.TempDir(), "report.xml")
		runner := fakeRunner(permissiveDriver(), dir)
		runner.Clock = &advancingClock{now: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)}
		if code := runner.Run(
			context.Background(),
			[]string{"--format", "JUNIT", "--output", output,
				"--test-output-dir", t.TempDir(), filepath.Join(dir, "passing.yaml")},
			discard{}, discard{}); code != ExitOK {
			t.Fatalf("exit = %d", code)
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		return data
	}

	if first, second := render(), render(); string(first) != string(second) {
		t.Fatalf("two runs on one fixed clock differ:\n%s\n%s", first, second)
	}
}

// The root is <testsuites> and the failure message is the element's text --
// the shape the contract writes (internal/report/junit_shape_test.go).
type junitDocumentProbe struct {
	XMLName xml.Name          `xml:"testsuites"`
	Suites  []junitSuiteProbe `xml:"testsuite"`
}

type junitSuiteProbe struct {
	Name     string `xml:"name,attr"`
	Tests    int    `xml:"tests,attr"`
	Failures int    `xml:"failures,attr"`
	Cases    []struct {
		Name    string  `xml:"name,attr"`
		File    string  `xml:"file,attr"`
		Status  string  `xml:"status,attr"`
		Failure *string `xml:"failure"`
	} `xml:"testcase"`
}

func readSuite(t *testing.T, path string) junitSuiteProbe {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var document junitDocumentProbe
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode report %s: %v", data, err)
	}
	if len(document.Suites) != 1 {
		t.Fatalf("report holds %d suites, want one: %s", len(document.Suites), data)
	}
	return document.Suites[0]
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestTheReportUsesTheFlowsConfiguredName(t *testing.T) {
	t.Parallel()

	// The report must use the flow's configured name so terminal and report
	// identities remain cross-linkable.
	//
	// The lookup has to survive symlink resolution — a temporary directory
	// reaches discovery as /var/... and comes back from the engine as
	// /private/var/... on macOS — which is what makes this more than a map.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "named.yaml"),
		"appId: com.example.a\nname: checkout-flow\n---\n- launchApp\n")
	output := filepath.Join(t.TempDir(), "junit.xml")

	_, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--format", "JUNIT", "--output", output, filepath.Join(dir, "named.yaml"),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	suite := readSuite(t, output)
	if len(suite.Cases) != 1 || suite.Cases[0].Name != "checkout-flow" {
		t.Fatalf("case name = %#v, want the flow's configured name", suite.Cases)
	}
}

func TestAFlowWithNoConfiguredNameIsNamedAfterItsFile(t *testing.T) {
	t.Parallel()

	// The control: reading the name from somewhere that is always populated
	// would satisfy the test above and rename every unnamed flow.
	//
	// An unnamed flow uses the file stem as its name and keeps the complete file
	// name in the file field.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "unnamed.yaml"), "appId: com.example.a\n---\n- launchApp\n")
	output := filepath.Join(t.TempDir(), "junit.xml")

	if _, _, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--format", "JUNIT", "--output", output, filepath.Join(dir, "unnamed.yaml"),
	}); code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	suite := readSuite(t, output)
	if len(suite.Cases) != 1 || suite.Cases[0].Name != "unnamed" {
		t.Fatalf("case name = %#v, want the file name without its extension", suite.Cases)
	}
	if suite.Cases[0].File != "unnamed.yaml" {
		t.Fatalf("case file = %q, want the file name", suite.Cases[0].File)
	}
}
