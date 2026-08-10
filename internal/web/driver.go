// Package web is the Chrome DevTools Protocol execution surface.
//
// Spec 02-device-drivers.md §4 defines it: the hierarchy comes from an injected
// script, actions are pointer and key input, and settling is
// document.readyState == "complete". Everything a browser has no notion of —
// apps, permissions, airplane mode, orientation, media — returns
// device.ErrUnsupported so preflight refuses such a flow before it runs rather
// than failing halfway through it.
package web

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

const Platform = device.Platform("web")

// DefaultSettleTimeout bounds a readyState wait. A page that never completes
// must report an unconfirmed settle rather than block the run forever.
const DefaultSettleTimeout = 10 * time.Second

// settlePollInterval is how often readyState is re-read while waiting.
const settlePollInterval = 100 * time.Millisecond

// Driver drives one attached browser page over CDP.
type Driver struct {
	baseURL string
	client  *http.Client

	// launch is set when this driver owns its browser: Open starts one and
	// Close stops it. It stays nil when attaching to an endpoint someone else
	// is responsible for, so Close never kills a browser it did not start. A
	// field rather than a direct call so the lifecycle is testable without one.
	launch  func(context.Context, ChromeOptions) (*Chrome, error)
	options ChromeOptions
	chrome  *Chrome

	connection *connection
}

// NewDriver builds a driver for a browser already listening for DevTools on
// baseURL (for example http://127.0.0.1:9222). A nil client uses the default.
func NewDriver(baseURL string, client *http.Client) *Driver {
	return &Driver{baseURL: strings.TrimSuffix(baseURL, "/"), client: client}
}

// NewLaunchingDriver builds a driver that starts its own browser on Open and
// stops it on Close. This is what a CLI run needs: it is handed flows, not a
// DevTools endpoint.
func NewLaunchingDriver(options ChromeOptions) *Driver {
	return &Driver{options: options, launch: LaunchChrome}
}

func (driver *Driver) Name() string { return "web" }

// Open attaches to the browser's page target, starting one first when this
// driver owns it.
func (driver *Driver) Open(ctx context.Context) error {
	if driver.connection != nil {
		return nil
	}
	if driver.launch != nil && driver.chrome == nil {
		chrome, err := driver.launch(ctx, driver.options)
		if err != nil {
			return err
		}
		driver.chrome = chrome
		driver.baseURL = strings.TrimSuffix(chrome.BaseURL, "/")
	}
	if err := driver.attach(ctx); err != nil {
		// A browser started for an attach that then failed is a leaked process
		// and a leaked profile directory, once per attempted run.
		driver.stopBrowser()
		return err
	}
	return nil
}

func (driver *Driver) attach(ctx context.Context) error {
	endpoint, err := discoverPageEndpoint(ctx, driver.baseURL, driver.client)
	if err != nil {
		return err
	}
	connection, err := dialEndpoint(ctx, endpoint)
	if err != nil {
		return err
	}
	driver.connection = connection
	// Page events are what navigation and screenshots depend on; enabling the
	// domains is cheap and a browser that refuses tells us now rather than at
	// the first command.
	for _, domain := range []string{"Page.enable", "Runtime.enable"} {
		if err := connection.call(ctx, domain, nil, nil); err != nil {
			_ = connection.close()
			driver.connection = nil
			return err
		}
	}
	return nil
}

func (driver *Driver) Close(context.Context) error {
	var err error
	if driver.connection != nil {
		err = driver.connection.close()
		driver.connection = nil
	}
	driver.stopBrowser()
	return err
}

// stopBrowser ends a browser this driver started. It is a no-op when the
// driver merely attached to someone else's.
func (driver *Driver) stopBrowser() {
	if driver.chrome == nil {
		return
	}
	_ = driver.chrome.Stop()
	driver.chrome = nil
}

