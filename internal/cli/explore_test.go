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

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/explore"
)

// silentLLM answers every chat with an empty reply; the fake crew never
// consults it, it only fills the model-set seam.
type silentLLM struct{}

func (silentLLM) Chat(context.Context, explore.ChatRequest) (explore.ChatResponse, error) {
	return explore.ChatResponse{}, nil
}

func fakeModelSet() explore.ModelSet {
	return explore.ModelSet{Worker: silentLLM{}, Manager: silentLLM{}, Vision: silentLLM{}}
}

// exploreCrewFake is one struct wearing every crew role: it plans a single
// scenario, runs it through the injected tester, and renders a fixed report.
type exploreCrewFake struct {
	state       *explore.ScreenState
	planned     bool
	scenarios   []explore.Scenario
	tester      func(explore.Scenario) (*explore.TestResult, error)
	report      string
	flowYAML    []byte
	exportErr   error
	researchErr error
	readyErr    error
}

func newExploreCrewFake() *exploreCrewFake {
	return &exploreCrewFake{
		state: &explore.ScreenState{Signature: explore.ScreenSignature{AppID: "com.example.app"}},
		tester: func(s explore.Scenario) (*explore.TestResult, error) {
			return &explore.TestResult{Scenario: s, Status: explore.TestPassed}, nil
		},
		report:   "# session report\n\none scenario passed\n",
		flowYAML: []byte("appId: com.example.app\n---\n- launchApp\n"),
	}
}

func (f *exploreCrewFake) Observe(context.Context) (*explore.ScreenState, error) {
	return f.state, nil
}

func (f *exploreCrewFake) Research(_ context.Context, s *explore.ScreenState) (*explore.UIMap, error) {
	if f.researchErr != nil {
		return nil, f.researchErr
	}
	return &explore.UIMap{Screen: s.Signature}, nil
}

func (f *exploreCrewFake) PlanNext(context.Context, explore.PlanRequest) ([]explore.Scenario, error) {
	if f.planned {
		return nil, nil
	}
	f.planned = true
	if len(f.scenarios) > 0 {
		return f.scenarios, nil
	}
	return []explore.Scenario{{Name: "login works", Priority: explore.PriorityNormal}}, nil
}

func (f *exploreCrewFake) RunScenario(
	_ context.Context, s explore.Scenario, _ *explore.ScreenState,
) (*explore.TestResult, error) {
	return f.tester(s)
}

func (f *exploreCrewFake) EnsureReady(context.Context) (*explore.ScreenState, error) {
	if f.readyErr != nil {
		return nil, f.readyErr
	}
	return f.state, nil
}

func (f *exploreCrewFake) Reach(
	context.Context, string,
) (*explore.ScreenState, []explore.StepRecord, error) {
	return f.state, nil, nil
}

func (f *exploreCrewFake) Report(context.Context, *explore.SessionReport) (string, error) {
	return f.report, nil
}

func (f *exploreCrewFake) ExportFlow(*explore.TestResult, string) ([]byte, error) {
	return f.flowYAML, f.exportErr
}

func (f *exploreCrewFake) crew() explore.Crew {
	return explore.Crew{
		Observer:   f,
		Researcher: f,
		Planner:    f,
		Tester:     f,
		Navigator:  f,
		Analyst:    f,
		Exporter:   f,
	}
}

// assembledExploreRunner wires an ExploreRunner whose seams answer with the
// given crew and a fake driver, so no device, provider, or key is touched.
func assembledExploreRunner(fake *exploreCrewFake, driver device.Driver) ExploreRunner {
	return ExploreRunner{
		NewModels: func(func(string) string) (explore.ModelSet, error) {
			return fakeModelSet(), nil
		},
		NewCrew: func(deps ExploreDeps) (explore.Crew, error) {
			return fake.crew(), nil
		},
		NewDriver: func(context.Context, TestOptions, string, int) (device.Driver, error) {
			return driver, nil
		},
		Clock: &advancingClock{now: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)},
	}
}

func exploreArgs(outputDir, stateDir string, extra ...string) []string {
	args := []string{
		"--app", "com.example.app", "-p", "android",
		"--device", "emulator-5554", "--driver-port", "7001",
		"--max-tests", "1",
		"--output", outputDir, "--state-dir", stateDir,
	}
	return append(args, extra...)
}

func TestExploreRunRejectsBadCommandLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing app", args: []string{"-p", "android"}},
		{name: "missing platform", args: []string{"--app", "com.example.app"}},
		{name: "bad platform", args: []string{"--app", "com.example.app", "-p", "osx"}},
		{name: "zero driver port", args: []string{"--app", "com.example.app", "-p", "android", "--driver-port", "0"}},
		{name: "zero budget", args: []string{"--app", "com.example.app", "-p", "android", "--max-tests", "0"}},
		{name: "zero step bound", args: []string{"--app", "com.example.app", "-p", "android", "--max-steps", "0"}},
		{name: "empty styles", args: []string{"--app", "com.example.app", "-p", "android", "--styles", ","}},
		{name: "unknown flag", args: []string{"--app", "com.example.app", "-p", "android", "--frobnicate", "x"}},
		{name: "positional argument", args: []string{"--app", "com.example.app", "-p", "android", "stray"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			// The zero runner would refuse anyway, but a usage error must win:
			// these command lines never reach the seams.
			if got := (ExploreRunner{}).Run(context.Background(), test.args, &stdout, &stderr); got != ExitInvalid {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitInvalid, stderr.String())
			}
			if !strings.Contains(stderr.String(), ExploreUsage) {
				t.Fatalf("usage line missing from stderr: %q", stderr.String())
			}
			if stdout.String() != "" {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestExploreRunRefusesWhenSupportIsNotAssembled(t *testing.T) {
	t.Parallel()

	// Only one seam wired is still not assembled: half a crew must not open
	// a driver.
	runners := map[string]ExploreRunner{
		"no seams": {},
		"models only": {NewModels: func(func(string) string) (explore.ModelSet, error) {
			return fakeModelSet(), nil
		}},
	}
	for name, runner := range runners {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"--app", "com.example.app", "-p", "android"}
			if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
				t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
			}
			if !strings.Contains(stderr.String(), ErrExploreNotAssembled.Error()) {
				t.Fatalf("the typed refusal was not printed: %q", stderr.String())
			}
		})
	}
}

func TestExploreRunFailsClosedWithoutAProvider(t *testing.T) {
	t.Parallel()

	driverBuilt := false
	runner := ExploreRunner{
		NewModels: func(func(string) string) (explore.ModelSet, error) {
			return explore.ModelSet{}, nil
		},
		NewCrew: func(ExploreDeps) (explore.Crew, error) { return explore.Crew{}, nil },
		NewDriver: func(context.Context, TestOptions, string, int) (device.Driver, error) {
			driverBuilt = true
			return nil, errors.New("must not be reached")
		},
	}
	var stdout, stderr bytes.Buffer
	args := exploreArgs(t.TempDir(), t.TempDir())
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	if !strings.Contains(stderr.String(), explore.ErrNoAIProvider.Error()) {
		t.Fatalf("the provider refusal was not printed: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "OPENAI_API_KEY") ||
		!strings.Contains(stderr.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("the refusal does not name the variables to set: %q", stderr.String())
	}
	if driverBuilt {
		t.Fatal("a driver was constructed despite the missing provider")
	}
}

func TestExploreRunGivesFlagCredentialsPrecedenceOverTheEnvironment(t *testing.T) {
	t.Parallel()

	var seenKey, seenURL string
	runner := ExploreRunner{
		NewModels: func(getenv func(string) string) (explore.ModelSet, error) {
			seenKey = getenv("OPENAI_API_KEY")
			seenURL = getenv("FLOWBATON_AI_BASE_URL")
			return explore.ModelSet{}, nil
		},
		NewCrew: func(ExploreDeps) (explore.Crew, error) { return explore.Crew{}, nil },
		Getenv:  func(string) string { return "from-environment" },
	}
	var stdout, stderr bytes.Buffer
	args := exploreArgs(t.TempDir(), t.TempDir(),
		"--api-key", "flag-key", "--api-url", "https://flag.example/v1")
	runner.Run(context.Background(), args, &stdout, &stderr)
	if seenKey != "flag-key" {
		t.Fatalf("OPENAI_API_KEY = %q, want the --api-key value", seenKey)
	}
	if seenURL != "https://flag.example/v1" {
		t.Fatalf("FLOWBATON_AI_BASE_URL = %q, want the --api-url value", seenURL)
	}
}

func TestExploreRunExecutesASessionAndWritesArtifacts(t *testing.T) {
	t.Parallel()

	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	driver := permissiveDriver()
	runner := assembledExploreRunner(fake, driver)
	var seenUDID string
	var seenPort int
	var seenOptions TestOptions
	runner.NewDriver = func(_ context.Context, options TestOptions, udid string, port int) (device.Driver, error) {
		seenOptions, seenUDID, seenPort = options, udid, port
		return driver, nil
	}

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitOK, stderr.String())
	}
	if seenUDID != "emulator-5554" || seenPort != 7001 {
		t.Fatalf("driver built for %q:%d, want emulator-5554:7001", seenUDID, seenPort)
	}
	if !seenOptions.ReinstallDriver {
		t.Fatal("explore built its driver without ReinstallDriver; a plain device has no operator-managed runner to connect to")
	}
	if !strings.Contains(stdout.String(), "# session report") {
		t.Fatalf("the report markdown was not written to stdout: %q", stdout.String())
	}
	reportPath := filepath.Join(outputDir, "report-night-shift.md")
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(reportBytes), "one scenario passed") {
		t.Fatalf("report file = %q, want the analyst markdown", reportBytes)
	}
	flowPath := filepath.Join(outputDir, "flows", "flow-01.yaml")
	flowBytes, err := os.ReadFile(flowPath)
	if err != nil {
		t.Fatalf("read exported flow: %v", err)
	}
	if string(flowBytes) != string(fake.flowYAML) {
		t.Fatalf("exported flow = %q, want the exporter's bytes", flowBytes)
	}
	if !calledMethod(driver, enginetest.MethodOpen) || !calledMethod(driver, enginetest.MethodClose) {
		t.Fatal("the driver was not opened and closed around the session")
	}
}

