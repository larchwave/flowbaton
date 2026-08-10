package android

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/larchwave/flowbaton/internal/android/pbwire"
	"github.com/larchwave/flowbaton/internal/device"
)

// The composition batch for Android: the gRPC agent half rides a REAL
// grpcwire connection against an h2c stub speaking the gRPC framing by hand,
// and the adb half rides the recording runner — so every test is about what
// actually crosses each boundary.

func TestDriverIsADeviceDriver(t *testing.T) {
	t.Parallel()

	var _ device.Driver = (*Driver)(nil)
	var _ device.OrientationReader = (*Driver)(nil)
}

// agentRecorder remembers every rpc the stub served, in order.
type agentRecorder struct {
	mu       sync.Mutex
	methods  []string
	requests map[string][][]byte
}

func (recorder *agentRecorder) record(method string, messages [][]byte) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.methods = append(recorder.methods, method)
	if recorder.requests == nil {
		recorder.requests = map[string][][]byte{}
	}
	recorder.requests[method] = append(recorder.requests[method], messages...)
}

func (recorder *agentRecorder) calledMethods() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]string(nil), recorder.methods...)
}

func (recorder *agentRecorder) messagesFor(method string) [][]byte {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([][]byte(nil), recorder.requests[method]...)
}

// stubFrame builds a length-prefixed gRPC message on the stub's side, kept
// separate from production framing so a framing bug cannot cancel itself out.
func stubFrame(message []byte) []byte {
	framed := make([]byte, 5+len(message))
	binary.BigEndian.PutUint32(framed[1:5], uint32(len(message)))
	copy(framed[5:], message)
	return framed
}

// splitStubFrames undoes the framing for every message of a request body.
func splitStubFrames(t *testing.T, body []byte) [][]byte {
	t.Helper()
	var messages [][]byte
	for len(body) > 0 {
		if len(body) < 5 {
			t.Fatalf("truncated frame prefix: % x", body)
		}
		length := int(binary.BigEndian.Uint32(body[1:5]))
		if len(body) < 5+length {
			t.Fatalf("frame announces %d bytes, %d remain", length, len(body)-5)
		}
		messages = append(messages, body[5:5+length])
		body = body[5+length:]
	}
	return messages
}

// startAgentStub serves the gRPC surface over h2c and returns the recorder
// plus the port the driver should treat as its host port. respond returns
// the reply message for a method; nil means an empty message.
func startAgentStub(
	t *testing.T,
	respond func(method string, requests [][]byte) []byte,
) (*agentRecorder, int) {
	t.Helper()
	recorder := &agentRecorder{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("stub read: %v", err)
		}
		messages := splitStubFrames(t, body)
		recorder.record(r.URL.Path, messages)
		var reply []byte
		if respond != nil {
			reply = respond(r.URL.Path, messages)
		}
		w.Header().Set("Content-Type", "application/grpc+proto")
		_, _ = w.Write(stubFrame(reply))
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return recorder, listener.Addr().(*net.TCPAddr).Port
}

// answerDeviceInfo is the minimal respond function: enough for Open's probe.
func answerDeviceInfo(width, height uint32) func(string, [][]byte) []byte {
	return func(method string, _ [][]byte) []byte {
		if method == pbwire.MethodDeviceInfo {
			return pbwire.DeviceInfo{WidthPixels: width, HeightPixels: height}.Marshal()
		}
		return nil
	}
}

const testSerial = "emulator-5554"

// newOpenDriver opens a driver against the stub, with a recording runner
// standing in for adb.
func newOpenDriver(
	t *testing.T,
	respond func(string, [][]byte) []byte,
) (*Driver, *recordingRunner, *agentRecorder) {
	t.Helper()
	if respond == nil {
		respond = answerDeviceInfo(1080, 2400)
	}
	recorder, port := startAgentStub(t, respond)
	runner := &recordingRunner{}
	driver := NewDriver(testSerial, port, runner, nil)
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })
	return driver, runner, recorder
}

func TestOpenForwardsThePortAndProbesTheAgent(t *testing.T) {
	t.Parallel()

	driver, runner, recorder := newOpenDriver(t, nil)
	port := driver.hostPort
	want := []string{"-s", testSerial, "forward",
		"tcp:" + strconv.Itoa(port), "tcp:7001"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0][1:], want) {
		t.Fatalf("adb calls = %v, want exactly the forward %v", runner.calls, want)
	}
	if got := recorder.calledMethods(); len(got) != 1 || got[0] != pbwire.MethodDeviceInfo {
		t.Fatalf("agent calls = %v, want the deviceInfo probe", got)
	}
	if got, want := driver.Name(), fmt.Sprintf("android:%s:%d", testSerial, port); got != want {
		t.Fatalf("Name() = %q, want %q", got, want)
	}
}

func TestOpenNamesTheAddressWhenTheAgentIsNotUp(t *testing.T) {
	t.Parallel()

	// A dead port instead of a stub: the true state of a device whose
	// instrumentation was never started.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	driver := NewDriver(testSerial, port, &recordingRunner{}, nil)
	err = driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open() succeeded against a dead port")
	}
	for _, fragment := range []string{
		"127.0.0.1:" + strconv.Itoa(port),
		"instrumentation",
		testSerial,
	} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to contain %q", err, fragment)
		}
	}
}

func TestOpenRemovesTheForwardWhenTheProbeFails(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	runner := &recordingRunner{}
	_ = NewDriver(testSerial, port, runner, nil).Open(context.Background())
	if len(runner.calls) != 2 {
		t.Fatalf("adb calls = %v, want the forward and its removal", runner.calls)
	}
	want := []string{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)}
	if !reflect.DeepEqual(runner.calls[1][1:], want) {
		t.Fatalf("second call = %v, want %v", runner.calls[1][1:], want)
	}
}

func TestCloseRemovesTheForward(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	last := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "forward", "--remove",
		"tcp:" + strconv.Itoa(driver.hostPort)}
	if !reflect.DeepEqual(last, want) {
		t.Fatalf("last adb call = %v, want %v", last, want)
	}
}

// The managed-agent lifecycle (specs/02-device-drivers.md §2.2): when Open
// is handed the two APKs it owns delivery — reinstall, `am instrument`, and
// the 100ms reachability poll bounded by the 15000ms startup timeout.

// newManagedDriver builds (but does not open) a driver that owns the agent
// lifecycle, with the waits compressed to test scale.
func newManagedDriver(port int, runner CommandRunner) *Driver {
	driver := NewDriver(testSerial, port, runner,
		&AgentAPKs{App: "/apks/agent-app.apk", Test: "/apks/agent-test.apk"})
	driver.startupPoll = time.Millisecond
	driver.startupTimeout = 2 * time.Second
	return driver
}

// deadPort returns a port nothing listens on.
func deadPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

// awaitInstrumentCall waits for the backgrounded `am instrument` launch to be
// recorded: it runs on its own goroutine, so only its eventual presence — not
// its interleaving — is deterministic.
func awaitInstrumentCall(t *testing.T, runner *recordingRunner) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, call := range runner.recorded() {
			if slices.Contains(call, "instrument") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no am instrument call was recorded: %v", runner.recorded())
		}
		time.Sleep(time.Millisecond)
	}
}

func isADBOperation(args []string, operation ...string) bool {
	if len(args) < 2+len(operation) {
		return false
	}
	return slices.Equal(args[2:2+len(operation)], operation)
}

func managedCleanupTail(port int) [][]string {
	return [][]string{
		{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)},
		{"-s", testSerial, "uninstall", AgentAppPackage},
		{"-s", testSerial, "uninstall", AgentTestPackage},
	}
}

