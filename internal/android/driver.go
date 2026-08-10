package android

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/larchwave/flowbaton/internal/android/grpcwire"
	"github.com/larchwave/flowbaton/internal/android/pbwire"
	"github.com/larchwave/flowbaton/internal/device"
)

// Driver composes the two halves of the Android host side into the frozen
// device.Driver, mirroring how internal/ios composed its runner and simctl.
//
// The split rule: the gRPC agent owns what happens ON screen or in the app's
// process — hierarchy, screenshot, tap, typing, mock location, media — and
// adb owns what happens AROUND the app — lifecycle, permissions, settings,
// key events, the port forward. Where both could serve, the half that can
// carry the FULL request wins: launchApp goes to the agent because only its
// typed extras survive the trip, while swipes go to `input swipe` because
// specs/02-device-drivers.md line 43 pins them there.

// Platform is the device platform this driver reports.
const Platform = device.Platform("android")

// agentPort is where the agent's gRPC server listens ON THE DEVICE
// (specs/04-wire-protocols.md §1, default 7001). The host reaches it through
// `adb forward tcp:<hostPort> tcp:7001`, owned by Open and undone by Close.
const agentPort = 7001

const (
	// startupTimeoutEnv overrides the agent startup wait, in milliseconds —
	// the same knob on both platforms (specs/04-wire-protocols.md line 93).
	startupTimeoutEnv = "FLOWBATON_DRIVER_STARTUP_TIMEOUT"
	// agentStartupPoll and agentStartupTimeout pace Open's reachability wait
	// when it owns the agent lifecycle (specs/02-device-drivers.md §2.2:
	// poll 100ms, startup timeout 15000ms).
	agentStartupPoll    = 100 * time.Millisecond
	agentStartupTimeout = 15000 * time.Millisecond
)

const (
	// defaultSwipeMillis keeps gesture timing uniform across device drivers.
	defaultSwipeMillis = 500
	// defaultLongPressMillis is specs/02-device-drivers.md line 60's long
	// press default; `input swipe` with zero duration would be a plain tap,
	// which is the one thing a long press must not degrade into.
	defaultLongPressMillis = 3000
	// swipeGridEdge is where a directional swipe ends, as a fraction of the
	// screen: specs/02-device-drivers.md line 43, "UP: center → 10% height".
	swipeGridEdge = 0.1
	// mediaChunkBytes keeps every streamed addMedia frame far under gRPC's
	// conventional 4 MiB message ceiling.
	mediaChunkBytes = 64 * 1024
)

// AgentAPKs names the two installable halves of the on-device agent: the app
// APK and the instrumentation APK hosting the gRPC server. Handing them to
// NewDriver makes Open own the whole spec 02 §2.2 lifecycle — reinstall,
// `am instrument`, and the 100ms reachability poll; a nil AgentAPKs keeps
// the operator-started mode, where the agent must already be running.
type AgentAPKs struct {
	App  string
	Test string
}

// Driver drives one device or emulator.
type Driver struct {
	serial string
	// hostPort is the host side of the adb forward; it is what Name reports,
	// because a sharded run needs the number in failure messages.
	hostPort int
	adb      *Adb
	apks     *AgentAPKs

	// devtools is the attached WebView page when the flow asked for the
	// Chrome DevTools hierarchy; devtoolsPort is the host side of its forward.
	devtools     webViewSource
	devtoolsPort int

	// devtoolsNotice is where "you asked for devtools and there is none" goes.
	// A field so a test can read it; nil means stderr. The message exists
	// because degrading silently is how an operator concludes the merge is
	// happening when it is not.
	devtoolsNotice io.Writer

	// startupPoll and startupTimeout pace Open's reachability wait; fields so
	// tests can compress them. The env override is read at Open time, so the
	// timeout the operator set is the timeout the error message reports.
	startupPoll    time.Duration
	startupTimeout time.Duration
	cleanupTimeout time.Duration

	// stopInstrumentation kills the `am instrument -w` child Open started;
	// nil when the operator owns the agent. Open sets it, Close calls it.
	stopInstrumentation context.CancelFunc

	// connMu guards conn, which Open sets and Close clears.
	connMu sync.Mutex
	conn   *grpcwire.Conn

	// recMu guards recordings, the screen recordings in flight keyed by the
	// CaptureID StartScreenRecording handed out.
	recMu      sync.Mutex
	recordings map[device.CaptureID]androidRecording
	// spawnRecorder starts the long-lived `adb shell screenrecord <devicePath>`
	// child. It is a field so a test drives the start/stop lifecycle without a
	// device; nil means spawn for real.
	spawnRecorder func(ctx context.Context, devicePath string) (screenRecorder, error)

	// diagnosticMu guards the capture sequence and active logcat children.
	diagnosticMu       sync.Mutex
	diagnosticSequence uint64
	deviceLogs         map[device.CaptureID]androidDeviceLog
	spawnDeviceLog     func(context.Context, *os.File, []string) (deviceLogProcess, error)
}

// screenRecorder is a running screenrecord child the driver can stop. stop
// interrupts screenrecord so it finalizes the on-device mp4, then pulls that
// file to sinkPath and removes the device copy.
type screenRecorder interface {
	stop(ctx context.Context, sinkPath string) error
}

// androidRecording pairs a running recorder with the host sink it lands at.
type androidRecording struct {
	recorder screenRecorder
	sinkPath string
}

// NewDriver binds a device serial and a host port to an adb runner. A nil
// runner executes adb for real; a nil apks means the agent is already
// running and Open only forwards and probes.
func NewDriver(serial string, hostPort int, runner CommandRunner, apks *AgentAPKs) *Driver {
	return &Driver{
		serial:         serial,
		hostPort:       hostPort,
		adb:            NewAdb(serial, runner),
		apks:           apks,
		startupPoll:    agentStartupPoll,
		startupTimeout: agentStartupTimeout,
		cleanupTimeout: recordingFinalizeTimeout,
	}
}

