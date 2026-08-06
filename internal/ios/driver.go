package ios

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
)

// Driver composes the two halves of the iOS host side into the frozen
// device.Driver.
//
// The split is not arbitrary. The XCTest runner owns everything that happens
// on screen — gestures, typing, the accessibility hierarchy — because those
// need a process inside the app's UI session. simctl owns everything around
// the app — install, launch with arguments, permissions, location, media —
// because those are simulator-level operations the runner has no access to.
// Where both could serve, the one that can carry the full request wins:
// launchApp goes to simctl because the runner's /launchApp route takes only a
// bundle id and would silently drop the arguments.

// Platform is the device platform this driver reports.
const Platform = device.Platform("ios")

// checkableTypes are the XCUIElementTypes that have a checked state at all,
// per specs/02-device-drivers.md line 49: checkbox=12, switch=40, toggle=41.
// Every other type has no checked state, which is not the same as false.
var checkableTypes = map[int]bool{12: true, 40: true, 41: true}

func checkableElementType(elementType int) bool {
	return checkableTypes[elementType]
}

// Driver drives one simulator.
type Driver struct {
	udid string
	// port is carried only so Name can report it. The client already holds the
	// base URL it talks to; a sharded run needs the number in failure messages.
	port   int
	client *Client
	simctl *Simctl

	// launchedMu guards launchedAppID, which the runner needs and only this
	// driver knows. See defaultAppIDs.
	launchedMu    sync.Mutex
	launchedAppID string

	// recMu guards recordings, the screen recordings in flight keyed by the
	// CaptureID StartScreenRecording handed out.
	recMu      sync.Mutex
	recordings map[device.CaptureID]iosRecording
	// spawnRecorder starts the long-lived `xcrun simctl io recordVideo` child.
	// It is a field so a test drives the start/stop lifecycle without a booted
	// simulator; nil means spawn for real.
	spawnRecorder func(ctx context.Context, args []string) (screenRecorder, error)

	// runner names a prebuilt runner Open may start itself; nil leaves the
	// runner the operator's job. process is the child it started, so Close can
	// take it down. See managed_runner.go.
	runner  *RunnerBundle
	process runnerProcess
	// spawnRunner is the same kind of seam as spawnRecorder; nil spawns for
	// real. startupPoll and startupTimeout pace Open's reachability wait.
	spawnRunner    func(ctx context.Context, args, environment []string) (runnerProcess, error)
	startupPoll    time.Duration
	startupTimeout time.Duration
}

// screenRecorder is a running simctl recorder the driver can stop. stop sends
// SIGINT so simctl finalizes the .mov, then returns once the child has exited.
type screenRecorder interface {
	stop() error
}

// iosRecording pairs a running recorder with the sink simctl writes to.
type iosRecording struct {
	recorder   screenRecorder
	outputPath string
}

// NewDriver binds a runner client and a simctl surface to one udid.
func NewDriver(
	udid string, port int, client *Client, simctl *Simctl, runner *RunnerBundle,
) *Driver {
	return &Driver{
		udid:           udid,
		port:           port,
		client:         client,
		simctl:         simctl,
		runner:         runner,
		startupPoll:    runnerStartupPoll,
		startupTimeout: runnerStartupTimeout,
	}
}

// Name identifies the runner this driver talks to, port included. Each shard
// has its own port, so the pair identifies the runner in operator messages.
func (driver *Driver) Name() string {
	return fmt.Sprintf("ios:%s:%d", driver.udid, driver.port)
}

// Open confirms the runner is answering so absence is reported as a setup
// failure. With a bundle it owns the runner's whole life: start it,
// then poll until it answers. See managed_runner.go.
func (driver *Driver) Open(ctx context.Context) error {
	if driver.runner != nil {
		return driver.openManagedRunner(ctx)
	}
	return driver.client.Status(ctx)
}

