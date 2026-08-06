// Package enginetest provides deterministic host-side test doubles for the
// execution engine and other callers of the frozen device contract.
package enginetest

import (
	"context"
	"sync"

	"github.com/larchwave/flowbaton/internal/device"
)

// Method names one device.Driver operation in an action trace.
type Method string

const (
	MethodName                            Method = "Name"
	MethodOpen                            Method = "Open"
	MethodClose                           Method = "Close"
	MethodDeviceInfo                      Method = "DeviceInfo"
	MethodLaunchApp                       Method = "LaunchApp"
	MethodStopApp                         Method = "StopApp"
	MethodKillApp                         Method = "KillApp"
	MethodClearAppState                   Method = "ClearAppState"
	MethodClearKeychain                   Method = "ClearKeychain"
	MethodTap                             Method = "Tap"
	MethodLongPress                       Method = "LongPress"
	MethodPressKey                        Method = "PressKey"
	MethodContentDescriptor               Method = "ContentDescriptor"
	MethodScrollVertical                  Method = "ScrollVertical"
	MethodIsKeyboardVisible               Method = "IsKeyboardVisible"
	MethodSwipe                           Method = "Swipe"
	MethodBackPress                       Method = "BackPress"
	MethodInputText                       Method = "InputText"
	MethodOpenLink                        Method = "OpenLink"
	MethodHideKeyboard                    Method = "HideKeyboard"
	MethodTakeScreenshot                  Method = "TakeScreenshot"
	MethodStartScreenRecording            Method = "StartScreenRecording"
	MethodSetLocation                     Method = "SetLocation"
	MethodSetOrientation                  Method = "SetOrientation"
	MethodEraseText                       Method = "EraseText"
	MethodSetProxy                        Method = "SetProxy"
	MethodResetProxy                      Method = "ResetProxy"
	MethodIsShutdown                      Method = "IsShutdown"
	MethodWaitUntilScreenIsStatic         Method = "WaitUntilScreenIsStatic"
	MethodWaitForAppToSettle              Method = "WaitForAppToSettle"
	MethodCapabilities                    Method = "Capabilities"
	MethodSetPermissions                  Method = "SetPermissions"
	MethodAddMedia                        Method = "AddMedia"
	MethodIsAirplaneModeEnabled           Method = "IsAirplaneModeEnabled"
	MethodSetAirplaneMode                 Method = "SetAirplaneMode"
	MethodSetAndroidChromeDevToolsEnabled Method = "SetAndroidChromeDevToolsEnabled"
	MethodQueryOnDeviceElements           Method = "QueryOnDeviceElements"
	MethodStartDeviceLogCapture           Method = "StartDeviceLogCapture"
	MethodStopDeviceLogCapture            Method = "StopDeviceLogCapture"
	MethodCollectCrashArtifacts           Method = "CollectCrashArtifacts"
)

// Result is one scripted FIFO return from a fake operation.
type Result[T any] struct {
	Value T
	Err   error
}

