package ios

import (
	"context"
	"errors"
	"net/http"
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

	// The port answers only once the child was started, as a real one does.
	var started atomic.Bool
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && started.Load() {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		http.Error(w, "not yet", http.StatusServiceUnavailable)
	})
	process := &fakeRunnerProcess{}
	driver.runner = &RunnerBundle{XCTestRun: "/built/Runner.xctestrun"}
	driver.startupPoll = time.Millisecond
	driver.spawnRunner = func(_ context.Context, args, environment []string) (runnerProcess, error) {
		process.args, process.environment = args, environment
		started.Store(true)
		return process, nil
	}

	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	want := []string{
		"test-without-building",
		"-xctestrun", "/built/Runner.xctestrun",
		"-destination", "platform=iOS Simulator,id=UDID-1",
		"-only-testing:" + runnerServeTest,
	}
	if !reflect.DeepEqual(process.args, want) {
		t.Fatalf("xcodebuild args = %#v, want %#v", process.args, want)
	}
	// The runner reads its own configuration from the process it is launched
	// with, and only TEST_RUNNER_-prefixed names survive the trip into the
	// simulator — a plain PORT never arrives.
	for _, wanted := range []string{
		"TEST_RUNNER_FLOWBATON_RUNNER_SERVE=1",
		"TEST_RUNNER_PORT=" + strconv.Itoa(driver.port),
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