// attached returns the live connection or explains that nothing is attached.
func (driver *Driver) attached() (*connection, error) {
	if driver.connection == nil {
		return nil, fmt.Errorf("web driver: no page is attached; call Open first")
	}
	return driver.connection, nil
}

func (driver *Driver) Capabilities() device.Capabilities {
	return DeclaredCapabilities()
}

func (driver *Driver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	connection, err := driver.attached()
	if err != nil {
		return device.DeviceInfo{}, err
	}
	var viewport struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	err = connection.evaluate(ctx,
		`({width: window.innerWidth || 0, height: window.innerHeight || 0})`, &viewport)
	if err != nil {
		return device.DeviceInfo{}, err
	}
	return device.DeviceInfo{
		Platform:     Platform,
		WidthPixels:  viewport.Width,
		HeightPixels: viewport.Height,
		WidthGrid:    viewport.Width,
		HeightGrid:   viewport.Height,
	}, nil
}

// ContentDescriptor runs the injected walker and converts its payload.
func (driver *Driver) ContentDescriptor(
	ctx context.Context, _ device.ContentDescriptorRequest,
) (device.TreeNode, error) {
	connection, err := driver.attached()
	if err != nil {
		return device.TreeNode{}, err
	}
	var payload string
	if err := connection.evaluate(ctx, contentDescriptionScript(), &payload); err != nil {
		return device.TreeNode{}, err
	}
	return parseHierarchy([]byte(payload))
}

// WaitForAppToSettle polls document.readyState (spec §4). A nil hierarchy means
// settling could not be confirmed, which the contract separates from settled.
func (driver *Driver) WaitForAppToSettle(
	ctx context.Context, request device.SettleRequest,
) (*device.ViewHierarchy, error) {
	connection, err := driver.attached()
	if err != nil {
		return nil, err
	}
	timeout := DefaultSettleTimeout
	if request.TimeoutMillis != nil && *request.TimeoutMillis > 0 {
		timeout = time.Duration(*request.TimeoutMillis) * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		var state string
		if err := connection.evaluate(ctx, `document.readyState`, &state); err != nil {
			return nil, err
		}
		if state == "complete" {
			root, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
			if err != nil {
				return nil, err
			}
			return &device.ViewHierarchy{Root: root}, nil
		}
		if !time.Now().Before(deadline) {
			// Unconfirmed, not settled: the caller must not treat this as proof.
			return nil, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(settlePollInterval):
		}
	}
}

func (driver *Driver) WaitUntilScreenIsStatic(
	ctx context.Context, request device.ScreenStaticRequest,
) (bool, error) {
	timeout := request.TimeoutMillis
	hierarchy, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{TimeoutMillis: &timeout})
	if err != nil {
		return false, err
	}
	return hierarchy != nil, nil
}

// Tap is the full pointer sequence: a page listening for mousedown/mouseup
// never sees a click sent as one event (spec §4 pointer input).
func (driver *Driver) Tap(ctx context.Context, request device.TapRequest) error {
	return driver.click(ctx, request.Point, 0)
}

func (driver *Driver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	hold := time.Duration(request.DurationMillis) * time.Millisecond
	return driver.click(ctx, request.Point, hold)
}

func (driver *Driver) click(ctx context.Context, point device.Point, hold time.Duration) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	base := func(kind string) map[string]any {
		return map[string]any{
			"type": kind, "x": point.X, "y": point.Y,
			"button": "left", "clickCount": 1, "buttons": 1,
		}
	}
	moved := base("mouseMoved")
	moved["button"] = "none"
	moved["buttons"] = 0
	if err := connection.call(ctx, "Input.dispatchMouseEvent", moved, nil); err != nil {
		return err
	}
	if err := connection.call(ctx, "Input.dispatchMouseEvent", base("mousePressed"), nil); err != nil {
		return err
	}
	if hold > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(hold):
		}
	}
	released := base("mouseReleased")
	released["buttons"] = 0
	return connection.call(ctx, "Input.dispatchMouseEvent", released, nil)
}

