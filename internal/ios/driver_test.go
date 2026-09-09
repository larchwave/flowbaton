package ios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

// These tests pin the iOS device.Driver composition: which boundary answers
// each method, call ordering, and unsupported operations.
//
// The refusals matter as much as the successes. A driver that silently
// succeeds at an operation the platform cannot perform reports a green flow
// that never ran, which is worse than a red one.

func TestDriverIsADeviceDriver(t *testing.T) {
	t.Parallel()

	var _ device.Driver = (*Driver)(nil)
	var _ device.OrientationReader = (*Driver)(nil)
}

func TestDriverRefusesTheOperationsIOSCannotPerform(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md documents these as Android-side capabilities:
	// the proxy and airplane-mode surfaces are the Android agent's, and Chrome
	// DevTools hierarchy merging is named androidWebViewHierarchy=devtools.
	// None of them has an iOS route in the frozen contract or a simctl verb.
	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	ctx := context.Background()

	for _, test := range []struct {
		name string
		call func() error
	}{
		{"SetProxy", func() error { return driver.SetProxy(ctx, device.Proxy{Host: "h", Port: 1}) }},
		{"ResetProxy", func() error { return driver.ResetProxy(ctx) }},
		{"SetAirplaneMode", func() error {
			return driver.SetAirplaneMode(ctx, device.AirplaneModeRequest{Enabled: true})
		}},
		{"IsAirplaneModeEnabled", func() error { _, err := driver.IsAirplaneModeEnabled(ctx); return err }},
		{"SetAndroidChromeDevToolsEnabled", func() error {
			return driver.SetAndroidChromeDevToolsEnabled(ctx, device.ChromeDevToolsRequest{Enabled: true})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.call()
			if err == nil {
				t.Fatalf("%s succeeded on iOS; want an explicit refusal", test.name)
			}
			if !errors.Is(err, device.ErrUnsupported) {
				t.Fatalf("%s error = %v, want it to wrap device.ErrUnsupported", test.name, err)
			}
		})
	}
}

func TestCapabilitiesReportTheSameRefusals(t *testing.T) {
	t.Parallel()

	// A refusal at call time is too late for preflight. Whatever the driver
	// refuses must also be declared unsupported, or a flow gets to run halfway
	// before learning it cannot finish.
	capabilities := newTestDriver(t, func(http.ResponseWriter, *http.Request) {}).Capabilities()
	if capabilities.Platform != device.Platform("ios") {
		t.Fatalf("Platform = %q, want ios", capabilities.Platform)
	}
	for _, feature := range []string{
		"proxy", "airplaneMode", "androidChromeDevTools",
	} {
		supported, declared := capabilities.Features[feature]
		if !declared {
			t.Fatalf("Capabilities() does not mention %q, which the driver refuses at call time", feature)
		}
		if supported {
			t.Fatalf("Capabilities() reports %q supported, but the driver refuses it", feature)
		}
	}
	for _, feature := range []string{"deviceLogCapture", "crashArtifacts"} {
		if !capabilities.Features[feature] {
			t.Fatalf("Capabilities() reports real diagnostic feature %q unsupported", feature)
		}
	}
}

func TestBackPressIsUnsupportedOnIOS(t *testing.T) {
	t.Parallel()

	// Capabilities declares backPress unsupported. Call time must agree so a
	// direct driver caller cannot record a successful step that did nothing.
	var reached []string
	driver := newTestDriver(t, func(_ http.ResponseWriter, request *http.Request) {
		reached = append(reached, request.URL.Path)
	})
	if err := driver.BackPress(context.Background()); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("BackPress() error = %v, want device.ErrUnsupported", err)
	}
	if len(reached) != 0 {
		t.Fatalf("BackPress() called %v; iOS has no back", reached)
	}
}

