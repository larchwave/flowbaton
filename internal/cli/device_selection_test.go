package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/ios"
	"github.com/larchwave/flowbaton/internal/iosdevice"
)

// newDriver resolves an iOS udid's flavor by inventory membership. The tests
// in this package run in parallel, so per-test seam swaps would race; the
// whole package therefore sees one fake inventory, and the tests that need a
// different one swap it without t.Parallel and restore via t.Cleanup.
func init() {
	iosSimulatorInventory = func(context.Context) ([]ios.Device, error) {
		return []ios.Device{
			{UDID: "UDID-1"}, {UDID: "UDID-2"},
			{UDID: "udid-one"}, {UDID: "udid-two"}, {UDID: "solo-udid"},
		}, nil
	}
	iosPhysicalInventory = func(context.Context) ([]iosdevice.Device, error) {
		return []iosdevice.Device{{UDID: "00008110-PHYS"}}, nil
	}
}

// Device resolution must never invent a device identifier.
//
// The refusals matter more than the successes here. A resolution that guesses
// runs a suite against a device the operator did not name, and the report will
// not say which one.

func rootShard(udid string) Shard {
	// Every shard requires an assigned port; the resolver does not substitute a
	// default.
	return Shard{Device: udid, DriverPort: 22087}
}

func TestDeviceResolutionRequiresAPlatform(t *testing.T) {
	t.Parallel()

	_, err := NewDeviceSession(context.Background(), TestOptions{Roots: []string{"flow.yaml"}}, rootShard("UDID-1"))
	if err == nil {
		t.Fatal("a session was built with no platform")
	}
	if !strings.Contains(err.Error(), "platform") {
		t.Fatalf("error = %q, want it to name the missing platform", err)
	}
}

func TestDeviceResolutionRequiresAUDID(t *testing.T) {
	t.Parallel()

	// A shard with no device is a guess waiting to happen. Spreading a suite
	// over several devices is sharding's job, and PlanShards refuses the
	// combinations that do not add up before anything reaches here.
	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}}, rootShard(""))
	if err == nil {
		t.Fatal("a session was built with no udid")
	}
	if !strings.Contains(err.Error(), "udid is required") {
		t.Fatalf("error = %q, want it to name the missing udid", err)
	}
}

func TestIOSResolvesToADriverNamedForItsSimulator(t *testing.T) {
	t.Parallel()

	// The positive control: without it, every refusal above would be satisfied
	// by a resolver that refuses everything.
	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}}, rootShard("UDID-1"))
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if session.Driver == nil {
		t.Fatal("no driver was built")
	}
	if !strings.Contains(session.Driver.Name(), "UDID-1") {
		t.Fatalf("driver name = %q, want the requested udid", session.Driver.Name())
	}
	if got := string(session.Driver.Capabilities().Platform); got != "ios" {
		t.Fatalf("platform = %q, want ios", got)
	}
}

func TestIOSPhysicalUdidResolvesToTheDeviceDriver(t *testing.T) {
	t.Parallel()

	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}}, rootShard("00008110-PHYS"))
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if !strings.HasPrefix(session.Driver.Name(), "ios-device:00008110-PHYS") {
		t.Fatalf("driver name = %q, want the physical-device driver", session.Driver.Name())
	}
}

func TestUnknownIOSUdidNamesBothInventories(t *testing.T) {
	t.Parallel()

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}}, rootShard("UDID-NOWHERE"))
	if err == nil {
		t.Fatal("an unknown udid built a session")
	}
	for _, fragment := range []string{"UDID-NOWHERE", "simctl", "usbmuxd"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to mention %q", err, fragment)
		}
	}
}

func TestExplicitIOSPhysicalTokenSkipsInventoryResolution(t *testing.T) {
	t.Parallel()

	// The udid is in NEITHER fake inventory: the explicit token must not
	// consult them — the serve inventory already named the flavor.
	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios-physical", Roots: []string{"flow.yaml"}},
		rootShard("00008110-UNLISTED"))
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if !strings.HasPrefix(session.Driver.Name(), "ios-device:00008110-UNLISTED") {
		t.Fatalf("driver name = %q, want the physical-device driver", session.Driver.Name())
	}
}

