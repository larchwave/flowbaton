package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/larchwave/flowbaton/internal/android"
	"github.com/larchwave/flowbaton/internal/ios"
	"github.com/larchwave/flowbaton/internal/iosdevice"
	"github.com/larchwave/flowbaton/internal/web"
)

// list-devices answers "what can I target?" through simctl for iOS, adb for
// Android, and the web pseudo-device. It reads inventory only; nothing is
// booted, installed, or changed.

// ListDevicesRunner holds the listing calls behind fields so a test can
// stand in a fake without a simulator or an attached phone. The defaults reach
// real tooling.
type ListDevicesRunner struct {
	IOS         func(context.Context) ([]ios.Device, error)
	IOSPhysical func(context.Context) ([]iosdevice.Device, error)
	Android     func(context.Context) ([]android.Device, error)
}

func (runner ListDevicesRunner) iosList() func(context.Context) ([]ios.Device, error) {
	if runner.IOS != nil {
		return runner.IOS
	}
	return ios.NewSimctl("", nil).ListDevices
}

func (runner ListDevicesRunner) iosPhysicalList() func(context.Context) ([]iosdevice.Device, error) {
	if runner.IOSPhysical != nil {
		return runner.IOSPhysical
	}
	return iosdevice.ListDevices
}

func (runner ListDevicesRunner) androidList() func(context.Context) ([]android.Device, error) {
	if runner.Android != nil {
		return runner.Android
	}
	return func(ctx context.Context) ([]android.Device, error) {
		return android.ListDevices(ctx, nil)
	}
}

// Run lists devices for the selected platform(s). With no -p it lists both, and
// a platform whose tooling is absent is a note rather than a failure — the
// command is most wanted exactly on a machine that has only one SDK. With an
// explicit -p, that platform's error is the failure the operator asked to hear.
func (runner ListDevicesRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	platform, code := listDevicesPlatform(args, stderr)
	if code != ExitOK {
		return code
	}

	wantIOS := platform == "" || platform == "ios"
	wantAndroid := platform == "" || platform == "android"
	wantWeb := platform == "" || platform == "web"
	explicit := platform != ""

	listed := 0
	failed := false

	if wantIOS {
		devices, err := runner.iosList()(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "ios: %v\n", err)
		}
		for _, d := range devices {
			fmt.Fprintln(stdout, formatIOSDevice(d))
			listed++
		}
		physical, physicalErr := runner.iosPhysicalList()(ctx)
		if physicalErr != nil {
			fmt.Fprintf(stderr, "ios devices: %v\n", physicalErr)
		}
		for _, d := range physical {
			fmt.Fprintln(stdout, formatIOSPhysicalDevice(d))
			listed++
		}
		// One inventory failing is a note — a Linux host has no simctl, a
		// mac without usbmuxd running has no hardware list. Both failing
		// under an explicit -p ios is the failure the operator asked about.
		failed = failed || (explicit && err != nil && physicalErr != nil)
	}
	if wantAndroid {
		devices, err := runner.androidList()(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "android: %v\n", err)
			failed = failed || explicit
		}
		for _, d := range devices {
			fmt.Fprintln(stdout, formatAndroidDevice(d))
			listed++
		}
	}
	if wantWeb {
		// Deliberately not counted: the pseudo-device is always there, so
		// counting it would make "no devices attached" impossible to print,
		// and that line is the answer an operator with nothing plugged in
		// actually needs.
		fmt.Fprintln(stdout, formatWebPseudoDevice())
	}

	if failed {
		return ExitFailure
	}
	// Only when a platform that enumerates real hardware was in scope: with
	// -p web the pseudo-device above IS the answer, and the note would
	// contradict the line printed right before it.
	if listed == 0 && (wantIOS || wantAndroid) {
		fmt.Fprintln(stderr, "no devices found")
	}
	return ExitOK
}

// listDevicesPlatform reads the optional -p/--platform flag and refuses stray
// arguments. Returning ExitOK with an empty platform means "list both".
func listDevicesPlatform(args []string, stderr io.Writer) (string, int) {
	platform := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-p" || arg == "--platform":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "list-devices: -p needs a platform (ios or android)")
				return "", ExitInvalid
			}
			platform = args[i+1]
			i++
		case strings.HasPrefix(arg, "--platform="):
			platform = strings.TrimPrefix(arg, "--platform=")
		default:
			fmt.Fprintf(stderr, "list-devices: unexpected argument %q\n", arg)
			return "", ExitInvalid
		}
	}
	if platform != "" && platform != "ios" && platform != "android" && platform != "web" {
		fmt.Fprintf(stderr, "list-devices: unknown platform %q (want ios, android or web)\n", platform)
		return "", ExitInvalid
	}
	return platform, ExitOK
}

func formatIOSDevice(d ios.Device) string {
	line := fmt.Sprintf("ios\t%s\t%s\t%s", d.UDID, d.State, d.Name)
	if d.Runtime != "" {
		line += fmt.Sprintf(" (%s)", d.Runtime)
	}
	return line
}

func formatIOSPhysicalDevice(d iosdevice.Device) string {
	return fmt.Sprintf("ios\t%s\tattached\tphysical device", d.UDID)
}

func formatAndroidDevice(d android.Device) string {
	return fmt.Sprintf("android\t%s\t%s\t%s", d.Serial, d.State, d.Model)
}

// formatWebPseudoDevice is specs/02-device-drivers.md:53's "web pseudo-device".
// There is nothing to enumerate — a browser is launched per run, not attached
// beforehand — so the useful thing to report is whether the binary the driver
// would launch is actually there. An operator whose web run fails at launch
// gets the answer directly from the listing command.
func formatWebPseudoDevice() string {
	binary := os.Getenv("FLOWBATON_CHROME")
	if binary == "" {
		binary = web.DefaultChromeBinary()
	}
	state := "available"
	if _, err := os.Stat(binary); err != nil {
		state = "missing"
	}
	return fmt.Sprintf("web\tchrome\t%s\t%s", state, binary)
}
