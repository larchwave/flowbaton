package ios

import (
	"context"
	"time"
)

// DeviceTools is the out-of-app half of the driver: everything it does to
// the device from outside the runner's wire. The simulator implementation
// is Simctl; a physical device supplies its own implementation, and the
// Driver cannot tell them apart. The in-app half stays on Client.
type DeviceTools interface {
	Launch(ctx context.Context, bundleID string, arguments []LaunchArgument, terminateRunning bool) error
	Terminate(ctx context.Context, bundleID string) error
	Uninstall(ctx context.Context, bundleID string) error
	AppContainer(ctx context.Context, bundleID string) (string, error)
	Install(ctx context.Context, appPath string) error
	Diagnose(ctx context.Context, outputDirectory string, timeout time.Duration) error
	ResetKeychain(ctx context.Context) error
	OpenURL(ctx context.Context, url string) error
	SetLocation(ctx context.Context, latitude, longitude float64) error
	AddMedia(ctx context.Context, paths []string) error
	SetPermission(ctx context.Context, bundleID, permission, grant string) error
}

var _ DeviceTools = (*Simctl)(nil)
