package engine

import (
	"context"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/model"
)

type setClipboardCompiled struct {
	authored string
}

type setClipboardEvaluated struct {
	value string
}

type copyTextFromCompiled struct{}

type copyTextFromEvaluated struct {
	selector *model.ElementSelector
	optional bool
}

type copyTextFromLookup interface {
	Find(context.Context, model.ElementSelector, LookupOptions) (*hierarchy.Element, error)
	WaitForElementStability(context.Context, *hierarchy.Element) (ElementStabilityResult, error)
}

var _ copyTextFromLookup = (*ElementLookup)(nil)

type pasteTextCompiled struct{}

type pasteTextEvaluated struct {
	appID string
}

// clipboardHandlerSpecs returns the clipboard handlers. The production
// registry deliberately does not call this helper before the public exposure
// checkpoint.
func clipboardHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandCopyTextFrom, effectClass: EffectHostMutation,
			compile: pureCompiler(compileCopyTextFrom), evaluate: evaluateCopyTextFrom, execute: executeCopyTextFrom,
		},
		{
			keyword: model.CommandSetClipboard, effectClass: EffectHostMutation,
			compile: pureCompiler(compileSetClipboard), evaluate: evaluateSetClipboard, execute: executeSetClipboard,
		},
		{
			keyword: model.CommandPasteText, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compilePasteText), evaluate: evaluatePasteText, execute: executePasteText,
		},
	}
}

func compileSetClipboard(command model.Command) (any, error) {
	if command.Kind != model.CommandSetClipboard {
		return nil, NewConfigurationError("setClipboard keyword is invalid", nil)
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "accepts only one scalar string")
	}
	authored, err := decodeString(command)
	if err != nil {
		return nil, err
	}
	return setClipboardCompiled{authored: authored}, nil
}