// Close stops a runner this driver started and leaves the simulator as it was.
// Shutting the simulator down would destroy the state an operator may want to
// inspect after a failed run, and the session that booted it is the one that
// should decide its fate. An operator-started runner is left alone for the
// same reason.
func (driver *Driver) Close(context.Context) error {
	return driver.stopRunnerProcess()
}

func (driver *Driver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	info, err := driver.client.DeviceInfo(ctx)
	if err != nil {
		return device.DeviceInfo{}, err
	}
	// specs/02-device-drivers.md line 28: the iOS grid is points. Pixels and
	// points differ by the Retina scale factor, so collapsing them would
	// misplace every coordinate.
	return device.DeviceInfo{
		Platform:     Platform,
		WidthPixels:  int(info.WidthPixels),
		HeightPixels: int(info.HeightPixels),
		WidthGrid:    int(info.WidthPoints),
		HeightGrid:   int(info.HeightPoints),
	}, nil
}

// LaunchApp goes through simctl, not the runner: only simctl can carry the
// typed launch arguments, and the runner's route would drop them silently.
func (driver *Driver) LaunchApp(ctx context.Context, request device.LaunchAppRequest) error {
	arguments := make([]LaunchArgument, 0, len(request.Arguments))
	for _, argument := range request.Arguments {
		arguments = append(arguments, LaunchArgument(argument))
	}
	if err := driver.simctl.Launch(ctx, request.AppID, arguments, false); err != nil {
		return err
	}
	driver.rememberLaunch(request.AppID)
	return nil
}

func (driver *Driver) StopApp(ctx context.Context, request device.AppRequest) error {
	return driver.simctl.Terminate(ctx, request.AppID)
}

// KillApp is StopApp on iOS. A simulator has no distinction between a graceful
// stop and a kill: simctl terminate is the only verb, and inventing a
// difference would mean one of the two silently does the other's job.
func (driver *Driver) KillApp(ctx context.Context, request device.AppRequest) error {
	return driver.simctl.Terminate(ctx, request.AppID)
}

// ClearAppState uninstalls. A simulator has no per-app "clear data" verb, and
// reporting success without clearing anything would leave a flow running
// against the previous session's data.
func (driver *Driver) ClearAppState(ctx context.Context, request device.AppRequest) error {
	return driver.simctl.Uninstall(ctx, request.AppID)
}

func (driver *Driver) ClearKeychain(ctx context.Context) error {
	return driver.simctl.ResetKeychain(ctx)
}

func (driver *Driver) Tap(ctx context.Context, request device.TapRequest) error {
	return driver.client.Touch(ctx, TouchRequest{X: request.Point.X, Y: request.Point.Y})
}

// LongPress sends the same route with a duration. TouchRequest.Duration is a
// pointer because its presence is what makes the touch a long press, so a tap
// must not send a zero.
func (driver *Driver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	seconds := float64(request.DurationMillis) / 1000
	return driver.client.Touch(ctx, TouchRequest{
		X: request.Point.X, Y: request.Point.Y, Duration: &seconds,
	})
}

func (driver *Driver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	key, ok := keyCodes[strings.ToLower(string(request.Code))]
	if !ok {
		return fmt.Errorf("%w: iOS has no key %q", device.ErrUnsupported, request.Code)
	}
	return driver.client.PressKey(ctx, key)
}

// keyCodes are the keys the frozen contract's pressKey route accepts. A code
// outside this set is refused rather than passed through, because the runner
// would reject it as a precondition failure that reads like a flow error.
var keyCodes = map[string]Key{
	"delete": KeyDelete, "backspace": KeyDelete,
	"return": KeyReturn, "enter": KeyEnter,
	"tab": KeyTab, "space": KeySpace, "escape": KeyEscape,
}

func (driver *Driver) ContentDescriptor(
	ctx context.Context,
	request device.ContentDescriptorRequest,
) (device.TreeNode, error) {
	hierarchy, err := driver.client.ViewHierarchy(
		ctx, driver.defaultAppIDs(request.AppIDs), request.ExcludeKeyboardElements)
	if err != nil {
		return device.TreeNode{}, err
	}
	return convertAXElement(hierarchy.AXElement), nil
}

