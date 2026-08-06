package cli

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// driver-setup builds the selected platform driver. A simulator build needs no
// signing; --apple-team-id is passed through for iOS device builds. Android
// builds and installs its Gradle-produced APK pair.

type driverSetupCall struct {
	platform string
	teamID   string
}

func driverSetupRecording(err error) (*[]driverSetupCall, DriverSetupRunner) {
	var calls []driverSetupCall
	runner := DriverSetupRunner{
		Build: func(_ context.Context, platform, teamID string) error {
			calls = append(calls, driverSetupCall{platform: platform, teamID: teamID})
			return err
		},
	}
	return &calls, runner
}

func runDriverSetup(t *testing.T, runner DriverSetupRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestDriverSetupBuildsIOSByDefault(t *testing.T) {
	t.Parallel()

	// Without -p, driver-setup selects iOS.
	calls, runner := driverSetupRecording(nil)
	_, stderr, code := runDriverSetup(t, runner)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if len(*calls) != 1 || (*calls)[0].platform != "ios" {
		t.Fatalf("build calls = %#v, want one ios build", *calls)
	}
}

func TestDriverSetupPassesTheAppleTeamID(t *testing.T) {
	t.Parallel()

	calls, runner := driverSetupRecording(nil)
	_, _, code := runDriverSetup(t, runner, "--apple-team-id", "ABCDE12345")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if len(*calls) != 1 || (*calls)[0].teamID != "ABCDE12345" {
		t.Fatalf("build calls = %#v, want the team id passed through", *calls)
	}
}

// iOS is the default setup target, and an explicit Android target builds and
// installs the agent APK pair.
func TestDriverSetupBuildsTheAndroidAgent(t *testing.T) {
	t.Parallel()

	calls, runner := driverSetupRecording(nil)
	stdout, stderr, code := runDriverSetup(t, runner, "-p", "android")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if len(*calls) != 1 || (*calls)[0].platform != "android" {
		t.Fatalf("build calls = %#v, want one android build", *calls)
	}
	if !strings.Contains(stdout, "android") {
		t.Fatalf("the report did not name the platform built: %q", stdout)
	}
}

// The report has to name the platform, or `driver-setup -p android` printing
// "ios driver built" tells the operator the wrong thing was built.
func TestDriverSetupNamesTheIOSPlatformItBuilt(t *testing.T) {
	t.Parallel()

	_, runner := driverSetupRecording(nil)
	stdout, _, code := runDriverSetup(t, runner)
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "ios") {
		t.Fatalf("the report did not name the platform built: %q", stdout)
	}
}

// --apple-team-id is an iOS signing detail; accepting it for android would
// promise a signing step there is none of.
func TestDriverSetupRefusesAnAppleTeamIDForAndroid(t *testing.T) {
	t.Parallel()

	calls, runner := driverSetupRecording(nil)
	_, stderr, code := runDriverSetup(t, runner, "-p", "android", "--apple-team-id", "ABCDE12345")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if len(*calls) != 0 {
		t.Fatalf("built despite the bad flag combination: %#v", *calls)
	}
	if !strings.Contains(stderr, "--apple-team-id") {
		t.Fatalf("the refusal did not name the flag: %q", stderr)
	}
}

// The Android build installs both APKs where runtime lookup reads them.
func TestTheAndroidBuildInstallsThePairWhereTheRunLooks(t *testing.T) {
	source := t.TempDir()
	app := filepath.Join(source, "agent-debug.apk")
	test := filepath.Join(source, "agent-debug-androidTest.apk")
	writeAPK(t, app)
	writeAPK(t, test)
	directory := agentHome(t)

	if err := installAndroidAgent(app, test); err != nil {
		t.Fatalf("installAndroidAgent() error = %v", err)
	}
	apks, err := androidAgentAPKs()
	if err != nil {
		t.Fatalf("androidAgentAPKs() error = %v", err)
	}
	if apks == nil {
		t.Fatalf("the build left nothing in %s, so -p android still needs adb by hand", directory)
	}
}

// A Gradle build that produced nothing must say so here rather than leave the
// last build in place and let the run drive a stale agent.
func TestTheAndroidBuildRefusesAMissingOutput(t *testing.T) {
	source := t.TempDir()
	app := filepath.Join(source, "agent-debug.apk")
	writeAPK(t, app)
	agentHome(t)

	err := installAndroidAgent(app, filepath.Join(source, "absent.apk"))
	if err == nil {
		t.Fatal("a missing build output was installed as if it were there")
	}
	if !strings.Contains(err.Error(), "absent.apk") {
		t.Fatalf("the error did not name the missing output: %v", err)
	}
	// And it left nothing behind: half an install is refused by
	// androidAgentAPKs, which would strand the operator on an error instead of
	// the working operator-started mode.
	if _, err := androidAgentAPKs(); err != nil {
		t.Fatalf("the failed build left a half install behind: %v", err)
	}
}

func TestDriverSetupRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runDriverSetup(t, DriverSetupRunner{}, "-p", "web")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "web") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}

func TestDriverSetupReportsABuildFailure(t *testing.T) {
	t.Parallel()

	_, stderr, code := runDriverSetup(t,
		DriverSetupRunner{Build: func(context.Context, string, string) error {
			return errors.New("xcodebuild: scheme not found")
		}})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "scheme not found") {
		t.Fatalf("the failure did not carry the xcodebuild error: %q", stderr)
	}
}

func TestIOSDriverBuildRequiresXcodeGen(t *testing.T) {
	t.Parallel()

	runCalled := false
	err := generateIOSProject(
		context.Background(),
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(context.Context, string, ...string) ([]byte, error) {
			runCalled = true
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("generateIOSProject() accepted a missing generator")
	}
	if runCalled {
		t.Fatal("generateIOSProject() tried to execute a missing generator")
	}
	for _, want := range []string{"xcodegen 2.44.1 is required", "install that release", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("generateIOSProject() error = %q, want %q", err, want)
		}
	}
}

func TestIOSDriverBuildGeneratesTheProjectFromTheTrackedSpec(t *testing.T) {
	t.Parallel()

	type invocation struct {
		name string
		args []string
	}
	var calls []invocation
	err := generateIOSProject(
		context.Background(),
		func(name string) (string, error) {
			if name != iosProjectGenerator {
				t.Fatalf("find(%q), want %q", name, iosProjectGenerator)
			}
			return "/tools/xcodegen", nil
		},
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, invocation{name: name, args: append([]string(nil), args...)})
			if reflect.DeepEqual(args, []string{"--version"}) {
				return []byte("Version: " + iosProjectGeneratorVersion + "\n"), nil
			}
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("generateIOSProject() error = %v", err)
	}
	want := []invocation{{
		name: "/tools/xcodegen",
		args: []string{"--version"},
	}, {
		name: "/tools/xcodegen",
		args: []string{"generate", "--spec", iosRunnerSpec, "--project", "drivers/ios"},
	}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("generator calls = %#v, want %#v", calls, want)
	}
}

func TestIOSDriverBuildRejectsAnotherXcodeGenRelease(t *testing.T) {
	t.Parallel()

	err := generateIOSProject(
		context.Background(),
		func(string) (string, error) { return "/tools/xcodegen", nil },
		func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if reflect.DeepEqual(args, []string{"--version"}) {
				return []byte("Version: 2.45.0\n"), nil
			}
			t.Fatalf("unexpected generator call after version refusal: %v", args)
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("generateIOSProject() accepted another generator release")
	}
	for _, want := range []string{iosProjectGeneratorVersion, "2.45.0", "deterministic", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("generateIOSProject() error = %q, want %q", err, want)
		}
	}
}
