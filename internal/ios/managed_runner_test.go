package ios

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A prebuilt runner needs no project at run time. The managed runner uses the
// generated test bundle with these environment variables:
//
//	TEST_RUNNER_FLOWBATON_RUNNER_SERVE=1 TEST_RUNNER_PORT=41411 \
//	xcodebuild test-without-building -xctestrun <path> -destination id=<udid> \
//	  -only-testing:.../testServeTheWireUntilTheHostIsDone
//	$ curl 127.0.0.1:41411/status → {"status":"ok"}

// fakeRunnerProcess stands in for the xcodebuild child. A nil exit channel
// blocks forever, which is what a child that has not died looks like.
type fakeRunnerProcess struct {
	args        []string
	environment []string
	stopped     bool
	exit        chan error
}

func (process *fakeRunnerProcess) stopRunner() error {
	process.stopped = true
	return nil
}

func (process *fakeRunnerProcess) exited() <-chan error { return process.exit }

func TestOpenStartsTheRunnerWhenItOwnsOne(t *testing.T) {
	t.Parallel()

	// The port answers only once the child was started, as a real one does,
	// and echoes the id the child was launched with.
	var started atomic.Bool
	var launchedID atomic.Value
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && started.Load() {
			_, _ = w.Write([]byte(runnerStatusBody(launchedID.Load().(string))))
			return
		}
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	process := &fakeRunnerProcess{}
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	driver.spawnRunner = func(_ context.Context, args, environment []string) (runnerProcess, error) {
		process.args, process.environment = args, environment
		launchedID.Store(launchedRunnerID(environment))
		started.Store(true)
		return process, nil
	}

	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	// What the runner is asked to run. The rest of the invocation is about
	// where xcodebuild puts its leavings, and is pinned by
	// TestTheManagedRunnerLeavesNoDerivedDataBehind.
	want := []string{
		"test-without-building",
		"-xctestrun", "/built/Runner.xctestrun",
		"-destination", "platform=iOS Simulator,id=UDID-1",
		"-only-testing:" + runnerServeTest,
	}
	if len(process.args) < len(want) || !reflect.DeepEqual(process.args[:len(want)], want) {
		t.Fatalf("xcodebuild args = %#v, want them to begin %#v", process.args, want)
	}
	// The runner reads its own configuration from the process it is launched
	// with, and only TEST_RUNNER_-prefixed names survive the trip into the
	// simulator — a plain PORT never arrives.
	if id := launchedRunnerID(process.environment); len(id) != 16 || id != driver.runnerID {
		t.Fatalf("runner id in the environment = %q, driver holds %q; want one fresh 16-hex id in both", id, driver.runnerID)
	}
	for _, wanted := range []string{
		"TEST_RUNNER_FLOWBATON_RUNNER_SERVE=1",
		"TEST_RUNNER_PORT=" + strconv.Itoa(driver.port),
		"TEST_RUNNER_FLOWBATON_RUNNER_ID=" + driver.runnerID,
	} {
		if !slices.Contains(process.environment, wanted) {
			// Only what this test is about. The child inherits the whole
			// environment, and a failure that prints it puts every API key on
			// the machine into the log.
			var runnerSettings []string
			for _, entry := range process.environment {
				if strings.HasPrefix(entry, "TEST_RUNNER_") {
					runnerSettings = append(runnerSettings, entry)
				}
			}
			t.Fatalf("runner settings = %v, want them to contain %q", runnerSettings, wanted)
		}
	}

	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !process.stopped {
		t.Fatal("Close left the runner it started behind")
	}
}

// An operator-started runner is left untouched.
func TestOpenLeavesAnOperatorStartedRunnerAlone(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	})
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		t.Error("a driver with no bundle started a runner anyway")
		return nil, nil
	}
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// A runner that never answers has to fail Open, with the device named and the
// knob that changes the wait. Failing later, inside a flow, would report a
// setup problem as a flow failure.
func TestOpenGivesUpOnARunnerThatNeverAnswers(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	driver.startupTimeout = 20 * time.Millisecond
	process := &fakeRunnerProcess{}
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		return process, nil
	}

	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open accepted a runner that never answered")
	}
	if !strings.Contains(err.Error(), "UDID-1") {
		t.Fatalf("error = %q, want it to name the device", err)
	}
	if !strings.Contains(err.Error(), startupTimeoutEnv) {
		t.Fatalf("error = %q, want it to name the knob that changes the wait", err)
	}
	if !process.stopped {
		t.Fatal("the runner it started was left behind after a failed open")
	}
}

// A runner that dies on its own fails Open immediately, carrying what
// xcodebuild said. Waiting out the whole three-minute budget to report "no
// answer" would hide the one line that explains it — a wrong .xctestrun path,
// a simulator that is not booted.
func TestOpenReportsWhyTheRunnerDied(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	driver.runner = &RunnerBundle{XCTestRun: "/gone/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	// Short on purpose: if the death ever stopped being noticed, this would
	// still fail — with the timeout's wording instead of xcodebuild's.
	driver.startupTimeout = 200 * time.Millisecond
	exit := make(chan error, 1)
	exit <- errors.New("exit status 66: does not exist: /gone/Runner.xctestrun")
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		return &fakeRunnerProcess{exit: exit}, nil
	}

	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open accepted a runner that died")
	}
	if !strings.Contains(err.Error(), "/gone/Runner.xctestrun") {
		t.Fatalf("error = %q, want it to carry what xcodebuild said", err)
	}
}

