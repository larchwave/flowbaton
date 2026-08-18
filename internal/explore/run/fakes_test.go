package run

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// makeState builds an observation from a tree the way the observer role
// does: signature plus flattened element table.
func makeState(appID string, root device.TreeNode) *explore.ScreenState {
	elements, err := explore.FlattenScreen(root)
	if err != nil {
		panic(err)
	}
	return &explore.ScreenState{
		Signature:  explore.ComputeSignature(appID, root),
		Hierarchy:  root,
		Elements:   elements,
		CapturedAt: time.Unix(0, 0),
	}
}

func button(label, id, bounds string) device.TreeNode {
	yes := true
	return device.TreeNode{
		Attributes: map[string]string{
			"text":        label,
			"resource-id": id,
			"bounds":      bounds,
			"class":       "android.widget.Button",
		},
		Clickable: &yes,
	}
}

func screen(title string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "text": title, "bounds": "[0,0][400,800]"},
		Children:   children,
	}
}

// fakeObserver serves scripted observations; the last one repeats. A nil
// entry in errs means success at that call index.
type fakeObserver struct {
	states []*explore.ScreenState
	errs   []error
	calls  int
}

func (f *fakeObserver) Observe(context.Context) (*explore.ScreenState, error) {
	index := f.calls
	f.calls++
	if index < len(f.errs) && f.errs[index] != nil {
		return nil, f.errs[index]
	}
	if len(f.states) == 0 {
		return nil, errors.New("no scripted states")
	}
	if index >= len(f.states) {
		index = len(f.states) - 1
	}
	return f.states[index], nil
}

// scriptedLLM replays canned assistant messages and captures every request
// so tests can assert on injected conversation content.
type scriptedLLM struct {
	replies  []explore.Message
	requests []explore.ChatRequest
	onCall   func(call int)
}

func (s *scriptedLLM) Chat(_ context.Context, request explore.ChatRequest) (explore.ChatResponse, error) {
	call := len(s.requests)
	s.requests = append(s.requests, request)
	if s.onCall != nil {
		s.onCall(call)
	}
	if call >= len(s.replies) {
		return explore.ChatResponse{}, fmt.Errorf("llm script exhausted at call %d", call)
	}
	return explore.ChatResponse{Message: s.replies[call], Usage: explore.Usage{InputTokens: 1, OutputTokens: 1}}, nil
}

func toolCall(id, name, args string) explore.Message {
	return explore.Message{
		Role:      explore.RoleAssistant,
		ToolCalls: []explore.ToolCall{{ID: id, Name: name, Arguments: []byte(args)}},
	}
}

func textReply(text string) explore.Message {
	return explore.Message{Role: explore.RoleAssistant, Text: text}
}

// fakeDriver records calls and delegates a few behaviors to overridable
// functions; everything else is a successful no-op.
type fakeDriver struct {
	calls    []string
	tapped   []device.Point
	backErr  error
	inputErr error
}

func (f *fakeDriver) log(name string) { f.calls = append(f.calls, name) }

func (f *fakeDriver) Name() string                        { return "fake" }
func (f *fakeDriver) Open(context.Context) error          { f.log("Open"); return nil }
func (f *fakeDriver) Close(context.Context) error         { f.log("Close"); return nil }
func (f *fakeDriver) ClearKeychain(context.Context) error { f.log("ClearKeychain"); return nil }

func (f *fakeDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	return device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 800}, nil
}

func (f *fakeDriver) LaunchApp(_ context.Context, request device.LaunchAppRequest) error {
	f.log("LaunchApp:" + request.AppID)
	return nil
}

func (f *fakeDriver) StopApp(_ context.Context, request device.AppRequest) error {
	f.log("StopApp:" + request.AppID)
	return nil
}

func (f *fakeDriver) KillApp(_ context.Context, request device.AppRequest) error {
	f.log("KillApp:" + request.AppID)
	return nil
}

func (f *fakeDriver) ClearAppState(_ context.Context, request device.AppRequest) error {
	f.log("ClearAppState:" + request.AppID)
	return nil
}

func (f *fakeDriver) Tap(_ context.Context, request device.TapRequest) error {
	f.log("Tap")
	f.tapped = append(f.tapped, request.Point)
	return nil
}

func (f *fakeDriver) LongPress(_ context.Context, request device.LongPressRequest) error {
	f.log("LongPress")
	f.tapped = append(f.tapped, request.Point)
	return nil
}

func (f *fakeDriver) PressKey(_ context.Context, request device.PressKeyRequest) error {
	f.log("PressKey:" + string(request.Code))
	return nil
}

func (f *fakeDriver) ContentDescriptor(context.Context, device.ContentDescriptorRequest) (device.TreeNode, error) {
	f.log("ContentDescriptor")
	return device.TreeNode{}, nil
}

