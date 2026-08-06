package ios

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// The simctl half of the iOS host side. specs/02-device-drivers.md line 65
// names the operations: list -j, boot/shutdown, launch/terminate,
// install/uninstall, keychain reset, privacy permissions, location,
// screenshot, media.
//
// The command runner is injected rather than called directly, so the argv this
// package builds is testable without a simulator. A wrong flag here is a wrong
// device operation, which makes argv the thing worth pinning.

// CommandRunner executes one external command and returns its combined output.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands for real.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Simctl is the typed surface over `xcrun simctl` for one simulator.
type Simctl struct {
	udid   string
	runner CommandRunner
}

// NewSimctl binds a udid to a runner. An empty udid is legal only for the
// commands that are not device-scoped, such as ListDevices.
func NewSimctl(udid string, runner CommandRunner) *Simctl {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Simctl{udid: udid, runner: runner}
}

// LaunchArgument is one typed launch argument. Type uses the public
// documentation's vocabulary: string, boolean, integer, double.
type LaunchArgument struct {
	Key   string
	Value string
	Type  string
}

// Device is one entry of the simulator inventory.
type Device struct {
	UDID      string
	Name      string
	State     string
	Runtime   string
	Available bool
}

func (simctl *Simctl) Boot(ctx context.Context) error {
	return simctl.deviceCommand(ctx, "boot")
}

func (simctl *Simctl) Shutdown(ctx context.Context) error {
	return simctl.deviceCommand(ctx, "shutdown")
}

// Launch starts an app. terminateRunning maps onto simctl's own
// --terminate-running-process, which is how stopApp is expressed here.
func (simctl *Simctl) Launch(
	ctx context.Context,
	bundleID string,
	arguments []LaunchArgument,
	terminateRunning bool,
) error {
	args := []string{"launch"}
	if terminateRunning {
		args = append(args, "--terminate-running-process")
	}
	args = append(args, simctl.udid, bundleID)
	args = append(args, renderLaunchArguments(arguments)...)
	return simctl.run(ctx, args, true)
}

// renderLaunchArguments applies the serialization defined by
// specs/06-launch-app-semantics.md section 5: a boolean is
// passed bare, everything else takes a leading dash.
func renderLaunchArguments(arguments []LaunchArgument) []string {
	rendered := make([]string, 0, len(arguments)*2)
	for _, argument := range arguments {
		key := argument.Key
		if argument.Type != "boolean" {
			key = "-" + key
		}
		rendered = append(rendered, key, argument.Value)
	}
	return rendered
}

// nothingToTerminate is simctl's own wording when the app was not running.
//
// Matched on the message rather than the exit status: simctl uses code 3 for
// several NSPOSIXError conditions, and swallowing all of them would hide a
// shut-down device.
const nothingToTerminate = "found nothing to terminate"

// Terminate stops an app, and treats an app that was not running as success.
//
// simctl exits 3 with "found nothing to terminate" when the app is already
// stopped. The caller's goal is satisfied in that case. Handle it here because
// StopApp and KillApp share this behavior.
func (simctl *Simctl) Terminate(ctx context.Context, bundleID string) error {
	err := simctl.run(ctx, []string{"terminate", simctl.udid, bundleID}, true)
	if err != nil && strings.Contains(err.Error(), nothingToTerminate) {
		return nil
	}
	return err
}

func (simctl *Simctl) Uninstall(ctx context.Context, bundleID string) error {
	return simctl.run(ctx, []string{"uninstall", simctl.udid, bundleID}, true)
}

func (simctl *Simctl) Install(ctx context.Context, appPath string) error {
	return simctl.run(ctx, []string{"install", simctl.udid, appPath}, true)
}

// ResetKeychain clears the whole simulator keychain, which is what
// clearKeychain means on iOS.
func (simctl *Simctl) ResetKeychain(ctx context.Context) error {
	return simctl.run(ctx, []string{"keychain", simctl.udid, "reset"}, true)
}

func (simctl *Simctl) OpenURL(ctx context.Context, url string) error {
	return simctl.run(ctx, []string{"openurl", simctl.udid, url}, true)
}

// SetLocation sets the simulated location. simctl takes one "lat,lon"
// argument, and the numbers are rendered without an exponent so a coordinate
// never reaches the device in a form it cannot parse.
func (simctl *Simctl) SetLocation(ctx context.Context, latitude, longitude float64) error {
	pair := formatCoordinate(latitude) + "," + formatCoordinate(longitude)
	return simctl.run(ctx, []string{"location", simctl.udid, "set", pair}, true)
}

func formatCoordinate(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func (simctl *Simctl) AddMedia(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("simctl: addmedia requires at least one path")
	}
	return simctl.run(ctx, append([]string{"addmedia", simctl.udid}, paths...), true)
}

// SetPermission maps the authored grant onto simctl's privacy verbs. The grant
// set is the same exact three the engine validates, so an unknown one is
// refused here rather than passed to the device as a stray verb.
func (simctl *Simctl) SetPermission(ctx context.Context, bundleID, permission, grant string) error {
	verb := ""
	switch grant {
	case "allow":
		verb = "grant"
	case "deny":
		verb = "revoke"
	case "unset":
		verb = "reset"
	default:
		return fmt.Errorf("simctl: permission grant %q must be allow, deny, or unset", grant)
	}
	return simctl.run(ctx, []string{"privacy", simctl.udid, verb, permission, bundleID}, true)
}

func (simctl *Simctl) Screenshot(ctx context.Context, outputPath string) error {
	return simctl.run(ctx, []string{"io", simctl.udid, "screenshot", outputPath}, true)
}

// ListDevices returns every available simulator across runtimes. Unavailable
// entries are dropped: they cannot be booted, so offering them as targets
// would only produce a later failure.
func (simctl *Simctl) ListDevices(ctx context.Context) ([]Device, error) {
	output, err := simctl.runOutput(ctx, []string{"list", "devices", "-j"}, false)
	if err != nil {
		return nil, err
	}
	var inventory struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			State       string `json:"state"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(output, &inventory); err != nil {
		return nil, fmt.Errorf("simctl: decoding device inventory: %w", err)
	}
	runtimes := make([]string, 0, len(inventory.Devices))
	for runtime := range inventory.Devices {
		runtimes = append(runtimes, runtime)
	}
	// Map iteration order must not decide what an operator sees listed.
	slices.Sort(runtimes)
	var devices []Device
	for _, runtime := range runtimes {
		for _, entry := range inventory.Devices[runtime] {
			if !entry.IsAvailable {
				continue
			}
			devices = append(devices, Device{
				UDID: entry.UDID, Name: entry.Name, State: entry.State,
				Runtime: runtime, Available: true,
			})
		}
	}
	return devices, nil
}

func (simctl *Simctl) deviceCommand(ctx context.Context, verb string) error {
	return simctl.run(ctx, []string{verb, simctl.udid}, true)
}

func (simctl *Simctl) run(ctx context.Context, args []string, needsUDID bool) error {
	_, err := simctl.runOutput(ctx, args, needsUDID)
	return err
}

// runOutput executes one simctl invocation. simctl reports why it refused on
// its own output, so that output is carried into the error rather than
// discarded in favor of a bare exit status.
func (simctl *Simctl) runOutput(ctx context.Context, args []string, needsUDID bool) ([]byte, error) {
	if needsUDID && strings.TrimSpace(simctl.udid) == "" {
		return nil, fmt.Errorf("simctl: %s requires a simulator udid", args[0])
	}
	output, err := simctl.runner.Run(ctx, "xcrun", append([]string{"simctl"}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("simctl %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