func TestSetOrientationMapsEveryCanonicalFlowValue(t *testing.T) {
	t.Parallel()

	tests := map[device.Orientation]string{
		"PORTRAIT":        "portrait",
		"LANDSCAPE_LEFT":  "landscapeLeft",
		"LANDSCAPE_RIGHT": "landscapeRight",
		"UPSIDE_DOWN":     "upsideDown",
	}
	for input, want := range tests {
		input, want := input, want
		t.Run(string(input), func(t *testing.T) {
			t.Parallel()
			var got string
			driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/setOrientation" {
					t.Errorf("path = %q, want /setOrientation", request.URL.Path)
				}
				var body struct {
					Orientation string `json:"orientation"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode orientation request: %v", err)
				}
				got = body.Orientation
				writeJSON(t, writer, map[string]any{})
			})
			if err := driver.SetOrientation(context.Background(), input); err != nil {
				t.Fatalf("SetOrientation(%s) error = %v", input, err)
			}
			if got != want {
				t.Fatalf("orientation = %q, want %q", got, want)
			}
		})
	}

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsupported orientation reached the runner")
	})
	if err := driver.SetOrientation(context.Background(), "DIAGONAL"); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("SetOrientation(DIAGONAL) error = %v, want device.ErrUnsupported", err)
	}
}

func TestCurrentOrientationMapsEveryWireValue(t *testing.T) {
	t.Parallel()

	for wire, want := range map[ScreenOrientation]device.Orientation{
		ScreenOrientationPortrait:       "PORTRAIT",
		ScreenOrientationUpsideDown:     "UPSIDE_DOWN",
		ScreenOrientationLandscapeLeft:  "LANDSCAPE_LEFT",
		ScreenOrientationLandscapeRight: "LANDSCAPE_RIGHT",
	} {
		wire, want := wire, want
		t.Run(string(wire), func(t *testing.T) {
			t.Parallel()
			driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/deviceInfo" {
					t.Errorf("path = %q, want /deviceInfo", request.URL.Path)
				}
				writeJSON(t, writer, DeviceInfo{Orientation: wire})
			})
			got, err := driver.CurrentOrientation(context.Background())
			if err != nil {
				t.Fatalf("CurrentOrientation() error = %v", err)
			}
			if got != want {
				t.Fatalf("CurrentOrientation() = %q, want %q", got, want)
			}
		})
	}
}

func TestLongPressUsesTheTouchRouteWithItsDuration(t *testing.T) {
	t.Parallel()

	// TouchRequest.Duration is a pointer precisely because its presence is what
	// turns a touch into a long press. A tap must not send a zero duration.
	for _, test := range []struct {
		name         string
		call         func(*Driver) error
		wantDuration any
	}{
		{
			name: "tap sends no duration",
			call: func(driver *Driver) error {
				return driver.Tap(context.Background(), device.TapRequest{Point: device.Point{X: 3, Y: 4}})
			},
			wantDuration: nil,
		},
		{
			name: "long press sends its duration in seconds",
			call: func(driver *Driver) error {
				return driver.LongPress(context.Background(), device.LongPressRequest{
					Point: device.Point{X: 3, Y: 4}, DurationMillis: 3000,
				})
			},
			wantDuration: 3.0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var body map[string]any
			driver := newTestDriver(t, func(_ http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/touch" {
					t.Errorf("path = %q, want /touch", request.URL.Path)
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
				}
			})
			if err := test.call(driver); err != nil {
				t.Fatalf("call error = %v", err)
			}
			if body["x"] != 3.0 || body["y"] != 4.0 {
				t.Fatalf("point = (%v,%v), want (3,4)", body["x"], body["y"])
			}
			if got := body["duration"]; !reflect.DeepEqual(got, test.wantDuration) {
				t.Fatalf("duration = %#v, want %#v", got, test.wantDuration)
			}
		})
	}
}

func TestPressKeyTargetsTheRememberedForegroundApplication(t *testing.T) {
	t.Parallel()

	var body struct {
		Key    string   `json:"key"`
		AppIDs []string `json:"appIds"`
	}
	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode pressKey request: %v", err)
		}
		writeJSON(t, writer, map[string]any{})
	})
	driver.rememberLaunch("com.example.a")
	if err := driver.PressKey(
		context.Background(), device.PressKeyRequest{Code: device.KeyCode("enter")}); err != nil {
		t.Fatalf("PressKey() error = %v", err)
	}
	if body.Key != "enter" || !reflect.DeepEqual(body.AppIDs, []string{"com.example.a"}) {
		t.Fatalf("pressKey body = %#v, want enter targeting com.example.a", body)
	}
}

// pressKey HOME is the runner's /pressButton route, not /pressKey: the
// keyboard route needs a focused field and knows nothing about the device's
// home button (issue #21). After it the launched app is in the background, so
// the driver forgets it: a filter-less hierarchy read then gets the home
// screen instead of a "not in the foreground" refusal.
func TestPressKeyHomeGoesThroughThePressButtonRouteAndForgetsTheApp(t *testing.T) {
	t.Parallel()

	var paths []string
	var body map[string]any
	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		paths = append(paths, request.URL.Path)
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode %s request: %v", request.URL.Path, err)
		}
		writeJSON(t, writer, map[string]any{})
	})
	driver.rememberLaunch("com.example.a")
	if err := driver.PressKey(
		context.Background(), device.PressKeyRequest{Code: device.KeyCode("HOME"), AppIDs: []string{"com.example.a"}}); err != nil {
		t.Fatalf("PressKey(HOME) error = %v", err)
	}
	if !reflect.DeepEqual(paths, []string{"/pressButton"}) || body["button"] != "home" {
		t.Fatalf("HOME reached %v with body %#v, want one /pressButton with button home", paths, body)
	}
	if got := driver.defaultAppIDs(nil); len(got) != 0 {
		t.Fatalf("after HOME the driver still names %v as the foreground app", got)
	}
}

func TestPressKeyLockIsStillRefusedOnIOS(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("LOCK reached the runner at %s", request.URL.Path)
		writeJSON(t, writer, map[string]any{})
	})
	err := driver.PressKey(context.Background(), device.PressKeyRequest{Code: device.KeyCode("LOCK")})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("PressKey(LOCK) = %v, want ErrUnsupported", err)
	}
}

func TestContentDescriptorConvertsTheAccessibilityTree(t *testing.T) {
	t.Parallel()

	// The hierarchy is what every selector runs against, so the conversion from
	// the runner's AXElement to the platform-neutral TreeNode is the highest
	// consequence mapping in this package.
	//
	// bounds is an Android-style "[l,t][r,b]" string on every platform:
	// specs/02-device-drivers.md line 26 parses that shape, and line 77 has the
	// web driver emitting it too, "for uniform parsing".
	value, title, placeholder := "field value", "field title", "type here"
	hierarchy := ViewHierarchy{Depth: 2, AXElement: AXElement{
		Identifier:  "root",
		Label:       "Root",
		ElementType: 1,
		Enabled:     true,
		Frame:       Frame{X: 0, Y: 0, Width: 390, Height: 844},
		Children: []AXElement{
			{
				Identifier:       "field",
				Label:            "Email",
				Value:            &value,
				Title:            &title,
				PlaceholderValue: &placeholder,
				ElementType:      49,
				Enabled:          true,
				Selected:         true,
				HasFocus:         true,
				Frame:            Frame{X: 16, Y: 100, Width: 358, Height: 44},
			},
			{
				Identifier:  "toggle",
				Label:       "Notifications",
				ElementType: 40, // switch — checkable, and valueless, so the trait rules
				Enabled:     false,
				Selected:    true,
				Frame:       Frame{X: 16, Y: 200, Width: 60, Height: 30},
			},
		},
	}}

	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/viewHierarchy" {
			t.Errorf("path = %q, want /viewHierarchy", request.URL.Path)
		}
		writeJSON(t, writer, hierarchy)
	})

	root, err := driver.ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{AppIDs: []string{"com.example.a"}})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}

	if got := root.Attributes["bounds"]; got != "[0,0][390,844]" {
		t.Fatalf("root bounds = %q, want the Android-style [l,t][r,b] string", got)
	}
	if got := root.Attributes["id"]; got != "root" {
		t.Fatalf("root id = %q, want the accessibility identifier", got)
	}
	if len(root.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(root.Children))
	}

	field := root.Children[0]
	if got := field.Attributes["text"]; got != "field value" {
		t.Fatalf("field text = %q; a field with a value shows its value", got)
	}
	if got := field.Attributes["accessibilityText"]; got != "Email" {
		t.Fatalf("field accessibilityText = %q, want the label", got)
	}
	if got := field.Attributes["hintText"]; got != "type here" {
		t.Fatalf("field hintText = %q, want the placeholder", got)
	}
	if got := field.Attributes["title"]; got != "field title" {
		t.Fatalf("field title = %q", got)
	}
	if got := field.Attributes["bounds"]; got != "[16,100][374,144]" {
		t.Fatalf("field bounds = %q, want right/bottom computed from width/height", got)
	}
	if field.Enabled == nil || !*field.Enabled {
		t.Fatal("field must report enabled")
	}
	if field.Focused == nil || !*field.Focused {
		t.Fatal("field must report focused")
	}
	if field.Selected == nil || !*field.Selected {
		t.Fatal("field must report selected")
	}
	// A plain Settings cell reports a false checked value:
	//
	//	checked: false -> copies "General"
	//	checked: true  -> both fail
	//
	// A plain cell is not checkable, so its wire value is false. Keeping the
	// field present lets selectors distinguish false from a missing attribute.
	if field.Checked == nil {
		t.Fatal("field Checked is nil; the contract answers false for a non-checkable element")
	}
	if *field.Checked {
		t.Fatalf("field Checked = true; element type 49 has no checked state, so it reports false")
	}

	toggle := root.Children[1]
	// `text` mirrors a non-empty value; labels stay in accessibilityText.
	//
	// It is not cosmetic: `text: ".*"` then resolves to a different first node,
	// which is what `text-any`/`text-empty` require. Selectors still find a labelled element,
	// because text matching tests accessibilityText too.
	if _, present := toggle.Attributes["text"]; present {
		t.Fatalf("toggle text = %q; with no value there is no text, only a label",
			toggle.Attributes["text"])
	}
	if got := toggle.Attributes["accessibilityText"]; got != "Notifications" {
		t.Fatalf("toggle accessibilityText = %q, want the label", got)
	}
	if toggle.Checked == nil {
		t.Fatal("element type 40 is a switch and must report checked")
	}
	if !*toggle.Checked {
		t.Fatal("a selected switch must report checked true")
	}
	if toggle.Enabled == nil || *toggle.Enabled {
		t.Fatal("toggle must report enabled false")
	}
}

func TestCheckableElementTypesAreExactlyThree(t *testing.T) {
	t.Parallel()

	// checkbox=12, switch=40, toggle=41. Anything else has no checked state at
	// all, and reporting false for it would make `checked: false` match every
	// button on screen. No spec in this tree names the codes -- an earlier
	// comment here quoted one that is not in specs/02-device-drivers.md -- so
	// checkableTypes and this test are the whole rule.
	for elementType, wantCheckable := range map[int]bool{
		12: true, 40: true, 41: true,
		11: false, 39: false, 42: false, 0: false,
	} {
		got := checkableElementType(elementType)
		if got != wantCheckable {
			t.Fatalf("checkableElementType(%d) = %v, want %v", elementType, got, wantCheckable)
		}
	}
}

func TestDeviceInfoReportsPixelsAndPointsSeparately(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md line 28: the iOS grid is points, not pixels.
	// Collapsing the two would misplace every coordinate on a Retina screen.
	driver := newTestDriver(t, func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(t, writer, DeviceInfo{
			WidthPoints: 390, HeightPoints: 844, WidthPixels: 1170, HeightPixels: 2532,
		})
	})
	info, err := driver.DeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("DeviceInfo() error = %v", err)
	}
	want := device.DeviceInfo{
		Platform:    device.Platform("ios"),
		WidthPixels: 1170, HeightPixels: 2532,
		WidthGrid: 390, HeightGrid: 844,
	}
	if info != want {
		t.Fatalf("DeviceInfo() = %#v, want %#v", info, want)
	}
}

func TestLaunchAppGoesThroughSimctlWithItsArguments(t *testing.T) {
	t.Parallel()

	// launchApp carries typed arguments, and the runner's /launchApp route takes
	// only a bundle id. simctl is the half that can pass arguments, so it is the
	// half that launches.
	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 0), runner)
	err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{
		AppID: "com.example.a",
		Arguments: []device.LaunchArgument{
			{Key: "mode", Value: "probe", Type: "string"},
			{Key: "verbose", Value: "true", Type: "boolean"},
		},
	})
	if err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	want := []string{
		"xcrun", "simctl", "launch", "UDID-1", "com.example.a",
		"-mode", "probe", "verbose", "true",
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("calls = %v, want exactly %v", runner.calls, want)
	}
}

func TestClearAppStatePreservesAndReinstallsTheApplication(t *testing.T) {
	t.Parallel()

	// A simulator has no per-app "clear data" verb. The installed bundle must be
	// preserved before uninstall so the engine can still grant permissions and
	// launch it after this call returns.
	source := filepath.Join(t.TempDir(), "Probe.app")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{output: []byte(source + "\n")}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	temporaryRoot := filepath.Join(t.TempDir(), "clear-state")
	driver.makeTemporaryDirectory = func() (string, error) {
		if err := os.Mkdir(temporaryRoot, 0o755); err != nil {
			return "", err
		}
		return temporaryRoot, nil
	}
	var copiedFrom, copiedTo string
	driver.copyAppBundle = func(_ context.Context, from, to string) error {
		copiedFrom, copiedTo = from, to
		return os.Mkdir(to, 0o755)
	}
	if err := driver.ClearAppState(context.Background(), device.AppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("ClearAppState() error = %v", err)
	}
	if copiedFrom != source || copiedTo != filepath.Join(temporaryRoot, "Probe.app") {
		t.Fatalf("copied %q to %q, want %q to temporary Probe.app", copiedFrom, copiedTo, source)
	}
	want := [][]string{
		{"xcrun", "simctl", "get_app_container", "UDID-1", "com.example.a", "app"},
		{"xcrun", "simctl", "uninstall", "UDID-1", "com.example.a"},
		{"xcrun", "simctl", "install", "UDID-1", filepath.Join(temporaryRoot, "Probe.app")},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
	}
	if _, err := os.Stat(temporaryRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary bundle directory still exists after reinstall: %v", err)
	}
}

func TestSetPermissionsIssuesOneSimctlCallPerPermissionInAStableOrder(t *testing.T) {
	t.Parallel()

	// simctl takes one service per invocation, so a permissions map becomes N
	// calls. Map iteration order must not decide the order they are applied in.
	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	err := driver.SetPermissions(context.Background(), device.PermissionsRequest{
		AppID:       "com.example.a",
		Permissions: map[string]string{"photos": "deny", "camera": "allow", "location": "unset"},
	})
	if err != nil {
		t.Fatalf("SetPermissions() error = %v", err)
	}
	want := [][]string{
		{"xcrun", "simctl", "privacy", "UDID-1", "grant", "camera", "com.example.a"},
		{"xcrun", "simctl", "privacy", "UDID-1", "reset", "location", "com.example.a"},
		{"xcrun", "simctl", "privacy", "UDID-1", "revoke", "photos", "com.example.a"},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %v, want %v in sorted order", runner.calls, want)
	}
}

func TestOpenLinkRefusesABrowserChoiceItCannotHonor(t *testing.T) {
	t.Parallel()

	// simctl openurl hands the URL to the system, which opens it in the default
	// browser. Accepting a browser choice and ignoring it would silently test
	// the wrong browser.
	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	ctx := context.Background()

	if err := driver.OpenLink(ctx, device.OpenLinkRequest{Link: "https://example.invalid"}); err != nil {
		t.Fatalf("OpenLink() error = %v", err)
	}
	want := []string{"xcrun", "simctl", "openurl", "UDID-1", "https://example.invalid"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
	}

	err := driver.OpenLink(ctx, device.OpenLinkRequest{
		Link: "https://example.invalid", Browser: device.Browser("chrome")})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("OpenLink() with a browser choice error = %v, want device.ErrUnsupported", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("a refused OpenLink still ran %v", runner.calls[1:])
	}
}

func TestOpenReportsAnUnreachableRunner(t *testing.T) {
	t.Parallel()

	// Open is the point where an absent runner must be discovered. A driver
	// that opens successfully against nothing defers the failure to the first
	// command, where it reads as a flow failure rather than a setup failure.
	driver := newTestDriver(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"errorMessage":"runner not ready"}`))
	})
	err := driver.Open(context.Background())
	if err == nil {
		t.Fatal("Open() succeeded against an unhealthy runner")
	}
	if !strings.Contains(err.Error(), "runner not ready") {
		t.Fatalf("Open() error = %v, want the runner's own message", err)
	}
}