func TestExploreRunSkipsFlowExportWithoutAnExporter(t *testing.T) {
	t.Parallel()

	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	runner := assembledExploreRunner(fake, permissiveDriver())
	runner.NewCrew = func(ExploreDeps) (explore.Crew, error) {
		crew := fake.crew()
		crew.Exporter = nil
		return crew, nil
	}

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "no-export")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "skipping flow export") {
		t.Fatalf("the missing exporter was not warned about: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outputDir, "flows")); !os.IsNotExist(err) {
		t.Fatalf("a flows directory appeared without an exporter: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "report-no-export.md")); err != nil {
		t.Fatalf("the report was not written: %v", err)
	}
}

func TestExploreRunReportsASessionFailureAndStillClosesTheDriver(t *testing.T) {
	t.Parallel()

	fake := newExploreCrewFake()
	fake.tester = func(explore.Scenario) (*explore.TestResult, error) {
		return nil, errors.New("the app crashed mid-scenario")
	}
	driver := permissiveDriver()
	runner := assembledExploreRunner(fake, driver)

	var stdout, stderr bytes.Buffer
	args := exploreArgs(filepath.Join(t.TempDir(), "out"), t.TempDir())
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	if !strings.Contains(stderr.String(), "the app crashed mid-scenario") {
		t.Fatalf("the session error was not surfaced: %q", stderr.String())
	}
	if !calledMethod(driver, enginetest.MethodClose) {
		t.Fatal("the driver was not closed after the failed session")
	}
}

func TestExploreRunKeepsTheReportWhenAFlowCannotBeExported(t *testing.T) {
	t.Parallel()

	// A passed scenario the exporter cannot turn into a flow is a warning:
	// the report already holds the evidence, so the session still succeeds.
	fake := newExploreCrewFake()
	fake.exportErr = errors.New("no selector survived validation")
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	outputDir := filepath.Join(t.TempDir(), "out")
	args := exploreArgs(outputDir, t.TempDir())
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitOK, stderr.String())
	}
	if !strings.Contains(stderr.String(), "no selector survived validation") {
		t.Fatalf("the export failure was not surfaced: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outputDir, "flows", "flow-01.yaml")); !os.IsNotExist(err) {
		t.Fatalf("a flow file was written for the failed export: %v", err)
	}
	if strings.Contains(stdout.String(), "flow-01.yaml") {
		t.Fatalf("stdout lists a flow that was not written: %q", stdout.String())
	}
}