// Swipe is touch input: move to the start, press, move to the end, release
// (spec §4). Direction-only and element-anchored swipes are resolved against
// the viewport before dispatch.
func (driver *Driver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	start, end, err := driver.resolveSwipe(ctx, request)
	if err != nil {
		return err
	}
	send := func(kind string, point device.Point, buttons int) error {
		return connection.call(ctx, "Input.dispatchMouseEvent", map[string]any{
			"type": kind, "x": point.X, "y": point.Y,
			"button": "left", "clickCount": 1, "buttons": buttons,
		}, nil)
	}
	if err := send("mouseMoved", start, 0); err != nil {
		return err
	}
	if err := send("mousePressed", start, 1); err != nil {
		return err
	}
	if err := send("mouseMoved", end, 1); err != nil {
		return err
	}
	return send("mouseReleased", end, 0)
}

// resolveSwipe turns the three swipe shapes into two points.
func (driver *Driver) resolveSwipe(
	ctx context.Context, request device.SwipeRequest,
) (device.Point, device.Point, error) {
	if request.Start != nil && request.End != nil {
		return *request.Start, *request.End, nil
	}
	info, err := driver.DeviceInfo(ctx)
	if err != nil {
		return device.Point{}, device.Point{}, err
	}
	width, height := float64(info.WidthPixels), float64(info.HeightPixels)
	origin := device.Point{X: width / 2, Y: height / 2}
	if request.ElementPoint != nil {
		origin = *request.ElementPoint
	}
	// A quarter of the viewport is the travel; a full-viewport swipe would
	// start or end off-screen for an element near an edge.
	deltaX, deltaY := width/4, height/4
	target := origin
	switch strings.ToUpper(string(request.Direction)) {
	case "UP":
		target.Y = origin.Y - deltaY
	case "DOWN":
		target.Y = origin.Y + deltaY
	case "LEFT":
		target.X = origin.X - deltaX
	case "RIGHT":
		target.X = origin.X + deltaX
	default:
		return device.Point{}, device.Point{}, fmt.Errorf(
			"web driver: swipe needs start/end or a direction, got %q", request.Direction)
	}
	return origin, target, nil
}

func (driver *Driver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	amount := request.Amount
	if amount == 0 {
		amount = 1
	}
	sign := "1"
	if strings.EqualFold(string(request.Direction), "UP") {
		sign = "-1"
	}
	expression := fmt.Sprintf(
		`(function(){window.scrollBy(0, %s * %s * (window.innerHeight||600)); return "";})()`,
		sign, strconv.FormatFloat(amount, 'f', -1, 64))
	return connection.evaluate(ctx, expression, nil)
}

// InputText types the text as one insertion. Input.insertText is what a page's
// input handlers see as real typing, including for non-ASCII text a synthetic
// per-key sequence would mangle.
func (driver *Driver) InputText(ctx context.Context, request device.InputTextRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	if request.Text == "" {
		return nil
	}
	return connection.call(ctx, "Input.insertText", map[string]any{"text": request.Text}, nil)
}

