package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// These tests cover artifact and media commands. Recording start/stop go
// through RecordingController rather than the driver: frozen Driver v0 has
// StartScreenRecording but no stop counterpart, and the controller exists
// precisely to complete that lifecycle outside the frozen contract.

func TestMediaHandlerSpecsComposeExactFour(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(mediaHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(media) error = %v", err)
	}
	want := []string{"addMedia", "startRecording", "stopRecording", "takeScreenshot"}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, want) {
		t.Fatalf("media registry = %#v, want %#v", got, want)
	}
	for _, test := range []struct {
		keyword model.CommandKeyword
		class   effectClass
	}{
		{keyword: model.CommandTakeScreenshot, class: EffectArtifact},
		{keyword: model.CommandStopRecording, class: EffectArtifact},
		{keyword: model.CommandStartRecording, class: EffectHostMutation},
		{keyword: model.CommandAddMedia, class: EffectDeviceMutation},
	} {
		spec, _ := registry.lookup(test.keyword)
		if spec.effectClass != test.class {
			t.Fatalf("%s effect class = %v, want %v", test.keyword, spec.effectClass, test.class)
		}
		// Only device mutations may declare a post-action policy.
		if test.class == EffectDeviceMutation {
			if spec.postAction != postActionNoSettle {
				t.Fatalf("%s post action = %v, want no-settle", test.keyword, spec.postAction)
			}
		} else if spec.postAction != postActionUnspecified || spec.settleRequest != nil {
			t.Fatalf("%s must not declare a post-action policy: %#v", test.keyword, spec)
		}
	}
}

func TestTakeScreenshotWritesArtifactWithInterpolatedName(t *testing.T) {
	t.Parallel()

	driver := mediaDriver(nil)
	sink := &recordingArtifactSink{}
	command := model.Command{
		Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: "shot-${'one'}",
	}
	result, err := runMediaCommand(t, driver, sink, nil, command)
	if err != nil {
		t.Fatalf("execute(takeScreenshot) error = %T %v", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	if len(sink.requests) != 1 {
		t.Fatalf("artifact writes = %#v, want exactly one", sink.requests)
	}
	written := sink.requests[0]
	if written.Kind != screenshotArtifactKind {
		t.Fatalf("artifact kind = %q, want %q", written.Kind, screenshotArtifactKind)
	}
	// The extension is derived from the encoded bytes rather than authored text.
	if written.SuggestedName != "shot-one.png" {
		t.Fatalf("suggested name = %q, want the interpolated name plus .png", written.SuggestedName)
	}
	if string(written.Data) != string(mediaScreenshotBytes) {
		t.Fatalf("artifact data = %q, want the driver screenshot bytes", written.Data)
	}
}

func TestTakeScreenshotPublishesTheFinalizedArtifact(t *testing.T) {
	t.Parallel()

	driver := mediaDriver(nil)
	sink := &recordingArtifactSink{}
	command := model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: "shot"}
	result, err := runMediaCommand(t, driver, sink, nil, command)
	if err != nil {
		t.Fatalf("execute error = %v", err)
	}
	commands := result.Commands()
	if len(commands) != 1 {
		t.Fatalf("command results = %d, want one", len(commands))
	}
	artifacts := commands[0].Artifacts()
	if len(artifacts) != 1 || artifacts[0].Kind != screenshotArtifactKind {
		t.Fatalf("published artifacts = %#v, want one screenshot", artifacts)
	}
}

func TestTakeScreenshotFailsClosedWithoutAnArtifactSink(t *testing.T) {
	t.Parallel()

	// The sink is optional on Dependencies, so a flow authoring takeScreenshot
	// without one must fail rather than silently discard the capture.
	driver := mediaDriver(nil)
	command := model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: "shot"}
	_, err := runMediaCommand(t, driver, nil, nil, command)
	if !isConfigurationError(err) {
		t.Fatalf("execute without sink error = %T %v, want ConfigurationError", err, err)
	}
}

