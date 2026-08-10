package android

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// The adb half of the Android host side. The gRPC agent owns what happens ON
// screen; adb owns everything around the app — lifecycle, permissions,
// settings, key events, the port forward, and the device inventory.
//
// The command runner is injected, so every argv this package builds is
// testable without a device. A wrong flag here is a wrong device operation,
// so tests pin argv shapes accepted by `am`, `pm`, `input`, `settings`, and
// `cmd connectivity`.

// CommandRunner executes one external command and returns its combined output.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands for real.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// adbExecutable resolves the adb binary. ANDROID_HOME is how the SDK names
// its install root, and platform-tools is rarely on PATH; falling back to a
// bare "adb" keeps machines that did put it there working.
func adbExecutable() string {
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		return filepath.Join(home, "platform-tools", "adb")
	}
	return "adb"
}

// chromePackage is the package openLink forces when the flow asked for a
// browser: openBrowser is a boolean "force Google Chrome" flag, Android-only.
const chromePackage = "com.android.chrome"

// The two installable halves of the on-device agent and the instrumentation
// entrypoint between them are defined by specs/02-device-drivers.md §2.2.
const (
	// AgentAppPackage is the app APK: drivers/android/agent's applicationId.
	AgentAppPackage = "dev.larchwave.flowbaton"
	// AgentTestPackage is the instrumentation APK that hosts the gRPC server.
	AgentTestPackage = "dev.larchwave.flowbaton.test"
	// agentServiceClass is the -e class filter FlowBatonDriverService#grpcServer
	// requires before it will serve (it skips itself on blanket runs).
	agentServiceClass = AgentAppPackage + ".FlowBatonDriverService#grpcServer"
	// agentRunnerComponent is the instrumentation component to start.
	agentRunnerComponent = AgentTestPackage + "/androidx.test.runner.AndroidJUnitRunner"
)

// Adb is the typed surface over adb for one device.
type Adb struct {
	serial string
	runner CommandRunner
}

// NewAdb binds a serial to a runner. An empty serial is legal only for the
// commands that are not device-scoped, such as ListDevices.
func NewAdb(serial string, runner CommandRunner) *Adb {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Adb{serial: serial, runner: runner}
}

// Device is one entry of the adb inventory.
type Device struct {
	Serial string
	State  string
	Model  string
}

// ForwardAdd publishes the on-device agent port on a host port. The forward
// is the transport the whole gRPC half rides on.
func (adb *Adb) ForwardAdd(ctx context.Context, hostPort, devicePort int) error {
	return adb.run(ctx,
		"forward", "tcp:"+strconv.Itoa(hostPort), "tcp:"+strconv.Itoa(devicePort))
}

// ForwardAbstract publishes an abstract unix socket on a host TCP port. The
// Chrome DevTools endpoint of a debuggable WebView lives on one of these
// (spec 02:44), so a TCP-to-TCP forward cannot reach it.
func (adb *Adb) ForwardAbstract(ctx context.Context, hostPort int, socket string) error {
	return adb.run(ctx,
		"forward", "tcp:"+strconv.Itoa(hostPort), "localabstract:"+socket)
}

func (adb *Adb) ForwardRemove(ctx context.Context, hostPort int) error {
	return adb.run(ctx, "forward", "--remove", "tcp:"+strconv.Itoa(hostPort))
}

func (adb *Adb) ForceStop(ctx context.Context, packageName string) error {
	return adb.shell(ctx, "am", "force-stop", packageName)
}

// Kill sends the system-initiated process death: the public docs pin killApp
// on Android to exactly `adb shell am kill <package>`, which the tool's own
// help describes as killing the app's background processes.
func (adb *Adb) Kill(ctx context.Context, packageName string) error {
	return adb.shell(ctx, "am", "kill", packageName)
}

// ClearPackageData is what clearState means on Android: pm clear wipes the
// app's data and cache without uninstalling it.
func (adb *Adb) ClearPackageData(ctx context.Context, packageName string) error {
	return adb.shell(ctx, "pm", "clear", packageName)
}

// AllowMockLocation lets a package feed the platform a mock location. The
// agent is an ordinary app, and LocationManager throws MOCK_LOCATION at one
// that has not been allowed the app-op — which is what setLocation hit on a
// real emulator.
func (adb *Adb) AllowMockLocation(ctx context.Context, packageName string) error {
	return adb.shell(ctx, "appops", "set", packageName, "android:mock_location", "allow")
}

func (adb *Adb) GrantPermission(ctx context.Context, packageName, permission string) error {
	return adb.shell(ctx, "pm", "grant", packageName, permission)
}

