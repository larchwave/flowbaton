package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/nohavewho/flowbaton/internal/ios"
)

// start-device starts a target device (spec 03, spec 02 lines 81-83). iOS boots
// an existing simulator by udid, or with --force-create builds one via
// `simctl create` first. Android launches an AVD via `emulator -avd`, or with
// --force-create builds one via `avdmanager create` first. Every external tool
// sits behind an injectable func field so the orchestration is testable without
// a device.

// alreadyBooted is simctl's own wording when a boot target is already running.
//
// simctl exits non-zero for this, but the operator's goal — a running simulator
// — is already met, so it is success. Matched on the message, like Simctl's
// Terminate matches "found nothing to terminate", rather than on the exit code,
// which simctl reuses for unrelated conditions.
const alreadyBooted = "current state: Booted"

// StartDeviceRunner holds each external operation behind a field so tests can
// record calls without a real device. The defaults reach simctl/emulator.
type StartDeviceRunner struct {
	// Boot boots an existing iOS simulator by udid (default: simctl boot).
	Boot func(ctx context.Context, platform, udid string) error
	// ListAVDs enumerates installed Android AVD names (default: emulator -list-avds).
	ListAVDs func(ctx context.Context) ([]string, error)
	// LaunchAVD launches an Android AVD by name (default: emulator -avd <name>).
	LaunchAVD func(ctx context.Context, avd string) error
	// CreateSim creates a new iOS simulator and returns its udid (default: simctl create).
	CreateSim func(ctx context.Context, osVersion, deviceLocale string) (string, error)
	// CreateAVD creates a new Android AVD and returns its name (default: avdmanager create).
	CreateAVD func(ctx context.Context, osVersion, deviceLocale string) (string, error)
}

func (runner StartDeviceRunner) boot() func(context.Context, string, string) error {
	if runner.Boot != nil {
		return runner.Boot
	}
	return func(ctx context.Context, _ string, udid string) error {
		return ios.NewSimctl(udid, nil).Boot(ctx)
	}
}

func (runner StartDeviceRunner) listAVDs() func(context.Context) ([]string, error) {
	if runner.ListAVDs != nil {
		return runner.ListAVDs
	}
	return realListAVDs
}

func (runner StartDeviceRunner) launchAVD() func(context.Context, string) error {
	if runner.LaunchAVD != nil {
		return runner.LaunchAVD
	}
	return realLaunchAVD
}

func (runner StartDeviceRunner) createSim() func(context.Context, string, string) (string, error) {
	if runner.CreateSim != nil {
		return runner.CreateSim
	}
	return realCreateSim
}

func (runner StartDeviceRunner) createAVD() func(context.Context, string, string) (string, error) {
	if runner.CreateAVD != nil {
		return runner.CreateAVD
	}
	return realCreateAVD
}

func (runner StartDeviceRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseStartDeviceArgs(args, stderr)
	if code != ExitOK {
		return code
	}
	if !options.forceCreate && (options.osVersion != "" || options.deviceLocale != "") {
		// os-version/locale only shape a freshly created device; an existing one
		// keeps its own. Say so rather than silently dropping the operator's intent.
		fmt.Fprintln(stderr, "start-device: --os-version/--device-locale are ignored without --force-create")
	}

	switch options.platform {
	case "ios":
		return runner.startIOS(ctx, options, stdout, stderr)
	case "android":
		return runner.startAndroid(ctx, options, stdout, stderr)
	}
	// parseStartDeviceArgs guarantees one of the two above.
	return ExitFailure
}

