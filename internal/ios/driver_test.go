package ios

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
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
				ElementType: 40, // switch — checkable per specs/02 line 49
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

func TestCheckableElementTypesAreExactlyTheThreeTheSpecNames(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md line 49: "Checkable elementTypes: checkbox=12,
	// switch=40, toggle=41." Anything else has no checked state at all, and
	// reporting false for it would make `checked: false` match every button on
	// screen.
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
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
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
	return NewDriver("UDID-1", 41001, NewClient(server.URL), NewSimctl("UDID-1", runner), nil)
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
}

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
	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		return &fakeIOSLogProcess{}, nil
	}
	id, err := driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil {
		t.Fatal("StopDeviceLogCapture accepted an empty artifact")
	}

	driver.spawnDeviceLog = func(context.Context, []string, io.Writer) (deviceLogProcess, error) {
		return &fakeIOSLogProcess{stopErr: errors.New("log child failed")}, nil
	}
	id, err = driver.StartDeviceLogCapture(
		context.Background(), device.DeviceLogRequest{OutputDirectory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driver.StopDeviceLogCapture(context.Background(), id); err == nil ||
		!strings.Contains(err.Error(), "log child failed") {
		t.Fatalf("StopDeviceLogCapture error = %v, want child failure", err)
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
