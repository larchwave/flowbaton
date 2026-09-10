package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// A flow that names an application expects that application to be alive. When
// it dies on its own, a screenshot captures the home screen, and reporting that
// as a pass is the failure this file pins: the run says "Completed" while the
// picture holds someone else's screen.

const livenessAppID = "com.example.liveness"

func TestTakeScreenshotFailsWhenTheFlowsApplicationDied(t *testing.T) {
	t.Parallel()

	driver := livenessDriver(false)
	result, err := runLivenessFlow(t, driver, screenshotCommand("after-crash"))
	if err == nil {
		t.Fatal("takeScreenshot succeeded with a dead application")
	}
	if !strings.Contains(err.Error(), livenessAppID) {
		t.Fatalf("error = %v, want it to name %s", err, livenessAppID)
	}
	if result.Outcome() == Completed {
		t.Fatalf("outcome = %s, want a failure", result.Outcome())
	}
	// The executor's own failure screenshot is compressed; the authored
	// capture is not. Only the authored one must be missing.
	for _, action := range driver.Actions() {
		request, ok := action.Request.(device.ScreenshotRequest)
		if action.Method == enginetest.MethodTakeScreenshot && ok && !request.Compressed {
			t.Fatal("the authored screenshot was taken anyway; the check must come first")
		}
	}
}

func TestTakeScreenshotAcceptsAnApplicationTheFlowStoppedItself(t *testing.T) {
	t.Parallel()

	// stopApp is the flow's own decision: the application is gone on purpose,
	// so the screenshot that follows is what the author asked for.
	driver := livenessDriver(false)
	if _, err := runLivenessFlow(t, driver,
		model.Command{Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: livenessAppID},
		screenshotCommand("after-stop"),
	); err != nil {
		t.Fatalf("execute(stopApp, takeScreenshot) error = %T %v", err, err)
	}
}

func TestTakeScreenshotAcceptsALivingApplication(t *testing.T) {
	t.Parallel()

	driver := livenessDriver(true)
	if _, err := runLivenessFlow(t, driver, screenshotCommand("alive")); err != nil {
		t.Fatalf("execute(takeScreenshot) error = %T %v", err, err)
	}
}

func TestTakeScreenshotSkipsTheCheckWhereThePlatformCannotAnswer(t *testing.T) {
	t.Parallel()

	// A web flow's appId is a URL and no process belongs to it. The driver
	// says so with the shared unsupported sentinel, and the flow proceeds.
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:     []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("web"), WidthGrid: 400, HeightGrid: 884}}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: mediaScreenshotBytes}},
		IsAppRunning:   []enginetest.Result[bool]{{Err: device.ErrUnsupported}},
	})
	if _, err := runLivenessFlow(t, driver, screenshotCommand("web")); err != nil {
		t.Fatalf("execute(takeScreenshot) error = %T %v", err, err)
	}
}

func screenshotCommand(name string) model.Command {
	return model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: name}
}

func livenessDriver(running bool) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:     []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 400, HeightGrid: 884}}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: mediaScreenshotBytes}, {Value: mediaScreenshotBytes}},
		IsAppRunning:   []enginetest.Result[bool]{{Value: running}, {Value: running}},
	})
	return driver
}

func runLivenessFlow(
	t testing.TB,
	driver *enginetest.FakeDriver,
	commands ...model.Command,
) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(append(mediaHandlerSpecs(), lifecycleHandlerSpecs()...)...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(media, lifecycle) error = %v", err)
	}
	path := "/workspace/liveness.yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: livenessAppID},
		Commands: commands,
	}
	program := &Program{
		roots: []string{path}, paths: []string{path},
		flows:   map[string]model.Flow{path: flow},
		aliases: map[string]string{path: path},
		graph: capability.Report{
			Roots: []string{path},
			Nodes: []capability.GraphNode{{Path: path}},
		},
	}
	compiled, compileErr := compileProgram(context.Background(), program, registry)
	if compileErr != nil {
		t.Fatalf("compileProgram(liveness) error = %v", compileErr)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	return executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "liveness", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		ArtifactSink: &recordingArtifactSink{},
	}, root, "liveness/root-run-000001")
}
