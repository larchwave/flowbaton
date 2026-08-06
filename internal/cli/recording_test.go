package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
)

// Start and stop share one host service so a failure cannot leave a recorder
// process behind. Completed recordings are written to the working directory.

// recordingSpy is a driver that can record and remembers how it was asked to.
type recordingSpy struct {
	started []string
	stopped []device.CaptureID
	startErr,
	stopErr error
}

func (spy *recordingSpy) StartScreenRecording(
	_ context.Context, request device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	if spy.startErr != nil {
		return "", spy.startErr
	}
	spy.started = append(spy.started, request.OutputPath)
	return device.CaptureID(request.OutputPath), nil
}

func (spy *recordingSpy) StopScreenRecording(
	_ context.Context, id device.CaptureID,
) ([]device.Artifact, error) {
	spy.stopped = append(spy.stopped, id)
	if spy.stopErr != nil {
		return nil, spy.stopErr
	}
	return []device.Artifact{{Kind: "recording", Path: string(id)}}, nil
}

func TestARecordingLandsInTheWorkingDirectoryUnderTheAuthoredName(t *testing.T) {
	t.Parallel()

	spy := &recordingSpy{}
	controller := NewDriverRecordingController(spy, ".")
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "clip"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	artifacts, err := controller.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	want := filepath.Join(".", "clip.mp4")
	if len(spy.started) != 1 || spy.started[0] != want {
		t.Fatalf("started = %v, want [%s]", spy.started, want)
	}
	if len(artifacts) != 1 || artifacts[0].Path != want {
		t.Fatalf("artifacts = %v, want the recording at %s", artifacts, want)
	}
}

func TestARecordingNameKeepsAnExtensionItAlreadyHas(t *testing.T) {
	t.Parallel()

	spy := &recordingSpy{}
	controller := NewDriverRecordingController(spy, ".")
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "clip.mp4"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// clip.mp4.mp4 would be worse than the bug it fixes.
	if want := filepath.Join(".", "clip.mp4"); spy.started[0] != want {
		t.Fatalf("started = %q, want %q", spy.started[0], want)
	}
}

func TestASecondStartRecordingIsRefusedRatherThanLosingTheFirst(t *testing.T) {
	t.Parallel()

	spy := &recordingSpy{}
	controller := NewDriverRecordingController(spy, ".")
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "a"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	// Overwriting the capture id would leave the first recording running on the
	// device with nothing left that could ever stop it.
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "b"}); err == nil {
		t.Fatal("a second startRecording was accepted")
	}
	if len(spy.started) != 1 {
		t.Fatalf("started = %v, want only the first", spy.started)
	}
}

func TestStopRecordingWithoutStartIsRefused(t *testing.T) {
	t.Parallel()

	spy := &recordingSpy{}
	controller := NewDriverRecordingController(spy, ".")
	if _, err := controller.Stop(context.Background()); err == nil {
		t.Fatal("a stopRecording with nothing running was accepted")
	}
	if len(spy.stopped) != 0 {
		t.Fatalf("stopped = %v, want the driver left alone", spy.stopped)
	}
}

// A failed stop must not leave the id behind: every later startRecording would
// then report one already running, and the run could never record again.
func TestAFailedStopStillEndsTheRecordingForTheRun(t *testing.T) {
	t.Parallel()

	spy := &recordingSpy{stopErr: errors.New("adb pull: no such file")}
	controller := NewDriverRecordingController(spy, ".")
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "a"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := controller.Stop(context.Background()); err == nil {
		t.Fatal("the stop failure was swallowed")
	}
	spy.stopErr = nil
	if err := controller.Start(
		context.Background(), engine.RecordingStartRequest{Name: "b"}); err != nil {
		t.Fatalf("the next Start() was blocked by the failed stop: %v", err)
	}
}

func TestADriverThatCanRecordGetsARealController(t *testing.T) {
	t.Parallel()

	// The Android driver carries both halves and therefore needs the concrete
	// recording controller.
	androidDriver := android.NewDriver("emulator-5554", 7001, nil, nil)
	if _, refusing := recordingController(androidDriver).(UnsupportedRecordingController); refusing {
		t.Fatal("the android driver was given the controller that refuses to record")
	}
}

func TestADriverThatCannotRecordStillSaysSo(t *testing.T) {
	t.Parallel()

	// permissiveDriver is the FakeDriver, which has no stop half. Reporting
	// success and producing no file would tell a run it has a recording it does
	// not have.
	controller := recordingController(permissiveDriver())
	err := controller.Start(context.Background(), engine.RecordingStartRequest{Name: "clip"})
	if err == nil {
		t.Fatal("a driver that cannot finish a recording accepted one")
	}
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("error = %v, want it to be device.ErrUnsupported", err)
	}
	if !strings.Contains(err.Error(), "record") {
		t.Fatalf("error = %q, want it to name what is missing", err)
	}
}