func TestNameIdentifiesTheDeviceItDrives(t *testing.T) {
	t.Parallel()

	// Reports name the driver. "ios" alone cannot distinguish two simulators in
	// the same run, and a udid alone cannot distinguish two SHARDS: a sharded
	// run can put the same udid behind two runners on different ports, and the
	// name is what "opening ..." says when one of them does not answer.
	name := newTestDriver(t, func(http.ResponseWriter, *http.Request) {}).Name()
	if !strings.Contains(name, "UDID-1") {
		t.Fatalf("Name() = %q, want it to carry the udid", name)
	}
	if !strings.Contains(name, "41001") {
		t.Fatalf("Name() = %q, want it to carry the runner port", name)
	}
}

func newTestDriver(t *testing.T, handler http.HandlerFunc) *Driver {
	t.Helper()
	return newTestDriverWithSimctl(t, handler, &recordingRunner{})
}

func newTestDriverWithSimctl(t *testing.T, handler http.HandlerFunc, runner CommandRunner) *Driver {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Timeout = defaultTimeout
	return NewDriver(
		"UDID-1", 41001,
		NewClient(server.URL, WithHTTPClient(httpClient)),
		NewSimctl("UDID-1", runner), nil,
	)
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		t.Error(err)
	}
}

// XCUITest cannot snapshot "the frontmost app" — it needs a bundle id — so the
// runner's viewHierarchy route takes appIds, and an empty list can only mean the
// springboard's own tree, which is the home screen BEHIND whatever is running.
// The driver therefore supplies the app it launched when no filter is present.

func TestTheHierarchyDefaultsToTheAppTheDriverLaunched(t *testing.T) {
	t.Parallel()

	recorder := &appIDRecorder{}
	driver := newTestDriver(t, recorder.handler(t))

	if err := driver.LaunchApp(context.Background(),
		device.LaunchAppRequest{AppID: "com.example.launched"}); err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	if _, err := driver.ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{}); err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}

	sent := recorder.appIDs()
	if len(sent) != 1 || sent[0] != "com.example.launched" {
		t.Fatalf("appIds = %#v, want the launched app so the hierarchy is not the home screen", sent)
	}
}

func TestAnExplicitAppFilterOutranksTheLaunchedApp(t *testing.T) {
	t.Parallel()

	// The control. Always substituting the launched app would ignore a caller
	// that asked about a specific one, which is the whole point of the field.
	recorder := &appIDRecorder{}
	driver := newTestDriver(t, recorder.handler(t))

	if err := driver.LaunchApp(context.Background(),
		device.LaunchAppRequest{AppID: "com.example.launched"}); err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	if _, err := driver.ContentDescriptor(context.Background(),
		device.ContentDescriptorRequest{AppIDs: []string{"com.example.asked"}}); err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}

	sent := recorder.appIDs()
	if len(sent) != 1 || sent[0] != "com.example.asked" {
		t.Fatalf("appIds = %#v, want the caller's own filter", sent)
	}
}

func TestWithNothingLaunchedTheFilterStaysEmpty(t *testing.T) {
	t.Parallel()

	// The other control. Inventing a bundle id would make the runner refuse with
	// "not in the foreground" instead of answering about the home screen, which
	// is a real answer when nothing has been launched.
	recorder := &appIDRecorder{}
	if _, err := newTestDriver(t, recorder.handler(t)).ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{}); err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if sent := recorder.appIDs(); len(sent) != 0 {
		t.Fatalf("appIds = %#v, want it empty when nothing was launched", sent)
	}
}

// appIDRecorder captures the app filter the driver put on the wire for the last
// /viewHierarchy request, and answers with a hierarchy small enough to decode.
type appIDRecorder struct {
	mu     sync.Mutex
	sent   []string
	seenAt bool
}

func (recorder *appIDRecorder) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/runningApp" {
			// Whatever the driver launched is in front at once here.
			var body struct {
				AppIDs []string `json:"appIds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.AppIDs) == 0 {
				t.Errorf("decoding %s: %v %v", request.URL.Path, err, body)
			}
			writeJSON(t, writer, map[string]string{"runningAppBundleId": body.AppIDs[0]})
			return
		}
		if request.URL.Path == "/viewHierarchy" {
			var body struct {
				AppIDs []string `json:"appIds"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decoding %s: %v", request.URL.Path, err)
			}
			recorder.mu.Lock()
			recorder.sent = body.AppIDs
			recorder.seenAt = true
			recorder.mu.Unlock()
			writeJSON(t, writer, map[string]any{
				"axElement": map[string]any{
					"identifier": "root", "label": "root", "elementType": 0, "enabled": true,
					"horizontalSizeClass": 0, "verticalSizeClass": 0, "selected": false,
					"hasFocus": false, "windowContextID": 0, "displayID": 0,
					"frame": map[string]any{"X": 0, "Y": 0, "Width": 10, "Height": 10},
				},
				"depth": 1,
			})
			return
		}
		writeJSON(t, writer, map[string]any{})
	}
}

func (recorder *appIDRecorder) appIDs() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.sent
}

func TestALaunchThatFailedIsNotRemembered(t *testing.T) {
	t.Parallel()

	// Remembering a launch that did not happen makes every later hierarchy
	// request name an app that is not running, and the runner answers that with
	// "not in the foreground" — an error about the wrong thing entirely, instead
	// of the home screen, which is the truth.
	recorder := &appIDRecorder{}
	driver := newTestDriverWithSimctl(t, recorder.handler(t),
		&recordingRunner{output: []byte("Invalid device: UDID-1"), err: errors.New("exit status 164")})

	if err := driver.LaunchApp(context.Background(),
		device.LaunchAppRequest{AppID: "com.example.never"}); err == nil {
		t.Fatal("LaunchApp() accepted a failing simctl launch")
	}
	if _, err := driver.ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{}); err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if sent := recorder.appIDs(); len(sent) != 0 {
		t.Fatalf("appIds = %#v, want nothing remembered from a failed launch", sent)
	}
}

// fakeIOSRecorder stands in for the long-lived simctl recorder so the
// start/stop wiring is testable without a booted simulator.
type fakeIOSRecorder struct {
	stopped bool
	stopErr error
}

type fakeIOSLogProcess struct {
	stopped bool
	stopErr error
	// gone, when set, tells the driver the child is already dead.
	gone chan struct{}
}

func (process *fakeIOSLogProcess) exited() <-chan struct{} { return process.gone }

func (process *notifyingIOSLogProcess) exited() <-chan struct{} { return nil }

type notifyingIOSLogProcess struct {
	once    sync.Once
	stopped chan struct{}
}

func (process *notifyingIOSLogProcess) stop(context.Context) error {
	process.once.Do(func() { close(process.stopped) })
	return nil
}

func (process *fakeIOSLogProcess) stop(context.Context) error {
	process.stopped = true
	return process.stopErr
}

func (r *fakeIOSRecorder) stop(context.Context) error {
	r.stopped = true
	return r.stopErr
}

func TestScreenRecordingStartsAndStopsCleanly(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md line 9: startScreenRecording(sink). The recorder
	// writes straight to the sink and finalizes the .mov on stop, so the artifact
	// must be at the sink once StopScreenRecording returns — and the recorder must
	// not be interrupted before that.
	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	var gotArgs []string
	rec := &fakeIOSRecorder{}
	driver.spawnRecorder = func(_ context.Context, args []string) (screenRecorder, error) {
		gotArgs = args
		return rec, nil
	}

	output := filepath.Join(t.TempDir(), "out.mov")
	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: output})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	wantArgs := []string{"simctl", "io", "UDID-1", "recordVideo", output}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("recorder argv = %#v, want %#v", gotArgs, wantArgs)
	}
	if rec.stopped {
		t.Fatal("the recorder was stopped before StopScreenRecording was called")
	}
	if err := os.WriteFile(output, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}

	artifacts, err := driver.StopScreenRecording(context.Background(), id)
	if err != nil {
		t.Fatalf("StopScreenRecording() error = %v", err)
	}
	if !rec.stopped {
		t.Fatal("StopScreenRecording did not interrupt the recorder")
	}
	if len(artifacts) != 1 || artifacts[0].Path != output {
		t.Fatalf("artifacts = %#v, want one at the sink path", artifacts)
	}
}

func TestStopScreenRecordingRejectsAnUnknownCapture(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	if _, err := driver.StopScreenRecording(
		context.Background(), device.CaptureID("never-started")); err == nil {
		t.Fatal("StopScreenRecording accepted a capture id it never handed out")
	}
}

func TestScreenRecordingRejectsAnUnsafeSink(t *testing.T) {
	t.Parallel()

	// The sink is flow-controlled input passed as an argv to `simctl io
	// recordVideo`; a leading '-' (argv injection) or an unsafe basename must be
	// refused before any child spawns.
	for _, sink := range []string{"-x.mov", "/tmp/-x.mov", "/tmp/x; reboot.mov", ""} {
		driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
		spawned := false
		driver.spawnRecorder = func(context.Context, []string) (screenRecorder, error) {
			spawned = true
			return &fakeIOSRecorder{}, nil
		}
		if _, err := driver.StartScreenRecording(
			context.Background(), device.ScreenRecordingRequest{OutputPath: sink}); err == nil {
			t.Errorf("StartScreenRecording accepted an unsafe sink %q", sink)
		}
		if spawned {
			t.Errorf("spawned a recorder for an unsafe sink %q", sink)
		}
	}
}

