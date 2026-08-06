package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// start-device starts a target device (spec 03, spec 02 lines 81-83): iOS boots
// (or creates then boots) a simulator via simctl; Android launches (or creates
// then launches) an AVD via emulator/avdmanager. Every external tool sits behind
// an injectable func field so these run with no device.

type startDeviceCalls struct {
	booted     []string
	listedAVDs int
	launched   []string
	createdSim int
	createdAVD int
}

func recordingStartDevice(calls *startDeviceCalls) StartDeviceRunner {
	return StartDeviceRunner{
		Boot: func(_ context.Context, platform, udid string) error {
			calls.booted = append(calls.booted, platform+":"+udid)
			return nil
		},
		ListAVDs: func(context.Context) ([]string, error) {
			calls.listedAVDs++
			return []string{"pixel_6", "pixel_7"}, nil
		},
		LaunchAVD: func(_ context.Context, avd string) error {
			calls.launched = append(calls.launched, avd)
			return nil
		},
		CreateSim: func(context.Context, string, string) (string, error) {
			calls.createdSim++
			return "NEWSIM", nil
		},
		CreateAVD: func(context.Context, string, string) (string, error) {
			calls.createdAVD++
			return "flowbaton_avd", nil
		},
	}
}

func runStartDevice(t *testing.T, runner StartDeviceRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestStartDeviceBootsTheNamedSimulator(t *testing.T) {
	t.Parallel()

	var calls startDeviceCalls
	stdout, stderr, code := runStartDevice(t, recordingStartDevice(&calls), "-p", "ios", "--device", "AAAA")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if len(calls.booted) != 1 || calls.booted[0] != "ios:AAAA" {
		t.Fatalf("boot calls = %#v, want one ios:AAAA", calls.booted)
	}
	if !strings.Contains(stdout, "AAAA") {
		t.Fatalf("stdout did not name the booted device: %q", stdout)
	}
}

func TestStartDeviceRequiresAPlatform(t *testing.T) {
	t.Parallel()

	var calls startDeviceCalls
	_, stderr, code := runStartDevice(t, recordingStartDevice(&calls), "--device", "AAAA")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if len(calls.booted) != 0 {
		t.Fatalf("booted despite no platform: %#v", calls.booted)
	}
	if !strings.Contains(stderr, "platform") {
		t.Fatalf("the refusal did not mention platform: %q", stderr)
	}
}

func TestStartDeviceRequiresADeviceForIOS(t *testing.T) {
	t.Parallel()

	// iOS boots an existing simulator by udid; without --force-create it needs a
	// target. (Android can discover its AVD, iOS cannot pick one for you here.)
	var calls startDeviceCalls
	_, stderr, code := runStartDevice(t, recordingStartDevice(&calls), "-p", "ios")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if len(calls.booted) != 0 {
		t.Fatalf("booted despite no udid: %#v", calls.booted)
	}
	if !strings.Contains(stderr, "device") {
		t.Fatalf("the refusal did not mention the missing device: %q", stderr)
	}
}

func TestStartDeviceForceCreateBuildsThenBootsAnIOSSimulator(t *testing.T) {
	t.Parallel()

	var calls startDeviceCalls
	stdout, stderr, code := runStartDevice(t, recordingStartDevice(&calls),
		"-p", "ios", "--force-create", "--os-version", "17.2", "--device-locale", "de_DE")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if calls.createdSim != 1 {
		t.Fatalf("create-sim calls = %d, want 1", calls.createdSim)
	}
	if len(calls.booted) != 1 || calls.booted[0] != "ios:NEWSIM" {
		t.Fatalf("boot calls = %#v, want one ios:NEWSIM", calls.booted)
	}
	if !strings.Contains(stdout, "NEWSIM") {
		t.Fatalf("stdout did not name the created device: %q", stdout)
	}
}

func TestStartDeviceLaunchesADiscoveredAndroidAVD(t *testing.T) {
	t.Parallel()

	// No --device: discover the AVDs and launch the first one.
	var calls startDeviceCalls
	stdout, stderr, code := runStartDevice(t, recordingStartDevice(&calls), "-p", "android")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if calls.listedAVDs != 1 {
		t.Fatalf("list-avds calls = %d, want 1", calls.listedAVDs)
	}
	if len(calls.launched) != 1 || calls.launched[0] != "pixel_6" {
		t.Fatalf("launched = %#v, want one pixel_6", calls.launched)
	}
	if !strings.Contains(stdout, "pixel_6") {
		t.Fatalf("stdout did not name the launched AVD: %q", stdout)
	}
}

func TestStartDeviceLaunchesANamedAndroidAVD(t *testing.T) {
	t.Parallel()

	// --device names the AVD directly; no discovery needed.
	var calls startDeviceCalls
	_, stderr, code := runStartDevice(t, recordingStartDevice(&calls), "-p", "android", "--device", "pixel_7")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if calls.listedAVDs != 0 {
		t.Fatalf("discovered AVDs despite a named device: %d", calls.listedAVDs)
	}
	if len(calls.launched) != 1 || calls.launched[0] != "pixel_7" {
		t.Fatalf("launched = %#v, want one pixel_7", calls.launched)
	}
}

func TestStartDeviceForceCreateBuildsThenLaunchesAnAndroidAVD(t *testing.T) {
	t.Parallel()

	var calls startDeviceCalls
	_, stderr, code := runStartDevice(t, recordingStartDevice(&calls),
		"-p", "android", "--force-create", "--os-version", "34")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if calls.createdAVD != 1 {
		t.Fatalf("create-avd calls = %d, want 1", calls.createdAVD)
	}
	if calls.listedAVDs != 0 {
		t.Fatalf("discovered AVDs despite force-create: %d", calls.listedAVDs)
	}
	if len(calls.launched) != 1 || calls.launched[0] != "flowbaton_avd" {
		t.Fatalf("launched = %#v, want one flowbaton_avd", calls.launched)
	}
}

func TestStartDeviceReportsNoAndroidAVDsToLaunch(t *testing.T) {
	t.Parallel()

	runner := recordingStartDevice(&startDeviceCalls{})
	runner.ListAVDs = func(context.Context) ([]string, error) { return nil, nil }
	_, stderr, code := runStartDevice(t, runner, "-p", "android")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "no") || !strings.Contains(stderr, "AVD") {
		t.Fatalf("the failure was not clear about the empty AVD list: %q", stderr)
	}
}

