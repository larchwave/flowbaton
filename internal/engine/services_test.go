package engine

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/imagecheck"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

var (
	_ ArtifactSink        = (*serviceFixture)(nil)
	_ RecordingController = (*serviceFixture)(nil)
	_ ResourceReader      = (*serviceFixture)(nil)
	_ InputGenerator      = (*serviceFixture)(nil)
	_ ImageChecker        = (*serviceFixture)(nil)
)

func TestInputKindsAreStableAndClosed(t *testing.T) {
	t.Parallel()

	want := []InputKind{
		InputText,
		InputNumber,
		InputEmail,
		InputPersonName,
		InputCityName,
		InputCountryName,
		InputColorName,
	}
	for index, kind := range want {
		if !kind.valid() {
			t.Errorf("input kind %d (%q) is invalid", index, kind)
		}
	}
	if InputKind("").valid() || InputKind("future-kind").valid() {
		t.Fatal("input kind enum accepted a value outside the stable set")
	}
}

func TestCommandEvaluatorReceivesOnlyReadOnlyEvaluationSnapshot(t *testing.T) {
	t.Parallel()

	var _ commandEvaluator = func(
		context.Context,
		evaluationContext,
		model.Command,
		any,
	) (evaluatedDispatch, error) {
		return evaluatedDispatch{}, nil
	}

	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error: %v", err)
	}
	defer runtime.Close()

	active := model.Config{AppID: "com.example.original", Env: map[string]string{"TOKEN": "original"}}
	state := &executionState{
		runtimeFn:       func() (js.Runtime, error) { return runtime, nil },
		currentConfigFn: func() (model.Config, error) { return active, nil },
	}
	evaluation, err := captureEvaluationContext(state)
	if err != nil {
		t.Fatalf("captureEvaluationContext() error: %v", err)
	}
	if got, appErr := evaluation.ActiveAppID(); appErr != nil || got != "com.example.original" {
		t.Fatalf("initial ActiveAppID() = %q %v, want immutable captured app ID", got, appErr)
	}
	active.AppID = "com.example.mutated"
	active.Env["TOKEN"] = "mutated"

	if got, interpolationErr := evaluation.Interpolate(context.Background(), "${'runtime-ok'}", nil); interpolationErr != nil || got != "runtime-ok" {
		t.Fatalf("Interpolate() = %q %v, want narrow captured evaluation capability", got, interpolationErr)
	}
	if got, appErr := evaluation.ActiveAppID(); appErr != nil || got != "com.example.original" {
		t.Fatalf("ActiveAppID() = %q %v, want immutable captured app ID", got, appErr)
	}
	config, configErr := evaluation.ActiveConfig()
	if configErr != nil {
		t.Fatalf("ActiveConfig() error: %v", configErr)
	}
	config.Env["TOKEN"] = "caller mutation"
	second, configErr := evaluation.ActiveConfig()
	if configErr != nil || second.Env["TOKEN"] != "original" {
		t.Fatalf("ActiveConfig() second snapshot = %#v %v, want immutable clone", second, configErr)
	}

	typeOf := reflect.TypeOf(evaluation)
	wantMethods := map[string]struct{}{
		"ActiveAppID": {}, "ActiveConfig": {}, "Evaluate": {}, "Interpolate": {},
	}
	if typeOf.NumMethod() != len(wantMethods) {
		t.Fatalf("evaluationContext methods = %d, want exactly %d read-only methods", typeOf.NumMethod(), len(wantMethods))
	}
	for index := 0; index < typeOf.NumMethod(); index++ {
		method := typeOf.Method(index)
		if _, allowed := wantMethods[method.Name]; !allowed {
			t.Fatalf("evaluationContext exposes effectful or unknown method %s", method.Name)
		}
	}
	forbiddenFields := []reflect.Type{
		reflect.TypeOf(executionState{}),
		reflect.TypeOf(Dependencies{}),
		reflect.TypeOf(&ElementLookup{}),
		reflect.TypeOf((*js.Runtime)(nil)).Elem(),
	}
	for fieldIndex := 0; fieldIndex < typeOf.NumField(); fieldIndex++ {
		field := typeOf.Field(fieldIndex)
		for _, forbidden := range forbiddenFields {
			if field.Type == forbidden {
				t.Fatalf("evaluationContext exposes forbidden field %s of type %v", field.Name, field.Type)
			}
		}
	}
}