// Name identifies the agent this driver talks to, port included, like the
// iOS driver's ios:UDID:PORT.
func (driver *Driver) Name() string {
	return fmt.Sprintf("android:%s:%d", driver.serial, driver.hostPort)
}

func (driver *Driver) agentAddress() string {
	return "127.0.0.1:" + strconv.Itoa(driver.hostPort)
}

// Open publishes the agent port on the host and confirms the agent answers.
// With APKs it owns the whole spec 02 §2.2 lifecycle: reinstall both halves,
// start the instrumentation, and poll for reachability. Without them the
// instrumentation must already be RUNNING for any gRPC call to answer;
// discovering an absent agent here, with the address in the error, is the
// difference between a setup failure and a flow failure.
func (driver *Driver) Open(ctx context.Context) error {
	if driver.apks != nil {
		return driver.openManagedAgent(ctx)
	}
	if err := driver.adb.ForwardAdd(ctx, driver.hostPort, agentPort); err != nil {
		return err
	}
	conn, err := grpcwire.Dial(driver.agentAddress())
	if err != nil {
		_ = driver.adb.ForwardRemove(ctx, driver.hostPort)
		return err
	}
	driver.setConn(conn)
	if _, err := driver.agentDeviceInfo(ctx); err != nil {
		_ = conn.Close()
		driver.setConn(nil)
		_ = driver.adb.ForwardRemove(context.WithoutCancel(ctx), driver.hostPort)
		// The session already prefixes "opening <name>:", so this names only
		// what the caller cannot know — the address behind the forward.
		return fmt.Errorf(
			"the agent for %s at %s is not answering (is the instrumentation running on the device?): %w",
			driver.serial, driver.agentAddress(), err)
	}
	return nil
}

// instrumentExit is how the backgrounded `am instrument -w` ended.
type instrumentExit struct {
	output []byte
	err    error
}

// refused reports whether the instrumentation refused to start rather than
// merely exiting: spec 02 §2.2's started-cleanly check is output free of
// FAILED/UNABLE markers. CombinedOutput cannot separate stderr, so the
// markers are searched in everything the process said.
func (exit instrumentExit) refused() bool {
	if exit.err != nil {
		return true
	}
	output := strings.ToUpper(string(exit.output))
	return strings.Contains(output, "FAILED") || strings.Contains(output, "UNABLE")
}

func (exit instrumentExit) describe() string {
	if exit.err != nil {
		return exit.err.Error()
	}
	return strings.TrimSpace(string(exit.output))
}

// openManagedAgent is spec 02 §2.2's open(): uninstall-first reinstall of
// both APKs (so a stale agent from an earlier run cannot answer for this
// one), `am instrument` on its own goroutine because -w blocks for the
// agent's whole life, then the 100ms reachability poll.
func (driver *Driver) openManagedAgent(ctx context.Context) error {
	timeout, err := driver.agentStartupBudget()
	if err != nil {
		return err
	}
	state := managedOpenState{}
	fail := func(openErr error) error {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), driver.cleanupTimeout)
		defer cancel()
		rollbackErr := driver.rollbackManagedOpen(cleanupCtx, state)
		if rollbackErr != nil {
			rollbackErr = fmt.Errorf("rolling back managed agent open: %w", rollbackErr)
		}
		return errors.Join(openErr, rollbackErr)
	}
	// The uninstalls are expected to fail on a clean device; the installs
	// are not allowed to.
	_ = driver.adb.Uninstall(ctx, AgentAppPackage)
	_ = driver.adb.Uninstall(ctx, AgentTestPackage)
	if err := driver.adb.Install(ctx, driver.apks.App); err != nil {
		return fail(err)
	}
	if err := driver.adb.Install(ctx, driver.apks.Test); err != nil {
		return fail(err)
	}
	// setLocation goes through the agent, and the platform refuses a mock
	// location from an app that has not been allowed the app-op. Granted here
	// rather than lazily in setLocation: it belongs to installing this agent,
	// and doing it once beats doing it on every call.
	if err := driver.adb.AllowMockLocation(ctx, AgentAppPackage); err != nil {
		return fail(err)
	}
	state.forwardAttempted = true
	if err := driver.adb.ForwardAdd(ctx, driver.hostPort, agentPort); err != nil {
		return fail(err)
	}

	// The instrumentation outlives Open, so its context detaches from the
	// caller's; Close (or a failed open) is what cancels it.
	instrumentCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	driver.stopInstrumentation = cancel
	exited := make(chan instrumentExit, 1)
	done := make(chan struct{})
	state.instrumentCancel = cancel
	state.instrumentDone = done
	go func() {
		defer close(done)
		output, err := driver.adb.Instrument(instrumentCtx, agentPort)
		exited <- instrumentExit{output: output, err: err}
	}()

	if err := driver.awaitAgent(ctx, timeout, exited); err != nil {
		return fail(err)
	}
	return nil
}

type managedOpenState struct {
	forwardAttempted bool
	instrumentCancel context.CancelFunc
	instrumentDone   <-chan struct{}
}

func (driver *Driver) rollbackManagedOpen(ctx context.Context, state managedOpenState) error {
	var cleanupErrs []error
	if state.instrumentCancel != nil {
		state.instrumentCancel()
		driver.stopInstrumentation = nil
	}
	if conn := driver.takeConn(); conn != nil {
		cleanupErrs = append(cleanupErrs, conn.Close())
	}
	if state.forwardAttempted {
		if err := driver.adb.ForwardRemove(ctx, driver.hostPort); err != nil {
			cleanupErrs = append(cleanupErrs,
				fmt.Errorf("removing managed agent forward: %w", err))
		}
	}
	if err := driver.adb.Uninstall(ctx, AgentAppPackage); err != nil {
		cleanupErrs = append(cleanupErrs,
			fmt.Errorf("uninstalling managed agent app: %w", err))
	}
	if err := driver.adb.Uninstall(ctx, AgentTestPackage); err != nil {
		cleanupErrs = append(cleanupErrs,
			fmt.Errorf("uninstalling managed agent test package: %w", err))
	}
	if state.instrumentDone != nil {
		select {
		case <-state.instrumentDone:
		case <-ctx.Done():
			cleanupErrs = append(cleanupErrs,
				fmt.Errorf("stopping managed agent instrumentation: %w", ctx.Err()))
		}
	}
	return errors.Join(cleanupErrs...)
}