func TestStartDeviceTreatsAnAlreadyBootedSimulatorAsSuccess(t *testing.T) {
	t.Parallel()

	// simctl exits non-zero when the device is already Booted; the operator's
	// goal (a running simulator) is already met, so this is success, not exit 1.
	runner := recordingStartDevice(&startDeviceCalls{})
	runner.Boot = func(context.Context, string, string) error {
		return errors.New("simctl boot: exit status 149: Unable to boot device in current state: Booted")
	}
	stdout, stderr, code := runStartDevice(t, runner, "-p", "ios", "--device", "AAAA")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if !strings.Contains(stdout, "AAAA") {
		t.Fatalf("stdout did not acknowledge the already-booted device: %q", stdout)
	}
}

func TestStartDeviceReportsAGenuineBootFailure(t *testing.T) {
	t.Parallel()

	runner := recordingStartDevice(&startDeviceCalls{})
	runner.Boot = func(context.Context, string, string) error {
		return errors.New("simctl boot: Invalid device: AAAA")
	}
	_, stderr, code := runStartDevice(t, runner, "-p", "ios", "--device", "AAAA")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "Invalid device") {
		t.Fatalf("the failure did not carry the simctl error: %q", stderr)
	}
}

func TestStartDeviceRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runStartDevice(t, StartDeviceRunner{}, "-p", "web", "--device", "AAAA")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "web") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}
