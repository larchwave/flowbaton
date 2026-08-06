package enginetest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

func TestFakeDriverHasStableDefaultName(t *testing.T) {
	t.Parallel()

	driver := NewFakeDriver()
	if got, want := driver.Name(), "FakeDriver"; got != want {
		t.Fatalf("Name = %q, want %q", got, want)
	}
	actions := driver.Actions()
	if len(actions) != 1 || actions[0].Method != MethodName || actions[0].Sequence != 1 {
		t.Fatalf("Name actions = %#v, want one sequenced Name call", actions)
	}
}

func TestFakeDriverRejectsCancelledCallBeforeTraceOrScriptConsumption(t *testing.T) {
	t.Parallel()

	scriptedErr := errors.New("scripted open failure")
	driver := NewFakeDriver()
	driver.Enqueue(DriverScript{
		Open: []Result[struct{}]{{Err: scriptedErr}},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := driver.Open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Open error = %v, want context.Canceled", err)
	}
	if got := driver.Actions(); len(got) != 0 {
		t.Fatalf("cancelled Open actions = %#v, want none", got)
	}

	if err := driver.Open(context.Background()); !errors.Is(err, scriptedErr) {
		t.Fatalf("subsequent Open error = %v, want scripted error", err)
	}
	actions := driver.Actions()
	if len(actions) != 1 {
		t.Fatalf("Open actions = %#v, want one", actions)
	}
	if actions[0].Sequence != 1 || actions[0].Method != MethodOpen || actions[0].Request != nil {
		t.Fatalf("Open action = %#v, want sequence 1 method %q and nil request", actions[0], MethodOpen)
	}
}

func TestFakeDriverCopiesScriptedBytesOnEnqueueAndReturn(t *testing.T) {
	t.Parallel()

	screenshot := []byte("png")
	driver := NewFakeDriver()
	driver.Enqueue(DriverScript{
		TakeScreenshot: []Result[[]byte]{
			{Value: screenshot},
			{Value: screenshot},
		},
	})
	screenshot[0] = 'x'

	first, err := driver.TakeScreenshot(context.Background(), device.ScreenshotRequest{})
	if err != nil {
		t.Fatalf("first TakeScreenshot error: %v", err)
	}
	if got, want := string(first), "png"; got != want {
		t.Fatalf("first screenshot = %q, want %q", got, want)
	}
	first[0] = 'y'

	second, err := driver.TakeScreenshot(context.Background(), device.ScreenshotRequest{})
	if err != nil {
		t.Fatalf("second TakeScreenshot error: %v", err)
	}
	if got, want := string(second), "png"; got != want {
		t.Fatalf("second screenshot = %q, want %q", got, want)
	}
}

func TestFakeDriverCopiesScriptedHierarchiesOnEnqueueAndReturn(t *testing.T) {
	t.Parallel()

	node := device.TreeNode{
		Attributes: map[string]string{"text": "ready"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{"text": "child"},
		}},
	}
	hierarchy := &device.ViewHierarchy{Root: node}
	driver := NewFakeDriver()
	driver.Enqueue(DriverScript{
		ContentDescriptor:  []Result[device.TreeNode]{{Value: node}, {Value: node}},
		WaitForAppToSettle: []Result[*device.ViewHierarchy]{{Value: hierarchy}, {Value: hierarchy}},
		QueryOnDeviceElements: []Result[[]device.TreeNode]{
			{Value: []device.TreeNode{node}},
			{Value: []device.TreeNode{node}},
		},
	})
	node.Attributes["text"] = "caller-mutated"
	node.Children[0].Attributes["text"] = "caller-mutated-child"

	ctx := context.Background()
	firstNode, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("first ContentDescriptor error: %v", err)
	}
	assertTreeTexts(t, firstNode, "ready", "child")
	firstNode.Attributes["text"] = "returned-mutated"
	firstNode.Children[0].Attributes["text"] = "returned-mutated-child"
	secondNode, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
	if err != nil {
		t.Fatalf("second ContentDescriptor error: %v", err)
	}
	assertTreeTexts(t, secondNode, "ready", "child")

	firstHierarchy, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if err != nil {
		t.Fatalf("first WaitForAppToSettle error: %v", err)
	}
	assertTreeTexts(t, firstHierarchy.Root, "ready", "child")
	firstHierarchy.Root.Attributes["text"] = "returned-mutated"
	secondHierarchy, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	if err != nil {
		t.Fatalf("second WaitForAppToSettle error: %v", err)
	}
	assertTreeTexts(t, secondHierarchy.Root, "ready", "child")

	firstQuery, err := driver.QueryOnDeviceElements(ctx, device.QueryRequest{})
	if err != nil {
		t.Fatalf("first QueryOnDeviceElements error: %v", err)
	}
	assertTreeTexts(t, firstQuery[0], "ready", "child")
	firstQuery[0].Attributes["text"] = "returned-mutated"
	secondQuery, err := driver.QueryOnDeviceElements(ctx, device.QueryRequest{})
	if err != nil {
		t.Fatalf("second QueryOnDeviceElements error: %v", err)
	}
	assertTreeTexts(t, secondQuery[0], "ready", "child")
}