func TestStartRecordingPassesTheInterpolatedName(t *testing.T) {
	t.Parallel()

	controller := &recordingControllerStub{}
	command := model.Command{
		Kind: model.CommandStartRecording, Form: model.CommandFormObject, Arguments: "run-${'two'}",
	}
	if _, err := runMediaCommand(t, mediaDriver(nil), &recordingArtifactSink{}, controller, command); err != nil {
		t.Fatalf("execute(startRecording) error = %T %v", err, err)
	}
	if len(controller.started) != 1 || controller.started[0].Name != "run-two" {
		t.Fatalf("recording starts = %#v, want one interpolated name", controller.started)
	}
	if controller.stops != 0 {
		t.Fatalf("startRecording also stopped the recording: %d stops", controller.stops)
	}
}

func TestStopRecordingPublishesControllerArtifacts(t *testing.T) {
	t.Parallel()

	controller := &recordingControllerStub{artifacts: []device.Artifact{
		{Kind: "recording", Path: "/artifacts/run.mp4"},
	}}
	command := model.Command{Kind: model.CommandStopRecording, Form: model.CommandFormScalar}
	result, err := runMediaCommand(t, mediaDriver(nil), &recordingArtifactSink{}, controller, command)
	if err != nil {
		t.Fatalf("execute(stopRecording) error = %T %v", err, err)
	}
	if controller.stops != 1 {
		t.Fatalf("controller stops = %d, want exactly one", controller.stops)
	}
	artifacts := result.Commands()[0].Artifacts()
	if len(artifacts) != 1 || artifacts[0].Path != "/artifacts/run.mp4" {
		t.Fatalf("published artifacts = %#v, want the controller artifact", artifacts)
	}
}

func TestStopRecordingDoesNotAliasControllerArtifacts(t *testing.T) {
	t.Parallel()

	controller := &recordingControllerStub{artifacts: []device.Artifact{
		{Kind: "recording", Path: "/artifacts/run.mp4", Metadata: map[string]string{"codec": "h264"}},
	}}
	command := model.Command{Kind: model.CommandStopRecording, Form: model.CommandFormScalar}
	result, err := runMediaCommand(t, mediaDriver(nil), &recordingArtifactSink{}, controller, command)
	if err != nil {
		t.Fatalf("execute error = %v", err)
	}
	published := result.Commands()[0].Artifacts()
	published[0].Metadata["codec"] = "mutated"
	if controller.artifacts[0].Metadata["codec"] != "h264" {
		t.Fatalf("controller artifact was mutated through the result: %#v", controller.artifacts[0])
	}
}

func TestRecordingFailsClosedWithoutAController(t *testing.T) {
	t.Parallel()

	for _, command := range []model.Command{
		{Kind: model.CommandStartRecording, Form: model.CommandFormObject, Arguments: "run"},
		{Kind: model.CommandStopRecording, Form: model.CommandFormScalar},
	} {
		t.Run(string(command.Kind), func(t *testing.T) {
			_, err := runMediaCommand(t, mediaDriver(nil), &recordingArtifactSink{}, nil, command)
			if !isConfigurationError(err) {
				t.Fatalf("execute without controller error = %T %v, want ConfigurationError", err, err)
			}
		})
	}
}

