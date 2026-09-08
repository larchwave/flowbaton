package cli

// startLogCapture / stopLogCapture.
//
// DriverLogCaptureController keeps the capture ID between the driver's start
// and stop calls. The finished file lands in the run output directory under
// the authored name, and its metadata says which log source it is and whether
// it covers the flow's application or the whole device.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
)

// deviceLogArtifactKind matches the engine's label for a finished capture.
const deviceLogArtifactKind = "device-log"

// deviceLogDriver is the pair of frozen Driver v0 methods a capture needs.
type deviceLogDriver interface {
	StartDeviceLogCapture(context.Context, device.DeviceLogRequest) (device.CaptureID, error)
	StopDeviceLogCapture(context.Context, device.CaptureID) ([]device.Artifact, error)
}

// DriverLogCaptureController holds one capture between the two commands.
type DriverLogCaptureController struct {
	mu     sync.Mutex
	driver deviceLogDriver
	// directory is where the driver writes and where the finished file stays.
	directory string
	// capture is the id in flight, blank when none is; it is also how a
	// second startLogCapture is caught.
	capture device.CaptureID
	name    string
	appID   string
	// scope is "app" when the driver accepted the application filter and
	// "device" when it refused and the capture fell back to the whole device.
	scope    string
	closed   bool
	closeErr error
}

func NewDriverLogCaptureController(driver deviceLogDriver, directory string) *DriverLogCaptureController {
	if directory == "" {
		directory = "."
	}
	return &DriverLogCaptureController{driver: driver, directory: directory}
}

func (controller *DriverLogCaptureController) Start(
	ctx context.Context, request engine.LogCaptureStartRequest,
) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	if controller.closed {
		return errors.New("log capture controller is closed")
	}
	if controller.capture != "" {
		return fmt.Errorf("a log capture is already running (%s)", controller.name)
	}
	name, err := logCaptureFileName(request.Name)
	if err != nil {
		return err
	}
	appID := strings.TrimSpace(request.AppID)
	scope := "app"
	capture, err := controller.driver.StartDeviceLogCapture(
		ctx, device.DeviceLogRequest{OutputDirectory: controller.directory, AppID: appID})
	if errors.Is(err, device.ErrUnsupported) && appID != "" {
		// The driver cannot filter by application: capture the device and say so,
		// rather than pretending the wider file is the application's log.
		scope = "device"
		capture, err = controller.driver.StartDeviceLogCapture(
			ctx, device.DeviceLogRequest{OutputDirectory: controller.directory})
	}
	if err != nil {
		return err
	}
	controller.capture, controller.name, controller.appID, controller.scope = capture, name, appID, scope
	return nil
}

func (controller *DriverLogCaptureController) Stop(ctx context.Context) ([]device.Artifact, error) {
	return controller.stop(ctx, true)
}

// StopAll finalizes the in-flight capture, if any. Unlike authored Stop it is
// idempotent, so session cleanup can call it whether or not the flow stopped
// its own capture.
func (controller *DriverLogCaptureController) StopAll(ctx context.Context) ([]device.Artifact, error) {
	return controller.stop(ctx, false)
}

// Close makes the controller usable as a cleanup resource.
func (controller *DriverLogCaptureController) Close(ctx context.Context) error {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	if controller.closed {
		return controller.closeErr
	}
	controller.closed = true
	_, controller.closeErr = controller.stopLocked(ctx, false)
	return controller.closeErr
}

func (controller *DriverLogCaptureController) stop(ctx context.Context, requireActive bool) ([]device.Artifact, error) {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.stopLocked(ctx, requireActive)
}

func (controller *DriverLogCaptureController) stopLocked(ctx context.Context, requireActive bool) ([]device.Artifact, error) {
	if controller.capture == "" {
		if requireActive {
			return nil, errors.New("no log capture is running")
		}
		return nil, nil
	}
	capture, name, appID, scope := controller.capture, controller.name, controller.appID, controller.scope
	// Cleared before the call, not after: a stop that fails has still ended
	// this capture as far as the run is concerned.
	controller.capture, controller.name, controller.appID, controller.scope = "", "", "", ""
	artifacts, err := controller.driver.StopDeviceLogCapture(ctx, capture)
	if err != nil {
		return nil, err
	}
	finalized := make([]device.Artifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		named, err := finalizeDeviceLogArtifact(artifact, name, appID, scope)
		if err != nil {
			return nil, err
		}
		finalized = append(finalized, named)
	}
	return finalized, nil
}

// finalizeDeviceLogArtifact moves the driver's temp-named file to the authored
// name in the same directory and records what the file is.
func finalizeDeviceLogArtifact(artifact device.Artifact, name, appID, scope string) (device.Artifact, error) {
	if strings.TrimSpace(artifact.Path) == "" {
		return device.Artifact{}, errors.New("log capture produced no file")
	}
	target := filepath.Join(filepath.Dir(artifact.Path), name+filepath.Ext(artifact.Path))
	if target != artifact.Path {
		if err := os.Rename(artifact.Path, target); err != nil {
			return device.Artifact{}, fmt.Errorf("naming log capture %q: %w", name, err)
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return device.Artifact{}, fmt.Errorf("inspecting log capture %q: %w", name, err)
	}
	metadata := make(map[string]string, len(artifact.Metadata)+4)
	for key, value := range artifact.Metadata {
		metadata[key] = value
	}
	metadata["scope"] = scope
	metadata["bytes"] = strconv.FormatInt(info.Size(), 10)
	if appID != "" {
		metadata["appId"] = appID
	}
	return device.Artifact{Kind: deviceLogArtifactKind, Path: target, Metadata: metadata}, nil
}

// logCaptureFileName keeps the authored name a basename; the extension comes
// from the driver's file, since each log source has its own format.
func logCaptureFileName(authored string) (string, error) {
	trimmed := strings.TrimSpace(authored)
	if trimmed == "" {
		return "", errors.New("a log capture needs a name")
	}
	if trimmed == "." || trimmed == ".." || filepath.IsAbs(trimmed) ||
		strings.ContainsAny(trimmed, `/\`) || filepath.Base(trimmed) != trimmed {
		return "", fmt.Errorf("log capture name %q must be a basename without path traversal", authored)
	}
	return trimmed, nil
}

// logCaptureController wraps the driver in play. Both halves of the capture
// live on frozen Driver v0, so a driver without the capability (web) refuses
// at Start with the shared unsupported sentinel, and preflight refuses the
// command before that. The file lands in the run output directory: a log is
// diagnostic evidence, not an authored deliverable like a screenshot.
func logCaptureController(driver device.Driver, directory string) engine.LogCaptureController {
	return NewDriverLogCaptureController(driver, directory)
}