// agentStartupBudget is the reachability wait: 15000ms unless the
// milliseconds env override says otherwise (specs/02 §2.2, specs/04 line 93).
func (driver *Driver) agentStartupBudget() (time.Duration, error) {
	raw := os.Getenv(startupTimeoutEnv)
	if raw == "" {
		return driver.startupTimeout, nil
	}
	millis, err := strconv.Atoi(raw)
	if err != nil || millis <= 0 {
		return 0, fmt.Errorf(
			"%s must be a positive millisecond count, not %q", startupTimeoutEnv, raw)
	}
	return time.Duration(millis) * time.Millisecond, nil
}

// awaitAgent redials and probes until the agent answers, the budget runs
// out, or the instrumentation reports it never started. An instrumentation
// that exits CLEANLY before the agent answered only stops the wait at the
// deadline: its output carried no refusal, so the poll is the judge.
func (driver *Driver) awaitAgent(
	ctx context.Context,
	timeout time.Duration,
	exited <-chan instrumentExit,
) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := grpcwire.Dial(driver.agentAddress())
		if err == nil {
			driver.setConn(conn)
			_, probeErr := driver.agentDeviceInfo(ctx)
			if probeErr == nil {
				return nil
			}
			lastErr = probeErr
			_ = conn.Close()
			driver.setConn(nil)
		} else {
			lastErr = err
		}

		select {
		case exit := <-exited:
			if exit.refused() {
				if exit.err != nil {
					return fmt.Errorf(
						"the agent instrumentation on %s did not start cleanly: %w",
						driver.serial, exit.err)
				}
				return fmt.Errorf(
					"the agent instrumentation on %s did not start cleanly: %s",
					driver.serial, exit.describe())
			}
			// Receiving from a nil channel blocks forever, which is exactly
			// what a cleanly exited instrumentation should do here.
			exited = nil
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(driver.startupPoll):
		}

		if time.Now().After(deadline) {
			return fmt.Errorf(
				"the agent for %s at %s did not answer within %v (%s overrides the wait, in milliseconds): %w",
				driver.serial, driver.agentAddress(), timeout, startupTimeoutEnv, lastErr)
		}
	}
}

// Close undoes what Open did: the connection, the instrumentation and the
// APKs when Open owned them, and the forward. An operator-started agent is
// left exactly as it was, for the same reason the iOS driver leaves the
// simulator running — the state after a failed run is something an operator
// inspects.
func (driver *Driver) Close(ctx context.Context) error {
	var cleanupErrs []error
	cleanupErrs = append(cleanupErrs, driver.drainDeviceLogs(context.WithoutCancel(ctx))...)
	cleanupErrs = append(cleanupErrs, driver.drainRecordings(context.WithoutCancel(ctx))...)
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordingFinalizeTimeout)
	defer cancel()
	if conn := driver.takeConn(); conn != nil {
		cleanupErrs = append(cleanupErrs, conn.Close())
	}
	// A run that ends mid-flow with the devtools hierarchy on is the ordinary
	// crash case, and its forward would outlive the run.
	cleanupErrs = append(cleanupErrs, driver.disableDevTools(cleanupCtx))
	if driver.stopInstrumentation != nil {
		driver.stopInstrumentation()
		driver.stopInstrumentation = nil
	}
	cleanupErrs = append(cleanupErrs, driver.adb.ForwardRemove(cleanupCtx, driver.hostPort))
	if driver.apks != nil {
		// The uninstall-on-close half of spec 02 §2.2's reinstallDriver.
		cleanupErrs = append(cleanupErrs,
			driver.adb.Uninstall(cleanupCtx, AgentAppPackage),
			driver.adb.Uninstall(cleanupCtx, AgentTestPackage),
		)
	}
	return errors.Join(cleanupErrs...)
}

func (driver *Driver) drainRecordings(ctx context.Context) []error {
	driver.recMu.Lock()
	ids := make([]device.CaptureID, 0, len(driver.recordings))
	for id := range driver.recordings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	recordings := driver.recordings
	driver.recordings = nil
	driver.recMu.Unlock()

	errs := make([]error, 0, len(ids))
	for _, id := range ids {
		recording := recordings[id]
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordingFinalizeTimeout)
		err := recording.recorder.stop(cleanupCtx, recording.sinkPath)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("stopping android screen recording %q: %w", id, err))
		}
	}
	return errs
}

func (driver *Driver) setConn(conn *grpcwire.Conn) {
	driver.connMu.Lock()
	defer driver.connMu.Unlock()
	driver.conn = conn
}

func (driver *Driver) takeConn() *grpcwire.Conn {
	driver.connMu.Lock()
	defer driver.connMu.Unlock()
	conn := driver.conn
	driver.conn = nil
	return conn
}

func (driver *Driver) connection() (*grpcwire.Conn, error) {
	driver.connMu.Lock()
	defer driver.connMu.Unlock()
	if driver.conn == nil {
		return nil, fmt.Errorf("android driver %s is not open", driver.Name())
	}
	return driver.conn, nil
}

// wireMessage is what every pbwire request satisfies.
type wireMessage interface{ Marshal() []byte }

func (driver *Driver) invoke(ctx context.Context, method string, request wireMessage) ([]byte, error) {
	conn, err := driver.connection()
	if err != nil {
		return nil, err
	}
	return conn.Invoke(ctx, method, request.Marshal())
}