func assertCallTail(t *testing.T, calls, want [][]string) {
	t.Helper()
	if len(calls) < len(want) {
		t.Fatalf("adb calls = %v, want tail %v", calls, want)
	}
	tail := calls[len(calls)-len(want):]
	for index := range want {
		if !reflect.DeepEqual(tail[index][1:], want[index]) {
			t.Fatalf("adb call tail = %v, want %v", tail, want)
		}
	}
}

func TestManagedOpenRollsBackAtEveryMutationFailureBoundary(t *testing.T) {
	t.Parallel()

	primaryErr := errors.New("injected open failure")
	tests := []struct {
		name        string
		fails       func([]string) bool
		wantForward bool
	}{
		{
			name:  "app install",
			fails: func(args []string) bool { return slices.Contains(args, "/apks/agent-app.apk") },
		},
		{
			name:  "test install",
			fails: func(args []string) bool { return slices.Contains(args, "/apks/agent-test.apk") },
		},
		{
			name: "mock location grant",
			fails: func(args []string) bool {
				return isADBOperation(args, "shell", "appops")
			},
		},
		{
			name: "forward",
			fails: func(args []string) bool {
				return isADBOperation(args, "forward") && !slices.Contains(args, "--remove")
			},
			wantForward: true,
		},
		{
			name:        "instrumentation start",
			fails:       func(args []string) bool { return slices.Contains(args, "instrument") },
			wantForward: true,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failed := false
			runner := &recordingRunner{respond: func(args []string) ([]byte, error) {
				if !failed && test.fails(args) {
					failed = true
					return nil, primaryErr
				}
				return nil, nil
			}}
			port := deadPort(t)
			driver := newManagedDriver(port, runner)
			err := driver.Open(context.Background())
			if !errors.Is(err, primaryErr) {
				t.Fatalf("Open() error = %v, want injected failure", err)
			}
			if driver.stopInstrumentation != nil {
				t.Fatal("failed Open left an instrumentation cancel function installed")
			}
			calls := runner.recorded()
			want := managedCleanupTail(port)[1:]
			if test.wantForward {
				want = managedCleanupTail(port)
			}
			assertCallTail(t, calls, want)
		})
	}
}

type cancellingFailureRunner struct {
	recordingRunner
	cancel              context.CancelFunc
	primaryErr          error
	rollbackErr         error
	failed              bool
	cleanupContextErr   error
	cleanupHasDeadline  bool
	rollbackFailureUsed bool
}

func (runner *cancellingFailureRunner) Run(
	ctx context.Context, name string, args ...string,
) ([]byte, error) {
	runner.mu.Lock()
	runner.calls = append(runner.calls, append([]string{name}, args...))
	runner.mu.Unlock()
	if !runner.failed && isADBOperation(args, "shell", "appops") {
		runner.failed = true
		runner.cancel()
		return nil, runner.primaryErr
	}
	if runner.failed && isADBOperation(args, "uninstall") {
		runner.cleanupContextErr = ctx.Err()
		_, runner.cleanupHasDeadline = ctx.Deadline()
		if !runner.rollbackFailureUsed {
			runner.rollbackFailureUsed = true
			return nil, runner.rollbackErr
		}
	}
	return nil, nil
}

func TestManagedOpenUsesFreshBoundedCleanupAndJoinsRollbackFailure(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	primaryErr := errors.New("mock location setup failed")
	rollbackErr := errors.New("package cleanup failed")
	runner := &cancellingFailureRunner{
		cancel:      cancel,
		primaryErr:  primaryErr,
		rollbackErr: rollbackErr,
	}
	driver := newManagedDriver(deadPort(t), runner)
	err := driver.Open(ctx)
	if !errors.Is(err, primaryErr) {
		t.Fatalf("Open() error = %v, want original failure", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("Open() error = %v, want rollback failure", err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("execution context error = %v, want cancellation", ctx.Err())
	}
	if runner.cleanupContextErr != nil {
		t.Fatalf("rollback context error = %v, want fresh context", runner.cleanupContextErr)
	}
	if !runner.cleanupHasDeadline {
		t.Fatal("rollback context had no deadline")
	}
}

type blockingInstrumentationRunner struct {
	recordingRunner
	stopped chan struct{}
}

func (runner *blockingInstrumentationRunner) Run(
	ctx context.Context, name string, args ...string,
) ([]byte, error) {
	runner.mu.Lock()
	runner.calls = append(runner.calls, append([]string{name}, args...))
	runner.mu.Unlock()
	if slices.Contains(args, "instrument") {
		<-ctx.Done()
		close(runner.stopped)
		return nil, ctx.Err()
	}
	return nil, nil
}

func TestManagedOpenStopsInstrumentationWhenReachabilityFails(t *testing.T) {
	t.Parallel()

	port := deadPort(t)
	runner := &blockingInstrumentationRunner{stopped: make(chan struct{})}
	driver := newManagedDriver(port, runner)
	driver.startupTimeout = 20 * time.Millisecond
	driver.startupPoll = time.Millisecond
	if err := driver.Open(context.Background()); err == nil {
		t.Fatal("Open() succeeded against an unreachable managed agent")
	}
	select {
	case <-runner.stopped:
	default:
		t.Fatal("failed Open returned before instrumentation stopped")
	}
	assertCallTail(t, runner.recorded(), managedCleanupTail(port))
}

func TestOpenInstallsAndStartsTheAgentWhenAPKsAreGiven(t *testing.T) {
	t.Parallel()

	recorder, port := startAgentStub(t, answerDeviceInfo(1080, 2400))
	runner := &recordingRunner{}
	driver := newManagedDriver(port, runner)
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })
	awaitInstrumentCall(t, runner)

	calls := runner.recorded()
	want := [][]string{
		{"-s", testSerial, "uninstall", "dev.larchwave.flowbaton"},
		{"-s", testSerial, "uninstall", "dev.larchwave.flowbaton.test"},
		{"-s", testSerial, "install", "-r", "/apks/agent-app.apk"},
		{"-s", testSerial, "install", "-r", "/apks/agent-test.apk"},
		{"-s", testSerial, "shell", "appops", "set",
			"dev.larchwave.flowbaton", "android:mock_location", "allow"},
		{"-s", testSerial, "forward", "tcp:" + strconv.Itoa(port), "tcp:7001"},
		{"-s", testSerial, "shell", "am", "instrument", "-w", "-m",
			"-e", "debug", "false",
			"-e", "class", "dev.larchwave.flowbaton.FlowBatonDriverService#grpcServer",
			"-e", "port", "7001",
			"dev.larchwave.flowbaton.test/androidx.test.runner.AndroidJUnitRunner"},
	}
	if len(calls) != len(want) {
		t.Fatalf("adb calls = %v, want %v", calls, want)
	}
	for i := range want {
		if !reflect.DeepEqual(calls[i][1:], want[i]) {
			t.Fatalf("call %d = %v, want %v", i, calls[i][1:], want[i])
		}
	}
	if got := recorder.calledMethods(); len(got) == 0 || got[len(got)-1] != pbwire.MethodDeviceInfo {
		t.Fatalf("agent calls = %v, want a deviceInfo probe", got)
	}
}