func TestStopScreenRecordingSurfacesAStopFailure(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	rec := &fakeIOSRecorder{stopErr: errors.New("interrupt refused")}
	driver.spawnRecorder = func(context.Context, []string) (screenRecorder, error) {
		return rec, nil
	}
	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: "/tmp/out.mov"})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	if _, err := driver.StopScreenRecording(context.Background(), id); err == nil {
		t.Fatal("StopScreenRecording swallowed the recorder's stop failure")
	}
}

func TestStopScreenRecordingRejectsAnEmptyArtifact(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	output := filepath.Join(t.TempDir(), "empty.mov")
	driver.spawnRecorder = func(context.Context, []string) (screenRecorder, error) {
		return &fakeIOSRecorder{}, nil
	}
	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: output})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	if err := os.WriteFile(output, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.StopScreenRecording(context.Background(), id); err == nil {
		t.Fatal("StopScreenRecording accepted an empty artifact")
	}
}

func TestCloseDrainsOutstandingRecordingsIdempotently(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	first := &fakeIOSRecorder{}
	second := &fakeIOSRecorder{}
	recorders := []screenRecorder{first, second}
	driver.spawnRecorder = func(context.Context, []string) (screenRecorder, error) {
		recorder := recorders[0]
		recorders = recorders[1:]
		return recorder, nil
	}
	for _, name := range []string{"first.mov", "second.mov"} {
		output := filepath.Join(t.TempDir(), name)
		if _, err := driver.StartScreenRecording(
			context.Background(), device.ScreenRecordingRequest{OutputPath: output}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, []byte("video"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !first.stopped || !second.stopped {
		t.Fatalf("Close() stopped first=%v second=%v, want both", first.stopped, second.stopped)
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestRecorderCleanupKillsAndBoundedlyReapsAStuckChild(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	killed := make(chan struct{}, 1)
	child := &iosLocalChild{
		done:   done,
		signal: func(os.Signal) error { return nil },
		kill: func() error {
			killed <- struct{}{}
			done <- nil
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := stopIOSLocalChild(ctx, child)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stopIOSLocalChild() error = %v, want deadline exceeded", err)
	}
	if time.Since(started) > time.Second {
		t.Fatalf("child cleanup exceeded its deadline by too much: %v", time.Since(started))
	}
	select {
	case <-killed:
	default:
		t.Fatal("stuck iOS recorder child was not killed")
	}
}

func TestDeviceLogCaptureStartsStopsAndReturnsANonEmptyArtifact(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	outputDirectory := t.TempDir()
	process := &fakeIOSLogProcess{}
	var gotArgs []string
	driver.spawnDeviceLog = func(_ context.Context, args []string, output io.Writer) (deviceLogProcess, error) {
		gotArgs = append([]string(nil), args...)
		if _, err := io.WriteString(output, "{\"eventMessage\":\"probe\"}\n"); err != nil {
			return nil, err
		}
		return process, nil
	}
	id, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: outputDirectory})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture() error = %v", err)
	}
	wantArgs := []string{"simctl", "spawn", "UDID-1", "log", "stream", "--style", "ndjson"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("log argv = %#v, want %#v", gotArgs, wantArgs)
	}
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	if !process.stopped {
		t.Fatal("StopDeviceLogCapture did not stop the log child")
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "log" || artifacts[0].Path == "" {
		t.Fatalf("artifacts = %#v, want one log", artifacts)
	}
	resolvedOutputDirectory, err := filepath.EvalSymlinks(outputDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(artifacts[0].Path) != resolvedOutputDirectory {
		t.Fatalf("artifact path = %q, want it under %q", artifacts[0].Path, outputDirectory)
	}
}

func TestDeviceLogCaptureRejectsUnknownEmptyAndFailedCaptures(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	if _, err := driver.StopDeviceLogCapture(context.Background(), "unknown"); err == nil {
		t.Fatal("StopDeviceLogCapture accepted an unknown id")
	}
	// A stream that never writes is a failed start, not a capture that fails
	// later at the stop (issue #28): the file is removed and the slot freed.
	driver.deviceLogStartWait = 50 * time.Millisecond
	silent := &fakeIOSLogProcess{}
	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		return silent, nil
	}
	directory := t.TempDir()
	if _, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: directory}); err == nil ||
		!strings.Contains(err.Error(), "wrote nothing within") {
		t.Fatalf("StartDeviceLogCapture error = %v, want the silent-stream failure", err)
	}
	if !silent.stopped {
		t.Fatal("a silent stream was left running")
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("silent stream left files %v (err %v), want none", entries, err)
	}

	// A child that dies before its first byte names the exit.
	dead := &fakeIOSLogProcess{gone: make(chan struct{}), stopErr: errors.New("exit status 1")}
	close(dead.gone)
	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		return dead, nil
	}
	if _, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()}); err == nil ||
		!strings.Contains(err.Error(), "exited before writing") || !strings.Contains(err.Error(), "exit status 1") {
		t.Fatalf("StartDeviceLogCapture error = %v, want the dead-child failure with its exit", err)
	}

	// After the stream is up, a child failure still fails the stop.
	driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
		_, _ = io.WriteString(output, "Filtering the log data using \"process == \"Fixture\"\"\n")
		return &fakeIOSLogProcess{stopErr: errors.New("log child failed")}, nil
	}
	id, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil ||
		!strings.Contains(err.Error(), "log child failed") {
		t.Fatalf("StopDeviceLogCapture error = %v, want child failure", err)
	}
}

// A capture stopped right after it started keeps the stream's header as its
// small file: the quiet-application case spec 05 §10 promises.
func TestDeviceLogCaptureStoppedAtOnceKeepsTheHeader(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	header := "Filtering the log data using \"process == \"Fixture\"\"\n"
	driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_, _ = io.WriteString(output, header)
		}()
		return &fakeIOSLogProcess{}, nil
	}
	id, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture() error = %v", err)
	}
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	data, err := os.ReadFile(artifacts[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != header {
		t.Fatalf("artifact = %q, want the header the stream wrote before the stop", data)
	}
}

// realIOSDeviceLog against a shell child: the start returns once the child
// has written, the stop returns promptly, and a grandchild holding the output
// pipe cannot hold the stop past the pipe delay (issue #28's hang).
func TestRealIOSDeviceLogStartsOnFirstBytesAndBoundsTheStop(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell child")
	}

	for _, tc := range []struct {
		name   string
		script string
	}{
		{"child", "echo header; sleep 30"},
		{"grandchild holds the pipe", "echo header; sleep 30 & sleep 30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output := newIOSLogWriter(io.Discard, 1<<20)
			process, err := realIOSDeviceLog(context.Background(), []string{"sh", "-c", tc.script}, output)
			if err != nil {
				t.Fatalf("realIOSDeviceLog() error = %v", err)
			}
			child := process.(*execDeviceLogProcess)
			select {
			case <-output.ready:
			case <-time.After(5 * time.Second):
				t.Fatal("the child wrote nothing")
			}
			// The shell ignores nothing here: SIGINT ends it. A stop context far
			// longer than the pipe delay proves the bound comes from WaitDelay.
			stopContext, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			stopStarted := time.Now()
			if err := process.stop(stopContext); err != nil {
				t.Fatalf("stop() error = %v", err)
			}
			if took := time.Since(stopStarted); took > iosDeviceLogPipeDelay+2*time.Second {
				t.Fatalf("stop took %s, want it bounded by the pipe delay", took)
			}
			select {
			case <-child.gone:
			case <-time.After(time.Second):
				t.Fatal("exited() never closed after the stop")
			}
		})
	}
}

func TestDeviceLogCaptureSpawnFailureRemovesThePartialArtifact(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		return nil, errors.New("spawn refused")
	}
	if _, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: directory}); err == nil {
		t.Fatal("StartDeviceLogCapture swallowed a spawn failure")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spawn failure left partial artifacts: %v", entries)
	}
}

