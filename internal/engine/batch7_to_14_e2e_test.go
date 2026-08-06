package engine

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// TestAuthoredYAMLExecutesAllExtendedCommands runs the complete authored fixture
// through parser, capability preflight, registry, handlers, and device services.
func TestAuthoredYAMLExecutesAllExtendedCommands(t *testing.T) {
	t.Parallel()

	program := batch714Prepare(t)
	driver := batch714Driver()
	sink := &recordingArtifactSink{}
	controller := &recordingControllerStub{artifacts: []device.Artifact{{Kind: "recording", Path: "/captures/session.mp4"}}}
	reader := &batch714ResourceReader{}
	checker := &stubImageChecker{}

	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "batch714-e2e", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		ArtifactSink: sink, RecordingController: controller,
		ResourceReader: reader, ImageChecker: checker,
	})
	if err != nil {
		t.Fatalf("Execute(batch7-14 happy.yaml) error = %T %v", err, err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want exactly one root result", len(results))
	}
	if got := results[0].Outcome(); got != Completed {
		t.Fatalf("outcome = %s, want %s", got, Completed)
	}

	// Every command in the fixture must have run: a skipped or absent command
	// would otherwise leave its keyword silently unexercised.
	commands := results[0].Commands()
	if len(commands) != len(batch714Keywords) {
		t.Fatalf("executed commands = %d, want %d", len(commands), len(batch714Keywords))
	}
	for index, command := range commands {
		if got := command.Command().Kind; got != batch714Keywords[index] {
			t.Fatalf("command %d = %s, want %s", index, got, batch714Keywords[index])
		}
		if got := command.Outcome(); got != Completed {
			t.Fatalf("command %s outcome = %s, want %s", got, command.Outcome(), Completed)
		}
	}
}

func TestBatches7To14ReachTheirExactDriverAndServiceBoundaries(t *testing.T) {
	t.Parallel()

	program := batch714Prepare(t)
	driver := batch714Driver()
	sink := &recordingArtifactSink{}
	controller := &recordingControllerStub{artifacts: []device.Artifact{{Kind: "recording", Path: "/captures/session.mp4"}}}
	reader := &batch714ResourceReader{}
	checker := &stubImageChecker{}

	if _, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "batch714-boundaries", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		ArtifactSink: sink, RecordingController: controller,
		ResourceReader: reader, ImageChecker: checker,
	}); err != nil {
		t.Fatalf("Execute(batch7-14 happy.yaml) error = %T %v", err, err)
	}

	// The driver-side half. travel dominates the SetLocation count: one
	// undelayed start emission plus fifty steps, on top of setLocation's one.
	for _, test := range []struct {
		method enginetest.Method
		want   int
	}{
		{method: enginetest.MethodStopApp, want: 1},
		{method: enginetest.MethodKillApp, want: 1},
		{method: enginetest.MethodClearAppState, want: 1},
		{method: enginetest.MethodClearKeychain, want: 1},
		{method: enginetest.MethodOpenLink, want: 2},
		{method: enginetest.MethodSetLocation, want: 1 + 1 + travelStepsPerSegment},
		{method: enginetest.MethodSetOrientation, want: 1},
		{method: enginetest.MethodSetPermissions, want: 1},
		{method: enginetest.MethodSetAirplaneMode, want: 2},
		{method: enginetest.MethodIsAirplaneModeEnabled, want: 1},
		{method: enginetest.MethodAddMedia, want: 1},
	} {
		if got := batch714DriverCalls(driver, test.method); got != test.want {
			t.Fatalf("%s calls = %d, want %d", test.method, got, test.want)
		}
	}

	// The service-side half, which no driver method can witness.
	if len(controller.started) != 1 || controller.stops != 1 {
		t.Fatalf("recording lifecycle = %d starts / %d stops, want exactly one of each",
			len(controller.started), controller.stops)
	}
	// takeScreenshot writes one artifact; stopRecording finalizes the
	// controller's own artifacts without going through the sink.
	if len(sink.requests) != 1 || sink.requests[0].Kind != screenshotArtifactKind {
		t.Fatalf("artifact writes = %#v, want exactly one screenshot", sink.requests)
	}
	if len(reader.requests) != 2 {
		t.Fatalf("resource reads = %d, want two (the assertScreenshot expected and the runScript source)",
			len(reader.requests))
	}
	if len(checker.requests) != 1 {
		t.Fatalf("image checks = %d, want exactly one", len(checker.requests))
	}
}

