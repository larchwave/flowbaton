package ios

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The HTTP runner drives on-screen interaction. Device lifecycle operations —
// installing, launching, clearing, permissions, location, media, and recording
// — use `xcrun simctl` as specified in specs/02-device-drivers.md line 65.
//
// The command runner is injected, so every expectation here is about the exact
// argv the driver will hand the OS. That is the part worth pinning: a wrong
// flag is a wrong device operation.

func TestSimctlBuildsTheExactCommandLine(t *testing.T) {
	t.Parallel()

	const udid = "UDID-1"
	for _, test := range []struct {
		name string
		call func(context.Context, *Simctl) error
		want []string
	}{
		{
			name: "boot",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.Boot(ctx) },
			want: []string{"simctl", "boot", udid},
		},
		{
			name: "shutdown",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.Shutdown(ctx) },
			want: []string{"simctl", "shutdown", udid},
		},
		{
			name: "launch without arguments",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.Launch(ctx, "com.example.a", nil, false)
			},
			want: []string{"simctl", "launch", udid, "com.example.a"},
		},
		{
			name: "launch terminating a running process first",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.Launch(ctx, "com.example.a", nil, true)
			},
			want: []string{"simctl", "launch", "--terminate-running-process", udid, "com.example.a"},
		},
		{
			name: "launch with typed arguments",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.Launch(ctx, "com.example.a", []LaunchArgument{
					{Key: "mode", Value: "probe", Type: "string"},
					{Key: "flag", Value: "true", Type: "boolean"},
				}, false)
			},
			// specs/06 section 5: a boolean argument is passed bare, everything
			// else gets a leading dash. Required by the launch argument contract.
			want: []string{"simctl", "launch", udid, "com.example.a", "-mode", "probe", "flag", "true"},
		},
		{
			name: "terminate",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.Terminate(ctx, "com.example.a") },
			want: []string{"simctl", "terminate", udid, "com.example.a"},
		},
		{
			name: "uninstall",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.Uninstall(ctx, "com.example.a") },
			want: []string{"simctl", "uninstall", udid, "com.example.a"},
		},
		{
			name: "install",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.Install(ctx, "/tmp/Probe.app") },
			want: []string{"simctl", "install", udid, "/tmp/Probe.app"},
		},
		{
			name: "resetKeychain",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.ResetKeychain(ctx) },
			want: []string{"simctl", "keychain", udid, "reset"},
		},
		{
			name: "openURL",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.OpenURL(ctx, "https://example.invalid")
			},
			want: []string{"simctl", "openurl", udid, "https://example.invalid"},
		},
		{
			name: "setLocation",
			call: func(ctx context.Context, simctl *Simctl) error { return simctl.SetLocation(ctx, 48.5, -2.25) },
			want: []string{"simctl", "location", udid, "set", "48.5,-2.25"},
		},
		{
			name: "addMedia",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.AddMedia(ctx, []string{"/tmp/a.png", "/tmp/b.mp4"})
			},
			want: []string{"simctl", "addmedia", udid, "/tmp/a.png", "/tmp/b.mp4"},
		},
		{
			name: "grant a permission",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.SetPermission(ctx, "com.example.a", "camera", "allow")
			},
			want: []string{"simctl", "privacy", udid, "grant", "camera", "com.example.a"},
		},
		{
			name: "deny a permission",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.SetPermission(ctx, "com.example.a", "photos", "deny")
			},
			want: []string{"simctl", "privacy", udid, "revoke", "photos", "com.example.a"},
		},
		{
			name: "unset a permission",
			call: func(ctx context.Context, simctl *Simctl) error {
				return simctl.SetPermission(ctx, "com.example.a", "photos", "unset")
			},
			want: []string{"simctl", "privacy", udid, "reset", "photos", "com.example.a"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingRunner{}
			if err := test.call(context.Background(), NewSimctl(udid, runner)); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("commands run = %d, want exactly one", len(runner.calls))
			}
			if !reflect.DeepEqual(runner.calls[0], append([]string{"xcrun"}, test.want...)) {
				t.Fatalf("argv = %v, want xcrun %v", runner.calls[0], test.want)
			}
		})
	}
}

func TestSetPermissionRejectsAGrantOutsideTheExactSet(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	err := NewSimctl("UDID-1", runner).SetPermission(context.Background(), "com.example.a", "camera", "maybe")
	if err == nil {
		t.Fatal("SetPermission() accepted an unknown grant; want a refusal")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran %v; an unknown grant must not reach the device", runner.calls)
	}
}

func TestScreenshotWritesToTheGivenPath(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	if err := NewSimctl("UDID-1", runner).Screenshot(context.Background(), "/tmp/shot.png"); err != nil {
		t.Fatalf("Screenshot() error = %v", err)
	}
	want := []string{"xcrun", "simctl", "io", "UDID-1", "screenshot", "/tmp/shot.png"}
	if !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("argv = %v, want %v", runner.calls[0], want)
	}
}

