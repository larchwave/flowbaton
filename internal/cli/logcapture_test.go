package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/ios"
)

// logCaptureSpy is a driver that captures logs the way the real ones do: it
// writes a temp-named file into the requested directory and hands it back on
// stop. rejectApp makes it behave like a driver that cannot filter.
type logCaptureSpy struct {
	requests  []device.DeviceLogRequest
	stopped   []device.CaptureID
	rejectApp bool
	content   string
	metadata  map[string]string
	startErr  error
	stopErr   error
}

func (spy *logCaptureSpy) StartDeviceLogCapture(
	_ context.Context, request device.DeviceLogRequest,
) (device.CaptureID, error) {
	spy.requests = append(spy.requests, request)
	if spy.startErr != nil {
		return "", spy.startErr
	}
	if spy.rejectApp && request.AppID != "" {
		return "", errors.Join(device.ErrUnsupported, errors.New("device-wide only"))
	}
	file, err := os.CreateTemp(request.OutputDirectory, "spy-log-*.ndjson")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(spy.content); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return device.CaptureID(file.Name()), nil
}

func (spy *logCaptureSpy) StopDeviceLogCapture(
	_ context.Context, id device.CaptureID,
) ([]device.Artifact, error) {
	spy.stopped = append(spy.stopped, id)
	if spy.stopErr != nil {
		return nil, spy.stopErr
	}
	return []device.Artifact{{Kind: "log", Path: string(id), Metadata: spy.metadata}}, nil
}

func TestALogCaptureLandsInTheRunDirectoryUnderTheAuthoredName(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	spy := &logCaptureSpy{content: "{\"process\":\"Fixture\"}\n", metadata: map[string]string{"source": "unified-log"}}
	controller := NewDriverLogCaptureController(spy, directory)
	if err := controller.Start(context.Background(),
		engine.LogCaptureStartRequest{Name: "fixture-console", AppID: "com.example.fixture"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	artifacts, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(spy.requests) != 1 || spy.requests[0].AppID != "com.example.fixture" ||
		spy.requests[0].OutputDirectory != directory {
		t.Fatalf("driver requests = %#v, want one app-scoped request into the run directory", spy.requests)
	}
	want := filepath.Join(directory, "fixture-console.ndjson")
	if len(artifacts) != 1 || artifacts[0].Path != want || artifacts[0].Kind != deviceLogArtifactKind {
		t.Fatalf("artifacts = %#v, want one %s at %s", artifacts, deviceLogArtifactKind, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("finished capture missing: %v", err)
	}
	for key, value := range map[string]string{
		"source": "unified-log", "scope": "app", "appId": "com.example.fixture", "bytes": "22",
	} {
		if got := artifacts[0].Metadata[key]; got != value {
			t.Fatalf("metadata[%s] = %q, want %q (all: %#v)", key, got, value, artifacts[0].Metadata)
		}
	}
}

func TestADriverThatCannotFilterCapturesTheDeviceAndSaysSo(t *testing.T) {
	t.Parallel()

	spy := &logCaptureSpy{rejectApp: true, content: "everything\n"}
	controller := NewDriverLogCaptureController(spy, t.TempDir())
	if err := controller.Start(context.Background(),
		engine.LogCaptureStartRequest{Name: "all", AppID: "com.example.fixture"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	artifacts, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if len(spy.requests) != 2 || spy.requests[0].AppID == "" || spy.requests[1].AppID != "" {
		t.Fatalf("driver requests = %#v, want the app filter tried first and then dropped", spy.requests)
	}
	if got := artifacts[0].Metadata["scope"]; got != "device" {
		t.Fatalf("scope = %q, want device (metadata %#v)", got, artifacts[0].Metadata)
	}
	if got := artifacts[0].Metadata["appId"]; got != "com.example.fixture" {
		t.Fatalf("appId = %q, the requested application is still recorded", got)
	}
}

func TestOtherStartFailuresAreNotRetriedDeviceWide(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no such process")
	spy := &logCaptureSpy{startErr: sentinel}
	controller := NewDriverLogCaptureController(spy, t.TempDir())
	err := controller.Start(context.Background(),
		engine.LogCaptureStartRequest{Name: "x", AppID: "com.example.fixture"})
	if !errors.Is(err, sentinel) || len(spy.requests) != 1 {
		t.Fatalf("Start() error = %v after %d requests, want the driver cause after one request", err, len(spy.requests))
	}
}

func TestLogCaptureLifecycleGuards(t *testing.T) {
	t.Parallel()

	spy := &logCaptureSpy{content: "x\n"}
	controller := NewDriverLogCaptureController(spy, t.TempDir())
	if _, err := controller.Stop(context.Background()); err == nil {
		t.Fatal("Stop() before Start() succeeded")
	}
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "one"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "two"}); err == nil ||
		!strings.Contains(err.Error(), "already running (one)") {
		t.Fatalf("second Start() error = %v, want the running capture named", err)
	}
	for _, bad := range []string{"", " ", "..", "a/b", `a\b`, "/abs"} {
		fresh := NewDriverLogCaptureController(&logCaptureSpy{}, t.TempDir())
		if err := fresh.Start(context.Background(), engine.LogCaptureStartRequest{Name: bad}); err == nil {
			t.Fatalf("Start(%q) accepted a bad name", bad)
		}
	}
}

func TestCloseKeepsAnUnstoppedCaptureUnderItsName(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	spy := &logCaptureSpy{content: "kept\n"}
	controller := NewDriverLogCaptureController(spy, directory)
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "orphan"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := controller.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, "orphan.ndjson")); err != nil {
		t.Fatalf("orphaned capture not kept: %v", err)
	}
	if artifacts, err := controller.StopAll(context.Background()); err != nil || artifacts != nil {
		t.Fatalf("StopAll() after Close() = %#v, %v; want nothing left", artifacts, err)
	}
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "late"}); err == nil {
		t.Fatal("Start() after Close() succeeded")
	}
}

