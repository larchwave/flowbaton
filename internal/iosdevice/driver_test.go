package iosdevice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	goios "github.com/danielpaulus/go-ios/ios"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/drivercontract"
	"github.com/larchwave/flowbaton/internal/ios"
)

type closeRecorder struct {
	closed int
	err    error
}

func (recorder *closeRecorder) Close() error {
	recorder.closed++
	return recorder.err
}

// fakeSession wires every seam so no test touches usbmuxd. iosMajor decides
// whether the tunnel seam must run.
func fakeSession(udid string, hostPort int, iosMajor int64) (*session, *closeRecorder, *closeRecorder) {
	tunnelHandle := &closeRecorder{}
	forwardHandle := &closeRecorder{}
	built := newSession(udid, hostPort)
	built.resolve = func(string) (goios.DeviceEntry, error) { return goios.DeviceEntry{}, nil }
	built.pairState = func(string) error { return nil }
	built.version = func(goios.DeviceEntry) (int64, error) { return iosMajor, nil }
	built.startTunnel = func(context.Context, string) (io.Closer, tunnelInfo, error) {
		return tunnelHandle, tunnelInfo{Address: "fd00::1", RsdPort: 50051}, nil
	}
	built.enrich = func(entry goios.DeviceEntry, _ string, _ tunnelInfo) (goios.DeviceEntry, error) {
		return entry, nil
	}
	built.forwardPort = func(goios.DeviceEntry, int, int) (io.Closer, error) { return forwardHandle, nil }
	return built, tunnelHandle, forwardHandle
}

func TestSessionErrorsAreActionable(t *testing.T) {
	cases := []struct {
		name  string
		wreck func(*session)
		want  []string
	}{
		{
			name: "unreachable over usbmuxd",
			wreck: func(broken *session) {
				broken.resolve = func(string) (goios.DeviceEntry, error) {
					return goios.DeviceEntry{}, errors.New("no such device")
				}
			},
			want: []string{"usbmuxd", "connected and unlocked"},
		},
		{
			name: "not paired",
			wreck: func(broken *session) {
				broken.pairState = func(string) error { return errors.New("no pair record") }
			},
			want: []string{"not paired", "Trust"},
		},
		{
			name: "tunnel refused",
			wreck: func(broken *session) {
				broken.startTunnel = func(context.Context, string) (io.Closer, tunnelInfo, error) {
					return nil, tunnelInfo{}, errors.New("no tunnel endpoint")
				}
			},
			want: []string{"tunnel", "ios tunnel start"},
		},
		{
			name: "rsd refused",
			wreck: func(broken *session) {
				broken.enrich = func(goios.DeviceEntry, string, tunnelInfo) (goios.DeviceEntry, error) {
					return goios.DeviceEntry{}, errors.New("handshake refused")
				}
			},
			want: []string{"RSD"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			broken, _, _ := fakeSession("00008110-TEST", 30001, 18)
			testCase.wreck(broken)
			_, err := broken.start(context.Background())
			if err == nil {
				t.Fatal("expected an error")
			}
			for _, fragment := range testCase.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Fatalf("error %q should contain %q", err, fragment)
				}
			}
		})
	}
}

func TestSessionSkipsTunnelBeforeIOS17(t *testing.T) {
	session, _, _ := fakeSession("00008110-TEST", 30001, 16)
	session.startTunnel = func(context.Context, string) (io.Closer, tunnelInfo, error) {
		t.Fatal("iOS 16 must not start a tunnel")
		return nil, tunnelInfo{}, nil
	}
	if _, err := session.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if session.tunnel != nil {
		t.Fatal("no tunnel handle expected before iOS 17")
	}
}

func TestSessionStartsTunnelFromIOS17(t *testing.T) {
	session, tunnelHandle, _ := fakeSession("00008110-TEST", 30001, 17)
	if _, err := session.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if session.tunnel == nil {
		t.Fatal("iOS 17 requires the tunnel")
	}
	if err := session.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if tunnelHandle.closed != 1 {
		t.Fatalf("tunnel closed %d times, want once", tunnelHandle.closed)
	}
}

func TestSessionFailedForwardClosesTheTunnel(t *testing.T) {
	session, tunnelHandle, _ := fakeSession("00008110-TEST", 30001, 17)
	session.forwardPort = func(goios.DeviceEntry, int, int) (io.Closer, error) {
		return nil, errors.New("port busy")
	}
	if _, err := session.start(context.Background()); err == nil {
		t.Fatal("expected the forward failure")
	}
	if tunnelHandle.closed != 1 {
		t.Fatalf("tunnel closed %d times after failed forward, want once", tunnelHandle.closed)
	}
}

func TestSessionStopClosesForwardThenTunnelOnce(t *testing.T) {
	session, tunnelHandle, forwardHandle := fakeSession("00008110-TEST", 30001, 17)
	if _, err := session.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := session.stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := session.stop(); err != nil {
		t.Fatalf("second stop must be a no-op, got %v", err)
	}
	if forwardHandle.closed != 1 || tunnelHandle.closed != 1 {
		t.Fatalf("closes = forward %d / tunnel %d, want 1 / 1",
			forwardHandle.closed, tunnelHandle.closed)
	}
}

