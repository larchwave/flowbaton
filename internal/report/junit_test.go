package report

import (
	"bytes"
	"encoding/xml"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMarshalJUnitMatchesGoldenAndProducesValidXML(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.July, 15, 18, 30, 0, 0, time.UTC)
	flows := []FlowResult{
		{
			Name: "completed <flow> ✅", Status: Completed,
			StartedAt: started, EndedAt: started.Add(500 * time.Millisecond), DurationMillis: 500,
		},
		{
			Name: "skipped", Status: Skipped,
			StartedAt: started.Add(500 * time.Millisecond), EndedAt: started.Add(500 * time.Millisecond),
		},
		{
			Name: "warned", Status: Warned,
			StartedAt: started.Add(500 * time.Millisecond), EndedAt: started.Add(750 * time.Millisecond), DurationMillis: 250,
			Failure: &Failure{Message: "slow \x01 response", Details: "retry & observe"},
		},
		{
			Name: "failed", Status: Failed,
			StartedAt: started.Add(750 * time.Millisecond), EndedAt: started.Add(2750 * time.Millisecond), DurationMillis: 2000,
			Failure: &Failure{
				Message: "expected <Pay> & continue",
				Details: `selector "Pay" wasn't found; next: <retry>`,
			},
		},
		{
			Name: "cancelled", Status: Cancelled,
			StartedAt: started.Add(2750 * time.Millisecond), EndedAt: started.Add(3750 * time.Millisecond), DurationMillis: 1000,
		},
	}

	got, err := MarshalJUnit(JUnitOptions{
		SuiteName: "mobile & web 🚀",
		Timestamp: started,
	}, flows)
	if err != nil {
		t.Fatalf("MarshalJUnit() error = %v", err)
	}

	want, err := os.ReadFile("../../testdata/report/junit.golden.xml")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalJUnit() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}

	var parsed any
	if err := xml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("MarshalJUnit() produced invalid XML: %v", err)
	}
	if bytes.Contains(got, []byte{0x01}) {
		t.Fatal("MarshalJUnit() retained an XML 1.0-invalid control character")
	}
	if !bytes.Contains(got, []byte("✅")) || !bytes.Contains(got, []byte("🚀")) {
		t.Fatal("MarshalJUnit() did not preserve valid Unicode")
	}

	again, err := MarshalJUnit(JUnitOptions{
		SuiteName: "mobile & web 🚀",
		Timestamp: started,
	}, flows)
	if err != nil {
		t.Fatalf("second MarshalJUnit() error = %v", err)
	}
	if !bytes.Equal(got, again) {
		t.Fatal("MarshalJUnit() output changed for identical fixed input")
	}
}

func TestMarshalJUnitRejectsInvalidFlowStatus(t *testing.T) {
	t.Parallel()

	_, err := MarshalJUnit(JUnitOptions{SuiteName: "suite"}, []FlowResult{
		{Name: "flow", Status: Status("unknown")},
	})
	if err == nil {
		t.Fatal("MarshalJUnit() error = nil, want invalid status error")
	}
	if !strings.Contains(err.Error(), `flow "flow" status "unknown"`) {
		t.Fatalf("MarshalJUnit() error = %q, want flow name and status", err)
	}
}

func TestMarshalJUnitUsesFallbackFailureText(t *testing.T) {
	t.Parallel()

	got, err := MarshalJUnit(JUnitOptions{SuiteName: "suite"}, []FlowResult{
		{Name: "failed", Status: Failed},
		{Name: "warned", Status: Warned},
	})
	if err != nil {
		t.Fatalf("MarshalJUnit() error = %v", err)
	}
	// A failure with no message still emits fallback text in the element body.
	if !bytes.Contains(got, []byte(`<failure>Failed</failure>`)) {
		t.Fatalf("MarshalJUnit() missing failed fallback: %s", got)
	}
	// A warned flow is a plain success with no fallback failure text.
	if !bytes.Contains(got, []byte(`name="warned" classname="warned" time="0.0" status="SUCCESS"`)) {
		t.Fatalf("MarshalJUnit() did not report the warned flow as a success: %s", got)
	}
	if !bytes.Contains(got, []byte(`failures="1"`)) {
		t.Fatalf("MarshalJUnit() counted the warned flow as a failure: %s", got)
	}
}
