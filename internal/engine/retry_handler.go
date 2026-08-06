package engine

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/model"
)

const retrySuccessInsight = "Command succeeded after retry"

type retrySource uint8

const (
	retryLinkedSource retrySource = iota + 1
	retryInlineSource
)

type retryCounterKind uint8

const (
	retryCounterMissing retryCounterKind = iota + 1
	retryCounterFixed
	retryCounterDynamic
)

type retryCompiled struct {
	source               retrySource
	linked               *compiledFlow
	counterKind          retryCounterKind
	fixedMaxRetries      int
	maxRetriesExpression string
}

type retryEvaluated struct {
	source     retrySource
	linked     *compiledFlow
	maxRetries int
}

type retryDeferredEvaluation struct {
	compiled retryCompiled
}

type retryAttemptResult struct {
	results             []CommandResult
	directSkipped       bool
	productError        error
	retryClassification *retryErrorClassification
	exactErrorSource    *CommandResult
}

func retryHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandRetry, effectClass: EffectComposite,
		exactErrorPolicy: exactErrorPublicationRetryPropagation,
		compile:          compileRetry, evaluate: evaluateRetryForExecution, execute: executeRetry,
	}
}

func compileRetry(_ context.Context, compileCtx compileContext, command model.Command) (any, error) {
	object, err := decodeObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("maxRetries", "commands", "file"); err != nil {
		return nil, err
	}
	// `label` and `optional` are universal metadata.
	if command.Selector != nil || command.Condition != nil {
		return nil, commandDecodeError(command.Kind, "contains unsupported typed metadata")
	}

	_, hasFile := object.raw("file")
	rawCommands, hasCommands := object.raw("commands")
	if hasFile == hasCommands {
		return nil, commandDecodeError(command.Kind, "requires exactly one of file or commands")
	}

	payload := retryCompiled{counterKind: retryCounterMissing}
	rawMaxRetries, hasMaxRetries := object.raw("maxRetries")
	if hasMaxRetries {
		switch value := rawMaxRetries.(type) {
		case int64:
			if value < 0 || value > RetryCommandMaxRetries {
				return nil, object.fieldError("maxRetries", "must be between zero and three")
			}
			payload.counterKind = retryCounterFixed
			payload.fixedMaxRetries = int(value)
		case string:
			payload.counterKind = retryCounterDynamic
			payload.maxRetriesExpression = value
		default:
			return nil, object.fieldError("maxRetries", "must be an int64 integer or string")
		}
	}

	if hasFile {
		file, _, err := object.optionalString("file")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(file) == "" {
			return nil, object.fieldError("file", "must not be blank")
		}
		if len(command.Children) != 0 {
			return nil, commandDecodeError(command.Kind, "file form must not contain inline child commands")
		}
		if len(command.Links) != 1 {
			return nil, commandDecodeError(command.Kind, "file form requires exactly one prepared flow link")
		}
		link := command.Links[0]
		if link.Kind != model.FileLinkFlow {
			return nil, commandDecodeError(command.Kind, "file link must be a flow")
		}
		if strings.TrimSpace(link.Path) == "" || link.Path != file {
			return nil, commandDecodeError(command.Kind, "file argument does not match its prepared flow link")
		}
		linked, err := compileCtx.RequireFlow(link)
		if err != nil {
			return nil, err
		}
		if linked == nil {
			return nil, NewConfigurationError("retry linked compiler returned a nil flow", nil)
		}
		payload.source = retryLinkedSource
		payload.linked = linked
		return payload, nil
	}

	sequence, ok := rawCommands.([]any)
	if !ok {
		return nil, object.fieldError("commands", "must be an array")
	}
	if !retryRawChildrenMatch(sequence, command.Children) {
		return nil, object.fieldError("commands", "does not match the typed child sequence")
	}
	if len(command.Links) != 0 {
		return nil, commandDecodeError(command.Kind, "inline form must not contain file links")
	}
	payload.source = retryInlineSource
	return payload, nil
}