// invokeEmpty performs a unary call whose reply carries no fields. Every
// empty *Response decodes identically (skip unknown fields, refuse malformed
// bytes), so one decoder validates them all.
func (driver *Driver) invokeEmpty(ctx context.Context, method string, request wireMessage) error {
	reply, err := driver.invoke(ctx, method, request)
	if err != nil {
		return err
	}
	var empty pbwire.EmptyResponse
	if err := empty.Unmarshal(reply); err != nil {
		return fmt.Errorf("decoding %s reply: %w", method, err)
	}
	return nil
}

func (driver *Driver) agentDeviceInfo(ctx context.Context) (pbwire.DeviceInfo, error) {
	reply, err := driver.invoke(ctx, pbwire.MethodDeviceInfo, pbwire.DeviceInfoRequest{})
	if err != nil {
		return pbwire.DeviceInfo{}, err
	}
	var info pbwire.DeviceInfo
	if err := info.Unmarshal(reply); err != nil {
		return pbwire.DeviceInfo{}, fmt.Errorf("decoding deviceInfo reply: %w", err)
	}
	return info, nil
}

func (driver *Driver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	info, err := driver.agentDeviceInfo(ctx)
	if err != nil {
		return device.DeviceInfo{}, err
	}
	// specs/02-device-drivers.md line 28: the Android grid IS pixels. The
	// agent reports real display pixels and every coordinate the engine
	// computes on the grid lands on the same numbers.
	return device.DeviceInfo{
		Platform:     Platform,
		WidthPixels:  int(info.WidthPixels),
		HeightPixels: int(info.HeightPixels),
		WidthGrid:    int(info.WidthPixels),
		HeightGrid:   int(info.HeightPixels),
	}, nil
}

// launchArgumentTypes maps the engine's argument vocabulary
// (specs/06-launch-app-semantics.md §5) onto the Java class FQNs the frozen
// proto's ArgumentValue.type carries (specs/04-wire-protocols.md §1) — the
// agent uses them to pick the putExtra coercion.
var launchArgumentTypes = map[string]string{
	"string":  "java.lang.String",
	"boolean": "java.lang.Boolean",
	"integer": "java.lang.Integer",
	"double":  "java.lang.Double",
}

// LaunchApp goes through the agent, not adb: only the gRPC route carries the
// typed extras, and `am start` would need the launch activity resolved by
// hand. An unknown argument type is refused before anything crosses the wire.
func (driver *Driver) LaunchApp(ctx context.Context, request device.LaunchAppRequest) error {
	arguments := make([]pbwire.ArgumentValue, 0, len(request.Arguments))
	for _, argument := range request.Arguments {
		typeName, known := launchArgumentTypes[argument.Type]
		if !known {
			return fmt.Errorf(
				"launchApp argument %q has type %q, not one of string, boolean, integer, double",
				argument.Key, argument.Type)
		}
		arguments = append(arguments, pbwire.ArgumentValue{
			Key: argument.Key, Value: argument.Value, Type: typeName,
		})
	}
	return driver.invokeEmpty(ctx, pbwire.MethodLaunchApp, pbwire.LaunchAppRequest{
		PackageName: request.AppID, Arguments: arguments,
	})
}

func (driver *Driver) StopApp(ctx context.Context, request device.AppRequest) error {
	return driver.adb.ForceStop(ctx, request.AppID)
}

// KillApp is the system-initiated process death, distinct from StopApp on
// this platform: the public docs pin it to `adb shell am kill`.
func (driver *Driver) KillApp(ctx context.Context, request device.AppRequest) error {
	return driver.adb.Kill(ctx, request.AppID)
}

// ClearAppState is pm clear: data and cache wiped, app still installed —
// unlike iOS, Android has the per-app verb, so no uninstall is needed.
func (driver *Driver) ClearAppState(ctx context.Context, request device.AppRequest) error {
	return driver.adb.ClearPackageData(ctx, request.AppID)
}

// ClearKeychain is a documented no-op: the public docs state the command
// "only applies to iOS and has no effect on Android or Web". There is no
// keychain here to clear, so the success is vacuous rather than a lie.
func (driver *Driver) ClearKeychain(context.Context) error {
	return nil
}

func (driver *Driver) Tap(ctx context.Context, request device.TapRequest) error {
	x, err := gridCoordinate("tap x", request.Point.X)
	if err != nil {
		return err
	}
	y, err := gridCoordinate("tap y", request.Point.Y)
	if err != nil {
		return err
	}
	return driver.invokeEmpty(ctx, pbwire.MethodTap, pbwire.TapRequest{X: x, Y: y})
}

// gridCoordinate rounds a grid position onto the proto's uint32. A negative
// coordinate is refused: casting would wrap it onto the far side of the
// screen, and tapping somewhere nobody asked for while reporting success is
// worse than an error naming the number.
func gridCoordinate(label string, value float64) (uint32, error) {
	rounded := math.Round(value)
	if rounded < 0 || rounded > math.MaxUint32 {
		return 0, fmt.Errorf("%s %v is outside the device grid", label, value)
	}
	return uint32(rounded), nil
}

// LongPress is a zero-travel `input swipe`: the frozen proto has no long
// press rpc, and holding one point for the duration is exactly what the
// swipe verb does when start and end coincide.
func (driver *Driver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	duration := request.DurationMillis
	if duration <= 0 {
		duration = defaultLongPressMillis
	}
	x := int(math.Round(request.Point.X))
	y := int(math.Round(request.Point.Y))
	return driver.adb.Swipe(ctx, x, y, x, y, duration)
}

// keyEvents maps the engine's canonical key codes onto the platform's
// KEYCODE_* names. LOCK is KEYCODE_SLEEP rather than KEYCODE_POWER: SLEEP
// only turns the screen off, while POWER toggles and would wake a device
// that was already locked.
var keyEvents = map[device.KeyCode]string{
	"ENTER":       "KEYCODE_ENTER",
	"BACK":        "KEYCODE_BACK",
	"HOME":        "KEYCODE_HOME",
	"LOCK":        "KEYCODE_SLEEP",
	"VOLUME_UP":   "KEYCODE_VOLUME_UP",
	"VOLUME_DOWN": "KEYCODE_VOLUME_DOWN",
	"POWER":       "KEYCODE_POWER",
}