func TestListDevicesParsesTheJSONInventory(t *testing.T) {
	t.Parallel()

	inventory := map[string]any{"devices": map[string]any{
		"com.apple.CoreSimulator.SimRuntime.iOS-26-2": []any{
			map[string]any{"udid": "A", "name": "iPhone 17 Pro", "state": "Booted", "isAvailable": true},
			map[string]any{"udid": "B", "name": "iPhone 17", "state": "Shutdown", "isAvailable": true},
			map[string]any{"udid": "C", "name": "Broken", "state": "Shutdown", "isAvailable": false},
		},
	}}
	encoded, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: encoded}

	devices, err := NewSimctl("", runner).ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	want := []Device{
		{UDID: "A", Name: "iPhone 17 Pro", State: "Booted", Runtime: "com.apple.CoreSimulator.SimRuntime.iOS-26-2", Available: true},
		{UDID: "B", Name: "iPhone 17", State: "Shutdown", Runtime: "com.apple.CoreSimulator.SimRuntime.iOS-26-2", Available: true},
	}
	if !reflect.DeepEqual(devices, want) {
		t.Fatalf("devices = %#v, want %#v", devices, want)
	}
	if got := runner.calls[0]; !reflect.DeepEqual(got, []string{"xcrun", "simctl", "list", "devices", "-j"}) {
		t.Fatalf("argv = %v, want the JSON device listing", got)
	}
}

func TestSimctlPropagatesTheRunnerFailureWithItsOutput(t *testing.T) {
	t.Parallel()

	// simctl reports why it refused on its own output, so dropping that output
	// would turn a diagnosable failure into a bare exit code.
	sentinel := errors.New("exit status 1")
	runner := &recordingRunner{err: sentinel, output: []byte("Uninstall prohibited.")}
	err := NewSimctl("UDID-1", runner).Uninstall(context.Background(), "com.apple.Settings")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %T %v, want the exact runner cause", err, err)
	}
	if !strings.Contains(err.Error(), "Uninstall prohibited.") {
		t.Fatalf("error = %v, want it to carry simctl's own output", err)
	}
}

func TestSimctlRequiresAUDIDForDeviceScopedCommands(t *testing.T) {
	t.Parallel()

	// Every device-scoped command puts the udid in argv. An empty one would
	// silently shift the arguments and address the wrong thing.
	runner := &recordingRunner{}
	if err := NewSimctl("", runner).Boot(context.Background()); err == nil {
		t.Fatal("Boot() succeeded without a udid; want a refusal")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran %v without a udid", runner.calls)
	}
}

type recordingRunner struct {
	calls  [][]string
	output []byte
	err    error
}

func (runner *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, append([]string{name}, args...))
	return runner.output, runner.err
}

// launchApp stops the app first (that is what stopApp: true means), the stop
// goes to `simctl terminate`, and simctl exits 3 with "found nothing to
// terminate" when the app was not running. That state already satisfies the
// stop request.
//
// The simctl output has this form:
//
//	An error was encountered processing the command (domain=NSPOSIXErrorDomain,
//	code=3): Simulator device failed to terminate com.apple.Settings.
//	found nothing to terminate

func TestTerminatingAnAppThatIsNotRunningSucceeds(t *testing.T) {
	t.Parallel()

	// Terminate's caller wants the app not running. It already is not running.
	// Reporting a failure for a goal that is already met is what broke every
	// launchApp against a real device.
	runner := &recordingRunner{
		output: []byte("An error was encountered processing the command " +
			"(domain=NSPOSIXErrorDomain, code=3):\n" +
			"Simulator device failed to terminate com.apple.Settings.\n" +
			"found nothing to terminate\n"),
		err: errors.New("exit status 3"),
	}
	if err := NewSimctl("UDID-1", runner).Terminate(
		context.Background(), "com.apple.Settings"); err != nil {
		t.Fatalf("Terminate() error = %v, want nil for an app that is not running", err)
	}
}

func TestTerminateStillReportsARealFailure(t *testing.T) {
	t.Parallel()

	// The control. Swallowing every terminate failure would satisfy the test
	// above and hide a wrong udid, a shut-down device, or a missing simctl —
	// each of which is a setup problem the operator has to see.
	runner := &recordingRunner{
		output: []byte("Invalid device: UDID-1"),
		err:    errors.New("exit status 164"),
	}
	err := NewSimctl("UDID-1", runner).Terminate(context.Background(), "com.example.a")
	if err == nil {
		t.Fatal("Terminate() accepted a failure that was not about a missing process")
	}
	if !strings.Contains(err.Error(), "Invalid device") {
		t.Fatalf("error = %q, want simctl's own explanation carried through", err)
	}
}