func TestOpenPollsUntilTheAgentAnswers(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	_, port := startAgentStub(t, func(method string, _ [][]byte) []byte {
		if method != pbwire.MethodDeviceInfo {
			return nil
		}
		if attempts.Add(1) < 3 {
			return []byte{0xff} // malformed: the agent is not ready yet
		}
		return pbwire.DeviceInfo{WidthPixels: 1080, HeightPixels: 2400}.Marshal()
	})
	driver := newManagedDriver(port, &recordingRunner{})
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })
	if got := attempts.Load(); got < 3 {
		t.Fatalf("deviceInfo probes = %d, want at least 3: Open must poll, not probe once", got)
	}
}

func TestOpenGivesUpAtTheStartupTimeout(t *testing.T) {
	t.Parallel()

	port := deadPort(t)
	runner := &recordingRunner{}
	driver := newManagedDriver(port, runner)
	driver.startupTimeout = 50 * time.Millisecond
	driver.startupPoll = 5 * time.Millisecond

	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open() succeeded against a dead port")
	}
	for _, fragment := range []string{testSerial, "FLOWBATON_DRIVER_STARTUP_TIMEOUT", "50ms"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to contain %q", err, fragment)
		}
	}
	wantRemove := []string{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)}
	removed := false
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[1:], wantRemove) {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("adb calls = %v, want the forward removed after the failed open", runner.recorded())
	}
}

func TestOpenHonorsTheStartupTimeoutEnv(t *testing.T) {
	port := deadPort(t)
	driver := newManagedDriver(port, &recordingRunner{})
	// The field would wait an hour; only the env override can bound this run.
	driver.startupTimeout = time.Hour

	t.Setenv(startupTimeoutEnv, "40")
	start := time.Now()
	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open() succeeded against a dead port")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Open() took %v, so the 40ms env override was ignored", elapsed)
	}
	if !strings.Contains(err.Error(), "40ms") {
		t.Fatalf("error = %q, want the env-provided 40ms named", err)
	}

	t.Setenv(startupTimeoutEnv, "soon")
	err = driver.Open(context.Background())
	if err == nil || !strings.Contains(err.Error(), startupTimeoutEnv) {
		t.Fatalf("error = %v, want a refusal naming %s", err, startupTimeoutEnv)
	}
}

func TestOpenReportsAnInstrumentationThatRefusesToStart(t *testing.T) {
	t.Parallel()

	port := deadPort(t)
	runner := &recordingRunner{respond: func(args []string) ([]byte, error) {
		if slices.Contains(args, "instrument") {
			return []byte("INSTRUMENTATION_FAILED: dev.larchwave.flowbaton.test"), nil
		}
		return nil, nil
	}}
	driver := newManagedDriver(port, runner)
	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open() succeeded although the instrumentation refused to start")
	}
	if !strings.Contains(err.Error(), "INSTRUMENTATION_FAILED") {
		t.Fatalf("error = %q, want the instrumentation's own output carried", err)
	}
}

func TestCloseStopsAndUninstallsTheManagedAgent(t *testing.T) {
	t.Parallel()

	_, port := startAgentStub(t, answerDeviceInfo(1080, 2400))
	runner := &recordingRunner{}
	driver := newManagedDriver(port, runner)
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	awaitInstrumentCall(t, runner)

	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	calls := runner.recorded()
	if len(calls) < 3 {
		t.Fatalf("adb calls = %v, want the close sequence", calls)
	}
	want := [][]string{
		{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)},
		{"-s", testSerial, "uninstall", "dev.larchwave.flowbaton"},
		{"-s", testSerial, "uninstall", "dev.larchwave.flowbaton.test"},
	}
	tail := calls[len(calls)-3:]
	for i := range want {
		if !reflect.DeepEqual(tail[i][1:], want[i]) {
			t.Fatalf("close calls = %v, want tail %v", tail, want)
		}
	}
}

func TestDeviceInfoReportsTheGridAsPixels(t *testing.T) {
	t.Parallel()

	driver, _, _ := newOpenDriver(t, answerDeviceInfo(1080, 2400))
	info, err := driver.DeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("DeviceInfo() error = %v", err)
	}
	want := device.DeviceInfo{
		Platform:    Platform,
		WidthPixels: 1080, HeightPixels: 2400,
		WidthGrid: 1080, HeightGrid: 2400,
	}
	if info != want {
		// specs/02-device-drivers.md line 28: the Android grid IS pixels.
		t.Fatalf("DeviceInfo() = %+v, want grid == pixels: %+v", info, want)
	}
}

func TestLaunchAppMapsTypedArgumentsToJavaClassNames(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, nil)
	err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{
		AppID: "com.example.a",
		Arguments: []device.LaunchArgument{
			{Key: "mode", Value: "probe", Type: "string"},
			{Key: "flag", Value: "true", Type: "boolean"},
			{Key: "count", Value: "3", Type: "integer"},
			{Key: "ratio", Value: "0.5", Type: "double"},
		},
	})
	if err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	messages := recorder.messagesFor(pbwire.MethodLaunchApp)
	if len(messages) != 1 {
		t.Fatalf("launchApp messages = %d, want 1", len(messages))
	}
	var request pbwire.LaunchAppRequest
	if err := request.Unmarshal(messages[0]); err != nil {
		t.Fatalf("decoding launchApp request: %v", err)
	}
	if request.PackageName != "com.example.a" {
		t.Fatalf("packageName = %q", request.PackageName)
	}
	want := []pbwire.ArgumentValue{
		{Key: "mode", Value: "probe", Type: "java.lang.String"},
		{Key: "flag", Value: "true", Type: "java.lang.Boolean"},
		{Key: "count", Value: "3", Type: "java.lang.Integer"},
		{Key: "ratio", Value: "0.5", Type: "java.lang.Double"},
	}
	if !reflect.DeepEqual(request.Arguments, want) {
		t.Fatalf("arguments = %+v, want the java.lang FQNs %+v", request.Arguments, want)
	}
}

func TestAddMediaRefusesAPI28BeforeUploading(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "holiday.mp4")
	if err := os.WriteFile(path, []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	driver, runner, recorder := newOpenDriver(t, nil)
	runner.respond = func(args []string) ([]byte, error) {
		if slices.Contains(args, "ro.build.version.sdk") {
			return []byte("28\n"), nil
		}
		return nil, nil
	}

	err := driver.AddMedia(context.Background(), device.AddMediaRequest{
		Files: []device.MediaFile{{Path: path}},
	})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("AddMedia() error = %v, want device.ErrUnsupported", err)
	}
	if got := recorder.messagesFor(pbwire.MethodAddMedia); len(got) != 0 {
		t.Fatalf("agent received %d addMedia frames before the refusal", len(got))
	}
}

func TestRuntimePreflightRefusesAddMediaBeforeManagedOpenMutation(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	runner.respond = func(args []string) ([]byte, error) {
		if slices.Contains(args, "ro.build.version.sdk") {
			return []byte("28\n"), nil
		}
		return nil, nil
	}
	driver := NewDriver(testSerial, 7001, runner, &AgentAPKs{App: "agent.apk", Test: "agent-test.apk"})
	err := driver.PreflightRuntime(context.Background(), device.RuntimeRequirements{
		Commands: []string{"launchApp", "addMedia"},
	})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("PreflightRuntime() error = %v, want device.ErrUnsupported", err)
	}
	for _, call := range runner.calls {
		if slices.Contains(call, "install") || slices.Contains(call, "uninstall") || slices.Contains(call, "instrument") {
			t.Fatalf("runtime preflight mutated the managed driver: %v", runner.calls)
		}
	}
	if len(runner.calls) != 1 || !slices.Contains(runner.calls[0], "ro.build.version.sdk") {
		t.Fatalf("runtime preflight calls = %v, want only the read-only SDK query", runner.calls)
	}
}

