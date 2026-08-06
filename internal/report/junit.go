package report

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// JUnitOptions supplies suite-level values that must remain fixed across
// repeated renders. MarshalJUnit never reads the wall clock.
type JUnitOptions struct {
	SuiteName string
	// Device names the device the suite ran on. Empty omits the attribute rather
	// than inventing a value the host did not request from the driver.
	Device    string
	Timestamp time.Time
}

// defaultSuiteName labels a run when no suite name is provided.
const defaultSuiteName = "Test Suite"

// junitTestSuites is the plural root required by consumers that do not accept a
// bare <testsuite>. specs/03-cli-tooling.md:34 calls the field `suites[]`.
// A single-device run renders exactly one suite.
type junitTestSuites struct {
	XMLName xml.Name         `xml:"testsuites"`
	Suites  []junitTestSuite `xml:"testsuite"`
}

type junitTestSuite struct {
	Name      string          `xml:"name,attr"`
	Device    string          `xml:"device,attr,omitempty"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Time      string          `xml:"time,attr"`
	TestCases []junitTestCase `xml:"testcase"`
}

// junitTestCase carries the flow name in id, name, and classname, with the
// authored path in file.
type junitTestCase struct {
	ID        string        `xml:"id,attr"`
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr,omitempty"`
	Time      string        `xml:"time,attr"`
	Status    string        `xml:"status,attr"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitSkipped struct {
	Message string `xml:"message,attr"`
}

// junitFailure carries its message as element text for standard JUnit readers.
type junitFailure struct {
	Details string `xml:",chardata"`
}

// JUnit status values. Completed and warned flows report SUCCESS; failed flows
// report ERROR; cancelled flows report SKIPPED.
const (
	junitStatusSuccess = "SUCCESS"
	junitStatusError   = "ERROR"
	junitStatusSkipped = "SKIPPED"
)

// MarshalJUnit renders one testcase per flow. Failed flows become failures,
// skipped and cancelled flows become skipped testcases, and warned flows are
// successes because optional-command failures do not fail the flow.
func MarshalJUnit(options JUnitOptions, flows []FlowResult) ([]byte, error) {
	name := options.SuiteName
	if name == "" {
		name = defaultSuiteName
	}
	suite := junitTestSuite{
		Name:      sanitizeXML(name),
		Device:    sanitizeXML(options.Device),
		Tests:     len(flows),
		TestCases: make([]junitTestCase, 0, len(flows)),
	}

	var totalMillis int64
	for _, flow := range flows {
		if !flow.Status.Valid() {
			return nil, fmt.Errorf("flow %q status %q is invalid", flow.Name, flow.Status)
		}
		if flow.DurationMillis < 0 {
			return nil, fmt.Errorf("flow %q durationMillis must be non-negative", flow.Name)
		}

		flowName := sanitizeXML(flow.Name)
		testCase := junitTestCase{
			ID:        flowName,
			Name:      flowName,
			Classname: flowName,
			File:      sanitizeXML(flow.File),
			Time:      formatDurationMillis(flow.DurationMillis),
			Status:    junitStatusSuccess,
		}
		switch flow.Status {
		case Failed:
			suite.Failures++
			testCase.Status = junitStatusError
			testCase.Failure = junitFailureFor(flow.Failure)
		case Skipped:
			testCase.Status = junitStatusSkipped
			testCase.Skipped = &junitSkipped{Message: string(Skipped)}
		case Cancelled:
			testCase.Status = junitStatusSkipped
			testCase.Skipped = &junitSkipped{Message: string(Cancelled)}
		case Warned, Completed:
			// Optional-command failures do not fail the flow.
		}

		totalMillis += flow.DurationMillis
		suite.TestCases = append(suite.TestCases, testCase)
	}
	suite.Time = formatDurationMillis(totalMillis)

	var output bytes.Buffer
	output.WriteString(xml.Header)
	encoder := xml.NewEncoder(&output)
	encoder.Indent("", "  ")
	if err := encoder.Encode(junitTestSuites{Suites: []junitTestSuite{suite}}); err != nil {
		return nil, fmt.Errorf("marshal junit: %w", err)
	}
	output.WriteByte('\n')
	return output.Bytes(), nil
}

func junitFailureFor(failure *Failure) *junitFailure {
	details := string(Failed)
	if failure != nil {
		if failure.Message != "" {
			details = failure.Message
		}
		if failure.Details != "" {
			details = failure.Details
		}
	}
	return &junitFailure{Details: sanitizeXML(details)}
}

// formatDurationMillis renders seconds with one required decimal and no
// additional trailing zeroes: 1000ms is "1.0" and 3750ms is "3.75".
func formatDurationMillis(milliseconds int64) string {
	seconds := strconv.FormatFloat(float64(milliseconds)/1000, 'f', -1, 64)
	if !strings.Contains(seconds, ".") {
		seconds += ".0"
	}
	return seconds
}

func sanitizeXML(value string) string {
	var output strings.Builder
	output.Grow(len(value))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		value = value[size:]
		if !validXMLRune(r) {
			r = utf8.RuneError
		}
		output.WriteRune(r)
	}
	return output.String()
}

func validXMLRune(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD ||
		r >= 0x10000 && r <= utf8.MaxRune
}
