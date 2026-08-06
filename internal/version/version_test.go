package version

import "testing"

func TestLineReportsDevelopmentBuildHonestly(t *testing.T) {
	if got, want := Line(), "flowbaton dev"; got != want {
		t.Fatalf("Line() = %q, want %q", got, want)
	}
}
