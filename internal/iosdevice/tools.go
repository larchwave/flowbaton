package iosdevice

import (
	"context"
	"fmt"
	"io"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/instruments"
	"github.com/danielpaulus/go-ios/ios/zipconduit"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/ios"
)

// Tools is the physical-device implementation of ios.DeviceTools: the
// out-of-app half the simulator covers with simctl, expressed through
// go-ios services instead. The DeviceEntry is bound by the session after
// it resolves and prepares the device; every method fails closed until then.
//
// Operations Apple locks on hardware (keychain reset, media injection)
// return device.ErrUnsupported permanently. Permissions, open-link, and
// diagnostics gain runner-side/go-ios implementations in later slices and
// fail with ErrUnsupported until those land.
type Tools struct {
	udid   string
	device *goios.DeviceEntry

	// Seams over go-ios services; nil means talk to the real device.
	launch        func(entry goios.DeviceEntry, bundleID string, args []any, env map[string]any, opts map[string]any) (uint64, error)
	kill          func(entry goios.DeviceEntry, bundleID string) error
	install       func(entry goios.DeviceEntry, appPath string) error
	uninstall     func(entry goios.DeviceEntry, bundleID string) error
	startLocation func(entry goios.DeviceEntry, latitude, longitude float64) (io.Closer, error)

	// locationSession holds the open location-simulation service: the device
	// keeps the simulated location only while the service stays connected.
	locationSession io.Closer
}

var _ ios.DeviceTools = (*Tools)(nil)

// NewTools returns the physical-device tools for one udid. bind must run
// before any device operation.
func NewTools(udid string) *Tools {
	return &Tools{udid: udid}
}

// bind attaches the resolved device entry. The session calls it once the
// device is reachable, paired, and (on iOS 17+) tunneled.
func (tools *Tools) bind(entry goios.DeviceEntry) {
	tools.device = &entry
}

func (tools *Tools) entry() (goios.DeviceEntry, error) {
	if tools.device == nil {
		return goios.DeviceEntry{}, fmt.Errorf(
			"physical iOS device %s is not open yet: the session binds the device before use", tools.udid)
	}
	return *tools.device, nil
}

func (tools *Tools) Launch(
	ctx context.Context,
	bundleID string,
	arguments []ios.LaunchArgument,
	terminateRunning bool,
) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	launch := tools.launch
	if launch == nil {
		launch = launchOnDevice
	}
	options := map[string]any{}
	if terminateRunning {
		options["KillExisting"] = 1
	}
	if _, err := launch(entry, bundleID, renderProcessArguments(arguments), nil, options); err != nil {
		return fmt.Errorf("launch %s on physical device %s: %w", bundleID, tools.udid, err)
	}
	return nil
}

// renderProcessArguments mirrors the simulator's argv rendering: non-boolean
// keys become -key value pairs, boolean keys stand alone with their value.
func renderProcessArguments(arguments []ios.LaunchArgument) []any {
	rendered := make([]any, 0, len(arguments)*2)
	for _, argument := range arguments {
		key := argument.Key
		if argument.Type != "boolean" {
			key = "-" + key
		}
		rendered = append(rendered, key, argument.Value)
	}
	return rendered
}

func (tools *Tools) Terminate(ctx context.Context, bundleID string) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	kill := tools.kill
	if kill == nil {
		kill = killOnDevice
	}
	if err := kill(entry, bundleID); err != nil {
		return fmt.Errorf("terminate %s on physical device %s: %w", bundleID, tools.udid, err)
	}
	return nil
}

func (tools *Tools) Uninstall(ctx context.Context, bundleID string) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	uninstall := tools.uninstall
	if uninstall == nil {
		uninstall = uninstallOnDevice
	}
	if err := uninstall(entry, bundleID); err != nil {
		return fmt.Errorf("uninstall %s from physical device %s: %w", bundleID, tools.udid, err)
	}
	return nil
}

func (tools *Tools) Install(ctx context.Context, appPath string) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	install := tools.install
	if install == nil {
		install = installOnDevice
	}
	if err := install(entry, appPath); err != nil {
		return fmt.Errorf("install %s on physical device %s: %w", appPath, tools.udid, err)
	}
	return nil
}

// AppContainer is the simulator's clear-state mechanism (copy the bundle out
// of the container before uninstall). Hardware exposes no app container to
// the host; the physical clear-state path reinstalls from a host-supplied
// .ipa instead and never calls this.
func (tools *Tools) AppContainer(context.Context, string) (string, error) {
	return "", fmt.Errorf(
		"%w: a physical device exposes no app container; clearState reinstalls from a configured .ipa",
		device.ErrUnsupported)
}

func (tools *Tools) Diagnose(context.Context, string, time.Duration) error {
	return fmt.Errorf(
		"%w: physical-device diagnostics arrive with the go-ios syslog/crash slice",
		device.ErrUnsupported)
}

// ResetKeychain is an Apple platform limit: no tool resets the keychain of a
// physical device without jailbreaking it.
func (tools *Tools) ResetKeychain(context.Context) error {
	return fmt.Errorf(
		"%w: Apple provides no keychain reset on physical devices; use a simulator for keychain flows",
		device.ErrUnsupported)
}

