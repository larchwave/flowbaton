package android

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// specs/02-device-drivers.md:44 names the localabstract CDP socket. A
// debuggable WebView publishes a PID-suffixed socket:
//
//	$ adb shell cat /proc/net/unix | grep -i devtools
//	0000000000000000: 00000002 … 965529 @webview_devtools_remote_26798
//
// WebView sockets use webview_devtools_remote_<pid>; Chrome uses
// chrome_devtools_remote. Discovery supports both forms.
//
// So the driver reads the device's abstract socket list and attaches to the
// socket that is actually present.

// unixSocketDump renders /proc/net/unix the way the kernel does: the path is
// the last field, and an abstract socket is prefixed with '@'.
func unixSocketDump(names ...string) []byte {
	var out strings.Builder
	out.WriteString("Num       RefCount Protocol Flags    Type St Inode Path\n")
	for index, name := range names {
		out.WriteString("0000000000000000: 00000002 00000000 00010000 0001 01 " +
			strconv.Itoa(900000+index) + " @" + name + "\n")
	}
	// A non-devtools socket and a pathless row, both of which a real dump has.
	out.WriteString("0000000000000000: 00000003 00000000 00000000 0001 01 12345 /dev/socket/zygote\n")
	out.WriteString("0000000000000000: 00000003 00000000 00000000 0002 01 12346 \n")
	return []byte(out.String())
}

// answerSocketDump makes `adb shell cat /proc/net/unix` return the given
// sockets and leaves every other adb call alone.
func answerSocketDump(names ...string) func([]string) ([]byte, error) {
	dump := unixSocketDump(names...)
	return func(args []string) ([]byte, error) {
		if len(args) > 0 && args[len(args)-1] == "/proc/net/unix" {
			return dump, nil
		}
		return nil, nil
	}
}

// forwardedSockets returns the localabstract names the driver forwarded, in order.
func forwardedSockets(runner *recordingRunner) []string {
	var sockets []string
	for _, call := range runner.recorded() {
		for _, argument := range call {
			if strings.HasPrefix(argument, "localabstract:") {
				sockets = append(sockets, strings.TrimPrefix(argument, "localabstract:"))
			}
		}
	}
	return sockets
}

// treeHasText walks the whole tree, so the assertion does not depend on where
// the merge decided to graft the page.
func treeHasText(node device.TreeNode, text string) bool {
	if node.Attributes["text"] == text {
		return true
	}
	for _, child := range node.Children {
		if treeHasText(child, text) {
			return true
		}
	}
	return false
}

func TestADebuggableWebViewsSocketIsFoundAndAttached(t *testing.T) {
	withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	runner.respond = answerSocketDump("webview_devtools_remote_26798")
	driver.devtoolsNotice = &strings.Builder{}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}
	if driver.devtools == nil {
		t.Fatal("the webview's devtools endpoint was published and not attached")
	}
	if got := forwardedSockets(runner); len(got) != 1 || got[0] != "webview_devtools_remote_26798" {
		t.Fatalf("forwarded %v, want the webview socket that is actually published", got)
	}
}

// Chrome's own name goes first when both are up: it is the one the spec names,
// and a browser under test is a likelier merge target than whatever WebView
// some background app happens to be debugging.
func TestChromesSocketWinsWhenBothArePublished(t *testing.T) {
	withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	runner.respond = answerSocketDump("webview_devtools_remote_26798", "chrome_devtools_remote")
	driver.devtoolsNotice = &strings.Builder{}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}
	if got := forwardedSockets(runner); len(got) != 1 || got[0] != devToolsAbstractSocket {
		t.Fatalf("forwarded %v, want %q first", got, devToolsAbstractSocket)
	}
}

// The merge is the point of all this, so it is asserted end to end: a native
// dump whose only leaf is an opaque WebView, plus the page behind the socket
// the driver had to discover, has to yield the button.
func TestTheWebViewSocketProducesAMergedTree(t *testing.T) {
	withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	runner.respond = answerSocketDump("webview_devtools_remote_26798")
	driver.devtoolsNotice = &strings.Builder{}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}
	tree, err := driver.ContentDescriptor(
		context.Background(), device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("ContentDescriptor() error = %v", err)
	}
	if !treeHasText(tree, "Sign in") {
		t.Fatalf("the merged tree has no page content: %+v", tree)
	}
	// The stub dialer answers any port, so the merge alone would pass with the
	// wrong socket forwarded. Pin the chain: the port the driver kept has to be
	// the one it forwarded the webview socket to.
	want := []string{"-s", testSerial, "forward",
		"tcp:" + strconv.Itoa(driver.devtoolsPort),
		"localabstract:webview_devtools_remote_26798"}
	for _, call := range runner.recorded() {
		if strings.Join(call[1:], " ") == strings.Join(want, " ") {
			return
		}
	}
	t.Fatalf("adb calls = %v, want %v", runner.recorded(), want)
}

// A dump with nothing debuggable in it must not be read as "attach anyway": the
// run degrades, says so, and leaves no forward behind.
func TestADumpWithNoDevToolsSocketDegrades(t *testing.T) {
	dialled := withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	runner.respond = answerSocketDump()
	var notices strings.Builder
	driver.devtoolsNotice = &notices

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}
	if driver.devtools != nil {
		t.Fatal("the driver attached to a socket the device never published")
	}
	if len(*dialled) != 0 {
		t.Fatalf("dialled %v with nothing published", *dialled)
	}
	if got := forwardedSockets(runner); len(got) != 0 {
		t.Fatalf("forwarded %v with nothing published", got)
	}
	if !strings.Contains(notices.String(), "devtools") {
		t.Fatalf("the operator was told nothing: %q", notices.String())
	}
}