// DriverScript appends typed FIFO results to a FakeDriver. Empty queues make
// their operation return its zero value and a nil error.
type DriverScript struct {
	Name                            []string
	Open                            []Result[struct{}]
	Close                           []Result[struct{}]
	DeviceInfo                      []Result[device.DeviceInfo]
	LaunchApp                       []Result[struct{}]
	StopApp                         []Result[struct{}]
	KillApp                         []Result[struct{}]
	ClearAppState                   []Result[struct{}]
	ClearKeychain                   []Result[struct{}]
	Tap                             []Result[struct{}]
	LongPress                       []Result[struct{}]
	PressKey                        []Result[struct{}]
	ContentDescriptor               []Result[device.TreeNode]
	ScrollVertical                  []Result[struct{}]
	IsKeyboardVisible               []Result[bool]
	Swipe                           []Result[struct{}]
	BackPress                       []Result[struct{}]
	InputText                       []Result[struct{}]
	OpenLink                        []Result[struct{}]
	HideKeyboard                    []Result[struct{}]
	TakeScreenshot                  []Result[[]byte]
	StartScreenRecording            []Result[device.CaptureID]
	SetLocation                     []Result[struct{}]
	SetOrientation                  []Result[struct{}]
	EraseText                       []Result[struct{}]
	SetProxy                        []Result[struct{}]
	ResetProxy                      []Result[struct{}]
	IsShutdown                      []Result[bool]
	WaitUntilScreenIsStatic         []Result[bool]
	WaitForAppToSettle              []Result[*device.ViewHierarchy]
	Capabilities                    []device.Capabilities
	SetPermissions                  []Result[struct{}]
	AddMedia                        []Result[struct{}]
	IsAirplaneModeEnabled           []Result[bool]
	SetAirplaneMode                 []Result[struct{}]
	SetAndroidChromeDevToolsEnabled []Result[struct{}]
	QueryOnDeviceElements           []Result[[]device.TreeNode]
	StartDeviceLogCapture           []Result[device.CaptureID]
	StopDeviceLogCapture            []Result[[]device.Artifact]
	CollectCrashArtifacts           []Result[[]device.Artifact]
}

// Action records one accepted driver call.
type Action struct {
	Sequence uint64
	Method   Method
	Request  any
}

// FakeDriver is a concurrency-safe, deterministic device test double.
type FakeDriver struct {
	mu      sync.Mutex
	next    uint64
	actions []Action
	plan    DriverScript
}

var _ device.Driver = (*FakeDriver)(nil)

// NewFakeDriver returns a driver with empty scripts and a sequence starting at
// one.
func NewFakeDriver() *FakeDriver {
	return &FakeDriver{}
}

// Enqueue atomically appends a typed batch of scripted results.
func (f *FakeDriver) Enqueue(script DriverScript) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.plan.Name = append(f.plan.Name, script.Name...)
	f.plan.Open = append(f.plan.Open, script.Open...)
	f.plan.Close = append(f.plan.Close, script.Close...)
	f.plan.DeviceInfo = append(f.plan.DeviceInfo, script.DeviceInfo...)
	f.plan.LaunchApp = append(f.plan.LaunchApp, script.LaunchApp...)
	f.plan.StopApp = append(f.plan.StopApp, script.StopApp...)
	f.plan.KillApp = append(f.plan.KillApp, script.KillApp...)
	f.plan.ClearAppState = append(f.plan.ClearAppState, script.ClearAppState...)
	f.plan.ClearKeychain = append(f.plan.ClearKeychain, script.ClearKeychain...)
	f.plan.Tap = append(f.plan.Tap, script.Tap...)
	f.plan.LongPress = append(f.plan.LongPress, script.LongPress...)
	f.plan.PressKey = append(f.plan.PressKey, script.PressKey...)
	f.plan.ContentDescriptor = appendResults(f.plan.ContentDescriptor, script.ContentDescriptor)
	f.plan.ScrollVertical = append(f.plan.ScrollVertical, script.ScrollVertical...)
	f.plan.IsKeyboardVisible = append(f.plan.IsKeyboardVisible, script.IsKeyboardVisible...)
	f.plan.Swipe = append(f.plan.Swipe, script.Swipe...)
	f.plan.BackPress = append(f.plan.BackPress, script.BackPress...)
	f.plan.InputText = append(f.plan.InputText, script.InputText...)
	f.plan.OpenLink = append(f.plan.OpenLink, script.OpenLink...)
	f.plan.HideKeyboard = append(f.plan.HideKeyboard, script.HideKeyboard...)
	f.plan.TakeScreenshot = appendResults(f.plan.TakeScreenshot, script.TakeScreenshot)
	f.plan.StartScreenRecording = append(f.plan.StartScreenRecording, script.StartScreenRecording...)
	f.plan.SetLocation = append(f.plan.SetLocation, script.SetLocation...)
	f.plan.SetOrientation = append(f.plan.SetOrientation, script.SetOrientation...)
	f.plan.EraseText = append(f.plan.EraseText, script.EraseText...)
	f.plan.SetProxy = append(f.plan.SetProxy, script.SetProxy...)
	f.plan.ResetProxy = append(f.plan.ResetProxy, script.ResetProxy...)
	f.plan.IsShutdown = append(f.plan.IsShutdown, script.IsShutdown...)
	f.plan.WaitUntilScreenIsStatic = append(f.plan.WaitUntilScreenIsStatic, script.WaitUntilScreenIsStatic...)
	f.plan.WaitForAppToSettle = appendResults(f.plan.WaitForAppToSettle, script.WaitForAppToSettle)
	f.plan.Capabilities = appendValues(f.plan.Capabilities, script.Capabilities)
	f.plan.SetPermissions = append(f.plan.SetPermissions, script.SetPermissions...)
	f.plan.AddMedia = append(f.plan.AddMedia, script.AddMedia...)
	f.plan.IsAirplaneModeEnabled = append(f.plan.IsAirplaneModeEnabled, script.IsAirplaneModeEnabled...)
	f.plan.SetAirplaneMode = append(f.plan.SetAirplaneMode, script.SetAirplaneMode...)
	f.plan.SetAndroidChromeDevToolsEnabled = append(f.plan.SetAndroidChromeDevToolsEnabled, script.SetAndroidChromeDevToolsEnabled...)
	f.plan.QueryOnDeviceElements = appendResults(f.plan.QueryOnDeviceElements, script.QueryOnDeviceElements)
	f.plan.StartDeviceLogCapture = append(f.plan.StartDeviceLogCapture, script.StartDeviceLogCapture...)
	f.plan.StopDeviceLogCapture = appendResults(f.plan.StopDeviceLogCapture, script.StopDeviceLogCapture)
	f.plan.CollectCrashArtifacts = appendResults(f.plan.CollectCrashArtifacts, script.CollectCrashArtifacts)
}

