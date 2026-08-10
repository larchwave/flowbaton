package android

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

const (
	maxActiveDeviceLogs   = 4
	maxDeviceLogBytes     = int64(16 << 20)
	maxCrashArtifactBytes = int64(1 << 30)
	deviceLogStopTimeout  = 15 * time.Second
	crashArtifactTimeout  = 2 * time.Minute
	diagnosticPoll        = 100 * time.Millisecond
	diagnosticStopGrace   = 5 * time.Second
)

var (
	errDeviceLogLimit     = errors.New("android device log reached its byte limit")
	errCrashArtifactLimit = errors.New("android crash artifact reached its byte limit")
)

type deviceLogProcess interface {
	stop(context.Context) error
}

type androidDeviceLog struct {
	process deviceLogProcess
	path    string
	appID   string
	serial  string
}

var androidAppID = regexp.MustCompile(`^[A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)+$`)

func (driver *Driver) StartDeviceLogCapture(
	ctx context.Context,
	request device.DeviceLogRequest,
) (device.CaptureID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	directory, err := prepareDiagnosticDirectory(request.OutputDirectory)
	if err != nil {
		return "", err
	}
	args := []string{"-s", driver.serial, "logcat", "-v", "threadtime", "-T", "1"}
	appID := strings.TrimSpace(request.AppID)
	if appID != "" {
		if !androidAppID.MatchString(appID) {
			return "", fmt.Errorf("invalid Android application id %q", appID)
		}
		pid, pidErr := driver.adb.ProcessID(ctx, appID)
		if pidErr != nil {
			return "", fmt.Errorf("starting Android device log for %q: %w", appID, pidErr)
		}
		args = append(args, "--pid="+pid)
	}

	driver.diagnosticMu.Lock()
	defer driver.diagnosticMu.Unlock()
	if len(driver.deviceLogs) >= maxActiveDeviceLogs {
		return "", fmt.Errorf("Android device log capture limit of %d is already active", maxActiveDeviceLogs)
	}
	id, output, path, err := driver.reserveDiagnosticFileLocked(directory, "android-log", ".log")
	if err != nil {
		return "", err
	}
	spawn := driver.spawnDeviceLog
	if spawn == nil {
		spawn = driver.realSpawnDeviceLog
	}
	process, err := spawn(ctx, output, args)
	if err != nil {
		_ = output.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("starting Android device log capture: %w", err)
	}
	if driver.deviceLogs == nil {
		driver.deviceLogs = map[device.CaptureID]androidDeviceLog{}
	}
	driver.deviceLogs[id] = androidDeviceLog{
		process: process,
		path:    path,
		appID:   appID,
		serial:  driver.serial,
	}
	return id, nil
}

func (driver *Driver) StopDeviceLogCapture(
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.diagnosticMu.Lock()
	capture, ok := driver.deviceLogs[id]
	delete(driver.deviceLogs, id)
	driver.diagnosticMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no Android device log capture is running for %q", id)
	}
	return finalizeDeviceLog(ctx, id, capture)
}

func (driver *Driver) drainDeviceLogs(ctx context.Context) []error {
	driver.diagnosticMu.Lock()
	ids := make([]device.CaptureID, 0, len(driver.deviceLogs))
	for id := range driver.deviceLogs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	captures := driver.deviceLogs
	driver.deviceLogs = nil
	driver.diagnosticMu.Unlock()

	errs := make([]error, 0, len(ids))
	for _, id := range ids {
		if _, err := finalizeDeviceLog(ctx, id, captures[id]); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func finalizeDeviceLog(
	ctx context.Context,
	id device.CaptureID,
	capture androidDeviceLog,
) ([]device.Artifact, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deviceLogStopTimeout)
	defer cancel()
	stopErr := capture.process.stop(cleanupCtx)
	validationErr := validateDiagnosticArtifact(capture.path, maxDeviceLogBytes)
	if err := errors.Join(stopErr, validationErr); err != nil {
		removeErr := os.Remove(capture.path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing partial device log %q: %w", capture.path, removeErr))
		}
		return nil, fmt.Errorf("finalizing Android device log %q: %w", id, err)
	}
	metadata := map[string]string{"serial": capture.serial}
	if capture.appID != "" {
		metadata["app_id"] = capture.appID
	}
	return []device.Artifact{{Kind: "device-log", Path: capture.path, Metadata: metadata}}, nil
}

func (driver *Driver) CollectCrashArtifacts(
	ctx context.Context,
	request device.ArtifactRequest,
) ([]device.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if appID := strings.TrimSpace(request.AppID); appID != "" {
		return nil, fmt.Errorf(
			"%w: adb bugreport is device-wide and cannot filter application %q",
			device.ErrUnsupported, appID)
	}
	directory, err := prepareDiagnosticDirectory(request.OutputDirectory)
	if err != nil {
		return nil, err
	}
	driver.diagnosticMu.Lock()
	_, output, path, err := driver.reserveDiagnosticFileLocked(directory, "android-bugreport", ".zip")
	driver.diagnosticMu.Unlock()
	if err != nil {
		return nil, err
	}
	if closeErr := output.Close(); closeErr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("preparing Android bugreport output: %w", closeErr)
	}
	collectCtx, cancel := context.WithTimeout(ctx, crashArtifactTimeout)
	defer cancel()
	err = runBoundedArtifact(collectCtx, path, maxCrashArtifactBytes, errCrashArtifactLimit,
		func(runCtx context.Context) error { return driver.adb.Bugreport(runCtx, path) })
	if err == nil {
		err = validateDiagnosticArtifact(path, maxCrashArtifactBytes)
	}
	if err != nil {
		removeErr := os.Remove(path)
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("removing partial crash artifact %q: %w", path, removeErr))
		}
		return nil, fmt.Errorf("collecting Android crash artifacts: %w", err)
	}
	return []device.Artifact{{
		Kind: "crash-artifact", Path: path,
		Metadata: map[string]string{"serial": driver.serial},
	}}, nil
}

