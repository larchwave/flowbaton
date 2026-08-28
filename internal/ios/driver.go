package ios

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
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
	simctl DeviceTools

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
	// logMu guards deviceLogs, the long-lived unified-log captures in flight.
	logMu              sync.Mutex
	deviceLogs         map[device.CaptureID]*iosDeviceLog
	spawnDeviceLog     func(context.Context, []string, io.Writer) (deviceLogProcess, error)
	logReservations    int
	deviceLogByteLimit int64
	diagnoseInFlight   bool
	diagnosticQuota    iosArtifactQuota
	// ClearAppState preserves the installed bundle across the uninstall. These
	// seams keep the filesystem work deterministic in tests.
	makeTemporaryDirectory func() (string, error)
	copyAppBundle          func(context.Context, string, string) error

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
	stop(context.Context) error
}

// iosRecording pairs a running recorder with the sink simctl writes to.
type iosRecording struct {
	recorder   screenRecorder
	outputPath string
}

// NewDriver binds a runner client and a device-tools surface to one udid.
// The simulator passes Simctl; a physical device passes its own DeviceTools.
func NewDriver(
	udid string, port int, client *Client, simctl DeviceTools, runner *RunnerBundle,
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
func (driver *Driver) Close(ctx context.Context) error {
	return errors.Join(
		driver.stopAllRecordings(ctx), driver.stopAllDeviceLogs(ctx), driver.stopRunnerProcess())
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
	return driver.waitForForeground(ctx, request.AppID)
}

// foregroundTimeout bounds how long a launched app may take to come to the
// front. The runner's own launch route allows the same.
const foregroundTimeout = 30 * time.Second

// waitForForeground polls the runner until appID is the foreground app.
// simctl returns as soon as the process starts, and a hierarchy asked for
// before the app is in front is the springboard's: the runner answers with
// the home screen for an app id that is not yet foreground. An explore
// session took its start screen from the home screen that way.
func (driver *Driver) waitForForeground(ctx context.Context, appID string) error {
	deadline := time.Now().Add(foregroundTimeout)
	for {
		running, err := driver.client.RunningApp(ctx, []string{appID})
		if err != nil {
			return fmt.Errorf("waiting for %s to come to the foreground: %w", appID, err)
		}
		if running == appID {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not come to the foreground within %s", appID, foregroundTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
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

// ClearAppState preserves the installed application bundle, uninstalls it to
// clear its data container, then reinstalls the same bundle. The engine applies
// permissions and launches only after this call succeeds.
func (driver *Driver) ClearAppState(ctx context.Context, request device.AppRequest) error {
	installedPath, err := driver.simctl.AppContainer(ctx, request.AppID)
	if err != nil {
		return fmt.Errorf("locating installed iOS application %q: %w", request.AppID, err)
	}
	makeTemporaryDirectory := driver.makeTemporaryDirectory
	if makeTemporaryDirectory == nil {
		makeTemporaryDirectory = func() (string, error) {
			return os.MkdirTemp("", "flowbaton-ios-clear-state-")
		}
	}
	temporaryDirectory, err := makeTemporaryDirectory()
	if err != nil {
		return fmt.Errorf("creating temporary iOS application directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(temporaryDirectory) }()

	bundleName := filepath.Base(filepath.Clean(installedPath))
	if bundleName == "." || bundleName == string(filepath.Separator) {
		return fmt.Errorf("installed iOS application path %q has no bundle name", installedPath)
	}
	preservedPath := filepath.Join(temporaryDirectory, bundleName)
	copyAppBundle := driver.copyAppBundle
	if copyAppBundle == nil {
		copyAppBundle = copyIOSAppBundle
	}
	if err := copyAppBundle(ctx, installedPath, preservedPath); err != nil {
		return fmt.Errorf("preserving installed iOS application %q: %w", request.AppID, err)
	}
	if err := driver.simctl.Uninstall(ctx, request.AppID); err != nil {
		return err
	}
	restoreContext, cancelRestore := context.WithTimeout(
		context.WithoutCancel(ctx), iosAppRestoreTimeout)
	defer cancelRestore()
	if err := driver.simctl.Install(restoreContext, preservedPath); err != nil {
		return fmt.Errorf("reinstalling cleared iOS application %q: %w", request.AppID, err)
	}
	return nil
}

func copyIOSAppBundle(ctx context.Context, source, destination string) error {
	output, err := exec.CommandContext(ctx, "ditto", source, destination).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ditto: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

const iosAppRestoreTimeout = 30 * time.Second

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
	return driver.client.PressKey(ctx, key, driver.defaultAppIDs(request.AppIDs))
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

// BackPress agrees with Capabilities: iOS has no platform back gesture.
func (driver *Driver) BackPress(context.Context) error {
	return fmt.Errorf("%w: iOS has no platform back gesture", device.ErrUnsupported)
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
	return driver.client.PressKey(ctx, KeyReturn, driver.defaultAppIDs(nil))
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
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.recMu.Lock()
	recording, ok := driver.recordings[id]
	delete(driver.recordings, id)
	driver.recMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no iOS screen recording is running for %q", id)
	}
	if err := stopIOSRecording(ctx, recording); err != nil {
		return nil, err
	}
	return []device.Artifact{{Kind: "recording", Path: recording.outputPath}}, nil
}

func (driver *Driver) stopAllRecordings(ctx context.Context) error {
	driver.recMu.Lock()
	recordings := driver.recordings
	driver.recordings = nil
	driver.recMu.Unlock()

	ids := make([]string, 0, len(recordings))
	for id := range recordings {
		ids = append(ids, string(id))
	}
	slices.Sort(ids)
	errs := make([]error, 0, len(ids))
	for _, rawID := range ids {
		cleanupContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), iosRecordingFinalizeTimeout)
		err := stopIOSRecording(cleanupContext, recordings[device.CaptureID(rawID)])
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("screen recording %q: %w", rawID, err))
		}
	}
	return errors.Join(errs...)
}

func stopIOSRecording(ctx context.Context, recording iosRecording) error {
	stopContext, cancel := context.WithTimeout(ctx, iosRecordingFinalizeTimeout)
	defer cancel()
	stopErr := recording.recorder.stop(stopContext)
	info, statErr := os.Stat(recording.outputPath)
	if statErr == nil && (!info.Mode().IsRegular() || info.Size() == 0) {
		statErr = fmt.Errorf("artifact %q is not a non-empty regular file", recording.outputPath)
	}
	if statErr != nil {
		statErr = fmt.Errorf("validating iOS screen recording: %w", statErr)
	}
	if stopErr != nil {
		stopErr = fmt.Errorf("stopping iOS screen recording: %w", stopErr)
	}
	return errors.Join(stopErr, statErr)
}

// realIOSRecorder spawns the simctl recorder. It uses exec.Command, NOT
// CommandContext: cancelling the context would SIGKILL simctl and leave a
// truncated .mov, whereas stop() sends SIGINT so the recorder writes the moov
// atom and finalizes the file.
func realIOSRecorder(ctx context.Context, args []string) (screenRecorder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.Command("xcrun", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	child := newIOSLocalChild(cmd)
	// Start waits for the capture marker so a later interrupt finalizes a valid
	// video file.
	startupErr := awaitRecordingStartedWithin(ctx, stdout, iosRecordingStartupTimeout)
	if startupErr != nil {
		killErr := child.kill()
		var reapErr error
		select {
		case waitErr := <-child.done:
			reapErr = allowKilledExit(waitErr)
		case <-time.After(iosLocalChildReapTimeout):
			reapErr = errors.New("timed out reaping killed iOS recorder child during startup")
		}
		return nil, errors.Join(startupErr, killErr, reapErr)
	}
	// simctl keeps talking after that line, and a full pipe would wedge it.
	go func() { _, _ = io.Copy(io.Discard, stdout) }()
	return &execRecorder{child: child}, nil
}

// recordingStartedMarker is what simctl prints once it is capturing:
//
//	$ xcrun simctl io UDID recordVideo capture.mp4
//	Recording started
//	^C
//	Recording completed. Writing to disk.
//	Wrote video to: capture.mp4
const recordingStartedMarker = "Recording started"

const (
	iosRecordingStartupTimeout  = 15 * time.Second
	iosRecordingFinalizeTimeout = 15 * time.Second
	iosLocalChildReapTimeout    = time.Second
)

func awaitRecordingStartedWithin(ctx context.Context, output io.Reader, timeout time.Duration) error {
	started := make(chan error, 1)
	go func() { started <- awaitRecordingStarted(output) }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-started:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf("timed out after %s", timeout)
	}
}

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

type iosLocalChild struct {
	done   <-chan error
	signal func(os.Signal) error
	kill   func() error
}

func newIOSLocalChild(cmd *exec.Cmd) *iosLocalChild {
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &iosLocalChild{done: done, signal: cmd.Process.Signal, kill: cmd.Process.Kill}
}

func stopIOSLocalChild(ctx context.Context, child *iosLocalChild) error {
	if err := child.signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case err := <-child.done:
		return allowInterruptExit(err)
	case <-ctx.Done():
		killErr := child.kill()
		select {
		case waitErr := <-child.done:
			return errors.Join(ctx.Err(), killErr, allowKilledExit(waitErr))
		case <-time.After(iosLocalChildReapTimeout):
			return errors.Join(ctx.Err(), killErr, errors.New("timed out reaping killed iOS recorder child"))
		}
	}
}

func allowKilledExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok &&
			status.Signaled() && status.Signal() == syscall.SIGKILL {
			return nil
		}
	}
	return err
}