func TestLaunchAppRefusesAnUnknownArgumentTypeBeforeTheWire(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, nil)
	err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{
		AppID:     "com.example.a",
		Arguments: []device.LaunchArgument{{Key: "x", Value: "1", Type: "long"}},
	})
	if err == nil {
		t.Fatal("an unknown argument type crossed the wire")
	}
	if calls := recorder.messagesFor(pbwire.MethodLaunchApp); len(calls) != 0 {
		t.Fatalf("launchApp was called %d times; the refusal must come first", len(calls))
	}
}

func TestTapRoundsOntoTheGridAndRefusesNegatives(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, nil)
	if err := driver.Tap(context.Background(), device.TapRequest{
		Point: device.Point{X: 9.6, Y: 20.4},
	}); err != nil {
		t.Fatalf("Tap() error = %v", err)
	}
	var request pbwire.TapRequest
	if err := request.Unmarshal(recorder.messagesFor(pbwire.MethodTap)[0]); err != nil {
		t.Fatal(err)
	}
	if request.X != 10 || request.Y != 20 {
		t.Fatalf("tap = (%d,%d), want (10,20)", request.X, request.Y)
	}

	if err := driver.Tap(context.Background(), device.TapRequest{
		Point: device.Point{X: -1, Y: 5},
	}); err == nil {
		// A negative coordinate cast onto uint32 would wrap to the far side
		// of the screen — a tap nobody asked for, reported as success.
		t.Fatal("a negative coordinate was accepted")
	}
	if calls := recorder.messagesFor(pbwire.MethodTap); len(calls) != 1 {
		t.Fatalf("tap crossed the wire %d times, want only the valid one", len(calls))
	}
}

func TestInputAndEraseGoThroughTheAgent(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, nil)
	if err := driver.InputText(context.Background(), device.InputTextRequest{
		Text: "héllo",
	}); err != nil {
		t.Fatal(err)
	}
	var input pbwire.InputTextRequest
	if err := input.Unmarshal(recorder.messagesFor(pbwire.MethodInputText)[0]); err != nil {
		t.Fatal(err)
	}
	if input.Text != "héllo" {
		t.Fatalf("text = %q, want the unicode intact", input.Text)
	}

	if err := driver.EraseText(context.Background(), device.EraseTextRequest{
		CharactersToErase: 12,
	}); err != nil {
		t.Fatal(err)
	}
	var erase pbwire.EraseAllTextRequest
	if err := erase.Unmarshal(recorder.messagesFor(pbwire.MethodEraseAllText)[0]); err != nil {
		t.Fatal(err)
	}
	if erase.CharactersToErase != 12 {
		t.Fatalf("charactersToErase = %d, want 12", erase.CharactersToErase)
	}
}

func TestSetLocationEnablesMockProvidersFirst(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, nil)
	if err := driver.SetLocation(context.Background(), device.Location{
		Latitude: 48.5, Longitude: -2.25,
	}); err != nil {
		t.Fatalf("SetLocation() error = %v", err)
	}
	methods := recorder.calledMethods()
	// [0] is Open's probe. The coordinate lands on the MOCK providers, which
	// do not exist until the agent registers them — order is the behavior.
	want := []string{
		pbwire.MethodDeviceInfo,
		pbwire.MethodEnableMockLocationProviders,
		pbwire.MethodSetLocation,
	}
	if !reflect.DeepEqual(methods, want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	var request pbwire.SetLocationRequest
	if err := request.Unmarshal(recorder.messagesFor(pbwire.MethodSetLocation)[0]); err != nil {
		t.Fatal(err)
	}
	if request.Latitude != 48.5 || request.Longitude != -2.25 {
		t.Fatalf("location = (%v,%v), want (48.5,-2.25)", request.Latitude, request.Longitude)
	}
}

func TestScreenshotReturnsTheAgentBytes(t *testing.T) {
	t.Parallel()

	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	driver, _, _ := newOpenDriver(t, func(method string, _ [][]byte) []byte {
		switch method {
		case pbwire.MethodDeviceInfo:
			return pbwire.DeviceInfo{WidthPixels: 1, HeightPixels: 1}.Marshal()
		case pbwire.MethodScreenshot:
			return pbwire.ScreenshotResponse{Bytes: png}.Marshal()
		}
		return nil
	})
	got, err := driver.TakeScreenshot(context.Background(), device.ScreenshotRequest{})
	if err != nil {
		t.Fatalf("TakeScreenshot() error = %v", err)
	}
	if !reflect.DeepEqual(got, png) {
		t.Fatalf("screenshot = % x, want % x", got, png)
	}
}

func TestWaitUntilScreenIsStaticInvertsIsWindowUpdating(t *testing.T) {
	t.Parallel()

	for updating, wantStatic := range map[bool]bool{true: false, false: true} {
		driver, _, _ := newOpenDriver(t, func(method string, _ [][]byte) []byte {
			switch method {
			case pbwire.MethodDeviceInfo:
				return pbwire.DeviceInfo{WidthPixels: 1, HeightPixels: 1}.Marshal()
			case pbwire.MethodIsWindowUpdating:
				return pbwire.CheckWindowUpdatingResponse{IsWindowUpdating: updating}.Marshal()
			}
			return nil
		})
		static, err := driver.WaitUntilScreenIsStatic(context.Background(), device.ScreenStaticRequest{})
		if err != nil {
			t.Fatalf("WaitUntilScreenIsStatic() error = %v", err)
		}
		if static != wantStatic {
			t.Fatalf("static = %v while updating = %v", static, updating)
		}
	}
}

func TestWaitForAppToSettleReturnsNilWhileUpdating(t *testing.T) {
	t.Parallel()

	hierarchyCalls := 0
	driver, _, _ := newOpenDriver(t, func(method string, _ [][]byte) []byte {
		switch method {
		case pbwire.MethodDeviceInfo:
			return pbwire.DeviceInfo{WidthPixels: 1, HeightPixels: 1}.Marshal()
		case pbwire.MethodIsWindowUpdating:
			return pbwire.CheckWindowUpdatingResponse{IsWindowUpdating: true}.Marshal()
		case pbwire.MethodViewHierarchy:
			hierarchyCalls++
		}
		return nil
	})
	hierarchy, err := driver.WaitForAppToSettle(context.Background(), device.SettleRequest{})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	// nil means "could not confirm settling" — the frozen contract forbids
	// reading it as settled, and no hierarchy round trip should happen.
	if hierarchy != nil {
		t.Fatalf("hierarchy = %+v while the window is updating, want nil", hierarchy)
	}
	if hierarchyCalls != 0 {
		t.Fatal("viewHierarchy was fetched while the window was still updating")
	}
}

