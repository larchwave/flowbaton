package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

type executorCore struct {
	dispatcher dispatcher
	timeline   *Timeline
	ledger     *commandResultLedger
	state      executionState
}

const failureScreenshotArtifactKind = "failure-screenshot"

func newExecutorCore(dependencies Dependencies, specs ...handlerSpec) (*executorCore, error) {
	return newExecutorCoreForRootRun(dependencies, "", specs...)
}

func newExecutorCoreForRootRun(dependencies Dependencies, rootRunID string, specs ...handlerSpec) (*executorCore, error) {
	if err := validateCoreDependencies(dependencies); err != nil {
		return nil, err
	}
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		return nil, err
	}
	timeline, err := newTimeline(dependencies.Clock, rootRunID)
	if err != nil {
		return nil, err
	}
	dependencies.Listeners = append([]Listener(nil), dependencies.Listeners...)
	ledger := newCommandResultLedger()
	core := &executorCore{
		dispatcher: newLedgerDispatcher(registry, ledger),
		timeline:   timeline,
		ledger:     ledger,
		state:      executionState{dependencies: dependencies},
	}
	core.state.executeCompiledSequence = core.executeCompiledSequence
	return core, nil
}

func validateCoreDependencies(dependencies Dependencies) error {
	if isNilValue(dependencies.Driver) {
		return NewConfigurationError("executor core requires a device driver", nil)
	}
	if isNilValue(dependencies.Clock) {
		return NewConfigurationError("executor core requires a clock", nil)
	}
	if isNilValue(dependencies.JSFactory) {
		return NewConfigurationError("executor core requires a JavaScript factory", nil)
	}
	if isNilValue(dependencies.Controller) {
		return NewConfigurationError("executor core requires a controller", nil)
	}
	return nil
}

func (core *executorCore) execute(ctx context.Context, command model.Command, depth int) (CommandResult, error) {
	if err := validateExecutionInput(ctx, depth); err != nil {
		return CommandResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return core.finishWithoutHandler(ctx, command, depth, err)
	}
	compiled, err := core.dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return CommandResult{}, err
	}
	return core.executeCompiled(ctx, compiled, depth)
}

func (core *executorCore) executeSequence(ctx context.Context, commands []model.Command, depth int) ([]CommandResult, error) {
	if err := validateExecutionInput(ctx, depth); err != nil {
		return nil, err
	}
	if len(commands) == 0 {
		return []CommandResult{}, nil
	}
	if err := ctx.Err(); err != nil {
		candidate, finishErr := core.finishWithoutHandler(ctx, commands[0], depth, err)
		result, authenticated := core.ledger.authenticate(candidate)
		if !authenticated {
			return nil, err
		}
		return []CommandResult{result}, finishErr
	}
	compiled, err := core.dispatcher.compileSequence(ctx, compileContext{}, commands)
	if err != nil {
		return nil, err
	}
	return core.executeCompiledSequence(ctx, compiled, depth)
}

func (core *executorCore) executeCompiledSequence(ctx context.Context, compiled []compiledDispatch, depth int) ([]CommandResult, error) {
	if err := validateExecutionInput(ctx, depth); err != nil {
		return nil, err
	}
	results := make([]CommandResult, 0, len(compiled))
	for _, item := range compiled {
		candidate, executeErr := core.executeCompiled(ctx, item, depth)
		result, authenticated := core.ledger.authenticate(candidate)
		if !authenticated {
			if executeErr != nil {
				return results, executeErr
			}
			return results, NewConfigurationError("command execution returned an unauthenticated result", nil)
		}
		results = append(results, result)
		if executeErr != nil {
			return results, executeErr
		}
	}
	return results, nil
}

func validateExecutionInput(ctx context.Context, depth int) error {
	if ctx == nil {
		return NewConfigurationError("execution context must not be nil", nil)
	}
	if depth < 0 {
		return NewConfigurationError("command depth must not be negative", nil)
	}
	return nil
}

func (core *executorCore) executeCompiled(ctx context.Context, compiled compiledDispatch, depth int) (CommandResult, error) {
	if err := validateRequiredService(compiled.spec, core.state.dependencies); err != nil {
		return CommandResult{}, err
	}
	productError := ctx.Err()
	if productError == nil {
		controllerResult := WaitForController(ctx, core.state.dependencies.Controller, nil)
		if controllerFailure := controllerResult.EffectiveError(); controllerFailure != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				productError = cancellation
			} else if controllerResult.controllerFailureMalformed {
				productError = controllerFailure
			} else {
				productError = NewConfigurationError("controller wait failed", controllerFailure)
			}
		}
	}
	return core.runCommandSpan(ctx, compiled.command, &compiled, depth, productError)
}