func (driver *Driver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	code, known := keyEvents[device.KeyCode(strings.ToUpper(string(request.Code)))]
	if !known {
		return fmt.Errorf("%w: android has no key %q", device.ErrUnsupported, request.Code)
	}
	return driver.adb.Keyevent(ctx, code)
}

// ContentDescriptor asks the agent for the spec 04 §2 XML and converts it.
// The rpc takes no parameters: the agent dumps the whole screen, so the app
// filter and the keyboard exclusion have nothing to carry here.
func (driver *Driver) ContentDescriptor(
	ctx context.Context,
	_ device.ContentDescriptorRequest,
) (device.TreeNode, error) {
	hierarchy, err := driver.agentHierarchy(ctx)
	if err != nil {
		return device.TreeNode{}, err
	}
	return driver.webViewHierarchy(ctx, hierarchy)
}

func (driver *Driver) agentHierarchy(ctx context.Context) (device.TreeNode, error) {
	reply, err := driver.invoke(ctx, pbwire.MethodViewHierarchy, pbwire.ViewHierarchyRequest{})
	if err != nil {
		return device.TreeNode{}, err
	}
	var response pbwire.ViewHierarchyResponse
	if err := response.Unmarshal(reply); err != nil {
		return device.TreeNode{}, fmt.Errorf("decoding viewHierarchy reply: %w", err)
	}
	return parseHierarchy(response.Hierarchy)
}

// ScrollVertical is a swipe across the middle of the screen, sized from the
// device's own geometry — the same rule as the iOS driver, on the pixel grid.
func (driver *Driver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	info, err := driver.agentDeviceInfo(ctx)
	if err != nil {
		return err
	}
	amount := request.Amount
	if amount <= 0 {
		amount = 0.5
	}
	centerX := float64(info.WidthPixels) / 2
	centerY := float64(info.HeightPixels) / 2
	if request.ElementPoint != nil {
		centerX, centerY = request.ElementPoint.X, request.ElementPoint.Y
	}
	travel := float64(info.HeightPixels) * amount / 2
	// Scrolling down means dragging content up.
	if strings.EqualFold(string(request.Direction), "down") {
		travel = -travel
	}
	return driver.adb.Swipe(ctx,
		int(math.Round(centerX)), int(math.Round(centerY-travel)),
		int(math.Round(centerX)), int(math.Round(centerY+travel)),
		defaultSwipeMillis)
}

func (driver *Driver) IsKeyboardVisible(ctx context.Context, _ device.KeyboardRequest) (bool, error) {
	return driver.adb.KeyboardShown(ctx)
}

// Swipe covers the three shapes specs/02-device-drivers.md §1 gives the
// driver, all through `input swipe` (specs/02 line 43): explicit points, a
// bare direction, and an element point plus a direction.
func (driver *Driver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	duration := request.DurationMillis
	if duration <= 0 {
		duration = defaultSwipeMillis
	}
	if request.Start != nil && request.End != nil {
		return driver.adb.Swipe(ctx,
			int(math.Round(request.Start.X)), int(math.Round(request.Start.Y)),
			int(math.Round(request.End.X)), int(math.Round(request.End.Y)),
			duration)
	}
	if request.Direction == "" {
		return fmt.Errorf(
			"%w: a swipe needs either both points or a direction", device.ErrUnsupported)
	}
	info, err := driver.agentDeviceInfo(ctx)
	if err != nil {
		return err
	}
	width, height := float64(info.WidthPixels), float64(info.HeightPixels)
	start := device.Point{X: width / 2, Y: height / 2}
	if request.ElementPoint != nil {
		start = *request.ElementPoint
	}
	end, err := swipeGridEnd(request.Direction, start, width, height)
	if err != nil {
		return err
	}
	return driver.adb.Swipe(ctx,
		int(math.Round(start.X)), int(math.Round(start.Y)),
		int(math.Round(end.X)), int(math.Round(end.Y)),
		duration)
}

// swipeGridEnd is where a swipe in this direction ends; the cross axis keeps
// the start's coordinate so a swipe from an element travels straight. Device
// drivers share this fraction to keep gesture distance uniform.
func swipeGridEnd(
	direction device.Direction,
	start device.Point,
	width, height float64,
) (device.Point, error) {
	switch strings.ToUpper(string(direction)) {
	case "UP":
		return device.Point{X: start.X, Y: height * swipeGridEdge}, nil
	case "DOWN":
		return device.Point{X: start.X, Y: height * (1 - swipeGridEdge)}, nil
	case "LEFT":
		return device.Point{X: width * swipeGridEdge, Y: start.Y}, nil
	case "RIGHT":
		return device.Point{X: width * (1 - swipeGridEdge), Y: start.Y}, nil
	}
	return device.Point{}, fmt.Errorf(
		"%w: swipe direction %q is not one of UP, DOWN, LEFT, RIGHT",
		device.ErrUnsupported, direction)
}

func (driver *Driver) BackPress(ctx context.Context) error {
	return driver.adb.Keyevent(ctx, "KEYCODE_BACK")
}

// InputText goes to the agent because shell `input text` mangles unicode and
// spaces. The agent commits text in-process to the focused field. AppIDs has
// nothing to carry: input always targets whatever holds focus.
func (driver *Driver) InputText(ctx context.Context, request device.InputTextRequest) error {
	return driver.invokeEmpty(ctx, pbwire.MethodInputText, pbwire.InputTextRequest{
		Text: request.Text,
	})
}

