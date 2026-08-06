package android

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/nohavewho/flowbaton/internal/android/pbwire"
	"github.com/nohavewho/flowbaton/internal/device"
)

// specs/01-core-engine.md:62 — the engine initialises Android Chrome DevTools
// when the flow's config says `androidWebViewHierarchy: devtools`, and
// specs/02-device-drivers.md:44 says the WebView hierarchy is then merged over
// the localabstract CDP socket.
//
// Enabling the option must attach to the browser and merge its page tree.
// These tests check both the port forward and the merged result.

// stubWebView stands in for the browser side so these tests need no Chrome.
type stubWebView struct {
	tree   device.TreeNode
	err    error
	closed bool
}

func (stub *stubWebView) ContentDescriptor(
	context.Context, device.ContentDescriptorRequest,
) (device.TreeNode, error) {
	return stub.tree, stub.err
}

func (stub *stubWebView) Close(context.Context) error {
	stub.closed = true
	return nil
}

// withStubWebView swaps the browser dialer for one that yields the given page.
// Not parallel-safe: the hook is package state.
func withStubWebView(t *testing.T, stub *stubWebView) *[]string {
	t.Helper()
	dialled := []string{}
	previous := dialWebView
	dialWebView = func(_ context.Context, baseURL string) (webViewSource, error) {
		dialled = append(dialled, baseURL)
		if stub == nil {
			return nil, errors.New("no browser")
		}
		return stub, nil
	}
	t.Cleanup(func() { dialWebView = previous })
	return &dialled
}

// The page walker emits Android-style bounds, so the stub does too.
var stubPage = device.TreeNode{
	Attributes: map[string]string{"tagName": "body", "bounds": "[0,0][500,500]"},
	Children: []device.TreeNode{{
		Attributes: map[string]string{"tagName": "button", "bounds": "[10,20][110,60]", "text": "Sign in"},
	}},
}

// webViewHierarchyXML is an accessibility dump whose only leaf is a WebView.
const webViewHierarchyXML = `<?xml version="1.0"?>
<hierarchy>
  <node class="android.widget.FrameLayout" bounds="[0,0][1080,1920]">
    <node class="android.webkit.WebView" bounds="[40,200][1040,1200]"/>
  </node>
</hierarchy>`

func webViewAgent(method string, _ [][]byte) []byte {
	switch method {
	case pbwire.MethodDeviceInfo:
		return pbwire.DeviceInfo{WidthPixels: 1080, HeightPixels: 1920}.Marshal()
	case pbwire.MethodViewHierarchy:
		return pbwire.ViewHierarchyResponse{Hierarchy: webViewHierarchyXML}.Marshal()
	}
	return nil
}

func TestEnablingDevToolsForwardsTheAbstractSocket(t *testing.T) {
	dialled := withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)

	err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true})
	if err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}

	// The socket name is fixed by spec 02:44; the host port is whatever the
	// driver reserved, and it must be the one the browser side was dialled on
	// or the forward points somewhere nothing reads.
	port := driver.devtoolsPort
	if port <= 0 {
		t.Fatal("no devtools host port was reserved")
	}
	want := []string{"-s", testSerial, "forward",
		"tcp:" + strconv.Itoa(port), "localabstract:chrome_devtools_remote"}
	found := false
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[1:], want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("adb calls = %v, want a forward to the abstract socket", runner.recorded())
	}
	if len(*dialled) != 1 || (*dialled)[0] != "http://127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("dialled %v, want the forwarded port", *dialled)
	}
}

