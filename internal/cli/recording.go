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
	"path/filepath"
	"strings"

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
	driver screenRecordingDriver
	// directory is where the finished file lands.
	directory string
	// capture is the id of the recording in flight, blank when none is; it is
	// also how a second startRecording is caught.
	capture device.CaptureID
}

func NewDriverRecordingController(
	driver screenRecordingDriver, directory string,
) *DriverRecordingController {
	if directory == "" {
		directory = "."
	}
	return &DriverRecordingController{driver: driver, directory: directory}
}

func (controller *DriverRecordingController) Start(
	ctx context.Context, request engine.RecordingStartRequest,
) error {
	if controller.capture != "" {
		return fmt.Errorf("a recording is already running (%s)", controller.capture)
	}
	name, err := recordingFileName(request.Name)
	if err != nil {
		return err
	}
	capture, err := controller.driver.StartScreenRecording(
		ctx, device.ScreenRecordingRequest{OutputPath: filepath.Join(controller.directory, name)})
	if err != nil {
		return err
	}
	controller.capture = capture
	return nil
}

func (controller *DriverRecordingController) Stop(ctx context.Context) ([]device.Artifact, error) {
	if controller.capture == "" {
		return nil, errors.New("no recording is running")
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
	if !strings.EqualFold(filepath.Ext(trimmed), recordingExtension) {
		trimmed += recordingExtension
	}
	return trimmed, nil
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