func TestExecutionStateRequiresOptionalServicesAtUseSite(t *testing.T) {
	t.Parallel()

	state := &executionState{}
	operations := []struct {
		name string
		run  func() error
	}{
		{name: "artifact sink", run: func() error {
			_, err := state.writeArtifact(context.Background(), ArtifactWriteRequest{})
			return err
		}},
		{name: "recording start", run: func() error {
			return state.startRecording(context.Background(), RecordingStartRequest{})
		}},
		{name: "recording stop", run: func() error {
			_, err := state.stopRecording(context.Background())
			return err
		}},
		{name: "resource reader", run: func() error {
			_, err := state.readResource(context.Background(), ResourceReadRequest{})
			return err
		}},
		{name: "input generator", run: func() error {
			_, err := state.generateInput(context.Background(), InputRequest{Kind: InputText})
			return err
		}},
		{name: "image checker", run: func() error {
			_, err := state.checkImages(context.Background(), ImageCheckRequest{})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			var configurationError *ConfigurationError
			if err := operation.run(); !errors.As(err, &configurationError) {
				t.Fatalf("operation error = %T %v, want *ConfigurationError", err, err)
			}
		})
	}
}

func TestExecutionStateRejectsTypedNilOptionalServices(t *testing.T) {
	t.Parallel()

	var typedNil *serviceFixture
	operations := []struct {
		name  string
		state *executionState
		run   func(*executionState) error
	}{
		{name: "artifact sink", state: &executionState{dependencies: Dependencies{ArtifactSink: typedNil}}, run: func(state *executionState) error {
			_, err := state.writeArtifact(context.Background(), ArtifactWriteRequest{})
			return err
		}},
		{name: "recording controller", state: &executionState{dependencies: Dependencies{RecordingController: typedNil}}, run: func(state *executionState) error {
			return state.startRecording(context.Background(), RecordingStartRequest{})
		}},
		{name: "resource reader", state: &executionState{dependencies: Dependencies{ResourceReader: typedNil}}, run: func(state *executionState) error {
			_, err := state.readResource(context.Background(), ResourceReadRequest{})
			return err
		}},
		{name: "input generator", state: &executionState{dependencies: Dependencies{InputGenerator: typedNil}}, run: func(state *executionState) error {
			_, err := state.generateInput(context.Background(), InputRequest{Kind: InputText})
			return err
		}},
		{name: "image checker", state: &executionState{dependencies: Dependencies{ImageChecker: typedNil}}, run: func(state *executionState) error {
			_, err := state.checkImages(context.Background(), ImageCheckRequest{})
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			var configurationError *ConfigurationError
			if err := operation.run(operation.state); !errors.As(err, &configurationError) {
				t.Fatalf("operation error = %T %v, want *ConfigurationError", err, err)
			}
		})
	}
}