// safariBundleID is the system browser every physical device carries.
const safariBundleID = "com.apple.mobilesafari"

// OpenURL opens a link by relaunching Safari with the URL as its launch
// argument — the same technique the WebDriverAgent ecosystem uses, because
// hardware has no host-side `simctl openurl`. KillExisting makes repeated
// openLink steps deterministic instead of reusing a stale page.
func (tools *Tools) OpenURL(ctx context.Context, url string) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	launch := tools.launch
	if launch == nil {
		launch = launchOnDevice
	}
	arguments := []any{"-u", url}
	options := map[string]any{"KillExisting": 1}
	if _, err := launch(entry, safariBundleID, arguments, nil, options); err != nil {
		return fmt.Errorf("open %s in Safari on physical device %s: %w", url, tools.udid, err)
	}
	return nil
}

// SetLocation starts location simulation through the instruments service.
// The service connection must stay open for the location to hold, so the
// session is kept until the next SetLocation or close(). The new session
// starts before the old one closes, so the device never snaps back to its
// real location between two flow steps.
func (tools *Tools) SetLocation(ctx context.Context, latitude, longitude float64) error {
	entry, err := tools.entry()
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	start := tools.startLocation
	if start == nil {
		start = startLocationOnDevice
	}
	session, err := start(entry, latitude, longitude)
	if err != nil {
		return fmt.Errorf("simulate location on physical device %s: %w", tools.udid, err)
	}
	previous := tools.locationSession
	tools.locationSession = session
	if previous != nil {
		_ = previous.Close()
	}
	return nil
}

// close releases the long-lived service sessions the tools hold open. The
// driver calls it on Close; the device then reverts to its real location.
func (tools *Tools) close() error {
	if tools.locationSession == nil {
		return nil
	}
	err := tools.locationSession.Close()
	tools.locationSession = nil
	return err
}

// AddMedia is an Apple platform limit: no tool injects media into the photo
// library of a physical device without jailbreaking it.
func (tools *Tools) AddMedia(context.Context, []string) error {
	return fmt.Errorf(
		"%w: Apple provides no media injection on physical devices; use a simulator for media flows",
		device.ErrUnsupported)
}

// SetPermission is the simulator's per-permission simctl mechanism. The
// physical driver overrides SetPermissions to route the whole map through
// the runner (springboard alert auto-answering), so this backstop only
// fires if the embedded simulator path is reached by mistake.
func (tools *Tools) SetPermission(context.Context, string, string, string) error {
	return fmt.Errorf(
		"%w: hardware permissions are answered by the runner, not by a host tool",
		device.ErrUnsupported)
}

// launchOnDevice starts an app through the instruments process-control
// service and reports its pid.
func launchOnDevice(
	entry goios.DeviceEntry,
	bundleID string,
	args []any,
	env map[string]any,
	opts map[string]any,
) (uint64, error) {
	control, err := instruments.NewProcessControl(entry)
	if err != nil {
		return 0, err
	}
	defer control.Close()
	return control.LaunchAppWithArgs(bundleID, args, env, opts)
}

// killOnDevice resolves the bundle's executable name through installation
// proxy, finds its pid through the device-info service, and kills it.
func killOnDevice(entry goios.DeviceEntry, bundleID string) error {
	proxy, err := installationproxy.New(entry)
	if err != nil {
		return err
	}
	apps, err := proxy.BrowseAllApps()
	proxy.Close()
	if err != nil {
		return err
	}
	executable := ""
	for _, app := range apps {
		if app.CFBundleIdentifier() == bundleID {
			executable = app.CFBundleExecutable()
			break
		}
	}
	if executable == "" {
		return fmt.Errorf("bundle %s is not installed", bundleID)
	}
	info, err := instruments.NewDeviceInfoService(entry)
	if err != nil {
		return err
	}
	process, err := info.ProcessByName(executable)
	info.Close()
	if err != nil {
		return fmt.Errorf("process for %s: %w", bundleID, err)
	}
	control, err := instruments.NewProcessControl(entry)
	if err != nil {
		return err
	}
	defer control.Close()
	return control.KillProcess(process.Pid)
}

func installOnDevice(entry goios.DeviceEntry, appPath string) error {
	conn, err := zipconduit.New(entry)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.SendFile(appPath)
}

// locationService adapts the instruments location-simulation service to
// io.Closer: closing stops the simulation.
type locationService struct {
	service *instruments.LocationSimulationService
}

func (session locationService) Close() error {
	if err := session.service.StopSimulateLocation(); err != nil {
		session.service.Close()
		return err
	}
	session.service.Close()
	return nil
}

func startLocationOnDevice(
	entry goios.DeviceEntry,
	latitude, longitude float64,
) (io.Closer, error) {
	service, err := instruments.NewLocationSimulationService(entry)
	if err != nil {
		return nil, err
	}
	if err := service.StartSimulateLocation(latitude, longitude); err != nil {
		service.Close()
		return nil, err
	}
	return locationService{service: service}, nil
}

func uninstallOnDevice(entry goios.DeviceEntry, bundleID string) error {
	proxy, err := installationproxy.New(entry)
	if err != nil {
		return err
	}
	defer proxy.Close()
	return proxy.Uninstall(bundleID)
}
