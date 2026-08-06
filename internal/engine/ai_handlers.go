package engine

import (
	"context"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

// AI commands (specs/01-core-engine.md). All three are screenshot-based: they
// capture one uncompressed PNG and hand it to the injected AIPredictionEngine.
// A nil engine fails closed with ErrCloudAPIKeyNotAvailable. Every AI command
// defaults optional=true (see commandIsOptional), so a missing provider or a
// failed assertion warns rather than aborts unless the author set optional:false.

type assertNoDefectsWithAICompiled struct{}

type assertNoDefectsWithAIEvaluated struct{}

type assertWithAICompiled struct {
	assertion string
}

type assertWithAIEvaluated struct {
	assertion string
}

type extractTextWithAICompiled struct {
	query          string
	outputVariable string
}

type extractTextWithAIEvaluated struct {
	query          string
	outputVariable string
}

func aiHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandAssertNoDefectsWithAI, effectClass: EffectObserved,
			compile: pureCompiler(compileAssertNoDefectsWithAI), evaluate: evaluateAssertNoDefectsWithAI, execute: executeAssertNoDefectsWithAI,
		},
		{
			keyword: model.CommandAssertWithAI, effectClass: EffectObserved,
			compile: pureCompiler(compileAssertWithAI), evaluate: evaluateAssertWithAI, execute: executeAssertWithAI,
		},
		{
			keyword: model.CommandExtractTextWithAI, effectClass: EffectHostMutation,
			compile: pureCompiler(compileExtractTextWithAI), evaluate: evaluateExtractTextWithAI, execute: executeExtractTextWithAI,
		},
	}
}

// validateAITypedMetadata requires authored label and optional values to match
// the typed command snapshot produced by the parser.
func validateAITypedMetadata(object decodedObject, command model.Command) error {
	label, hasLabel, err := object.optionalString("label")
	if err != nil {
		return err
	}
	if hasLabel != (command.Label != nil) || hasLabel && label != *command.Label {
		return NewConfigurationError("command "+string(command.Kind)+" label does not match its typed snapshot", nil)
	}
	optional, hasOptional, err := object.optionalBool("optional")
	if err != nil {
		return err
	}
	if hasOptional != (command.Optional != nil) || hasOptional && optional != *command.Optional {
		return NewConfigurationError("command "+string(command.Kind)+" optional flag does not match its typed snapshot", nil)
	}
	return nil
}

func rejectAICommandStructure(command model.Command) error {
	if command.Selector != nil || command.Condition != nil {
		return commandDecodeError(command.Kind, "must not contain selector or condition metadata")
	}
	if len(command.Children) != 0 {
		return commandDecodeError(command.Kind, "must not contain child commands")
	}
	return nil
}

func compileAssertNoDefectsWithAI(command model.Command) (any, error) {
	if command.Kind != model.CommandAssertNoDefectsWithAI {
		return nil, NewConfigurationError("assertNoDefectsWithAI keyword is invalid", nil)
	}
	if err := rejectAICommandStructure(command); err != nil {
		return nil, err
	}
	object, hasObject, err := decodeOptionalObject(command)
	if err != nil {
		return nil, err
	}
	if !hasObject {
		// Bare `- assertNoDefectsWithAI` carries no label or optional metadata.
		if command.Label != nil || command.Optional != nil {
			return nil, commandDecodeError(command.Kind, "bare form must not contain label or optional metadata")
		}
		return assertNoDefectsWithAICompiled{}, nil
	}
	if err := object.rejectUnknown("label", "optional"); err != nil {
		return nil, err
	}
	if err := validateAITypedMetadata(object, command); err != nil {
		return nil, err
	}
	return assertNoDefectsWithAICompiled{}, nil
}

func evaluateAssertNoDefectsWithAI(
	_ context.Context,
	_ evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if _, ok := compiled.(assertNoDefectsWithAICompiled); !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertNoDefectsWithAI received an invalid compiled payload", nil)
	}
	return evaluatedDispatch{command: cloneCommand(command), value: assertNoDefectsWithAIEvaluated{}}, nil
}

func executeAssertNoDefectsWithAI(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectObserved}
	if _, ok := evaluated.value.(assertNoDefectsWithAIEvaluated); !ok {
		return effect, NewConfigurationError("assertNoDefectsWithAI received an invalid evaluated payload", nil)
	}
	engine, screenshot, err := captureForAI(ctx, state, model.CommandAssertNoDefectsWithAI)
	if err != nil {
		return effect, err
	}
	result, err := engine.FindDefects(ctx, screenshot)
	if err != nil {
		return effect, err
	}
	effect.aiReasoning = result.Reasoning
	if len(result.Defects) != 0 {
		return effect, NewAssertionError("assertNoDefectsWithAI found "+joinDefects(result.Defects), nil)
	}
	return effect, nil
}