func TestContentDescriptorMergesTheWebViewWhenDevToolsIsOn(t *testing.T) {
	withStubWebView(t, &stubWebView{tree: stubPage})
	driver, _, _ := newOpenDriver(t, webViewAgent)

	before, err := driver.ContentDescriptor(context.Background(), device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	// The control: without the merge the WebView is an opaque leaf, which is
	// exactly the thing the option exists to fix. The dump nests it one level
	// down, under the FrameLayout the parser puts beneath the document root.
	if len(before.Children[0].Children[0].Children) != 0 {
		t.Fatalf("the WebView already has children before devtools is enabled: %#v", before)
	}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}

	after, err := driver.ContentDescriptor(context.Background(), device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	webView := after.Children[0].Children[0]
	if len(webView.Children) != 1 {
		t.Fatalf("the WebView got %d children, want the page: %#v", len(webView.Children), webView)
	}
	button := webView.Children[0].Children[0]
	// The page is 500 CSS px in a 1000 device-px WebView at (40,200): scale 2.
	if got := button.Attributes["bounds"]; got != "[60,240][260,320]" {
		t.Fatalf("button bounds = %q, want it placed on the screen", got)
	}
}

// Disabling has to undo both halves. A forward left behind pins a host port for
// the life of the adb server, so the next shard that reserves it gets a socket
// pointing at a dead page.
func TestDisablingDevToolsClosesTheBrowserAndRemovesTheForward(t *testing.T) {
	stub := &stubWebView{tree: stubPage}
	withStubWebView(t, stub)
	driver, runner, _ := newOpenDriver(t, webViewAgent)

	ctx := context.Background()
	if err := driver.SetAndroidChromeDevToolsEnabled(
		ctx, device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("enable error = %v", err)
	}
	port := driver.devtoolsPort
	if err := driver.SetAndroidChromeDevToolsEnabled(
		ctx, device.ChromeDevToolsRequest{Enabled: false}); err != nil {
		t.Fatalf("disable error = %v", err)
	}
	if !stub.closed {
		t.Error("the browser connection was left open")
	}
	want := []string{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)}
	found := false
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[1:], want) {
			found = true
		}
	}
	if !found {
		t.Fatalf("adb calls = %v, want the forward removed", runner.recorded())
	}
	// And the tree goes back to the native one.
	after, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if len(after.Children[0].Children[0].Children) != 0 {
		t.Fatal("the WebView is still merged after devtools was disabled")
	}
}

// A page the browser will not describe must fail the hierarchy read. Falling
// back to the native tree would silently give a flow the opaque WebView it
// asked not to have, and its assertions would fail somewhere else entirely.
func TestContentDescriptorFailsWhenTheWebViewCannotBeRead(t *testing.T) {
	withStubWebView(t, &stubWebView{err: errors.New("target closed")})
	driver, _, _ := newOpenDriver(t, webViewAgent)

	ctx := context.Background()
	if err := driver.SetAndroidChromeDevToolsEnabled(
		ctx, device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("enable error = %v", err)
	}
	if _, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{}); err == nil {
		t.Fatal("a WebView that could not be read was reported as an empty one")
	}
}

// The capability has to say yes, or preflight refuses the flow that turns it on.
func TestAndroidAdvertisesTheChromeDevToolsCapability(t *testing.T) {
	t.Parallel()

	driver := NewDriver(testSerial, 7001, &recordingRunner{}, nil)
	if !driver.Capabilities().Features["androidChromeDevTools"] {
		t.Fatal("the android driver does not advertise androidChromeDevTools")
	}
}

// Close must not leave a forward behind either — a run that ends mid-flow with
// devtools on is the ordinary crash case.
func TestCloseRemovesTheDevToolsForward(t *testing.T) {
	stub := &stubWebView{tree: stubPage}
	withStubWebView(t, stub)
	driver, runner, _ := newOpenDriver(t, webViewAgent)

	ctx := context.Background()
	if err := driver.SetAndroidChromeDevToolsEnabled(
		ctx, device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("enable error = %v", err)
	}
	port := driver.devtoolsPort
	_ = driver.Close(ctx)
	if !stub.closed {
		t.Error("Close left the browser connection open")
	}
	want := []string{"-s", testSerial, "forward", "--remove", "tcp:" + strconv.Itoa(port)}
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[1:], want) {
			return
		}
	}
	t.Fatalf("adb calls = %v, want the devtools forward removed on Close", runner.recorded())
}
