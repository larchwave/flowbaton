package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/ios"
	"github.com/larchwave/flowbaton/internal/web"
)

// Resolving the operator's -p/--device into a driver.
//
// iOS requires an explicit --device udid. Android may resolve one from the
// adb inventory instead, because `adb devices -l` is authoritative in a way
// `simctl list` is not: it lists only attached, usable devices, and the
// one-device case is the ordinary one.

// androidInventory lists the usable adb devices; a variable so tests can
// stand in a fake without a device attached.
var androidInventory = func(ctx context.Context) ([]android.Device, error) {
	return android.ListDevices(ctx, nil)
}

var (
	resolveAndroidAgentAPKs = androidAgentAPKs
	resolveIOSRunnerBundle  = iosRunnerBundle
)

// resolveAndroidSerial picks the device when the operator named none:
// exactly one connected device is used, anything else is refused with the
// candidates named — a guessed device runs the suite somewhere the report
// will not admit to.
func resolveAndroidSerial() (string, error) {
	devices, err := androidInventory(context.Background())
	if err != nil {
		return "", fmt.Errorf("listing android devices: %w", err)
	}
	switch len(devices) {
	case 0:
		return "", fmt.Errorf("no android devices are connected: adb reported none")
	case 1:
		return devices[0].Serial, nil
	}
	serials := make([]string, 0, len(devices))
	for _, entry := range devices {
		serials = append(serials, entry.Serial)
	}
	return "", fmt.Errorf(
		"several android devices are connected (%s): pass --device <serial>",
		strings.Join(serials, ", "))
}

// NewDeviceSession builds a session for one shard, or explains why it cannot.
func NewDeviceSession(options TestOptions, shard Shard) (DeviceSession, error) {
	driver, err := newDriver(options, shard.Device, shard.DriverPort, shard.Count())
	if err != nil {
		return DeviceSession{}, err
	}
	return DeviceSession{
		Driver: driver,
		// Where a shard writes is the runner's decision, not the session's:
		// only the runner knows how many shards there are.
		OutputDirectory: shard.OutputDirectory,
		BaseDirectory:   baseDirectoryFor(options.Roots),
		Shard:           shard,
	}, nil
}

func newDriver(options TestOptions, udid string, port int, shardNumber int) (device.Driver, error) {
	switch strings.ToLower(options.Platform) {
	case "ios":
		if udid == "" {
			return nil, fmt.Errorf("a simulator udid is required: pass --device <udid>")
		}
		if port <= 0 {
			// Fails closed rather than defaulting to the contract port: a zero
			// port means nobody assigned one, and quietly using 22087 would put
			// every shard on one runner — the failure this batch prevents.
			return nil, fmt.Errorf("shard %d has no driver port assigned", shardNumber)
		}
		var bundle *ios.RunnerBundle
		if options.ReinstallDriver {
			var err error
			bundle, err = resolveIOSRunnerBundle()
			if err != nil {
				return nil, err
			}
		}
		return ios.NewDriver(
			udid,
			port,
			ios.NewClient(ios.DefaultBaseURL(port)),
			ios.NewSimctl(udid, nil),
			bundle,
		), nil
	case "android":
		serial := udid
		if serial == "" {
			resolved, err := resolveAndroidSerial()
			if err != nil {
				return nil, err
			}
			serial = resolved
		}
		if port <= 0 {
			// Same fail-closed rule as iOS: a zero port means nobody assigned
			// one, and defaulting would put every shard on one agent.
			return nil, fmt.Errorf("shard %d has no driver port assigned", shardNumber)
		}
		var apks *android.AgentAPKs
		if options.ReinstallDriver {
			var err error
			apks, err = resolveAndroidAgentAPKs()
			if err != nil {
				return nil, err
			}
		}
		return android.NewDriver(serial, port, nil, apks), nil
	case "web":
		// No udid: a browser is not a device the operator picks between. The
		// port is still required, and it is the DevTools port the browser will
		// listen on, so two shards cannot land in one browser.
		if port <= 0 {
			return nil, fmt.Errorf("shard %d has no devtools port assigned", shardNumber)
		}
		chrome, err := webChromeOptions(options, port)
		if err != nil {
			return nil, err
		}
		return web.NewLaunchingDriver(chrome), nil
	case "":
		return nil, fmt.Errorf("a platform is required: pass -p ios, -p android or -p web")
	default:
		return nil, fmt.Errorf("unsupported platform %q; supported: ios, android, web", options.Platform)
	}
}

// baseDirectoryFor resolves flow resources relative to the first root. A
// directory root is itself the base; a file root's directory is.
func baseDirectoryFor(roots []string) string {
	if len(roots) == 0 {
		return "."
	}
	info, err := os.Stat(roots[0])
	if err == nil && info.IsDir() {
		return roots[0]
	}
	return filepath.Dir(roots[0])
}