func TestDeviceLogCaptureHonorsCancellationAndCloseDrains(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	driver.spawnDeviceLog = func(ctx context.Context, _ []string, _ io.Writer) (deviceLogProcess, error) {
		return nil, ctx.Err()
	}
	if _, err := driver.StartDeviceLogCapture(
		cancelled, device.DeviceLogRequest{OutputDirectory: t.TempDir()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("StartDeviceLogCapture error = %v, want context.Canceled", err)
	}

	first := &fakeIOSLogProcess{}
	second := &fakeIOSLogProcess{}
	processes := []deviceLogProcess{first, second}
	driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
		if _, err := io.WriteString(output, "log\n"); err != nil {
			return nil, err
		}
		process := processes[0]
		processes = processes[1:]
		return process, nil
	}
	for range 2 {
		if _, err := driver.StartDeviceLogCapture(
			context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !first.stopped || !second.stopped {
		t.Fatalf("Close stopped first=%v second=%v, want both", first.stopped, second.stopped)
	}
	if err := driver.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestDeviceLogCaptureEnforcesActiveAndByteLimits(t *testing.T) {
	t.Parallel()

	t.Run("active capture limit", func(t *testing.T) {
		driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
		driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
			_, _ = io.WriteString(output, "log\n")
			return &fakeIOSLogProcess{}, nil
		}
		for range maxActiveIOSDeviceLogs {
			if _, err := driver.StartDeviceLogCapture(
				context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := driver.StartDeviceLogCapture(
			context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()}); err == nil {
			t.Fatal("StartDeviceLogCapture exceeded the active-capture limit")
		}
		if err := driver.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("byte overflow stops and removes partial output", func(t *testing.T) {
		directory := t.TempDir()
		driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
		driver.deviceLogByteLimit = 8
		process := &fakeIOSLogProcess{}
		driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
			_, _ = io.WriteString(output, "0123456789")
			return process, nil
		}
		if _, err := driver.StartDeviceLogCapture(
			context.Background(), device.DeviceLogRequest{OutputDirectory: directory}); !errors.Is(err, errIOSDeviceLogLimit) {
			t.Fatalf("StartDeviceLogCapture error = %v, want byte-limit error", err)
		}
		if !process.stopped {
			t.Fatal("overflow did not terminate the iOS log child")
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("overflow left partial log files: %v", entries)
		}
	})

	t.Run("asynchronous overflow terminates the active child", func(t *testing.T) {
		directory := t.TempDir()
		driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
		driver.deviceLogByteLimit = 8
		process := &notifyingIOSLogProcess{stopped: make(chan struct{})}
		var logOutput io.Writer
		driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
			logOutput = output
			_, _ = io.WriteString(output, "log\n")
			return process, nil
		}
		id, err := driver.StartDeviceLogCapture(
			context.Background(), device.DeviceLogRequest{OutputDirectory: directory})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(logOutput, "0123456789"); !errors.Is(err, errIOSDeviceLogLimit) {
			t.Fatalf("log write error = %v, want byte-limit error", err)
		}
		select {
		case <-process.stopped:
		case <-time.After(time.Second):
			t.Fatal("overflow did not stop the active iOS log child")
		}
		if _, err := driver.StopDeviceLogCapture(context.Background(), id); !errors.Is(err, errIOSDeviceLogLimit) {
			t.Fatalf("StopDeviceLogCapture error = %v, want persisted byte-limit error", err)
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("asynchronous overflow left partial log files: %v", entries)
		}
	})
}

func TestCrashArtifactsRunBoundedDiagnoseAndValidateResults(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	runner.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		for _, argument := range args {
			if strings.HasPrefix(argument, "--output=") {
				path := strings.TrimPrefix(argument, "--output=")
				if err := os.WriteFile(filepath.Join(path, "diagnose.tar.gz"), []byte("archive"), 0o600); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	artifacts, err := driver.CollectCrashArtifacts(
		context.Background(), device.ArtifactRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("CollectCrashArtifacts() error = %v", err)
	}
	if len(artifacts) != 1 || artifacts[0].Kind != "crash" ||
		filepath.Base(artifacts[0].Path) != "diagnose.tar.gz" {
		t.Fatalf("artifacts = %#v, want diagnose.tar.gz crash artifact", artifacts)
	}
	if len(runner.calls) != 1 || !slices.Contains(runner.calls[0], "-b") {
		t.Fatalf("diagnose calls = %#v, want one background-safe invocation", runner.calls)
	}
}

func TestCrashArtifactFailureRemovesThePartialDiagnoseDirectory(t *testing.T) {
	t.Parallel()

	outputDirectory := t.TempDir()
	var diagnoseDirectory string
	runner := &recordingRunner{run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("diagnose context has no deadline")
		}
		for _, argument := range args {
			if strings.HasPrefix(argument, "--output=") {
				diagnoseDirectory = strings.TrimPrefix(argument, "--output=")
				if err := os.WriteFile(filepath.Join(diagnoseDirectory, "partial.tar.gz"), []byte("partial"), 0o600); err != nil {
					return nil, err
				}
			}
		}
		return nil, errors.New("diagnose failed")
	}}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	if _, err := driver.CollectCrashArtifacts(
		context.Background(), device.ArtifactRequest{OutputDirectory: outputDirectory}); err == nil {
		t.Fatal("CollectCrashArtifacts swallowed diagnose failure")
	}
	if diagnoseDirectory == "" {
		t.Fatal("diagnose was not invoked")
	}
	if _, err := os.Stat(diagnoseDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed diagnose directory still exists: %v", err)
	}
}

func TestCrashArtifactsRejectEmptyResultsAndAppSpecificPretence(t *testing.T) {
	t.Parallel()

	driver := newTestDriverWithSimctl(
		t, func(http.ResponseWriter, *http.Request) {}, &recordingRunner{})
	if _, err := driver.CollectCrashArtifacts(
		context.Background(), device.ArtifactRequest{OutputDirectory: t.TempDir()}); err == nil {
		t.Fatal("CollectCrashArtifacts accepted an empty diagnose result")
	}
	if _, err := driver.CollectCrashArtifacts(context.Background(), device.ArtifactRequest{
		OutputDirectory: t.TempDir(), AppID: "com.example.a",
	}); !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("app-filtered CollectCrashArtifacts error = %v, want ErrUnsupported", err)
	}
}

func TestCrashArtifactQuotasRejectFilesCountsAndCombinedBytes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		files map[string]string
		quota iosArtifactQuota
	}{
		{"per file", map[string]string{"large.log": "12345"}, iosArtifactQuota{2, 4, 10}},
		{"file count", map[string]string{"a.log": "1", "b.log": "2"}, iosArtifactQuota{1, 4, 10}},
		{"combined bytes", map[string]string{"a.log": "123", "b.log": "456"}, iosArtifactQuota{2, 4, 5}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			for name, contents := range test.files {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := collectIOSCrashArtifacts(directory, test.quota); err == nil {
				t.Fatalf("collectIOSCrashArtifacts accepted quota violation %#v", test)
			}
		})
	}
}

func TestCrashArtifactOverflowCancelsDiagnoseAndRemovesPartialOutput(t *testing.T) {
	t.Parallel()

	outputDirectory := t.TempDir()
	cancelled := make(chan struct{})
	runner := &recordingRunner{run: func(ctx context.Context, _ string, args ...string) ([]byte, error) {
		for _, argument := range args {
			if strings.HasPrefix(argument, "--output=") {
				path := strings.TrimPrefix(argument, "--output=")
				if err := os.WriteFile(filepath.Join(path, "too-large.log"), []byte("12345"), 0o600); err != nil {
					return nil, err
				}
			}
		}
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	driver.diagnosticQuota = iosArtifactQuota{maximumFiles: 2, maximumFileBytes: 4, maximumAllBytes: 10}
	if _, err := driver.CollectCrashArtifacts(
		context.Background(), device.ArtifactRequest{OutputDirectory: outputDirectory}); err == nil {
		t.Fatal("CollectCrashArtifacts accepted an over-quota diagnose")
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("quota overflow did not cancel the running diagnose command")
	}
	entries, err := os.ReadDir(outputDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quota overflow left partial diagnose output: %v", entries)
	}
}

func TestCrashArtifactCaptureEnforcesTheConcurrentLimit(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	runner := &recordingRunner{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		for _, argument := range args {
			if strings.HasPrefix(argument, "--output=") {
				path := strings.TrimPrefix(argument, "--output=")
				if err := os.WriteFile(filepath.Join(path, "diagnose.log"), []byte("ok"), 0o600); err != nil {
					return nil, err
				}
			}
		}
		close(started)
		<-release
		return nil, nil
	}}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	firstResult := make(chan error, 1)
	go func() {
		_, err := driver.CollectCrashArtifacts(
			context.Background(), device.ArtifactRequest{OutputDirectory: t.TempDir()})
		firstResult <- err
	}()
	<-started
	if _, err := driver.CollectCrashArtifacts(
		context.Background(), device.ArtifactRequest{OutputDirectory: t.TempDir()}); err == nil {
		t.Fatal("a second concurrent diagnose capture was accepted")
	}
	close(release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first diagnose capture error = %v", err)
	}
}

func TestRecordingStartupHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := realIOSRecorder(ctx, []string{"simctl", "io", "no-device", "recordVideo", "out.mov"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("realIOSRecorder() error = %v, want context.Canceled", err)
	}
}

// simctl must reach its capture loop before an interrupt means "finalize"
// rather than "stop during startup". The readiness line marks that boundary:
//
//	$ xcrun simctl io UDID recordVideo probe.mp4
//	Recording started
//	^C
//	Recording completed. Writing to disk.
//	Wrote video to: probe.mp4
func TestTheRecorderWaitsUntilTheSimulatorIsActuallyRecording(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader("some preamble\nRecording started\n")
	if err := awaitRecordingStarted(reader); err != nil {
		t.Fatalf("awaitRecordingStarted() error = %v", err)
	}
}

// A recorder that exits without ever recording must be reported, not waited on
// forever: `simctl io` on a shut-down device prints its complaint and leaves.
func TestARecorderThatNeverStartsIsReported(t *testing.T) {
	t.Parallel()

	reader := strings.NewReader("Invalid device state\n")
	err := awaitRecordingStarted(reader)
	if err == nil {
		t.Fatal("a recorder that never started was treated as recording")
	}
	if !strings.Contains(err.Error(), "Invalid device state") {
		t.Fatalf("error = %q, want it to carry what simctl said", err)
	}
}

func TestRecorderStartupWaitIsCancellableAndBounded(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
		timeout time.Duration
		want    error
	}{
		{
			name: "cancellation",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			timeout: time.Second,
			want:    context.Canceled,
		},
		{
			name: "timeout",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			timeout: time.Millisecond,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			defer writer.Close()
			ctx, cancel := test.context()
			defer cancel()
			err := awaitRecordingStartedWithin(ctx, reader, test.timeout)
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if test.want == nil && (err == nil || !strings.Contains(err.Error(), "timed out")) {
				t.Fatalf("error = %v, want timeout", err)
			}
		})
	}
}

// The interrupt finishes a recording, so the resulting process exit is not a
// failure.
func TestStoppingARecorderDoesNotReportTheInterruptAsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX SIGINT recorder shutdown contract")
	}
	t.Parallel()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the stand-in recorder: %v", err)
	}
	recorder := &execRecorder{child: newIOSLocalChild(cmd)}
	if err := recorder.stop(context.Background()); err != nil {
		t.Fatalf("stop() error = %v, want the interrupt to be the ordinary ending", err)
	}
}

func TestBoundsFloorAfterSinglePrecision(t *testing.T) {
	t.Parallel()

	// The frames below cover representative Settings hierarchy nodes.
	//
	// Not cosmetic: below/above/leftOf/rightOf check these numbers, so a
	// coordinate on the wrong side of a neighbour's edge changes which element
	// a selector resolves to.
	for _, tc := range []struct {
		name  string
		frame Frame
		want  string
	}{{
		// Plain rounding produced [16,121]: the .667 top belongs to the row
		// above it, not below.
		name:  "fraction is floored",
		frame: Frame{X: 15.999999999999986, Y: 120.66666666666667, Width: 129.33333333333331, Height: 39.66666666666667},
		want:  "[16,120][145,160]",
	}, {
		// Plain flooring produced [360,207]: 360.99999999999994 is the
		// simulator's double noise around a whole 361, and truncating it walks
		// the element one pixel left of where the contract puts it.
		name:  "double noise below a whole number is not a fraction",
		frame: Frame{X: 360.99999999999994, Y: 207.33333333333331, Width: 6.6666669845581055, Height: 11.333333969116211},
		want:  "[361,207][367,218]",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			node := convertAXElement(AXElement{Frame: tc.frame})
			if got := node.Attributes["bounds"]; got != tc.want {
				t.Fatalf("bounds = %q, want %q", got, tc.want)
			}
		})
	}
}