func assertTreeTexts(t *testing.T, node device.TreeNode, rootText string, childText string) {
	t.Helper()
	if got := node.Attributes["text"]; got != rootText {
		t.Fatalf("root text = %q, want %q", got, rootText)
	}
	if len(node.Children) != 1 {
		t.Fatalf("children = %#v, want one", node.Children)
	}
	if got := node.Children[0].Attributes["text"]; got != childText {
		t.Fatalf("child text = %q, want %q", got, childText)
	}
}

func TestFakeDriverCopiesScriptedArtifactsAndCapabilities(t *testing.T) {
	t.Parallel()

	artifacts := []device.Artifact{{
		Kind:     "log",
		Path:     "device.log",
		Metadata: map[string]string{"source": "driver"},
	}}
	capabilities := device.Capabilities{
		Platform: device.Platform("android"),
		Features: map[string]bool{"tap": true},
	}
	driver := NewFakeDriver()
	driver.Enqueue(DriverScript{
		StopDeviceLogCapture:  []Result[[]device.Artifact]{{Value: artifacts}, {Value: artifacts}},
		CollectCrashArtifacts: []Result[[]device.Artifact]{{Value: artifacts}, {Value: artifacts}},
		Capabilities:          []device.Capabilities{capabilities, capabilities},
	})
	artifacts[0].Metadata["source"] = "caller-mutated"
	capabilities.Features["tap"] = false

	ctx := context.Background()
	firstArtifacts, err := driver.StopDeviceLogCapture(ctx, device.CaptureID("capture"))
	if err != nil {
		t.Fatalf("first StopDeviceLogCapture error: %v", err)
	}
	assertArtifactSource(t, firstArtifacts, "driver")
	firstArtifacts[0].Metadata["source"] = "returned-mutated"
	secondArtifacts, err := driver.StopDeviceLogCapture(ctx, device.CaptureID("capture"))
	if err != nil {
		t.Fatalf("second StopDeviceLogCapture error: %v", err)
	}
	assertArtifactSource(t, secondArtifacts, "driver")

	firstCrashArtifacts, err := driver.CollectCrashArtifacts(ctx, device.ArtifactRequest{})
	if err != nil {
		t.Fatalf("first CollectCrashArtifacts error: %v", err)
	}
	assertArtifactSource(t, firstCrashArtifacts, "driver")
	firstCrashArtifacts[0].Metadata["source"] = "returned-mutated"
	secondCrashArtifacts, err := driver.CollectCrashArtifacts(ctx, device.ArtifactRequest{})
	if err != nil {
		t.Fatalf("second CollectCrashArtifacts error: %v", err)
	}
	assertArtifactSource(t, secondCrashArtifacts, "driver")

	firstCapabilities := driver.Capabilities()
	if !firstCapabilities.Features["tap"] {
		t.Fatalf("first capabilities = %#v, want tap enabled", firstCapabilities)
	}
	firstCapabilities.Features["tap"] = false
	secondCapabilities := driver.Capabilities()
	if !secondCapabilities.Features["tap"] {
		t.Fatalf("second capabilities = %#v, want tap enabled", secondCapabilities)
	}
}