func TestExecutionStateCopiesHostServiceInputsAndResults(t *testing.T) {
	t.Parallel()

	crop := image.Rect(1, 2, 3, 4)
	fixture := &serviceFixture{
		writeResult: ArtifactWriteResult{
			Artifact:     device.Artifact{Kind: "snapshot", Path: "owned/result.png", Metadata: map[string]string{"source": "sink"}},
			BytesWritten: 3,
		},
		stopArtifacts: []device.Artifact{{Kind: "recording", Path: "owned/run.mp4", Metadata: map[string]string{"codec": "h264"}}},
		readResult:    ResourceReadResult{Data: []byte("resource"), Metadata: map[string]string{"kind": "fixture"}},
		generated:     "generated",
	}
	state := &executionState{dependencies: Dependencies{
		ArtifactSink:        fixture,
		RecordingController: fixture,
		ResourceReader:      fixture,
		InputGenerator:      fixture,
		ImageChecker:        fixture,
	}}

	writeRequest := ArtifactWriteRequest{
		Owner: "command-1", Kind: "snapshot", SuggestedName: "screen.png",
		Data: []byte("png"), Metadata: map[string]string{"screen": "checkout"},
	}
	writeResult, err := state.writeArtifact(context.Background(), writeRequest)
	if err != nil {
		t.Fatalf("writeArtifact() error: %v", err)
	}
	startRequest := RecordingStartRequest{Name: "run", Metadata: map[string]string{"flow": "root"}}
	if err := state.startRecording(context.Background(), startRequest); err != nil {
		t.Fatalf("startRecording() error: %v", err)
	}
	artifacts, err := state.stopRecording(context.Background())
	if err != nil {
		t.Fatalf("stopRecording() error: %v", err)
	}
	resource, err := state.readResource(context.Background(), ResourceReadRequest{Path: "fixture.txt"})
	if err != nil {
		t.Fatalf("readResource() error: %v", err)
	}
	generated, err := state.generateInput(context.Background(), InputRequest{Kind: InputEmail, Length: -1})
	if err != nil || generated != "generated" {
		t.Fatalf("generateInput() = %q, %v", generated, err)
	}
	if fixture.lastInput != (InputRequest{Kind: InputEmail, Length: -1}) {
		t.Fatalf("input request = %#v, want parsed length passed unchanged", fixture.lastInput)
	}
	expected := []byte{1, 2, 3}
	actual := []byte{4, 5, 6}
	if _, err := state.checkImages(context.Background(), ImageCheckRequest{Expected: expected, Actual: actual, Crop: &crop}); err != nil {
		t.Fatalf("checkImages() error: %v", err)
	}

	fixture.lastWrite.Data[0] = 'X'
	fixture.lastWrite.Metadata["screen"] = "mutated"
	fixture.lastRecording.Metadata["flow"] = "mutated"
	fixture.lastCheck.Expected[0] = 9
	fixture.lastCheck.Crop.Min.X = 99
	fixture.writeResult.Artifact.Metadata["source"] = "mutated"
	fixture.stopArtifacts[0].Metadata["codec"] = "mutated"
	fixture.readResult.Data[0] = 'X'
	fixture.readResult.Metadata["kind"] = "mutated"

	if string(writeRequest.Data) != "png" || writeRequest.Metadata["screen"] != "checkout" {
		t.Fatalf("artifact service mutated caller request: %#v", writeRequest)
	}
	if startRequest.Metadata["flow"] != "root" {
		t.Fatalf("recording service mutated caller request: %#v", startRequest)
	}
	if expected[0] != 1 || crop.Min.X != 1 {
		t.Fatalf("image service mutated caller request: expected=%v crop=%v", expected, crop)
	}
	if writeResult.Artifact.Metadata["source"] != "sink" || artifacts[0].Metadata["codec"] != "h264" {
		t.Fatalf("service result aliases provider storage: %#v / %#v", writeResult, artifacts)
	}
	if string(resource.Data) != "resource" || resource.Metadata["kind"] != "fixture" {
		t.Fatalf("resource result aliases provider storage: %#v", resource)
	}
}