func validateRequiredService(spec handlerSpec, dependencies Dependencies) error {
	switch spec.requiredService {
	case requiredServiceNone:
		return nil
	case requiredServiceInputGenerator:
		if isNilValue(dependencies.InputGenerator) {
			return NewConfigurationError("handler requires an input generator", nil)
		}
		return nil
	default:
		return NewConfigurationError("handler required service is invalid", nil)
	}
}

func (core *executorCore) finishWithoutHandler(ctx context.Context, command model.Command, depth int, productError error) (CommandResult, error) {
	return core.runCommandSpan(ctx, cloneCommand(command), nil, depth, productError)
}

func (core *executorCore) runCommandSpan(
	ctx context.Context,
	command model.Command,
	compiled *compiledDispatch,
	depth int,
	productError error,
) (CommandResult, error) {
	span, started, err := core.timeline.BeginCommand(command, depth)
	if err != nil {
		return CommandResult{}, err
	}
	operation := fmt.Sprintf("command %s failed", command.Kind)
	productError = sanitizeMalformedError(operation, productError)
	classificationError := productError
	DispatchListeners(ctx, started, productError, core.state.dependencies.Listeners...)
	if productError == nil {
		productError = ctx.Err()
	}

	effect := commandEffect{}
	artifacts := []device.Artifact(nil)
	if productError == nil && compiled != nil {
		handlerState := core.state
		handlerState.compiledChildren = append([]compiledDispatch(nil), compiled.children...)
		handlerState.depth = depth
		handlerState.metadataUpdatedFn = func(callbackCtx context.Context, metadata CommandMetadata) error {
			event, eventErr := span.MetadataUpdated(metadata)
			if eventErr != nil {
				return eventErr
			}
			DispatchListeners(callbackCtx, event, nil, handlerState.dependencies.Listeners...)
			return nil
		}
		handlerState.commandResetFn = func(callbackCtx context.Context, previous CommandResult) error {
			recorded, ok := core.ledger.authenticate(previous)
			if !ok {
				return NewConfigurationError("command reset requires the exact recorded child result", nil)
			}
			event, eventErr := span.CommandReset(recorded)
			if eventErr != nil {
				return eventErr
			}
			DispatchListeners(callbackCtx, event, nil, handlerState.dependencies.Listeners...)
			return nil
		}
		effect, productError = core.runHandler(ctx, &handlerState, *compiled)
		if productError == nil {
			productError = ctx.Err()
		}
		artifacts = cloneDeviceArtifacts(effect.finalizedArtifacts)
		writtenArtifacts, artifactError := core.writeArtifacts(ctx, effect.artifactWrites)
		artifacts = append(artifacts, writtenArtifacts...)
		if productError == nil && artifactError != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				productError = cancellation
			} else {
				productError = NewConfigurationError("artifact finalization failed", artifactError)
			}
		}
	}

	if disposition := effect.exactErrorDisposition; disposition != nil {
		if invalidExactErrorDisposition(operation, disposition) {
			effect = clearExactErrorState(effect)
			productError = NewConfigurationError("exact-error disposition is incoherent", nil)
			classificationError = productError
		} else {
			productError = disposition.published
			classificationError = disposition.classification
		}
	} else {
		productError = normalizeTerminalError(operation, productError)
		classificationError = productError
	}
	metadata := metadataForEffect(command, effect)
	if metadataErr := validateCommandMetadata(metadata); metadataErr != nil {
		metadata.numberOfRuns = 0
		metadata.numberOfRunsSet = false
		if productError == nil {
			productError = metadataErr
			classificationError = metadataErr
		}
	}
	outcome := ClassifyOutcome(classificationError, commandIsOptional(command))
	if artifact, finalized := core.captureFailureEvidence(ctx, span, command, outcome, classificationError); finalized {
		artifacts = append(artifacts, artifact)
	}
	result, finished, err := span.FinishWithArtifacts(outcome, productError, metadata, artifacts)
	if err != nil {
		if productError != nil {
			return CommandResult{}, productError
		}
		return CommandResult{}, err
	}
	surfaceError := surfacedExecutionError(outcome, productError)
	if effect.exactErrorDisposition != nil && surfaceError != nil {
		result.retryClassification = &retryErrorClassification{
			classification: classificationError,
			publication:    effect.exactErrorDisposition,
		}
	}
	result = sealCommandResultErrors(result)
	if err := core.ledger.record(result); err != nil {
		if productError != nil {
			return result, productError
		}
		return result, err
	}
	// The pre-seal event already owns the raw product error. Keep the separate
	// dispatch diagnostic channel empty so it cannot inspect attacker-owned errors.
	DispatchListeners(ctx, finished, nil, core.state.dependencies.Listeners...)
	return result, surfaceError
}