// Actions returns an isolated snapshot of accepted calls.
func (f *FakeDriver) Actions() []Action {
	f.mu.Lock()
	defer f.mu.Unlock()
	cloned := make([]Action, len(f.actions))
	for index, action := range f.actions {
		cloned[index] = action
		cloned[index].Request = cloneRequest(action.Request)
	}
	return cloned
}

func (f *FakeDriver) Name() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked(MethodName, nil)
	if len(f.plan.Name) == 0 {
		return "FakeDriver"
	}
	name := f.plan.Name[0]
	f.plan.Name = f.plan.Name[1:]
	return name
}

func (f *FakeDriver) Open(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodOpen, nil, &f.plan.Open)
}

func (f *FakeDriver) Close(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodClose, nil, &f.plan.Close)
}

func (f *FakeDriver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	return invoke(ctx, f, MethodDeviceInfo, nil, &f.plan.DeviceInfo)
}

func (f *FakeDriver) LaunchApp(ctx context.Context, request device.LaunchAppRequest) error {
	return invokeVoid(ctx, f, MethodLaunchApp, request, &f.plan.LaunchApp)
}

func (f *FakeDriver) StopApp(ctx context.Context, request device.AppRequest) error {
	return invokeVoid(ctx, f, MethodStopApp, request, &f.plan.StopApp)
}

func (f *FakeDriver) KillApp(ctx context.Context, request device.AppRequest) error {
	return invokeVoid(ctx, f, MethodKillApp, request, &f.plan.KillApp)
}

func (f *FakeDriver) ClearAppState(ctx context.Context, request device.AppRequest) error {
	return invokeVoid(ctx, f, MethodClearAppState, request, &f.plan.ClearAppState)
}

func (f *FakeDriver) ClearKeychain(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodClearKeychain, nil, &f.plan.ClearKeychain)
}