func (adb *Adb) RevokePermission(ctx context.Context, packageName, permission string) error {
	return adb.shell(ctx, "pm", "revoke", packageName, permission)
}

// RuntimePermissions lists the app's own changeable permissions, read off
// `dumpsys package`. This is what `all` expands to: only what the app
// requested can be granted, and pm refuses everything else.
func (adb *Adb) RuntimePermissions(ctx context.Context, packageName string) ([]string, error) {
	output, err := adb.shellOutput(ctx, "dumpsys", "package", packageName)
	if err != nil {
		return nil, err
	}
	return parseRuntimePermissions(string(output)), nil
}

// OpenLink fires the VIEW intent. am start may report resolution failures in
// its output while exiting successfully, so success requires reading both.
func (adb *Adb) OpenLink(ctx context.Context, link string, forceChrome bool) error {
	args := []string{"am", "start", "-a", "android.intent.action.VIEW", "-d", link}
	if forceChrome {
		args = append(args, chromePackage)
	}
	output, err := adb.shellOutput(ctx, args...)
	if err != nil {
		return err
	}
	if text := string(output); strings.Contains(text, "Error") || strings.Contains(text, "Exception") {
		return fmt.Errorf("adb am start: %s", strings.TrimSpace(text))
	}
	return nil
}

// Keyevent presses one key. The symbolic KEYCODE_* names are used rather than
// raw numbers so the argv reads as the operation it performs.
func (adb *Adb) Keyevent(ctx context.Context, code string) error {
	return adb.shell(ctx, "input", "keyevent", code)
}

// Swipe drags between two points over a duration; the same verb with zero
// travel is how a long press is expressed. specs/02-device-drivers.md line 43
// pins swipes to `adb shell input swipe`, not the gRPC agent.
func (adb *Adb) Swipe(ctx context.Context, startX, startY, endX, endY int, durationMillis int64) error {
	return adb.shell(ctx, "input", "swipe",
		strconv.Itoa(startX), strconv.Itoa(startY),
		strconv.Itoa(endX), strconv.Itoa(endY),
		strconv.FormatInt(durationMillis, 10))
}

// PutSetting writes one settings row; specs/02-device-drivers.md line 46 pins
// the proxy to `settings put global http_proxy` and orientation to
// `settings put system user_rotation`.
func (adb *Adb) PutSetting(ctx context.Context, namespace, key, value string) error {
	return adb.shell(ctx, "settings", "put", namespace, key, value)
}

// AirplaneModeEnabled reads the state from the connectivity service's own
// command, which reports exactly "enabled" or "disabled".
func (adb *Adb) AirplaneModeEnabled(ctx context.Context) (bool, error) {
	output, err := adb.shellOutput(ctx, "cmd", "connectivity", "airplane-mode")
	if err != nil {
		return false, err
	}
	switch state := strings.TrimSpace(string(output)); state {
	case "enabled":
		return true, nil
	case "disabled":
		return false, nil
	default:
		return false, fmt.Errorf("adb: airplane-mode reported %q, want enabled or disabled", state)
	}
}

func (adb *Adb) SetAirplaneMode(ctx context.Context, enabled bool) error {
	verb := "disable"
	if enabled {
		verb = "enable"
	}
	return adb.shell(ctx, "cmd", "connectivity", "airplane-mode", verb)
}

// KeyboardShown reads the input-method service state; mInputShown is the
// flag the IME flips while the soft keyboard is on screen.
func (adb *Adb) KeyboardShown(ctx context.Context) (bool, error) {
	output, err := adb.shellOutput(ctx, "dumpsys", "input_method")
	if err != nil {
		return false, err
	}
	return strings.Contains(string(output), "mInputShown=true"), nil
}

// APILevel reads Android's immutable SDK property without starting the agent
// or mutating device state. Runtime-gated operations use it before upload.
func (adb *Adb) APILevel(ctx context.Context) (int, error) {
	output, err := adb.shellOutput(ctx, "getprop", "ro.build.version.sdk")
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(output))
	level, err := strconv.Atoi(raw)
	if err != nil || level < 1 {
		return 0, fmt.Errorf("adb: ro.build.version.sdk reported %q, want a positive integer", raw)
	}
	return level, nil
}

// Install replaces an existing install (-r): the agent APKs are reinstalled
// per session, and a plain install fails on the second one.
func (adb *Adb) Install(ctx context.Context, apkPath string) error {
	return adb.run(ctx, "install", "-r", apkPath)
}

