package android

// The Chrome DevTools WebView hierarchy.
//
// specs/01-core-engine.md:62 has the engine initialise this when a flow's
// config says `androidWebViewHierarchy: devtools`; specs/02-device-drivers.md:44
// says the WebView hierarchy is then merged in over the localabstract CDP
// socket. The page walker and CDP transport in internal/web operate on any
// answering DevTools endpoint, including an Android WebView.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/web"
)

// devToolsAbstractSocket is the name Chrome publishes on (spec 02:44). It is
// not the only one: a WebView with setWebContentsDebuggingEnabled(true)
// publishes webview_devtools_remote_<pid> instead, and a WebView is exactly
// what this mode exists to see into. So the name is discovered, with this one
// preferred when it is up.
const devToolsAbstractSocket = "chrome_devtools_remote"

// devToolsSocketMarker is what every one of those names has in common:
// chrome_devtools_remote, webview_devtools_remote_<pid>,
// <package>_devtools_remote. Matching the marker accepts package-specific
// socket names without maintaining a fixed list.
const devToolsSocketMarker = "devtools_remote"

// devToolsSockets reads the device's abstract socket names, most preferred
// first. Unread output falls back to the standard Chrome socket. Empty output
// is also unread because a running Android normally exposes many unix sockets.
func (driver *Driver) devToolsSockets(ctx context.Context) []string {
	dump, err := driver.adb.shellOutput(ctx, "cat", "/proc/net/unix")
	if err != nil || len(bytes.TrimSpace(dump)) == 0 {
		return []string{devToolsAbstractSocket}
	}
	found := map[string]bool{}
	var others []string
	for _, line := range strings.Split(string(dump), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		path := fields[len(fields)-1]
		// Abstract sockets only. A filesystem socket cannot be forwarded with
		// localabstract, and the last field of a pathless row is a number.
		if !strings.HasPrefix(path, "@") {
			continue
		}
		name := strings.TrimPrefix(path, "@")
		if !strings.Contains(name, devToolsSocketMarker) || found[name] {
			continue
		}
		found[name] = true
		if name != devToolsAbstractSocket {
			others = append(others, name)
		}
	}
	if found[devToolsAbstractSocket] {
		return append([]string{devToolsAbstractSocket}, others...)
	}
	return others
}

// webViewSource supplies the browser half of the merge. The interface keeps
// driver tests independent of a live Chrome process.
type webViewSource interface {
	ContentDescriptor(context.Context, device.ContentDescriptorRequest) (device.TreeNode, error)
	Close(context.Context) error
}

// dialWebView attaches to a forwarded DevTools endpoint. A variable so a test
// can stand one in.
var dialWebView = func(ctx context.Context, baseURL string) (webViewSource, error) {
	driver := web.NewDriver(baseURL, nil)
	if err := driver.Open(ctx); err != nil {
		return nil, err
	}
	return driver, nil
}

// SetAndroidChromeDevToolsEnabled turns the WebView merge on or off.
//
// Enabling publishes the device's abstract DevTools socket on a host port and
// attaches to it; disabling closes the connection and removes the forward. A
// forward left behind pins its host port for the life of the adb server, so the
// next shard that reserves that number gets a socket pointing at a dead page.
func (driver *Driver) SetAndroidChromeDevToolsEnabled(
	ctx context.Context, request device.ChromeDevToolsRequest,
) error {
	if !request.Enabled {
		return driver.disableDevTools(ctx)
	}
	if driver.devtools != nil {
		return nil
	}
	sockets := driver.devToolsSockets(ctx)
	lastErr := error(nil)
	for _, socket := range sockets {
		source, port, err := driver.attachDevTools(ctx, socket)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", socket, err)
			continue
		}
		driver.devtools = source
		driver.devtoolsPort = port
		return nil
	}
	// Stock devices may publish no DevTools socket. Preserve the native
	// hierarchy and emit an actionable notice.
	if lastErr == nil {
		lastErr = errors.New("the device publishes no devtools socket")
	}
	fmt.Fprintf(driver.notice(),
		"flowbaton: androidWebViewHierarchy: devtools was asked for, but no devtools "+
			"endpoint answered; continuing with the accessibility hierarchy (%v)\n", lastErr)
	return nil
}

// attachDevTools forwards one abstract socket and dials it. A failed attempt
// takes its forward with it: one left behind pins its host port for the life of
// the adb server, so the next shard that reserves that number gets a socket
// pointing at a dead page.
func (driver *Driver) attachDevTools(
	ctx context.Context, socket string,
) (webViewSource, int, error) {
	port, err := reserveHostPort()
	if err != nil {
		return nil, 0, fmt.Errorf("reserving a devtools host port: %w", err)
	}
	if err := driver.adb.ForwardAbstract(ctx, port, socket); err != nil {
		return nil, 0, fmt.Errorf("forwarding the devtools socket: %w", err)
	}
	source, err := dialWebView(ctx, "http://127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		_ = driver.adb.ForwardRemove(ctx, port)
		return nil, 0, err
	}
	return source, port, nil
}

// notice is where the degradation message goes. Stderr unless a test set one.
func (driver *Driver) notice() io.Writer {
	if driver.devtoolsNotice != nil {
		return driver.devtoolsNotice
	}
	return os.Stderr
}

// disableDevTools tears both halves down. It reports the first failure but
// always attempts both, because a browser that will not close is no reason to
// leak the forward as well.
func (driver *Driver) disableDevTools(ctx context.Context) error {
	if driver.devtools == nil {
		return nil
	}
	source, port := driver.devtools, driver.devtoolsPort
	driver.devtools, driver.devtoolsPort = nil, 0
	closeErr := source.Close(ctx)
	if err := driver.adb.ForwardRemove(ctx, port); err != nil && closeErr == nil {
		closeErr = err
	}
	return closeErr
}

// webViewHierarchy merges the attached page into the accessibility dump. It
// returns the native tree unchanged when devtools is off.
//
// A page the browser will not describe fails the read rather than falling back:
// silently returning the native tree would hand the flow the opaque WebView it
// asked not to have, and its assertions would then fail somewhere else.
func (driver *Driver) webViewHierarchy(
	ctx context.Context, native device.TreeNode,
) (device.TreeNode, error) {
	if driver.devtools == nil {
		return native, nil
	}
	page, err := driver.devtools.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
	if err != nil {
		return device.TreeNode{}, fmt.Errorf("reading the webview hierarchy: %w", err)
	}
	return mergeWebViewHierarchy(native, page)
}

// reserveHostPort binds port zero and returns the assigned host port without
// scanning.
func reserveHostPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", listener.Addr())
	}
	return address.Port, nil
}