// foregroundAfter answers /runningApp with the springboard for the first
// `polls` calls and with appID after that.
func foregroundAfter(t *testing.T, appID string, polls int) http.HandlerFunc {
	t.Helper()
	var calls atomic.Int32
	return func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/runningApp" {
			return
		}
		running := "com.apple.springboard"
		if int(calls.Add(1)) > polls {
			running = appID
		}
		writeJSON(t, writer, map[string]string{"runningAppBundleId": running})
	}
}

// simctl launch returns as soon as the process starts. Until the app is in
// front, the runner serves the springboard's tree for its id, and a session
// that observed right after the launch got the home screen as its start
// screen. LaunchApp therefore returns only once the runner reports the app
// as the foreground one.
func TestLaunchAppWaitsForTheAppToComeToTheForeground(t *testing.T) {
	t.Parallel()
	var polls atomic.Int32
	handler := foregroundAfter(t, "com.example.a", 3)
	counting := func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/runningApp" {
			polls.Add(1)
		}
		handler(writer, request)
	}
	driver := newTestDriverWithSimctl(t, counting, &recordingRunner{})
	if err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	if got := polls.Load(); got != 4 {
		t.Fatalf("polled /runningApp %d times, want 4 (three springboard answers, then the app)", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	never := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 1<<30), &recordingRunner{})
	if err := never.LaunchApp(ctx, device.LaunchAppRequest{AppID: "com.example.a"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("LaunchApp() error = %v, want the context deadline", err)
	}
}

// hideKeyboard must never press Return: on an on-submit field that submits the
// form, in a multiline field it inserts a newline (issue #25). Dismissal only
// goes through a control a person would use — the iPad hide key or the app's
// own keyboard toolbar button — and is verified through /keyboard afterwards.

