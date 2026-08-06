package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/android"
	"github.com/nohavewho/flowbaton/internal/ios"
)

// list-devices is the operator diagnostic named in G006: "what can I target?"
// It leans on the listing surfaces both drivers already have — simctl for iOS,
// adb for Android — rather than re-deriving either.

func listDevicesWith(
	iosDevices []ios.Device, iosErr error,
	androidDevices []android.Device, androidErr error,
) ListDevicesRunner {
	return ListDevicesRunner{
		IOS:     func(context.Context) ([]ios.Device, error) { return iosDevices, iosErr },
		Android: func(context.Context) ([]android.Device, error) { return androidDevices, androidErr },
	}
}

func runListDevices(t *testing.T, runner ListDevicesRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestListDevicesShowsBothPlatformsByDefault(t *testing.T) {
	t.Parallel()

	runner := listDevicesWith(
		[]ios.Device{{UDID: "AAAA", Name: "iPhone 17 Pro", State: "Booted", Runtime: "iOS 26.2"}},
		nil,
		[]android.Device{{Serial: "emulator-5554", State: "device", Model: "sdk_gphone64_arm64"}},
		nil,
	)
	stdout, _, code := runListDevices(t, runner)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	// Both a simulator and an adb device must appear, each tagged with its
	// platform so the id is unambiguous — a udid and a serial can look alike.
	for _, want := range []string{"ios", "AAAA", "iPhone 17 Pro", "android", "emulator-5554"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q\n%s", want, stdout)
		}
	}
}

func TestListDevicesFiltersToTheRequestedPlatform(t *testing.T) {
	t.Parallel()

	runner := listDevicesWith(
		[]ios.Device{{UDID: "AAAA", Name: "iPhone", State: "Booted"}}, nil,
		[]android.Device{{Serial: "emulator-5554", State: "device"}}, nil,
	)
	stdout, _, code := runListDevices(t, runner, "-p", "ios")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "AAAA") {
		t.Fatalf("the iOS device was not listed\n%s", stdout)
	}
	if strings.Contains(stdout, "emulator-5554") {
		t.Fatalf("android was listed under -p ios\n%s", stdout)
	}
}

func TestListDevicesReportsAnEmptyInventoryHonestly(t *testing.T) {
	t.Parallel()

	runner := listDevicesWith(nil, nil, nil, nil)
	stdout, stderr, code := runListDevices(t, runner)
	// Zero devices is a valid answer, not a failure. But silence would read as
	// a broken command, so it says so.
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout+stderr, "no devices") {
		t.Fatalf("an empty inventory said nothing\nstdout: %q\nstderr: %q", stdout, stderr)
	}
}

func TestListDevicesRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runListDevices(t, listDevicesWith(nil, nil, nil, nil), "-p", "windows")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "windows") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}

func TestListDevicesRefusesStrayArguments(t *testing.T) {
	t.Parallel()

	_, _, code := runListDevices(t, listDevicesWith(nil, nil, nil, nil), "extra")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
}

func TestListDevicesFailsWhenTheRequestedPlatformsToolErrors(t *testing.T) {
	t.Parallel()

	// -p android names the platform, so adb being broken is a failure the
	// operator asked to hear about.
	_, stderr, code := runListDevices(t,
		listDevicesWith(nil, nil, nil, errors.New("adb: command not found")),
		"-p", "android")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "adb") {
		t.Fatalf("the failure did not carry the tool error: %q", stderr)
	}
}

func TestListDevicesSkipsAnAbsentToolWhenListingEverything(t *testing.T) {
	t.Parallel()

	// The default (both) on a machine with only Xcode: adb is absent. The
	// simulators must still list, with the adb trouble noted rather than fatal —
	// otherwise the command is useless exactly where it is most wanted.
	runner := listDevicesWith(
		[]ios.Device{{UDID: "AAAA", Name: "iPhone", State: "Booted"}}, nil,
		nil, errors.New("exec: adb: not found"),
	)
	stdout, stderr, code := runListDevices(t, runner)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (a missing adb must not sink a simulator listing)", code, ExitOK)
	}
	if !strings.Contains(stdout, "AAAA") {
		t.Fatalf("the simulator was not listed despite adb being the broken half\n%s", stdout)
	}
	if !strings.Contains(stderr, "adb") {
		t.Fatalf("the adb trouble was swallowed silently: %q", stderr)
	}
}

// `flowbaton list-devices --platform=<platform>` accepts android, ios, and web.
// The web branch reports the Web/CDP driver's pseudo-device.
//
// specs/02-device-drivers.md:53 calls it a "web pseudo-device": there is no
// device to enumerate, so what is worth reporting is whether the browser the
// driver would launch is actually present.
func TestListDevicesReportsTheWebPseudoDevice(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := listDevicesWith(nil, nil, nil, nil)
	if code := runner.Run(context.Background(), []string{"--platform", "web"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	line := stdout.String()
	if !strings.HasPrefix(line, "web\t") {
		t.Fatalf("stdout = %q, want a web row", line)
	}
	// The other platforms must not be listed: an explicit filter is a filter.
	if strings.Contains(line, "android\t") || strings.Contains(line, "ios\t") {
		t.Fatalf("stdout = %q, want only the web row", line)
	}
}

// The control: with no filter, web is listed alongside the others rather than
// instead of them.
func TestListDevicesIncludesWebWhenNoPlatformIsNamed(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner := listDevicesWith(nil, nil, nil, nil)
	if code := runner.Run(context.Background(), nil, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "web\t") {
		t.Fatalf("stdout = %q, want a web row", stdout.String())
	}
}

// "no devices found" contradicts the row printed right above it when the
// operator asked only for web: the pseudo-device IS the answer to that
// question. The note belongs to the platforms that enumerate real hardware.
func TestListDevicesDoesNotClaimEmptinessWhenOnlyWebWasAskedFor(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runListDevices(t, listDevicesWith(nil, nil, nil, nil), "-p", "web")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if strings.Contains(stdout+stderr, "no devices") {
		t.Fatalf("a web-only listing claimed there was nothing\nstdout: %q\nstderr: %q", stdout, stderr)
	}
}