func (f *fakeDriver) ScrollVertical(_ context.Context, request device.ScrollVerticalRequest) error {
	f.log("Scroll:" + string(request.Direction))
	return nil
}

func (f *fakeDriver) IsKeyboardVisible(context.Context, device.KeyboardRequest) (bool, error) {
	return false, nil
}

func (f *fakeDriver) Swipe(_ context.Context, request device.SwipeRequest) error {
	f.log("Swipe:" + string(request.Direction))
	return nil
}

func (f *fakeDriver) BackPress(context.Context) error {
	f.log("BackPress")
	return f.backErr
}

func (f *fakeDriver) InputText(_ context.Context, request device.InputTextRequest) error {
	f.log("InputText:" + request.Text)
	return f.inputErr
}

func (f *fakeDriver) OpenLink(context.Context, device.OpenLinkRequest) error { return nil }
func (f *fakeDriver) HideKeyboard(context.Context) error                     { f.log("HideKeyboard"); return nil }

func (f *fakeDriver) TakeScreenshot(context.Context, device.ScreenshotRequest) ([]byte, error) {
	return nil, nil
}

func (f *fakeDriver) StartScreenRecording(context.Context, device.ScreenRecordingRequest) (device.CaptureID, error) {
	return "", nil
}

func (f *fakeDriver) SetLocation(context.Context, device.Location) error       { return nil }
func (f *fakeDriver) SetOrientation(context.Context, device.Orientation) error { return nil }

func (f *fakeDriver) EraseText(_ context.Context, request device.EraseTextRequest) error {
	f.log(fmt.Sprintf("EraseText:%d", request.CharactersToErase))
	return nil
}

func (f *fakeDriver) SetProxy(context.Context, device.Proxy) error { return nil }
func (f *fakeDriver) ResetProxy(context.Context) error             { return nil }
func (f *fakeDriver) IsShutdown(context.Context) (bool, error)     { return false, nil }

func (f *fakeDriver) WaitUntilScreenIsStatic(context.Context, device.ScreenStaticRequest) (bool, error) {
	return true, nil
}

func (f *fakeDriver) WaitForAppToSettle(context.Context, device.SettleRequest) (*device.ViewHierarchy, error) {
	f.log("Settle")
	return nil, nil
}

func (f *fakeDriver) Capabilities() device.Capabilities {
	return device.Capabilities{Platform: "android"}
}

func (f *fakeDriver) SetPermissions(context.Context, device.PermissionsRequest) error { return nil }
func (f *fakeDriver) AddMedia(context.Context, device.AddMediaRequest) error          { return nil }
func (f *fakeDriver) IsAirplaneModeEnabled(context.Context) (bool, error)             { return false, nil }
func (f *fakeDriver) SetAirplaneMode(context.Context, device.AirplaneModeRequest) error {
	return nil
}
func (f *fakeDriver) SetAndroidChromeDevToolsEnabled(context.Context, device.ChromeDevToolsRequest) error {
	return nil
}

func (f *fakeDriver) QueryOnDeviceElements(context.Context, device.QueryRequest) ([]device.TreeNode, error) {
	return nil, nil
}

func (f *fakeDriver) StartDeviceLogCapture(context.Context, device.DeviceLogRequest) (device.CaptureID, error) {
	return "", nil
}

func (f *fakeDriver) StopDeviceLogCapture(context.Context, device.CaptureID) ([]device.Artifact, error) {
	return nil, nil
}

func (f *fakeDriver) CollectCrashArtifacts(context.Context, device.ArtifactRequest) ([]device.Artifact, error) {
	return nil, nil
}

var _ device.Driver = (*fakeDriver)(nil)

// fakeStore is a scripted in-memory experience store.
type fakeStore struct {
	entries  map[string]map[string]string
	recorded []explore.MemoryEntry
}

func (f *fakeStore) key(screen explore.ScreenSignature) string { return screen.Key() }

func (f *fakeStore) Index(_ context.Context, screen explore.ScreenSignature) ([]string, error) {
	titles := []string{}
	for title := range f.entries[f.key(screen)] {
		titles = append(titles, title)
	}
	return titles, nil
}

func (f *fakeStore) Get(_ context.Context, screen explore.ScreenSignature, title string) (string, error) {
	body, ok := f.entries[f.key(screen)][title]
	if !ok {
		return "", fmt.Errorf("no entry %q", title)
	}
	return body, nil
}

func (f *fakeStore) Record(_ context.Context, _ explore.ScreenSignature, entry explore.MemoryEntry) error {
	f.recorded = append(f.recorded, entry)
	return nil
}

func noSleep(context.Context, time.Duration) error { return nil }