func TestAddMediaSendsTheResolvedPathsOfItsPreparedLinks(t *testing.T) {
	t.Parallel()

	// The paths the driver receives are the parser's resolved host paths, not
	// the authored text: the capability preflight canonicalizes every media
	// prepared file before execution, so there is nothing left to interpolate.
	for _, test := range []struct {
		name      string
		arguments any
		authored  []string
		want      []device.MediaFile
	}{
		{
			name: "single path", arguments: []any{"media/one.png"}, authored: []string{"media/one.png"},
			want: []device.MediaFile{{Path: "/resolved/media/one.png"}},
		},
		{
			name: "ordered list", arguments: []any{"media/a.png", "media/b.png"},
			authored: []string{"media/a.png", "media/b.png"},
			want: []device.MediaFile{
				{Path: "/resolved/media/a.png"}, {Path: "/resolved/media/b.png"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := mediaDriver(nil)
			command := addMediaCommand(test.arguments, test.authored...)
			if _, err := runMediaCommand(t, driver, &recordingArtifactSink{}, nil, command); err != nil {
				t.Fatalf("execute(addMedia) error = %T %v", err, err)
			}
			request, ok := mediaAction(t, driver).Request.(device.AddMediaRequest)
			if !ok {
				t.Fatalf("request = %#v, want device.AddMediaRequest", mediaAction(t, driver).Request)
			}
			if !reflect.DeepEqual(request.Files, test.want) {
				t.Fatalf("media files = %#v, want %#v", request.Files, test.want)
			}
		})
	}
}

func TestAddMediaRejectsLinksThatDoNotMatchItsPaths(t *testing.T) {
	t.Parallel()

	// Neither half of the pair can be forged alone: the authored list and the
	// parser's prepared links have to agree positionally.
	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{
			name:    "no links at all",
			command: model.Command{Kind: model.CommandAddMedia, Form: model.CommandFormObject, Arguments: "media/a.png"},
		},
		{
			name:    "fewer links than paths",
			command: addMediaCommand([]any{"media/a.png", "media/b.png"}, "media/a.png"),
		},
		{
			name:    "link for a different path",
			command: addMediaCommand("media/a.png", "media/other.png"),
		},
		{
			name: "link of the wrong kind",
			command: func() model.Command {
				command := addMediaCommand("media/a.png", "media/a.png")
				command.Links[0].Kind = model.FileLinkFlow
				return command
			}(),
		},
		{
			name: "link without a resolved path",
			command: func() model.Command {
				command := addMediaCommand("media/a.png", "media/a.png")
				command.Links[0].ResolvedPath = "  "
				return command
			}(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileMedia(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileMedia() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

// addMediaCommand builds the command shape the parser produces: the authored
// argument plus one prepared media link per path.
func addMediaCommand(arguments any, authored ...string) model.Command {
	links := make([]model.FileLink, len(authored))
	for index, path := range authored {
		links[index] = model.FileLink{
			Kind: model.FileLinkMedia, Path: path, ResolvedPath: "/resolved/" + path,
		}
	}
	return model.Command{
		Kind: model.CommandAddMedia, Form: model.CommandFormObject,
		Arguments: arguments, Links: links,
	}
}

func TestMediaCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
		{name: "takeScreenshot bare", command: model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormScalar}},
		{name: "takeScreenshot blank name", command: model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: "  "}},
		{name: "takeScreenshot wrong type", command: model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: int64(1)}},
		{name: "startRecording bare", command: model.Command{Kind: model.CommandStartRecording, Form: model.CommandFormScalar}},
		{name: "stopRecording with a value", command: model.Command{Kind: model.CommandStopRecording, Form: model.CommandFormObject, Arguments: "run"}},
		{name: "addMedia empty list", command: addMediaCommand([]any{})},
		{name: "addMedia non-string entry", command: addMediaCommand([]any{int64(1)}, "media/a.png")},
		{name: "addMedia blank entry", command: addMediaCommand([]any{" "}, " ")},
		{name: "addMedia bare", command: model.Command{Kind: model.CommandAddMedia, Form: model.CommandFormScalar}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileMedia(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileMedia(%#v) = %#v, %T %v; want nil and ConfigurationError", test.command, compiled, err, err)
			}
		})
	}
}

func TestMediaPropagatesDriverAndControllerFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("media boundary refused")
	t.Run("takeScreenshot driver failure", func(t *testing.T) {
		_, err := runMediaCommand(t, mediaDriver(sentinel), &recordingArtifactSink{}, nil,
			model.Command{Kind: model.CommandTakeScreenshot, Form: model.CommandFormObject, Arguments: "shot"})
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %T %v, want the exact driver cause", err, err)
		}
	})
	t.Run("addMedia driver failure", func(t *testing.T) {
		_, err := runMediaCommand(t, mediaDriver(sentinel), &recordingArtifactSink{}, nil,
			addMediaCommand([]any{"media/a.png"}, "media/a.png"))
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %T %v, want the exact driver cause", err, err)
		}
	})
	t.Run("stopRecording controller failure", func(t *testing.T) {
		controller := &recordingControllerStub{stopErr: sentinel}
		_, err := runMediaCommand(t, mediaDriver(nil), &recordingArtifactSink{}, controller,
			model.Command{Kind: model.CommandStopRecording, Form: model.CommandFormScalar})
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %T %v, want the exact controller cause", err, err)
		}
	})
}

var mediaScreenshotBytes = enginetest.PNGFixture("media-screenshot-png")

func mediaDriver(failure error) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:     []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: mediaScreenshotBytes, Err: failure}},
		AddMedia:       []enginetest.Result[struct{}]{{Err: failure}},
	})
	return driver
}

func mediaAction(t testing.TB, driver *enginetest.FakeDriver) enginetest.Action {
	t.Helper()
	var found []enginetest.Action
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodDeviceInfo {
			continue
		}
		found = append(found, action)
	}
	if len(found) != 1 {
		t.Fatalf("media driver calls = %#v, want exactly one", found)
	}
	return found[0]
}

type recordingArtifactSink struct {
	requests []ArtifactWriteRequest
	err      error
}

func (sink *recordingArtifactSink) Write(_ context.Context, request ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.requests = append(sink.requests, request)
	if sink.err != nil {
		return ArtifactWriteResult{}, sink.err
	}
	return ArtifactWriteResult{
		Artifact:     device.Artifact{Kind: request.Kind, Path: "/artifacts/" + request.SuggestedName},
		BytesWritten: int64(len(request.Data)),
	}, nil
}

type recordingControllerStub struct {
	started   []RecordingStartRequest
	stops     int
	artifacts []device.Artifact
	startErr  error
	stopErr   error
}

func (controller *recordingControllerStub) Start(_ context.Context, request RecordingStartRequest) error {
	controller.started = append(controller.started, request)
	return controller.startErr
}

func (controller *recordingControllerStub) Stop(context.Context) ([]device.Artifact, error) {
	controller.stops++
	if controller.stopErr != nil {
		return nil, controller.stopErr
	}
	return controller.artifacts, nil
}

func runMediaCommand(
	t testing.TB,
	driver *enginetest.FakeDriver,
	sink ArtifactSink,
	controller RecordingController,
	command model.Command,
) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(mediaHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(media) error = %v", err)
	}
	path := "/workspace/media-" + string(command.Kind) + ".yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.media"},
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
		ExecutionID: "media", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	if sink != nil {
		dependencies.ArtifactSink = sink
	}
	if controller != nil {
		dependencies.RecordingController = controller
	}
	return executeCompiledRootForRun(context.Background(), dependencies, root, "media/root-run-000001")
}

// The compiler rejects the scalar addMedia form. Authored media is always a
// non-empty list with one prepared link per item.
func TestAddMediaCompileRefusesTheScalarForm(t *testing.T) {
	t.Parallel()

	// A valid prepared link isolates scalar-shape validation.
	if compiled, err := compileMedia(addMediaCommand("media/a.png", "media/a.png")); compiled != nil ||
		!isConfigurationError(err) {
		t.Fatalf("compileMedia(scalar) = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
	}
	// The list form with the same prepared link is valid.
	if _, err := compileMedia(addMediaCommand([]any{"media/a.png"}, "media/a.png")); err != nil {
		t.Fatalf("compileMedia(list) error = %v, want the list form to still compile", err)
	}
}
