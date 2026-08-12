package android

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

type fakeDeviceLogProcess struct {
	output       *os.File
	content      []byte
	stopErr      error
	stopped      bool
	cancelledCtx bool
	boundedCtx   bool
}

func (process *fakeDeviceLogProcess) stop(ctx context.Context) error {
	process.stopped = true
	process.cancelledCtx = ctx.Err() != nil
	_, process.boundedCtx = ctx.Deadline()
	if len(process.content) > 0 {
		_, _ = process.output.Write(process.content)
	}
	_ = process.output.Close()
	return process.stopErr
}

func injectLogProcesses(
	driver *Driver,
	processes ...*fakeDeviceLogProcess,
) *[][]string {
	calls := [][]string{}
	index := 0
	driver.spawnDeviceLog = func(
		_ context.Context,
		output *os.File,
		args []string,
	) (deviceLogProcess, error) {
		calls = append(calls, append([]string(nil), args...))
		if index >= len(processes) {
			return nil, errors.New("unexpected log spawn")
		}
		process := processes[index]
		index++
		process.output = output
		return process, nil
	}
	return &calls
}

func TestDeviceLogCaptureUsesSafeUniqueIDsAndExactArgv(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	process := &fakeDeviceLogProcess{content: []byte("08-10 07:00:00.000 message\n")}
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	calls := injectLogProcesses(driver, process)

	id, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: directory,
	})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture() error = %v", err)
	}
	if id != "android-log-000001" {
		t.Fatalf("capture id = %q, want android-log-000001", id)
	}
	wantArgs := []string{"-s", testSerial, "logcat", "-v", "threadtime", "-T", "1"}
	if !reflect.DeepEqual((*calls)[0], wantArgs) {
		t.Fatalf("logcat argv = %v, want %v", (*calls)[0], wantArgs)
	}

	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "device-log" {
		t.Fatalf("artifacts = %#v, want one device-log", artifacts)
	}
	if artifacts[0].Metadata["serial"] != testSerial {
		t.Fatalf("artifact metadata = %#v, want serial %q", artifacts[0].Metadata, testSerial)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(artifacts[0].Path) != resolvedDirectory {
		t.Fatalf("artifact path = %q, want it inside %q", artifacts[0].Path, resolvedDirectory)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
		t.Fatal("a duplicate stop accepted an already-finalized capture id")
	}
}

func TestDeviceLogCaptureFiltersByReadOnlyPIDLookup(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{respond: func(args []string) ([]byte, error) {
		if slices.Contains(args, "pidof") {
			return []byte("123 456\n"), nil
		}
		return nil, nil
	}}
	driver := NewDriver(testSerial, 7001, runner, nil)
	process := &fakeDeviceLogProcess{content: []byte("app log\n")}
	calls := injectLogProcesses(driver, process)
	id, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: t.TempDir(),
		AppID:           "com.example.app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains((*calls)[0], "--pid=123") {
		t.Fatalf("logcat argv = %v, want --pid=123", (*calls)[0])
	}
	wantPID := []string{"-s", testSerial, "shell", "pidof", "com.example.app"}
	if got := runner.calls[0][1:]; !reflect.DeepEqual(got, wantPID) {
		t.Fatalf("pid lookup argv = %v, want %v", got, wantPID)
	}
	_, _ = driver.StopDeviceLogCapture(context.Background(), id)
}

func TestDeviceLogStartFailureRemovesItsPartialFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	driver.spawnDeviceLog = func(
		_ context.Context,
		output *os.File,
		_ []string,
	) (deviceLogProcess, error) {
		_, _ = output.WriteString("partial")
		_ = output.Close()
		return nil, errors.New("spawn failed")
	}
	if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: directory,
	}); err == nil {
		t.Fatal("StartDeviceLogCapture swallowed the spawn failure")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial files after failed start = %v", entries)
	}
}

