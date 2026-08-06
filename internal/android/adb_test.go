package android

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// The command runner is injected, so every expectation here is about the
// exact argv handed to the OS. That is the part worth pinning: a wrong flag
// is a wrong device operation.

// recordingRunner records every argv and answers with a scripted response.
// The mutex is for the managed-agent path, whose `am instrument` runs on its
// own goroutine while the test reads what was recorded.
type recordingRunner struct {
	mu      sync.Mutex
	calls   [][]string
	output  []byte
	err     error
	respond func(args []string) ([]byte, error)
}

func (runner *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.mu.Lock()
	runner.calls = append(runner.calls, append([]string{name}, args...))
	runner.mu.Unlock()
	if runner.respond != nil {
		return runner.respond(args)
	}
	return runner.output, runner.err
}

// recorded snapshots the calls so far.
func (runner *recordingRunner) recorded() [][]string {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([][]string(nil), runner.calls...)
}

func TestAdbBuildsTheExactCommandLine(t *testing.T) {
	t.Parallel()

	const serial = "emulator-5554"
	for _, test := range []struct {
		name string
		call func(context.Context, *Adb) error
		want []string
	}{
		{
			name: "forward add",
			call: func(ctx context.Context, adb *Adb) error { return adb.ForwardAdd(ctx, 41001, 7001) },
			want: []string{"forward", "tcp:41001", "tcp:7001"},
		},
		{
			name: "forward remove",
			call: func(ctx context.Context, adb *Adb) error { return adb.ForwardRemove(ctx, 41001) },
			want: []string{"forward", "--remove", "tcp:41001"},
		},
		{
			name: "force stop",
			call: func(ctx context.Context, adb *Adb) error { return adb.ForceStop(ctx, "com.example.a") },
			want: []string{"shell", "am", "force-stop", "com.example.a"},
		},
		{
			name: "kill",
			call: func(ctx context.Context, adb *Adb) error { return adb.Kill(ctx, "com.example.a") },
			want: []string{"shell", "am", "kill", "com.example.a"},
		},
		{
			name: "clear package data",
			call: func(ctx context.Context, adb *Adb) error { return adb.ClearPackageData(ctx, "com.example.a") },
			want: []string{"shell", "pm", "clear", "com.example.a"},
		},
		{
			name: "grant a permission",
			call: func(ctx context.Context, adb *Adb) error {
				return adb.GrantPermission(ctx, "com.example.a", "android.permission.CAMERA")
			},
			want: []string{"shell", "pm", "grant", "com.example.a", "android.permission.CAMERA"},
		},
		{
			name: "revoke a permission",
			call: func(ctx context.Context, adb *Adb) error {
				return adb.RevokePermission(ctx, "com.example.a", "android.permission.CAMERA")
			},
			want: []string{"shell", "pm", "revoke", "com.example.a", "android.permission.CAMERA"},
		},
		{
			name: "open a link",
			call: func(ctx context.Context, adb *Adb) error {
				return adb.OpenLink(ctx, "https://example.invalid/x", false)
			},
			want: []string{"shell", "am", "start", "-a", "android.intent.action.VIEW",
				"-d", "https://example.invalid/x"},
		},
		{
			name: "open a link forcing chrome",
			call: func(ctx context.Context, adb *Adb) error {
				return adb.OpenLink(ctx, "https://example.invalid/x", true)
			},
			// The positional package is the INTENT spec's own selector
			// ([<URI> | <PACKAGE> | <COMPONENT>], am's usage on API 34).
			want: []string{"shell", "am", "start", "-a", "android.intent.action.VIEW",
				"-d", "https://example.invalid/x", "com.android.chrome"},
		},
		{
			name: "keyevent",
			call: func(ctx context.Context, adb *Adb) error { return adb.Keyevent(ctx, "KEYCODE_BACK") },
			want: []string{"shell", "input", "keyevent", "KEYCODE_BACK"},
		},
		{
			name: "swipe",
			call: func(ctx context.Context, adb *Adb) error { return adb.Swipe(ctx, 540, 1800, 540, 240, 500) },
			want: []string{"shell", "input", "swipe", "540", "1800", "540", "240", "500"},
		},
		{
			name: "put a setting",
			call: func(ctx context.Context, adb *Adb) error {
				return adb.PutSetting(ctx, "system", "user_rotation", "1")
			},
			want: []string{"shell", "settings", "put", "system", "user_rotation", "1"},
		},
		{
			name: "enable airplane mode",
			call: func(ctx context.Context, adb *Adb) error { return adb.SetAirplaneMode(ctx, true) },
			want: []string{"shell", "cmd", "connectivity", "airplane-mode", "enable"},
		},
		{
			name: "disable airplane mode",
			call: func(ctx context.Context, adb *Adb) error { return adb.SetAirplaneMode(ctx, false) },
			want: []string{"shell", "cmd", "connectivity", "airplane-mode", "disable"},
		},
		{
			name: "install replacing",
			call: func(ctx context.Context, adb *Adb) error { return adb.Install(ctx, "/tmp/agent.apk") },
			want: []string{"install", "-r", "/tmp/agent.apk"},
		},
		{
			name: "uninstall",
			call: func(ctx context.Context, adb *Adb) error { return adb.Uninstall(ctx, "com.example.a") },
			want: []string{"uninstall", "com.example.a"},
		},
		{
			// specs/02-device-drivers.md §2.2's launch line, with FlowBaton's
			// packages; -m unconditional because the agent's minSdk is 26.
			name: "instrument",
			call: func(ctx context.Context, adb *Adb) error {
				_, err := adb.Instrument(ctx, 7001)
				return err
			},
			want: []string{"shell", "am", "instrument", "-w", "-m",
				"-e", "debug", "false",
				"-e", "class", "dev.larchwave.flowbaton.FlowBatonDriverService#grpcServer",
				"-e", "port", "7001",
				"dev.larchwave.flowbaton.test/androidx.test.runner.AndroidJUnitRunner"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingRunner{}
			if err := test.call(context.Background(), NewAdb(serial, runner)); err != nil {
				t.Fatalf("%s error = %v", test.name, err)
			}
			if len(runner.calls) != 1 {
				t.Fatalf("commands run = %d, want exactly one", len(runner.calls))
			}
			want := append([]string{"-s", serial}, test.want...)
			if got := runner.calls[0][1:]; !reflect.DeepEqual(got, want) {
				t.Fatalf("argv = %v, want %v", got, want)
			}
		})
	}
}

