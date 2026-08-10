package iosdevice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/crashreport"
	"github.com/danielpaulus/go-ios/ios/syslog"

	"github.com/larchwave/flowbaton/internal/device"
)

// The physical diagnostics: device logs stream from the syslog relay, crash
// artifacts download through the crash-report copier. Both replace the
// embedded simulator methods, which would otherwise run simctl against a
// hardware udid.

// logStream is the slice of *syslog.Connection this driver uses: a blocking
// line read plus the Close that unblocks it.
type logStream interface {
	ReadLogMessage() (string, error)
	Close() error
}

// syslogCapture is one running device-log capture.
type syslogCapture struct {
	stream     logStream
	outputPath string
	done       chan struct{}
}

const (
	// deviceLogLimitEnv mirrors the simulator's byte cap for one capture.
	deviceLogLimitEnv     = "FLOWBATON_IOS_DEVICE_LOG_LIMIT"
	defaultDeviceLogLimit = int64(256 << 20)
)

func deviceLogLimit() (int64, error) {
	raw := strings.TrimSpace(os.Getenv(deviceLogLimitEnv))
	if raw == "" {
		return defaultDeviceLogLimit, nil
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("%s must be a positive byte count, not %q", deviceLogLimitEnv, raw)
	}
	return limit, nil
}

// prepareArtifactDirectory resolves and creates the artifact directory,
// mirroring the simulator driver's rules: empty is refused, the result is
// absolute and symlink-resolved.
func prepareArtifactDirectory(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("iOS diagnostic output directory is empty")
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolving iOS diagnostic output directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("creating iOS diagnostic output directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving iOS diagnostic output directory: %w", err)
	}
	return resolved, nil
}

// StartDeviceLogCapture streams the device's syslog relay into an artifact
// file until the matching stop. Capture is device-wide, like the simulator.
func (driver *Driver) StartDeviceLogCapture(
	ctx context.Context,
	request device.DeviceLogRequest,
) (device.CaptureID, error) {
	if strings.TrimSpace(request.AppID) != "" {
		return "", fmt.Errorf(
			"%w: iOS device-log capture is device-wide and cannot filter bundle %q",
			device.ErrUnsupported, request.AppID)
	}
	limit, err := deviceLogLimit()
	if err != nil {
		return "", err
	}
	entry, err := driver.tools.entry()
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := prepareArtifactDirectory(request.OutputDirectory)
	if err != nil {
		return "", err
	}
	output, err := os.CreateTemp(directory, "ios-device-log-*.log")
	if err != nil {
		return "", fmt.Errorf("creating iOS device-log artifact: %w", err)
	}
	open := driver.openSyslog
	if open == nil {
		open = openSyslogOnDevice
	}
	stream, err := open(entry)
	if err != nil {
		_ = output.Close()
		_ = os.Remove(output.Name())
		return "", fmt.Errorf("open the syslog relay on %s: %w", driver.session.udid, err)
	}
	capture := &syslogCapture{stream: stream, outputPath: output.Name(), done: make(chan struct{})}
	id := device.CaptureID(capture.outputPath)

	driver.diagMu.Lock()
	if driver.logCaptures == nil {
		driver.logCaptures = map[device.CaptureID]*syslogCapture{}
	}
	driver.logCaptures[id] = capture
	driver.diagMu.Unlock()

	go func() {
		defer close(capture.done)
		defer output.Close()
		written := int64(0)
		for {
			line, err := stream.ReadLogMessage()
			if err != nil {
				return
			}
			if !strings.HasSuffix(line, "\n") {
				line += "\n"
			}
			if written+int64(len(line)) > limit {
				fmt.Fprintf(output, "[flowbaton] capture truncated at %d bytes (%s)\n",
					written, deviceLogLimitEnv)
				_ = stream.Close()
				return
			}
			count, err := output.WriteString(line)
			written += int64(count)
			if err != nil {
				_ = stream.Close()
				return
			}
		}
	}()
	return id, nil
}

// StopDeviceLogCapture ends a capture and hands back its artifact.
func (driver *Driver) StopDeviceLogCapture(
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.diagMu.Lock()
	capture, ok := driver.logCaptures[id]
	delete(driver.logCaptures, id)
	driver.diagMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no iOS device-log capture is running for %q", id)
	}
	_ = capture.stream.Close()
	select {
	case <-capture.done:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []device.Artifact{{Kind: "log", Path: capture.outputPath}}, nil
}

// stopAllLogCaptures releases every capture still running at Close.
func (driver *Driver) stopAllLogCaptures() {
	driver.diagMu.Lock()
	captures := driver.logCaptures
	driver.logCaptures = nil
	driver.diagMu.Unlock()
	for _, capture := range captures {
		_ = capture.stream.Close()
		<-capture.done
	}
}

// CollectCrashArtifacts downloads the device's crash reports. Collection is
// device-wide: crash filenames carry process names, not bundle ids, so an
// AppID filter would silently miss reports.
func (driver *Driver) CollectCrashArtifacts(
	ctx context.Context,
	request device.ArtifactRequest,
) ([]device.Artifact, error) {
	if strings.TrimSpace(request.AppID) != "" {
		return nil, fmt.Errorf(
			"%w: iOS crash collection is device-wide and cannot filter bundle %q",
			device.ErrUnsupported, request.AppID)
	}
	entry, err := driver.tools.entry()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := prepareArtifactDirectory(request.OutputDirectory)
	if err != nil {
		return nil, err
	}
	download := driver.downloadCrashReports
	if download == nil {
		download = crashreport.DownloadReports
	}
	if err := download(entry, "*", directory); err != nil {
		return nil, fmt.Errorf("download crash reports from %s: %w", driver.session.udid, err)
	}
	var artifacts []device.Artifact
	err = filepath.WalkDir(directory, func(path string, info os.DirEntry, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		artifacts = append(artifacts, device.Artifact{Kind: "crash", Path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing downloaded crash reports: %w", err)
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	return artifacts, nil
}

func openSyslogOnDevice(entry goios.DeviceEntry) (logStream, error) {
	return syslog.New(entry)
}
