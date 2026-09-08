package ios

// stdio capture: the application's standard output and error as a flow log.
//
// The unified log never carries a process's stdout or stderr. On the
// Simulator the driver launches the app itself, so it can bind both streams
// to files with `simctl launch --stdout --stderr`. The capture is opened
// before the launch and stays pending until the flow launches its
// application; every launch while it is open lands in its own file pair, and
// the stop merges them in order into one file.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
)

// ErrStdioCaptureUnused is the stop error of a stdio capture that saw no
// launch of its application: there is nothing to report and no file is left.
var ErrStdioCaptureUnused = errors.New(
	"no launch of the application happened while the stdio capture was open; put startLogCapture before launchApp")

// stdioLauncher is the launch a stdio capture needs. Only the Simulator's
// tools have it: a physical device's launch cannot bind the process's streams.
type stdioLauncher interface {
	LaunchCapturingStdio(
		ctx context.Context, bundleID string, arguments []LaunchArgument, stdoutPath, stderrPath string,
	) error
}

type iosStdioCapture struct {
	id    device.CaptureID
	appID string
	// outputPath is the merged file; the id is the same path.
	outputPath string
	// segments are the stdout/stderr file pairs, one per launch, in order.
	segments []stdioSegment
}

type stdioSegment struct {
	stdout string
	stderr string
}

// StartStdioCapture opens a pending capture of request.AppID's stdout and
// stderr in request.OutputDirectory. It is not part of frozen Driver v0; the
// host asks for it by type and stops it through StopDeviceLogCapture.
func (driver *Driver) StartStdioCapture(
	_ context.Context, request device.DeviceLogRequest,
) (device.CaptureID, error) {
	if _, ok := driver.simctl.(stdioLauncher); !ok {
		return "", fmt.Errorf(
			"%w: stdio capture needs the Simulator; a physical device exposes syslog only", device.ErrUnsupported)
	}
	appID := strings.TrimSpace(request.AppID)
	if appID == "" {
		return "", errors.New("iOS stdio capture needs the flow's application")
	}
	directory, err := prepareIOSArtifactDirectory(request.OutputDirectory)
	if err != nil {
		return "", err
	}
	driver.logMu.Lock()
	defer driver.logMu.Unlock()
	if driver.stdio != nil {
		return "", fmt.Errorf("an iOS stdio capture is already open (%s)", driver.stdio.id)
	}
	output, err := os.CreateTemp(directory, "ios-stdio-*.log")
	if err != nil {
		return "", fmt.Errorf("creating iOS stdio artifact: %w", err)
	}
	if err := output.Close(); err != nil {
		return "", fmt.Errorf("creating iOS stdio artifact: %w", err)
	}
	driver.stdio = &iosStdioCapture{id: device.CaptureID(output.Name()), appID: appID, outputPath: output.Name()}
	return driver.stdio.id, nil
}

// nextStdioSegment allocates the file pair for a launch of appID when a
// stdio capture is pending for it, and nil when the launch is a plain one.
// simctl does not create the files itself (checked on Xcode 26.2), so the
// driver does.
func (driver *Driver) nextStdioSegment(appID string) (*stdioSegment, error) {
	driver.logMu.Lock()
	defer driver.logMu.Unlock()
	capture := driver.stdio
	if capture == nil || capture.appID != appID {
		return nil, nil
	}
	base := strings.TrimSuffix(capture.outputPath, filepath.Ext(capture.outputPath))
	index := len(capture.segments) + 1
	segment := stdioSegment{
		stdout: fmt.Sprintf("%s-%d.out", base, index), stderr: fmt.Sprintf("%s-%d.err", base, index)}
	for _, path := range []string{segment.stdout, segment.stderr} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			removeStdioSegment(segment)
			return nil, fmt.Errorf("creating iOS stdio segment: %w", err)
		}
	}
	capture.segments = append(capture.segments, segment)
	return &segment, nil
}

