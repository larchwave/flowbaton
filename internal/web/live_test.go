package web

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

// A live end-to-end run against a real browser.
//
// Every other test in this package stands a fake in for the browser, which
// proves the driver speaks the protocol it thinks it speaks — not that a real
// Chrome agrees. This one launches the actual binary, so the injected script,
// the flag set, the handshake, and the input events are all exercised for real.
//
// It is opt-in (FLOWBATON_WEB_LIVE=1) because a browser is not present on every
// machine that runs the suite.
func TestLiveChromeDrivesARealPage(t *testing.T) {
	if os.Getenv("FLOWBATON_WEB_LIVE") != "1" {
		t.Skip("set FLOWBATON_WEB_LIVE=1 to run the live browser proof")
	}
	binary := os.Getenv("FLOWBATON_CHROME")
	if binary == "" {
		binary = DefaultChromeBinary()
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("no browser at %s", binary)
	}

	page := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<!doctype html><html><body>
		  <h1 id="title">Welcome</h1>
		  <button id="login" onclick="document.getElementById('title').textContent='Tapped'">Sign in</button>
		  <input id="email" name="email" placeholder="Email">
		  <select id="pick"><option>Alpha</option><option selected>Beta</option></select>
		</body></html>`))
	}))
	defer page.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	chrome, err := LaunchChrome(ctx, ChromeOptions{
		Binary: binary, Port: freePort(t), Headless: true, WindowSize: "1024,768",
	})
	if err != nil {
		t.Fatalf("LaunchChrome() error = %v", err)
	}
	defer func() { _ = chrome.Stop() }()

	driver := NewDriver(chrome.BaseURL, nil)
	if err := driver.Open(ctx); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = driver.Close(ctx) }()

	if err := driver.OpenLink(ctx, device.OpenLinkRequest{Link: page.URL}); err != nil {
		t.Fatalf("OpenLink() error = %v", err)
	}
	hierarchy, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if err != nil {
		t.Fatalf("WaitForAppToSettle() error = %v", err)
	}
	if hierarchy == nil {
		t.Fatal("the page never reported readyState complete")
	}

	// The injected walker must find the real elements, with parseable bounds.
	button := findByAttribute(hierarchy.Root, "resource-id", "login")
	if button == nil {
		t.Fatalf("the hierarchy has no #login button:\n%s", renderTree(hierarchy.Root, 0))
	}
	if button.Attributes["text"] != "Sign in" {
		t.Errorf("button text = %q, want %q", button.Attributes["text"], "Sign in")
	}
	if !strings.HasPrefix(button.Attributes["bounds"], "[") {
		t.Errorf("button bounds = %q, want Android-style", button.Attributes["bounds"])
	}
	// Spec §4 text precedence: a placeholder is the input's text when empty.
	if input := findByAttribute(hierarchy.Root, "resource-id", "email"); input == nil {
		t.Error("the hierarchy has no #email input")
	} else if input.Attributes["hintText"] != "Email" {
		t.Errorf("input hintText = %q, want Email", input.Attributes["hintText"])
	}
	// Spec §4: <option> nodes are synthetic, in the +100000 offset space. The
	// search is by tag as well as text because the <select> itself reports its
	// selected option as its own text (the spec's selectedOptions precedence),
	// so a text-only search finds the parent first.
	option := findNode(hierarchy.Root, func(node device.TreeNode) bool {
		return node.Attributes["tagName"] == "option" && node.Attributes["text"] == "Beta"
	})
	if option == nil {
		t.Errorf("the hierarchy has no Beta option:\n%s", renderTree(hierarchy.Root, 0))
	} else {
		if !strings.HasPrefix(option.Attributes["bounds"], "[100000,") {
			t.Errorf("option bounds = %q, want the synthetic offset", option.Attributes["bounds"])
		}
		if option.Selected == nil || !*option.Selected {
			t.Errorf("the selected option does not report selected: %#v", option)
		}
	}
	// The <select> itself takes its text from the selected option.
	if selectNode := findByAttribute(hierarchy.Root, "resource-id", "pick"); selectNode == nil {
		t.Error("the hierarchy has no #pick select")
	} else if selectNode.Attributes["text"] != "Beta" {
		t.Errorf("select text = %q, want the selected option", selectNode.Attributes["text"])
	}

	// A tap must be a real click the page reacts to.
	center := centerOf(t, button.Attributes["bounds"])
	if err := driver.Tap(ctx, device.TapRequest{Point: center}); err != nil {
		t.Fatalf("Tap() error = %v", err)
	}
	after, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if err != nil || after == nil {
		t.Fatalf("settle after tap: %v %v", after, err)
	}
	if title := findByAttribute(after.Root, "resource-id", "title"); title == nil {
		t.Fatal("the title element vanished")
	} else if title.Attributes["text"] != "Tapped" {
		t.Fatalf("title text = %q; the click never reached the page", title.Attributes["text"])
	}

	// Typing must land in the focused field.
	if err := driver.Tap(ctx, centerTap(t, after, "email")); err != nil {
		t.Fatalf("Tap(email) error = %v", err)
	}
	if err := driver.InputText(ctx, device.InputTextRequest{Text: "user@example.com"}); err != nil {
		t.Fatalf("InputText() error = %v", err)
	}
	typed, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if err != nil || typed == nil {
		t.Fatalf("settle after typing: %v %v", typed, err)
	}
	email := findByAttribute(typed.Root, "resource-id", "email")
	if email == nil || email.Attributes["text"] != "user@example.com" {
		t.Fatalf("email field = %#v; the text never landed", email)
	}

	// Erase must remove exactly what it was told to.
	if err := driver.EraseText(ctx, device.EraseTextRequest{CharactersToErase: 12}); err != nil {
		t.Fatalf("EraseText() error = %v", err)
	}
	erased, _ := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if erased != nil {
		if field := findByAttribute(erased.Root, "resource-id", "email"); field != nil {
			if got := field.Attributes["text"]; got != "user" {
				t.Errorf("after erasing 12 of 16 characters the field is %q, want %q", got, "user")
			}
		}
	}

	// CSS resolution is what a web flow selects with.
	nodes, err := driver.QueryOnDeviceElements(ctx, device.QueryRequest{Expression: "#login"})
	if err != nil {
		t.Fatalf("QueryOnDeviceElements() error = %v", err)
	}
	if len(nodes) != 1 || nodes[0].Attributes["text"] != "Sign in" {
		t.Fatalf("css query returned %#v", nodes)
	}

	// A screenshot must be a real PNG, not the base64 that carried it.
	image, err := driver.TakeScreenshot(ctx, device.ScreenshotRequest{})
	if err != nil {
		t.Fatalf("TakeScreenshot() error = %v", err)
	}
	if !bytes.HasPrefix(image, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatalf("screenshot is not a PNG: %q", image[:min(16, len(image))])
	}

	info, err := driver.DeviceInfo(ctx)
	if err != nil {
		t.Fatalf("DeviceInfo() error = %v", err)
	}
	if info.WidthPixels <= 0 || info.HeightPixels <= 0 {
		t.Fatalf("device info = %#v", info)
	}
	t.Logf("live proof: viewport %dx%d, screenshot %d bytes", info.WidthPixels, info.HeightPixels, len(image))
}

// A live negative control: the driver must report a bad css selector as an
// error rather than as "nothing matched", which would read as a missing element
// and send a flow down its not-found path for the wrong reason.
func TestLiveChromeRejectsAnInvalidSelector(t *testing.T) {
	if os.Getenv("FLOWBATON_WEB_LIVE") != "1" {
		t.Skip("set FLOWBATON_WEB_LIVE=1 to run the live browser proof")
	}
	binary := DefaultChromeBinary()
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("no browser at %s", binary)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	chrome, err := LaunchChrome(ctx, ChromeOptions{Binary: binary, Port: freePort(t), Headless: true})
	if err != nil {
		t.Fatalf("LaunchChrome() error = %v", err)
	}
	defer func() { _ = chrome.Stop() }()

	driver := NewDriver(chrome.BaseURL, nil)
	if err := driver.Open(ctx); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = driver.Close(ctx) }()

	_, err = driver.QueryOnDeviceElements(ctx, device.QueryRequest{Expression: "((("})
	if err == nil {
		t.Fatal("an invalid css selector was accepted; it must surface as an error")
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func findByAttribute(node device.TreeNode, key, value string) *device.TreeNode {
	return findNode(node, func(candidate device.TreeNode) bool {
		return candidate.Attributes[key] == value
	})
}

func findNode(node device.TreeNode, matches func(device.TreeNode) bool) *device.TreeNode {
	if matches(node) {
		found := node
		return &found
	}
	for index := range node.Children {
		if found := findNode(node.Children[index], matches); found != nil {
			return found
		}
	}
	return nil
}

func centerTap(t *testing.T, hierarchy *device.ViewHierarchy, id string) device.TapRequest {
	t.Helper()
	node := findByAttribute(hierarchy.Root, "resource-id", id)
	if node == nil {
		t.Fatalf("no element with id %q", id)
	}
	return device.TapRequest{Point: centerOf(t, node.Attributes["bounds"])}
}

func centerOf(t *testing.T, bounds string) device.Point {
	t.Helper()
	parsed, err := hierarchy.ParseBounds(bounds)
	if err != nil {
		t.Fatalf("parsing bounds %q: %v", bounds, err)
	}
	return device.Point{
		X: float64(parsed.X) + float64(parsed.Width)/2,
		Y: float64(parsed.Y) + float64(parsed.Height)/2,
	}
}

func renderTree(node device.TreeNode, depth int) string {
	line := strings.Repeat("  ", depth) + node.Attributes["tagName"] +
		" id=" + node.Attributes["resource-id"] + " text=" + node.Attributes["text"] + "\n"
	for _, child := range node.Children {
		line += renderTree(child, depth+1)
	}
	return line
}