func assertArtifactSource(t *testing.T, artifacts []device.Artifact, want string) {
	t.Helper()
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v, want one", artifacts)
	}
	if got := artifacts[0].Metadata["source"]; got != want {
		t.Fatalf("artifact source = %q, want %q", got, want)
	}
}

func TestFakeDriverCopiesRecordedRequestsAndActionSnapshots(t *testing.T) {
	t.Parallel()

	permissions := map[string]string{"camera": "allow"}
	hierarchy := &device.ViewHierarchy{Root: device.TreeNode{
		Attributes: map[string]string{"text": "ready"},
	}}
	timeout := int64(500)
	driver := NewFakeDriver()
	ctx := context.Background()
	if err := driver.SetPermissions(ctx, device.PermissionsRequest{
		AppID:       "com.example",
		Permissions: permissions,
	}); err != nil {
		t.Fatalf("SetPermissions error: %v", err)
	}
	if _, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{
		InitialHierarchy: hierarchy,
		TimeoutMillis:    &timeout,
	}); err != nil {
		t.Fatalf("WaitForAppToSettle error: %v", err)
	}
	permissions["camera"] = "deny"
	hierarchy.Root.Attributes["text"] = "caller-mutated"
	timeout = 900

	actions := driver.Actions()
	recordedPermissions := actions[0].Request.(device.PermissionsRequest)
	if got, want := recordedPermissions.Permissions["camera"], "allow"; got != want {
		t.Fatalf("recorded permission = %q, want %q", got, want)
	}
	recordedSettle := actions[1].Request.(device.SettleRequest)
	if got, want := recordedSettle.InitialHierarchy.Root.Attributes["text"], "ready"; got != want {
		t.Fatalf("recorded hierarchy text = %q, want %q", got, want)
	}
	if got, want := *recordedSettle.TimeoutMillis, int64(500); got != want {
		t.Fatalf("recorded timeout = %d, want %d", got, want)
	}

	recordedPermissions.Permissions["camera"] = "snapshot-mutated"
	recordedSettle.InitialHierarchy.Root.Attributes["text"] = "snapshot-mutated"
	*recordedSettle.TimeoutMillis = 1200
	actions = driver.Actions()
	recordedPermissions = actions[0].Request.(device.PermissionsRequest)
	recordedSettle = actions[1].Request.(device.SettleRequest)
	if got := recordedPermissions.Permissions["camera"]; got != "allow" {
		t.Fatalf("second snapshot permission = %q, want allow", got)
	}
	if got := recordedSettle.InitialHierarchy.Root.Attributes["text"]; got != "ready" {
		t.Fatalf("second snapshot hierarchy text = %q, want ready", got)
	}
	if got := *recordedSettle.TimeoutMillis; got != 500 {
		t.Fatalf("second snapshot timeout = %d, want 500", got)
	}
}