// Instrument starts the agent's gRPC server and blocks until the
// instrumentation exits — for a healthy agent that is the rest of the
// session, so callers run this on its own goroutine. The shell line is
// specs/02-device-drivers.md §2.2's, with FlowBaton's packages: -w keeps the
// process attached so its output reports a refusal, and -m is unconditional
// because the agent's minSdk is 26, the exact API level the spec gates -m on.
func (adb *Adb) Instrument(ctx context.Context, devicePort int) ([]byte, error) {
	return adb.shellOutput(ctx, "am", "instrument", "-w", "-m",
		"-e", "debug", "false",
		"-e", "class", agentServiceClass,
		"-e", "port", strconv.Itoa(devicePort),
		agentRunnerComponent)
}

func (adb *Adb) Uninstall(ctx context.Context, packageName string) error {
	return adb.run(ctx, "uninstall", packageName)
}

// Bugreport writes the device's diagnostic bundle to outputPath. adb chooses
// zip vs flat based on the device's own bugreport support; the path is passed
// through unchanged.
func (adb *Adb) Bugreport(ctx context.Context, outputPath string) error {
	return adb.run(ctx, "bugreport", outputPath)
}

// ProcessID returns one running process id for an application. The read-only
// pidof query scopes logcat without changing application state.
func (adb *Adb) ProcessID(ctx context.Context, appID string) (string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", fmt.Errorf("adb: process lookup requires an application id")
	}
	output, err := adb.shellOutput(ctx, "pidof", appID)
	if err != nil {
		return "", err
	}
	for _, field := range strings.Fields(string(output)) {
		pid, parseErr := strconv.Atoi(field)
		if parseErr == nil && pid > 0 {
			return strconv.Itoa(pid), nil
		}
	}
	return "", fmt.Errorf("adb: pidof %q reported no positive process id", appID)
}

// ListDevices returns every device adb can currently use. Entries in any
// other state (offline, unauthorized) are dropped: they cannot run a flow, so
// offering them as targets would only defer the failure.
func ListDevices(ctx context.Context, runner CommandRunner) ([]Device, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	output, err := runner.Run(ctx, adbExecutable(), "devices", "-l")
	if err != nil {
		return nil, fmt.Errorf("adb devices: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseDeviceList(string(output)), nil
}

// parseDeviceList reads `adb devices -l`: a header line, then one line per
// device — serial, state, and key:value descriptors.
func parseDeviceList(listing string) []Device {
	var devices []Device
	for _, line := range strings.Split(listing, "\n") {
		trimmed := strings.TrimSpace(line)
		// The server may prepend "* daemon started successfully *" noise.
		if trimmed == "" || trimmed == "List of devices attached" || strings.HasPrefix(trimmed, "*") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		entry := Device{Serial: fields[0], State: fields[1]}
		for _, field := range fields[2:] {
			if model, found := strings.CutPrefix(field, "model:"); found {
				entry.Model = model
			}
		}
		devices = append(devices, entry)
	}
	return devices
}

// parseRuntimePermissions collects the names under every "runtime
// permissions:" section of a package dump. The sections repeat per user, so
// names are deduplicated; they are sorted so map-free callers still see a
// stable order.
func parseRuntimePermissions(dump string) []string {
	seen := map[string]bool{}
	var names []string
	inSection := false
	sectionIndent := 0
	for _, line := range strings.Split(dump, "\n") {
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if trimmed == "runtime permissions:" {
			inSection = true
			sectionIndent = indent
			continue
		}
		if !inSection {
			continue
		}
		if trimmed == "" || indent <= sectionIndent {
			inSection = false
			continue
		}
		name, _, found := strings.Cut(trimmed, ":")
		if !found || !strings.Contains(trimmed, "granted=") {
			continue
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// shell runs one on-device command through `adb shell`.
func (adb *Adb) shell(ctx context.Context, args ...string) error {
	_, err := adb.shellOutput(ctx, args...)
	return err
}

func (adb *Adb) shellOutput(ctx context.Context, args ...string) ([]byte, error) {
	return adb.runOutput(ctx, append([]string{"shell"}, args...))
}

func (adb *Adb) run(ctx context.Context, args ...string) error {
	_, err := adb.runOutput(ctx, args)
	return err
}

// runOutput executes one adb invocation, always device-scoped with -s: a
// multi-device host would otherwise make adb pick the target itself. adb
// reports why it refused on its own output, so that output is carried into
// the error rather than discarded in favor of a bare exit status.
func (adb *Adb) runOutput(ctx context.Context, args []string) ([]byte, error) {
	if strings.TrimSpace(adb.serial) == "" {
		return nil, fmt.Errorf("adb: %s requires a device serial", args[0])
	}
	argv := append([]string{"-s", adb.serial}, args...)
	output, err := adb.runner.Run(ctx, adbExecutable(), argv...)
	if err != nil {
		return nil, fmt.Errorf("adb %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
