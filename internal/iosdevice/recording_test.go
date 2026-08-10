package iosdevice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goios "github.com/danielpaulus/go-ios/ios"

	"github.com/larchwave/flowbaton/internal/device"
)

// fakeScreens serves a PNG per call and signals each capture so tests can
// wait for frames deterministically.
type fakeScreens struct {
	frame    []byte
	captured chan struct{}
	closed   bool
}

func newFakeScreens(t *testing.T) *fakeScreens {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 32, 16))
	for x := range 32 {
		canvas.Set(x, 8, color.RGBA{R: 255, A: 255})
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return &fakeScreens{frame: buffer.Bytes(), captured: make(chan struct{}, 64)}
}

func (screens *fakeScreens) TakeScreenshot() ([]byte, error) {
	select {
	case screens.captured <- struct{}{}:
	default:
	}
	return screens.frame, nil
}

func (screens *fakeScreens) Close() { screens.closed = true }

func TestScreenRecordingAssemblesAnMJPEGAVI(t *testing.T) {
	driver := boundDriver(t)
	screens := newFakeScreens(t)
	driver.openScreenshots = func(goios.DeviceEntry) (screenshotter, error) { return screens, nil }

	output := filepath.Join(t.TempDir(), "run.avi")
	id, err := driver.StartScreenRecording(context.Background(),
		device.ScreenRecordingRequest{OutputPath: output})
	if err != nil {
		t.Fatalf("StartScreenRecording: %v", err)
	}
	<-screens.captured
	<-screens.captured

	artifacts, err := driver.StopScreenRecording(context.Background(), id)
	if err != nil {
		t.Fatalf("StopScreenRecording: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "recording" || artifacts[0].Path != output {
		t.Fatalf("artifacts = %#v", artifacts)
	}
	if !screens.closed {
		t.Fatal("the screenshot service must close with the recording")
	}

	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(content[0:4]) != "RIFF" || string(content[8:12]) != "AVI " {
		t.Fatalf("artifact is not a RIFF AVI: % x", content[:12])
	}
	if int(binary.LittleEndian.Uint32(content[4:8])) != len(content)-8 {
		t.Fatalf("RIFF size = %d, file = %d", binary.LittleEndian.Uint32(content[4:8]), len(content))
	}
	frames := binary.LittleEndian.Uint32(content[48:52])
	if frames < 1 {
		t.Fatal("the container reports zero frames")
	}
	if frames != binary.LittleEndian.Uint32(content[140:144]) {
		t.Fatal("avih and strh frame counts disagree")
	}
	if !bytes.Contains(content, []byte("MJPG")) || !bytes.Contains(content, []byte("idx1")) {
		t.Fatal("the container lacks the MJPG stream or its index")
	}
	firstChunk := bytes.Index(content, []byte("00dc"))
	if firstChunk < 0 || content[firstChunk+8] != 0xFF || content[firstChunk+9] != 0xD8 {
		t.Fatal("the first video chunk is not a JPEG frame")
	}
}

func TestScreenRecordingValidatesTheSink(t *testing.T) {
	driver := boundDriver(t)
	_, err := driver.StartScreenRecording(context.Background(),
		device.ScreenRecordingRequest{OutputPath: "-starts-with-dash.avi"})
	if err == nil {
		t.Fatal("an argv-shaped sink was accepted")
	}
}

func TestScreenRecordingSurfacesCaptureFailures(t *testing.T) {
	driver := boundDriver(t)
	screens := &brokenScreens{captured: make(chan struct{}, 1)}
	driver.openScreenshots = func(goios.DeviceEntry) (screenshotter, error) {
		return screens, nil
	}
	output := filepath.Join(t.TempDir(), "run.avi")
	id, err := driver.StartScreenRecording(context.Background(),
		device.ScreenRecordingRequest{OutputPath: output})
	if err != nil {
		t.Fatalf("StartScreenRecording: %v", err)
	}
	<-screens.captured
	_, err = driver.StopScreenRecording(context.Background(), id)
	if err == nil || !strings.Contains(err.Error(), "screenshot service broke") {
		t.Fatalf("error = %v, want the capture failure surfaced", err)
	}
}

type brokenScreens struct{ captured chan struct{} }

func (screens *brokenScreens) TakeScreenshot() ([]byte, error) {
	select {
	case screens.captured <- struct{}{}:
	default:
	}
	return nil, errors.New("screenshot service broke")
}
func (screens *brokenScreens) Close() {}