func TestAdbExecutableFollowsAndroidHome(t *testing.T) {
	t.Setenv("ANDROID_HOME", "/opt/android")
	if got := adbExecutable(); got != "/opt/android/platform-tools/adb" {
		t.Fatalf("adbExecutable() = %q with ANDROID_HOME set", got)
	}
	t.Setenv("ANDROID_HOME", "")
	if got := adbExecutable(); got != "adb" {
		t.Fatalf("adbExecutable() = %q without ANDROID_HOME, want the PATH lookup", got)
	}
}

func TestAdbRequiresASerialForDeviceScopedCommands(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	err := NewAdb("", runner).ForceStop(context.Background(), "com.example.a")
	if err == nil {
		t.Fatal("a device-scoped command ran without a serial")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran %v; nothing must reach adb without a serial", runner.calls)
	}
}

func TestAdbPropagatesTheFailureWithItsOutput(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("Failed\n"), err: errors.New("exit status 1")}
	err := NewAdb("emulator-5554", runner).ClearPackageData(context.Background(), "com.missing")
	if err == nil {
		t.Fatal("a failed pm clear was reported as success")
	}
	// pm reports why it refused on its own output; discarding it turns a
	// diagnosable refusal into a bare exit code.
	if !strings.Contains(err.Error(), "Failed") {
		t.Fatalf("error = %q, want it to carry pm's own output", err)
	}
}

func TestOpenLinkReadsTheErrorOffTheOutput(t *testing.T) {
	t.Parallel()

	// `am start` may exit 0 and print "Error: Activity not started..." when
	// nothing resolves the intent.
	// Trusting the exit status alone would report that as success.
	runner := &recordingRunner{output: []byte(
		"Starting: Intent { act=android.intent.action.VIEW }\n" +
			"Error: Activity not started, unable to resolve Intent\n")}
	err := NewAdb("emulator-5554", runner).OpenLink(context.Background(), "https://example.invalid", false)
	if err == nil {
		t.Fatal("an unresolved intent was reported as an opened link")
	}
	if !strings.Contains(err.Error(), "Activity not started") {
		t.Fatalf("error = %q, want am's own diagnosis", err)
	}
}