// OpenLink fires the VIEW intent. The only browser choice Android honors is
// the boolean chrome force (openBrowser); any other named browser is refused
// rather than silently opened in the default.
func (driver *Driver) OpenLink(ctx context.Context, request device.OpenLinkRequest) error {
	forceChrome := false
	switch {
	case request.Browser == "":
	case strings.EqualFold(string(request.Browser), "chrome"):
		forceChrome = true
	default:
		return fmt.Errorf(
			"%w: android can only force chrome, not %q", device.ErrUnsupported, request.Browser)
	}
	return driver.adb.OpenLink(ctx, request.Link, forceChrome)
}

// HideKeyboard is the back key: the public docs record that Android hides
// the keyboard with "a back button event, ... identical to the back command".
func (driver *Driver) HideKeyboard(ctx context.Context) error {
	return driver.adb.Keyevent(ctx, "KEYCODE_BACK")
}

// TakeScreenshot returns the agent's PNG. The compressed flag is iOS's
// JPEG-vs-PNG switch; the Android agent produces PNG either way (spec 04 §1),
// so there is nothing here for the flag to change.
func (driver *Driver) TakeScreenshot(
	ctx context.Context,
	_ device.ScreenshotRequest,
) ([]byte, error) {
	reply, err := driver.invoke(ctx, pbwire.MethodScreenshot, pbwire.ScreenshotRequest{})
	if err != nil {
		return nil, err
	}
	var response pbwire.ScreenshotResponse
	if err := response.Unmarshal(reply); err != nil {
		return nil, fmt.Errorf("decoding screenshot reply: %w", err)
	}
	return response.Bytes, nil
}

// StartScreenRecording spawns `adb shell screenrecord <devicePath>` as a
// long-lived child (specs/02-device-drivers.md line 9) and returns a CaptureID
// that StopScreenRecording later stops. screenrecord can only write to device
// storage, so it records to /sdcard/<name> and stop pulls the finished file to
// the host sink.
func (driver *Driver) StartScreenRecording(
	ctx context.Context,
	request device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	sink := strings.TrimSpace(request.OutputPath)
	base, err := device.ValidateRecordingSink(sink)
	if err != nil {
		return "", err
	}
	devicePath := "/sdcard/" + base
	id := device.CaptureID(sink)
	driver.recMu.Lock()
	defer driver.recMu.Unlock()
	if _, exists := driver.recordings[id]; exists {
		return "", fmt.Errorf("an android screen recording is already running for %q", id)
	}
	spawn := driver.spawnRecorder
	if spawn == nil {
		spawn = driver.realSpawnRecorder
	}
	recorder, err := spawn(ctx, devicePath)
	if err != nil {
		return "", fmt.Errorf("starting android screen recording: %w", err)
	}
	if driver.recordings == nil {
		driver.recordings = map[device.CaptureID]androidRecording{}
	}
	driver.recordings[id] = androidRecording{recorder: recorder, sinkPath: sink}
	return id, nil
}

// StopScreenRecording ends a recording StartScreenRecording began, pulls the
// artifact to its sink, and returns it. Like the iOS counterpart it is NOT on
// the frozen Driver surface — v0 declares only the start half — so the
// recording controller completes the lifecycle by calling this directly.
func (driver *Driver) StopScreenRecording(
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.recMu.Lock()
	recording, ok := driver.recordings[id]
	delete(driver.recordings, id)
	driver.recMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no android screen recording is running for %q", id)
	}
	if err := recording.recorder.stop(ctx, recording.sinkPath); err != nil {
		return nil, fmt.Errorf("stopping android screen recording: %w", err)
	}
	return []device.Artifact{{Kind: "recording", Path: recording.sinkPath}}, nil
}

// realSpawnRecorder starts screenrecord on the device. It uses exec.Command,
// NOT CommandContext: cancelling the context would SIGKILL adb and leave a
// truncated mp4, whereas stop() interrupts screenrecord so it finalizes the
// file before it is pulled.
func (driver *Driver) realSpawnRecorder(_ context.Context, devicePath string) (screenRecorder, error) {
	cmd := exec.Command(adbExecutable(), "-s", driver.serial, "shell", "screenrecord", devicePath)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &adbRecorder{
		cmd: cmd, child: newAndroidLocalChild(cmd), serial: driver.serial, devicePath: devicePath,
	}, nil
}

// recordingFinalizeTimeout bounds the wait for the local `adb shell` child to
// exit after the device-side interrupt. It exits as soon as screenrecord does;
// the bound only stops a device that never answers from hanging the flow.
const recordingFinalizeTimeout = 15 * time.Second

// adbRecorder is a real screenrecord child plus the adb identity needed to
// retrieve and clean up its on-device output.
type adbRecorder struct {
	cmd        *exec.Cmd
	child      *androidLocalChild
	serial     string
	devicePath string
	// run executes one adb subcommand. It is a field so the stop sequence is
	// testable without a device; nil runs adb for real.
	run func(ctx context.Context, args ...string) ([]byte, error)
}

type androidLocalChild struct {
	done   <-chan error
	signal func(os.Signal) error
	kill   func() error
}

func newAndroidLocalChild(cmd *exec.Cmd) *androidLocalChild {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &androidLocalChild{
		done:   done,
		signal: cmd.Process.Signal,
		kill:   cmd.Process.Kill,
	}
}

const androidLocalChildReapTimeout = time.Second

func waitForAndroidLocalChild(ctx context.Context, child *androidLocalChild) error {
	if child == nil {
		return nil
	}
	select {
	case <-child.done:
		return nil
	case <-ctx.Done():
		killErr := child.kill()
		select {
		case <-child.done:
			return errors.Join(ctx.Err(), killErr)
		case <-time.After(androidLocalChildReapTimeout):
			return errors.Join(ctx.Err(), killErr, errors.New("timed out reaping killed adb recorder child"))
		}
	}
}

func (r *adbRecorder) adb(ctx context.Context, args ...string) ([]byte, error) {
	if r.run != nil {
		return r.run(ctx, args...)
	}
	return exec.CommandContext(
		ctx, adbExecutable(), append([]string{"-s", r.serial}, args...)...).CombinedOutput()
}