// execRecorder is a real simctl recorder child.
type execRecorder struct{ child *iosLocalChild }

func (r *execRecorder) stop(ctx context.Context) error {
	return stopIOSLocalChild(ctx, r.child)
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

func (driver *Driver) CurrentOrientation(ctx context.Context) (device.Orientation, error) {
	info, err := driver.client.DeviceInfo(ctx)
	if err != nil {
		return "", err
	}
	orientation, ok := currentOrientations[info.Orientation]
	if !ok {
		return "", fmt.Errorf("iOS runner reported unsupported orientation %q", info.Orientation)
	}
	return orientation, nil
}

var currentOrientations = map[ScreenOrientation]device.Orientation{
	ScreenOrientationPortrait:       "PORTRAIT",
	ScreenOrientationUpsideDown:     "UPSIDE_DOWN",
	ScreenOrientationLandscapeLeft:  "LANDSCAPE_LEFT",
	ScreenOrientationLandscapeRight: "LANDSCAPE_RIGHT",
}

var orientations = map[string]Orientation{
	"portrait":        OrientationPortrait,
	"landscape_left":  OrientationLandscapeLeft,
	"landscape_right": OrientationLandscapeRight,
	"upside_down":     OrientationUpsideDown,
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
	return DeclaredCapabilities()
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
	ctx context.Context,
	request device.DeviceLogRequest,
) (device.CaptureID, error) {
	if strings.TrimSpace(request.AppID) != "" {
		return "", fmt.Errorf(
			"%w: iOS unified-log capture is device-wide and cannot filter bundle %q",
			device.ErrUnsupported, request.AppID)
	}
	directory, err := prepareIOSArtifactDirectory(request.OutputDirectory)
	if err != nil {
		return "", err
	}
	driver.logMu.Lock()
	if len(driver.deviceLogs)+driver.logReservations >= maxActiveIOSDeviceLogs {
		driver.logMu.Unlock()
		return "", fmt.Errorf("iOS device-log capture limit of %d is already active", maxActiveIOSDeviceLogs)
	}
	driver.logReservations++
	driver.logMu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			driver.logMu.Lock()
			driver.logReservations--
			driver.logMu.Unlock()
		}
	}()
	output, err := os.CreateTemp(directory, "ios-device-log-*.ndjson")
	if err != nil {
		return "", fmt.Errorf("creating iOS device-log artifact: %w", err)
	}
	outputPath := output.Name()
	spawn := driver.spawnDeviceLog
	if spawn == nil {
		spawn = realIOSDeviceLog
	}
	limiter := newIOSLogWriter(output, driver.iosDeviceLogLimit())
	process, err := spawn(
		ctx,
		[]string{"simctl", "spawn", driver.udid, "log", "stream", "--style", "ndjson"},
		limiter,
	)
	if err != nil {
		_ = output.Close()
		_ = os.Remove(outputPath)
		return "", fmt.Errorf("starting iOS device-log capture: %w", err)
	}
	id := device.CaptureID(outputPath)
	capture := &iosDeviceLog{
		process: process, output: output, outputPath: outputPath, limiter: limiter, done: make(chan struct{}),
	}
	if limiter.limitError() != nil {
		cleanupContext, cancel := context.WithTimeout(context.Background(), iosDiagnosticStopTimeout)
		err := finalizeIOSDeviceLog(cleanupContext, capture)
		cancel()
		return "", fmt.Errorf("starting iOS device-log capture: %w", err)
	}
	driver.logMu.Lock()
	if driver.deviceLogs == nil {
		driver.deviceLogs = map[device.CaptureID]*iosDeviceLog{}
	}
	driver.deviceLogs[id] = capture
	driver.logReservations--
	reserved = false
	driver.logMu.Unlock()
	go func() {
		select {
		case <-limiter.exceeded:
			cleanupContext, cancel := context.WithTimeout(context.Background(), iosDiagnosticStopTimeout)
			defer cancel()
			_ = finalizeIOSDeviceLog(cleanupContext, capture)
		case <-capture.done:
		}
	}()
	return id, nil
}