// EraseText sends one BACK_SPACE per character (spec §4).
func (driver *Driver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	for index := uint32(0); index < request.CharactersToErase; index++ {
		for _, kind := range []string{"keyDown", "keyUp"} {
			err := connection.call(ctx, "Input.dispatchKeyEvent", map[string]any{
				"type": kind, "key": "Backspace", "code": "Backspace",
				"windowsVirtualKeyCode": 8, "nativeVirtualKeyCode": 8,
			}, nil)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// PressKey maps the neutral key codes a browser can act on. A code with no web
// meaning (LOCK, HOME on a phone) is refused rather than silently dropped.
func (driver *Driver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	key, code, virtual, ok := webKey(request.Code)
	if !ok {
		return fmt.Errorf("%w: web has no key %q", device.ErrUnsupported, request.Code)
	}
	for _, kind := range []string{"keyDown", "keyUp"} {
		params := map[string]any{
			"type": kind, "key": key, "code": code,
			"windowsVirtualKeyCode": virtual, "nativeVirtualKeyCode": virtual,
		}
		if kind == "keyDown" && key == "Enter" {
			params["text"] = "\r"
		}
		if err := connection.call(ctx, "Input.dispatchKeyEvent", params, nil); err != nil {
			return err
		}
	}
	return nil
}

func webKey(code device.KeyCode) (string, string, int, bool) {
	switch strings.ToUpper(string(code)) {
	case "ENTER":
		return "Enter", "Enter", 13, true
	case "BACKSPACE":
		return "Backspace", "Backspace", 8, true
	case "TAB":
		return "Tab", "Tab", 9, true
	case "BACK":
		// A browser's back is history, not a key; BackPress covers it.
		return "", "", 0, false
	}
	return "", "", 0, false
}

// BackPress is browser history, which is what "back" means on the web.
func (driver *Driver) BackPress(ctx context.Context) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	return connection.evaluate(ctx, `(function(){window.history.back(); return "";})()`, nil)
}

func (driver *Driver) OpenLink(ctx context.Context, request device.OpenLinkRequest) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	if request.Link == "" {
		return fmt.Errorf("web driver: openLink requires a link")
	}
	return connection.call(ctx, "Page.navigate", map[string]any{"url": request.Link}, nil)
}

func (driver *Driver) TakeScreenshot(
	ctx context.Context, _ device.ScreenshotRequest,
) ([]byte, error) {
	connection, err := driver.attached()
	if err != nil {
		return nil, err
	}
	var reply struct {
		Data string `json:"data"`
	}
	if err := connection.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, &reply); err != nil {
		return nil, err
	}
	// The protocol carries base64; forwarding it undecoded would write a text
	// file with a .png name.
	image, err := base64.StdEncoding.DecodeString(reply.Data)
	if err != nil {
		return nil, fmt.Errorf("web driver: decoding the screenshot: %w", err)
	}
	return image, nil
}

// QueryOnDeviceElements resolves a CSS expression in the page. This is the
// driver-side resolver a css selector needs: CSS is a query, not a value, so it
// cannot be matched against a captured attribute.
func (driver *Driver) QueryOnDeviceElements(
	ctx context.Context, request device.QueryRequest,
) ([]device.TreeNode, error) {
	connection, err := driver.attached()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Expression) == "" {
		return nil, fmt.Errorf("web driver: a query needs a css expression")
	}
	selector, err := json.Marshal(request.Expression)
	if err != nil {
		return nil, fmt.Errorf("web driver: encoding the query: %w", err)
	}
	var payload string
	if err := connection.evaluate(ctx, querySelectorScript(string(selector)), &payload); err != nil {
		return nil, err
	}
	var nodes []scriptNode
	if err := json.Unmarshal([]byte(payload), &nodes); err != nil {
		return nil, fmt.Errorf("web driver: decoding the query result: %w", err)
	}
	results := make([]device.TreeNode, 0, len(nodes))
	for _, node := range nodes {
		results = append(results, convertScriptNode(node))
	}
	return results, nil
}

func (driver *Driver) IsKeyboardVisible(context.Context, device.KeyboardRequest) (bool, error) {
	// A browser has no soft keyboard to show or hide.
	return false, nil
}

func (driver *Driver) HideKeyboard(context.Context) error { return nil }

func (driver *Driver) IsShutdown(context.Context) (bool, error) {
	return driver.connection == nil, nil
}

// SetLocation overrides geolocation, which a browser genuinely supports.
func (driver *Driver) SetLocation(ctx context.Context, location device.Location) error {
	connection, err := driver.attached()
	if err != nil {
		return err
	}
	return connection.call(ctx, "Emulation.setGeolocationOverride", map[string]any{
		"latitude": location.Latitude, "longitude": location.Longitude, "accuracy": 1,
	}, nil)
}

