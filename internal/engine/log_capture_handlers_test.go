package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

type logCaptureControllerStub struct {
	started   []LogCaptureStartRequest
	stops     int
	artifacts []device.Artifact
	startErr  error
	stopErr   error
}

func (controller *logCaptureControllerStub) Start(_ context.Context, request LogCaptureStartRequest) error {
	controller.started = append(controller.started, request)
	return controller.startErr
}

func (controller *logCaptureControllerStub) Stop(context.Context) ([]device.Artifact, error) {
	controller.stops++
	if controller.stopErr != nil {
		return nil, controller.stopErr
	}
	return controller.artifacts, nil
}

func startLogCaptureCommand(name string) model.Command {
	return model.Command{Kind: model.CommandStartLogCapture, Form: model.CommandFormObject, Arguments: name}
}

func stopLogCaptureCommand() model.Command {
	return model.Command{Kind: model.CommandStopLogCapture, Form: model.CommandFormScalar}
}

func TestStartLogCapturePassesTheNameAndTheFlowApp(t *testing.T) {
	t.Parallel()

	controller := &logCaptureControllerStub{}
	result, err := runLogCaptureCommands(t, controller, startLogCaptureCommand("fixture-${SUFFIX}"))
	if err != nil {
		t.Fatalf("startLogCapture error = %v", err)
	}
	if len(controller.started) != 1 {
		t.Fatalf("controller starts = %#v, want exactly one", controller.started)
	}
	want := LogCaptureStartRequest{Name: "fixture-console", AppID: "com.example.logs"}
	if controller.started[0] != want {
		t.Fatalf("start request = %#v, want %#v", controller.started[0], want)
	}
	if got := result.Commands()[0].Outcome(); got != Completed {
		t.Fatalf("startLogCapture outcome = %q, want %q", got, Completed)
	}
}

func TestStopLogCaptureLinksTheArtifactAndSaysWhatItIs(t *testing.T) {
	t.Parallel()

	controller := &logCaptureControllerStub{artifacts: []device.Artifact{{
		Kind: deviceLogArtifactKind, Path: "/run/fixture-console.ndjson",
		Metadata: map[string]string{
			"source": "unified-log", "scope": "app", "appId": "com.example.logs", "bytes": "8192",
		},
	}}}
	result, err := runLogCaptureCommands(t, controller, stopLogCaptureCommand())
	if err != nil {
		t.Fatalf("stopLogCapture error = %v", err)
	}
	if controller.stops != 1 {
		t.Fatalf("controller stops = %d, want 1", controller.stops)
	}
	command := result.Commands()[0]
	artifacts := command.Artifacts()
	if len(artifacts) != 1 || artifacts[0].Path != "/run/fixture-console.ndjson" ||
		artifacts[0].Kind != deviceLogArtifactKind || artifacts[0].Metadata["source"] != "unified-log" {
		t.Fatalf("stopLogCapture artifacts = %#v, want the controller's finalized capture", artifacts)
	}
	messages := command.Metadata().LogMessages()
	if len(messages) != 1 {
		t.Fatalf("log messages = %#v, want one line", messages)
	}
	for _, fragment := range []string{"device-log /run/fixture-console.ndjson", "unified-log", "scope app com.example.logs", "8192 bytes"} {
		if !strings.Contains(messages[0], fragment) {
			t.Fatalf("log message %q lacks %q", messages[0], fragment)
		}
	}
}

func TestStopLogCaptureNamesADeviceWideCapture(t *testing.T) {
	t.Parallel()

	line := describeDeviceLogArtifact(device.Artifact{
		Kind: deviceLogArtifactKind, Path: "/run/all.log",
		Metadata: map[string]string{"source": "syslog", "scope": "device", "truncated": "true"},
	})
	for _, fragment := range []string{"scope device-wide", "source syslog", "truncated"} {
		if !strings.Contains(line, fragment) {
			t.Fatalf("description %q lacks %q", line, fragment)
		}
	}
	if bare := describeDeviceLogArtifact(device.Artifact{Kind: deviceLogArtifactKind, Path: "/run/x.log"}); bare != "device-log /run/x.log" {
		t.Fatalf("description without metadata = %q", bare)
	}
}

func TestLogCaptureFailsClosedWithoutAController(t *testing.T) {
	t.Parallel()

	for _, command := range []model.Command{startLogCaptureCommand("console"), stopLogCaptureCommand()} {
		_, err := runLogCaptureCommands(t, nil, command)
		if !isConfigurationError(err) {
			t.Fatalf("%s without a controller: error = %T %v, want ConfigurationError", command.Kind, err, err)
		}
	}
}

func TestLogCapturePropagatesTheControllerCause(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("no log capture is running")
	_, err := runLogCaptureCommands(t, &logCaptureControllerStub{stopErr: sentinel}, stopLogCaptureCommand())
	if !errors.Is(err, sentinel) {
		t.Fatalf("stop error = %T %v, want the exact controller cause", err, err)
	}
	_, err = runLogCaptureCommands(t, &logCaptureControllerStub{startErr: sentinel}, startLogCaptureCommand("console"))
	if !errors.Is(err, sentinel) {
		t.Fatalf("start error = %T %v, want the exact controller cause", err, err)
	}
}

func TestLogCaptureCompileRefusesBadShapes(t *testing.T) {
	t.Parallel()

	for _, command := range []model.Command{
		{Kind: model.CommandStartLogCapture, Form: model.CommandFormObject, Arguments: "   "},
		{Kind: model.CommandStartLogCapture, Form: model.CommandFormObject, Arguments: []any{"console"}},
		{Kind: model.CommandStopLogCapture, Form: model.CommandFormObject, Arguments: map[string]any{"mystery": true}},
	} {
		if compiled, err := compileLogCapture(command); compiled != nil || !isConfigurationError(err) {
			t.Fatalf("compileLogCapture(%s %#v) = %#v, %T %v; want nil and ConfigurationError",
				command.Kind, command.Arguments, compiled, err, err)
		}
	}
}

func runLogCaptureCommands(t testing.TB, controller LogCaptureController, command model.Command) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(logCaptureHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(logCapture) error = %v", err)
	}
	path := "/workspace/logs-" + string(command.Kind) + ".yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.logs", Env: map[string]string{"SUFFIX": "console"}},
		Commands: []model.Command{command},
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
		t.Fatalf("compileProgram(%s) error = %v", command.Kind, compileErr)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	dependencies := Dependencies{
		ExecutionID: "logs", Driver: enginetest.NewFakeDriver(), Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	if controller != nil {
		dependencies.LogCaptureController = controller
	}
	return executeCompiledRootForRun(context.Background(), dependencies, root, "logs/root-run-000001")
}