func TestWaitForAppToSettleReturnsTheHierarchyOnceStatic(t *testing.T) {
	t.Parallel()

	driver, _, recorder := newOpenDriver(t, func(method string, _ [][]byte) []byte {
		switch method {
		case pbwire.MethodDeviceInfo:
			return pbwire.DeviceInfo{WidthPixels: 1, HeightPixels: 1}.Marshal()
		case pbwire.MethodIsWindowUpdating:
			return pbwire.CheckWindowUpdatingResponse{IsWindowUpdating: false}.Marshal()
		case pbwire.MethodViewHierarchy:
			return pbwire.ViewHierarchyResponse{Hierarchy: `<hierarchy rotation="0">
  <node text="Ready" bounds="[0,0][10,10]"/>
</hierarchy>`}.Marshal()
		}
		return nil
	})
	hierarchy, err := driver.WaitForAppToSettle(context.Background(), device.SettleRequest{
		AppID: "com.example.a",
	})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	if hierarchy == nil || len(hierarchy.Root.Children) != 1 {
		t.Fatalf("hierarchy = %+v, want the parsed tree", hierarchy)
	}
	if got := hierarchy.Root.Children[0].Attributes["text"]; got != "Ready" {
		t.Fatalf("text = %q", got)
	}
	var request pbwire.CheckWindowUpdatingRequest
	if err := request.Unmarshal(recorder.messagesFor(pbwire.MethodIsWindowUpdating)[0]); err != nil {
		t.Fatal(err)
	}
	if request.AppID != "com.example.a" {
		t.Fatalf("isWindowUpdating appId = %q, want the settle request's", request.AppID)
	}
}

func TestContentDescriptorParsesTheAgentXML(t *testing.T) {
	t.Parallel()

	driver, _, _ := newOpenDriver(t, func(method string, _ [][]byte) []byte {
		switch method {
		case pbwire.MethodDeviceInfo:
			return pbwire.DeviceInfo{WidthPixels: 1, HeightPixels: 1}.Marshal()
		case pbwire.MethodViewHierarchy:
			return pbwire.ViewHierarchyResponse{Hierarchy: sampleHierarchy}.Marshal()
		}
		return nil
	})
	root, err := driver.ContentDescriptor(context.Background(), device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if len(root.Children) != 1 || len(root.Children[0].Children) != 3 {
		t.Fatalf("tree shape = %+v, want the sample's 1×3", root)
	}
}

func TestAddMediaStreamsChunksCarryingNameAndExtension(t *testing.T) {
	t.Parallel()

	// Larger than one chunk, so the split is exercised for real.
	payload := make([]byte, mediaChunkBytes+1000)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	path := filepath.Join(t.TempDir(), "holiday.mp4")
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	driver, runner, recorder := newOpenDriver(t, nil)
	runner.respond = func(args []string) ([]byte, error) {
		if slices.Contains(args, "ro.build.version.sdk") {
			return []byte("34\n"), nil
		}
		return nil, nil
	}
	if err := driver.AddMedia(context.Background(), device.AddMediaRequest{
		Files: []device.MediaFile{{Path: path}},
	}); err != nil {
		t.Fatalf("AddMedia() error = %v", err)
	}

	messages := recorder.messagesFor(pbwire.MethodAddMedia)
	if len(messages) != 2 {
		t.Fatalf("stream frames = %d, want 2 for %d bytes", len(messages), len(payload))
	}
	var reassembled []byte
	for index, message := range messages {
		var request pbwire.AddMediaRequest
		if err := request.Unmarshal(message); err != nil {
			t.Fatalf("frame %d: %v", index, err)
		}
		// Every frame carries the metadata, so the agent needs no
		// first-frame special case.
		if request.MediaName != "holiday" || request.MediaExt != "mp4" {
			t.Fatalf("frame %d metadata = %q.%q, want holiday.mp4",
				index, request.MediaName, request.MediaExt)
		}
		if request.Payload == nil {
			t.Fatalf("frame %d has no payload", index)
		}
		reassembled = append(reassembled, request.Payload.Data...)
	}
	if !reflect.DeepEqual(reassembled, payload) {
		t.Fatalf("reassembled %d bytes differ from the %d written", len(reassembled), len(payload))
	}
}

func TestDriverRefusesTheOperationsAndroidCannotPerform(t *testing.T) {
	t.Parallel()

	driver, runner, recorder := newOpenDriver(t, nil)
	ctx := context.Background()
	adbCalls, agentCalls := len(runner.calls), len(recorder.calledMethods())

	for _, test := range []struct {
		name string
		call func() error
	}{
		// SetAndroidChromeDevToolsEnabled is not here any more: it forwards the
		// abstract CDP socket and merges the page. See webview_devtools_test.go.
		{"QueryOnDeviceElements", func() error {
			_, err := driver.QueryOnDeviceElements(ctx, device.QueryRequest{Expression: "x"})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil {
				t.Fatalf("%s succeeded; want an explicit refusal", test.name)
			}
			if !errors.Is(err, device.ErrUnsupported) {
				t.Fatalf("%s error = %v, want it to wrap device.ErrUnsupported", test.name, err)
			}
		})
	}
	if len(runner.calls) != adbCalls || len(recorder.calledMethods()) != agentCalls {
		t.Fatal("a refused operation still reached a device boundary")
	}
}

func TestCapabilitiesReportTheSameRefusals(t *testing.T) {
	t.Parallel()

	// A refusal at call time is too late for preflight: whatever the driver
	// refuses must also be declared unsupported, and what it performs must
	// not be declared refused.
	capabilities := NewDriver(testSerial, 7001, &recordingRunner{}, nil).Capabilities()
	if capabilities.Platform != Platform {
		t.Fatalf("Platform = %q, want android", capabilities.Platform)
	}
	for feature, supported := range map[string]bool{
		"androidChromeDevTools": true,
		"screenRecording":       true,
		"onDeviceQuery":         false,
		"deviceLogCapture":      true,
		"crashArtifacts":        true,
		"proxy":                 true,
		"airplaneMode":          true,
		"backPress":             true,
		"browserChoice":         true,
	} {
		declared, present := capabilities.Features[feature]
		if !present {
			t.Fatalf("Capabilities() does not mention %q", feature)
		}
		if declared != supported {
			t.Fatalf("Capabilities()[%q] = %v; call-time behavior says %v", feature, declared, supported)
		}
	}
}

func TestClearKeychainIsADocumentedNoOp(t *testing.T) {
	t.Parallel()

	driver, runner, recorder := newOpenDriver(t, nil)
	adbCalls, agentCalls := len(runner.calls), len(recorder.calledMethods())
	if err := driver.ClearKeychain(context.Background()); err != nil {
		t.Fatalf("ClearKeychain() error = %v; the docs say it has no effect on Android", err)
	}
	if len(runner.calls) != adbCalls || len(recorder.calledMethods()) != agentCalls {
		t.Fatal("ClearKeychain touched a device boundary; there is no keychain to touch")
	}
}

func TestLifecycleVerbsUseTheExactAdbCommands(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	ctx := context.Background()
	base := len(runner.calls)

	for _, test := range []struct {
		name string
		call func() error
		want []string
	}{
		{"StopApp", func() error {
			return driver.StopApp(ctx, device.AppRequest{AppID: "com.example.a"})
		}, []string{"shell", "am", "force-stop", "com.example.a"}},
		{"KillApp", func() error {
			return driver.KillApp(ctx, device.AppRequest{AppID: "com.example.a"})
		}, []string{"shell", "am", "kill", "com.example.a"}},
		{"ClearAppState", func() error {
			return driver.ClearAppState(ctx, device.AppRequest{AppID: "com.example.a"})
		}, []string{"shell", "pm", "clear", "com.example.a"}},
		{"BackPress", func() error { return driver.BackPress(ctx) },
			[]string{"shell", "input", "keyevent", "KEYCODE_BACK"}},
		{"HideKeyboard", func() error { return driver.HideKeyboard(ctx) },
			[]string{"shell", "input", "keyevent", "KEYCODE_BACK"}},
	} {
		if err := test.call(); err != nil {
			t.Fatalf("%s error = %v", test.name, err)
		}
		got := runner.calls[len(runner.calls)-1][1:]
		want := append([]string{"-s", testSerial}, test.want...)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s argv = %v, want %v", test.name, got, want)
		}
	}
	if len(runner.calls) != base+5 {
		t.Fatalf("adb calls = %d, want one per verb", len(runner.calls)-base)
	}
}