func TestPhysicalIOSReinstallResolvesTheDeviceFlavor(t *testing.T) {
	previous := resolveIOSRunnerBundle
	var asked iosRunnerFlavor
	resolveIOSRunnerBundle = func(_ context.Context, flavor iosRunnerFlavor) (*ios.RunnerBundle, error) {
		asked = flavor
		return nil, nil
	}
	t.Cleanup(func() { resolveIOSRunnerBundle = previous })

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}, ReinstallDriver: true},
		rootShard("00008110-PHYS"))
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if asked != iosRunnerFlavorDevice {
		t.Fatalf("bundle resolved for flavor %q, want %q — a simulator build cannot run on hardware",
			asked, iosRunnerFlavorDevice)
	}
}

func TestEachShardResolvesItsOwnDevice(t *testing.T) {
	t.Parallel()

	// The control for the test above: a resolver reading options.Devices[0]
	// instead of the shard's own device would build every shard's driver
	// against the first simulator, and all of them would fight over one screen.
	options := TestOptions{
		Platform: "ios",
		Roots:    []string{"flow.yaml"},
		Devices:  []string{"UDID-1", "UDID-2"},
	}
	session, err := NewDeviceSession(context.Background(), options, Shard{Index: 1, Device: "UDID-2", DriverPort: 41001})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(session.Driver.Name(), "UDID-2") {
		t.Fatalf("driver name = %q, want the second shard's own device", session.Driver.Name())
	}
}

func TestAShardWritesWhereTheRunnerSentIt(t *testing.T) {
	t.Parallel()

	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}},
		Shard{Device: "UDID-1", DriverPort: 22087, OutputDirectory: "/artifacts/shard-1"})
	if err != nil {
		t.Fatal(err)
	}
	if session.OutputDirectory != "/artifacts/shard-1" {
		t.Fatalf("OutputDirectory = %q, want the shard's own", session.OutputDirectory)
	}
}

func TestAndroidResolvesToADriverNamedForItsDevice(t *testing.T) {
	t.Parallel()

	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{Device: "emulator-5554", DriverPort: 7001})
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if got := session.Driver.Name(); got != "android:emulator-5554:7001" {
		t.Fatalf("driver name = %q, want android:serial:port", got)
	}
	if got := string(session.Driver.Capabilities().Platform); got != "android" {
		t.Fatalf("platform = %q, want android", got)
	}
}

// withAndroidInventory swaps the adb inventory for a fake and restores it.
// Tests using it must not run in parallel: the hook is package state.
func withAndroidInventory(t *testing.T, devices []android.Device, err error) {
	t.Helper()
	previous := androidInventory
	androidInventory = func(context.Context) ([]android.Device, error) { return devices, err }
	t.Cleanup(func() { androidInventory = previous })
}

func TestAndroidWithNoDeviceUsesTheOnlyConnectedOne(t *testing.T) {
	withAndroidInventory(t, []android.Device{{Serial: "emulator-5554", State: "device"}}, nil)

	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{Device: "", DriverPort: 7001})
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if !strings.Contains(session.Driver.Name(), "emulator-5554") {
		t.Fatalf("driver name = %q, want the one connected device", session.Driver.Name())
	}
}

func TestAndroidWithSeveralDevicesRefusesToGuess(t *testing.T) {
	withAndroidInventory(t, []android.Device{
		{Serial: "emulator-5554", State: "device"},
		{Serial: "R58M12ABCDE", State: "device"},
	}, nil)

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{Device: "", DriverPort: 7001})
	if err == nil {
		t.Fatal("a device was picked from several without being asked")
	}
	// The refusal must name the candidates: an operator fixing this needs
	// the serials, not a hunt through adb.
	for _, serial := range []string{"emulator-5554", "R58M12ABCDE"} {
		if !strings.Contains(err.Error(), serial) {
			t.Fatalf("error = %q, want it to name %s", err, serial)
		}
	}
}