func (driver *Driver) StopDeviceLogCapture(
	ctx context.Context,
	id device.CaptureID,
) ([]device.Artifact, error) {
	driver.logMu.Lock()
	capture, ok := driver.deviceLogs[id]
	delete(driver.deviceLogs, id)
	driver.logMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no iOS device-log capture is running for %q", id)
	}
	stopContext, cancel := context.WithTimeout(ctx, iosDiagnosticStopTimeout)
	defer cancel()
	if err := finalizeIOSDeviceLog(stopContext, capture); err != nil {
		return nil, err
	}
	return []device.Artifact{{Kind: "log", Path: capture.outputPath}}, nil
}

func (driver *Driver) CollectCrashArtifacts(
	ctx context.Context,
	request device.ArtifactRequest,
) ([]device.Artifact, error) {
	if strings.TrimSpace(request.AppID) != "" {
		return nil, fmt.Errorf(
			"%w: simctl diagnose is device-wide and cannot filter bundle %q",
			device.ErrUnsupported, request.AppID)
	}
	directory, err := prepareIOSArtifactDirectory(request.OutputDirectory)
	if err != nil {
		return nil, err
	}
	driver.logMu.Lock()
	if driver.diagnoseInFlight {
		driver.logMu.Unlock()
		return nil, fmt.Errorf("an iOS diagnose capture is already running")
	}
	driver.diagnoseInFlight = true
	driver.logMu.Unlock()
	defer func() {
		driver.logMu.Lock()
		driver.diagnoseInFlight = false
		driver.logMu.Unlock()
	}()
	diagnoseDirectory, err := os.MkdirTemp(directory, "ios-crash-")
	if err != nil {
		return nil, fmt.Errorf("creating iOS diagnose directory: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(diagnoseDirectory)
		}
	}()
	diagnoseContext, cancel := context.WithTimeout(ctx, iosDiagnoseOuterTimeout)
	defer cancel()
	quota := driver.iosDiagnosticQuota()
	quotaErrors := make(chan error, 1)
	monitorContext, stopMonitor := context.WithCancel(diagnoseContext)
	defer stopMonitor()
	go monitorIOSCrashArtifacts(monitorContext, diagnoseDirectory, quota, quotaErrors, cancel)
	diagnoseErr := driver.simctl.Diagnose(
		diagnoseContext, diagnoseDirectory, iosDiagnoseCommandTimeout)
	stopMonitor()
	var quotaErr error
	select {
	case quotaErr = <-quotaErrors:
	default:
		quotaErr = inspectIOSCrashArtifacts(diagnoseDirectory, quota)
	}
	if err := errors.Join(quotaErr, diagnoseErr); err != nil {
		return nil, err
	}
	artifacts, err := collectIOSCrashArtifacts(diagnoseDirectory, quota)
	if err != nil {
		return nil, err
	}
	keep = true
	return artifacts, nil
}