func TestPressKeyMapsOntoKeyevents(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	ctx := context.Background()
	for code, keyevent := range map[string]string{
		"ENTER":       "KEYCODE_ENTER",
		"BACK":        "KEYCODE_BACK",
		"HOME":        "KEYCODE_HOME",
		"LOCK":        "KEYCODE_SLEEP",
		"VOLUME_UP":   "KEYCODE_VOLUME_UP",
		"VOLUME_DOWN": "KEYCODE_VOLUME_DOWN",
		"POWER":       "KEYCODE_POWER",
	} {
		if err := driver.PressKey(ctx, device.PressKeyRequest{Code: device.KeyCode(code)}); err != nil {
			t.Fatalf("PressKey(%s) error = %v", code, err)
		}
		got := runner.calls[len(runner.calls)-1][1:]
		want := []string{"-s", testSerial, "shell", "input", "keyevent", keyevent}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("PressKey(%s) argv = %v, want %v", code, got, want)
		}
	}

	err := driver.PressKey(ctx, device.PressKeyRequest{Code: device.KeyCode("REMOTE_UP")})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("PressKey(REMOTE_UP) = %v, want an ErrUnsupported refusal", err)
	}
}

func TestLongPressIsAZeroTravelSwipe(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	if err := driver.LongPress(context.Background(), device.LongPressRequest{
		Point: device.Point{X: 540, Y: 1200}, DurationMillis: 2500,
	}); err != nil {
		t.Fatal(err)
	}
	got := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "shell", "input", "swipe",
		"540", "1200", "540", "1200", "2500"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestSwipeShapesLandOnInputSwipe(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, answerDeviceInfo(1000, 2000))
	ctx := context.Background()

	if err := driver.Swipe(ctx, device.SwipeRequest{
		Start: &device.Point{X: 100, Y: 1500}, End: &device.Point{X: 100, Y: 300},
		DurationMillis: 400,
	}); err != nil {
		t.Fatal(err)
	}
	got := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "shell", "input", "swipe",
		"100", "1500", "100", "300", "400"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("point swipe argv = %v, want %v", got, want)
	}

	// Directional: center → 10% height (specs/02 line 43), duration default.
	if err := driver.Swipe(ctx, device.SwipeRequest{Direction: device.Direction("UP")}); err != nil {
		t.Fatal(err)
	}
	got = runner.calls[len(runner.calls)-1][1:]
	want = []string{"-s", testSerial, "shell", "input", "swipe",
		"500", "1000", "500", "200", "500"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("directional swipe argv = %v, want %v", got, want)
	}

	adbCalls := len(runner.calls)
	err := driver.Swipe(ctx, device.SwipeRequest{Direction: device.Direction("SIDEWAYS")})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("invented direction = %v, want an ErrUnsupported refusal", err)
	}
	if len(runner.calls) != adbCalls {
		t.Fatal("a refused direction still swiped somewhere")
	}
}

func TestOpenLinkForcesChromeOnlyWhenAsked(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	ctx := context.Background()

	if err := driver.OpenLink(ctx, device.OpenLinkRequest{Link: "https://example.invalid"}); err != nil {
		t.Fatal(err)
	}
	got := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "shell", "am", "start",
		"-a", "android.intent.action.VIEW", "-d", "https://example.invalid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("plain openLink argv = %v, want %v", got, want)
	}

	if err := driver.OpenLink(ctx, device.OpenLinkRequest{
		Link: "https://example.invalid", Browser: device.Browser("chrome"),
	}); err != nil {
		t.Fatal(err)
	}
	got = runner.calls[len(runner.calls)-1][1:]
	if got[len(got)-1] != "com.android.chrome" {
		t.Fatalf("forced openLink argv = %v, want it to end with com.android.chrome", got)
	}

	adbCalls := len(runner.calls)
	err := driver.OpenLink(ctx, device.OpenLinkRequest{
		Link: "https://example.invalid", Browser: device.Browser("firefox"),
	})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("firefox = %v, want an ErrUnsupported refusal", err)
	}
	if len(runner.calls) != adbCalls {
		t.Fatal("a refused browser still opened the link")
	}
}

func TestOrientationAndProxyLandOnSettings(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	ctx := context.Background()

	for orientation, rotation := range map[string]string{
		"PORTRAIT": "0", "LANDSCAPE_LEFT": "1", "UPSIDE_DOWN": "2", "LANDSCAPE_RIGHT": "3",
	} {
		if err := driver.SetOrientation(ctx, device.Orientation(orientation)); err != nil {
			t.Fatalf("SetOrientation(%s) error = %v", orientation, err)
		}
		got := runner.calls[len(runner.calls)-1][1:]
		want := []string{"-s", testSerial, "shell", "settings", "put", "system", "user_rotation", rotation}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("SetOrientation(%s) argv = %v, want %v", orientation, got, want)
		}
	}
	if err := driver.SetOrientation(ctx, device.Orientation("DIAGONAL")); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("SetOrientation(DIAGONAL) = %v, want a refusal", err)
	}

	if err := driver.SetProxy(ctx, device.Proxy{Host: "10.0.0.5", Port: 8888}); err != nil {
		t.Fatal(err)
	}
	got := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "shell", "settings", "put", "global", "http_proxy", "10.0.0.5:8888"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SetProxy argv = %v, want %v", got, want)
	}

	if err := driver.SetProxy(ctx, device.Proxy{Host: " ", Port: 8888}); err == nil {
		t.Fatal("a blank proxy host was written to the device")
	}

	if err := driver.ResetProxy(ctx); err != nil {
		t.Fatal(err)
	}
	got = runner.calls[len(runner.calls)-1][1:]
	want = []string{"-s", testSerial, "shell", "settings", "put", "global", "http_proxy", ":0"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ResetProxy argv = %v, want %v", got, want)
	}
}

func TestSetPermissionsExpandsAndAppliesInAStableOrder(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	base := len(runner.calls)
	if err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID: "com.example.a",
		Permissions: map[string]string{
			"camera":                           "allow",
			"bluetooth":                        "deny",
			"android.permission.ADD_VOICEMAIL": "unset",
		},
	}); err != nil {
		t.Fatalf("SetPermissions() error = %v", err)
	}
	var got [][]string
	for _, call := range runner.calls[base:] {
		got = append(got, call[1:])
	}
	prefix := []string{"-s", testSerial, "shell", "pm"}
	want := [][]string{
		// Keys sorted; bluetooth expands to the docs' own pair; a dotted key
		// passes through; unset revokes (pm has no per-permission reset).
		append(append([]string{}, prefix...), "revoke", "com.example.a", "android.permission.ADD_VOICEMAIL"),
		append(append([]string{}, prefix...), "revoke", "com.example.a", "android.permission.BLUETOOTH_CONNECT"),
		append(append([]string{}, prefix...), "revoke", "com.example.a", "android.permission.BLUETOOTH_SCAN"),
		append(append([]string{}, prefix...), "grant", "com.example.a", "android.permission.CAMERA"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pm calls = %v, want %v", got, want)
	}
}