func TestExecutionStateValidatesInputLengthAndRechecksCancellationAfterServiceCall(t *testing.T) {
	t.Parallel()

	fixture := &serviceFixture{generated: "unused"}
	state := &executionState{dependencies: Dependencies{InputGenerator: fixture}}
	if _, err := state.generateInput(context.Background(), InputRequest{Kind: InputText, Length: -1}); err == nil {
		t.Fatal("generateInput() error = nil, want negative length rejection")
	}
	if fixture.inputCalls != 0 {
		t.Fatalf("invalid input reached generator %d time(s)", fixture.inputCalls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	state.dependencies.InputGenerator = cancellingInputGenerator{cancel: cancel}
	if _, err := state.generateInput(ctx, InputRequest{Kind: InputText, Length: 4}); !errors.Is(err, context.Canceled) {
		t.Fatalf("generateInput() error = %v, want cancellation observed after provider return", err)
	}
}

func TestArtifactAndRecordingServicesPreserveFinalizedOutputOnPostProviderCancellation(t *testing.T) {
	t.Parallel()

	t.Run("artifact", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		sink := &cancellingArtifactSink{
			cancel: cancel,
			result: ArtifactWriteResult{
				Artifact: device.Artifact{
					Kind: "screenshot", Path: "owned/checkout.png",
					Metadata: map[string]string{"screen": "checkout"},
				},
				BytesWritten: 3,
			},
		}
		state := &executionState{dependencies: Dependencies{ArtifactSink: sink}}
		result, err := state.writeArtifact(ctx, ArtifactWriteRequest{Kind: "screenshot"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("writeArtifact error = %v, want context.Canceled", err)
		}
		if result.Artifact.Path != "owned/checkout.png" || result.BytesWritten != 3 || result.Artifact.Metadata["screen"] != "checkout" {
			t.Fatalf("writeArtifact result = %#v, want finalized output with cancellation", result)
		}
		sink.result.Artifact.Metadata["screen"] = "provider-mutated"
		if result.Artifact.Metadata["screen"] != "checkout" {
			t.Fatalf("writeArtifact result aliases provider storage: %#v", result)
		}
	})

	t.Run("recording", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		controller := &cancellingRecordingController{
			cancel: cancel,
			artifacts: []device.Artifact{{
				Kind: "recording", Path: "owned/run.mp4",
				Metadata: map[string]string{"codec": "h264"},
			}},
		}
		state := &executionState{dependencies: Dependencies{RecordingController: controller}}
		artifacts, err := state.stopRecording(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stopRecording error = %v, want context.Canceled", err)
		}
		if len(artifacts) != 1 || artifacts[0].Path != "owned/run.mp4" || artifacts[0].Metadata["codec"] != "h264" {
			t.Fatalf("stopRecording artifacts = %#v, want finalized output with cancellation", artifacts)
		}
		controller.artifacts[0].Metadata["codec"] = "provider-mutated"
		if artifacts[0].Metadata["codec"] != "h264" {
			t.Fatalf("stopRecording result aliases provider storage: %#v", artifacts)
		}
	})
}

type serviceFixture struct {
	lastWrite     ArtifactWriteRequest
	writeResult   ArtifactWriteResult
	lastRecording RecordingStartRequest
	stopArtifacts []device.Artifact
	readResult    ResourceReadResult
	generated     string
	lastInput     InputRequest
	inputCalls    int
	lastCheck     ImageCheckRequest
}

func (fixture *serviceFixture) Write(_ context.Context, request ArtifactWriteRequest) (ArtifactWriteResult, error) {
	fixture.lastWrite = request
	return fixture.writeResult, nil
}

func (fixture *serviceFixture) Start(_ context.Context, request RecordingStartRequest) error {
	fixture.lastRecording = request
	return nil
}

func (fixture *serviceFixture) Stop(context.Context) ([]device.Artifact, error) {
	return fixture.stopArtifacts, nil
}

func (fixture *serviceFixture) Read(context.Context, ResourceReadRequest) (ResourceReadResult, error) {
	return fixture.readResult, nil
}

func (fixture *serviceFixture) Generate(_ context.Context, request InputRequest) (string, error) {
	fixture.inputCalls++
	fixture.lastInput = request
	return fixture.generated, nil
}

func (fixture *serviceFixture) Check(_ context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	fixture.lastCheck = request
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		return imagecheck.Result{}, err
	}
	return imagecheck.Check(encoded.Bytes(), encoded.Bytes(), nil)
}

type cancellingInputGenerator struct {
	cancel context.CancelFunc
}

type cancellingArtifactSink struct {
	cancel context.CancelFunc
	result ArtifactWriteResult
}

func (sink *cancellingArtifactSink) Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.cancel()
	return sink.result, nil
}

type cancellingRecordingController struct {
	cancel    context.CancelFunc
	artifacts []device.Artifact
}

func (controller *cancellingRecordingController) Start(context.Context, RecordingStartRequest) error {
	return nil
}

func (controller *cancellingRecordingController) Stop(context.Context) ([]device.Artifact, error) {
	controller.cancel()
	return controller.artifacts, nil
}

func (generator cancellingInputGenerator) Generate(context.Context, InputRequest) (string, error) {
	generator.cancel()
	return "generated", nil
}
