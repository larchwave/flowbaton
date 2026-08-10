package iosdevice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/ios"
)

// Driver drives one physical iOS device. It embeds the shared iOS driver —
// gestures, hierarchy, waits, and screenshots ride the same runner wire the
// simulator uses — and replaces only what hardware changes: the session
// (usbmuxd + pairing + tunnel + port forward), the out-of-app tools, and
// runner delivery (go-ios testmanagerd instead of xcodebuild).
type Driver struct {
	*ios.Driver
	session *session
	tools   *Tools
	client  *ios.Client
	// runner is nil in operator-started mode, where the runner must already
	// serve on the device — the same split the simulator driver has.
	runner *deviceRunner

	startupPoll    time.Duration
	startupTimeout time.Duration

	// Diagnostics state: device-log captures and screen recordings running
	// against this device, plus their go-ios seams (nil means real device).
	diagMu               sync.Mutex
	logCaptures          map[device.CaptureID]*syslogCapture
	recordings           map[device.CaptureID]*deviceRecording
	openSyslog           func(entry goios.DeviceEntry) (logStream, error)
	downloadCrashReports func(entry goios.DeviceEntry, pattern, directory string) error
	openScreenshots      func(entry goios.DeviceEntry) (screenshotter, error)
}

var _ device.Driver = (*Driver)(nil)

// NewDriver binds the runner client and the go-ios tools to one physical
// device. The client must point at the HOST end of the forward (the shard
// port); the session forwards it to the runner's device port. A non-nil
// bundle makes Open install and start the runner itself.
func NewDriver(udid string, hostPort int, client *ios.Client, bundle *ios.RunnerBundle) *Driver {
	tools := NewTools(udid)
	driver := &Driver{
		Driver:         ios.NewDriver(udid, hostPort, client, tools, nil),
		session:        newSession(udid, hostPort),
		tools:          tools,
		client:         client,
		startupPoll:    runnerStartupPoll,
		startupTimeout: runnerStartupTimeout,
	}
	if bundle != nil {
		driver.runner = newDeviceRunner(udid, *bundle)
	}
	return driver
}

// Name distinguishes hardware from simulators in operator messages.
func (driver *Driver) Name() string {
	return fmt.Sprintf("ios-device:%s:%d", driver.session.udid, driver.session.hostPort)
}

// Open prepares the device (reachability, pairing, tunnel, forward) before
// any device mutation, binds the tools, delivers the runner when this
// driver manages it, and confirms the runner answers over the wire.
func (driver *Driver) Open(ctx context.Context) error {
	entry, err := driver.session.start(ctx)
	if err != nil {
		return err
	}
	driver.tools.bind(entry)
	if driver.runner == nil {
		if err := driver.Driver.Open(ctx); err != nil {
			return errors.Join(err, driver.session.stop())
		}
		return nil
	}
	if err := driver.runner.start(ctx, entry); err != nil {
		return errors.Join(err, driver.session.stop())
	}
	if err := driver.awaitRunner(ctx); err != nil {
		return errors.Join(err, driver.runner.stop(), driver.session.stop())
	}
	return nil
}

// awaitRunner polls the wire until the serving test answers, honoring the
// same startup-budget override the other drivers read. A runner that dies
// first explains itself instead of timing out silently.
func (driver *Driver) awaitRunner(ctx context.Context) error {
	timeout, err := driver.startupBudget()
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if lastErr = driver.client.Status(ctx); lastErr == nil {
			return nil
		}
		select {
		case <-driver.runner.exited():
			return fmt.Errorf(
				"the runner for %s stopped before it answered: %v",
				driver.session.udid, driver.runner.exitReason())
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(driver.startupPoll):
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"the runner for %s did not answer within %v (%s overrides the wait, in milliseconds): %w",
				driver.session.udid, timeout, startupTimeoutEnv, lastErr)
		}
	}
}

func (driver *Driver) startupBudget() (time.Duration, error) {
	raw := os.Getenv(startupTimeoutEnv)
	if raw == "" {
		return driver.startupTimeout, nil
	}
	millis, err := strconv.Atoi(raw)
	if err != nil || millis <= 0 {
		return 0, fmt.Errorf(
			"%s must be a positive millisecond count, not %q", startupTimeoutEnv, raw)
	}
	return time.Duration(millis) * time.Millisecond, nil
}

// appIPAEnv names the archive ClearAppState reinstalls. Hardware exposes no
// app container to preserve-and-restore the way the simulator does, so the
// operator supplies the app to restore.
const appIPAEnv = "FLOWBATON_IOS_APP_IPA"

// ClearAppState uninstalls the app and reinstalls it from the operator's
// archive — the only state reset Apple allows on hardware.
func (driver *Driver) ClearAppState(ctx context.Context, request device.AppRequest) error {
	archive := os.Getenv(appIPAEnv)
	if archive == "" {
		return fmt.Errorf(
			"clearState on a physical device reinstalls the app: set %s to the .ipa or .app to restore",
			appIPAEnv)
	}
	if _, err := os.Stat(archive); err != nil {
		return fmt.Errorf("%s names an unreadable archive: %w", appIPAEnv, err)
	}
	if err := driver.tools.Uninstall(ctx, request.AppID); err != nil {
		return err
	}
	if err := driver.tools.Install(ctx, archive); err != nil {
		return fmt.Errorf("reinstall %s after clearState: %w", request.AppID, err)
	}
	return nil
}

// SetPermissions hands the whole permission map to the runner, which
// auto-answers the springboard permission alerts as they appear — the only
// permission mechanism Apple leaves open on hardware (there is no host-side
// TCC write like the simulator's `simctl privacy`). The alerts are
// device-global, so the request's app id does not travel on the wire.
func (driver *Driver) SetPermissions(ctx context.Context, request device.PermissionsRequest) error {
	return driver.client.SetPermissions(ctx, request.Permissions)
}

// Close stops what the embedded driver started, then this driver's own
// captures and runner, then the held tool sessions, then the session.
func (driver *Driver) Close(ctx context.Context) error {
	errs := []error{driver.Driver.Close(ctx)}
	driver.stopAllRecordings()
	driver.stopAllLogCaptures()
	if driver.runner != nil {
		errs = append(errs, driver.runner.stop())
	}
	errs = append(errs, driver.tools.close(), driver.session.stop())
	return errors.Join(errs...)
}

// Capabilities declares the physical surface; the embedded simulator
// document would over-promise.
func (driver *Driver) Capabilities() device.Capabilities {
	return DeclaredCapabilities()
}
