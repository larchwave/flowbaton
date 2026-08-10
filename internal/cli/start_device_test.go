package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	ready      []string
	created    []deviceCreateOptions
	locales    []string
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
		LaunchAVD: func(_ context.Context, avd, locale string) error {
			calls.launched = append(calls.launched, avd)
			calls.locales = append(calls.locales, locale)
			return nil
		},
		CreateSim: func(_ context.Context, options deviceCreateOptions) (string, error) {
			calls.createdSim++
			calls.created = append(calls.created, options)
			return "NEWSIM", nil
		},
		CreateAVD: func(_ context.Context, options deviceCreateOptions) (string, error) {
			calls.createdAVD++
			calls.created = append(calls.created, options)
			return "flowbaton_avd", nil
		},
		WaitReady: func(_ context.Context, platform, target string) error {
			calls.ready = append(calls.ready, platform+":"+target)
			return nil
		},
		ConfigureLocale: func(context.Context, string, string, string) error { return nil },
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
		"-p", "ios", "--force-create", "--os-version", "17.2", "--device-locale", "de_DE",
		"--device-model", "iPhone 16")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if calls.createdSim != 1 {
		t.Fatalf("create-sim calls = %d, want 1", calls.createdSim)
	}
	if len(calls.booted) != 1 || calls.booted[0] != "ios:NEWSIM" {
		t.Fatalf("boot calls = %#v, want one ios:NEWSIM", calls.booted)
	}
	if len(calls.created) != 1 || calls.created[0].Model != "iPhone 16" || calls.created[0].Locale != "de_DE" {
		t.Fatalf("create options = %#v", calls.created)
	}
	if len(calls.ready) != 1 || calls.ready[0] != "ios:NEWSIM" {
		t.Fatalf("ready calls = %#v, want ios:NEWSIM", calls.ready)
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
		"-p", "android", "--force-create", "--os-version", "34",
		"--device-locale", "fr_CA", "--device-model", "pixel_8",
		"--system-image", "system-images;android-34;google_apis;arm64-v8a")
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
	if len(calls.created) != 1 || calls.created[0].Model != "pixel_8" ||
		calls.created[0].SystemImage != "system-images;android-34;google_apis;arm64-v8a" {
		t.Fatalf("create options = %#v", calls.created)
	}
	if len(calls.locales) != 1 || calls.locales[0] != "fr_CA" {
		t.Fatalf("launch locales = %#v", calls.locales)
	}
	if len(calls.ready) != 1 || calls.ready[0] != "android:flowbaton_avd" {
		t.Fatalf("ready calls = %#v", calls.ready)
	}
}

func TestStartDeviceDoesNotReportSuccessBeforeAndroidIsReady(t *testing.T) {
	t.Parallel()

	runner := recordingStartDevice(&startDeviceCalls{})
	runner.WaitReady = func(context.Context, string, string) error {
		return errors.New("boot readiness timed out")
	}
	stdout, stderr, code := runStartDevice(t, runner, "-p", "android", "--device", "pixel_7")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if stdout != "" || !strings.Contains(stderr, "boot readiness timed out") {
		t.Fatalf("stdout = %q stderr = %q", stdout, stderr)
	}
}

func TestStartDeviceBoundsReadinessWaiting(t *testing.T) {
	t.Parallel()

	runner := recordingStartDevice(&startDeviceCalls{})
	runner.ReadyTimeout = 5 * time.Millisecond
	runner.WaitReady = func(ctx context.Context, _, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	_, stderr, code := runStartDevice(t, runner, "-p", "android", "--device", "pixel_7")
	if code != ExitFailure || !strings.Contains(stderr, "deadline exceeded") {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
}

func TestStartDeviceRejectsCreationOnlyFlagsWithoutForceCreate(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"-p", "android", "--device", "pixel", "--os-version", "34"},
		{"-p", "android", "--device", "pixel", "--device-model", "pixel_8"},
		{"-p", "android", "--device", "pixel", "--system-image", "image"},
	} {
		_, stderr, code := runStartDevice(t, recordingStartDevice(&startDeviceCalls{}), args...)
		if code != ExitInvalid || !strings.Contains(stderr, "--force-create") {
			t.Fatalf("args %v: exit=%d stderr=%q", args, code, stderr)
		}
	}
}

func TestAndroidCreationDefaultsAreHostAwareAndNamed(t *testing.T) {
	t.Parallel()

	options := defaultAndroidCreateOptions(deviceCreateOptions{})
	if options.Model == "" || options.OSVersion == "" || options.SystemImage == "" || options.Name == "" {
		t.Fatalf("defaults are incomplete: %+v", options)
	}
	if !strings.Contains(options.SystemImage, "android-"+options.OSVersion) {
		t.Fatalf("system image %q does not match API %q", options.SystemImage, options.OSVersion)
	}
	custom := defaultAndroidCreateOptions(deviceCreateOptions{
		SystemImage: "system-images;android-35;google_apis;x86_64",
	})
	if custom.OSVersion != "35" {
		t.Fatalf("custom image API = %q, want 35", custom.OSVersion)
	}
}

func TestInstalledSDKItemRequiresAnExactListEntry(t *testing.T) {
	t.Parallel()

	listing := "Installed packages:\n  Path                                      | Version\n  system-images;android-34;google_apis;x86_64 | 12\n  pixel_8                                    | device\n"
	if !sdkListContains(listing, "system-images;android-34;google_apis;x86_64") {
		t.Fatal("exact installed package was not found")
	}
	if sdkListContains(listing, "system-images;android-34;google_apis;x86") {
		t.Fatal("a package prefix was accepted as an installed package")
	}
}

func TestRealCreateAVDChecksInstalledImageAndModelBeforeCreation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses small POSIX command fixtures")
	}

	sdk := t.TempDir()
	bin := filepath.Join(sdk, "cmdline-tools", "latest", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	image := "system-images;android-34;google_apis;x86_64"
	sdkmanager := filepath.Join(bin, "sdkmanager")
	if err := os.WriteFile(sdkmanager, []byte("#!/bin/sh\nprintf '%s | 1\\n' '"+image+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	captured := filepath.Join(t.TempDir(), "created.txt")
	avdmanager := filepath.Join(bin, "avdmanager")
	script := "#!/bin/sh\nif [ \"$1 $2 $3\" = \"list device -c\" ]; then\n  printf 'pixel_8\\n'\n  exit 0\nfi\nprintf '%s\\n' \"$*\" > \"$FLOWBATON_CAPTURE\"\n"
	if err := os.WriteFile(avdmanager, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("FLOWBATON_CAPTURE", captured)

	name, err := realCreateAVD(context.Background(), deviceCreateOptions{
		Name: "ci_pixel", OSVersion: "34", Model: "pixel_8", SystemImage: image,
	})
	if err != nil {
		t.Fatalf("realCreateAVD: %v", err)
	}
	if name != "ci_pixel" {
		t.Fatalf("name = %q", name)
	}
	command, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"create avd", "-n ci_pixel", "-k " + image, "-d pixel_8"} {
		if !strings.Contains(string(command), want) {
			t.Fatalf("create command %q missing %q", command, want)
		}
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
