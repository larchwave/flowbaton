package iosdevice

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png" // the instruments screenshot service produces PNG
	"os"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/instruments"

	"github.com/larchwave/flowbaton/internal/device"
)

// Screen recording on hardware: Apple exposes no recording service to the
// host, so the driver polls the instruments screenshot service and assembles
// the frames into an MJPEG AVI. The artifact keeps the operator's requested
// path; the container is AVI regardless of extension.

// recordingFrameRate is the nominal capture rate. Screenshots travel the
// whole tunnel round-trip, so real rates float below it; the container's
// timebase keeps playback at capture speed.
const recordingFrameRate = 4

// screenshotter is the slice of *instruments.ScreenshotService this driver
// uses.
type screenshotter interface {
	TakeScreenshot() ([]byte, error)
	Close()
}

// deviceRecording is one running screen recording.
type deviceRecording struct {
	stop       chan struct{}
	done       chan struct{}
	outputPath string
	err        error
}

// StartScreenRecording begins polling frames into the requested sink.
func (driver *Driver) StartScreenRecording(
	ctx context.Context,
	request device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	output := request.OutputPath
	if _, err := device.ValidateRecordingSink(output); err != nil {
		return "", err
	}
	entry, err := driver.tools.entry()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	open := driver.openScreenshots
	if open == nil {
		open = openScreenshotsOnDevice
	}
	service, err := open(entry)
	if err != nil {
		return "", fmt.Errorf("open the screenshot service on %s: %w", driver.session.udid, err)
	}
	file, err := os.Create(output)
	if err != nil {
		service.Close()
		return "", fmt.Errorf("creating iOS screen-recording artifact: %w", err)
	}
	recording := &deviceRecording{
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		outputPath: output,
	}
	id := device.CaptureID(output)
	driver.diagMu.Lock()
	if driver.recordings == nil {
		driver.recordings = map[device.CaptureID]*deviceRecording{}
	}
	if _, exists := driver.recordings[id]; exists {
		driver.diagMu.Unlock()
		service.Close()
		_ = file.Close()
		return "", fmt.Errorf("an iOS screen recording is already running for %q", id)
	}
	driver.recordings[id] = recording
	driver.diagMu.Unlock()

	go func() {
		defer close(recording.done)
		defer service.Close()
		recording.err = recordFrames(recording.stop, service, file)
	}()
	return id, nil
}

// recordFrames polls screenshots into an AVI until stop closes. The writer
// is created from the first frame, which fixes the stream dimensions.
func recordFrames(stop <-chan struct{}, service screenshotter, file *os.File) error {
	var writer *aviWriter
	interval := time.Second / recordingFrameRate
	for {
		select {
		case <-stop:
			if writer == nil {
				_ = file.Close()
				return fmt.Errorf("the recording stopped before the first frame arrived")
			}
			return writer.close()
		default:
		}
		begun := time.Now()
		screenshot, err := service.TakeScreenshot()
		if err != nil {
			if writer != nil {
				_ = writer.close()
			} else {
				_ = file.Close()
			}
			return fmt.Errorf("capture screen frame: %w", err)
		}
		frame, width, height, err := jpegFrame(screenshot)
		if err == nil && writer == nil {
			writer, err = newAVIWriter(file, width, height, recordingFrameRate)
		}
		if err == nil {
			err = writer.writeFrame(frame)
		}
		if err != nil {
			if writer != nil {
				_ = writer.close()
			} else {
				_ = file.Close()
			}
			return err
		}
		if remaining := interval - time.Since(begun); remaining > 0 {
			select {
			case <-stop:
			case <-time.After(remaining):
			}
		}
	}
}

// StopScreenRecording ends a recording and returns its artifact. Mirrors the
// simulator: the stop half completes the lifecycle for the recording
// controller and is not part of the frozen v0 surface.
func (driver *Driver) StopScreenRecording(
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.diagMu.Lock()
	recording, ok := driver.recordings[id]
	delete(driver.recordings, id)
	driver.diagMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no iOS screen recording is running for %q", id)
	}
	close(recording.stop)
	select {
	case <-recording.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if recording.err != nil {
		return nil, fmt.Errorf("iOS screen recording failed: %w", recording.err)
	}
	return []device.Artifact{{Kind: "recording", Path: recording.outputPath}}, nil
}

// stopAllRecordings releases every recording still running at Close.
func (driver *Driver) stopAllRecordings() {
	driver.diagMu.Lock()
	recordings := driver.recordings
	driver.recordings = nil
	driver.diagMu.Unlock()
	for _, recording := range recordings {
		close(recording.stop)
		<-recording.done
	}
}

func decodeScreenshotImage(screenshot []byte) (image.Image, error) {
	decoded, _, err := image.Decode(bytes.NewReader(screenshot))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return decoded, nil
}

func openScreenshotsOnDevice(entry goios.DeviceEntry) (screenshotter, error) {
	return instruments.NewScreenshotService(entry)
}