func TestFakeDriverScriptsAndTracesEveryDriverMethod(t *testing.T) {
	t.Parallel()

	node := device.TreeNode{Attributes: map[string]string{"text": "ready"}}
	hierarchy := &device.ViewHierarchy{Root: node}
	artifact := device.Artifact{Kind: "log", Path: "device.log"}
	driver := NewFakeDriver()
	driver.Enqueue(DriverScript{
		Name:                            []string{"scripted-driver"},
		Open:                            oneVoidResult(),
		Close:                           oneVoidResult(),
		DeviceInfo:                      []Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android")}}},
		LaunchApp:                       oneVoidResult(),
		StopApp:                         oneVoidResult(),
		KillApp:                         oneVoidResult(),
		ClearAppState:                   oneVoidResult(),
		ClearKeychain:                   oneVoidResult(),
		Tap:                             oneVoidResult(),
		LongPress:                       oneVoidResult(),
		PressKey:                        oneVoidResult(),
		ContentDescriptor:               []Result[device.TreeNode]{{Value: node}},
		ScrollVertical:                  oneVoidResult(),
		IsKeyboardVisible:               []Result[bool]{{Value: true}},
		Swipe:                           oneVoidResult(),
		BackPress:                       oneVoidResult(),
		InputText:                       oneVoidResult(),
		OpenLink:                        oneVoidResult(),
		HideKeyboard:                    oneVoidResult(),
		TakeScreenshot:                  []Result[[]byte]{{Value: []byte("png")}},
		StartScreenRecording:            []Result[device.CaptureID]{{Value: device.CaptureID("screen")}},
		SetLocation:                     oneVoidResult(),
		SetOrientation:                  oneVoidResult(),
		EraseText:                       oneVoidResult(),
		SetProxy:                        oneVoidResult(),
		ResetProxy:                      oneVoidResult(),
		IsShutdown:                      []Result[bool]{{Value: true}},
		WaitUntilScreenIsStatic:         []Result[bool]{{Value: true}},
		WaitForAppToSettle:              []Result[*device.ViewHierarchy]{{Value: hierarchy}},
		Capabilities:                    []device.Capabilities{{Platform: device.Platform("android")}},
		SetPermissions:                  oneVoidResult(),
		AddMedia:                        oneVoidResult(),
		IsAirplaneModeEnabled:           []Result[bool]{{Value: true}},
		SetAirplaneMode:                 oneVoidResult(),
		SetAndroidChromeDevToolsEnabled: oneVoidResult(),
		QueryOnDeviceElements:           []Result[[]device.TreeNode]{{Value: []device.TreeNode{node}}},
		StartDeviceLogCapture:           []Result[device.CaptureID]{{Value: device.CaptureID("log")}},
		StopDeviceLogCapture:            []Result[[]device.Artifact]{{Value: []device.Artifact{artifact}}},
		CollectCrashArtifacts:           []Result[[]device.Artifact]{{Value: []device.Artifact{artifact}}},
	})

	ctx := context.Background()
	if got := driver.Name(); got != "scripted-driver" {
		t.Fatalf("Name = %q, want scripted-driver", got)
	}
	mustNoError(t, driver.Open(ctx), "Open")
	mustNoError(t, driver.Close(ctx), "Close")
	info, err := driver.DeviceInfo(ctx)
	mustNoError(t, err, "DeviceInfo")
	if info.Platform != device.Platform("android") {
		t.Fatalf("DeviceInfo = %#v, want android", info)
	}
	mustNoError(t, driver.LaunchApp(ctx, device.LaunchAppRequest{}), "LaunchApp")
	mustNoError(t, driver.StopApp(ctx, device.AppRequest{}), "StopApp")
	mustNoError(t, driver.KillApp(ctx, device.AppRequest{}), "KillApp")
	mustNoError(t, driver.ClearAppState(ctx, device.AppRequest{}), "ClearAppState")
	mustNoError(t, driver.ClearKeychain(ctx), "ClearKeychain")
	mustNoError(t, driver.Tap(ctx, device.TapRequest{}), "Tap")
	mustNoError(t, driver.LongPress(ctx, device.LongPressRequest{}), "LongPress")
	mustNoError(t, driver.PressKey(ctx, device.PressKeyRequest{}), "PressKey")
	descriptor, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{})
	mustNoError(t, err, "ContentDescriptor")
	if descriptor.Attributes["text"] != "ready" {
		t.Fatalf("ContentDescriptor = %#v, want ready node", descriptor)
	}
	mustNoError(t, driver.ScrollVertical(ctx, device.ScrollVerticalRequest{}), "ScrollVertical")
	keyboardVisible, err := driver.IsKeyboardVisible(ctx, device.KeyboardRequest{})
	mustNoError(t, err, "IsKeyboardVisible")
	if !keyboardVisible {
		t.Fatal("IsKeyboardVisible = false, want true")
	}
	mustNoError(t, driver.Swipe(ctx, device.SwipeRequest{}), "Swipe")
	mustNoError(t, driver.BackPress(ctx), "BackPress")
	mustNoError(t, driver.InputText(ctx, device.InputTextRequest{}), "InputText")
	mustNoError(t, driver.OpenLink(ctx, device.OpenLinkRequest{}), "OpenLink")
	mustNoError(t, driver.HideKeyboard(ctx), "HideKeyboard")
	screenshot, err := driver.TakeScreenshot(ctx, device.ScreenshotRequest{})
	mustNoError(t, err, "TakeScreenshot")
	if string(screenshot) != "png" {
		t.Fatalf("TakeScreenshot = %q, want png", screenshot)
	}
	screenCapture, err := driver.StartScreenRecording(ctx, device.ScreenRecordingRequest{})
	mustNoError(t, err, "StartScreenRecording")
	if screenCapture != device.CaptureID("screen") {
		t.Fatalf("StartScreenRecording = %q, want screen", screenCapture)
	}
	mustNoError(t, driver.SetLocation(ctx, device.Location{}), "SetLocation")
	mustNoError(t, driver.SetOrientation(ctx, device.Orientation("portrait")), "SetOrientation")
	mustNoError(t, driver.EraseText(ctx, device.EraseTextRequest{}), "EraseText")
	mustNoError(t, driver.SetProxy(ctx, device.Proxy{}), "SetProxy")
	mustNoError(t, driver.ResetProxy(ctx), "ResetProxy")
	shutdown, err := driver.IsShutdown(ctx)
	mustNoError(t, err, "IsShutdown")
	if !shutdown {
		t.Fatal("IsShutdown = false, want true")
	}
	static, err := driver.WaitUntilScreenIsStatic(ctx, device.ScreenStaticRequest{})
	mustNoError(t, err, "WaitUntilScreenIsStatic")
	if !static {
		t.Fatal("WaitUntilScreenIsStatic = false, want true")
	}
	settled, err := driver.WaitForAppToSettle(ctx, device.SettleRequest{})
	mustNoError(t, err, "WaitForAppToSettle")
	if settled == nil || settled.Root.Attributes["text"] != "ready" {
		t.Fatalf("WaitForAppToSettle = %#v, want ready hierarchy", settled)
	}
	if got := driver.Capabilities().Platform; got != device.Platform("android") {
		t.Fatalf("Capabilities platform = %q, want android", got)
	}
	mustNoError(t, driver.SetPermissions(ctx, device.PermissionsRequest{}), "SetPermissions")
	mustNoError(t, driver.AddMedia(ctx, device.AddMediaRequest{}), "AddMedia")
	airplaneMode, err := driver.IsAirplaneModeEnabled(ctx)
	mustNoError(t, err, "IsAirplaneModeEnabled")
	if !airplaneMode {
		t.Fatal("IsAirplaneModeEnabled = false, want true")
	}
	mustNoError(t, driver.SetAirplaneMode(ctx, device.AirplaneModeRequest{}), "SetAirplaneMode")
	mustNoError(t, driver.SetAndroidChromeDevToolsEnabled(ctx, device.ChromeDevToolsRequest{}), "SetAndroidChromeDevToolsEnabled")
	queryNodes, err := driver.QueryOnDeviceElements(ctx, device.QueryRequest{})
	mustNoError(t, err, "QueryOnDeviceElements")
	if len(queryNodes) != 1 || queryNodes[0].Attributes["text"] != "ready" {
		t.Fatalf("QueryOnDeviceElements = %#v, want ready node", queryNodes)
	}
	logCapture, err := driver.StartDeviceLogCapture(ctx, device.DeviceLogRequest{})
	mustNoError(t, err, "StartDeviceLogCapture")
	if logCapture != device.CaptureID("log") {
		t.Fatalf("StartDeviceLogCapture = %q, want log", logCapture)
	}
	logArtifacts, err := driver.StopDeviceLogCapture(ctx, logCapture)
	mustNoError(t, err, "StopDeviceLogCapture")
	if len(logArtifacts) != 1 || logArtifacts[0].Path != "device.log" {
		t.Fatalf("StopDeviceLogCapture = %#v, want device.log", logArtifacts)
	}
	crashArtifacts, err := driver.CollectCrashArtifacts(ctx, device.ArtifactRequest{})
	mustNoError(t, err, "CollectCrashArtifacts")
	if len(crashArtifacts) != 1 || crashArtifacts[0].Path != "device.log" {
		t.Fatalf("CollectCrashArtifacts = %#v, want device.log", crashArtifacts)
	}

	wantMethods := []Method{
		MethodName, MethodOpen, MethodClose, MethodDeviceInfo, MethodLaunchApp,
		MethodStopApp, MethodKillApp, MethodClearAppState, MethodClearKeychain,
		MethodTap, MethodLongPress, MethodPressKey, MethodContentDescriptor,
		MethodScrollVertical, MethodIsKeyboardVisible, MethodSwipe, MethodBackPress,
		MethodInputText, MethodOpenLink, MethodHideKeyboard, MethodTakeScreenshot,
		MethodStartScreenRecording, MethodSetLocation, MethodSetOrientation,
		MethodEraseText, MethodSetProxy, MethodResetProxy, MethodIsShutdown,
		MethodWaitUntilScreenIsStatic, MethodWaitForAppToSettle, MethodCapabilities,
		MethodSetPermissions, MethodAddMedia, MethodIsAirplaneModeEnabled,
		MethodSetAirplaneMode, MethodSetAndroidChromeDevToolsEnabled,
		MethodQueryOnDeviceElements, MethodStartDeviceLogCapture,
		MethodStopDeviceLogCapture, MethodCollectCrashArtifacts,
	}
	actions := driver.Actions()
	if len(actions) != len(wantMethods) {
		t.Fatalf("action count = %d, want %d: %#v", len(actions), len(wantMethods), actions)
	}
	for index, action := range actions {
		if action.Sequence != uint64(index+1) || action.Method != wantMethods[index] {
			t.Fatalf("action[%d] = %#v, want sequence %d method %q", index, action, index+1, wantMethods[index])
		}
	}
}