// convertAXElement maps the runner's accessibility node onto the neutral tree
// every selector runs against.
//
// bounds is an Android-style "[l,t][r,b]" string on every platform, not a
// structured field: specs/02-device-drivers.md line 26 parses that shape, and
// line 77 has even the web driver emitting it "for uniform parsing".
//
// Checked is present on every element. Types without a checked state report
// false so `checked: false` remains a usable selector.
func convertAXElement(element AXElement) device.TreeNode {
	attributes := map[string]string{
		"bounds": renderBounds(element.Frame),
	}
	putAttribute(attributes, "id", element.Identifier)
	putAttribute(attributes, "accessibilityText", element.Label)
	putAttribute(attributes, "elementType", strconv.Itoa(element.ElementType))
	if element.Value != nil {
		putAttribute(attributes, "value", *element.Value)
	}
	if element.Title != nil {
		putAttribute(attributes, "title", *element.Title)
	}
	if element.PlaceholderValue != nil {
		putAttribute(attributes, "hintText", *element.PlaceholderValue)
	}
	// text mirrors the VALUE and nothing else. The label is already in
	// accessibilityText and does not belong in both.
	//
	// Labels stay in accessibilityText. Mirroring them into text would make a
	// single accessibility value visible under two attributes and alter broad
	// text selectors.
	if element.Value != nil && *element.Value != "" {
		attributes["text"] = *element.Value
	}

	checked := false
	if checkableElementType(element.ElementType) {
		checked = element.Selected
	}
	node := device.TreeNode{
		Attributes: attributes,
		Enabled:    boolPointer(element.Enabled),
		Focused:    boolPointer(element.HasFocus),
		Selected:   boolPointer(element.Selected),
		Checked:    boolPointer(checked),
	}
	if len(element.Children) != 0 {
		node.Children = make([]device.TreeNode, 0, len(element.Children))
		for _, child := range element.Children {
			node.Children = append(node.Children, convertAXElement(child))
		}
	}
	return node
}

// renderBounds converts an origin-plus-size frame to the left/top/right/bottom
// string every platform's hierarchy uses.
//
// Each coordinate is narrowed to single precision and then floored:
//
//	x=15.999999999999986 y=120.66666666666667  -> [16,120]
//	x=360.99999999999994 y=207.33333333333331  -> [361,207]
//
// Rounding gets the first row wrong (121 belongs to the row above), flooring
// the raw double gets the second wrong (360 walks the element a pixel left).
// The simulator's doubles carry noise around whole numbers. The wire value
// survives a float32 trip, which snaps the
// noise away and leaves a real .667 fraction alone.
//
// A pixel sounds cosmetic and is not: below, above, leftOf and rightOf check
// these numbers, so a coordinate on the wrong side of a neighbour's edge
// changes which element a selector resolves to.
func renderBounds(frame Frame) string {
	return fmt.Sprintf("[%d,%d][%d,%d]",
		floorSinglePrecision(frame.X), floorSinglePrecision(frame.Y),
		floorSinglePrecision(frame.X+frame.Width), floorSinglePrecision(frame.Y+frame.Height))
}

func floorSinglePrecision(value float64) int {
	return int(math.Floor(float64(float32(value))))
}

// putAttribute drops empty values rather than storing them. An attribute that
// is present but empty and one that is absent read the same to a person and
// differently to a matcher, so only one of them should exist.
func putAttribute(attributes map[string]string, key, value string) {
	if value != "" {
		attributes[key] = value
	}
}

func boolPointer(value bool) *bool {
	return &value
}