// Everything below has no browser meaning. Returning the sentinel is what lets
// preflight refuse such a flow up front instead of failing it halfway.

func unsupported(operation string) error {
	return fmt.Errorf("%w: web cannot %s", device.ErrUnsupported, operation)
}

// LaunchApp opens the flow's page.
//
// specs/01-core-engine.md:17 makes `url` the web flow's effective appId, so the
// target arrives here in AppID. A value that is not an http(s) url is a mobile
// flow aimed at a browser: navigating to it anyway would resolve it as a
// relative path and run the whole suite against some other page.
func (driver *Driver) LaunchApp(ctx context.Context, request device.LaunchAppRequest) error {
	if !isWebTarget(request.AppID) {
		return fmt.Errorf(
			"web driver: %q is not a page to open; a web flow declares url: https://…", request.AppID)
	}
	return driver.OpenLink(ctx, device.OpenLinkRequest{Link: request.AppID})
}

// isWebTarget reports whether a flow target names a page rather than an app.
func isWebTarget(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://")
}

// StopApp unloads the page.
//
// specs/06-launch-app-semantics.md has launchApp stop the app before starting
// it unless the flow says otherwise, so every web run reaches this on its way
// to its first page. about:blank is the browser's version of stopping: it drops
// the document, its scripts and its timers. Returning nil without navigating
// would leave the old page live and let a stale assertion pass against it.
func (driver *Driver) StopApp(ctx context.Context, request device.AppRequest) error {
	if !isWebTarget(request.AppID) {
		return unsupported("stop an app")
	}
	return driver.OpenLink(ctx, device.OpenLinkRequest{Link: "about:blank"})
}
func (driver *Driver) KillApp(context.Context, device.AppRequest) error {
	return unsupported("kill an app")
}
func (driver *Driver) ClearAppState(context.Context, device.AppRequest) error {
	return unsupported("clear app state")
}
func (driver *Driver) ClearKeychain(context.Context) error {
	return unsupported("clear a keychain")
}
func (driver *Driver) SetPermissions(context.Context, device.PermissionsRequest) error {
	return unsupported("set app permissions")
}
func (driver *Driver) AddMedia(context.Context, device.AddMediaRequest) error {
	return unsupported("add media")
}
func (driver *Driver) IsAirplaneModeEnabled(context.Context) (bool, error) {
	return false, unsupported("read airplane mode")
}
func (driver *Driver) SetAirplaneMode(context.Context, device.AirplaneModeRequest) error {
	return unsupported("set airplane mode")
}
func (driver *Driver) SetOrientation(context.Context, device.Orientation) error {
	return unsupported("set orientation")
}
func (driver *Driver) SetProxy(context.Context, device.Proxy) error {
	return unsupported("set a proxy")
}
func (driver *Driver) ResetProxy(context.Context) error {
	return unsupported("reset a proxy")
}
func (driver *Driver) SetAndroidChromeDevToolsEnabled(
	context.Context, device.ChromeDevToolsRequest,
) error {
	return unsupported("toggle android chrome devtools")
}
func (driver *Driver) StartScreenRecording(
	context.Context, device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	return "", unsupported("record the screen")
}
func (driver *Driver) StartDeviceLogCapture(
	context.Context, device.DeviceLogRequest,
) (device.CaptureID, error) {
	return "", unsupported("capture device logs")
}
func (driver *Driver) StopDeviceLogCapture(
	context.Context, device.CaptureID,
) ([]device.Artifact, error) {
	return nil, unsupported("capture device logs")
}
func (driver *Driver) CollectCrashArtifacts(
	context.Context, device.ArtifactRequest,
) ([]device.Artifact, error) {
	return nil, unsupported("collect crash artifacts")
}
