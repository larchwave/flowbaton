package engine

import (
	"context"
	"image"
	"math"
	"sync"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/imagecheck"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

// ArtifactWriteRequest describes content whose final path is owned by the
// injected sink rather than by a command handler.
type ArtifactWriteRequest struct {
	Owner         string
	Kind          string
	SuggestedName string
	Data          []byte
	Metadata      map[string]string
}

// ArtifactWriteResult identifies the finalized artifact owned by the sink.
type ArtifactWriteResult struct {
	Artifact     device.Artifact
	BytesWritten int64
}

// ArtifactSink owns artifact naming, storage, and finalization.
type ArtifactSink interface {
	Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error)
}

// RecordingStartRequest describes one host-managed recording session.
type RecordingStartRequest struct {
	Name     string
	Metadata map[string]string
}

// RecordingController completes the start/stop lifecycle outside frozen
// device.Driver v0 and returns only finalized artifacts from Stop.
type RecordingController interface {
	Start(context.Context, RecordingStartRequest) error
	Stop(context.Context) ([]device.Artifact, error)
}

// ResourceReadRequest names one independently resolved host resource.
type ResourceReadRequest struct {
	Path string
}

// ResourceReadResult is an owned snapshot of resource bytes and metadata.
type ResourceReadResult struct {
	Data     []byte
	Metadata map[string]string
}

// ResourceReader resolves command resources without direct filesystem access
// in handlers.
type ResourceReader interface {
	Read(context.Context, ResourceReadRequest) (ResourceReadResult, error)
}

// InputKind is the stable input-generation category used by random-input
// command handlers.
type InputKind string

const (
	InputText        InputKind = "text"
	InputNumber      InputKind = "number"
	InputEmail       InputKind = "email"
	InputPersonName  InputKind = "person-name"
	InputCityName    InputKind = "city-name"
	InputCountryName InputKind = "country-name"
	InputColorName   InputKind = "color-name"

	inputVariableMaximumLength = 1024
	inputNamedLengthSentinel   = -1
)

func (kind InputKind) valid() bool {
	switch kind {
	case InputText, InputNumber, InputEmail, InputPersonName, InputCityName, InputCountryName, InputColorName:
		return true
	default:
		return false
	}
}

// InputRequest asks the injected generator for one typed value.
type InputRequest struct {
	Kind   InputKind
	Length int
}

// InputGenerator supplies deterministic host-generated input.
type InputGenerator interface {
	Generate(context.Context, InputRequest) (string, error)
}

// ImageCheckRequest contains encoded images and an optional shared crop.
type ImageCheckRequest struct {
	Expected []byte
	Actual   []byte
	Crop     *image.Rectangle
}

// ImageChecker is the injectable host boundary around imagecheck.Result.
type ImageChecker interface {
	Check(context.Context, ImageCheckRequest) (imagecheck.Result, error)
}

// FailureDecision is the stable root-command failure action returned by a
// FailureResolver. CONTINUE is the only value that permits later root work.
type FailureDecision string

const (
	FailureDecisionFail     FailureDecision = "FAIL"
	FailureDecisionContinue FailureDecision = "CONTINUE"
)

// FailureResolver decides whether root-owned execution may continue after a
// finalized failed command. Child and nested execution never consults it.
type FailureResolver interface {
	ResolveFailure(context.Context, CommandResult) FailureDecision
}

type FailureResolverFunc func(context.Context, CommandResult) FailureDecision

func (resolver FailureResolverFunc) ResolveFailure(ctx context.Context, result CommandResult) FailureDecision {
	return resolver(ctx, result)
}