// dropStdioSegment forgets a segment whose launch failed, so a retry starts
// clean and the merge does not report an empty launch.
func (driver *Driver) dropStdioSegment(segment stdioSegment) {
	driver.logMu.Lock()
	defer driver.logMu.Unlock()
	removeStdioSegment(segment)
	if capture := driver.stdio; capture != nil {
		capture.segments = removeSegment(capture.segments, segment)
	}
}

func removeSegment(segments []stdioSegment, gone stdioSegment) []stdioSegment {
	kept := make([]stdioSegment, 0, len(segments))
	for _, segment := range segments {
		if segment != gone {
			kept = append(kept, segment)
		}
	}
	return kept
}

func removeStdioSegment(segment stdioSegment) {
	_ = os.Remove(segment.stdout)
	_ = os.Remove(segment.stderr)
}

// takeStdioCapture detaches the pending capture when id names it.
func (driver *Driver) takeStdioCapture(id device.CaptureID) *iosStdioCapture {
	driver.logMu.Lock()
	defer driver.logMu.Unlock()
	capture := driver.stdio
	if capture == nil || (id != "" && capture.id != id) {
		return nil
	}
	driver.stdio = nil
	return capture
}

// finalizeIOSStdioCapture merges the segments into the capture's file, in
// launch order, each stream under a marker line. The stderr section is
// written only when the process wrote to it. The byte cap applies to the
// merged file.
//
// ponytail: the cap is enforced here at stop, not while the app writes; a
// fifo-backed reader if runaway output ever matters.
func finalizeIOSStdioCapture(capture *iosStdioCapture, limit int64) ([]device.Artifact, error) {
	if len(capture.segments) == 0 {
		_ = os.Remove(capture.outputPath)
		return nil, fmt.Errorf("iOS stdio capture %q: %w", capture.id, ErrStdioCaptureUnused)
	}
	output, err := os.OpenFile(capture.outputPath, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("merging iOS stdio capture: %w", err)
	}
	writer := newIOSLogWriter(output, limit)
	var mergeErr error
	for index, segment := range capture.segments {
		for _, stream := range []struct{ label, path string }{{"stdout", segment.stdout}, {"stderr", segment.stderr}} {
			if mergeErr = appendStdioSection(writer, index+1, stream.label, stream.path); mergeErr != nil {
				break
			}
		}
		if mergeErr != nil {
			break
		}
	}
	if err := output.Close(); err != nil && mergeErr == nil {
		mergeErr = err
	}
	if mergeErr != nil && !errors.Is(mergeErr, errIOSDeviceLogLimit) {
		return nil, fmt.Errorf("merging iOS stdio capture: %w", mergeErr)
	}
	for _, segment := range capture.segments {
		removeStdioSegment(segment)
	}
	metadata := map[string]string{"source": "stdio", "scope": "app", "appId": capture.appID}
	if writer.limitError() != nil {
		metadata["truncated"] = "true"
	}
	return []device.Artifact{{Kind: "log", Path: capture.outputPath, Metadata: metadata}}, nil
}

// appendStdioSection streams one segment file through the capped writer. It
// never holds the file in memory: the process may have written far more than
// the cap, and the stop must not pay for that.
func appendStdioSection(writer io.Writer, launch int, label, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if label == "stderr" && info.Size() == 0 {
		return nil
	}
	if _, err := fmt.Fprintf(writer, "### launch %d %s\n", launch, label); err != nil {
		return err
	}
	tracked := &lastByteWriter{Writer: writer}
	written, err := io.Copy(tracked, file)
	if err != nil {
		return err
	}
	if written > 0 && tracked.last != '\n' {
		_, err = io.WriteString(writer, "\n")
	}
	return err
}

// lastByteWriter remembers the last byte that reached the writer, so a
// section that does not end in a newline gets one before the next marker.
type lastByteWriter struct {
	io.Writer
	last byte
}

func (writer *lastByteWriter) Write(data []byte) (int, error) {
	written, err := writer.Writer.Write(data)
	if written > 0 {
		writer.last = data[written-1]
	}
	return written, err
}