const (
	maxActiveIOSDeviceLogs    = 4
	maxIOSDeviceLogBytes      = int64(16 << 20)
	maxIOSCrashFiles          = 128
	maxIOSCrashFileBytes      = int64(256 << 20)
	maxIOSCrashAggregateBytes = int64(1 << 30)
	iosDiagnosticStopTimeout  = 10 * time.Second
	iosDiagnoseCommandTimeout = 30 * time.Second
	iosDiagnoseOuterTimeout   = 45 * time.Second
)

type deviceLogProcess interface {
	stop(context.Context) error
}

type iosDeviceLog struct {
	process    deviceLogProcess
	output     *os.File
	outputPath string
	limiter    *iosLogWriter
	once       sync.Once
	done       chan struct{}
	result     error
}

type iosArtifactQuota struct {
	maximumFiles     int
	maximumFileBytes int64
	maximumAllBytes  int64
}

var errIOSDeviceLogLimit = errors.New("iOS device log reached its byte limit")

func (driver *Driver) iosDeviceLogLimit() int64 {
	if driver.deviceLogByteLimit > 0 {
		return driver.deviceLogByteLimit
	}
	return maxIOSDeviceLogBytes
}

func (driver *Driver) iosDiagnosticQuota() iosArtifactQuota {
	if driver.diagnosticQuota.maximumFiles > 0 {
		return driver.diagnosticQuota
	}
	return iosArtifactQuota{
		maximumFiles: maxIOSCrashFiles, maximumFileBytes: maxIOSCrashFileBytes,
		maximumAllBytes: maxIOSCrashAggregateBytes,
	}
}