func TestAStopFailureEndsTheCapture(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("log child failed")
	spy := &logCaptureSpy{content: "x\n", stopErr: sentinel}
	controller := NewDriverLogCaptureController(spy, t.TempDir())
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "x"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := controller.Stop(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Stop() error = %v, want the driver cause", err)
	}
	if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{Name: "y"}); err != nil {
		t.Fatalf("Start() after a failed stop error = %v, want a fresh capture", err)
	}
}

// stdioLogCaptureSpy is a driver with the optional stdio start, the way the
// simulator driver has it; the plain spy stands for every driver without it.
type stdioLogCaptureSpy struct {
	*logCaptureSpy
	stdioRequests []device.DeviceLogRequest
}

func (spy *stdioLogCaptureSpy) StartStdioCapture(
	_ context.Context, request device.DeviceLogRequest,
) (device.CaptureID, error) {
	spy.stdioRequests = append(spy.stdioRequests, request)
	file, err := os.CreateTemp(request.OutputDirectory, "spy-stdio-*.log")
	if err != nil {
		return "", err
	}
	if _, err := file.WriteString(spy.content); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	return device.CaptureID(file.Name()), nil
}

func TestAStdioCaptureGoesToTheDriversStdioStart(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	spy := &stdioLogCaptureSpy{logCaptureSpy: &logCaptureSpy{
		content: "### launch 1 stdout\nhello\n", metadata: map[string]string{"source": "stdio", "scope": "app"}}}
	controller := NewDriverLogCaptureController(spy, directory)
	err := controller.Start(context.Background(), engine.LogCaptureStartRequest{
		Name: "console", AppID: "com.example.fixture", Stream: engine.LogStreamStdio})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(spy.requests) != 0 || len(spy.stdioRequests) != 1 ||
		spy.stdioRequests[0] != (device.DeviceLogRequest{OutputDirectory: directory, AppID: "com.example.fixture"}) {
		t.Fatalf("driver calls: system %#v stdio %#v; want one stdio request only", spy.requests, spy.stdioRequests)
	}
	artifacts, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := filepath.Join(directory, "console.log")
	if len(artifacts) != 1 || artifacts[0].Path != want || artifacts[0].Metadata["source"] != "stdio" ||
		artifacts[0].Metadata["scope"] != "app" || artifacts[0].Metadata["appId"] != "com.example.fixture" {
		t.Fatalf("artifacts = %#v, want %s with stdio/app metadata", artifacts, want)
	}
}

func TestADriverWithoutStdioRefusesAStdioCaptureInsteadOfFallingBack(t *testing.T) {
	t.Parallel()

	spy := &logCaptureSpy{}
	controller := NewDriverLogCaptureController(spy, t.TempDir())
	err := controller.Start(context.Background(), engine.LogCaptureStartRequest{
		Name: "console", AppID: "com.example.fixture", Stream: engine.LogStreamStdio})
	if !errors.Is(err, device.ErrUnsupported) || !strings.Contains(err.Error(), "Simulator only") {
		t.Fatalf("Start(stdio) error = %v, want ErrUnsupported naming the Simulator", err)
	}
	if len(spy.requests) != 0 {
		t.Fatalf("driver was asked for a system capture %#v under a stdio name", spy.requests)
	}
	if _, err := controller.Stop(context.Background()); err == nil {
		t.Fatal("Stop() succeeded after a refused start")
	}
}

func TestSessionCleanupSwallowsAnUnusedStdioCaptureButAFlowDoesNot(t *testing.T) {
	t.Parallel()

	unused := fmt.Errorf("iOS stdio capture: %w", ios.ErrStdioCaptureUnused)
	for _, authored := range []bool{true, false} {
		spy := &stdioLogCaptureSpy{logCaptureSpy: &logCaptureSpy{stopErr: unused}}
		controller := NewDriverLogCaptureController(spy, t.TempDir())
		if err := controller.Start(context.Background(), engine.LogCaptureStartRequest{
			Name: "console", AppID: "com.example.fixture", Stream: engine.LogStreamStdio}); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		var err error
		if authored {
			_, err = controller.Stop(context.Background())
		} else {
			_, err = controller.StopAll(context.Background())
		}
		if authored && !errors.Is(err, ios.ErrStdioCaptureUnused) {
			t.Fatalf("authored Stop() error = %v, want the unused sentinel", err)
		}
		if !authored && err != nil {
			t.Fatalf("StopAll() error = %v, want the unused capture dropped silently", err)
		}
		if len(spy.stopped) != 1 {
			t.Fatalf("driver stops = %#v, want exactly one", spy.stopped)
		}
	}
}