// ScrollVertical is a swipe across the middle of the screen. The runner has no
// scroll route, so the distance comes from the device's own geometry rather
// than a guessed constant.
func (driver *Driver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	info, err := driver.client.DeviceInfo(ctx)
	if err != nil {
		return err
	}
	amount := request.Amount
	if amount <= 0 {
		amount = 0.5
	}
	centerX, centerY := info.WidthPoints/2, info.HeightPoints/2
	if request.ElementPoint != nil {
		centerX, centerY = request.ElementPoint.X, request.ElementPoint.Y
	}
	travel := info.HeightPoints * amount / 2
	// Scrolling down means dragging content up.
	if strings.EqualFold(string(request.Direction), "down") {
		travel = -travel
	}
	return driver.client.SwipeV2(ctx, SwipeV2Request{
		StartX: centerX, StartY: centerY - travel,
		EndX: centerX, EndY: centerY + travel,
		Duration: defaultSwipeSeconds,
	})
}

// defaultSwipeSeconds is the ScrollVertical duration and the fallback for a
// Swipe request without a positive duration.
const defaultSwipeSeconds = 0.5

func (driver *Driver) IsKeyboardVisible(ctx context.Context, request device.KeyboardRequest) (bool, error) {
	return driver.client.KeyboardVisible(ctx, request.AppIDs)
}

// swipeGridEdge is where a directional swipe ends, as a fraction of the screen.
// specs/02-device-drivers.md line 43 defines the rule — "Directional swipes
// computed from grid (e.g. UP: center → 10% height)" — so UP ends a tenth of
// the way down and DOWN nine tenths. Device drivers share this fraction to
// keep gesture distance uniform.
const swipeGridEdge = 0.1

// Swipe covers all three shapes specs/02-device-drivers.md §1 gives the driver:
// explicit points, a bare direction, and an element point plus a direction.
//
// Direction-only swipes resolve screen geometry through the device before
// calculating their endpoints.
func (driver *Driver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	duration := float64(request.DurationMillis) / 1000
	if duration <= 0 {
		duration = defaultSwipeSeconds
	}
	if request.Start != nil && request.End != nil {
		// The caller already resolved both ends, so no round trip for geometry
		// nothing is going to read.
		return driver.client.SwipeV2(ctx, SwipeV2Request{
			StartX: request.Start.X, StartY: request.Start.Y,
			EndX: request.End.X, EndY: request.End.Y,
			Duration: duration,
		})
	}
	if request.Direction == "" {
		return fmt.Errorf(
			"%w: a swipe needs either both points or a direction", device.ErrUnsupported)
	}
	info, err := driver.client.DeviceInfo(ctx)
	if err != nil {
		return err
	}
	start := device.Point{X: info.WidthPoints / 2, Y: info.HeightPoints / 2}
	if request.ElementPoint != nil {
		start = *request.ElementPoint
	}
	end, err := swipeGridEnd(request.Direction, start, info.WidthPoints, info.HeightPoints)
	if err != nil {
		return err
	}
	return driver.client.SwipeV2(ctx, SwipeV2Request{
		StartX: start.X, StartY: start.Y,
		EndX: end.X, EndY: end.Y,
		Duration: duration,
	})
}

// swipeGridEnd is where a swipe in this direction ends. The cross axis keeps the
// start's coordinate, so a swipe from an element travels straight rather than
// drifting toward the middle of the screen.
//
// A direction outside the four is refused rather than defaulted. The parser
// already rejects anything else, so reaching this means the caller invented one
// — and swiping somewhere nobody asked for while reporting success is worse
// than an error naming the direction.
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

// BackPress is a no-op: specs/02-device-drivers.md line 49 defines that iOS
// has no back gesture at the driver level. Returning an error would fail every
// flow that shares a back step with Android.
func (driver *Driver) BackPress(context.Context) error {
	return nil
}

func (driver *Driver) InputText(ctx context.Context, request device.InputTextRequest) error {
	return driver.client.InputText(ctx, request.Text, request.AppIDs)
}