func prepareIOSArtifactDirectory(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return "", fmt.Errorf("iOS diagnostic output directory is empty")
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", fmt.Errorf("resolving iOS diagnostic output directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("creating iOS diagnostic output directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving iOS diagnostic output directory symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspecting iOS diagnostic output directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("iOS diagnostic output path %q is not a directory", resolved)
	}
	return resolved, nil
}

func realIOSDeviceLog(
	ctx context.Context,
	args []string,
	output io.Writer,
) (deviceLogProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "xcrun", args...)
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return &execDeviceLogProcess{cmd: cmd, done: done}, nil
}

type iosLogWriter struct {
	mu       sync.Mutex
	output   io.Writer
	maximum  int64
	written  int64
	err      error
	exceeded chan struct{}
	once     sync.Once
}

func newIOSLogWriter(output io.Writer, maximum int64) *iosLogWriter {
	return &iosLogWriter{output: output, maximum: maximum, exceeded: make(chan struct{})}
}

func (writer *iosLogWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.err != nil {
		return 0, writer.err
	}
	remaining := writer.maximum - writer.written
	if int64(len(data)) <= remaining {
		written, err := writer.output.Write(data)
		writer.written += int64(written)
		return written, err
	}
	written := 0
	if remaining > 0 {
		var err error
		written, err = writer.output.Write(data[:remaining])
		writer.written += int64(written)
		if err != nil {
			return written, err
		}
	}
	writer.err = errIOSDeviceLogLimit
	writer.once.Do(func() { close(writer.exceeded) })
	return written, writer.err
}

func (writer *iosLogWriter) limitError() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.err
}

type execDeviceLogProcess struct {
	cmd  *exec.Cmd
	done <-chan error
}