// Dependencies contains the required engine core and optional command-family
// services. Optional services are validated only by the handler that uses them.
type Dependencies struct {
	// ExecutionID is a caller-owned deterministic identity for one public
	// Execute invocation. Root-run correlation is derived from it.
	ExecutionID string
	// ExternalEnvironment is a caller-owned environment input. Execute takes
	// one sanitized snapshot and applies it only at each selected root scope.
	ExternalEnvironment map[string]string
	// ReservedEnvironment carries the variables only the host may set —
	// FLOWBATON_SHARD_ID, FLOWBATON_SHARD_INDEX, FLOWBATON_DEVICE_UDID. It is a
	// separate field because ExternalEnvironment is sanitized: those names are
	// stripped from the operator's map precisely so they can arrive only here.
	// Every key must carry the FLOWBATON_ prefix, and these win over both a
	// flow's own env and the operator's.
	ReservedEnvironment map[string]string

	// SequencedRoots is how many of the plan's LEADING roots
	// executionOrder.flowsOrder named. Those flows were declared to depend on
	// each other, so a failure among them ends the run unless
	// ContinueOnFailure says otherwise. Every remaining root is parallel-eligible
	// and always runs, as specified by specs/03-cli-tooling.md:30.
	SequencedRoots int
	// ContinueOnFailure carries the ordered sequence past a failed flow.
	ContinueOnFailure bool

	Driver     device.Driver
	Clock      Clock
	JSFactory  js.Factory
	Controller Controller
	// FailureResolver is optional. Nil and invalid resolvers fail closed.
	FailureResolver FailureResolver
	Listeners       []Listener

	ArtifactSink        ArtifactSink
	RecordingController RecordingController
	ResourceReader      ResourceReader
	InputGenerator      InputGenerator
	ImageChecker        ImageChecker
	// AIEngine is optional. A nil engine fails AI commands closed with
	// ErrCloudAPIKeyNotAvailable (specs/01-core-engine.md).
	AIEngine AIPredictionEngine
}

type executionState struct {
	dependencies            Dependencies
	executeCompiledSequence func(context.Context, []compiledDispatch, int) ([]CommandResult, error)
	executeCompiledFlow     func(context.Context, *compiledFlow, map[string]string, int) (FlowResult, error)
	metadataUpdatedFn       func(context.Context, CommandMetadata) error
	commandResetFn          func(context.Context, CommandResult) error
	compiledChildren        []compiledDispatch
	depth                   int

	runtimeFn       func() (js.Runtime, error)
	lookupFn        func() (*ElementLookup, error)
	currentConfigFn func() (model.Config, error)
	copiedTextFn    func() (string, error)
	setCopiedTextFn func(string) error
	putEnvFn        func(string, string) error
}

func (state *executionState) updateCommandMetadata(ctx context.Context, metadata CommandMetadata) error {
	if ctx == nil {
		return NewConfigurationError("command metadata update context must not be nil", nil)
	}
	if state == nil || state.metadataUpdatedFn == nil {
		return NewConfigurationError("command metadata updates are unavailable", nil)
	}
	return state.metadataUpdatedFn(ctx, cloneMetadata(metadata))
}

func (state *executionState) resetCommand(ctx context.Context, previous CommandResult) error {
	if ctx == nil {
		return NewConfigurationError("command reset context must not be nil", nil)
	}
	if state == nil || state.commandResetFn == nil {
		return NewConfigurationError("command resets are unavailable", nil)
	}
	return state.commandResetFn(ctx, cloneCommandResult(previous))
}

// evaluationContext is an immutable, effect-free snapshot for late command
// evaluation. It deliberately exposes only JavaScript evaluation and active
// configuration reads; full runtime and execution services remain core-owned.
type evaluationContext struct {
	evaluateFn      func(context.Context, js.EvalRequest) (js.Result, error)
	interpolateFn   func(context.Context, string, map[string]any) (string, error)
	activeConfigFn  func() (model.Config, error)
	activeConfig    model.Config
	hasActiveConfig bool
}