func TestSetPermissionsAllReadsTheAppsOwnRuntimePermissions(t *testing.T) {
	t.Parallel()

	dump := `  Package [com.example.a]:
    User 0: installed=true
      runtime permissions:
        android.permission.CAMERA: granted=false, flags=[]
        android.permission.RECORD_AUDIO: granted=false, flags=[]
`
	driver, runner, _ := newOpenDriver(t, nil)
	runner.respond = func(args []string) ([]byte, error) {
		if len(args) >= 4 && args[3] == "dumpsys" {
			return []byte(dump), nil
		}
		return nil, nil
	}
	base := len(runner.calls)
	if err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID: "com.example.a",
		Permissions: map[string]string{
			"all":    "deny",
			"camera": "allow", // an explicit key overrides all, docs' own example
		},
	}); err != nil {
		t.Fatalf("SetPermissions() error = %v", err)
	}
	var got [][]string
	for _, call := range runner.calls[base:] {
		got = append(got, call[4:]) // strip binary, -s, serial, shell
	}
	want := [][]string{
		{"dumpsys", "package", "com.example.a"},
		{"pm", "revoke", "com.example.a", "android.permission.CAMERA"},
		{"pm", "revoke", "com.example.a", "android.permission.RECORD_AUDIO"},
		{"pm", "grant", "com.example.a", "android.permission.CAMERA"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want all first then the override %v", got, want)
	}
}

func TestSetPermissionsRefusesAnUnknownShortName(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	base := len(runner.calls)
	err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID:       "com.example.a",
		Permissions: map[string]string{"telepathy": "allow"},
	})
	if err == nil {
		t.Fatal("an unknown short name was invented into a permission id")
	}
	if !strings.Contains(err.Error(), "telepathy") {
		t.Fatalf("error = %q, want it to name the key", err)
	}
	if len(runner.calls) != base {
		t.Fatal("something reached pm for an unknown permission name")
	}
}

func TestAirplaneModeGoesThroughConnectivity(t *testing.T) {
	t.Parallel()

	driver, runner, _ := newOpenDriver(t, nil)
	runner.output = []byte("enabled\n")
	enabled, err := driver.IsAirplaneModeEnabled(context.Background())
	if err != nil || !enabled {
		t.Fatalf("IsAirplaneModeEnabled() = %v, %v; want true", enabled, err)
	}
	if err := driver.SetAirplaneMode(context.Background(), device.AirplaneModeRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	got := runner.calls[len(runner.calls)-1][1:]
	want := []string{"-s", testSerial, "shell", "cmd", "connectivity", "airplane-mode", "disable"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
}

func TestEveryMethodRefusesBeforeOpen(t *testing.T) {
	t.Parallel()

	// The agent half is unreachable before Open wires the forward; the error
	// must say so instead of panicking on a nil connection.
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	if _, err := driver.DeviceInfo(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "not open") {
		t.Fatalf("DeviceInfo before Open = %v, want a not-open refusal", err)
	}
	shutdown, err := driver.IsShutdown(context.Background())
	if err != nil || !shutdown {
		t.Fatalf("IsShutdown before Open = %v, %v; a closed driver is shut down", shutdown, err)
	}
}

// fakeAndroidRecorder stands in for the long-lived `adb shell screenrecord`
// child so the start/stop wiring is testable without a device.
type fakeAndroidRecorder struct {
	stopped             bool
	sinkPath            string
	stopErr             error
	stopCtxWasCancelled bool
	stopCtxHadDeadline  bool
}

func (r *fakeAndroidRecorder) stop(ctx context.Context, sinkPath string) error {
	r.stopped = true
	r.sinkPath = sinkPath
	r.stopCtxWasCancelled = ctx.Err() != nil
	_, r.stopCtxHadDeadline = ctx.Deadline()
	return r.stopErr
}

func TestScreenRecordingStartsAndStopsCleanly(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md line 9: startScreenRecording(sink). screenrecord
	// records to a device path derived from the sink; stop() interrupts it and
	// pulls the finished file to the sink, so the artifact is at the sink once
	// StopScreenRecording returns — and the recorder must not be stopped early.
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	var gotDevicePath string
	rec := &fakeAndroidRecorder{}
	driver.spawnRecorder = func(_ context.Context, devicePath string) (screenRecorder, error) {
		gotDevicePath = devicePath
		return rec, nil
	}

	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: "/host/out.mp4"})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	if gotDevicePath != "/sdcard/out.mp4" {
		t.Fatalf("device path = %q, want /sdcard/out.mp4", gotDevicePath)
	}
	if rec.stopped {
		t.Fatal("the recorder was stopped before StopScreenRecording was called")
	}

	artifacts, err := driver.StopScreenRecording(context.Background(), id)
	if err != nil {
		t.Fatalf("StopScreenRecording() error = %v", err)
	}
	if !rec.stopped || rec.sinkPath != "/host/out.mp4" {
		t.Fatalf("stop(stopped=%v, sink=%q), want it interrupted with the sink path", rec.stopped, rec.sinkPath)
	}
	if len(artifacts) != 1 || artifacts[0].Path != "/host/out.mp4" {
		t.Fatalf("artifacts = %#v, want one at the sink path", artifacts)
	}
}

func TestStopScreenRecordingRejectsAnUnknownCapture(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	if _, err := driver.StopScreenRecording(
		context.Background(), device.CaptureID("never-started")); err == nil {
		t.Fatal("StopScreenRecording accepted a capture id it never handed out")
	}
}

func TestScreenRecordingRejectsADuplicateCaptureIDBeforeSpawning(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	spawned := 0
	driver.spawnRecorder = func(context.Context, string) (screenRecorder, error) {
		spawned++
		return &fakeAndroidRecorder{}, nil
	}
	request := device.ScreenRecordingRequest{OutputPath: "/host/out.mp4"}
	if _, err := driver.StartScreenRecording(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.StartScreenRecording(context.Background(), request); err == nil {
		t.Fatal("StartScreenRecording accepted a duplicate capture id")
	}
	if spawned != 1 {
		t.Fatalf("recorders spawned = %d, want one", spawned)
	}
}

func TestScreenRecordingRejectsAnUnsafeSink(t *testing.T) {
	t.Parallel()

	// The sink is flow-controlled input that reaches `adb shell screenrecord`
	// (device shell) and `adb pull`. A basename with shell metacharacters or a
	// leading '-' must be refused before any child spawns — command/argv injection.
	for _, sink := range []string{"/host/x; rm -rf sdcard.mp4", "/host/$(reboot).mp4", "-rf.mp4", "/host/-x.mp4", ""} {
		driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
		spawned := false
		driver.spawnRecorder = func(context.Context, string) (screenRecorder, error) {
			spawned = true
			return &fakeAndroidRecorder{}, nil
		}
		if _, err := driver.StartScreenRecording(
			context.Background(), device.ScreenRecordingRequest{OutputPath: sink}); err == nil {
			t.Errorf("StartScreenRecording accepted an unsafe sink %q", sink)
		}
		if spawned {
			t.Errorf("spawned a recorder for an unsafe sink %q", sink)
		}
	}
}

func TestStopScreenRecordingSurfacesAStopFailure(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	rec := &fakeAndroidRecorder{stopErr: errors.New("pull failed")}
	driver.spawnRecorder = func(context.Context, string) (screenRecorder, error) {
		return rec, nil
	}
	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: "/host/out.mp4"})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	if _, err := driver.StopScreenRecording(context.Background(), id); err == nil {
		t.Fatal("StopScreenRecording swallowed the recorder's stop failure")
	}
}