func (f *FakeDriver) Tap(ctx context.Context, request device.TapRequest) error {
	return invokeVoid(ctx, f, MethodTap, request, &f.plan.Tap)
}

func (f *FakeDriver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	return invokeVoid(ctx, f, MethodLongPress, request, &f.plan.LongPress)
}

func (f *FakeDriver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	return invokeVoid(ctx, f, MethodPressKey, request, &f.plan.PressKey)
}

func (f *FakeDriver) ContentDescriptor(ctx context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	return invoke(ctx, f, MethodContentDescriptor, request, &f.plan.ContentDescriptor)
}

func (f *FakeDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	return invokeVoid(ctx, f, MethodScrollVertical, request, &f.plan.ScrollVertical)
}

func (f *FakeDriver) IsKeyboardVisible(ctx context.Context, request device.KeyboardRequest) (bool, error) {
	return invoke(ctx, f, MethodIsKeyboardVisible, request, &f.plan.IsKeyboardVisible)
}

func (f *FakeDriver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	return invokeVoid(ctx, f, MethodSwipe, request, &f.plan.Swipe)
}

func (f *FakeDriver) BackPress(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodBackPress, nil, &f.plan.BackPress)
}

func (f *FakeDriver) InputText(ctx context.Context, request device.InputTextRequest) error {
	return invokeVoid(ctx, f, MethodInputText, request, &f.plan.InputText)
}

func (f *FakeDriver) OpenLink(ctx context.Context, request device.OpenLinkRequest) error {
	return invokeVoid(ctx, f, MethodOpenLink, request, &f.plan.OpenLink)
}

func (f *FakeDriver) HideKeyboard(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodHideKeyboard, nil, &f.plan.HideKeyboard)
}

func (f *FakeDriver) TakeScreenshot(ctx context.Context, request device.ScreenshotRequest) ([]byte, error) {
	return invoke(ctx, f, MethodTakeScreenshot, request, &f.plan.TakeScreenshot)
}

func (f *FakeDriver) StartScreenRecording(ctx context.Context, request device.ScreenRecordingRequest) (device.CaptureID, error) {
	return invoke(ctx, f, MethodStartScreenRecording, request, &f.plan.StartScreenRecording)
}

func (f *FakeDriver) SetLocation(ctx context.Context, request device.Location) error {
	return invokeVoid(ctx, f, MethodSetLocation, request, &f.plan.SetLocation)
}

func (f *FakeDriver) SetOrientation(ctx context.Context, request device.Orientation) error {
	return invokeVoid(ctx, f, MethodSetOrientation, request, &f.plan.SetOrientation)
}

func (f *FakeDriver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	return invokeVoid(ctx, f, MethodEraseText, request, &f.plan.EraseText)
}

func (f *FakeDriver) SetProxy(ctx context.Context, request device.Proxy) error {
	return invokeVoid(ctx, f, MethodSetProxy, request, &f.plan.SetProxy)
}

func (f *FakeDriver) ResetProxy(ctx context.Context) error {
	return invokeVoid(ctx, f, MethodResetProxy, nil, &f.plan.ResetProxy)
}

func (f *FakeDriver) IsShutdown(ctx context.Context) (bool, error) {
	return invoke(ctx, f, MethodIsShutdown, nil, &f.plan.IsShutdown)
}

func (f *FakeDriver) WaitUntilScreenIsStatic(ctx context.Context, request device.ScreenStaticRequest) (bool, error) {
	return invoke(ctx, f, MethodWaitUntilScreenIsStatic, request, &f.plan.WaitUntilScreenIsStatic)
}

func (f *FakeDriver) WaitForAppToSettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	return invoke(ctx, f, MethodWaitForAppToSettle, request, &f.plan.WaitForAppToSettle)
}