func TestExploreRunDefaultSessionNameComesFromTheInjectedClock(t *testing.T) {
	t.Parallel()

	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir())
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitOK, stderr.String())
	}
	want := filepath.Join(outputDir, "report-explore-20260810-120000.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("report at the clock-derived default name is missing: %v", err)
	}
}

func TestExploreInvokerAppliesDefaultsAndValidates(t *testing.T) {
	t.Parallel()

	var seenConfig explore.Config
	fake := newExploreCrewFake()
	runner := assembledExploreRunner(fake, permissiveDriver())
	runner.NewCrew = func(deps ExploreDeps) (explore.Crew, error) {
		seenConfig = deps.Config
		return fake.crew(), nil
	}
	outputDir := filepath.Join(t.TempDir(), "mcp-out")
	invoker := exploreRunnerInvoker{runner: runner}

	result, err := invoker.Explore(context.Background(), ExploreToolOptions{
		AppID: "com.example.app", Platform: "android",
		Device: "emulator-5554", DriverPort: 7001, OutputDir: outputDir,
	})
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if seenConfig.MaxTests != defaultExploreMaxTests ||
		seenConfig.MaxStepsPerTest != defaultExploreMaxSteps {
		t.Fatalf("config bounds = %d/%d, want the defaults %d/%d",
			seenConfig.MaxTests, seenConfig.MaxStepsPerTest,
			defaultExploreMaxTests, defaultExploreMaxSteps)
	}
	if !strings.Contains(result.Report, "# session report") {
		t.Fatalf("result report = %q, want the analyst markdown", result.Report)
	}
	if len(result.Flows) != 1 || !strings.HasPrefix(result.Flows[0], outputDir) {
		t.Fatalf("result flows = %v, want one path under %q", result.Flows, outputDir)
	}

	if _, err := invoker.Explore(context.Background(), ExploreToolOptions{
		AppID: "com.example.app", Platform: "solaris",
	}); err == nil {
		t.Fatal("an unsupported platform was accepted by the invoker")
	}
}

func TestExploreRunWritesTheStepLog(t *testing.T) {
	t.Parallel()

	// The report answers what the app did; only the step log answers what the
	// agent did. Without it, asking whether a tool was ever called costs a
	// whole session.
	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	fake.tester = func(s explore.Scenario) (*explore.TestResult, error) {
		return &explore.TestResult{
			Scenario: s,
			Status:   explore.TestFailed,
			Steps: []explore.StepRecord{
				{Index: 0, Action: explore.Action{Kind: explore.ActionTap, Text: "Search"}, Status: explore.StepOK},
				{
					Index:   1,
					Action:  explore.Action{Kind: explore.ActionInput, Text: "milk"},
					Status:  explore.StepFailed,
					ErrText: "nowhere to type",
				},
			},
		}, nil
	}
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitOK {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitOK, stderr.String())
	}
	logBytes, err := os.ReadFile(filepath.Join(outputDir, "steps-night-shift.md"))
	if err != nil {
		t.Fatalf("read step log: %v", err)
	}
	text := string(logBytes)
	for _, want := range []string{"login works", "step 1: tap \"Search\"", "step 2: input \"milk\"", "nowhere to type"} {
		if !strings.Contains(text, want) {
			t.Fatalf("step log = %q, want it to carry %q", text, want)
		}
	}
}

func TestExploreRunWritesTheStepLogAfterAFailedSession(t *testing.T) {
	// A session that dies mid-scenario is exactly when the step log is worth
	// having. The session loop hands back the partial report for that reason.
	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	fake.tester = func(s explore.Scenario) (*explore.TestResult, error) {
		partial := &explore.TestResult{
			Scenario: s,
			Status:   explore.TestStopped,
			Steps: []explore.StepRecord{{
				Index:  0,
				Action: explore.Action{Kind: explore.ActionTap, Text: "Search"},
				Status: explore.StepOK,
			}},
		}
		return partial, errors.New("the app crashed mid-scenario")
	}
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	logBytes, err := os.ReadFile(filepath.Join(outputDir, "steps-night-shift.md"))
	if err != nil {
		t.Fatalf("read step log after a failed session: %v", err)
	}
	if !strings.Contains(string(logBytes), "step 1: tap \"Search\"") {
		t.Fatalf("step log = %q, want the steps the run did manage", logBytes)
	}
}