func evaluateSetClipboard(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	plan, ok := compiled.(setClipboardCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("setClipboard received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: setClipboardEvaluated{}}
	value, err := evaluation.Interpolate(ctx, plan.authored, nil)
	if err != nil {
		return evaluated, err
	}
	evaluated.command.Arguments = value
	evaluated.value = setClipboardEvaluated{value: value}
	return evaluated, nil
}

func executeSetClipboard(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectHostMutation}
	plan, ok := evaluated.value.(setClipboardEvaluated)
	if !ok {
		return effect, NewConfigurationError("setClipboard received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("setClipboard execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	if err := state.setCopiedText(plan.value); err != nil {
		return effect, err
	}
	return effect, nil
}

func compileCopyTextFrom(command model.Command) (any, error) {
	if command.Kind != model.CommandCopyTextFrom {
		return nil, NewConfigurationError("copyTextFrom keyword is invalid", nil)
	}
	if command.Condition != nil || len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "accepts only an exact selector with command-level label and optional")
	}
	if err := validateSelectorCommand(command); err != nil {
		return nil, err
	}
	if !selectorCommandSnapshotMatches(command) {
		return nil, NewConfigurationError("command copyTextFrom selector, label, or optional flag does not match its typed snapshot", nil)
	}
	if err := rejectUnsupportedSelectorFeatures(command.Kind, command.Selector, false, true); err != nil {
		return nil, err
	}
	return copyTextFromCompiled{}, nil
}

func evaluateCopyTextFrom(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if _, ok := compiled.(copyTextFromCompiled); !ok {
		return evaluatedDispatch{}, NewConfigurationError("copyTextFrom received an invalid compiled payload", nil)
	}
	evaluated, err := evaluateSelectorCommand(ctx, evaluation, command)
	evaluated.value = copyTextFromEvaluated{}
	if err != nil {
		return evaluated, err
	}
	if evaluated.command.Selector == nil {
		return evaluated, NewConfigurationError("copyTextFrom evaluated selector is unavailable", nil)
	}
	evaluated.command.Optional = clonePointer(evaluated.command.Selector.Optional)
	evaluated.command.Label = clonePointer(evaluated.command.Selector.Label)
	evaluated.value = copyTextFromEvaluated{
		selector: cloneSelector(evaluated.command.Selector),
		optional: commandIsOptional(evaluated.command),
	}
	return evaluated, nil
}

func executeCopyTextFrom(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectHostMutation}
	plan, ok := evaluated.value.(copyTextFromEvaluated)
	if !ok || plan.selector == nil {
		return effect, NewConfigurationError("copyTextFrom received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("copyTextFrom execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	lookup, err := state.elementLookup()
	if err != nil {
		return effect, err
	}
	err = copyTextFromLookupAndSet(
		ctx,
		lookup,
		*plan.selector,
		LookupOptions{Optional: plan.optional},
		state.setCopiedText,
	)
	return effect, err
}

func copyTextFromLookupAndSet(
	ctx context.Context,
	lookup copyTextFromLookup,
	selector model.ElementSelector,
	options LookupOptions,
	setter func(string) error,
) error {
	element, err := lookup.Find(ctx, *cloneSelector(&selector), options)
	if err != nil {
		return err
	}
	if element == nil {
		return NewOperationError("copyTextFrom target not found", nil)
	}
	stability, err := lookup.WaitForElementStability(ctx, element)
	if err != nil {
		return err
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return cancellation
	}
	value, err := copyTextFromAcceptedElement(element, stability.Element)
	if err != nil {
		return err
	}
	if cancellation := ctx.Err(); cancellation != nil {
		return cancellation
	}
	return setter(value)
}

func copyTextFromAcceptedElement(_, accepted *hierarchy.Element) (string, error) {
	if accepted == nil {
		return "", NewConfigurationError("copyTextFrom stability returned no element", nil)
	}
	value, copyable := copyTextFromElementValue(accepted)
	if !copyable {
		return "", NewOperationError("copyTextFrom target has no text to copy", nil)
	}
	return value, nil
}

// copyTextFromElementValue returns the target's copyable text and whether the
// target carried any copyable attribute at all. A present-but-empty text
// attribute is copyable and wins over accessibility and hint text; an element
// carrying none of the three attributes has no text to copy, which the caller
// turns into a command failure.
func copyTextFromElementValue(element *hierarchy.Element) (string, bool) {
	if element == nil {
		return "", false
	}
	attributes := element.Node.Attributes
	for _, name := range []string{"text", "accessibilityText", "hintText"} {
		if value, exists := attributes[name]; exists {
			return strings.Clone(value), true
		}
	}
	return "", false
}

func compilePasteText(command model.Command) (any, error) {
	if command.Kind != model.CommandPasteText {
		return nil, NewConfigurationError("pasteText keyword is invalid", nil)
	}
	if err := decodeNoArguments(command); err != nil {
		return nil, err
	}
	if len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return nil, commandDecodeError(command.Kind, "must be a bare command without metadata, selector, or children")
	}
	return pasteTextCompiled{}, nil
}

func evaluatePasteText(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	if _, ok := compiled.(pasteTextCompiled); !ok {
		return evaluatedDispatch{}, NewConfigurationError("pasteText received an invalid compiled payload", nil)
	}
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: pasteTextEvaluated{}}
	appID, err := evaluatedActiveAppID(ctx, evaluation, command.Kind)
	if err != nil {
		return evaluated, err
	}
	evaluated.value = pasteTextEvaluated{appID: strings.Clone(appID)}
	return evaluated, nil
}

func executePasteText(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	plan, ok := evaluated.value.(pasteTextEvaluated)
	if !ok {
		return effect, NewConfigurationError("pasteText received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("pasteText execution context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	text, err := state.copiedTextValue()
	if err != nil {
		return effect, err
	}
	lookup, err := batch2AExecutionLookup(state, model.CommandPasteText, plan.appID)
	if err != nil {
		return effect, err
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	request := device.InputTextRequest{Text: strings.Clone(text), AppIDs: []string{strings.Clone(plan.appID)}}
	if err := state.dependencies.Driver.InputText(ctx, request); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		return effect, err
	}
	return finishBatch2APhysicalAction(ctx, state, lookup, model.CommandPasteText, plan.appID, effect)
}
