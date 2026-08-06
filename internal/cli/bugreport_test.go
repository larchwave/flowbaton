package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// bugreport collects a device diagnostic bundle (spec 03): adb supplies Android
// bundles and simctl supplies iOS diagnostics.

type bugreportCall struct {
	serial string
	output string
}

func bugreportRunnerRecording(serial string, resolveErr error) (*[]bugreportCall, BugreportRunner) {
	var calls []bugreportCall
	runner := BugreportRunner{
		Collect: func(_ context.Context, s, output string) error {
			calls = append(calls, bugreportCall{serial: s, output: output})
			return nil
		},
		ResolveSerial: func() (string, error) { return serial, resolveErr },
	}
	return &calls, runner
}

func runBugreport(t *testing.T, runner BugreportRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestBugreportCollectsToTheGivenPath(t *testing.T) {
	t.Parallel()

	calls, runner := bugreportRunnerRecording("emulator-5554", nil)
	_, stderr, code := runBugreport(t, runner, "-p", "android", "--device", "emulator-5554", "--output", "out.zip")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if len(*calls) != 1 || (*calls)[0].serial != "emulator-5554" || (*calls)[0].output != "out.zip" {
		t.Fatalf("collect calls = %#v, want one emulator-5554 -> out.zip", *calls)
	}
}

func TestBugreportResolvesTheSerialWhenDeviceOmitted(t *testing.T) {
	t.Parallel()

	// With no --device the single attached device is used, same rule as the
	// test runner's Android selection.
	calls, runner := bugreportRunnerRecording("emulator-5554", nil)
	_, stderr, code := runBugreport(t, runner, "-p", "android", "--output", "out.zip")
	if code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr)
	}
	if len(*calls) != 1 || (*calls)[0].serial != "emulator-5554" {
		t.Fatalf("collect calls = %#v, want the resolved serial", *calls)
	}
}

func TestBugreportDefaultsTheOutputPath(t *testing.T) {
	t.Parallel()

	calls, runner := bugreportRunnerRecording("emulator-5554", nil)
	_, _, code := runBugreport(t, runner, "-p", "android", "--device", "emulator-5554")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if len(*calls) != 1 || (*calls)[0].output == "" {
		t.Fatalf("collect calls = %#v, want a defaulted output path", *calls)
	}
}

func TestBugreportCollectsAnIOSDiagnostic(t *testing.T) {
	t.Parallel()

	var calls []bugreportCall
	runner := BugreportRunner{
		CollectIOS: func(_ context.Context, udid, output string) error {
			calls = append(calls, bugreportCall{serial: udid, output: output})
			return nil
		},
	}
	_, stderr, code := runBugreport(t, runner, "-p", "ios", "--device", "AAAA", "--output", "diag")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d\nstderr: %s", code, ExitOK, stderr)
	}
	if len(calls) != 1 || calls[0].serial != "AAAA" || calls[0].output != "diag" {
		t.Fatalf("collectIOS calls = %#v, want one AAAA -> diag", calls)
	}
}

func TestBugreportIOSRequiresADevice(t *testing.T) {
	t.Parallel()

	var calls []bugreportCall
	runner := BugreportRunner{
		CollectIOS: func(_ context.Context, udid, output string) error {
			calls = append(calls, bugreportCall{serial: udid, output: output})
			return nil
		},
	}
	// simctl has no authoritative single-device inventory, so iOS cannot
	// resolve a udid the way Android does: it must be named.
	_, stderr, code := runBugreport(t, runner, "-p", "ios", "--output", "diag")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if len(calls) != 0 {
		t.Fatalf("collected on iOS without a udid: %#v", calls)
	}
	if !strings.Contains(stderr, "udid") && !strings.Contains(stderr, "device") {
		t.Fatalf("the refusal did not ask for a device: %q", stderr)
	}
}

func TestBugreportReportsAnIOSCollectFailure(t *testing.T) {
	t.Parallel()

	runner := BugreportRunner{
		CollectIOS: func(_ context.Context, _, _ string) error {
			return errors.New("simctl: no such device")
		},
	}
	_, stderr, code := runBugreport(t, runner, "-p", "ios", "--device", "AAAA", "--output", "diag")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "no such device") {
		t.Fatalf("the failure did not carry the simctl error: %q", stderr)
	}
}

func TestBugreportRequiresAPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runBugreport(t, BugreportRunner{}, "--output", "out.zip")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "platform") {
		t.Fatalf("the refusal did not mention platform: %q", stderr)
	}
}

func TestBugreportRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runBugreport(t, BugreportRunner{}, "-p", "web")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "web") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}

func TestBugreportReportsACollectFailure(t *testing.T) {
	t.Parallel()

	runner := BugreportRunner{
		Collect: func(_ context.Context, _, _ string) error {
			return errors.New("adb: device offline")
		},
		ResolveSerial: func() (string, error) { return "emulator-5554", nil },
	}
	_, stderr, code := runBugreport(t, runner, "-p", "android", "--device", "emulator-5554", "--output", "out.zip")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "device offline") {
		t.Fatalf("the failure did not carry the adb error: %q", stderr)
	}
}

func TestBugreportReportsAResolveFailure(t *testing.T) {
	t.Parallel()

	calls, runner := bugreportRunnerRecording("", errors.New("no android devices are connected"))
	_, stderr, code := runBugreport(t, runner, "-p", "android", "--output", "out.zip")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if len(*calls) != 0 {
		t.Fatalf("collected despite an unresolved serial: %#v", *calls)
	}
	if !strings.Contains(stderr, "no android devices") {
		t.Fatalf("the failure did not carry the resolve error: %q", stderr)
	}
}