func captureEvaluationContext(state *executionState) (evaluationContext, error) {
	if state == nil {
		return evaluationContext{}, NewConfigurationError("execution state is unavailable for evaluation", nil)
	}
	captured := evaluationContext{}
	if state.runtimeFn != nil {
		var once sync.Once
		var runtime js.Runtime
		var runtimeErr error
		captureRuntime := func() (js.Runtime, error) {
			once.Do(func() {
				runtime, runtimeErr = state.jsRuntime()
			})
			return runtime, runtimeErr
		}
		captured.evaluateFn = func(ctx context.Context, request js.EvalRequest) (js.Result, error) {
			runtime, err := captureRuntime()
			if err != nil {
				return js.Result{}, err
			}
			return runtime.Evaluate(ctx, request)
		}
		captured.interpolateFn = func(ctx context.Context, input string, env map[string]any) (string, error) {
			runtime, err := captureRuntime()
			if err != nil {
				return "", err
			}
			return runtime.Interpolate(ctx, input, env)
		}
	}
	if state.currentConfigFn != nil {
		var once sync.Once
		var config model.Config
		var configErr error
		captured.activeConfigFn = func() (model.Config, error) {
			once.Do(func() {
				config, configErr = state.activeConfig()
			})
			return cloneConfig(config), configErr
		}
	}
	return captured, nil
}

func (evaluation evaluationContext) Evaluate(ctx context.Context, request js.EvalRequest) (js.Result, error) {
	if ctx == nil {
		return js.Result{}, NewConfigurationError("JavaScript evaluation context must not be nil", nil)
	}
	if evaluation.evaluateFn == nil {
		return js.Result{}, NewConfigurationError("JavaScript evaluation is unavailable", nil)
	}
	return evaluation.evaluateFn(ctx, request)
}

func (evaluation evaluationContext) Interpolate(
	ctx context.Context,
	input string,
	env map[string]any,
) (string, error) {
	if ctx == nil {
		return "", NewConfigurationError("JavaScript interpolation context must not be nil", nil)
	}
	if evaluation.interpolateFn == nil {
		return "", NewConfigurationError("JavaScript interpolation is unavailable", nil)
	}
	var clonedEnv map[string]any
	if env != nil {
		clonedEnv = cloneDynamic(env).(map[string]any)
	}
	return evaluation.interpolateFn(ctx, input, clonedEnv)
}

func (evaluation evaluationContext) ActiveConfig() (model.Config, error) {
	if evaluation.activeConfigFn != nil {
		config, err := evaluation.activeConfigFn()
		return cloneConfig(config), err
	}
	if !evaluation.hasActiveConfig {
		return model.Config{}, NewConfigurationError("active flow configuration is unavailable", nil)
	}
	return cloneConfig(evaluation.activeConfig), nil
}

func (evaluation evaluationContext) ActiveAppID() (string, error) {
	config, err := evaluation.ActiveConfig()
	if err != nil {
		return "", err
	}
	return config.EffectiveAppID(), nil
}