func TestAndroidWithNoDevicesExplains(t *testing.T) {
	withAndroidInventory(t, nil, nil)

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{Device: "", DriverPort: 7001})
	if err == nil {
		t.Fatal("a session was built with no device anywhere")
	}
	if !strings.Contains(err.Error(), "no android devices") {
		t.Fatalf("error = %q, want it to say adb listed nothing", err)
	}
}

func TestCanceledContextReachesSingleAndroidInventory(t *testing.T) {
	previous := androidInventory
	called := false
	androidInventory = func(ctx context.Context) ([]android.Device, error) {
		called = true
		return nil, ctx.Err()
	}
	t.Cleanup(func() { androidInventory = previous })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewDeviceSession(ctx,
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{DriverPort: 7001})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("NewDeviceSession() error = %v, want context.Canceled", err)
	}
	if !called {
		t.Fatal("session construction did not call the Android inventory")
	}
}

func TestAndroidRequiresAShardPortLikeIOS(t *testing.T) {
	t.Parallel()

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}},
		Shard{Device: "emulator-5554", DriverPort: 0})
	if err == nil {
		t.Fatal("a session was built with no driver port assigned")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Fatalf("error = %q, want it to name the missing port", err)
	}
}

// -p web resolves to the Web/CDP driver without launching it during session
// construction.
func TestWebResolvesToABrowserDriver(t *testing.T) {
	t.Parallel()

	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "web", Roots: []string{"flow.yaml"}},
		Shard{DriverPort: 9222})
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if got := string(session.Driver.Capabilities().Platform); got != "web" {
		t.Fatalf("platform = %q, want web", got)
	}
	// Nothing may have been launched yet: building a session is not running
	// one, and a browser started here would outlive a run that never happens.
	if got := session.Driver.Name(); got != "web" {
		t.Fatalf("driver name = %q, want web", got)
	}
}

// A browser has no udid, so demanding one would make -p web unusable — but the
// port is still required, for the same reason it is on the other platforms: an
// unassigned port silently puts every shard on one browser.
func TestWebNeedsNoDeviceButStillNeedsAPort(t *testing.T) {
	t.Parallel()

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "web", Roots: []string{"flow.yaml"}}, Shard{DriverPort: 0})
	if err == nil {
		t.Fatal("a web session was built with no devtools port assigned")
	}
	// Require the specific missing-port diagnostic, not the generic
	// supported-platform text.
	if !strings.Contains(err.Error(), "no devtools port") {
		t.Fatalf("error = %q, want it to name the missing devtools port", err)
	}
}

func TestAnUnknownPlatformNamesWhatIsSupported(t *testing.T) {
	t.Parallel()

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "blackberry", Roots: []string{"flow.yaml"}}, rootShard("UDID-1"))
	if err == nil {
		t.Fatal("an unknown platform was accepted")
	}
	for _, supported := range []string{"ios", "android", "web"} {
		if !strings.Contains(err.Error(), supported) {
			t.Fatalf("error = %q, want it to name %s", err, supported)
		}
	}
}

func TestTheBaseDirectoryFollowsTheFirstRoot(t *testing.T) {
	t.Parallel()

	// Flow resources resolve relative to the flow, so a file root contributes
	// its directory and a directory root contributes itself. Getting this
	// wrong makes every runScript path in a workspace fail to resolve.
	directory := t.TempDir()
	session, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{directory}}, rootShard("UDID-1"))
	if err != nil {
		t.Fatal(err)
	}
	if session.BaseDirectory != directory {
		t.Fatalf("BaseDirectory = %q, want the directory root itself", session.BaseDirectory)
	}

	file := directory + "/flow.yaml"
	writeFile(t, file, "appId: com.example.a\n---\n- launchApp\n")
	session, err = NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{file}}, rootShard("UDID-1"))
	if err != nil {
		t.Fatal(err)
	}
	if session.BaseDirectory != directory {
		t.Fatalf("BaseDirectory = %q, want the file's directory %q", session.BaseDirectory, directory)
	}
}

