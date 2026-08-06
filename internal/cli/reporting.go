package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/model"
	"github.com/nohavewho/flowbaton/internal/report"
)

// Writing the run report.
//
// specs/03-cli-tooling.md:20 names four formats and gives NOOP as the default.
// Every accepted format is wired to a file so CI cannot report success without
// producing the requested output.

// Reports land in the working directory under these names. --test-output-dir
// moves run artifacts but does not relocate the report.
const (
	defaultJUnitFileName = "report.xml"
	defaultHTMLFileName  = "report.html"
)

// writeReport renders the run in the requested format.
//
// It is called for a failing run as well as a passing one — the failing report
// is the one anybody actually opens.
func writeReport(
	options TestOptions,
	results []engine.FlowResult,
	now time.Time,
) error {
	switch strings.ToUpper(options.Format) {
	case "NOOP", "":
		// The default. NOOP writes nothing even when --output was given,
		// because the operator did not ask for a format.
		return nil
	case "JUNIT":
		return writeJUnitReport(options, results, now)
	case "HTML":
		return writeHTMLReport(options, results, now, false)
	case "HTML-DETAILED":
		return writeHTMLReport(options, results, now, true)
	default:
		// ParseTestOptions already refuses an unknown --format, so reaching here
		// means a format was added to the accepted list and not to this switch.
		// Refusing beats writing nothing.
		return fmt.Errorf(
			"report format %s is accepted but has no writer", strings.ToUpper(options.Format))
	}
}

func writeJUnitReport(
	options TestOptions,
	results []engine.FlowResult,
	now time.Time,
) error {
	flows, err := convertFlows(options, results)
	if err != nil {
		return err
	}

	// report.MarshalJUnit supplies "Test Suite" for an unnamed run.
	data, err := report.MarshalJUnit(
		report.JUnitOptions{SuiteName: options.TestSuiteName, Timestamp: now}, flows)
	if err != nil {
		return fmt.Errorf("report: rendering junit: %w", err)
	}
	return writeReportFile(reportPath(options, defaultJUnitFileName), data)
}

// writeHTMLReport renders the human-facing report. HTML-DETAILED is the same
// document with each flow's steps listed, which is the whole difference between
// the two formats specs/03-cli-tooling.md:20 names.
func writeHTMLReport(
	options TestOptions,
	results []engine.FlowResult,
	now time.Time,
	detailed bool,
) error {
	flows, err := convertFlows(options, results)
	if err != nil {
		return err
	}
	suiteName := options.TestSuiteName
	if suiteName == "" {
		suiteName = "flowbaton"
	}
	data, err := report.MarshalHTML(report.HTMLOptions{
		SuiteName: suiteName,
		Timestamp: now,
		Detailed:  detailed,
	}, flows)
	if err != nil {
		return fmt.Errorf("report: rendering html: %w", err)
	}
	return writeReportFile(reportPath(options, defaultHTMLFileName), data)
}

func writeReportFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("report: creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("report: writing %s: %w", path, err)
	}
	return nil
}

// convertFlows projects engine results onto the report model, once, for every
// format.
func convertFlows(options TestOptions, results []engine.FlowResult) ([]report.FlowResult, error) {
	// The contract reports a nested flow as sub/alpha.yaml — the path relative
	// to the run root, not the base name. Only this layer knows the root.
	base := baseDirectoryFor(options.Roots)
	flows := make([]report.FlowResult, 0, len(results))
	for _, result := range results {
		// No config is passed on purpose: the engine result carries the flow's
		// authored name, and FromEngineFlowResult prefers it over the file's
		// base name. Rebuilding a config here would mean matching plan paths
		// against result paths, which the loader canonicalizes — two places
		// that would have to agree forever.
		converted, err := report.FromEngineFlowResult(result, model.Config{})
		if err != nil {
			return nil, fmt.Errorf("report: converting %s: %w", result.Path(), err)
		}
		if relative, relErr := relativeFlowPath(base, result.Path()); relErr == nil {
			converted.File = relative
		}
		flows = append(flows, converted)
	}
	return flows, nil
}

// relativeFlowPath turns an engine result's absolute path back into the path
// relative to the run root. Both sides are resolved first: a temporary
// directory reaches discovery as /var/… and comes back from the engine as
// /private/var/… on macOS, and an unresolved Rel would answer with a stack of
// "..".
func relativeFlowPath(base, path string) (string, error) {
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		resolvedBase = base
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolvedPath = path
	}
	relative, err := filepath.Rel(resolvedBase, resolvedPath)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("%s is outside %s", path, base)
	}
	return filepath.ToSlash(relative), nil
}

// reportPath resolves where the report goes. An explicit --output names the
// file; otherwise it lands in the working directory under the format's name,
// which is where the contract puts it and where CI looks for it.
func reportPath(options TestOptions, defaultName string) string {
	if options.Output != "" {
		return options.Output
	}
	return defaultName
}
