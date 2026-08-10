package iosdevice

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goios "github.com/danielpaulus/go-ios/ios"

	"github.com/larchwave/flowbaton/internal/device"
)

// fakeLogStream feeds scripted lines and blocks until closed, like the real
// syslog relay.
type fakeLogStream struct {
	lines  chan string
	closed chan struct{}
}

// The lines channel is unbuffered: a send returns only once the capture
// goroutine took the line, which keeps write-then-stop tests deterministic.
func newFakeLogStream() *fakeLogStream {
	return &fakeLogStream{lines: make(chan string), closed: make(chan struct{})}
}

func (stream *fakeLogStream) ReadLogMessage() (string, error) {
	select {
	case line := <-stream.lines:
		return line, nil
	case <-stream.closed:
		return "", io.EOF
	}
}

func (stream *fakeLogStream) Close() error {
	select {
	case <-stream.closed:
	default:
		close(stream.closed)
	}
	return nil
}

func boundDriver(t *testing.T) *Driver {
	t.Helper()
	driver := NewDriver("00008110-TEST", 30001, nil, nil)
	driver.tools.bind(goios.DeviceEntry{})
	return driver
}

func TestDeviceLogCaptureStreamsSyslogIntoTheArtifact(t *testing.T) {
	driver := boundDriver(t)
	stream := newFakeLogStream()
	driver.openSyslog = func(goios.DeviceEntry) (logStream, error) { return stream, nil }

	directory := t.TempDir()
	id, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: directory})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture: %v", err)
	}
	stream.lines <- "line one"
	stream.lines <- "line two\n"

	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture: %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "log" {
		t.Fatalf("artifacts = %#v, want one log artifact", artifacts)
	}
	content, err := os.ReadFile(artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "line one\nline two\n" {
		t.Fatalf("artifact content = %q", content)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
		t.Fatal("a second stop must refuse: nothing is running")
	}
}

func TestDeviceLogCaptureIsDeviceWide(t *testing.T) {
	driver := boundDriver(t)
	_, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{AppID: "com.example.app", OutputDirectory: t.TempDir()})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported for a bundle filter", err)
	}
}

func TestDeviceLogCaptureHonorsTheByteLimit(t *testing.T) {
	t.Setenv(deviceLogLimitEnv, "10")
	driver := boundDriver(t)
	stream := newFakeLogStream()
	driver.openSyslog = func(goios.DeviceEntry) (logStream, error) { return stream, nil }

	id, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture: %v", err)
	}
	stream.lines <- "12345"
	stream.lines <- "this line breaks the limit"

	// The limit closes the stream; the capture goroutine exits on its own.
	<-stream.closed

	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture: %v", err)
	}
	content, err := os.ReadFile(artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "truncated") ||
		!strings.Contains(string(content), deviceLogLimitEnv) {
		t.Fatalf("content = %q, want a truncation marker naming the limit variable", content)
	}
	if strings.Contains(string(content), "breaks the limit") {
		t.Fatalf("content = %q: the over-limit line must not be written", content)
	}
}

func TestCloseReleasesRunningLogCaptures(t *testing.T) {
	driver := boundDriver(t)
	stream := newFakeLogStream()
	driver.openSyslog = func(goios.DeviceEntry) (logStream, error) { return stream, nil }
	if _, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir()}); err != nil {
		t.Fatalf("StartDeviceLogCapture: %v", err)
	}
	driver.stopAllLogCaptures()
	select {
	case <-stream.closed:
	default:
		t.Fatal("Close must close the syslog stream")
	}
}

func TestCollectCrashArtifactsDownloadsAndLists(t *testing.T) {
	driver := boundDriver(t)
	driver.downloadCrashReports = func(_ goios.DeviceEntry, pattern, directory string) error {
		if pattern != "*" {
			t.Fatalf("pattern = %q, want the wildcard", pattern)
		}
		for _, name := range []string{"b.ips", "a.ips"} {
			if err := os.WriteFile(filepath.Join(directory, name), []byte("crash"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	}

	artifacts, err := driver.CollectCrashArtifacts(context.Background(),
		device.ArtifactRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("CollectCrashArtifacts: %v", err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want both reports", artifacts)
	}
	if artifacts[0].Kind != "crash" || filepath.Base(artifacts[0].Path) != "a.ips" {
		t.Fatalf("artifacts = %#v, want sorted crash reports", artifacts)
	}
}

func TestCollectCrashArtifactsIsDeviceWide(t *testing.T) {
	driver := boundDriver(t)
	_, err := driver.CollectCrashArtifacts(context.Background(),
		device.ArtifactRequest{AppID: "com.example.app", OutputDirectory: t.TempDir()})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("error = %v, want ErrUnsupported for a bundle filter", err)
	}
}