func TestCloseDrainsEveryRecordingWithBoundedNonCancelledCleanup(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	first := &fakeAndroidRecorder{stopErr: errors.New("first stop failed")}
	second := &fakeAndroidRecorder{stopErr: errors.New("second stop failed")}
	recorders := []screenRecorder{first, second}
	driver.spawnRecorder = func(context.Context, string) (screenRecorder, error) {
		recorder := recorders[0]
		recorders = recorders[1:]
		return recorder, nil
	}
	for _, sink := range []string{"/host/first.mp4", "/host/second.mp4"} {
		if _, err := driver.StartScreenRecording(
			context.Background(), device.ScreenRecordingRequest{OutputPath: sink}); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := driver.Close(cancelled)
	if err == nil ||
		!strings.Contains(err.Error(), "first stop failed") ||
		!strings.Contains(err.Error(), "second stop failed") {
		t.Fatalf("Close() error = %v, want both recorder failures", err)
	}
	for index, recorder := range []*fakeAndroidRecorder{first, second} {
		if !recorder.stopped {
			t.Fatalf("recorder %d was not drained", index)
		}
		if recorder.stopCtxWasCancelled {
			t.Fatalf("recorder %d received an already-cancelled cleanup context", index)
		}
		if !recorder.stopCtxHadDeadline {
			t.Fatalf("recorder %d cleanup context has no deadline", index)
		}
	}
	driver.recMu.Lock()
	defer driver.recMu.Unlock()
	if len(driver.recordings) != 0 {
		t.Fatalf("recordings after Close = %d, want none", len(driver.recordings))
	}
}

// `- launchApp: permissions: {all: deny}` on com.android.settings must not fail
// the whole flow when pm
// refuses to revoke ACCESS_BACKGROUND_LOCATION on a system app and exits 255.
// Permission application is best effort because system-fixed permissions are
// outside an ordinary app's control.
func TestSetPermissionsSurvivesAPermissionTheSystemWillNotChange(t *testing.T) {
	t.Parallel()

	dump := `  Package [com.example.a]:
    User 0: installed=true
      runtime permissions:
        android.permission.ACCESS_BACKGROUND_LOCATION: granted=true, flags=[]
        android.permission.CAMERA: granted=false, flags=[]
`
	driver, runner, _ := newOpenDriver(t, nil)
	runner.respond = func(args []string) ([]byte, error) {
		switch {
		case len(args) >= 4 && args[3] == "dumpsys":
			return []byte(dump), nil
		case slices.Contains(args, "android.permission.ACCESS_BACKGROUND_LOCATION"):
			return nil, errors.New("exit status 255: Non-System UID cannot revoke system fixed permission")
		}
		return nil, nil
	}
	base := len(runner.calls)
	if err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID: "com.example.a",
		Permissions: map[string]string{
			"all": "deny",
			"android.permission.ACCESS_BACKGROUND_LOCATION": "deny",
		},
	}); err != nil {
		t.Fatalf("SetPermissions() error = %v, want the flow to survive it", err)
	}
	// The permission it COULD change still had to be changed: tolerating one
	// refusal must not become skipping the rest.
	var revokedCamera bool
	for _, call := range runner.calls[base:] {
		if slices.Contains(call, "revoke") && slices.Contains(call, "android.permission.CAMERA") {
			revokedCamera = true
		}
	}
	if !revokedCamera {
		t.Fatalf("calls = %v, want the revokable permission still revoked", runner.calls[base:])
	}
}

// setLocation requires the mock-location app-op; without it Android raises
// `java.lang.SecurityException: ... not allowed to perform MOCK_LOCATION`.
// The agent is an ordinary app, and an ordinary app may only feed a mock
// location once the mock_location app-op is allowed for it — so the host grants
// it to the agent it just installed.
func TestOpenLetsTheAgentMockLocation(t *testing.T) {
	t.Parallel()

	_, port := startAgentStub(t, answerDeviceInfo(1080, 2400))
	runner := &recordingRunner{}
	driver := newManagedDriver(port, runner)
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })

	want := []string{"shell", "appops", "set", AgentAppPackage, "android:mock_location", "allow"}
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[3:], want) {
			return
		}
	}
	t.Fatalf("adb calls = %v, want %v", runner.recorded(), want)
}

func TestRecorderStopFinalizesOnDeviceBeforePulling(t *testing.T) {
	t.Parallel()

	// Interrupting the local `adb shell` child does not reliably interrupt
	// screenrecord on the device, and screenrecord
	// writes the mp4 index last, so pulling straight after the local exit pulls
	// an unfinished file. Two probes on the device, same clip length:
	//
	//	local SIGINT only     ->  3232 bytes
	//	shell pkill -INT      -> 72100 bytes
	//
	// So the interrupt has to reach the device process, and the pull has to come
	// after it, not after the local child.
	var calls [][]string
	recorder := &adbRecorder{
		serial:     testSerial,
		devicePath: "/sdcard/out.mp4",
		run: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, args)
			return nil, nil
		},
	}
	if err := recorder.stop(context.Background(), "/host/out.mp4"); err != nil {
		t.Fatalf("stop() error = %v", err)
	}

	indexOf := func(verb string) int {
		for index, call := range calls {
			for _, argument := range call {
				if argument == verb {
					return index
				}
			}
		}
		return -1
	}
	interrupt, pull := indexOf("pkill"), indexOf("pull")
	if interrupt < 0 {
		t.Fatalf("stop() never interrupted screenrecord on the device: %v", calls)
	}
	if pull < 0 {
		t.Fatalf("stop() never pulled the recording: %v", calls)
	}
	if interrupt > pull {
		t.Fatalf("stop() pulled before finalizing on the device: %v", calls)
	}
	if remove := indexOf("rm"); remove >= 0 && remove < pull {
		t.Fatalf("stop() removed the device copy before pulling it: %v", calls)
	}
}

func TestRecorderStopCleansDeviceAndPartialHostFileWhenPullFails(t *testing.T) {
	t.Parallel()

	sink := filepath.Join(t.TempDir(), "partial.mp4")
	var calls [][]string
	recorder := &adbRecorder{
		serial:     testSerial,
		devicePath: "/sdcard/out.mp4",
		run: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, args)
			if slices.Contains(args, "pull") {
				if err := os.WriteFile(sink, []byte("partial"), 0o644); err != nil {
					t.Fatal(err)
				}
				return []byte("pull failed"), errors.New("exit status 1")
			}
			return nil, nil
		},
	}
	if err := recorder.stop(context.Background(), sink); err == nil {
		t.Fatal("stop() swallowed the pull failure")
	}
	if _, err := os.Stat(sink); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial host artifact still exists: %v", err)
	}
	removed := false
	for _, call := range calls {
		removed = removed || slices.Contains(call, "rm")
	}
	if !removed {
		t.Fatalf("device artifact was not removed after pull failure: %v", calls)
	}
}

func TestRecorderStopKillsAndBoundedlyReapsAStuckLocalChild(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	killed := make(chan struct{}, 1)
	recorder := &adbRecorder{
		serial:     testSerial,
		devicePath: "/sdcard/out.mp4",
		child: &androidLocalChild{
			done:   done,
			signal: func(os.Signal) error { return nil },
			kill: func() error {
				killed <- struct{}{}
				done <- nil
				return nil
			},
		},
		run: func(ctx context.Context, args ...string) ([]byte, error) {
			if slices.Contains(args, "pkill") {
				return nil, nil
			}
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := recorder.stop(ctx, filepath.Join(t.TempDir(), "out.mp4"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop() error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("stop() exceeded its cleanup deadline by too much: %v", time.Since(started))
	}
	select {
	case <-killed:
	default:
		t.Fatal("stuck local adb child was not killed")
	}
}
