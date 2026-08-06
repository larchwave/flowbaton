package ios

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"

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
}

func TestBackPressIsANoOpOnIOS(t *testing.T) {
	t.Parallel()

	// specs/02-device-drivers.md line 49: "backPress() = no-op on iOS." It must
	// not fail, and it must not reach the runner.
	var reached []string
	driver := newTestDriver(t, func(_ http.ResponseWriter, request *http.Request) {
		reached = append(reached, request.URL.Path)
	})
	if err := driver.BackPress(context.Background()); err != nil {
		t.Fatalf("BackPress() error = %v, want a silent no-op", err)
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

func TestClearAppStateUninstallsRatherThanPretending(t *testing.T) {
	t.Parallel()

	// A simulator has no per-app "clear data" verb. Uninstall is the operation
	// that actually clears state, and pretending otherwise would leave a flow
	// running against the previous session's data.
	runner := &recordingRunner{}
	driver := newTestDriverWithSimctl(t, func(http.ResponseWriter, *http.Request) {}, runner)
	if err := driver.ClearAppState(context.Background(), device.AppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("ClearAppState() error = %v", err)
	}
	want := []string{"xcrun", "simctl", "uninstall", "UDID-1", "com.example.a"}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0], want) {
		t.Fatalf("calls = %v, want %v", runner.calls, want)
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

func (r *fakeIOSRecorder) stop() error {
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

	id, err := driver.StartScreenRecording(
		context.Background(), device.ScreenRecordingRequest{OutputPath: "/tmp/out.mov"})
	if err != nil {
		t.Fatalf("StartScreenRecording() error = %v", err)
	}
	wantArgs := []string{"simctl", "io", "UDID-1", "recordVideo", "/tmp/out.mov"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("recorder argv = %#v, want %#v", gotArgs, wantArgs)
	}
	if rec.stopped {
		t.Fatal("the recorder was stopped before StopScreenRecording was called")
	}

	artifacts, err := driver.StopScreenRecording(context.Background(), id)
	if err != nil {
		t.Fatalf("StopScreenRecording() error = %v", err)
	}
	if !rec.stopped {
		t.Fatal("StopScreenRecording did not interrupt the recorder")
	}
	if len(artifacts) != 1 || artifacts[0].Path != "/tmp/out.mov" {
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

// The interrupt finishes a recording, so the resulting process exit is not a
// failure.
func TestStoppingARecorderDoesNotReportTheInterruptAsAnError(t *testing.T) {
	t.Parallel()

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the stand-in recorder: %v", err)
	}
	recorder := &execRecorder{cmd: cmd}
	if err := recorder.stop(); err != nil {
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
