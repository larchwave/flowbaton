package report

import (
	"strings"
	"testing"
	"time"
)

// JUnit output uses a plural root and one testcase per flow:
//
//	<testsuites>
//	  <testsuite name="Test Suite" device="…" tests="2" failures="1" time="1.0">
//	    <testcase id="beta" name="beta" classname="beta" file="beta.yaml"
//	              time="1.0" status="ERROR">
//	      <failure>Assertion is false: false is true</failure>
//	    </testcase>
//	    <testcase id="alpha" … status="SUCCESS"/>
//	  </testsuite>
//	</testsuites>
//
// specs/03-cli-tooling.md:34 describes the corresponding summary shape:
// TestExecutionSummary{passed, suites[{flows[{name,status,failure,…}]}]} —
// suites plural, and status on the flow.

func shapeFlows() []FlowResult {
	started := time.Date(2026, time.August, 4, 12, 0, 0, 0, time.UTC)
	return []FlowResult{
		{
			Name: "beta", File: "beta.yaml", Status: Failed,
			StartedAt: started, EndedAt: started.Add(time.Second), DurationMillis: 1000,
			Failure: &Failure{Message: "Assertion is false: false is true"},
		},
		{
			Name: "alpha", File: "sub/alpha.yaml", Status: Completed,
			StartedAt: started, EndedAt: started, DurationMillis: 0,
		},
	}
}

func marshalShape(t *testing.T, options JUnitOptions) string {
	t.Helper()
	data, err := MarshalJUnit(options, shapeFlows())
	if err != nil {
		t.Fatalf("MarshalJUnit() error = %v", err)
	}
	return string(data)
}

func TestTheJUnitRootIsThePluralElement(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, "<testsuites>") {
		t.Fatalf("no <testsuites> wrapper; a parser that requires it sees no tests:\n%s", got)
	}
	if !strings.Contains(got, "<testsuite ") {
		t.Fatalf("no inner <testsuite>:\n%s", got)
	}
}

func TestDefaultSuiteName(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, `name="Test Suite"`) {
		t.Fatalf(`the default suite name is not "Test Suite":\n%s`, got)
	}
}

func TestAnExplicitSuiteNameStillWins(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{SuiteName: "checkout"})
	if !strings.Contains(got, `name="checkout"`) {
		t.Fatalf("--test-suite-name did not reach the report:\n%s", got)
	}
}

// id, name and classname all carry the flow name; file carries its path.
func TestATestCaseIsIdentifiedByTheFlowName(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	for _, want := range []string{`id="beta"`, `name="beta"`, `classname="beta"`, `file="beta.yaml"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
}

// The contract gives the path relative to the workspace root, not the base
// name: a nested flow reports sub/alpha.yaml.
func TestANestedFlowKeepsItsPath(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, `file="sub/alpha.yaml"`) {
		t.Fatalf("a nested flow lost its path:\n%s", got)
	}
}

func TestEveryTestCaseCarriesAStatus(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, `status="ERROR"`) {
		t.Fatalf("a failed flow is not ERROR:\n%s", got)
	}
	if !strings.Contains(got, `status="SUCCESS"`) {
		t.Fatalf("a passing flow is not SUCCESS:\n%s", got)
	}
}

// A flow with only an optional-command failure remains successful.
func TestAWarnedFlowIsASuccess(t *testing.T) {
	t.Parallel()

	data, err := MarshalJUnit(JUnitOptions{}, []FlowResult{
		{Name: "warn", File: "warn.yaml", Status: Warned,
			Failure: &Failure{Message: "optional step did not find its target"}},
	})
	if err != nil {
		t.Fatalf("MarshalJUnit() error = %v", err)
	}
	got := string(data)
	if !strings.Contains(got, `status="SUCCESS"`) || !strings.Contains(got, `failures="0"`) {
		t.Fatalf("a warned flow is not reported as a success:\n%s", got)
	}
}

// The failure message is the element's text, not a message or type attribute.
func TestTheFailureMessageIsTheElementText(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, "<failure>Assertion is false: false is true</failure>") {
		t.Fatalf("the failure text is not the element body:\n%s", got)
	}
}

// Seconds retain one required decimal without extra trailing zeroes.
func TestDurationsAreSecondsWithOneDecimal(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	if !strings.Contains(got, `time="1.0"`) || !strings.Contains(got, `time="0.0"`) {
		t.Fatalf("durations are not one-decimal seconds:\n%s", got)
	}
}

// The suite time is the run's total, and the counts have to agree with the
// cases — a report whose header disagrees with its body is worse than none.
func TestTheSuiteCountsAgreeWithItsCases(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	for _, want := range []string{`tests="2"`, `failures="1"`, `time="1.0"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s:\n%s", want, got)
		}
	}
}

// Attributes the contract does not write are not written: a consumer diffing
// two reports should not see fields that exist on only one side.
func TestJUnitOmitsUndeclaredAttributes(t *testing.T) {
	t.Parallel()

	got := marshalShape(t, JUnitOptions{})
	for _, unwanted := range []string{"timestamp=", "skipped=", `type="failure"`} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("wrote %s, which the contract does not:\n%s", unwanted, got)
		}
	}
}