func (f *FakeDriver) Capabilities() device.Capabilities {
	return invokeWithoutContext(f, MethodCapabilities, nil, &f.plan.Capabilities)
}

func (f *FakeDriver) SetPermissions(ctx context.Context, request device.PermissionsRequest) error {
	return invokeVoid(ctx, f, MethodSetPermissions, request, &f.plan.SetPermissions)
}

func (f *FakeDriver) AddMedia(ctx context.Context, request device.AddMediaRequest) error {
	return invokeVoid(ctx, f, MethodAddMedia, request, &f.plan.AddMedia)
}

func (f *FakeDriver) IsAirplaneModeEnabled(ctx context.Context) (bool, error) {
	return invoke(ctx, f, MethodIsAirplaneModeEnabled, nil, &f.plan.IsAirplaneModeEnabled)
}

func (f *FakeDriver) SetAirplaneMode(ctx context.Context, request device.AirplaneModeRequest) error {
	return invokeVoid(ctx, f, MethodSetAirplaneMode, request, &f.plan.SetAirplaneMode)
}

func (f *FakeDriver) SetAndroidChromeDevToolsEnabled(ctx context.Context, request device.ChromeDevToolsRequest) error {
	return invokeVoid(ctx, f, MethodSetAndroidChromeDevToolsEnabled, request, &f.plan.SetAndroidChromeDevToolsEnabled)
}

func (f *FakeDriver) QueryOnDeviceElements(ctx context.Context, request device.QueryRequest) ([]device.TreeNode, error) {
	return invoke(ctx, f, MethodQueryOnDeviceElements, request, &f.plan.QueryOnDeviceElements)
}

func (f *FakeDriver) StartDeviceLogCapture(ctx context.Context, request device.DeviceLogRequest) (device.CaptureID, error) {
	return invoke(ctx, f, MethodStartDeviceLogCapture, request, &f.plan.StartDeviceLogCapture)
}

func (f *FakeDriver) StopDeviceLogCapture(ctx context.Context, request device.CaptureID) ([]device.Artifact, error) {
	return invoke(ctx, f, MethodStopDeviceLogCapture, request, &f.plan.StopDeviceLogCapture)
}

func (f *FakeDriver) CollectCrashArtifacts(ctx context.Context, request device.ArtifactRequest) ([]device.Artifact, error) {
	return invoke(ctx, f, MethodCollectCrashArtifacts, request, &f.plan.CollectCrashArtifacts)
}

func invokeVoid(ctx context.Context, f *FakeDriver, method Method, request any, plan *[]Result[struct{}]) error {
	_, err := invoke(ctx, f, method, request, plan)
	return err
}

func invoke[T any](ctx context.Context, f *FakeDriver, method Method, request any, plan *[]Result[T]) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	f.recordLocked(method, request)
	if len(*plan) == 0 {
		return zero, nil
	}
	result := (*plan)[0]
	*plan = (*plan)[1:]
	return cloneValue(result.Value), result.Err
}

func invokeWithoutContext[T any](f *FakeDriver, method Method, request any, plan *[]T) T {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordLocked(method, request)
	if len(*plan) == 0 {
		var zero T
		return zero
	}
	value := (*plan)[0]
	*plan = (*plan)[1:]
	return cloneValue(value)
}

func (f *FakeDriver) recordLocked(method Method, request any) {
	f.next++
	f.actions = append(f.actions, Action{Sequence: f.next, Method: method, Request: cloneRequest(request)})
}