// batch714Keywords defines the fixture's required command order.
var batch714Keywords = []model.CommandKeyword{
	model.CommandStopApp, model.CommandKillApp, model.CommandClearState, model.CommandClearKeychain,
	model.CommandOpenLink, model.CommandOpenBrowser, model.CommandWaitForAnimationToEnd,
	model.CommandSetLocation, model.CommandSetOrientation, model.CommandSetPermissions,
	model.CommandSetAirplaneMode, model.CommandToggleAirplaneMode, model.CommandTravel,
	model.CommandStartRecording, model.CommandAddMedia, model.CommandTakeScreenshot,
	model.CommandAssertScreenshot, model.CommandStopRecording,
	model.CommandRunScript, model.CommandEvalScript,
}

// batch714ResourceReader answers per path: the assertScreenshot expected is an
// image and the runScript source is JavaScript, so one shared byte slice
// cannot serve both.
type batch714ResourceReader struct {
	requests []ResourceReadRequest
}

func (reader *batch714ResourceReader) Read(
	_ context.Context,
	request ResourceReadRequest,
) (ResourceReadResult, error) {
	reader.requests = append(reader.requests, request)
	if strings.HasSuffix(request.Path, ".js") {
		return ResourceReadResult{Data: []byte("output.scriptRan = true\n")}, nil
	}
	return ResourceReadResult{Data: checkStripPNG(0)}, nil
}

func batch714Prepare(t testing.TB) *Program {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "flows", "g003-batch7-14", "happy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	program, err := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root}}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("Prepare(batch7-14 happy.yaml) error = %v", err)
	}
	return program
}

func batch714Driver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: batch714Results(device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
			WidthPixels: 400, HeightPixels: 884,
		}, batch714ServiceCallBudget),
		StopApp:                 batch714Void(1),
		KillApp:                 batch714Void(1),
		ClearAppState:           batch714Void(1),
		ClearKeychain:           batch714Void(1),
		OpenLink:                batch714Void(2),
		SetLocation:             batch714Void(2 + travelStepsPerSegment),
		SetOrientation:          batch714Void(1),
		SetPermissions:          batch714Void(1),
		SetAirplaneMode:         batch714Void(2),
		AddMedia:                batch714Void(1),
		IsAirplaneModeEnabled:   batch714Results(false, 1),
		WaitUntilScreenIsStatic: batch714Results(true, 1),
		TakeScreenshot:          batch714Results(checkStripPNG(0), 2),
		StartScreenRecording:    batch714Results(device.CaptureID("capture-1"), 1),
	})
	return driver
}

// batch714ServiceCallBudget is a ceiling, not an expectation: DeviceInfo is a
// read the engine may make once per command, and this test asserts behavior at
// the command boundaries rather than pinning that read's cadence.
const batch714ServiceCallBudget = 256

func batch714Results[T any](value T, count int) []enginetest.Result[T] {
	results := make([]enginetest.Result[T], count)
	for index := range results {
		results[index].Value = value
	}
	return results
}

func batch714Void(count int) []enginetest.Result[struct{}] {
	return make([]enginetest.Result[struct{}], count)
}

func batch714DriverCalls(driver *enginetest.FakeDriver, method enginetest.Method) int {
	calls := 0
	for _, action := range driver.Actions() {
		if action.Method == method {
			calls++
		}
	}
	return calls
}