// stop finalizes the recording on the device, then pulls it.
//
// The interrupt has to reach screenrecord ON THE DEVICE. Signalling the local
// `adb shell` child does not reliably reach it, and screenrecord writes the mp4
// index last, so a pull that follows only the local exit copies an unfinished
// file. The device-side interrupt lets screenrecord finalize the mp4 before it
// is pulled to the host.
//
// ponytail: pkill matches by name, so it finalizes every screenrecord on that
// device. One capture per driver and one driver per serial makes that the same
// process today; if concurrent recordings on one device ever appear, match the
// pid screenrecord was started with instead.
func (r *adbRecorder) stop(ctx context.Context, sinkPath string) error {
	var stopErrs []error
	stopContext, cancelStop := context.WithTimeout(ctx, recordingFinalizeTimeout)
	defer cancelStop()
	interrupted := true
	if _, err := r.adb(stopContext, "shell", "pkill", "-INT", "screenrecord"); err != nil {
		// A device without pkill still has the local child: worse output than a
		// device-side interrupt, but better than no recording at all.
		interrupted = false
		if r.child != nil && r.child.signal != nil {
			_ = r.child.signal(os.Interrupt)
		} else if r.cmd != nil && r.cmd.Process != nil {
			_ = r.cmd.Process.Signal(os.Interrupt)
		}
	}
	// The local child exits when screenrecord does, which is the signal that the
	// on-device file is closed. screenrecord exits non-zero when interrupted but
	// has already flushed, so the Wait error is expected and not a failure.
	if interrupted && r.child == nil && r.cmd != nil && r.cmd.Process != nil {
		// A successful device-side interrupt is the graceful path. The local
		// child is only signalled directly when the device command was unavailable.
		r.child = newAndroidLocalChild(r.cmd)
	}
	if err := waitForAndroidLocalChild(stopContext, r.child); err != nil {
		stopErrs = append(stopErrs, fmt.Errorf("waiting for screenrecord child: %w", err))
	}
	if out, err := r.adb(stopContext, "pull", r.devicePath, sinkPath); err != nil {
		stopErrs = append(stopErrs, fmt.Errorf(
			"adb pull: %w: %s", err, strings.TrimSpace(string(out))))
		if removeErr := os.Remove(sinkPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			stopErrs = append(stopErrs, fmt.Errorf("removing partial recording %q: %w", sinkPath, removeErr))
		}
	}
	if out, err := r.adb(stopContext, "shell", "rm", "-f", r.devicePath); err != nil {
		stopErrs = append(stopErrs, fmt.Errorf(
			"removing device recording: %w: %s", err, strings.TrimSpace(string(out))))
	}
	return errors.Join(stopErrs...)
}

// SetLocation enables the agent's mock providers first, then pushes the
// coordinate: setLocation lands on the mock Fused/LocationManager providers,
// which do not exist until the agent registers them.
func (driver *Driver) SetLocation(ctx context.Context, location device.Location) error {
	if err := driver.invokeEmpty(
		ctx, pbwire.MethodEnableMockLocationProviders, pbwire.EmptyRequest{}); err != nil {
		return err
	}
	return driver.invokeEmpty(ctx, pbwire.MethodSetLocation, pbwire.SetLocationRequest{
		Latitude: location.Latitude, Longitude: location.Longitude,
	})
}

// orientations maps the engine's canonical values onto user_rotation
// (specs/02 line 46). The numbers are the platform's Surface rotations:
// LANDSCAPE_LEFT is the docs' "90 degrees counter-clockwise", which is
// ROTATION_90; LANDSCAPE_RIGHT is ROTATION_270.
var orientations = map[device.Orientation]string{
	"PORTRAIT":        "0",
	"LANDSCAPE_LEFT":  "1",
	"UPSIDE_DOWN":     "2",
	"LANDSCAPE_RIGHT": "3",
}

func (driver *Driver) SetOrientation(ctx context.Context, orientation device.Orientation) error {
	rotation, known := orientations[device.Orientation(strings.ToUpper(string(orientation)))]
	if !known {
		return fmt.Errorf("%w: android has no orientation %q", device.ErrUnsupported, orientation)
	}
	return driver.adb.PutSetting(ctx, "system", "user_rotation", rotation)
}

func (driver *Driver) CurrentOrientation(ctx context.Context) (device.Orientation, error) {
	return driver.adb.CurrentOrientation(ctx)
}

func (driver *Driver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	return driver.invokeEmpty(ctx, pbwire.MethodEraseAllText, pbwire.EraseAllTextRequest{
		CharactersToErase: request.CharactersToErase,
	})
}

// SetProxy is the global http_proxy setting (specs/02 line 46).
func (driver *Driver) SetProxy(ctx context.Context, proxy device.Proxy) error {
	if strings.TrimSpace(proxy.Host) == "" {
		return fmt.Errorf("android proxy requires a host")
	}
	return driver.adb.PutSetting(ctx, "global", "http_proxy",
		proxy.Host+":"+strconv.Itoa(int(proxy.Port)))
}

// ResetProxy writes the sentinel ":0", the platform's own convention for "no
// proxy" — deleting the row does not reliably clear an applied proxy.
func (driver *Driver) ResetProxy(ctx context.Context) error {
	return driver.adb.PutSetting(ctx, "global", "http_proxy", ":0")
}

// IsShutdown asks the agent, not adb: a device whose agent has died is
// unusable for a flow even though adb still lists it.
func (driver *Driver) IsShutdown(ctx context.Context) (bool, error) {
	if _, err := driver.connection(); err != nil {
		return true, nil
	}
	if _, err := driver.agentDeviceInfo(ctx); err != nil {
		return true, nil
	}
	return false, nil
}

// WaitUntilScreenIsStatic is the inverse of the agent's isWindowUpdating.
// The timeout is the caller's to enforce: the engine owns every wait budget.
func (driver *Driver) WaitUntilScreenIsStatic(
	ctx context.Context,
	_ device.ScreenStaticRequest,
) (bool, error) {
	updating, err := driver.isWindowUpdating(ctx, "")
	if err != nil {
		return false, err
	}
	return !updating, nil
}