func (core *executorCore) captureFailureEvidence(
	ctx context.Context,
	span *CommandSpan,
	command model.Command,
	outcome Outcome,
	productError error,
) (artifact device.Artifact, finalized bool) {
	defer func() {
		if recover() != nil {
			artifact = device.Artifact{}
			finalized = false
		}
	}()

	if core == nil || span == nil || ctx == nil || ctx.Err() != nil {
		return device.Artifact{}, false
	}
	if outcome != Failed && outcome != Warned {
		return device.Artifact{}, false
	}
	classification := inspectTerminalError(productError)
	if classification.invalid || classification.class != terminalErrorRetryable {
		return device.Artifact{}, false
	}
	if isNilValue(core.state.dependencies.ArtifactSink) {
		return device.Artifact{}, false
	}

	screenshot, err := core.state.dependencies.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{Compressed: true})
	if err != nil || len(screenshot) == 0 || ctx.Err() != nil {
		return device.Artifact{}, false
	}
	metadata := map[string]string{
		"keyword":   string(command.Kind),
		"rootRunId": span.rootRunID,
		"sequence":  strconv.FormatUint(span.sequence, 10),
		"depth":     strconv.Itoa(span.depth),
		"outcome":   string(outcome),
	}
	if strings.TrimSpace(command.Source.Path) != "" {
		metadata["sourcePath"] = command.Source.Path
	}
	result, err := core.state.writeArtifact(ctx, ArtifactWriteRequest{
		Owner: span.rootRunID,
		Kind:  failureScreenshotArtifactKind,
		// Not a hardcoded .png: this capture is COMPRESSED, which on iOS means
		// JPEG bytes, and the extension has to say so.
		SuggestedName: screenshotFileName(
			fmt.Sprintf("failure-%06d", span.sequence), screenshot),
		Data:     append([]byte(nil), screenshot...),
		Metadata: metadata,
	})
	if err != nil || ctx.Err() != nil {
		return device.Artifact{}, false
	}
	if result.Artifact.Kind != failureScreenshotArtifactKind || strings.TrimSpace(result.Artifact.Path) == "" {
		return device.Artifact{}, false
	}
	return cloneDeviceArtifact(result.Artifact), true
}

func (core *executorCore) runHandler(
	ctx context.Context,
	state *executionState,
	compiled compiledDispatch,
) (effect commandEffect, productError error) {
	logs := make([]string, 0)
	if state.runtimeFn != nil {
		originalRuntimeFn := state.runtimeFn
		var once sync.Once
		var runtime js.Runtime
		var runtimeErr error
		var restoreLogSink func()
		state.runtimeFn = func() (js.Runtime, error) {
			once.Do(func() {
				runtime, runtimeErr = originalRuntimeFn()
				if runtimeErr != nil {
					return
				}
				if isNilValue(runtime) {
					runtimeErr = NewConfigurationError("JavaScript runtime is unavailable", nil)
					return
				}
				restoreLogSink = runtime.PushLogSink(func(message string) {
					logs = append(logs, message)
				})
				if restoreLogSink == nil {
					runtimeErr = NewConfigurationError("JavaScript log scope restore is unavailable", nil)
				}
			})
			return runtime, runtimeErr
		}
		defer func() {
			if restoreLogSink != nil {
				restoreLogSink()
			}
			effect.logMessages = append(append([]string(nil), logs...), effect.logMessages...)
		}()
	}

	evaluation, err := captureEvaluationContext(state)
	if err != nil {
		return effect, err
	}
	evaluated, err := core.dispatcher.evaluate(ctx, evaluation, compiled)
	if evaluated.command.Kind != "" {
		cloned := cloneCommand(evaluated.command)
		effect.evaluatedCommand = &cloned
	}
	if err != nil {
		return effect, err
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return effect, cancellation
	}

	effect, err = core.dispatcher.execute(ctx, state, compiled, evaluated)
	if effect.evaluatedCommand == nil {
		cloned := cloneCommand(evaluated.command)
		effect.evaluatedCommand = &cloned
	} else {
		cloned := cloneCommand(*effect.evaluatedCommand)
		if cloned.Kind != evaluated.command.Kind {
			fallback := cloneCommand(evaluated.command)
			effect.evaluatedCommand = &fallback
			if err == nil {
				return effect, NewConfigurationError("handler effect changed the evaluated command keyword", nil)
			}
		} else {
			effect.evaluatedCommand = &cloned
		}
	}
	if cancellation := ctx.Err(); cancellation != nil {
		if isSessionIntegrityError(err) {
			return effect, err
		}
		return clearExactErrorState(effect), cancellation
	}
	if err != nil {
		return effect, err
	}
	if err := validateSuccessfulEffect(compiled.spec.effectClass, effect.effectClass); err != nil {
		return effect, err
	}
	if err := core.applyPostAction(ctx, state, compiled, evaluated); err != nil {
		return effect, err
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return clearExactErrorState(effect), cancellation
	}
	return effect, err
}