func prepareDiagnosticDirectory(raw string) (string, error) {
	directory := strings.TrimSpace(raw)
	if directory == "" {
		return "", fmt.Errorf("Android diagnostics require an output directory")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolving Android diagnostic directory %q: %w", directory, err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("creating Android diagnostic directory %q: %w", directory, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("checking Android diagnostic directory %q: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("Android diagnostic output %q is not a real directory", directory)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving Android diagnostic directory %q: %w", directory, err)
	}
	return resolved, nil
}

func (driver *Driver) reserveDiagnosticFileLocked(
	directory string,
	prefix string,
	extension string,
) (device.CaptureID, *os.File, string, error) {
	for range 1000 {
		driver.diagnosticSequence++
		sequence := driver.diagnosticSequence
		id := device.CaptureID(fmt.Sprintf("%s-%06d", prefix, sequence))
		path := filepath.Join(directory, string(id)+extension)
		output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, "", fmt.Errorf("reserving Android diagnostic output %q: %w", path, err)
		}
		return id, output, path, nil
	}
	return "", nil, "", fmt.Errorf("could not reserve a unique Android diagnostic filename")
}

func validateDiagnosticArtifact(path string, maximumBytes int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("checking diagnostic artifact %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("diagnostic artifact %q is not a regular file", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("diagnostic artifact %q is empty", path)
	}
	if info.Size() > maximumBytes {
		return fmt.Errorf("diagnostic artifact %q exceeds the %d-byte limit", path, maximumBytes)
	}
	return nil
}

func copyBoundedDeviceLog(reader io.Reader, writer io.Writer, maximumBytes int64) error {
	buffer := make([]byte, 32*1024)
	var written int64
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			remaining := maximumBytes - written
			if int64(read) > remaining {
				if remaining > 0 {
					if _, writeErr := writer.Write(buffer[:remaining]); writeErr != nil {
						return writeErr
					}
				}
				return errDeviceLogLimit
			}
			count, writeErr := writer.Write(buffer[:read])
			written += int64(count)
			if writeErr != nil {
				return writeErr
			}
			if count != read {
				return io.ErrShortWrite
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type adbDeviceLogProcess struct {
	cancel   context.CancelFunc
	done     <-chan error
	copyDone <-chan error
	output   *os.File
	reader   *os.File
}

func (driver *Driver) realSpawnDeviceLog(
	ctx context.Context,
	output *os.File,
	args []string,
) (deviceLogProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	processCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	command := exec.CommandContext(processCtx, adbExecutable(), args...)
	command.Stdout = writer
	command.Stderr = writer
	if err := command.Start(); err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	_ = writer.Close()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	copyDone := make(chan error, 1)
	go func() {
		copyErr := copyBoundedDeviceLog(reader, output, maxDeviceLogBytes)
		_ = reader.Close()
		if copyErr != nil {
			cancel()
		}
		copyDone <- copyErr
	}()
	return &adbDeviceLogProcess{
		cancel: cancel, done: done, copyDone: copyDone, output: output, reader: reader,
	}, nil
}

func (process *adbDeviceLogProcess) stop(ctx context.Context) error {
	defer process.cancel()
	var errs []error
	naturallyExited := false
	var waitErr error
	select {
	case waitErr = <-process.done:
		naturallyExited = true
	default:
		process.cancel()
		select {
		case waitErr = <-process.done:
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			_ = process.reader.Close()
			select {
			case <-process.done:
			case <-time.After(diagnosticStopGrace):
				errs = append(errs, fmt.Errorf(
					"adb logcat did not stop within %v", diagnosticStopGrace))
			}
		}
	}
	if naturallyExited && waitErr != nil {
		errs = append(errs, fmt.Errorf("adb logcat exited: %w", waitErr))
	}
	select {
	case copyErr := <-process.copyDone:
		errs = append(errs, copyErr)
	case <-ctx.Done():
		errs = append(errs, ctx.Err())
		_ = process.reader.Close()
		select {
		case copyErr := <-process.copyDone:
			errs = append(errs, copyErr)
		case <-time.After(diagnosticStopGrace):
			errs = append(errs, fmt.Errorf(
				"Android device-log writer did not stop within %v", diagnosticStopGrace))
		}
	}
	if syncErr := process.output.Sync(); syncErr != nil {
		errs = append(errs, syncErr)
	}
	if closeErr := process.output.Close(); closeErr != nil {
		errs = append(errs, closeErr)
	}
	return errors.Join(errs...)
}

func runBoundedArtifact(
	ctx context.Context,
	path string,
	maximumBytes int64,
	limitErr error,
	run func(context.Context) error,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(runCtx) }()
	ticker := time.NewTicker(diagnosticPoll)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			cancel()
			return errors.Join(ctx.Err(), awaitDiagnosticStop(done))
		case <-ticker.C:
			info, err := os.Stat(path)
			if err == nil && info.Size() > maximumBytes {
				cancel()
				return errors.Join(limitErr, awaitDiagnosticStop(done))
			}
		}
	}
}

func awaitDiagnosticStop(done <-chan error) error {
	timer := time.NewTimer(diagnosticStopGrace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return fmt.Errorf("Android diagnostic command did not stop within %v", diagnosticStopGrace)
	}
}