func (process *execDeviceLogProcess) stop(ctx context.Context) error {
	select {
	case err := <-process.done:
		return err
	default:
	}
	if err := process.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case err := <-process.done:
		return allowInterruptExit(err)
	case <-ctx.Done():
		killErr := process.cmd.Process.Kill()
		select {
		case waitErr := <-process.done:
			return errors.Join(ctx.Err(), killErr, waitErr)
		case <-time.After(time.Second):
			return errors.Join(ctx.Err(), killErr, errors.New("timed out waiting for killed iOS log child"))
		}
	}
}

func allowInterruptExit(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok &&
			status.Signaled() && status.Signal() == syscall.SIGINT {
			return nil
		}
	}
	return err
}

func finalizeIOSDeviceLog(ctx context.Context, capture *iosDeviceLog) error {
	capture.once.Do(func() {
		stopErr := capture.process.stop(ctx)
		closeErr := capture.output.Close()
		limitErr := capture.limiter.limitError()
		var artifactErr error
		if limitErr == nil {
			artifactErr = validateIOSDiagnosticArtifact(capture.outputPath, capture.limiter.maximum)
		}
		capture.result = errors.Join(limitErr, stopErr, closeErr, artifactErr)
		if capture.result != nil {
			removeErr := os.Remove(capture.outputPath)
			if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				capture.result = errors.Join(capture.result,
					fmt.Errorf("removing partial iOS device log %q: %w", capture.outputPath, removeErr))
			}
		}
		close(capture.done)
	})
	<-capture.done
	return capture.result
}

func (driver *Driver) stopAllDeviceLogs(ctx context.Context) error {
	driver.logMu.Lock()
	captures := driver.deviceLogs
	driver.deviceLogs = nil
	driver.logMu.Unlock()
	ids := make([]string, 0, len(captures))
	for id := range captures {
		ids = append(ids, string(id))
	}
	slices.Sort(ids)
	stopContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx), iosDiagnosticStopTimeout)
	defer cancel()
	errs := make([]error, 0, len(ids))
	for _, rawID := range ids {
		if err := finalizeIOSDeviceLog(stopContext, captures[device.CaptureID(rawID)]); err != nil {
			errs = append(errs, fmt.Errorf("device-log capture %q: %w", rawID, err))
		}
	}
	return errors.Join(errs...)
}

func validateIOSDiagnosticArtifact(path string, maximumBytes int64) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("validating iOS diagnostic artifact %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("iOS diagnostic artifact %q is not a non-empty regular file", path)
	}
	if info.Size() > maximumBytes {
		return fmt.Errorf("iOS diagnostic artifact %q exceeds the %d-byte limit", path, maximumBytes)
	}
	return nil
}

func collectIOSCrashArtifacts(
	directory string, quota iosArtifactQuota,
) ([]device.Artifact, error) {
	var artifacts []device.Artifact
	var allBytes int64
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if len(artifacts) >= quota.maximumFiles {
			return fmt.Errorf("iOS diagnose exceeded the %d-file limit", quota.maximumFiles)
		}
		if err := validateIOSDiagnosticArtifact(path, quota.maximumFileBytes); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > quota.maximumAllBytes-allBytes {
			return fmt.Errorf("iOS diagnose exceeded the %d-byte total limit", quota.maximumAllBytes)
		}
		allBytes += info.Size()
		artifacts = append(artifacts, device.Artifact{Kind: "crash", Path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collecting iOS diagnose artifacts: %w", err)
	}
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("simctl diagnose produced no crash artifacts in %q", directory)
	}
	return artifacts, nil
}

func inspectIOSCrashArtifacts(directory string, quota iosArtifactQuota) error {
	_, err := collectIOSCrashArtifacts(directory, quota)
	if err != nil && strings.Contains(err.Error(), "produced no crash artifacts") {
		return nil
	}
	return err
}

func monitorIOSCrashArtifacts(
	ctx context.Context,
	directory string,
	quota iosArtifactQuota,
	violations chan<- error,
	cancel context.CancelFunc,
) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := inspectIOSCrashArtifacts(directory, quota); err != nil {
				select {
				case violations <- err:
				default:
				}
				cancel()
				return
			}
		}
	}
}
