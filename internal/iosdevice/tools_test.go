package iosdevice

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goios "github.com/danielpaulus/go-ios/ios"

	"github.com/larchwave/flowbaton/internal/device"
)

func boundTools(t *testing.T) *Tools {
	t.Helper()
	tools := NewTools("00008110-TEST")
	tools.bind(goios.DeviceEntry{})
	return tools
}

func TestSetLocationHoldsTheSessionAndSwapsWithoutAGap(t *testing.T) {
	tools := boundTools(t)
	var opened []*closeRecorder
	tools.startLocation = func(goios.DeviceEntry, float64, float64) (io.Closer, error) {
		recorder := &closeRecorder{}
		opened = append(opened, recorder)
		return recorder, nil
	}

	if err := tools.SetLocation(context.Background(), 43.65, -79.38); err != nil {
		t.Fatalf("SetLocation: %v", err)
	}
	if len(opened) != 1 || opened[0].closed != 0 {
		t.Fatal("the first location session must stay open to hold the location")
	}

	if err := tools.SetLocation(context.Background(), 48.85, 2.35); err != nil {
		t.Fatalf("second SetLocation: %v", err)
	}
	if opened[0].closed != 1 {
		t.Fatal("the first session must close after the second replaces it")
	}
	if opened[1].closed != 0 {
		t.Fatal("the live session must stay open")
	}

	if err := tools.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if opened[1].closed != 1 {
		t.Fatal("close() must release the held location session")
	}
	if err := tools.close(); err != nil {
		t.Fatalf("second close must be a no-op, got %v", err)
	}
}

func TestSetLocationFailureKeepsThePreviousSession(t *testing.T) {
	tools := boundTools(t)
	held := &closeRecorder{}
	tools.locationSession = held
	tools.startLocation = func(goios.DeviceEntry, float64, float64) (io.Closer, error) {
		return nil, errors.New("service refused")
	}
	if err := tools.SetLocation(context.Background(), 1, 2); err == nil {
		t.Fatal("expected the service failure")
	}
	if held.closed != 0 {
		t.Fatal("a failed swap must not drop the held location")
	}
}

func TestClearAppStateRequiresTheConfiguredArchive(t *testing.T) {
	t.Setenv(appIPAEnv, "")
	driver := NewDriver("00008110-TEST", 30001, nil, nil)
	err := driver.ClearAppState(context.Background(), device.AppRequest{AppID: "com.example.app"})
	if err == nil || !strings.Contains(err.Error(), appIPAEnv) {
		t.Fatalf("error = %v, want it to name %s", err, appIPAEnv)
	}

	t.Setenv(appIPAEnv, filepath.Join(t.TempDir(), "missing.ipa"))
	err = driver.ClearAppState(context.Background(), device.AppRequest{AppID: "com.example.app"})
	if err == nil || !strings.Contains(err.Error(), "unreadable") {
		t.Fatalf("error = %v, want the unreadable archive named", err)
	}
}

func TestClearAppStateUninstallsThenReinstallsTheArchive(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "app.ipa")
	if err := os.WriteFile(archive, []byte("ipa"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(appIPAEnv, archive)

	driver := NewDriver("00008110-TEST", 30001, nil, nil)
	driver.tools.bind(goios.DeviceEntry{})
	var order []string
	driver.tools.uninstall = func(_ goios.DeviceEntry, bundleID string) error {
		order = append(order, "uninstall "+bundleID)
		return nil
	}
	driver.tools.install = func(_ goios.DeviceEntry, path string) error {
		order = append(order, "install "+path)
		return nil
	}

	if err := driver.ClearAppState(context.Background(), device.AppRequest{AppID: "com.example.app"}); err != nil {
		t.Fatalf("ClearAppState: %v", err)
	}
	want := []string{"uninstall com.example.app", "install " + archive}
	if len(order) != 2 || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestOpenURLRelaunchesSafariWithTheLink(t *testing.T) {
	tools := boundTools(t)
	var launchedBundle string
	var launchedArgs []any
	var launchedOpts map[string]any
	tools.launch = func(_ goios.DeviceEntry, bundleID string, args []any, _ map[string]any, opts map[string]any) (uint64, error) {
		launchedBundle = bundleID
		launchedArgs = args
		launchedOpts = opts
		return 42, nil
	}

	if err := tools.OpenURL(context.Background(), "https://example.invalid/path"); err != nil {
		t.Fatalf("OpenURL: %v", err)
	}
	if launchedBundle != safariBundleID {
		t.Fatalf("launched %q, want Safari", launchedBundle)
	}
	if len(launchedArgs) != 2 || launchedArgs[0] != "-u" || launchedArgs[1] != "https://example.invalid/path" {
		t.Fatalf("args = %v, want the -u link pair", launchedArgs)
	}
	if launchedOpts["KillExisting"] != 1 {
		t.Fatal("Safari must relaunch so repeated openLink steps are deterministic")
	}
}
