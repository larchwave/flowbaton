package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// pageState is the fake page a driver test drives: it answers the six protocol
// methods the driver uses and records the input events it received, so a tap
// can be asserted as the events a real browser would have seen.
type pageState struct {
	hierarchy  string
	readyState string
	mouse      []map[string]any
	keys       []map[string]any
	navigated  string
	scrolled   bool
}

func newDriverUnderTest(t *testing.T, page *pageState) (*Driver, *pageState) {
	t.Helper()
	if page.readyState == "" {
		page.readyState = "complete"
	}
	server := newCDPServer(t, func(method string, params json.RawMessage) (any, string) {
		var decoded map[string]any
		_ = json.Unmarshal(params, &decoded)
		switch method {
		case "Runtime.evaluate":
			expression, _ := decoded["expression"].(string)
			return evaluateFake(page, expression), ""
		case "Page.navigate":
			page.navigated, _ = decoded["url"].(string)
			return map[string]any{"frameId": "1"}, ""
		case "Page.captureScreenshot":
			return map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("PNG"))}, ""
		case "Input.dispatchMouseEvent":
			page.mouse = append(page.mouse, decoded)
			return map[string]any{}, ""
		case "Input.dispatchKeyEvent":
			page.keys = append(page.keys, decoded)
			return map[string]any{}, ""
		case "Input.insertText":
			page.keys = append(page.keys, decoded)
			return map[string]any{}, ""
		case "Page.enable", "Runtime.enable", "DOM.enable":
			return map[string]any{}, ""
		}
		return map[string]any{}, ""
	})
	driver := NewDriver(server.Server.URL, nil)
	if err := driver.Open(context.Background()); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = driver.Close(context.Background()) })
	return driver, page
}

func evaluateFake(page *pageState, expression string) map[string]any {
	value := func(v any) map[string]any {
		return map[string]any{"result": map[string]any{"type": "string", "value": v}}
	}
	switch {
	case strings.Contains(expression, "getContentDescription"):
		return value(page.hierarchy)
	case strings.Contains(expression, "document.readyState"):
		return value(page.readyState)
	case strings.Contains(expression, "innerWidth"):
		return map[string]any{"result": map[string]any{
			"type": "object", "value": map[string]any{"width": 1024, "height": 768},
		}}
	case strings.Contains(expression, "scrollBy"):
		page.scrolled = true
		return value("")
	case strings.Contains(expression, "history.back"):
		page.navigated = "back"
		return value("")
	case strings.Contains(expression, "querySelectorAll"):
		return value(`[{"attributes":{"tagName":"button","css":"#login","bounds":"[1,2][3,4]"}}]`)
	}
	return value("")
}

const sampleHierarchy = `{"attributes":{"tagName":"body","bounds":"[0,0][1024,768]"},
 "children":[{"attributes":{"tagName":"button","text":"Sign in","css":"#login","bounds":"[10,20][110,60]","enabled":"true"}}]}`

func TestDriverReportsWebCapabilities(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	capabilities := driver.Capabilities()
	if capabilities.Platform != Platform {
		t.Fatalf("platform = %q, want %q", capabilities.Platform, Platform)
	}
	// CSS selection is the whole point of a web driver; a capability that says
	// otherwise would have preflight refuse every web flow.
	if !capabilities.Features["cssSelector"] {
		t.Error("web driver does not advertise cssSelector")
	}
	// A browser has no soft keyboard, apps, or airplane mode; claiming them
	// would let preflight pass a flow that then fails halfway through.
	for _, absent := range []string{"airplaneMode", "proxy", "screenRecording", "appLifecycle"} {
		if capabilities.Features[absent] {
			t.Errorf("web driver claims %q", absent)
		}
	}
}

func TestDriverContentDescriptorReturnsTheInjectedTree(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	tree, err := driver.ContentDescriptor(context.Background(), device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if len(tree.Children) != 1 {
		t.Fatalf("tree = %#v", tree)
	}
	button := tree.Children[0]
	if button.Attributes["text"] != "Sign in" || button.Attributes["css"] != "#login" {
		t.Fatalf("button = %#v", button.Attributes)
	}
	if button.Enabled == nil || !*button.Enabled {
		t.Fatalf("button enabled = %v", button.Enabled)
	}
}

// Spec 02 §4: settling on web is document.readyState == "complete". A page
// that never completes must time out rather than report a settled hierarchy.
func TestDriverWaitForAppToSettleReturnsTheHierarchyWhenComplete(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy, readyState: "complete"})
	hierarchy, err := driver.WaitForAppToSettle(context.Background(), device.SettleRequest{})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	if hierarchy == nil {
		t.Fatal("WaitForAppToSettle() = nil, want the settled hierarchy")
	}
	if len(hierarchy.Root.Children) != 1 {
		t.Fatalf("hierarchy = %#v", hierarchy.Root)
	}
}

