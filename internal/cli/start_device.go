package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/ios"
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

const startDeviceReadyTimeout = 2 * time.Minute

type deviceCreateOptions struct {
	Name        string
	OSVersion   string
	Locale      string
	Model       string
	SystemImage string
}

// StartDeviceRunner holds each external operation behind a field so tests can
// record calls without a real device. The defaults reach simctl/emulator.
type StartDeviceRunner struct {
	// Boot boots an existing iOS simulator by udid (default: simctl boot).
	Boot func(ctx context.Context, platform, udid string) error
	// ListAVDs enumerates installed Android AVD names (default: emulator -list-avds).
	ListAVDs func(ctx context.Context) ([]string, error)
	// LaunchAVD launches an Android AVD by name (default: emulator -avd <name>).
	LaunchAVD func(ctx context.Context, avd, locale string) error
	// CreateSim creates a new iOS simulator and returns its udid (default: simctl create).
	CreateSim func(ctx context.Context, options deviceCreateOptions) (string, error)
	// CreateAVD creates a new Android AVD and returns its name (default: avdmanager create).
	CreateAVD func(ctx context.Context, options deviceCreateOptions) (string, error)
	// WaitReady blocks until the started target is usable (default: simctl
	// bootstatus or adb's sys.boot_completed property).
	WaitReady func(ctx context.Context, platform, target string) error
	// ReadyTimeout bounds readiness independently from a long-lived parent.
	ReadyTimeout time.Duration
	// ConfigureLocale applies the requested locale after an iOS simulator is
	// booted. Android receives its locale as an emulator launch property.
	ConfigureLocale func(ctx context.Context, platform, target, locale string) error
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

func (runner StartDeviceRunner) launchAVD() func(context.Context, string, string) error {
	if runner.LaunchAVD != nil {
		return runner.LaunchAVD
	}
	return realLaunchAVD
}

func (runner StartDeviceRunner) createSim() func(context.Context, deviceCreateOptions) (string, error) {
	if runner.CreateSim != nil {
		return runner.CreateSim
	}
	return realCreateSim
}

func (runner StartDeviceRunner) createAVD() func(context.Context, deviceCreateOptions) (string, error) {
	if runner.CreateAVD != nil {
		return runner.CreateAVD
	}
	return realCreateAVD
}

func (runner StartDeviceRunner) waitReady(ctx context.Context, platform, target string) error {
	wait := runner.WaitReady
	if wait == nil {
		wait = realWaitReady
	}
	timeout := runner.ReadyTimeout
	if timeout <= 0 {
		timeout = startDeviceReadyTimeout
	}
	readyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := wait(readyCtx, platform, target); err != nil {
		return fmt.Errorf("waiting for %s %s readiness: %w", platform, target, err)
	}
	return nil
}

func (runner StartDeviceRunner) configureLocale() func(context.Context, string, string, string) error {
	if runner.ConfigureLocale != nil {
		return runner.ConfigureLocale
	}
	return realConfigureLocale
}

func (runner StartDeviceRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseStartDeviceArgs(args, stderr)
	if code != ExitOK {
		return code
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
		created, err := runner.createSim()(ctx, options.creationOptions())
		if err != nil {
			fmt.Fprintf(stderr, "start-device: %v\n", err)
			return ExitFailure
		}
		udid = created
	}
	alreadyRunning := false
	if err := runner.boot()(ctx, options.platform, udid); err != nil {
		if strings.Contains(err.Error(), alreadyBooted) {
			alreadyRunning = true
		} else {
			fmt.Fprintf(stderr, "start-device: %v\n", err)
			return ExitFailure
		}
	}
	if err := runner.waitReady(ctx, options.platform, udid); err != nil {
		fmt.Fprintf(stderr, "start-device: %v\n", err)
		return ExitFailure
	}
	if options.deviceLocale != "" {
		if err := runner.configureLocale()(ctx, options.platform, udid, options.deviceLocale); err != nil {
			fmt.Fprintf(stderr, "start-device: configuring locale: %v\n", err)
			return ExitFailure
		}
	}
	if alreadyRunning {
		fmt.Fprintf(stdout, "already booted %s\n", udid)
		return ExitOK
	}
	fmt.Fprintf(stdout, "booted %s\n", udid)
	return ExitOK
}

func (runner StartDeviceRunner) startAndroid(ctx context.Context, options startDeviceArgs, stdout, stderr io.Writer) int {
	avd := options.udid
	switch {
	case options.forceCreate:
		created, err := runner.createAVD()(ctx, defaultAndroidCreateOptions(options.creationOptions()))
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
	if err := runner.launchAVD()(ctx, avd, options.deviceLocale); err != nil {
		fmt.Fprintf(stderr, "start-device: %v\n", err)
		return ExitFailure
	}
	if err := runner.waitReady(ctx, options.platform, avd); err != nil {
		fmt.Fprintf(stderr, "start-device: %v\n", err)
		return ExitFailure
	}
	if options.deviceLocale != "" {
		if err := runner.configureLocale()(ctx, options.platform, avd, options.deviceLocale); err != nil {
			fmt.Fprintf(stderr, "start-device: verifying locale: %v\n", err)
			return ExitFailure
		}
	}
	fmt.Fprintf(stdout, "launched %s\n", avd)
	return ExitOK
}

// realCreateSim creates a simulator via `simctl create` and returns the udid it
// prints. The model and runtime are explicit when supplied; stable defaults are
// used otherwise.
func realCreateSim(ctx context.Context, options deviceCreateOptions) (string, error) {
	name := strings.TrimSpace(options.Name)
	if name == "" {
		name = "flowbaton-sim"
	}
	model := strings.TrimSpace(options.Model)
	if model == "" {
		model = "iPhone 15"
	}
	args := []string{"simctl", "create", name, model}
	if options.OSVersion != "" {
		args = append(args, "iOS"+strings.ReplaceAll(options.OSVersion, ".", "-"))
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
	emulator, err := androidSDKTool("emulator/emulator")
	if err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(ctx, emulator, "-list-avds").CombinedOutput()
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

func realWaitReady(ctx context.Context, platform, target string) error {
	switch platform {
	case "ios":
		output, err := exec.CommandContext(ctx, "xcrun", "simctl", "bootstatus", target, "-b").CombinedOutput()
		if err != nil {
			return fmt.Errorf("simctl bootstatus: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	case "android":
		adb, err := androidSDKTool("platform-tools/adb")
		if err != nil {
			return err
		}
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			serial, serialErr := androidEmulatorSerial(ctx, adb, target)
			if serialErr != nil {
				return serialErr
			}
			if serial != "" && androidDeviceReady(
				adbProperty(ctx, adb, serial, "sys.boot_completed"),
				adbProperty(ctx, adb, serial, "init.svc.bootanim"),
				exec.CommandContext(ctx, adb, "-s", serial,
					"shell", "pm", "path", "android").Run(),
			) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	default:
		return fmt.Errorf("unsupported readiness platform %q", platform)
	}
}

// androidDeviceReady decides Android readiness from three signals together:
// the boot property, the boot-animation service state, and a package-manager
// probe. The property alone flips well before the device can serve its first
// session, so start-device also waits for the animation to stop and for the
// package service to answer.
func androidDeviceReady(bootCompleted, bootAnimation string, packageManager error) bool {
	return strings.TrimSpace(bootCompleted) == "1" &&
		strings.TrimSpace(bootAnimation) == "stopped" &&
		packageManager == nil
}

// adbProperty reads one system property; a probe failure reads as "", which
// androidDeviceReady treats as not ready, and the poll loop retries.
func adbProperty(ctx context.Context, adb, serial, property string) string {
	output, err := exec.CommandContext(ctx, adb, "-s", serial,
		"shell", "getprop", property).CombinedOutput()
	if err != nil {
		return ""
	}
	return string(output)
}

func realConfigureLocale(ctx context.Context, platform, target, locale string) error {
	if strings.TrimSpace(locale) == "" {
		return nil
	}
	normalized := normalizeDeviceLocale(locale)
	if platform == "android" {
		adb, err := androidSDKTool("platform-tools/adb")
		if err != nil {
			return err
		}
		serial, err := androidEmulatorSerial(ctx, adb, target)
		if err != nil {
			return err
		}
		if serial == "" {
			return fmt.Errorf("Android emulator %q is not attached", target)
		}
		output, err := exec.CommandContext(ctx, adb, "-s", serial,
			"shell", "getprop", "persist.sys.locale").CombinedOutput()
		if err != nil {
			return fmt.Errorf("reading Android locale: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if actual := normalizeDeviceLocale(string(output)); actual != normalized {
			return fmt.Errorf("Android emulator locale is %q, want %q", actual, normalized)
		}
		return nil
	}
	if platform != "ios" {
		return fmt.Errorf("unsupported locale platform %q", platform)
	}
	output, err := exec.CommandContext(ctx, "xcrun", "simctl", "spawn", target,
		"defaults", "write", "NSGlobalDomain", "AppleLocale", strings.TrimSpace(locale)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("simctl locale: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func androidEmulatorSerial(ctx context.Context, adb, avd string) (string, error) {
	output, err := exec.CommandContext(ctx, adb, "devices").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb devices: %w: %s", err, strings.TrimSpace(string(output)))
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" || !strings.HasPrefix(fields[0], "emulator-") {
			continue
		}
		nameOutput, nameErr := exec.CommandContext(ctx, adb, "-s", fields[0], "emu", "avd", "name").CombinedOutput()
		if nameErr != nil {
			continue
		}
		name := strings.TrimSpace(strings.SplitN(string(nameOutput), "\n", 2)[0])
		if name == avd {
			return fields[0], nil
		}
	}
	return "", nil
}

// realLaunchAVD boots an emulator for the named AVD. The process is detached
// (Start, not Wait): the emulator runs for as long as the device is up, well
// past this command, and tying it to ctx would kill it on return.
func realLaunchAVD(_ context.Context, avd, locale string) error {
	emulator, err := androidSDKTool("emulator/emulator")
	if err != nil {
		return err
	}
	args := []string{"-avd", avd}
	if normalized := normalizeDeviceLocale(locale); normalized != "" {
		args = append(args, "-prop", "persist.sys.locale="+normalized)
	}
	cmd := exec.Command(emulator, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("emulator -avd %s: %w", avd, err)
	}
	return nil
}

// realCreateAVD creates an AVD only after sdkmanager and avdmanager confirm the
// requested image and hardware profile are installed.
func realCreateAVD(ctx context.Context, authored deviceCreateOptions) (string, error) {
	options := defaultAndroidCreateOptions(authored)
	if err := validateAndroidCreateOptions(authored, options); err != nil {
		return "", err
	}
	sdkmanager, err := androidCommandLineTool("sdkmanager")
	if err != nil {
		return "", err
	}
	installed, err := exec.CommandContext(ctx, sdkmanager, "--list_installed").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sdkmanager --list_installed: %w: %s", err, strings.TrimSpace(string(installed)))
	}
	if !sdkListContains(string(installed), options.SystemImage) {
		return "", fmt.Errorf("Android system image %q is not installed; install it with sdkmanager %q", options.SystemImage, options.SystemImage)
	}
	avdmanager, err := androidCommandLineTool("avdmanager")
	if err != nil {
		return "", err
	}
	devices, err := exec.CommandContext(ctx, avdmanager, "list", "device", "-c").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("avdmanager list device: %w: %s", err, strings.TrimSpace(string(devices)))
	}
	if !sdkListContains(string(devices), options.Model) {
		return "", fmt.Errorf("Android device model %q is not installed; inspect avdmanager list device", options.Model)
	}
	cmd := exec.CommandContext(ctx, avdmanager, "create", "avd", "-n", options.Name,
		"-k", options.SystemImage, "-d", options.Model, "--force")
	// avdmanager prompts "Do you wish to create a custom hardware profile? [no]";
	// feed a newline so it takes the default instead of blocking.
	cmd.Stdin = strings.NewReader("\n")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("avdmanager create: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return options.Name, nil
}

func validateAndroidCreateOptions(authored, resolved deviceCreateOptions) error {
	if authored.Name != "" && sanitizeAVDName(resolved.Name) != resolved.Name {
		return fmt.Errorf("Android AVD name %q may contain only letters, numbers, dot, dash, and underscore", resolved.Name)
	}
	if authored.OSVersion != "" && !strings.Contains(
		resolved.SystemImage, ";android-"+resolved.OSVersion+";") {
		return fmt.Errorf("Android system image %q does not match --os-version %s", resolved.SystemImage, resolved.OSVersion)
	}
	return nil
}

func defaultAndroidCreateOptions(options deviceCreateOptions) deviceCreateOptions {
	options.Name = strings.TrimSpace(options.Name)
	options.OSVersion = strings.TrimSpace(options.OSVersion)
	options.Locale = strings.TrimSpace(options.Locale)
	options.Model = strings.TrimSpace(options.Model)
	options.SystemImage = strings.TrimSpace(options.SystemImage)
	if options.OSVersion == "" {
		options.OSVersion = androidSystemImageAPI(options.SystemImage)
		if options.OSVersion == "" {
			options.OSVersion = "34"
		}
	}
	if options.Model == "" {
		options.Model = "pixel_6"
	}
	if options.SystemImage == "" {
		abi := "x86_64"
		if runtime.GOARCH == "arm64" {
			abi = "arm64-v8a"
		}
		options.SystemImage = fmt.Sprintf(
			"system-images;android-%s;google_apis;%s", options.OSVersion, abi)
	}
	if options.Name == "" {
		options.Name = sanitizeAVDName(fmt.Sprintf(
			"flowbaton_%s_api_%s_%s", options.Model, options.OSVersion, options.Locale))
	}
	return options
}

func androidSystemImageAPI(systemImage string) string {
	parts := strings.Split(systemImage, ";")
	if len(parts) < 2 || parts[0] != "system-images" || !strings.HasPrefix(parts[1], "android-") {
		return ""
	}
	return strings.TrimPrefix(parts[1], "android-")
}

func sanitizeAVDName(value string) string {
	var result strings.Builder
	lastUnderscore := false
	for _, char := range value {
		valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || char == '.' || char == '-'
		if valid {
			result.WriteRune(char)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			result.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(result.String(), "_")
}

func normalizeDeviceLocale(locale string) string {
	return strings.ReplaceAll(strings.TrimSpace(locale), "_", "-")
}

// sdkListContains matches the first column emitted by sdkmanager and the
// one-item-per-line output of avdmanager list device -c.
func sdkListContains(output, wanted string) bool {
	wanted = strings.TrimSpace(wanted)
	for _, line := range strings.Split(output, "\n") {
		item := strings.TrimSpace(strings.SplitN(line, "|", 2)[0])
		if item == wanted {
			return true
		}
	}
	return false
}

func androidSDKTool(relative string) (string, error) {
	sdk, err := androidSDKPath()
	if err != nil {
		return "", err
	}
	path := filepath.Join(sdk, filepath.FromSlash(relative))
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("Android SDK tool %s: %w", path, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("Android SDK tool %s is a directory", path)
	}
	return path, nil
}

func androidCommandLineTool(name string) (string, error) {
	sdk, err := androidSDKPath()
	if err != nil {
		return "", err
	}
	patterns := []string{
		filepath.Join(sdk, "cmdline-tools", "latest", "bin", name),
	}
	versioned, _ := filepath.Glob(filepath.Join(sdk, "cmdline-tools", "*", "bin", name))
	sort.Sort(sort.Reverse(sort.StringSlice(versioned)))
	patterns = append(patterns, versioned...)
	patterns = append(patterns, filepath.Join(sdk, "tools", "bin", name))
	seen := map[string]bool{}
	for _, path := range patterns {
		if seen[path] {
			continue
		}
		seen[path] = true
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			return path, nil
		}
	}
	return "", fmt.Errorf("Android SDK command-line tool %q was not found under %s", name, sdk)
}

type startDeviceArgs struct {
	platform     string
	udid         string
	osVersion    string
	deviceLocale string
	deviceModel  string
	systemImage  string
	forceCreate  bool
}

func (options startDeviceArgs) creationOptions() deviceCreateOptions {
	return deviceCreateOptions{
		Name: options.udid, OSVersion: options.osVersion, Locale: options.deviceLocale,
		Model: options.deviceModel, SystemImage: options.systemImage,
	}
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
		case arg == "--device-model":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.deviceModel = value
		case strings.HasPrefix(arg, "--device-model="):
			parsed.deviceModel = strings.TrimPrefix(arg, "--device-model=")
		case arg == "--system-image":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.systemImage = value
		case strings.HasPrefix(arg, "--system-image="):
			parsed.systemImage = strings.TrimPrefix(arg, "--system-image=")
		case arg == "--force-create":
			parsed.forceCreate = true
		default:
			fmt.Fprintf(stderr, "start-device: unexpected argument %q\n", arg)
			return parsed, ExitInvalid
		}
	}
	if !parsed.forceCreate && (parsed.osVersion != "" || parsed.deviceLocale != "" ||
		parsed.deviceModel != "" || parsed.systemImage != "") {
		fmt.Fprintln(stderr, "start-device: --os-version, --device-locale, --device-model, and --system-image require --force-create")
		return parsed, ExitInvalid
	}
	if parsed.platform == "ios" && parsed.systemImage != "" {
		fmt.Fprintln(stderr, "start-device: --system-image is Android-only")
		return parsed, ExitInvalid
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