func (driver *Driver) isWindowUpdating(ctx context.Context, appID string) (bool, error) {
	reply, err := driver.invoke(ctx, pbwire.MethodIsWindowUpdating, pbwire.CheckWindowUpdatingRequest{
		AppID: appID,
	})
	if err != nil {
		return false, err
	}
	var response pbwire.CheckWindowUpdatingResponse
	if err := response.Unmarshal(reply); err != nil {
		return false, fmt.Errorf("decoding isWindowUpdating reply: %w", err)
	}
	return response.IsWindowUpdating, nil
}

// WaitForAppToSettle returns nil while the window is still updating. Callers
// must not interpret nil as settled confirmation.
func (driver *Driver) WaitForAppToSettle(
	ctx context.Context,
	request device.SettleRequest,
) (*device.ViewHierarchy, error) {
	updating, err := driver.isWindowUpdating(ctx, request.AppID)
	if err != nil {
		return nil, err
	}
	if updating {
		return nil, nil
	}
	root, err := driver.agentHierarchy(ctx)
	if err != nil {
		return nil, err
	}
	return &device.ViewHierarchy{Root: root}, nil
}

// Capabilities declares what this driver refuses. Every false here has a
// matching ErrUnsupported at call time; preflight reads this so a flow is
// refused before it runs rather than halfway through.
func (driver *Driver) Capabilities() device.Capabilities {
	return DeclaredCapabilities()
}

// PreflightRuntime resolves Android-version-dependent support without opening
// or installing the agent. adb getprop is read-only, so an API 26--28 device
// that cannot implement scoped MediaStore insertion is refused before Open can
// uninstall or install either driver APK.
func (driver *Driver) PreflightRuntime(ctx context.Context, requirements device.RuntimeRequirements) error {
	for _, command := range requirements.Commands {
		if command == "addMedia" {
			return driver.requireAddMediaAPI(ctx)
		}
	}
	return nil
}

// AddMedia streams each file to the agent over the service's one
// client-streaming rpc. Every frame carries the name and extension alongside
// its payload chunk — the metadata is idempotent, which keeps the agent free
// of first-frame special cases. One rpc moves one file.
func (driver *Driver) AddMedia(ctx context.Context, request device.AddMediaRequest) error {
	if len(request.Files) == 0 {
		return nil
	}
	if err := driver.requireAddMediaAPI(ctx); err != nil {
		return err
	}
	for _, file := range request.Files {
		if err := driver.addMediaFile(ctx, file.Path); err != nil {
			return err
		}
	}
	return nil
}

func (driver *Driver) requireAddMediaAPI(ctx context.Context) error {
	apiLevel, err := driver.adb.APILevel(ctx)
	if err != nil {
		return fmt.Errorf("addMedia: determining Android API level: %w", err)
	}
	if apiLevel < 29 {
		return fmt.Errorf(
			"%w: addMedia requires Android API 29+, device reports API %d",
			device.ErrUnsupported, apiLevel)
	}
	return nil
}

func (driver *Driver) addMediaFile(ctx context.Context, path string) error {
	conn, err := driver.connection()
	if err != nil {
		return err
	}
	media, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("addMedia: %w", err)
	}
	defer func() { _ = media.Close() }()
	extension := strings.TrimPrefix(filepath.Ext(path), ".")
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	frames := make(chan []byte, 1)
	producerErr := make(chan error, 1)
	go streamMediaFrames(streamCtx, media, name, extension, frames, producerErr)
	reply, invokeErr := conn.InvokeClientStream(streamCtx, pbwire.StreamAddMedia, frames)
	cancel()
	readErr := <-producerErr
	if err := errors.Join(readErr, invokeErr); err != nil {
		return err
	}
	var response pbwire.AddMediaResponse
	if err := response.Unmarshal(reply); err != nil {
		return fmt.Errorf("decoding addMedia reply: %w", err)
	}
	return nil
}

func streamMediaFrames(
	ctx context.Context,
	media io.Reader,
	name string,
	extension string,
	frames chan<- []byte,
	result chan<- error,
) {
	defer close(frames)
	buffer := make([]byte, mediaChunkBytes)
	sent := false
	for {
		read, err := media.Read(buffer)
		if read > 0 {
			sent = true
			message := pbwire.AddMediaRequest{
				Payload:   &pbwire.Payload{Data: buffer[:read]},
				MediaName: name,
				MediaExt:  extension,
			}.Marshal()
			select {
			case frames <- message:
			case <-ctx.Done():
				result <- ctx.Err()
				return
			}
		}
		if errors.Is(err, io.EOF) {
			if !sent {
				select {
				case frames <- pbwire.AddMediaRequest{
					Payload:   &pbwire.Payload{},
					MediaName: name,
					MediaExt:  extension,
				}.Marshal():
				case <-ctx.Done():
					result <- ctx.Err()
					return
				}
			}
			result <- nil
			return
		}
		if err != nil {
			result <- fmt.Errorf("addMedia: reading %q: %w", name, err)
			return
		}
	}
}

func (driver *Driver) IsAirplaneModeEnabled(ctx context.Context) (bool, error) {
	return driver.adb.AirplaneModeEnabled(ctx)
}

func (driver *Driver) SetAirplaneMode(ctx context.Context, request device.AirplaneModeRequest) error {
	return driver.adb.SetAirplaneMode(ctx, request.Enabled)
}

// QueryOnDeviceElements has no rpc in the frozen proto: the agent returns a
// whole hierarchy, it does not evaluate queries against it.
func (driver *Driver) QueryOnDeviceElements(
	context.Context,
	device.QueryRequest,
) ([]device.TreeNode, error) {
	return nil, fmt.Errorf("%w: the android agent has no on-device query", device.ErrUnsupported)
}