func cloneRequest(request any) any {
	switch typed := request.(type) {
	case device.LaunchAppRequest:
		typed.Arguments = append([]device.LaunchArgument(nil), typed.Arguments...)
		return typed
	case device.PressKeyRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	case device.ContentDescriptorRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	case device.ScrollVerticalRequest:
		typed.ElementPoint = clonePoint(typed.ElementPoint)
		return typed
	case device.KeyboardRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	case device.SwipeRequest:
		typed.Start = clonePoint(typed.Start)
		typed.End = clonePoint(typed.End)
		typed.ElementPoint = clonePoint(typed.ElementPoint)
		return typed
	case device.InputTextRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	case device.EraseTextRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	case device.SettleRequest:
		typed.InitialHierarchy = cloneViewHierarchy(typed.InitialHierarchy)
		typed.TimeoutMillis = cloneInt64(typed.TimeoutMillis)
		return typed
	case device.PermissionsRequest:
		if typed.Permissions != nil {
			permissions := make(map[string]string, len(typed.Permissions))
			for name, value := range typed.Permissions {
				permissions[name] = value
			}
			typed.Permissions = permissions
		}
		return typed
	case device.AddMediaRequest:
		typed.Files = append([]device.MediaFile(nil), typed.Files...)
		return typed
	case device.QueryRequest:
		typed.AppIDs = append([]string(nil), typed.AppIDs...)
		return typed
	default:
		return request
	}
}

func clonePoint(value *device.Point) *device.Point {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func appendResults[T any](destination []Result[T], source []Result[T]) []Result[T] {
	for _, result := range source {
		result.Value = cloneValue(result.Value)
		destination = append(destination, result)
	}
	return destination
}

func appendValues[T any](destination []T, source []T) []T {
	for _, value := range source {
		destination = append(destination, cloneValue(value))
	}
	return destination
}

func cloneValue[T any](value T) T {
	switch typed := any(value).(type) {
	case []byte:
		return any(append([]byte(nil), typed...)).(T)
	case device.TreeNode:
		return any(cloneTreeNode(typed)).(T)
	case *device.ViewHierarchy:
		return any(cloneViewHierarchy(typed)).(T)
	case []device.TreeNode:
		return any(cloneTreeNodes(typed)).(T)
	case []device.Artifact:
		return any(cloneArtifacts(typed)).(T)
	case device.Capabilities:
		return any(cloneCapabilities(typed)).(T)
	}
	return value
}

func cloneCapabilities(value device.Capabilities) device.Capabilities {
	cloned := value
	if value.Features != nil {
		cloned.Features = make(map[string]bool, len(value.Features))
		for feature, supported := range value.Features {
			cloned.Features[feature] = supported
		}
	}
	return cloned
}

func cloneArtifacts(values []device.Artifact) []device.Artifact {
	if values == nil {
		return nil
	}
	cloned := make([]device.Artifact, len(values))
	for index, artifact := range values {
		cloned[index] = artifact
		if artifact.Metadata != nil {
			cloned[index].Metadata = make(map[string]string, len(artifact.Metadata))
			for key, value := range artifact.Metadata {
				cloned[index].Metadata[key] = value
			}
		}
	}
	return cloned
}

func cloneViewHierarchy(value *device.ViewHierarchy) *device.ViewHierarchy {
	if value == nil {
		return nil
	}
	return &device.ViewHierarchy{Root: cloneTreeNode(value.Root)}
}

func cloneTreeNodes(values []device.TreeNode) []device.TreeNode {
	if values == nil {
		return nil
	}
	cloned := make([]device.TreeNode, len(values))
	for index := range values {
		cloned[index] = cloneTreeNode(values[index])
	}
	return cloned
}

func cloneTreeNode(value device.TreeNode) device.TreeNode {
	cloned := value
	if value.Attributes != nil {
		cloned.Attributes = make(map[string]string, len(value.Attributes))
		for key, attribute := range value.Attributes {
			cloned.Attributes[key] = attribute
		}
	}
	cloned.Children = cloneTreeNodes(value.Children)
	cloned.Clickable = cloneBool(value.Clickable)
	cloned.Enabled = cloneBool(value.Enabled)
	cloned.Focused = cloneBool(value.Focused)
	cloned.Checked = cloneBool(value.Checked)
	cloned.Selected = cloneBool(value.Selected)
	return cloned
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