func TestAndroidAgentAPKsComeFromTheEnvironmentTogether(t *testing.T) {
	// With neither variable set, the run uses the pair installed under the home
	// by driver-setup (android_agent_apks_test.go).
	// The home is redirected here so this test cannot read — or depend on — the
	// developer's real installed agent.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("FLOWBATON_ANDROID_APP_APK", "")
	t.Setenv("FLOWBATON_ANDROID_TEST_APK", "")
	apks, err := androidAgentAPKs(context.Background())
	if err != nil || apks != nil {
		t.Fatalf("androidAgentAPKs() = %v, %v; want nil, nil with nothing set and nothing installed", apks, err)
	}

	t.Setenv("FLOWBATON_ANDROID_APP_APK", "/apks/app.apk")
	if _, err := androidAgentAPKs(context.Background()); err == nil {
		t.Fatal("one variable alone must be refused, not half-honored")
	}

	t.Setenv("FLOWBATON_ANDROID_TEST_APK", "/apks/test.apk")
	apks, err = androidAgentAPKs(context.Background())
	if err != nil {
		t.Fatalf("androidAgentAPKs() error = %v", err)
	}
	want := android.AgentAPKs{App: "/apks/app.apk", Test: "/apks/test.apk"}
	if apks == nil || *apks != want {
		t.Fatalf("androidAgentAPKs() = %+v, want %+v", apks, want)
	}
}

func TestNoReinstallDriverSkipsManagedAndroidAssetResolution(t *testing.T) {
	previous := resolveAndroidAgentAPKs
	called := false
	resolveAndroidAgentAPKs = func(context.Context) (*android.AgentAPKs, error) {
		called = true
		return nil, fmt.Errorf("managed Android assets must not be resolved")
	}
	t.Cleanup(func() { resolveAndroidAgentAPKs = previous })

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "android", Roots: []string{"flow.yaml"}, ReinstallDriver: false},
		Shard{Device: "emulator-5554", DriverPort: 7001})
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if called {
		t.Fatal("--no-reinstall-driver still resolved managed Android assets")
	}
}

func TestNoReinstallDriverSkipsManagedIOSAssetResolution(t *testing.T) {
	previous := resolveIOSRunnerBundle
	called := false
	resolveIOSRunnerBundle = func(context.Context, iosRunnerFlavor) (*ios.RunnerBundle, error) {
		called = true
		return nil, fmt.Errorf("managed iOS assets must not be resolved")
	}
	t.Cleanup(func() { resolveIOSRunnerBundle = previous })

	_, err := NewDeviceSession(context.Background(),
		TestOptions{Platform: "ios", Roots: []string{"flow.yaml"}, ReinstallDriver: false},
		rootShard("UDID-1"))
	if err != nil {
		t.Fatalf("NewDeviceSession() error = %v", err)
	}
	if called {
		t.Fatal("--no-reinstall-driver still resolved managed iOS assets")
	}
}

func TestCanceledContextReachesManagedAssetVerification(t *testing.T) {
	previousAndroid := resolveAndroidAgentAPKs
	previousIOS := resolveIOSRunnerBundle
	t.Cleanup(func() {
		resolveAndroidAgentAPKs = previousAndroid
		resolveIOSRunnerBundle = previousIOS
	})

	for _, test := range []struct {
		name     string
		platform string
		device   string
	}{
		{name: "android", platform: "android", device: "emulator-5554"},
		{name: "ios", platform: "ios", device: "UDID-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			switch test.platform {
			case "android":
				resolveAndroidAgentAPKs = func(ctx context.Context) (*android.AgentAPKs, error) {
					called = true
					return nil, ctx.Err()
				}
			case "ios":
				resolveIOSRunnerBundle = func(ctx context.Context, _ iosRunnerFlavor) (*ios.RunnerBundle, error) {
					called = true
					return nil, ctx.Err()
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := NewDeviceSession(ctx,
				TestOptions{Platform: test.platform, ReinstallDriver: true},
				Shard{Device: test.device, DriverPort: 7001})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("NewDeviceSession() error = %v, want context.Canceled", err)
			}
			if !called {
				t.Fatal("session construction did not reach managed asset verification")
			}
		})
	}
}