func TestDeviceLogRefusesCancelledStartAndUnsafeAppIDBeforeSpawn(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	spawned := false
	driver.spawnDeviceLog = func(context.Context, *os.File, []string) (deviceLogProcess, error) {
		spawned = true
		return &fakeDeviceLogProcess{}, nil
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := driver.StartDeviceLogCapture(cancelled, device.DeviceLogRequest{
		OutputDirectory: t.TempDir(),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled start error = %v, want context.Canceled", err)
	}
	if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: t.TempDir(),
		AppID:           "--all",
	}); err == nil {
		t.Fatal("StartDeviceLogCapture accepted an unsafe application id")
	}
	if spawned {
		t.Fatal("a refused log capture spawned a process")
	}
}

func TestDeviceLogStopFailureAndEmptyArtifactAreRemoved(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		content []byte
		stopErr error
	}{
		{name: "stop failure", content: []byte("partial"), stopErr: errors.New("wait failed")},
		{name: "empty output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
			process := &fakeDeviceLogProcess{content: test.content, stopErr: test.stopErr}
			injectLogProcesses(driver, process)
			id, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
				OutputDirectory: directory,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
				t.Fatal("StopDeviceLogCapture accepted an invalid artifact")
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("partial files after failed stop = %v", entries)
			}
		})
	}
}

func TestCloseDrainsAllDeviceLogsWithBoundedNonCancelledContexts(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	first := &fakeDeviceLogProcess{content: []byte("first\n"), stopErr: errors.New("first failed")}
	second := &fakeDeviceLogProcess{content: []byte("second\n"), stopErr: errors.New("second failed")}
	injectLogProcesses(driver, first, second)
	for range 2 {
		if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
			OutputDirectory: t.TempDir(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err := driver.Close(cancelled)
	if err == nil || !strings.Contains(err.Error(), "first failed") ||
		!strings.Contains(err.Error(), "second failed") {
		t.Fatalf("Close() error = %v, want both capture failures", err)
	}
	for index, process := range []*fakeDeviceLogProcess{first, second} {
		if !process.stopped || process.cancelledCtx || !process.boundedCtx {
			t.Fatalf("process %d cleanup = stopped:%v cancelled:%v bounded:%v",
				index, process.stopped, process.cancelledCtx, process.boundedCtx)
		}
	}
	driver.diagnosticMu.Lock()
	defer driver.diagnosticMu.Unlock()
	if len(driver.deviceLogs) != 0 {
		t.Fatalf("device logs after Close = %d, want none", len(driver.deviceLogs))
	}
}

func TestDeviceLogCaptureEnforcesTheActiveLimit(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	processes := make([]*fakeDeviceLogProcess, maxActiveDeviceLogs)
	for index := range processes {
		processes[index] = &fakeDeviceLogProcess{content: []byte("log\n")}
	}
	injectLogProcesses(driver, processes...)
	directory := t.TempDir()
	for range maxActiveDeviceLogs {
		if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
			OutputDirectory: directory,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: directory,
	}); err == nil {
		t.Fatal("StartDeviceLogCapture exceeded the active-capture limit")
	}
	_ = driver.Close(context.Background())
}

func TestCopyBoundedDeviceLogStopsAtTheByteLimit(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := copyBoundedDeviceLog(strings.NewReader("0123456789"), &output, 8)
	if !errors.Is(err, errDeviceLogLimit) {
		t.Fatalf("copy error = %v, want errDeviceLogLimit", err)
	}
	if output.String() != "01234567" {
		t.Fatalf("bounded output = %q, want exactly eight bytes", output.String())
	}
}

func TestADBDeviceLogStopWaitsForTheWriterAfterItsContextExpires(t *testing.T) {
	t.Parallel()

	output, err := os.CreateTemp(t.TempDir(), "device-log-*.log")
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	done := make(chan error, 1)
	copyDone := make(chan error, 1)
	process := &adbDeviceLogProcess{
		cancel: func() {}, done: done, copyDone: copyDone, output: output, reader: reader,
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		done <- nil
	}()
	go func() {
		time.Sleep(30 * time.Millisecond)
		_, writeErr := output.WriteString("late but complete\n")
		copyDone <- writeErr
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := process.stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop error = %v, want deadline plus completed writer cleanup", err)
	}
	contents, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "late but complete\n" {
		t.Fatalf("artifact = %q, writer was not drained before close", contents)
	}
}

func TestCrashArtifactsRunBoundedBugreportAndValidateOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	runner := &recordingRunner{respond: func(args []string) ([]byte, error) {
		if slices.Contains(args, "bugreport") {
			return nil, os.WriteFile(args[len(args)-1], []byte("bugreport"), 0o600)
		}
		return nil, nil
	}}
	driver := NewDriver(testSerial, 7001, runner, nil)
	artifacts, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: directory,
	})
	if err != nil {
		t.Fatalf("CollectCrashArtifacts() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "crash-artifact" ||
		artifacts[0].Metadata["serial"] != testSerial {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	want := []string{"-s", testSerial, "bugreport", artifacts[0].Path}
	if got := runner.calls[0][1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("bugreport argv = %v, want %v", got, want)
	}
}

func TestCrashArtifactsRejectAppFilteringBeforeADB(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	driver := NewDriver(testSerial, 7001, runner, nil)
	_, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: t.TempDir(),
		AppID:           "com.example.app",
	})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("app-filtered crash collection error = %v, want ErrUnsupported", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("app-filtered crash collection reached adb: %v", runner.calls)
	}
}

func TestCrashArtifactFailureRemovesPartialOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	runner := &recordingRunner{respond: func(args []string) ([]byte, error) {
		if slices.Contains(args, "bugreport") {
			_ = os.WriteFile(args[len(args)-1], []byte("partial"), 0o600)
			return nil, errors.New("bugreport failed")
		}
		return nil, nil
	}}
	driver := NewDriver(testSerial, 7001, runner, nil)
	if _, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: directory,
	}); err == nil {
		t.Fatal("CollectCrashArtifacts swallowed the bugreport failure")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial crash artifacts remain: %v", entries)
	}
}

func TestCrashArtifactRejectsAndRemovesEmptyOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	if _, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: directory,
	}); err == nil {
		t.Fatal("CollectCrashArtifacts accepted an empty artifact")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty crash artifact remains: %v", entries)
	}
}

type cancellingBugreportRunner struct {
	started chan struct{}
	once    sync.Once
}

func (runner *cancellingBugreportRunner) Run(
	ctx context.Context,
	_ string,
	args ...string,
) ([]byte, error) {
	if slices.Contains(args, "bugreport") {
		_ = os.WriteFile(args[len(args)-1], []byte("partial"), 0o600)
		runner.once.Do(func() { close(runner.started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, nil
}

func TestCrashArtifactCancellationRemovesPartialOutput(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	runner := &cancellingBugreportRunner{started: make(chan struct{})}
	driver := NewDriver(testSerial, 7001, runner, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := driver.CollectCrashArtifacts(ctx, device.ArtifactRequest{
			OutputDirectory: directory,
		})
		done <- err
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("bugreport never started")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("CollectCrashArtifacts() error = %v, want context.Canceled", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial crash artifacts remain after cancellation: %v", entries)
	}
}

func TestDiagnosticDirectoryMustBeARealDirectory(t *testing.T) {
	t.Parallel()

	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "diagnostics")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	if _, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{
		OutputDirectory: link,
	}); err == nil {
		t.Fatal("StartDeviceLogCapture accepted a symlink output directory")
	}
	if _, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: link,
	}); err == nil {
		t.Fatal("CollectCrashArtifacts accepted a symlink output directory")
	}
}

func TestDiagnosticDirectoryCreationIsPrivate(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by POSIX mode bits")
	}

	directory := filepath.Join(t.TempDir(), "private")
	resolved, err := prepareDiagnosticDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("diagnostic directory mode = %o, want no group/other access", info.Mode().Perm())
	}
}