func retryRawChildrenMatch(raw []any, typed []model.Command) bool {
	if len(raw) != len(typed) {
		return false
	}
	for index, command := range typed {
		switch command.Form {
		case model.CommandFormScalar:
			keyword, ok := raw[index].(string)
			if !ok || keyword != string(command.Kind) || command.Arguments != nil {
				return false
			}
		case model.CommandFormObject:
			object, ok := raw[index].(map[string]any)
			arguments, exists := object[string(command.Kind)]
			if !ok || !exists || len(object) != 1 || !reflect.DeepEqual(arguments, command.Arguments) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func evaluateRetryForExecution(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(retryCompiled)
	if !ok || !payload.valid() {
		return evaluatedDispatch{}, NewConfigurationError("retry received an invalid compiled payload", nil)
	}
	if payload.counterKind != retryCounterDynamic {
		return evaluateRetry(ctx, evaluation, command, payload)
	}
	return evaluatedDispatch{
		command: cloneCommand(command),
		value:   retryDeferredEvaluation{compiled: payload},
	}, nil
}

func evaluateRetry(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(retryCompiled)
	if !ok || !payload.valid() {
		return evaluatedDispatch{}, NewConfigurationError("retry received an invalid compiled payload", nil)
	}
	evaluated := cloneCommand(command)
	maxRetries := 1
	switch payload.counterKind {
	case retryCounterMissing:
	case retryCounterFixed:
		maxRetries = payload.fixedMaxRetries
	case retryCounterDynamic:
		interpolated, err := evaluation.Interpolate(ctx, payload.maxRetriesExpression, nil)
		if err != nil {
			return evaluatedDispatch{command: evaluated}, err
		}
		parsed, err := parseRetryMaxRetries(interpolated)
		if err != nil {
			return evaluatedDispatch{command: evaluated}, err
		}
		maxRetries = parsed
		synchronizeRetryMaxRetries(&evaluated, interpolated)
	default:
		return evaluatedDispatch{}, NewConfigurationError("retry counter kind is invalid", nil)
	}
	return evaluatedDispatch{
		command: evaluated,
		value: retryEvaluated{
			source: payload.source, linked: payload.linked, maxRetries: maxRetries,
		},
	}, nil
}

func (payload retryCompiled) valid() bool {
	if !validRetrySource(payload.source, payload.linked) {
		return false
	}
	switch payload.counterKind {
	case retryCounterMissing:
		return payload.fixedMaxRetries == 0 && payload.maxRetriesExpression == ""
	case retryCounterFixed:
		return payload.fixedMaxRetries >= 0 && payload.fixedMaxRetries <= RetryCommandMaxRetries &&
			payload.maxRetriesExpression == ""
	case retryCounterDynamic:
		return payload.fixedMaxRetries == 0
	default:
		return false
	}
}

func (payload retryEvaluated) valid() bool {
	return validRetrySource(payload.source, payload.linked) &&
		payload.maxRetries >= 0 && payload.maxRetries <= RetryCommandMaxRetries
}

func validRetrySource(source retrySource, linked *compiledFlow) bool {
	switch source {
	case retryLinkedSource:
		return linked != nil
	case retryInlineSource:
		return linked == nil
	default:
		return false
	}
}

func parseRetryMaxRetries(value string) (int, error) {
	if value == "" {
		return 0, NewConfigurationError("command retry field maxRetries must be an unsigned base-10 Int32", nil)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, NewConfigurationError("command retry field maxRetries must be an unsigned base-10 Int32", nil)
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 31)
	if err != nil {
		return 0, NewConfigurationError("command retry field maxRetries must be an unsigned base-10 Int32", err)
	}
	if parsed > RetryCommandMaxRetries {
		return 0, NewConfigurationError("command retry field maxRetries must not exceed three", nil)
	}
	return int(parsed), nil
}

func synchronizeRetryMaxRetries(command *model.Command, value string) {
	if command == nil {
		return
	}
	arguments, ok := command.Arguments.(map[string]any)
	if ok {
		arguments["maxRetries"] = value
	}
}

func executeRetry(
	ctx context.Context,
	state *executionState,
	evaluated evaluatedDispatch,
) (commandEffect, error) {
	effect := commandEffect{
		effectClass: EffectComposite, numberOfRunsSet: true,
		evaluatedCommand: clonedCommandPointer(evaluated.command),
	}
	if deferred, ok := evaluated.value.(retryDeferredEvaluation); ok {
		evaluation, err := captureEvaluationContext(state)
		if err != nil {
			return effect, err
		}
		resolved, err := evaluateRetry(ctx, evaluation, evaluated.command, deferred.compiled)
		if resolved.command.Kind != "" {
			effect.evaluatedCommand = clonedCommandPointer(resolved.command)
		}
		if err != nil {
			return effect, err
		}
		evaluated = resolved
		effect.evaluatedCommand = clonedCommandPointer(evaluated.command)
	}
	payload, ok := evaluated.value.(retryEvaluated)
	if !ok || !payload.valid() {
		return effect, NewConfigurationError("retry received an invalid evaluated payload", nil)
	}
	if ctx == nil {
		return effect, NewConfigurationError("retry execution context must not be nil", nil)
	}
	if state == nil {
		return effect, NewConfigurationError("retry execution state must not be nil", nil)
	}

	for attempt := 0; attempt <= payload.maxRetries; attempt++ {
		if cancellation := ctx.Err(); cancellation != nil {
			return effect, cancellation
		}
		effect.numberOfRuns++
		attemptResult := executeRetryAttempt(ctx, state, evaluated.command, payload)
		results := attemptResult.results
		attemptErr := attemptResult.productError
		var exactErrorPropagation *exactErrorPropagationRequest
		attemptClassification := attemptErr
		if attemptResult.retryClassification != nil && attemptResult.retryClassification.classification != nil {
			attemptClassification = unsealResultError(attemptResult.retryClassification.classification)
			if attemptResult.retryClassification.publication != nil && attemptResult.exactErrorSource != nil {
				source := cloneCommandResult(*attemptResult.exactErrorSource)
				exactErrorPropagation = &exactErrorPropagationRequest{source: &source}
			}
		}
		if cancellation := ctx.Err(); cancellation != nil {
			attemptErr = cancellation
			attemptClassification = cancellation
			exactErrorPropagation = nil
		}
		if attemptErr == nil && attempt > 0 {
			effect.insight = retrySuccessInsight
		}
		metadataErr := state.updateCommandMetadata(ctx, NewCommandMetadata(
			effect.numberOfRuns, effect.evaluatedCommand, nil, effect.insight, "",
		))
		if cancellation := ctx.Err(); cancellation != nil {
			attemptErr = cancellation
			attemptClassification = cancellation
			exactErrorPropagation = nil
			effect.insight = ""
		}
		if attemptErr == nil {
			if metadataErr != nil {
				effect.insight = ""
				return effect, metadataErr
			}
			return effect, nil
		}
		if metadataErr != nil {
			return effect, attemptErr
		}
		if attemptResult.directSkipped || !IsRetryable(attemptClassification) {
			return effect, attemptErr
		}
		if attempt == payload.maxRetries {
			if exactErrorPropagation != nil {
				effect.exactErrorPropagation = exactErrorPropagation
				return effect, nil
			}
			return effect, attemptErr
		}
		for _, previous := range results {
			resetErr := state.resetCommand(ctx, previous)
			if cancellation := ctx.Err(); cancellation != nil {
				return effect, cancellation
			}
			if resetErr != nil {
				return effect, resetErr
			}
		}
	}
	return effect, NewConfigurationError("retry attempts exceeded their validated bound", nil)
}

func retryClassificationError(source retrySource, results []CommandResult, fallback error) error {
	if source != retryInlineSource {
		return fallback
	}
	origin := inlineRetryClassification(results)
	if origin == nil || origin.classification == nil {
		return fallback
	}
	return unsealResultError(origin.classification)
}

func inlineRetryClassification(results []CommandResult) *retryErrorClassification {
	if len(results) == 0 {
		return nil
	}
	result := results[len(results)-1]
	if result.productError == nil {
		return nil
	}
	return result.retryClassification
}

func executeRetryAttempt(
	ctx context.Context,
	state *executionState,
	command model.Command,
	payload retryEvaluated,
) retryAttemptResult {
	switch payload.source {
	case retryInlineSource:
		results, err := state.executeChildren(ctx, command.Children)
		immediate := immediateRetryResults(results, state.depth+1)
		skippedErr := skippedRetryResultError(immediate)
		if err == nil {
			err = skippedErr
		}
		attempt := retryAttemptResult{
			results: immediate, directSkipped: skippedErr != nil, productError: err,
		}
		if skippedErr == nil && err != nil {
			attempt.retryClassification = inlineRetryClassification(immediate)
			if attempt.retryClassification != nil && attempt.retryClassification.publication != nil && len(immediate) != 0 {
				source := cloneCommandResult(immediate[len(immediate)-1])
				attempt.exactErrorSource = &source
			}
		}
		return attempt
	case retryLinkedSource:
		result, err := state.executeFlow(ctx, payload.linked, nil)
		immediate := immediateRetryResults(result.Commands(), state.depth+1)
		skippedErr := skippedRetryResultError(immediate)
		if err == nil {
			err = skippedErr
		}
		attempt := retryAttemptResult{
			results: immediate, directSkipped: skippedErr != nil, productError: err,
		}
		if skippedErr == nil && err != nil {
			attempt.retryClassification = result.retryClassification
			if result.exactErrorSource != nil {
				source := cloneCommandResult(*result.exactErrorSource)
				attempt.exactErrorSource = &source
			}
		}
		return attempt
	default:
		return retryAttemptResult{productError: NewConfigurationError("retry source is invalid", nil)}
	}
}

func skippedRetryResultError(results []CommandResult) error {
	for _, result := range results {
		if productError := result.ProductError(); IsCommandSkipped(productError) {
			return productError
		}
	}
	return nil
}

func immediateRetryResults(results []CommandResult, depth int) []CommandResult {
	immediate := make([]CommandResult, 0, len(results))
	for _, result := range results {
		if result.Depth() == depth {
			immediate = append(immediate, result)
		}
	}
	return immediate
}