func runnerStub(t *testing.T) (*httptest.Server, *ios.Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/status" {
			http.NotFound(writer, request)
			return
		}
		fmt.Fprint(writer, `{"status":"ok"}`)
	}))
	t.Cleanup(server.Close)
	return server, ios.NewClient(server.URL)
}

func TestDriverOpenBindsToolsAndConfirmsRunner(t *testing.T) {
	_, client := runnerStub(t)
	driver := NewDriver("00008110-TEST", 30001, client, nil)
	session, _, _ := fakeSession("00008110-TEST", 30001, 17)
	driver.session = session

	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() {
		if err := driver.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}()
	if driver.tools.device == nil {
		t.Fatal("Open must bind the resolved device entry to the tools")
	}
}

func TestDriverOpenRollsTheSessionBackWhenTheRunnerIsSilent(t *testing.T) {
	// No server: the client's status probe fails after the session started.
	driver := NewDriver("00008110-TEST", 30001, ios.NewClient("http://127.0.0.1:1"), nil)
	session, tunnelHandle, forwardHandle := fakeSession("00008110-TEST", 30001, 17)
	driver.session = session

	if err := driver.Open(context.Background()); err == nil {
		t.Fatal("expected Open to fail without a runner")
	}
	if forwardHandle.closed != 1 || tunnelHandle.closed != 1 {
		t.Fatalf("failed Open must roll the session back: forward %d / tunnel %d closes, want 1 / 1",
			forwardHandle.closed, tunnelHandle.closed)
	}
}

func TestSetPermissionsTravelsTheWireToTheRunner(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/setPermissions" || request.Method != http.MethodPost {
			http.NotFound(writer, request)
			return
		}
		body, _ = io.ReadAll(request.Body)
		fmt.Fprint(writer, `{}`)
	}))
	t.Cleanup(server.Close)

	driver := NewDriver("00008110-TEST", 30001, ios.NewClient(server.URL), nil)
	err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID:       "com.example.app",
		Permissions: map[string]string{"camera": "allow"},
	})
	if err != nil {
		t.Fatalf("SetPermissions: %v", err)
	}
	if !strings.Contains(string(body), `"camera":"allow"`) {
		t.Fatalf("wire body = %s, want the permission map", body)
	}
}

func TestDriverNameSaysDevice(t *testing.T) {
	driver := NewDriver("00008110-TEST", 30001, nil, nil)
	if driver.Name() != "ios-device:00008110-TEST:30001" {
		t.Fatalf("Name() = %q", driver.Name())
	}
}

func TestPhysicalCapabilitiesMaskUnimplementedOperations(t *testing.T) {
	features := DeclaredCapabilities().Features
	mustBeFalse := []string{
		drivercontract.CommandFeature("clearKeychain"),
		drivercontract.CommandFeature("addMedia"),
	}
	for _, feature := range mustBeFalse {
		if features[feature] {
			t.Errorf("feature %q must be declared unsupported", feature)
		}
	}
	mustBeTrue := []string{
		drivercontract.CommandFeature("tapOn"),
		drivercontract.CommandFeature("launchApp"),
		drivercontract.CommandFeature("takeScreenshot"),
		drivercontract.CommandFeature("setLocation"),
		drivercontract.CommandFeature("clearState"),
		drivercontract.CommandFeature("startRecording"),
		drivercontract.CommandFeature("stopRecording"),
		drivercontract.CommandFeature("setPermissions"),
		drivercontract.CommandFeature("openLink"),
		"screenRecording", "deviceLogCapture", "crashArtifacts",
	}
	for _, feature := range mustBeTrue {
		if !features[feature] {
			t.Errorf("feature %q must stay supported: hardware carries the full command surface", feature)
		}
	}
}

func TestUnboundToolsFailClosed(t *testing.T) {
	tools := NewTools("00008110-TEST")
	err := tools.Launch(context.Background(), "com.example.a", nil, false)
	if err == nil || !strings.Contains(err.Error(), "not open yet") {
		t.Fatalf("unbound tools must fail closed, got %v", err)
	}
}

func TestApplePlatformLimitsAreErrUnsupported(t *testing.T) {
	tools := NewTools("00008110-TEST")
	if err := tools.ResetKeychain(context.Background()); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("ResetKeychain: %v", err)
	}
	if err := tools.AddMedia(context.Background(), nil); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("AddMedia: %v", err)
	}
}

// TestPhysicalDeviceSessionEndToEnd runs only against real hardware named by
// FLOWBATON_TEST_IOS_DEVICE_UDID; absence of that variable is the sole skip
// path (mirrors FLOWBATON_TEST_POSTGRES_URL).
func TestPhysicalDeviceSessionEndToEnd(t *testing.T) {
	udid := os.Getenv("FLOWBATON_TEST_IOS_DEVICE_UDID")
	if udid == "" {
		t.Skip("set FLOWBATON_TEST_IOS_DEVICE_UDID to run against real hardware")
	}
	session := newSession(udid, 30099)
	if _, err := session.start(context.Background()); err != nil {
		t.Fatalf("session start against %s: %v", udid, err)
	}
	if err := session.stop(); err != nil {
		t.Fatalf("session stop: %v", err)
	}
}