func TestHideKeyboardDoesNothingWhenNoKeyboardIsUp(t *testing.T) {
	t.Parallel()

	var routes []string
	driver := newTestDriver(t, func(w http.ResponseWriter, r *http.Request) {
		routes = append(routes, r.URL.Path)
		switch r.URL.Path {
		case "/keyboard":
			_, _ = io.WriteString(w, `{"isKeyboardVisible": false}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	})
	if err := driver.HideKeyboard(context.Background()); err != nil {
		t.Fatalf("HideKeyboard: %v", err)
	}
	if slices.Contains(routes, "/pressKey") || slices.Contains(routes, "/touch") {
		t.Fatalf("routes = %v, want no key press or touch when the keyboard is already hidden", routes)
	}
}

func TestHideKeyboardTapsTheKeyboardToolbarButtonAndVerifies(t *testing.T) {
	t.Parallel()

	var touched []TouchRequest
	var visibilityChecks int
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy: hideKeyboardHierarchy(keyboardToolbar{
			frame: Frame{X: 0, Y: 491, Width: 402, Height: 48},
			buttons: []AXElement{{Identifier: "origami.keyboardDone", Label: "Done", ElementType: buttonElementType, Enabled: true,
				Frame: Frame{X: 318, Y: 497, Width: 63, Height: 36}}},
		}),
		visible: func() bool { visibilityChecks++; return len(touched) == 0 },
		touch:   func(request TouchRequest) { touched = append(touched, request) },
	}))
	driver.launchedAppID = "studio.example.origami"
	if err := driver.HideKeyboard(context.Background()); err != nil {
		t.Fatalf("HideKeyboard: %v", err)
	}
	// The point came from the hierarchy, so the tap is anchored in that
	// application's coordinate space, as Tap does for element points.
	if len(touched) != 1 || touched[0].X != 349.5 || touched[0].Y != 515 || touched[0].Duration != nil ||
		touched[0].AppID != "studio.example.origami" {
		t.Fatalf("touches = %#v, want one app-anchored tap on the Done button's center", touched)
	}
	if visibilityChecks < 2 {
		t.Fatalf("visibility checks = %d, want the keyboard to be re-checked after the tap", visibilityChecks)
	}
}

func TestHideKeyboardTapsTheIPadHideKey(t *testing.T) {
	t.Parallel()

	var touched []TouchRequest
	hierarchy := hideKeyboardHierarchy(keyboardToolbar{})
	keyboard := &hierarchy.AXElement.Children[0].Children[1]
	keyboard.Children = append(keyboard.Children, AXElement{
		Identifier: "Hide keyboard", Label: "Hide keyboard", ElementType: buttonElementType, Enabled: true,
		Frame: Frame{X: 340, Y: 760, Width: 50, Height: 50},
	})
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy: hierarchy,
		visible:   func() bool { return len(touched) == 0 },
		touch:     func(request TouchRequest) { touched = append(touched, request) },
	}))
	if err := driver.HideKeyboard(context.Background()); err != nil {
		t.Fatalf("HideKeyboard: %v", err)
	}
	if len(touched) != 1 || touched[0].X != 365 || touched[0].Y != 785 {
		t.Fatalf("touches = %#v, want one tap on the hide key", touched)
	}
}

// A "Done" in the navigation bar closes a sheet, and the keyboard's own
// "return" key is the bug being fixed: neither is a dismiss control. With no
// safe control nothing is tapped; the driver drags the keyboard down instead
// and, when that leaves it up, fails with what the flow can do.
func TestHideKeyboardSwipesWithoutAControlAndFailsWhenTheKeyboardStaysUp(t *testing.T) {
	t.Parallel()

	var routes []string
	var swipes []SwipeV2Request
	hierarchy := hideKeyboardHierarchy(keyboardToolbar{})
	app := &hierarchy.AXElement.Children[0]
	app.Children = append(app.Children, AXElement{
		Identifier: "NavigationBar", ElementType: navigationBarElementType, Frame: Frame{X: 0, Y: 0, Width: 402, Height: 100},
		Children: []AXElement{{Label: "Done", ElementType: buttonElementType, Enabled: true, Frame: Frame{X: 340, Y: 50, Width: 50, Height: 40}}},
	}, AXElement{
		Identifier: "BottomToolbar", ElementType: toolbarElementType, Frame: Frame{X: 0, Y: 830, Width: 402, Height: 44},
		Children: []AXElement{{Label: "Done", ElementType: buttonElementType, Enabled: true, Frame: Frame{X: 340, Y: 832, Width: 50, Height: 40}}},
	})
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy: hierarchy,
		visible:   func() bool { return true },
		touch:     func(TouchRequest) { t.Fatal("touched a control that is not a keyboard dismissal") },
		swipe:     func(request SwipeV2Request) { swipes = append(swipes, request) },
		routes:    &routes,
	}))
	driver.keyboardDismissWait = 30 * time.Millisecond
	err := driver.HideKeyboard(context.Background())
	if err == nil || !strings.Contains(err.Error(), "still visible") ||
		!strings.Contains(err.Error(), "no keyboard toolbar button") {
		t.Fatalf("HideKeyboard error = %v, want the actionable still-visible failure", err)
	}
	if len(swipes) != 1 {
		t.Fatalf("swipes = %#v, want exactly one attempt", swipes)
	}
	if slices.Contains(routes, "/pressKey") {
		t.Fatalf("routes = %v, want no Return press", routes)
	}
}

// Without a control the drag starts in the content just above the keyboard
// and ends at its bottom edge, in the application's coordinate space; the
// dismissal counts only when the text inputs read the same afterwards.
func TestHideKeyboardSwipesTheKeyboardDownAndKeepsTheInputs(t *testing.T) {
	t.Parallel()

	var swipes []SwipeV2Request
	hierarchy := hideKeyboardHierarchy(keyboardToolbar{})
	value := "LocalDraft"
	hierarchy.AXElement.Children[0].Children = append(hierarchy.AXElement.Children[0].Children, AXElement{
		Identifier: "onboarding.name", ElementType: textFieldElementType, Value: &value,
		Frame: Frame{X: 20, Y: 300, Width: 362, Height: 44},
	})
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy: hierarchy,
		visible:   func() bool { return len(swipes) == 0 },
		touch:     func(TouchRequest) { t.Fatal("tapped with no dismiss control on screen") },
		swipe:     func(request SwipeV2Request) { swipes = append(swipes, request) },
	}))
	driver.launchedAppID = "com.example.twoinks"
	if err := driver.HideKeyboard(context.Background()); err != nil {
		t.Fatalf("HideKeyboard: %v", err)
	}
	want := SwipeV2Request{StartX: 201, StartY: 559, EndX: 201, EndY: 816, Duration: 0.3,
		AppIDs: []string{"com.example.twoinks"}}
	if len(swipes) != 1 || !reflect.DeepEqual(swipes[0], want) {
		t.Fatalf("swipes = %#v, want %#v", swipes, want)
	}
}

// A drag can pull a sheet down or refresh a list and still take the
// keyboard with it. The keyboard being gone is not success when the text
// inputs changed.
func TestHideKeyboardFailsWhenTheSwipeChangesTheInputs(t *testing.T) {
	t.Parallel()

	var swipes []SwipeV2Request
	hierarchy := hideKeyboardHierarchy(keyboardToolbar{})
	value := "LocalDraft"
	hierarchy.AXElement.Children[0].Children = append(hierarchy.AXElement.Children[0].Children, AXElement{
		Identifier: "onboarding.name", ElementType: textFieldElementType, Value: &value,
		Frame: Frame{X: 20, Y: 300, Width: 362, Height: 44},
	})
	after := hideKeyboardHierarchy(keyboardToolbar{})
	birthday := ""
	after.AXElement.Children[0].Children = append(after.AXElement.Children[0].Children, AXElement{
		Identifier: "onboarding.birthday", ElementType: textFieldElementType, Value: &birthday,
		Frame: Frame{X: 20, Y: 300, Width: 362, Height: 44},
	})
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy:      hierarchy,
		hierarchyAfter: &after,
		visible:        func() bool { return len(swipes) == 0 },
		touch:          func(TouchRequest) { t.Fatal("tapped with no dismiss control on screen") },
		swipe:          func(request SwipeV2Request) { swipes = append(swipes, request) },
	}))
	err := driver.HideKeyboard(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed the screen") ||
		!strings.Contains(err.Error(), "onboarding.birthday") {
		t.Fatalf("HideKeyboard error = %v, want the changed-inputs failure naming the new field", err)
	}
}

func TestHideKeyboardFailsWhenTheKeyboardStaysUpAfterTheTap(t *testing.T) {
	t.Parallel()

	var touched []TouchRequest
	driver := newTestDriver(t, hideKeyboardRunner(t, hideKeyboardStub{
		hierarchy: hideKeyboardHierarchy(keyboardToolbar{
			frame:   Frame{X: 0, Y: 491, Width: 402, Height: 48},
			buttons: []AXElement{{Label: "Done", ElementType: buttonElementType, Enabled: true, Frame: Frame{X: 318, Y: 497, Width: 63, Height: 36}}},
		}),
		visible: func() bool { return true },
		touch:   func(request TouchRequest) { touched = append(touched, request) },
	}))
	driver.keyboardDismissWait = 30 * time.Millisecond
	err := driver.HideKeyboard(context.Background())
	if err == nil || !strings.Contains(err.Error(), "keyboard is still visible") {
		t.Fatalf("HideKeyboard error = %v, want the still-visible failure", err)
	}
	if len(touched) != 1 {
		t.Fatalf("touches = %#v, want exactly one attempt", touched)
	}
}

type keyboardToolbar struct {
	frame   Frame
	buttons []AXElement
}

// hideKeyboardHierarchy mirrors the AI Origami tree recorded on the Simulator
// for issue #25: an application, an optional keyboard toolbar sitting 44pt
// above the keyboard (the predictive bar lives in between), and a keyboard
// that carries its own "return" button.
func hideKeyboardHierarchy(toolbar keyboardToolbar) ViewHierarchy {
	app := AXElement{ElementType: 2, Frame: Frame{X: 0, Y: 0, Width: 402, Height: 874}}
	if toolbar.frame.Height != 0 {
		app.Children = append(app.Children, AXElement{
			Identifier: "Toolbar", Label: "Toolbar", ElementType: toolbarElementType, Frame: toolbar.frame,
			Children: toolbar.buttons,
		})
	} else {
		app.Children = append(app.Children, AXElement{ElementType: 1})
	}
	app.Children = append(app.Children, AXElement{
		ElementType: keyboardElementType, Frame: Frame{X: 0, Y: 583, Width: 402, Height: 233},
		Children: []AXElement{
			{Label: "Q", ElementType: keyElementType, Frame: Frame{X: 4, Y: 590, Width: 40, Height: 54}},
			{Identifier: "Return", Label: "return", ElementType: buttonElementType, Enabled: true, Frame: Frame{X: 300, Y: 752, Width: 99, Height: 54}},
		},
	})
	return ViewHierarchy{AXElement: AXElement{Children: []AXElement{app}}}
}

type hideKeyboardStub struct {
	hierarchy ViewHierarchy
	// hierarchyAfter, when set, answers every /viewHierarchy read after the
	// first: the screen a dismissal gesture left behind.
	hierarchyAfter *ViewHierarchy
	visible        func() bool
	touch          func(TouchRequest)
	swipe          func(SwipeV2Request)
	routes         *[]string
}

func hideKeyboardRunner(t *testing.T, stub hideKeyboardStub) http.HandlerFunc {
	t.Helper()
	var mu sync.Mutex
	var hierarchyReads int
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if stub.routes != nil {
			*stub.routes = append(*stub.routes, r.URL.Path)
		}
		switch r.URL.Path {
		case "/keyboard":
			_, _ = fmt.Fprintf(w, `{"isKeyboardVisible": %t}`, stub.visible())
		case "/viewHierarchy":
			hierarchyReads++
			hierarchy := stub.hierarchy
			if hierarchyReads > 1 && stub.hierarchyAfter != nil {
				hierarchy = *stub.hierarchyAfter
			}
			_ = json.NewEncoder(w).Encode(hierarchy)
		case "/touch":
			var request TouchRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decoding /touch: %v", err)
			}
			stub.touch(request)
			_, _ = io.WriteString(w, `{}`)
		case "/swipeV2":
			var request SwipeV2Request
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decoding /swipeV2: %v", err)
			}
			if stub.swipe == nil {
				t.Errorf("hideKeyboard swiped with a dismiss control on screen")
			} else {
				stub.swipe(request)
			}
			_, _ = io.WriteString(w, `{}`)
		case "/pressKey":
			t.Errorf("hideKeyboard pressed a key: %s", r.URL.Path)
			_, _ = io.WriteString(w, `{}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}
}

// The runner refuses a key outside the contract enum, so every value the
// host is willing to put on the wire has to be inside it. Session mmx25
// spent a scenario on `unsupported key enter (HTTP 400)`: "enter" is in the
// contract and the runner's own table had missed it. Both sides now read
// the contract, and this pins the host half against the file itself.
func TestEveryKeyTheHostSendsIsInTheContractEnum(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "v0", "ios-http.json"))
	if err != nil {
		t.Fatalf("read the iOS contract: %v", err)
	}
	var contract struct {
		Schemas map[string]struct {
			Fields map[string]struct {
				Enum []string `json:"enum"`
			} `json:"fields"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("decode the iOS contract: %v", err)
	}
	allowed := contract.Schemas["PressKeyRequest"].Fields["key"].Enum
	if len(allowed) == 0 {
		t.Fatal("the contract declares no pressKey enum, so this test proves nothing")
	}
	for code, key := range keyCodes {
		if !slices.Contains(allowed, string(key)) {
			t.Fatalf("keyCodes[%q] sends %q, which the contract enum %v does not allow", code, key, allowed)
		}
	}
	// Negative control: the check can fail.
	if slices.Contains(allowed, "f13") {
		t.Fatal("the contract allows a key it should not, so the check above cannot fail")
	}
}

// Settling is the engine's decision (internal/engine/settle.go wants two
// consecutive equal hierarchy samples). The driver's only job is to hand it a sample.
// Gating that sample on a pixel diff starved the engine on any screen
// with a decorative animation: the accessibility tree was stable, nothing was
// ever sampled, and launchApp failed after ten attempts (issue #7).

func TestWaitForAppToSettleSamplesTheHierarchyWhileThePixelsMove(t *testing.T) {
	t.Parallel()

	var staticProbes atomic.Int32
	driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/isScreenStatic":
			staticProbes.Add(1)
			writeJSON(t, writer, map[string]bool{"isScreenStatic": false})
		case "/viewHierarchy":
			writeJSON(t, writer, settleFixtureHierarchy())
		default:
			t.Errorf("unexpected request %s", request.URL.Path)
		}
	})

	hierarchy, err := driver.WaitForAppToSettle(context.Background(),
		device.SettleRequest{AppID: "com.example.animated"})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	if hierarchy == nil {
		t.Fatal("WaitForAppToSettle() = nil while the pixels move; the engine needs the hierarchy sample to settle on")
	}
	if got := hierarchy.Root.Attributes["id"]; got != "fixture.continue" {
		t.Fatalf("root id = %q, want the sampled tree", got)
	}
	if got := staticProbes.Load(); got != 0 {
		t.Fatalf("/isScreenStatic requests = %d, want none: pixel motion is waitForAnimationToEnd's signal, not settle's", got)
	}
}

func TestWaitForAppToSettleStaysFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("a hierarchy the runner cannot capture is an error, not a sample", func(t *testing.T) {
		t.Parallel()
		driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusInternalServerError)
			writeJSON(t, writer, map[string]any{"errorMessage": "the accessibility hierarchy could not be captured", "errorCode": "internal"})
		})
		hierarchy, err := driver.WaitForAppToSettle(context.Background(), device.SettleRequest{AppID: "com.example.app"})
		if err == nil {
			t.Fatal("WaitForAppToSettle() succeeded on a failed capture")
		}
		if hierarchy != nil {
			t.Fatalf("hierarchy = %#v, want nil alongside the error", hierarchy)
		}
	})

	t.Run("cancellation wins over the sample", func(t *testing.T) {
		t.Parallel()
		driver := newTestDriver(t, func(writer http.ResponseWriter, request *http.Request) {
			writeJSON(t, writer, settleFixtureHierarchy())
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{AppID: "com.example.app"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForAppToSettle() error = %v, want context.Canceled", err)
		}
	})
}

// settleFixtureHierarchy is the smallest tree the driver decodes: one enabled
// button that stays put while something invisible to accessibility animates.
func settleFixtureHierarchy() map[string]any {
	return map[string]any{
		"axElement": map[string]any{
			"identifier": "fixture.continue", "label": "Continue", "elementType": 9, "enabled": true,
			"horizontalSizeClass": 0, "verticalSizeClass": 0, "selected": false,
			"hasFocus": false, "windowContextID": 0, "displayID": 0,
			"frame": map[string]any{"X": 20, "Y": 741, "Width": 362, "Height": 52},
		},
		"depth": 1,
	}
}

// A flow with an application gets its unified log filtered to that process,
// exactly (`--process` would match any prefix), and the artifact says so.
func TestDeviceLogCaptureFiltersTheFlowApplicationByProcess(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("{\n    CFBundleExecutable = Fixture;\n}\n")}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	var gotArgs []string
	driver.spawnDeviceLog = func(_ context.Context, args []string, output io.Writer) (deviceLogProcess, error) {
		gotArgs = append([]string(nil), args...)
		_, err := io.WriteString(output, "{\"process\":\"Fixture\"}\n")
		return &fakeIOSLogProcess{}, err
	}
	id, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "dev.example.fixture"})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture() error = %v", err)
	}
	wantArgs := []string{"simctl", "spawn", "UDID-1", "log", "stream", "--style", "ndjson",
		"--predicate", `process == "Fixture"`}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("log argv = %#v, want %#v", gotArgs, wantArgs)
	}
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	want := map[string]string{"source": "unified-log", "scope": "app", "appId": "dev.example.fixture", "process": "Fixture"}
	if len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0].Metadata, want) {
		t.Fatalf("artifacts = %#v, want one with metadata %#v", artifacts, want)
	}
}

func TestDeviceLogCaptureWithoutAnApplicationIsDeviceWideAndSaysSo(t *testing.T) {
	t.Parallel()

	driver := newTestDriver(t, func(http.ResponseWriter, *http.Request) {})
	driver.spawnDeviceLog = func(_ context.Context, _ []string, output io.Writer) (deviceLogProcess, error) {
		_, err := io.WriteString(output, "{\"process\":\"SpringBoard\"}\n")
		return &fakeIOSLogProcess{}, err
	}
	id, err := driver.StartDeviceLogCapture(context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("StartDeviceLogCapture() error = %v", err)
	}
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	want := map[string]string{"source": "unified-log", "scope": "device"}
	if len(artifacts) != 1 || !reflect.DeepEqual(artifacts[0].Metadata, want) {
		t.Fatalf("artifacts = %#v, want one with metadata %#v", artifacts, want)
	}
}

// An application that is not installed is an error before any log child
// starts: a filter for a process that cannot exist would capture nothing and
// look like a quiet app.
func TestDeviceLogCaptureRefusesAnUnknownApplication(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{err: errors.New("exit status 3"), output: []byte("no such bundle")}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	spawned := false
	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		spawned = true
		return &fakeIOSLogProcess{}, nil
	}
	_, err := driver.StartDeviceLogCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "dev.example.missing"})
	if err == nil || errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("StartDeviceLogCapture() error = %v, want a plain failure, not unsupported", err)
	}
	if spawned {
		t.Fatal("a log child was started for an unknown application")
	}
}

// stdioCaptureFiles returns the --stdout / --stderr paths of the last simctl
// launch and fails when the launch was not a stdio-bound one.
func stdioCaptureFiles(t *testing.T, call []string) (string, string) {
	t.Helper()
	want := []string{"xcrun", "simctl", "launch", "--terminate-running-process"}
	if len(call) < 8 || !reflect.DeepEqual(call[:4], want) ||
		!strings.HasPrefix(call[4], "--stdout=") || !strings.HasPrefix(call[5], "--stderr=") {
		t.Fatalf("launch argv = %#v, want a stdio-bound simctl launch", call)
	}
	for _, path := range []string{call[4][len("--stdout="):], call[5][len("--stderr="):]} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("simctl does not create %s itself, the driver must: %v", path, err)
		}
	}
	return call[4][len("--stdout="):], call[5][len("--stderr="):]
}

func TestStdioCaptureBindsEveryLaunchOfTheFlowAppAndMergesThemOnStop(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 0), runner)
	directory := t.TempDir()
	id, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: directory, AppID: "com.example.a"})
	if err != nil {
		t.Fatalf("StartStdioCapture() error = %v", err)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolving the run directory: %v", err)
	}
	if filepath.Dir(string(id)) != resolvedDirectory {
		t.Fatalf("capture id %q is not under the run directory %q", id, resolvedDirectory)
	}
	// Another application's launch is a plain launch and leaves the capture pending.
	if err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.other"}); err == nil {
		t.Fatal("LaunchApp(other) succeeded although the runner reports only com.example.a in front")
	}
	if want := []string{"xcrun", "simctl", "launch", "UDID-1", "com.example.other"}; !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("other app launch argv = %#v, want plain %#v", runner.calls[0], want)
	}
	err = driver.LaunchApp(context.Background(), device.LaunchAppRequest{
		AppID: "com.example.a", Arguments: []device.LaunchArgument{{Key: "mode", Value: "probe", Type: "string"}},
	})
	if err != nil {
		t.Fatalf("LaunchApp(first) error = %v", err)
	}
	firstOut, firstErr := stdioCaptureFiles(t, runner.calls[1])
	if tail := runner.calls[1][6:]; !reflect.DeepEqual(tail, []string{"UDID-1", "com.example.a", "-mode", "probe"}) {
		t.Fatalf("launch argv tail = %#v, want the udid, bundle and rendered arguments", tail)
	}
	writeFile(t, firstOut, "P1 phase=menu\n")
	writeFile(t, firstErr, "warning one")
	if err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("LaunchApp(second) error = %v", err)
	}
	secondOut, secondErr := stdioCaptureFiles(t, runner.calls[2])
	if secondOut == firstOut || secondErr == firstErr {
		t.Fatalf("second launch reused the first segment files %q %q", secondOut, secondErr)
	}
	writeFile(t, secondOut, "P1 phase=menu again\n")

	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	wantMetadata := map[string]string{"source": "stdio", "scope": "app", "appId": "com.example.a"}
	if len(artifacts) != 1 || artifacts[0].Kind != "log" || artifacts[0].Path != string(id) ||
		!reflect.DeepEqual(artifacts[0].Metadata, wantMetadata) {
		t.Fatalf("artifacts = %#v, want one log at %q with %#v", artifacts, id, wantMetadata)
	}
	merged, err := os.ReadFile(string(id))
	if err != nil {
		t.Fatalf("reading the merged capture: %v", err)
	}
	want := "### launch 1 stdout\nP1 phase=menu\n### launch 1 stderr\nwarning one\n### launch 2 stdout\nP1 phase=menu again\n"
	if string(merged) != want {
		t.Fatalf("merged capture =\n%s\nwant\n%s", merged, want)
	}
	for _, path := range []string{firstOut, firstErr, secondOut, secondErr} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("segment %s survived the merge: %v", path, err)
		}
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
		t.Fatal("second stop of the same capture succeeded")
	}
}

func TestAStdioCaptureWithoutALaunchFailsTheStopAndLeavesNoFile(t *testing.T) {
	t.Parallel()

	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, &recordingRunner{})
	id, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"})
	if err != nil {
		t.Fatalf("StartStdioCapture() error = %v", err)
	}
	if _, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"}); err == nil {
		t.Fatal("a second stdio capture opened beside the first")
	}
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if !errors.Is(err, ErrStdioCaptureUnused) || artifacts != nil {
		t.Fatalf("StopDeviceLogCapture() = %#v, %v; want ErrStdioCaptureUnused", artifacts, err)
	}
	if _, err := os.Stat(string(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused capture left %s behind: %v", id, err)
	}
	if _, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"}); err != nil {
		t.Fatalf("StartStdioCapture() after the failed stop: %v", err)
	}
}

func TestAFailedStdioLaunchDropsItsSegmentAndKeepsTheCapturePending(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{err: errors.New("simulator is shut down")}
	driver := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 0), runner)
	id, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"})
	if err != nil {
		t.Fatalf("StartStdioCapture() error = %v", err)
	}
	if err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.a"}); err == nil {
		t.Fatal("LaunchApp() succeeded although simctl failed")
	}
	stdout, stderr := runner.calls[0][4][len("--stdout="):], runner.calls[0][5][len("--stderr="):]
	for _, path := range []string{stdout, stderr} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("segment %s of the failed launch survived: %v", path, err)
		}
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); !errors.Is(err, ErrStdioCaptureUnused) {
		t.Fatalf("stop after a failed launch: error = %v, want ErrStdioCaptureUnused", err)
	}
}

func TestAStdioCaptureIsCappedWhenMerged(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 0), runner)
	driver.deviceLogByteLimit = 32
	id, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"})
	if err != nil {
		t.Fatalf("StartStdioCapture() error = %v", err)
	}
	if err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	stdout, _ := stdioCaptureFiles(t, runner.calls[0])
	writeFile(t, stdout, strings.Repeat("x", 100)+"\n")
	artifacts, err := driver.StopDeviceLogCapture(context.Background(), id)
	if err != nil {
		t.Fatalf("StopDeviceLogCapture() error = %v", err)
	}
	if artifacts[0].Metadata["truncated"] != "true" {
		t.Fatalf("metadata = %#v, want truncated=true", artifacts[0].Metadata)
	}
	info, err := os.Stat(string(id))
	if err != nil || info.Size() != 32 {
		t.Fatalf("merged size = %d (%v), want the 32-byte cap", info.Size(), err)
	}
}

func TestClosingTheDriverDropsAPendingStdioCapture(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, foregroundAfter(t, "com.example.a", 0), runner)
	id, err := driver.StartStdioCapture(context.Background(),
		device.DeviceLogRequest{OutputDirectory: t.TempDir(), AppID: "com.example.a"})
	if err != nil {
		t.Fatalf("StartStdioCapture() error = %v", err)
	}
	if err := driver.stopAllDeviceLogs(context.Background()); err != nil {
		t.Fatalf("stopAllDeviceLogs() with an unused stdio capture: %v", err)
	}
	if _, err := os.Stat(string(id)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unused capture left %s behind after close: %v", id, err)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
		t.Fatal("a closed capture still stopped")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// A segment far larger than the cap is streamed, not loaded: the merge must
// stop at the cap without reading the rest of the file.
func TestAStdioSegmentLargerThanTheCapIsStreamedNotLoaded(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	segment := filepath.Join(directory, "big.out")
	file, err := os.Create(segment)
	if err != nil {
		t.Fatal(err)
	}
	// A sparse file: 1 GiB on disk costs nothing, in memory it would.
	if err := file.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var merged strings.Builder
	limiter := newIOSLogWriter(&merged, 64)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	err = appendStdioSection(limiter, 1, "stdout", segment)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, errIOSDeviceLogLimit) {
		t.Fatalf("appendStdioSection() error = %v, want the byte cap", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("merging allocated %d bytes for a 1 GiB segment; the merge must stream", allocated)
	}
	if merged.Len() != 64 || !strings.HasPrefix(merged.String(), "### launch 1 stdout\n") {
		t.Fatalf("merged %d bytes %q, want exactly the 64-byte cap after the marker", merged.Len(), merged.String())
	}
}