func TestAirplaneModeParsesTheExactStates(t *testing.T) {
	t.Parallel()

	for output, want := range map[string]bool{"enabled\n": true, "disabled\n": false} {
		runner := &recordingRunner{output: []byte(output)}
		got, err := NewAdb("emulator-5554", runner).AirplaneModeEnabled(context.Background())
		if err != nil {
			t.Fatalf("AirplaneModeEnabled() error = %v", err)
		}
		if got != want {
			t.Fatalf("AirplaneModeEnabled() = %v for %q", got, output)
		}
	}

	runner := &recordingRunner{output: []byte("unknown\n")}
	if _, err := NewAdb("emulator-5554", runner).AirplaneModeEnabled(context.Background()); err == nil {
		t.Fatal("an unrecognized state was coerced to a boolean")
	}
}

func TestKeyboardShownReadsTheInputMethodState(t *testing.T) {
	t.Parallel()

	shown := &recordingRunner{output: []byte("  mSystemReady=true\n  mInputShown=true\n")}
	got, err := NewAdb("emulator-5554", shown).KeyboardShown(context.Background())
	if err != nil || !got {
		t.Fatalf("KeyboardShown() = %v, %v; want true", got, err)
	}
	hidden := &recordingRunner{output: []byte("  mInputShown=false\n")}
	got, err = NewAdb("emulator-5554", hidden).KeyboardShown(context.Background())
	if err != nil || got {
		t.Fatalf("KeyboardShown() = %v, %v; want false", got, err)
	}
}

func TestListDevicesParsesTheInventory(t *testing.T) {
	t.Parallel()

	// The exact shape a live adb 36 printed on this machine, plus the daemon
	// noise and the states that must be dropped.
	listing := "* daemon not running; starting now at tcp:5037\n" +
		"* daemon started successfully\n" +
		"List of devices attached\n" +
		"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1\n" +
		"R58M12ABCDE            unauthorized transport_id:2\n" +
		"emulator-5556          offline transport_id:3\n" +
		"\n"
	runner := &recordingRunner{output: []byte(listing)}
	devices, err := ListDevices(context.Background(), runner)
	if err != nil {
		t.Fatalf("ListDevices() error = %v", err)
	}
	want := []Device{{Serial: "emulator-5554", State: "device", Model: "sdk_gphone64_arm64"}}
	if !reflect.DeepEqual(devices, want) {
		t.Fatalf("devices = %#v, want %#v", devices, want)
	}
	if got := runner.calls[0][1:]; !reflect.DeepEqual(got, []string{"devices", "-l"}) {
		t.Fatalf("argv = %v, want [devices -l]", got)
	}
}

func TestRuntimePermissionsParsesTheDumpsysSections(t *testing.T) {
	t.Parallel()

	// Shape taken from a live `dumpsys package` on API 34: the section
	// repeats per user, entries carry granted= and flags, and unrelated
	// sections must not leak in, and a line under the section with no
	// granted= (a restricted permission) is not a changeable runtime one.
	dump := `Packages:
  Package [com.example.a] (1234abc):
    requested permissions:
      android.permission.CAMERA
      android.permission.INTERNET
    install permissions:
      android.permission.INTERNET: granted=true
    User 0: installed=true
      runtime permissions:
        android.permission.CAMERA: granted=false, flags=[ USER_SENSITIVE_WHEN_GRANTED]
        android.permission.RECORD_AUDIO: granted=true, flags=[ USER_SET]
    User 10: installed=true
      runtime permissions:
        android.permission.CAMERA: granted=false, flags=[]
        android.permission.SCHEDULE_EXACT_ALARM: restricted=true

Queries:
  system apps queryable: false
`
	runner := &recordingRunner{output: []byte(dump)}
	permissions, err := NewAdb("emulator-5554", runner).RuntimePermissions(
		context.Background(), "com.example.a")
	if err != nil {
		t.Fatalf("RuntimePermissions() error = %v", err)
	}
	want := []string{"android.permission.CAMERA", "android.permission.RECORD_AUDIO"}
	if !reflect.DeepEqual(permissions, want) {
		t.Fatalf("permissions = %v, want %v (deduplicated, sorted, runtime only)", permissions, want)
	}
}
