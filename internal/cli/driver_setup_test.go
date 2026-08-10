package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// driver-setup installs the selected platform driver from the attested release.

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

func TestDriverSetupRefusesTheUnsupportedAppleTeamID(t *testing.T) {
	t.Parallel()

	calls, runner := driverSetupRecording(nil)
	_, stderr, code := runDriverSetup(t, runner, "--apple-team-id", "ABCDE12345")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if len(*calls) != 0 {
		t.Fatalf("build calls = %#v, want none", *calls)
	}
	if !strings.Contains(stderr, "signed iOS Simulator") {
		t.Fatalf("stderr = %q, want signed-driver explanation", stderr)
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
