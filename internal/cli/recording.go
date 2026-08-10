package cli

// startRecording / stopRecording.
//
// RecordingController keeps the capture ID between the driver's start and stop
// calls. The file lands in the process working directory alongside authored
// screenshots.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
)

// recordingExtension is what both recorders produce. Unlike a screenshot the
// format is not in doubt, so it is not sniffed from the bytes.
const recordingExtension = ".mp4"

// screenRecordingDriver is the pair of methods a driver needs to record. The
// start half is on frozen Driver v0; the stop half is not, which is the whole
// reason this controller exists.
type screenRecordingDriver interface {
	StartScreenRecording(context.Context, device.ScreenRecordingRequest) (device.CaptureID, error)
	StopScreenRecording(context.Context, device.CaptureID) ([]device.Artifact, error)
}

// DriverRecordingController holds the capture id between the two commands.
type DriverRecordingController struct {
	mu     sync.Mutex
	driver screenRecordingDriver
	// directory is where the finished file lands.
	directory    string
	directoryErr error
	// capture is the id of the recording in flight, blank when none is; it is
	// also how a second startRecording is caught.
	capture  device.CaptureID
	closed   bool
	closeErr error
}

func NewDriverRecordingController(
	driver screenRecordingDriver, directory string,
) *DriverRecordingController {
	if directory == "" {
		directory = "."
	}
	directory, err := canonicalRecordingDirectory(directory)
	return &DriverRecordingController{driver: driver, directory: directory, directoryErr: err}
}

func (controller *DriverRecordingController) Start(
	ctx context.Context, request engine.RecordingStartRequest,
) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	if controller.closed {
		return errors.New("recording controller is closed")
	}
	if controller.capture != "" {
		return fmt.Errorf("a recording is already running (%s)", controller.capture)
	}
	if controller.directoryErr != nil {
		return controller.directoryErr
	}
	name, err := recordingFileName(request.Name)
	if err != nil {
		return err
	}
	outputPath := filepath.Join(controller.directory, name)
	relative, err := filepath.Rel(controller.directory, outputPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("recording path %q escapes run directory %q", outputPath, controller.directory)
	}
	capture, err := controller.driver.StartScreenRecording(
		ctx, device.ScreenRecordingRequest{OutputPath: outputPath})
	if err != nil {
		return err
	}
	controller.capture = capture
	return nil
}

func (controller *DriverRecordingController) Stop(ctx context.Context) ([]device.Artifact, error) {
	return controller.stop(ctx, true)
}

// StopAll finalizes the in-flight recording, if any. Unlike authored Stop it
// is idempotent, so session and driver cleanup can both call it safely.
func (controller *DriverRecordingController) StopAll(
	ctx context.Context,
) ([]device.Artifact, error) {
	return controller.stop(ctx, false)
}

// Close makes DriverRecordingController usable as a cleanup resource while
// retaining StopAll's artifact-returning form for callers that need evidence.
func (controller *DriverRecordingController) Close(ctx context.Context) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	if controller.closed {
		return controller.closeErr
	}
	controller.closed = true
	_, controller.closeErr = controller.stopLocked(ctx, false)
	return controller.closeErr
}

func (controller *DriverRecordingController) stop(
	ctx context.Context, requireActive bool,
) ([]device.Artifact, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.stopLocked(ctx, requireActive)
}

func (controller *DriverRecordingController) stopLocked(
	ctx context.Context, requireActive bool,
) ([]device.Artifact, error) {
	if controller.capture == "" {
		if requireActive {
			return nil, errors.New("no recording is running")
		}
		return nil, nil
	}
	capture := controller.capture
	// Cleared before the call, not after: a stop that fails has still ended
	// this recording as far as the run is concerned, and leaving the id behind
	// would make every later startRecording report one already running.
	controller.capture = ""
	return controller.driver.StopScreenRecording(ctx, capture)
}

// recordingFileName gives the authored name its extension. The rest of the
// validation is device.ValidateRecordingSink's, which both drivers already
// apply to the path this builds.
func recordingFileName(authored string) (string, error) {
	trimmed := strings.TrimSpace(authored)
	if trimmed == "" {
		return "", errors.New("a recording needs a name")
	}
	if trimmed == "." || trimmed == ".." || filepath.IsAbs(trimmed) ||
		strings.ContainsAny(trimmed, `/\\`) || filepath.Base(trimmed) != trimmed {
		return "", fmt.Errorf("recording name %q must be a basename without path traversal", authored)
	}
	if !strings.EqualFold(filepath.Ext(trimmed), recordingExtension) {
		trimmed += recordingExtension
	}
	return trimmed, nil
}

func canonicalRecordingDirectory(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolving recording run directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving recording run directory %q: %w", directory, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspecting recording run directory %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("recording run root %q is not a directory", resolved)
	}
	return resolved, nil
}

// UnsupportedRecordingController refuses both halves of the recording
// lifecycle.
//
// It is what a driver without the stop half gets. Reporting success and
// producing no file would tell a run it has a recording it does not have.
type UnsupportedRecordingController struct{}

func (UnsupportedRecordingController) Start(context.Context, engine.RecordingStartRequest) error {
	return fmt.Errorf("%w: this driver cannot record the screen", device.ErrUnsupported)
}

func (UnsupportedRecordingController) Stop(context.Context) ([]device.Artifact, error) {
	return nil, fmt.Errorf("%w: this driver cannot record the screen", device.ErrUnsupported)
}

// recordingController asks the driver in play whether it can record, rather
// than switching on the platform name: the stop half is a concrete method, so
// the type is the honest test of whether a recording can be finished.
//
// The recording lands in ".", the process working directory, for the same
// reason an authored screenshot does — that is where the contract puts it and
// where the operator will look.
func recordingController(driver device.Driver) engine.RecordingController {
	recorder, ok := driver.(screenRecordingDriver)
	if !ok {
		return UnsupportedRecordingController{}
	}
	return NewDriverRecordingController(recorder, ".")
}