// A port that answers /status before this driver started anything belongs to
// someone else: another driver, possibly serving another simulator, whose
// status is just as "ok". Starting a child then would bind nothing and every
// request would go to the stranger — a session took its screens from another
// simulator that way. Managed delivery refuses instead, naming the port.
func TestOpenRefusesAPortAnotherRunnerAlreadyHolds(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		}
	})
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	spawned := false
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		spawned = true
		return &fakeRunnerProcess{}, nil
	}

	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open used a runner it did not start")
	}
	for _, want := range []string{strconv.Itoa(driver.port), "UDID-1", "--driver-port"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to mention %q", err, want)
		}
	}
	if spawned {
		t.Fatal("a child was started on a port someone else holds")
	}
}

// The probe before the spawn closes most of the window, not all of it: a
// stranger can take the port after the probe, the child then loses the bind
// and dies, and the stranger answers /status — with its own id, or none.
// Only an answer carrying the id this driver launched its child with is the
// child.
func TestOpenRejectsAStatusAnswerFromAfterTheChildDied(t *testing.T) {
	t.Parallel()

	var started atomic.Bool
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && started.Load() {
			_, _ = w.Write([]byte(`{"status":"ok","runner":"someone-elses"}`))
			return
		}
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	exit := make(chan error, 1)
	exit <- errors.New("address already in use")
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		started.Store(true)
		return &fakeRunnerProcess{exit: exit}, nil
	}

	err := driver.Open(context.Background())
	if err == nil || !strings.Contains(err.Error(), "someone-elses") || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("Open() error = %v, want the dead child and the stranger named", err)
	}
}

// The stranger need not have replaced a dead child: a runner from before ids
// existed, or one another host started, answers ok without this driver's id
// while the child is still coming up. That answer is not the child either.
func TestOpenRejectsAStatusAnswerWithoutTheLaunchedID(t *testing.T) {
	t.Parallel()

	var started atomic.Bool
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && started.Load() {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	process := &fakeRunnerProcess{}
	driver.spawnRunner = func(context.Context, []string, []string) (runnerProcess, error) {
		started.Store(true)
		return process, nil
	}

	err := driver.Open(context.Background())
	if err == nil || !strings.Contains(err.Error(), `runner ""`) || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("Open() error = %v, want the id-less answer refused", err)
	}
	if !process.stopped {
		t.Fatal("the child was left running after its port went to someone else")
	}
}

// launchedRunnerID reads the id the host handed the child.
func launchedRunnerID(environment []string) string {
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "TEST_RUNNER_FLOWBATON_RUNNER_ID="); ok {
			return value
		}
	}
	return ""
}

// runnerStatusBody is what a runner launched with an id answers.
func runnerStatusBody(id string) string {
	return `{"status":"ok","runner":"` + id + `"}`
}

// A managed runner is a transport, not a test run: the host always ends it, so
// xcodebuild counts every session as a failed test and collects a whole
// simulator sysdiagnose -- 168M of system log archive per session on
// 2026-08-30 -- into a fresh derived-data directory under
// ~/Library/Developer/Xcode/DerivedData that nothing ever removes. Four
// sessions filled this host's disk to 159Mi free and blocked every tool. The
// runner wants neither the diagnostics nor the directory.
func TestTheManagedRunnerLeavesNoDerivedDataBehind(t *testing.T) {
	t.Parallel()

	var started atomic.Bool
	var launchedID atomic.Value
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && started.Load() {
			_, _ = w.Write([]byte(runnerStatusBody(launchedID.Load().(string))))
			return
		}
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	process := &fakeRunnerProcess{}
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	driver.spawnRunner = func(_ context.Context, args, environment []string) (runnerProcess, error) {
		process.args = args
		launchedID.Store(launchedRunnerID(environment))
		started.Store(true)
		return process, nil
	}
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if index := slices.Index(process.args, "-collect-test-diagnostics"); index < 0 ||
		index+1 >= len(process.args) || process.args[index+1] != "never" {
		t.Fatalf("xcodebuild args = %#v, want -collect-test-diagnostics never", process.args)
	}
	index := slices.Index(process.args, "-derivedDataPath")
	if index < 0 || index+1 >= len(process.args) {
		t.Fatalf("xcodebuild args = %#v, want a -derivedDataPath the driver owns", process.args)
	}
	derivedData := process.args[index+1]
	if _, err := os.Stat(derivedData); err != nil {
		t.Fatalf("derived-data directory %q is not there while the runner runs: %v", derivedData, err)
	}

	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(derivedData); !os.IsNotExist(err) {
		t.Fatalf("Close left %q behind: %v", derivedData, err)
	}
}
