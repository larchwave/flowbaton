package ios

import (
	"context"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

// fakeDeviceTools records which method ran so the test proves the Driver
// reaches the device through the DeviceTools seam, not through *Simctl.
type fakeDeviceTools struct {
	calls []string
}

func (fake *fakeDeviceTools) note(call string) { fake.calls = append(fake.calls, call) }

func (fake *fakeDeviceTools) Launch(_ context.Context, bundleID string, _ []LaunchArgument, _ bool) error {
	fake.note("launch " + bundleID)
	return nil
}

func (fake *fakeDeviceTools) Terminate(_ context.Context, bundleID string) error {
	fake.note("terminate " + bundleID)
	return nil
}

func (fake *fakeDeviceTools) Uninstall(_ context.Context, bundleID string) error {
	fake.note("uninstall " + bundleID)
	return nil
}

func (fake *fakeDeviceTools) AppContainer(_ context.Context, bundleID string) (string, error) {
	fake.note("container " + bundleID)
	return "", nil
}

func (fake *fakeDeviceTools) Install(_ context.Context, appPath string) error {
	fake.note("install " + appPath)
	return nil
}

func (fake *fakeDeviceTools) Diagnose(_ context.Context, _ string, _ time.Duration) error {
	fake.note("diagnose")
	return nil
}

func (fake *fakeDeviceTools) ResetKeychain(context.Context) error {
	fake.note("resetKeychain")
	return nil
}

func (fake *fakeDeviceTools) OpenURL(_ context.Context, url string) error {
	fake.note("openURL " + url)
	return nil
}

func (fake *fakeDeviceTools) SetLocation(_ context.Context, _, _ float64) error {
	fake.note("setLocation")
	return nil
}

func (fake *fakeDeviceTools) AddMedia(_ context.Context, paths []string) error {
	fake.note("addMedia")
	return nil
}

func (fake *fakeDeviceTools) SetPermission(_ context.Context, bundleID, permission, grant string) error {
	fake.note("permission " + permission + "=" + grant)
	return nil
}

func TestDriverReachesTheDeviceThroughTheDeviceToolsSeam(t *testing.T) {
	fake := &fakeDeviceTools{}
	driver := NewDriver("UDID-1", 22087, nil, fake, nil)
	ctx := context.Background()

	if err := driver.StopApp(ctx, device.AppRequest{AppID: "com.example.a"}); err != nil {
		t.Fatalf("StopApp: %v", err)
	}
	if err := driver.ClearKeychain(ctx); err != nil {
		t.Fatalf("ClearKeychain: %v", err)
	}
	if err := driver.OpenLink(ctx, device.OpenLinkRequest{Link: "https://example.invalid"}); err != nil {
		t.Fatalf("OpenLink: %v", err)
	}
	if err := driver.SetLocation(ctx, device.Location{Latitude: 1, Longitude: 2}); err != nil {
		t.Fatalf("SetLocation: %v", err)
	}
	if err := driver.SetPermissions(ctx, device.PermissionsRequest{
		AppID:       "com.example.a",
		Permissions: map[string]string{"camera": "grant"},
	}); err != nil {
		t.Fatalf("SetPermissions: %v", err)
	}

	want := []string{
		"terminate com.example.a",
		"resetKeychain",
		"openURL https://example.invalid",
		"setLocation",
		"permission camera=grant",
	}
	if len(fake.calls) != len(want) {
		t.Fatalf("calls = %v, want %v", fake.calls, want)
	}
	for i, call := range want {
		if fake.calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all: %v)", i, fake.calls[i], call, fake.calls)
		}
	}
}