func TestDriverWaitForAppToSettleReportsUnconfirmedWhenLoading(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy, readyState: "loading"})
	timeout := int64(50)
	hierarchy, err := driver.WaitForAppToSettle(
		context.Background(), device.SettleRequest{TimeoutMillis: &timeout})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	// A nil hierarchy means "could not confirm settling", which the contract
	// (spec 02 §1) explicitly separates from "settled".
	if hierarchy != nil {
		t.Fatalf("WaitForAppToSettle() = %#v, want nil for an unconfirmed settle", hierarchy)
	}
}

// Spec §4 actions: a tap is pointer input — move, then press, then release, at
// the same point. Anything less and a page listening for mousedown/mouseup
// never sees the click.
func TestDriverTapDispatchesTheFullPointerSequence(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	err := driver.Tap(context.Background(), device.TapRequest{Point: device.Point{X: 60, Y: 40}})
	if err != nil {
		t.Fatalf("Tap() error = %v", err)
	}
	if len(page.mouse) != 3 {
		t.Fatalf("mouse events = %d, want move+press+release: %#v", len(page.mouse), page.mouse)
	}
	wantTypes := []string{"mouseMoved", "mousePressed", "mouseReleased"}
	for index, want := range wantTypes {
		if page.mouse[index]["type"] != want {
			t.Errorf("event %d = %v, want %s", index, page.mouse[index]["type"], want)
		}
		if page.mouse[index]["x"] != float64(60) || page.mouse[index]["y"] != float64(40) {
			t.Errorf("event %d landed at (%v,%v), want (60,40)",
				index, page.mouse[index]["x"], page.mouse[index]["y"])
		}
	}
}

func TestDriverInputTextSendsTheText(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	if err := driver.InputText(context.Background(), device.InputTextRequest{Text: "hello"}); err != nil {
		t.Fatalf("InputText() error = %v", err)
	}
	if len(page.keys) == 0 {
		t.Fatal("no key input reached the page")
	}
	var sent string
	for _, event := range page.keys {
		if text, ok := event["text"].(string); ok {
			sent += text
		}
	}
	if sent != "hello" {
		t.Fatalf("sent %q, want %q", sent, "hello")
	}
}

// Spec §4: erase is BACK_SPACE input, one per character.
func TestDriverEraseTextSendsBackspaces(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	if err := driver.EraseText(context.Background(), device.EraseTextRequest{CharactersToErase: 3}); err != nil {
		t.Fatalf("EraseText() error = %v", err)
	}
	backspaces := 0
	for _, event := range page.keys {
		if event["key"] == "Backspace" && event["type"] == "keyDown" {
			backspaces++
		}
	}
	if backspaces != 3 {
		t.Fatalf("backspaces = %d, want 3: %#v", backspaces, page.keys)
	}
}

func TestDriverOpenLinkNavigates(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	err := driver.OpenLink(context.Background(), device.OpenLinkRequest{Link: "https://example.invalid/x"})
	if err != nil {
		t.Fatalf("OpenLink() error = %v", err)
	}
	if page.navigated != "https://example.invalid/x" {
		t.Fatalf("navigated to %q", page.navigated)
	}
}

func TestDriverTakeScreenshotReturnsDecodedBytes(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	image, err := driver.TakeScreenshot(context.Background(), device.ScreenshotRequest{})
	if err != nil {
		t.Fatalf("TakeScreenshot() error = %v", err)
	}
	// The protocol carries base64; a driver that forwards it undecoded writes a
	// text file with a .png name.
	if string(image) != "PNG" {
		t.Fatalf("image = %q, want the decoded bytes", image)
	}
}