// OpenLink hands the URL to the simulator, which opens it in the system
// default. A browser choice cannot be honored, and honoring it silently in the
// wrong browser would be worse than refusing.
func (driver *Driver) OpenLink(ctx context.Context, request device.OpenLinkRequest) error {
	if request.Browser != "" {
		return fmt.Errorf(
			"%w: iOS cannot choose the browser %q; the system default opens every link",
			device.ErrUnsupported, request.Browser)
	}
	return driver.simctl.OpenURL(ctx, request.Link)
}

// HideKeyboard has no route of its own. The keyboard dismisses on return,
// which is the gesture a person would use.
func (driver *Driver) HideKeyboard(ctx context.Context) error {
	return driver.client.PressKey(ctx, KeyReturn)
}

func (driver *Driver) TakeScreenshot(
	ctx context.Context,
	request device.ScreenshotRequest,
) ([]byte, error) {
	return driver.client.Screenshot(ctx, request.Compressed)
}

// StartScreenRecording spawns `xcrun simctl io <udid> recordVideo <sink>` as a
// long-lived child (specs/02-device-drivers.md line 9) and returns a CaptureID
// that StopScreenRecording later stops. simctl writes straight to the sink and
// finalizes the .mov on SIGINT, so the sink is the artifact once stop returns.
func (driver *Driver) StartScreenRecording(
	ctx context.Context,
	request device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	output := strings.TrimSpace(request.OutputPath)
	if _, err := device.ValidateRecordingSink(output); err != nil {
		return "", err
	}
	spawn := driver.spawnRecorder
	if spawn == nil {
		spawn = realIOSRecorder
	}
	recorder, err := spawn(ctx, []string{"simctl", "io", driver.udid, "recordVideo", output})
	if err != nil {
		return "", fmt.Errorf("starting iOS screen recording: %w", err)
	}
	id := device.CaptureID(output)
	driver.recMu.Lock()
	if driver.recordings == nil {
		driver.recordings = map[device.CaptureID]iosRecording{}
	}
	driver.recordings[id] = iosRecording{recorder: recorder, outputPath: output}
	driver.recMu.Unlock()
	return id, nil
}

// StopScreenRecording ends a recording StartScreenRecording began and returns
// the artifact at its sink. It is deliberately NOT on the frozen Driver
// surface — v0 declares only the start half — so the recording controller
// completes the lifecycle by calling this concrete method directly.
func (driver *Driver) StopScreenRecording(
	_ context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.recMu.Lock()
	recording, ok := driver.recordings[id]
	delete(driver.recordings, id)
	driver.recMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no iOS screen recording is running for %q", id)
	}
	if err := recording.recorder.stop(); err != nil {
		return nil, fmt.Errorf("stopping iOS screen recording: %w", err)
	}
	return []device.Artifact{{Kind: "recording", Path: recording.outputPath}}, nil
}