func (runner StartDeviceRunner) startIOS(ctx context.Context, options startDeviceArgs, stdout, stderr io.Writer) int {
	udid := options.udid
	if options.forceCreate {
		created, err := runner.createSim()(ctx, options.osVersion, options.deviceLocale)
		if err != nil {
			fmt.Fprintf(stderr, "start-device: %v\n", err)
			return ExitFailure
		}
		udid = created
	}
	if err := runner.boot()(ctx, options.platform, udid); err != nil {
		if strings.Contains(err.Error(), alreadyBooted) {
			fmt.Fprintf(stdout, "already booted %s\n", udid)
			return ExitOK
		}
		fmt.Fprintf(stderr, "start-device: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "booted %s\n", udid)
	return ExitOK
}

func (runner StartDeviceRunner) startAndroid(ctx context.Context, options startDeviceArgs, stdout, stderr io.Writer) int {
	avd := options.udid
	switch {
	case options.forceCreate:
		created, err := runner.createAVD()(ctx, options.osVersion, options.deviceLocale)
		if err != nil {
			fmt.Fprintf(stderr, "start-device: %v\n", err)
			return ExitFailure
		}
		avd = created
	case avd == "":
		// No named AVD: discover and take the first installed one.
		names, err := runner.listAVDs()(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "start-device: %v\n", err)
			return ExitFailure
		}
		if len(names) == 0 {
			fmt.Fprintln(stderr, "start-device: no Android AVD to launch (create one, or pass --force-create)")
			return ExitFailure
		}
		avd = names[0]
	}
	if err := runner.launchAVD()(ctx, avd); err != nil {
		fmt.Fprintf(stderr, "start-device: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "launched %s\n", avd)
	return ExitOK
}

// realCreateSim creates a simulator via `simctl create` and returns the udid it
// prints. os-version, when given, selects the runtime; the device type has no
// flag in our surface, so it defaults below.
//
// ponytail: default device type "iPhone 15", no --device-model flag in spec 03 —
// widen the flag surface if a specific model is needed. Untestable without Xcode;
// covered by the injected CreateSim seam.
func realCreateSim(ctx context.Context, osVersion, _ string) (string, error) {
	name := "flowbaton-sim"
	args := []string{"simctl", "create", name, "iPhone 15"}
	if osVersion != "" {
		args = append(args, "iOS"+strings.ReplaceAll(osVersion, ".", "-"))
	}
	output, err := exec.CommandContext(ctx, "xcrun", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("simctl create: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
}

// realListAVDs enumerates installed AVDs via `emulator -list-avds`, one name per
// line.
func realListAVDs(ctx context.Context) ([]string, error) {
	output, err := exec.CommandContext(ctx, "emulator", "-list-avds").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("emulator -list-avds: %w: %s", err, strings.TrimSpace(string(output)))
	}
	var names []string
	for _, line := range strings.Split(string(output), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// realLaunchAVD boots an emulator for the named AVD. The process is detached
// (Start, not Wait): the emulator runs for as long as the device is up, well
// past this command, and tying it to ctx would kill it on return.
//
// ponytail: no boot-wait poll (spec 02 mentions "wait for boot"); adb
// wait-for-device polling can be added if a caller needs the device ready before
// start-device returns. Untestable without an SDK; covered by the LaunchAVD seam.
func realLaunchAVD(_ context.Context, avd string) error {
	cmd := exec.Command("emulator", "-avd", avd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("emulator -avd %s: %w", avd, err)
	}
	return nil
}

// realCreateAVD creates an AVD via `avdmanager create avd`. os-version selects
// the system image API level.
//
// ponytail: fixed name "flowbaton_avd" and a pixel-neutral image; no
// system-image install detection (spec 02 notes it), device model, or locale
// wiring — widen when a specific model/image is needed. Untestable without an
// SDK; covered by the CreateAVD seam.
func realCreateAVD(ctx context.Context, osVersion, _ string) (string, error) {
	name := "flowbaton_avd"
	apiLevel := osVersion
	if apiLevel == "" {
		apiLevel = "34"
	}
	pkg := fmt.Sprintf("system-images;android-%s;google_apis;x86_64", apiLevel)
	cmd := exec.CommandContext(ctx, "avdmanager", "create", "avd", "-n", name, "-k", pkg, "--force")
	// avdmanager prompts "Do you wish to create a custom hardware profile? [no]";
	// feed a newline so it takes the default instead of blocking.
	cmd.Stdin = strings.NewReader("\n")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("avdmanager create: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return name, nil
}

type startDeviceArgs struct {
	platform     string
	udid         string
	osVersion    string
	deviceLocale string
	forceCreate  bool
}

func parseStartDeviceArgs(args []string, stderr io.Writer) (startDeviceArgs, int) {
	var parsed startDeviceArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "start-device: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "-p" || arg == "--platform":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.platform = value
		case strings.HasPrefix(arg, "--platform="):
			parsed.platform = strings.TrimPrefix(arg, "--platform=")
		case arg == "--device" || arg == "--udid":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.udid = value
		case strings.HasPrefix(arg, "--device="):
			parsed.udid = strings.TrimPrefix(arg, "--device=")
		case strings.HasPrefix(arg, "--udid="):
			parsed.udid = strings.TrimPrefix(arg, "--udid=")
		case arg == "--os-version":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.osVersion = value
		case strings.HasPrefix(arg, "--os-version="):
			parsed.osVersion = strings.TrimPrefix(arg, "--os-version=")
		case arg == "--device-locale":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.deviceLocale = value
		case strings.HasPrefix(arg, "--device-locale="):
			parsed.deviceLocale = strings.TrimPrefix(arg, "--device-locale=")
		case arg == "--force-create":
			parsed.forceCreate = true
		default:
			fmt.Fprintf(stderr, "start-device: unexpected argument %q\n", arg)
			return parsed, ExitInvalid
		}
	}

	switch parsed.platform {
	case "ios", "android":
	case "":
		fmt.Fprintln(stderr, "start-device: a platform is required: pass -p ios or -p android")
		return parsed, ExitInvalid
	default:
		fmt.Fprintf(stderr, "start-device: unknown platform %q (want ios or android)\n", parsed.platform)
		return parsed, ExitInvalid
	}
	// iOS boots an existing simulator by udid; Android can discover its AVD and
	// --force-create builds a fresh device, so a target is only mandatory for a
	// plain iOS boot.
	if parsed.platform == "ios" && !parsed.forceCreate && parsed.udid == "" {
		fmt.Fprintln(stderr, "start-device: a device is required: pass --device <udid> (or --force-create)")
		return parsed, ExitInvalid
	}
	return parsed, ExitOK
}