// CSS is what a web flow selects with, and QueryOnDeviceElements is the
// driver-side resolver the engine hands an expression to.
func TestDriverQueryOnDeviceElementsResolvesCSS(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	nodes, err := driver.QueryOnDeviceElements(
		context.Background(), device.QueryRequest{Expression: "#login"})
	if err != nil {
		t.Fatalf("QueryOnDeviceElements() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Attributes["css"] != "#login" {
		t.Fatalf("nodes = %#v", nodes)
	}
}

// Every operation a browser cannot perform must return the sentinel, because
// preflight tells "this platform will never do that" from "that failed" by it.
func TestDriverRefusesDeviceOnlyOperations(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	ctx := context.Background()
	for name, call := range map[string]func() error{
		// LaunchApp is not here: on web it means "open the flow's url", which
		// the browser can do. Its two branches have their own tests.
		"StopApp":         func() error { return driver.StopApp(ctx, device.AppRequest{AppID: "x"}) },
		"KillApp":         func() error { return driver.KillApp(ctx, device.AppRequest{AppID: "x"}) },
		"ClearAppState":   func() error { return driver.ClearAppState(ctx, device.AppRequest{AppID: "x"}) },
		"ClearKeychain":   func() error { return driver.ClearKeychain(ctx) },
		"SetPermissions":  func() error { return driver.SetPermissions(ctx, device.PermissionsRequest{}) },
		"AddMedia":        func() error { return driver.AddMedia(ctx, device.AddMediaRequest{}) },
		"SetAirplaneMode": func() error { return driver.SetAirplaneMode(ctx, device.AirplaneModeRequest{}) },
		"SetOrientation":  func() error { return driver.SetOrientation(ctx, device.Orientation("PORTRAIT")) },
		"SetProxy":        func() error { return driver.SetProxy(ctx, device.Proxy{}) },
	} {
		if err := call(); !errors.Is(err, device.ErrUnsupported) {
			t.Errorf("%s() error = %v, want device.ErrUnsupported", name, err)
		}
	}
}

// The driver must satisfy the frozen contract surface, or it cannot be handed
// to a session at all.
func TestDriverImplementsTheDeviceContract(t *testing.T) {
	t.Parallel()

	var _ device.Driver = (*Driver)(nil)
}

// A web flow declares `url:` where a mobile flow declares `appId:`, and
// specs/01-core-engine.md:17 makes the url the effective appId. So `launchApp`
// on a web run means "navigate to the flow's url" — the driver receives it as
// the AppID, because that is the field the engine puts the effective target in.
//
// Without this, `-p web` runs a flow that opens nothing: every command lands on
// about:blank and every assertion fails for a reason that has nothing to do
// with the page under test.
func TestDriverLaunchAppNavigatesToTheFlowURL(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "https://example.invalid/start"})
	if err != nil {
		t.Fatalf("LaunchApp() error = %v", err)
	}
	if page.navigated != "https://example.invalid/start" {
		t.Fatalf("navigated to %q, want the flow url", page.navigated)
	}
}

// An appId that is not a url is a mobile flow pointed at a browser. Navigating
// to "com.example.app" would resolve as a relative path and load something, so
// the run would proceed against the wrong page rather than stop.
func TestDriverLaunchAppRefusesAMobileAppID(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	err := driver.LaunchApp(context.Background(), device.LaunchAppRequest{AppID: "com.example.app"})
	if err == nil {
		t.Fatal("a bundle id was accepted as a page to open")
	}
	if page.navigated != "" {
		t.Fatalf("navigated to %q; nothing should have been opened", page.navigated)
	}
}

// `launchApp` stops the app before starting it unless the flow says otherwise
// (specs/06-launch-app-semantics.md), so a web run reaches StopApp on the way
// to its very first page. Refusing it would make the default form of the one
// command every web flow starts with fail.
//
// Unloading the page is the browser's version of stopping: about:blank drops
// the document, its scripts and its timers, which is what a following launch
// then replaces. A no-op returning nil would leave the old page live and let a
// stale assertion pass against it.
func TestDriverStopAppUnloadsTheWebPage(t *testing.T) {
	t.Parallel()

	driver, page := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	if err := driver.StopApp(context.Background(), device.AppRequest{AppID: "https://example.invalid/x"}); err != nil {
		t.Fatalf("StopApp() error = %v", err)
	}
	if page.navigated != "about:blank" {
		t.Fatalf("navigated to %q, want about:blank", page.navigated)
	}
}

// A bundle id still cannot be stopped: there is no app.
func TestDriverStopAppStillRefusesAMobileAppID(t *testing.T) {
	t.Parallel()

	driver, _ := newDriverUnderTest(t, &pageState{hierarchy: sampleHierarchy})
	err := driver.StopApp(context.Background(), device.AppRequest{AppID: "com.example.app"})
	if !errors.Is(err, device.ErrUnsupported) {
		t.Fatalf("StopApp(bundle id) error = %v, want device.ErrUnsupported", err)
	}
}