// realIOSRecorder spawns the simctl recorder. It uses exec.Command, NOT
// CommandContext: cancelling the context would SIGKILL simctl and leave a
// truncated .mov, whereas stop() sends SIGINT so the recorder writes the moov
// atom and finalizes the file.
func realIOSRecorder(_ context.Context, args []string) (screenRecorder, error) {
	cmd := exec.Command("xcrun", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Start waits for the capture marker so a later interrupt finalizes a valid
	// video file.
	if err := awaitRecordingStarted(stdout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	// simctl keeps talking after that line, and a full pipe would wedge it.
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	return &execRecorder{cmd: cmd}, nil
}

// recordingStartedMarker is what simctl prints once it is capturing:
//
//	$ xcrun simctl io UDID recordVideo capture.mp4
//	Recording started
//	^C
//	Recording completed. Writing to disk.
//	Wrote video to: capture.mp4
const recordingStartedMarker = "Recording started"

// awaitRecordingStarted reads until the recorder says it is recording. A
// recorder that ends without ever saying so is reported with what it did say,
// rather than waited on: `simctl io` against a device it cannot use prints its
// complaint and leaves.
func awaitRecordingStarted(output io.Reader) error {
	scanner := bufio.NewScanner(output)
	var said []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, recordingStartedMarker) {
			return nil
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			said = append(said, trimmed)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading the recorder's output: %w", err)
	}
	return fmt.Errorf("the recorder never started recording: %s", strings.Join(said, "; "))
}

// execRecorder is a real simctl recorder child.
type execRecorder struct{ cmd *exec.Cmd }

func (r *execRecorder) stop() error {
	if err := r.cmd.Process.Signal(os.Interrupt); err != nil {
		return err
	}
	// The interrupt finishes the recording, so the resulting exit is ordinary.
	// The caller checks the output path for the finalized file.
	_ = r.cmd.Wait()
	return nil
}

func (driver *Driver) SetLocation(ctx context.Context, location device.Location) error {
	return driver.simctl.SetLocation(ctx, location.Latitude, location.Longitude)
}

func (driver *Driver) SetOrientation(ctx context.Context, orientation device.Orientation) error {
	value, ok := orientations[strings.ToLower(string(orientation))]
	if !ok {
		return fmt.Errorf("%w: iOS has no orientation %q", device.ErrUnsupported, orientation)
	}
	return driver.client.SetOrientation(ctx, value)
}

var orientations = map[string]Orientation{
	"portrait":       OrientationPortrait,
	"landscapeleft":  OrientationLandscapeLeft,
	"landscaperight": OrientationLandscapeRight,
	"upsidedown":     OrientationUpsideDown,
}

func (driver *Driver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	return driver.client.EraseText(ctx, int(request.CharactersToErase), request.AppIDs)
}

func (driver *Driver) SetProxy(context.Context, device.Proxy) error {
	return fmt.Errorf("%w: the simulator has no per-device proxy", device.ErrUnsupported)
}

func (driver *Driver) ResetProxy(context.Context) error {
	return fmt.Errorf("%w: the simulator has no per-device proxy", device.ErrUnsupported)
}

// IsShutdown asks the runner, not simctl. A booted simulator whose runner has
// died is unusable for a flow even though simctl still calls it Booted.
func (driver *Driver) IsShutdown(ctx context.Context) (bool, error) {
	if err := driver.client.Status(ctx); err != nil {
		return true, nil
	}
	return false, nil
}

func (driver *Driver) WaitUntilScreenIsStatic(
	ctx context.Context,
	_ device.ScreenStaticRequest,
) (bool, error) {
	// The timeout is the caller's to enforce: the engine owns every wait
	// budget (internal/engine/clock.go), and a second timer here would race it.
	return driver.client.IsScreenStatic(ctx)
}

// WaitForAppToSettle returns nil when settling cannot be confirmed. Callers
// must not interpret nil as settled confirmation.
func (driver *Driver) WaitForAppToSettle(
	ctx context.Context,
	request device.SettleRequest,
) (*device.ViewHierarchy, error) {
	static, err := driver.client.IsScreenStatic(ctx)
	if err != nil {
		return nil, err
	}
	if !static {
		return nil, nil
	}
	var appIDs []string
	if request.AppID != "" {
		appIDs = []string{request.AppID}
	}
	hierarchy, err := driver.client.ViewHierarchy(ctx, driver.defaultAppIDs(appIDs), false)
	if err != nil {
		return nil, err
	}
	return &device.ViewHierarchy{Root: convertAXElement(hierarchy.AXElement)}, nil
}

// rememberLaunch records what was launched, for defaultAppIDs.
func (driver *Driver) rememberLaunch(appID string) {
	driver.launchedMu.Lock()
	defer driver.launchedMu.Unlock()
	driver.launchedAppID = appID
}

// defaultAppIDs fills in the app filter when the caller did not name one.
//
// XCUITest requires a bundle identifier to snapshot an app. When the caller
// omits app IDs, this driver supplies the application it launched, which is the
// only active-app identity available at this boundary. Callers should provide
// an explicit filter after launching another app or returning home.
//
// A caller's own filter always wins, and nothing is invented when nothing has
// been launched: asking about an app that is not running makes the runner refuse
// with "not in the foreground", while asking about nothing at all gets the home
// screen, which is a valid answer.
func (driver *Driver) defaultAppIDs(requested []string) []string {
	if len(requested) > 0 {
		return requested
	}
	driver.launchedMu.Lock()
	defer driver.launchedMu.Unlock()
	if driver.launchedAppID == "" {
		return requested
	}
	return []string{driver.launchedAppID}
}

// Capabilities declares what this driver refuses. Every false here has a
// matching ErrUnsupported at call time; preflight reads this so a flow is
// refused before it runs rather than halfway through.
func (driver *Driver) Capabilities() device.Capabilities {
	return device.Capabilities{
		Platform: Platform,
		Features: map[string]bool{
			"proxy":                 false,
			"airplaneMode":          false,
			"androidChromeDevTools": false,
			"screenRecording":       true,
			"onDeviceQuery":         false,
			"deviceLogCapture":      false,
			"crashArtifacts":        false,
			"browserChoice":         false,
			"backPress":             false,
		},
	}
}

// SetPermissions issues one simctl call per permission. simctl takes a single
// service per invocation, and map order must not decide what gets applied
// first — a permission change can terminate the app, so the order is
// observable.
func (driver *Driver) SetPermissions(ctx context.Context, request device.PermissionsRequest) error {
	permissions := make([]string, 0, len(request.Permissions))
	for permission := range request.Permissions {
		permissions = append(permissions, permission)
	}
	slices.Sort(permissions)
	for _, permission := range permissions {
		if err := driver.simctl.SetPermission(
			ctx, request.AppID, permission, request.Permissions[permission]); err != nil {
			return err
		}
	}
	return nil
}

func (driver *Driver) AddMedia(ctx context.Context, request device.AddMediaRequest) error {
	paths := make([]string, 0, len(request.Files))
	for _, file := range request.Files {
		paths = append(paths, file.Path)
	}
	return driver.simctl.AddMedia(ctx, paths)
}

func (driver *Driver) IsAirplaneModeEnabled(context.Context) (bool, error) {
	return false, fmt.Errorf("%w: the simulator has no airplane mode", device.ErrUnsupported)
}

func (driver *Driver) SetAirplaneMode(context.Context, device.AirplaneModeRequest) error {
	return fmt.Errorf("%w: the simulator has no airplane mode", device.ErrUnsupported)
}

func (driver *Driver) SetAndroidChromeDevToolsEnabled(
	context.Context,
	device.ChromeDevToolsRequest,
) error {
	return fmt.Errorf("%w: Chrome DevTools hierarchy merging is Android-only", device.ErrUnsupported)
}

// QueryOnDeviceElements has no route in the frozen contract. The runner
// returns a whole hierarchy; it does not evaluate queries against it.
func (driver *Driver) QueryOnDeviceElements(
	context.Context,
	device.QueryRequest,
) ([]device.TreeNode, error) {
	return nil, fmt.Errorf("%w: the iOS runner has no on-device query", device.ErrUnsupported)
}

func (driver *Driver) StartDeviceLogCapture(
	context.Context,
	device.DeviceLogRequest,
) (device.CaptureID, error) {
	return "", fmt.Errorf("%w: iOS log capture is not wired to a stop path", device.ErrUnsupported)
}

func (driver *Driver) StopDeviceLogCapture(
	context.Context,
	device.CaptureID,
) ([]device.Artifact, error) {
	return nil, fmt.Errorf("%w: iOS log capture is not wired to a stop path", device.ErrUnsupported)
}

func (driver *Driver) CollectCrashArtifacts(
	context.Context,
	device.ArtifactRequest,
) ([]device.Artifact, error) {
	return nil, fmt.Errorf("%w: iOS crash artifact collection is not implemented", device.ErrUnsupported)
}
