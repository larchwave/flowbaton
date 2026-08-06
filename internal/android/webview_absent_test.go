package android

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// A WebView hierarchy request without a DevTools socket uses the native
// accessibility tree and reports one diagnostic. DOM-only nodes remain absent,
// but endpoint absence does not fail the run.

func TestAnAbsentDevToolsEndpointDoesNotFailTheRun(t *testing.T) {
	withStubWebView(t, nil) // nil stub → the dialer refuses, as an absent socket does
	driver, _, _ := newOpenDriver(t, webViewAgent)
	var notices strings.Builder
	driver.devtoolsNotice = &notices

	err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true})
	if err != nil {
		t.Fatalf("an absent devtools endpoint failed the run: %v", err)
	}
	if driver.devtools != nil {
		t.Fatal("the driver kept a devtools source it never attached")
	}
	if !strings.Contains(notices.String(), "devtools") {
		t.Fatalf("the operator was told nothing about the mode they asked for: %q", notices.String())
	}
}

// The forward has to go with it. One left behind pins its host port for the
// life of the adb server, and the next shard that reserves that number gets a
// socket pointing at nothing.
func TestAnAbsentEndpointLeavesNoForwardBehind(t *testing.T) {
	withStubWebView(t, nil)
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	driver.devtoolsNotice = &strings.Builder{}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}

	forwarded, removed := 0, 0
	for _, call := range runner.recorded() {
		if len(call) < 4 {
			continue
		}
		switch {
		case call[2] == "forward" && strings.HasPrefix(call[4], "localabstract:"):
			forwarded++
		case call[2] == "forward" && call[3] == "--remove":
			removed++
		}
	}
	if forwarded != removed {
		t.Fatalf("forwarded %d abstract sockets and removed %d: %v",
			forwarded, removed, runner.recorded())
	}
}

// The control: an endpoint that DOES answer is still attached and still merged,
// or the degradation would have quietly turned the feature off for everybody.
func TestAnEndpointThatAnswersIsStillAttached(t *testing.T) {
	dialled := withStubWebView(t, &stubWebView{tree: stubPage})
	driver, runner, _ := newOpenDriver(t, webViewAgent)
	driver.devtoolsNotice = &strings.Builder{}

	if err := driver.SetAndroidChromeDevToolsEnabled(
		context.Background(), device.ChromeDevToolsRequest{Enabled: true}); err != nil {
		t.Fatalf("SetAndroidChromeDevToolsEnabled() error = %v", err)
	}
	if driver.devtools == nil {
		t.Fatal("a working endpoint was not attached")
	}
	want := []string{"-s", testSerial, "forward",
		"tcp:" + strconv.Itoa(driver.devtoolsPort), "localabstract:chrome_devtools_remote"}
	found := false
	for _, call := range runner.recorded() {
		if reflect.DeepEqual(call[1:], want) {
			found = true
		}
	}
	if !found || len(*dialled) != 1 {
		t.Fatalf("the working endpoint was not forwarded and dialled: %v", runner.recorded())
	}
}