func (state *executionState) executeFlow(
	ctx context.Context,
	flow *compiledFlow,
	overlay map[string]string,
) (FlowResult, error) {
	if ctx == nil {
		return FlowResult{}, NewConfigurationError("nested flow execution context must not be nil", nil)
	}
	if state == nil || state.executeCompiledFlow == nil {
		return FlowResult{}, NewConfigurationError("nested flow execution is unavailable", nil)
	}
	if flow == nil {
		return FlowResult{}, NewConfigurationError("nested compiled flow must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return FlowResult{}, err
	}
	return state.executeCompiledFlow(ctx, flow, cloneStringMap(overlay), state.depth+1)
}

func (state *executionState) executeChildren(ctx context.Context, commands []model.Command) ([]CommandResult, error) {
	if state == nil || state.executeCompiledSequence == nil {
		return nil, NewConfigurationError("child command execution is unavailable", nil)
	}
	if len(commands) != len(state.compiledChildren) {
		return nil, NewConfigurationError("child commands do not match the precompiled sequence", nil)
	}
	for index, command := range commands {
		if !command.Equivalent(state.compiledChildren[index].command) {
			return nil, NewConfigurationError("child commands do not match the precompiled sequence", nil)
		}
	}
	compiled := append([]compiledDispatch(nil), state.compiledChildren...)
	return state.executeCompiledSequence(ctx, compiled, state.depth+1)
}

func (state *executionState) jsRuntime() (js.Runtime, error) {
	if state == nil || state.runtimeFn == nil {
		return nil, NewConfigurationError("JavaScript runtime is unavailable", nil)
	}
	runtime, err := state.runtimeFn()
	if err != nil {
		return nil, err
	}
	if isNilValue(runtime) {
		return nil, NewConfigurationError("JavaScript runtime is unavailable", nil)
	}
	return runtime, nil
}

func (state *executionState) elementLookup() (*ElementLookup, error) {
	if state == nil || state.lookupFn == nil {
		return nil, NewConfigurationError("element lookup is unavailable", nil)
	}
	lookup, err := state.lookupFn()
	if err != nil {
		return nil, err
	}
	if lookup == nil {
		return nil, NewConfigurationError("element lookup is unavailable", nil)
	}
	return lookup, nil
}

func (state *executionState) activeConfig() (model.Config, error) {
	if state == nil || state.currentConfigFn == nil {
		return model.Config{}, NewConfigurationError("active flow configuration is unavailable", nil)
	}
	config, err := state.currentConfigFn()
	if err != nil {
		return model.Config{}, err
	}
	return cloneConfig(config), nil
}

func (state *executionState) activeAppID() (string, error) {
	config, err := state.activeConfig()
	if err != nil {
		return "", err
	}
	return config.EffectiveAppID(), nil
}

func (state *executionState) copiedTextValue() (string, error) {
	if state == nil || state.copiedTextFn == nil {
		return "", NewConfigurationError("copied text is unavailable", nil)
	}
	return state.copiedTextFn()
}

func (state *executionState) setCopiedText(value string) error {
	if state == nil || state.setCopiedTextFn == nil {
		return NewConfigurationError("copied text mutation is unavailable", nil)
	}
	return state.setCopiedTextFn(value)
}

func (state *executionState) putEnv(name, value string) error {
	if state == nil || state.putEnvFn == nil {
		return NewConfigurationError("environment variable mutation is unavailable", nil)
	}
	return state.putEnvFn(name, value)
}

func (state *executionState) writeArtifact(ctx context.Context, request ArtifactWriteRequest) (ArtifactWriteResult, error) {
	if err := ctx.Err(); err != nil {
		return ArtifactWriteResult{}, err
	}
	if state == nil || isNilValue(state.dependencies.ArtifactSink) {
		return ArtifactWriteResult{}, NewConfigurationError("artifact sink is required by this command", nil)
	}
	result, err := state.dependencies.ArtifactSink.Write(ctx, cloneArtifactWriteRequest(request))
	result = cloneArtifactWriteResult(result)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (state *executionState) startRecording(ctx context.Context, request RecordingStartRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state == nil || isNilValue(state.dependencies.RecordingController) {
		return NewConfigurationError("recording controller is required by this command", nil)
	}
	if err := state.dependencies.RecordingController.Start(ctx, cloneRecordingStartRequest(request)); err != nil {
		return err
	}
	return ctx.Err()
}

func (state *executionState) stopRecording(ctx context.Context) ([]device.Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if state == nil || isNilValue(state.dependencies.RecordingController) {
		return nil, NewConfigurationError("recording controller is required by this command", nil)
	}
	artifacts, err := state.dependencies.RecordingController.Stop(ctx)
	artifacts = cloneDeviceArtifacts(artifacts)
	if err != nil {
		return artifacts, err
	}
	if err := ctx.Err(); err != nil {
		return artifacts, err
	}
	return artifacts, nil
}

func (state *executionState) readResource(ctx context.Context, request ResourceReadRequest) (ResourceReadResult, error) {
	if err := ctx.Err(); err != nil {
		return ResourceReadResult{}, err
	}
	if state == nil || isNilValue(state.dependencies.ResourceReader) {
		return ResourceReadResult{}, NewConfigurationError("resource reader is required by this command", nil)
	}
	result, err := state.dependencies.ResourceReader.Read(ctx, request)
	if err != nil {
		return cloneResourceReadResult(result), err
	}
	if err := ctx.Err(); err != nil {
		return ResourceReadResult{}, err
	}
	return cloneResourceReadResult(result), nil
}

func (state *executionState) generateInput(ctx context.Context, request InputRequest) (string, error) {
	if ctx == nil {
		return "", NewConfigurationError("input generation context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !request.Kind.valid() {
		return "", NewConfigurationError("input kind is invalid", nil)
	}
	switch request.Kind {
	case InputText, InputNumber:
		if request.Length < 0 || request.Length > inputVariableMaximumLength {
			return "", NewConfigurationError("text and number input length must be in [0,1024]", nil)
		}
	case InputEmail, InputPersonName, InputCityName, InputCountryName, InputColorName:
		if request.Length != inputNamedLengthSentinel {
			return "", NewConfigurationError("named input length must use the -1 sentinel", nil)
		}
	}
	if state == nil || isNilValue(state.dependencies.InputGenerator) {
		return "", NewConfigurationError("input generator is required by this command", nil)
	}
	ownedRequest := request
	generated, err := state.dependencies.InputGenerator.Generate(ctx, ownedRequest)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err != nil {
		return "", err
	}
	return generated, nil
}

func (state *executionState) checkImages(ctx context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	if err := ctx.Err(); err != nil {
		return imagecheck.Result{}, err
	}
	if state == nil || isNilValue(state.dependencies.ImageChecker) {
		return imagecheck.Result{}, NewConfigurationError("image checker is required by this command", nil)
	}
	result, err := state.dependencies.ImageChecker.Check(ctx, cloneImageCheckRequest(request))
	if err := ctx.Err(); err != nil {
		return imagecheck.Result{}, err
	}
	if err != nil {
		return result, err
	}
	if err := validateImageCheckValues(result.DifferentPixels(), result.TotalPixels(), result.DifferenceRatio()); err != nil {
		return imagecheck.Result{}, err
	}
	return result, nil
}

func validateImageCheckValues(different, total uint64, ratio float64) error {
	if total == 0 || different > total || math.IsNaN(ratio) || math.IsInf(ratio, 0) || ratio < 0 || ratio > 1 {
		return NewConfigurationError("image checker returned an invalid result", nil)
	}
	return nil
}

func cloneArtifactWriteRequest(request ArtifactWriteRequest) ArtifactWriteRequest {
	request.Data = append([]byte(nil), request.Data...)
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneArtifactWriteResult(result ArtifactWriteResult) ArtifactWriteResult {
	result.Artifact = cloneDeviceArtifact(result.Artifact)
	return result
}

func cloneRecordingStartRequest(request RecordingStartRequest) RecordingStartRequest {
	request.Metadata = cloneStringMap(request.Metadata)
	return request
}

func cloneResourceReadResult(result ResourceReadResult) ResourceReadResult {
	result.Data = append([]byte(nil), result.Data...)
	result.Metadata = cloneStringMap(result.Metadata)
	return result
}

func cloneImageCheckRequest(request ImageCheckRequest) ImageCheckRequest {
	request.Expected = append([]byte(nil), request.Expected...)
	request.Actual = append([]byte(nil), request.Actual...)
	if request.Crop != nil {
		crop := *request.Crop
		request.Crop = &crop
	}
	return request
}

func cloneDeviceArtifact(artifact device.Artifact) device.Artifact {
	artifact.Metadata = cloneStringMap(artifact.Metadata)
	return artifact
}

func cloneDeviceArtifacts(artifacts []device.Artifact) []device.Artifact {
	if artifacts == nil {
		return nil
	}
	cloned := make([]device.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		cloned[index] = cloneDeviceArtifact(artifact)
	}
	return cloned
}

// activeAppIDsFor is the app filter for a hierarchy request made outside
// ElementLookup. It reads the same value, because two answers to "which app is
// this about" in one run is a defect waiting for a screen to differ.
func activeAppIDsFor(state *executionState) []string {
	if state == nil || state.lookupFn == nil {
		return nil
	}
	lookup, err := state.lookupFn()
	if err != nil || lookup == nil {
		return nil
	}
	return lookup.activeAppIDs()
}
