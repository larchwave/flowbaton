package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// The runner's job is to fail BEFORE a device is involved whenever the reason
// is knowable without one. Every case here is a failure the host pipeline can
// find on its own: a missing file, an unparsable flow, an unknown command, a
// tag filter that selects nothing.
//
// The Session is nil throughout, which is also the production wiring: a nil
// Session makes the runner resolve a real device at the end. None of these
// cases names a platform, so reaching that step at all means the check the
// case was meant to prove never ran. deviceBoundaryMarker is what that looks
// like when it happens.

func TestRunnerRefusesAMissingFlowFile(t *testing.T) {
	t.Parallel()

	_, stderr, code := runTest(t, "/nonexistent-flow.yaml")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if strings.Contains(stderr, deviceBoundaryMarker) {
		t.Fatalf("reached the device boundary: %s", stderr)
	}
}

func TestRunnerRefusesAnUnparsableFlow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	writeFile(t, path, "appId: [\n")

	_, stderr, code := runTest(t, path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if strings.Contains(stderr, deviceBoundaryMarker) {
		t.Fatalf("a malformed flow reached the device boundary: %s", stderr)
	}
}

func TestRunnerRefusesAnUnknownCommandBeforeTouchingADevice(t *testing.T) {
	t.Parallel()

	// This is the whole reason preflight runs first. An unknown command is
	// knowable from the file, and discovering it after acquiring a device
	// would report a setup mistake as a flow failure.
	dir := t.TempDir()
	path := filepath.Join(dir, "unknown.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- notARealCommand: x\n")

	_, stderr, code := runTest(t, path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if strings.Contains(stderr, deviceBoundaryMarker) {
		t.Fatalf("an unknown command reached the device boundary: %s", stderr)
	}
}

func TestRunnerRefusesATagFilterThatSelectsNothing(t *testing.T) {
	t.Parallel()

	// Expected: exit 1 with "Include / Exclude tags did not
	// match any Flows". An empty selection that exited 0 would report a
	// passing suite that ran nothing.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tagged.yaml"),
		"appId: com.example.a\ntags:\n  - slow\n---\n- launchApp\n")

	_, stderr, code := runTest(t, "--include-tags", "nosuchtag", dir)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(strings.ToLower(stderr), "did not match any flows") {
		t.Fatalf("stderr = %q, want it to name the tag filter as the reason", stderr)
	}
	if !strings.Contains(stderr, "nosuchtag") {
		t.Fatalf("stderr = %q, want it to echo the tag that matched nothing", stderr)
	}
}

func TestRunnerRunsCapabilityPreflightNotJustParsing(t *testing.T) {
	t.Parallel()

	// This test exists because a negative control found the gap: deleting the
	// engine.Prepare error check left every other test in this file GREEN. The
	// unknown-command case is caught by the PARSER during discovery, so it
	// proves nothing about preflight.
	//
	// A runFlow pointing at a file that does not exist is the difference. It
	// parses cleanly — the command is well formed — and only the recursive
	// graph walk in preflight can see that the contract dangles.
	dir := t.TempDir()
	path := filepath.Join(dir, "dangling.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- runFlow: missing-subflow.yaml\n")

	_, stderr, code := runTest(t, path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if strings.Contains(stderr, deviceBoundaryMarker) {
		t.Fatalf("a dangling flow link reached the device boundary: %s", stderr)
	}
	if !strings.Contains(stderr, "missing_link") {
		t.Fatalf("stderr = %q, want the preflight graph error", stderr)
	}
}

func TestRunnerReachesTheDeviceBoundaryForAValidFlow(t *testing.T) {
	t.Parallel()

	// The positive control for every test above: a flow that IS valid must get
	// all the way through discovery and preflight, and stop only because no
	// device session exists. Without this, "did not reach the device boundary"
	// would be satisfied by a pipeline that never worked at all.
	dir := t.TempDir()
	path := filepath.Join(dir, "valid.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- launchApp\n")

	stdout, stderr, code := runTest(t, path)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, deviceBoundaryMarker) {
		t.Fatalf("a valid flow did not reach the device boundary; stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "valid.yaml") {
		t.Fatalf("stdout = %q, want the selected flow listed", stdout)
	}
}

func TestRunnerRejectsAPlatformImpossibleFlowBeforeSessionCreation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ios-only.yaml")
	writeFile(t, path, "appId: com.example.a\n---\n- back\n")

	sessionCreated := false
	runner := TestRunner{NewSession: func(_ context.Context, _ Shard, _ TestOptions) (TestSession, error) {
		sessionCreated = true
		return DeviceSession{}, nil
	}}
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), []string{"-p", "ios", path}, &stdout, &stderr)
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr = %q", code, ExitFailure, stderr.String())
	}
	if sessionCreated {
		t.Fatal("platform-impossible flow created a device session")
	}
	if !strings.Contains(stderr.String(), "unsupported_platform") {
		t.Fatalf("stderr = %q, want platform capability violation", stderr.String())
	}
}

func TestRunnerUsageErrorsExitTwoAndPrintUsage(t *testing.T) {
	t.Parallel()

	_, stderr, code := runTest(t)
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, TestUsage) {
		t.Fatalf("stderr = %q, want the usage line", stderr)
	}
}

func TestRunnerDoesNotPrintUsageForARunFailure(t *testing.T) {
	t.Parallel()

	// Usage is the answer to "you typed it wrong". Printing it after a real
	// failure sends an operator to reread flags that were already correct.
	_, stderr, code := runTest(t, "/nonexistent-flow.yaml")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if strings.Contains(stderr, TestUsage) {
		t.Fatalf("stderr = %q, want no usage line for a run failure", stderr)
	}
}

// deviceBoundaryMarker is what the runner says when it has finished every
// host-side check and needs a device it has not been told how to find. A case
// that is meant to fail EARLIER must never produce it.
const deviceBoundaryMarker = "a platform is required"

func runTest(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := TestRunner{}.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Replaying an exported flow on a simulator printed exactly "element not
// found" and nothing else: not which of the six steps, not what it looked
// for. The information was already in commands.json (sequence 2, tapOn "New
// Reminder"), so the terminal was the only place it was missing -- and the
// terminal is where CI and an operator read it.
func TestAFailedFlowNamesTheCommandThatFailed(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	timeline, err := engine.NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline: %v", err)
	}
	tapOn := model.Command{
		Kind:   model.CommandTapOn,
		Source: model.SourceInfo{Path: "flow-02.yaml", Start: model.Position{Line: 7}},
	}
	span, _, err := timeline.BeginCommand(tapOn, 0)
	if err != nil {
		t.Fatalf("BeginCommand: %v", err)
	}
	failure := errors.New("element not found")
	command, _, err := span.Finish(engine.Failed, failure, engine.CommandMetadata{})
	if err != nil {
		t.Fatalf("CommandSpan.Finish: %v", err)
	}
	flowSpan, _, err := timeline.BeginFlow("flow-02.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow: %v", err)
	}
	flow, _, err := flowSpan.Finish(engine.Failed, failure, []engine.CommandResult{command})
	if err != nil {
		t.Fatalf("FlowSpan.Finish: %v", err)
	}

	var stdout, stderr strings.Builder
	if code := reportResults(&stdout, &stderr, []engine.FlowResult{flow}); code != ExitFailure {
		t.Fatalf("exit code = %d, want a failure", code)
	}
	line := stderr.String()
	for _, want := range []string{"flow-02.yaml:7", "tapOn", "element not found"} {
		if !strings.Contains(line, want) {
			t.Errorf("failure line %q is missing %q", line, want)
		}
	}
}