func oneVoidResult() []Result[struct{}] {
	return []Result[struct{}]{{}}
}

func mustNoError(t *testing.T, err error, method string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error: %v", method, err)
	}
}

func TestFakeDriverSerializesConcurrentCallsAndSnapshots(t *testing.T) {
	t.Parallel()

	const calls = 128
	driver := NewFakeDriver()
	script := DriverScript{Tap: make([]Result[struct{}], calls)}
	driver.Enqueue(script)

	start := make(chan struct{})
	errorsSeen := make(chan error, calls)
	var workers sync.WaitGroup
	workers.Add(calls + 1)
	for index := range calls {
		go func(index int) {
			defer workers.Done()
			<-start
			errorsSeen <- driver.Tap(context.Background(), device.TapRequest{
				Point: device.Point{X: float64(index), Y: float64(index)},
			})
		}(index)
	}
	go func() {
		defer workers.Done()
		<-start
		for range calls {
			_ = driver.Actions()
		}
	}()
	close(start)
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Tap error: %v", err)
		}
	}

	actions := driver.Actions()
	if len(actions) != calls {
		t.Fatalf("concurrent action count = %d, want %d", len(actions), calls)
	}
	for index, action := range actions {
		if action.Sequence != uint64(index+1) || action.Method != MethodTap {
			t.Fatalf("action[%d] = %#v, want sequence %d Tap", index, action, index+1)
		}
	}
}
