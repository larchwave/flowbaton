package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

// --analyze is absent from specs/03-cli-tooling.md. It must be rejected as an
// unknown option rather than retained as a planned flag with invented output.

func TestAnalyzeIsRefusedRatherThanIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	_, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		"--analyze", filepath.Join(dir, "passing.yaml"),
	})
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want usage error %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "unknown option") || !strings.Contains(stderr, "--analyze") {
		t.Fatalf("the refusal did not name the flag: %q", stderr)
	}
}

// The refusal has to come BEFORE the run, or the operator pays for a full suite
// to be told the flag they passed does nothing.
func TestAnalyzeIsRefusedBeforeAnythingRuns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	stdout, _, _ := runSessionWithArgs(t, permissiveDriver(), []string{
		"--analyze", filepath.Join(dir, "passing.yaml"),
	})
	if strings.Contains(stdout, "PASS") {
		t.Fatalf("the suite ran before --analyze was refused:\n%s", stdout)
	}
}

// Without the flag the flow runs normally, isolating the refusal to --analyze.
func TestTheSameFlowRunsWithoutAnalyze(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "passing.yaml"), "appId: com.example.a\n---\n- launchApp\n")

	_, stderr, code := runSessionWithArgs(t, permissiveDriver(), []string{
		filepath.Join(dir, "passing.yaml"),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
}