func compileAssertWithAI(command model.Command) (any, error) {
	if command.Kind != model.CommandAssertWithAI {
		return nil, NewConfigurationError("assertWithAI keyword is invalid", nil)
	}
	if err := rejectAICommandStructure(command); err != nil {
		return nil, err
	}
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	if decoded.stringValue != nil {
		if command.Label != nil || command.Optional != nil {
			return nil, commandDecodeError(command.Kind, "scalar assertion must not contain label or optional metadata")
		}
		if strings.TrimSpace(*decoded.stringValue) == "" {
			return nil, commandDecodeError(command.Kind, "assertion must not be blank")
		}
		return assertWithAICompiled{assertion: *decoded.stringValue}, nil
	}
	object := *decoded.objectValue
	if err := object.rejectUnknown("assertion", "label", "optional"); err != nil {
		return nil, err
	}
	assertion, err := object.requireString("assertion")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(assertion) == "" {
		return nil, commandDecodeError(command.Kind, "assertion must not be blank")
	}
	if err := validateAITypedMetadata(object, command); err != nil {
		return nil, err
	}
	return assertWithAICompiled{assertion: assertion}, nil
}

func evaluateAssertWithAI(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(assertWithAICompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("assertWithAI received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: assertWithAIEvaluated{}}
	assertion, err := evaluation.Interpolate(ctx, payload.assertion, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(assertion) == "" {
		return evaluated, NewConfigurationError("assertWithAI assertion must not be blank after interpolation", nil)
	}
	evaluated.value = assertWithAIEvaluated{assertion: assertion}
	return evaluated, nil
}

func executeAssertWithAI(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectObserved}
	payload, ok := evaluated.value.(assertWithAIEvaluated)
	if !ok {
		return effect, NewConfigurationError("assertWithAI received an invalid evaluated payload", nil)
	}
	engine, screenshot, err := captureForAI(ctx, state, model.CommandAssertWithAI)
	if err != nil {
		return effect, err
	}
	result, err := engine.PerformAssertion(ctx, screenshot, payload.assertion)
	if err != nil {
		return effect, err
	}
	effect.aiReasoning = result.Reasoning
	if !result.Pass {
		return effect, NewAssertionError("assertWithAI assertion did not hold: "+payload.assertion, nil)
	}
	return effect, nil
}

func compileExtractTextWithAI(command model.Command) (any, error) {
	if command.Kind != model.CommandExtractTextWithAI {
		return nil, NewConfigurationError("extractTextWithAI keyword is invalid", nil)
	}
	if err := rejectAICommandStructure(command); err != nil {
		return nil, err
	}
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("query", "outputVariable", "label", "optional"); err != nil {
		return nil, err
	}
	query, err := object.requireString("query")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(query) == "" {
		return nil, commandDecodeError(command.Kind, "query must not be blank")
	}
	// extractTextWithAI requires query and allows an optional non-blank
	// outputVariable.
	outputVariable, hasOutputVariable, err := object.optionalString("outputVariable")
	if err != nil {
		return nil, err
	}
	if hasOutputVariable && strings.TrimSpace(outputVariable) == "" {
		return nil, commandDecodeError(command.Kind, "outputVariable must not be blank")
	}
	if err := validateAITypedMetadata(object, command); err != nil {
		return nil, err
	}
	return extractTextWithAICompiled{query: query, outputVariable: outputVariable}, nil
}

func evaluateExtractTextWithAI(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(extractTextWithAICompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("extractTextWithAI received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: extractTextWithAIEvaluated{}}
	query, err := evaluation.Interpolate(ctx, payload.query, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(query) == "" {
		return evaluated, NewConfigurationError("extractTextWithAI query must not be blank after interpolation", nil)
	}
	// outputVariable is a variable name, not an interpolated value, so it is
	// carried through literally.
	evaluated.value = extractTextWithAIEvaluated{query: query, outputVariable: payload.outputVariable}
	return evaluated, nil
}

func executeExtractTextWithAI(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectHostMutation}
	payload, ok := evaluated.value.(extractTextWithAIEvaluated)
	if !ok {
		return effect, NewConfigurationError("extractTextWithAI received an invalid evaluated payload", nil)
	}
	engine, screenshot, err := captureForAI(ctx, state, model.CommandExtractTextWithAI)
	if err != nil {
		return effect, err
	}
	result, err := engine.ExtractText(ctx, screenshot, payload.query)
	if err != nil {
		return effect, err
	}
	effect.aiReasoning = result.Reasoning
	// outputVariable is optional (operation contract); with none present the extracted
	// text has nowhere to bind, so skip the env write instead of storing "".
	if payload.outputVariable != "" {
		if err := state.putEnv(payload.outputVariable, result.Text); err != nil {
			return effect, err
		}
	}
	return effect, nil
}

// captureForAI resolves the engine and captures one screenshot shared by every
// AI command. A nil engine fails closed before any capture is spent.
func captureForAI(ctx context.Context, state *executionState, keyword model.CommandKeyword) (AIPredictionEngine, []byte, error) {
	if ctx == nil {
		return nil, nil, NewConfigurationError(string(keyword)+" execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if state == nil || isNilValue(state.dependencies.AIEngine) {
		return nil, nil, newCloudAPIKeyNotAvailableError(string(keyword))
	}
	screenshot, err := state.dependencies.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{})
	if err != nil {
		return nil, nil, err
	}
	return state.dependencies.AIEngine, screenshot, nil
}

func joinDefects(defects []string) string {
	if len(defects) == 0 {
		return "no defects"
	}
	return strings.Join(defects, "; ")
}