func TestExploreRunReplacesAStepLogFromAnEarlierRun(t *testing.T) {
	// Session names can repeat. A log left by an earlier run reads as this
	// run's evidence, so a run that records nothing must still say so.
	outputDir := filepath.Join(t.TempDir(), "run-output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(outputDir, "steps-night-shift.md")
	if err := os.WriteFile(stale, []byte("# session step log\n\n## old run (passed)\n- step 1: tap \"Ghost\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake := newExploreCrewFake()
	fake.tester = func(explore.Scenario) (*explore.TestResult, error) {
		return nil, errors.New("the app crashed before any step")
	}
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	logBytes, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read step log: %v", err)
	}
	if strings.Contains(string(logBytes), "Ghost") {
		t.Fatalf("the earlier run's log survived this run:\n%s", logBytes)
	}
	if !strings.Contains(string(logBytes), "no scenario ran") {
		t.Fatalf("step log = %q, want it to say that nothing ran", logBytes)
	}
}

func TestExploreRunReplacesAStepLogWhenTheDriverNeverOpens(t *testing.T) {
	// The run can die before any scenario: no device, no runner, no driver.
	// A log from an earlier run under the same name must not survive that
	// either, or it is read as this run's evidence.
	outputDir := filepath.Join(t.TempDir(), "run-output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(outputDir, "steps-night-shift.md")
	if err := os.WriteFile(stale, []byte("# session step log\n\n## old run (passed)\n- step 1: tap \"Ghost\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := assembledExploreRunner(newExploreCrewFake(), permissiveDriver())
	runner.NewDriver = func(context.Context, TestOptions, string, int) (device.Driver, error) {
		return nil, errors.New("no device answered")
	}

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	logBytes, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read step log: %v", err)
	}
	if strings.Contains(string(logBytes), "Ghost") {
		t.Fatalf("the earlier run's log survived a run that never started:\n%s", logBytes)
	}
}

func TestExploreRunReplacesAStepLogWithoutAProvider(t *testing.T) {
	t.Parallel()

	// Failing closed on the provider is the earliest exit of all, and it is
	// the one an operator hits repeatedly while wiring a key up. An earlier
	// run's log must not sit there looking like the result of this attempt.
	outputDir := filepath.Join(t.TempDir(), "run-output")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(outputDir, "steps-night-shift.md")
	if err := os.WriteFile(stale, []byte("# session step log\n\n## old run (passed)\n- step 1: tap \"Ghost\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := ExploreRunner{
		NewModels: func(func(string) string) (explore.ModelSet, error) {
			return explore.ModelSet{}, nil
		},
		NewCrew: func(ExploreDeps) (explore.Crew, error) { return explore.Crew{}, nil },
		NewDriver: func(context.Context, TestOptions, string, int) (device.Driver, error) {
			return nil, errors.New("must not be reached")
		},
	}
	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "night-shift")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	logBytes, err := os.ReadFile(stale)
	if err != nil {
		t.Fatalf("read step log: %v", err)
	}
	if strings.Contains(string(logBytes), "Ghost") {
		t.Fatalf("the earlier run's log survived a refused run:\n%s", logBytes)
	}
}

// mmx22 (2026-08-30, live): the first scenario passed and exported cleanly,
// the simulator's runner died in the second, and the run wrote only a step
// log -- the passed flow was lost with the session.
func TestExploreRunKeepsTheArtifactsOfAFailedSession(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "run-output")
	fake := newExploreCrewFake()
	fake.scenarios = []explore.Scenario{
		{Name: "create a reminder", Priority: explore.PriorityNormal},
		{Name: "rename a list", Priority: explore.PriorityNormal},
	}
	calls := 0
	fake.tester = func(s explore.Scenario) (*explore.TestResult, error) {
		calls++
		if calls == 1 {
			return &explore.TestResult{
				Scenario: s,
				Status:   explore.TestPassed,
				Steps: []explore.StepRecord{{
					Index:  0,
					Action: explore.Action{Kind: explore.ActionTap, Target: &explore.Locator{Kind: explore.LocatorText, Value: "New"}},
					Status: explore.StepOK,
				}},
			}, nil
		}
		return &explore.TestResult{Scenario: s, Status: explore.TestStopped},
			errors.New("device unreachable")
	}
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	args := exploreArgs(outputDir, t.TempDir(), "--session-name", "mmx22", "--max-tests", "2")
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got != ExitFailure {
		t.Fatalf("exit = %d, want %d; stderr: %s", got, ExitFailure, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outputDir, "report-mmx22.md")); err != nil {
		t.Fatalf("the report of a failed session is missing: %v", err)
	}
	flows, err := filepath.Glob(filepath.Join(outputDir, "flows", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(flows) != 1 {
		t.Fatalf("flows = %v, want the passed scenario exported; stderr: %s", flows, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(outputDir, "steps-mmx22.md")); err != nil {
		t.Fatalf("the step log is missing: %v", err)
	}
}

// A session that dies before it has a report hands the abort path a nil one,
// and the path wrote to it without looking: `flowbaton explore` against
// com.apple.mobilenotes crashed on a nil pointer at
// writeExploreArtifacts instead of naming the failure. The crash also buried
// the real cause, which is the thing an operator needed.
func TestExploreRunReportsAFailureThatCameBeforeAnyReport(t *testing.T) {
	t.Parallel()

	fake := newExploreCrewFake()
	// An app that never comes to the foreground is the earliest failure a
	// real session hits, and the one that crashed: `flowbaton explore` on an
	// app the simulator does not carry died on a nil pointer inside
	// writeExploreArtifacts instead of naming the failure, burying the very
	// cause an operator needed.
	fake.readyErr = errors.New("prepare app: the app never came to the foreground")
	runner := assembledExploreRunner(fake, permissiveDriver())

	var stdout, stderr bytes.Buffer
	outputDir := filepath.Join(t.TempDir(), "out")
	args := exploreArgs(outputDir, t.TempDir())
	if got := runner.Run(context.Background(), args, &stdout, &stderr); got == ExitOK {
		t.Fatalf("exit = %d, want a failure; stderr: %s", got, stderr.String())
	}
	if !strings.Contains(stderr.String(), "the app never came to the foreground") {
		t.Fatalf("the cause of the failure never reached stderr: %q", stderr.String())
	}
}

// The session name reaches the filesystem as part of every artifact name
// (report-<name>.md, steps-<name>.md), and filepath.Join folds "..", so
// "../../x" writes outside the output directory the operator chose. Both
// entry points share this check because the MCP tool builds the same options
// from whatever a model asked for.
func TestExploreRefusesASessionNameThatLeavesTheOutputDirectory(t *testing.T) {
	escapes := []string{
		"../../x",
		"..",
		"a/b",
		`a\b`,
		"nested/../../x",
	}
	for _, name := range escapes {
		options := exploreOptions{
			AppID: "com.example.app", Platform: "ios",
			MaxTests: 1, MaxSteps: 1, Styles: []string{"normal"},
			SessionName: name,
		}
		err := validateExploreOptions(options)
		if err == nil {
			t.Fatalf("session name %q was accepted", name)
		}
		if !strings.Contains(err.Error(), "--session-name") {
			t.Fatalf("session name %q refused without naming the option: %v", name, err)
		}
	}
}

func TestExploreAcceptsTheSessionNamesOperatorsActuallyUse(t *testing.T) {
	// The default is generated (explore-20260830-141500) and a session on a
	// device is tagged by hand; refusing either would be worse than the escape.
	for _, name := range []string{"", "explore-20260830-141500", "mmx33", "run_2.retry"} {
		options := exploreOptions{
			AppID: "com.example.app", Platform: "ios",
			MaxTests: 1, MaxSteps: 1, Styles: []string{"normal"},
			SessionName: name,
		}
		if err := validateExploreOptions(options); err != nil {
			t.Fatalf("session name %q refused: %v", name, err)
		}
	}
}