func (core *executorCore) applyPostAction(
	ctx context.Context,
	state *executionState,
	compiled compiledDispatch,
	evaluated evaluatedDispatch,
) error {
	switch compiled.spec.postAction {
	case postActionUnspecified, postActionNoSettle:
		return nil
	case postActionSettle:
		lookup, err := state.elementLookup()
		if err != nil {
			return normalizeTerminalError(fmt.Sprintf("command %s post-action lookup failed", compiled.command.Kind), err)
		}
		lookup.RecordInteraction(state.dependencies.Clock.Now())
		request, err := invokeSettleRequestFactory(compiled.spec.settleRequest, evaluated)
		if err != nil {
			return err
		}
		_, err = lookup.WaitForHierarchySettle(ctx, request)
		return normalizeTerminalError(fmt.Sprintf("command %s settle failed", compiled.command.Kind), err)
	default:
		return NewConfigurationError("handler has an invalid post-action policy", nil)
	}
}

func invokeSettleRequestFactory(
	factory settleRequestFactory,
	evaluated evaluatedDispatch,
) (request device.SettleRequest, err error) {
	if factory == nil {
		return device.SettleRequest{}, NewConfigurationError("handler settle request factory is unavailable", nil)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			request = device.SettleRequest{}
			err = NewConfigurationError("handler settle request factory panicked", fmt.Errorf("%v", recovered))
		}
	}()
	request, err = factory(evaluated)
	if err != nil {
		return device.SettleRequest{}, NewConfigurationError("handler settle request factory failed", err)
	}
	if strings.TrimSpace(request.AppID) == "" {
		return device.SettleRequest{}, NewConfigurationError("handler settle request requires a non-blank app ID", nil)
	}
	if request.TimeoutMillis != nil && *request.TimeoutMillis < 0 {
		return device.SettleRequest{}, NewConfigurationError("handler settle request timeout must not be negative", nil)
	}
	return request, nil
}

func validateSuccessfulEffect(declared, returned effectClass) error {
	if declared == EffectNone || returned == EffectNone {
		return NewConfigurationError("handler reported success without an effect", nil)
	}
	if returned != declared {
		return NewConfigurationError("handler returned an effect outside its registered class", nil)
	}
	return nil
}

func (core *executorCore) writeArtifacts(ctx context.Context, requests []ArtifactWriteRequest) ([]device.Artifact, error) {
	artifacts := make([]device.Artifact, 0, len(requests))
	for _, request := range requests {
		result, err := core.state.writeArtifact(ctx, request)
		if err != nil {
			if artifactIsNonzero(result.Artifact) {
				artifacts = append(artifacts, result.Artifact)
			}
			return artifacts, err
		}
		artifacts = append(artifacts, result.Artifact)
	}
	return artifacts, nil
}

func artifactIsNonzero(artifact device.Artifact) bool {
	return artifact.Kind != "" || artifact.Path != "" || artifact.Metadata != nil
}

func metadataForEffect(command model.Command, effect commandEffect) CommandMetadata {
	runs := 1
	if effect.numberOfRunsSet {
		runs = effect.numberOfRuns
	}
	evaluated := effect.evaluatedCommand
	if evaluated == nil {
		cloned := cloneCommand(command)
		evaluated = &cloned
	}
	return NewCommandMetadata(runs, evaluated, effect.logMessages, effect.insight, effect.aiReasoning)
}

func commandIsOptional(command model.Command) bool {
	if command.Optional != nil {
		return *command.Optional
	}
	// AI commands default to optional=true when the author did not set the flag
	// (specs/01-core-engine.md): a missing provider or a failed AI assertion
	// warns rather than aborts the flow unless optional:false is authored.
	switch command.Kind {
	case model.CommandAssertWithAI, model.CommandAssertNoDefectsWithAI, model.CommandExtractTextWithAI:
		return true
	default:
		return false
	}
}

func surfacedExecutionError(outcome Outcome, productError error) error {
	switch outcome {
	case Completed, Warned, Skipped:
		return nil
	case Failed, Cancelled:
		return productError
	default:
		return NewConfigurationError("command produced an invalid outcome", nil)
	}
}
