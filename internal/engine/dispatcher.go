package engine

import (
	"context"
	"fmt"
	"reflect"

	"github.com/nohavewho/flowbaton/internal/model"
)

type compiledDispatch struct {
	command  model.Command
	spec     handlerSpec
	value    any
	children []compiledDispatch
}

type dispatcher struct {
	registry handlerRegistry
	ledger   *commandResultLedger
}

func newDispatcher(registry handlerRegistry) dispatcher {
	return dispatcher{registry: registry}
}

func newLedgerDispatcher(registry handlerRegistry, ledger *commandResultLedger) dispatcher {
	return dispatcher{registry: registry, ledger: ledger}
}

func (dispatcher dispatcher) compile(ctx context.Context, compileCtx compileContext, command model.Command) (compiledDispatch, error) {
	if ctx == nil {
		return compiledDispatch{}, NewConfigurationError("command compilation context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return compiledDispatch{}, err
	}
	spec, ok := dispatcher.registry.lookup(command.Kind)
	if !ok {
		return compiledDispatch{}, NewConfigurationError("command has no registered handler", nil)
	}
	original := cloneCommand(command)
	value, err := invokeCompiler(ctx, spec.compile, compileCtx, cloneCommand(command))
	if cancellation := ctx.Err(); cancellation != nil {
		return compiledDispatch{}, cancellation
	}
	if err != nil {
		return compiledDispatch{}, err
	}
	if isNilValue(value) {
		return compiledDispatch{}, NewConfigurationError("handler compiler returned nil without an error", nil)
	}
	children, err := dispatcher.compileSequence(ctx, compileCtx, original.Children)
	if err != nil {
		return compiledDispatch{}, err
	}
	return compiledDispatch{command: original, spec: spec, value: value, children: children}, nil
}

func (dispatcher dispatcher) compileSequence(ctx context.Context, compileCtx compileContext, commands []model.Command) ([]compiledDispatch, error) {
	if ctx == nil {
		return nil, NewConfigurationError("command compilation context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	compiled := make([]compiledDispatch, len(commands))
	for index, command := range commands {
		item, err := dispatcher.compile(ctx, compileCtx, command)
		if err != nil {
			return nil, err
		}
		compiled[index] = item
	}
	return compiled, nil
}

func (dispatcher dispatcher) evaluate(
	ctx context.Context,
	evaluation evaluationContext,
	compiled compiledDispatch,
) (evaluated evaluatedDispatch, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			evaluated = evaluatedDispatch{}
			err = NewConfigurationError("handler evaluator panicked", fmt.Errorf("%v", recovered))
		}
	}()
	evaluated, err = compiled.spec.evaluate(ctx, evaluation, cloneCommand(compiled.command), compiled.value)
	evaluated.command = cloneCommand(evaluated.command)
	if err != nil {
		return evaluated, err
	}
	if evaluated.command.Kind != compiled.command.Kind {
		return evaluatedDispatch{}, NewConfigurationError("handler evaluator changed the command keyword", nil)
	}
	if isNilValue(evaluated.value) {
		return evaluatedDispatch{}, NewConfigurationError("handler evaluator returned nil without an error", nil)
	}
	return evaluated, nil
}

func (dispatcher dispatcher) execute(
	ctx context.Context,
	state *executionState,
	compiled compiledDispatch,
	evaluated evaluatedDispatch,
) (effect commandEffect, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			effect = commandEffect{}
			err = NewConfigurationError("handler executor panicked", fmt.Errorf("%v", recovered))
		}
	}()
	effect, err = compiled.spec.execute(ctx, state, evaluated)
	operation := fmt.Sprintf("command %s failed", compiled.command.Kind)
	return finalizeExactErrorRequest(operation, dispatcher.ledger, compiled.spec, effect, err)
}

func finalizeExactErrorRequest(
	operation string,
	ledger *commandResultLedger,
	spec handlerSpec,
	effect commandEffect,
	err error,
) (commandEffect, error) {
	if effect.exactErrorDisposition != nil {
		return clearExactErrorState(effect), NewConfigurationError("handler forged an exact-error disposition", nil)
	}
	request := effect.exactErrorRequest
	propagation := effect.exactErrorPropagation
	effect.exactErrorRequest = nil
	effect.exactErrorPropagation = nil
	if request != nil && propagation != nil {
		return clearExactErrorState(effect), NewConfigurationError("exact-error request channels are mutually exclusive", nil)
	}
	if propagation != nil {
		if spec.exactErrorPolicy != exactErrorPublicationRetryPropagation || spec.keyword != model.CommandRetry {
			return clearExactErrorState(effect), NewConfigurationError("handler requested unauthorized exact-error propagation", nil)
		}
		if !isNilValue(err) {
			return clearExactErrorState(effect), NewConfigurationError("exact-error propagation must be the sole error channel", nil)
		}
		disposition, ok := ledger.redeemExactError(propagation.source)
		if !ok || invalidExactErrorDisposition(operation, disposition) {
			return clearExactErrorState(effect), NewConfigurationError("exact-error propagation source is invalid", nil)
		}
		effect.exactErrorDisposition = disposition
		return effect, disposition.published
	}
	if request == nil {
		return effect, normalizeTerminalError(operation, err)
	}
	if spec.exactErrorPolicy != exactErrorPublicationPressKey || spec.keyword != model.CommandPressKey {
		return clearExactErrorState(effect), NewConfigurationError("handler requested unauthorized exact-error publication", nil)
	}
	if !isNilValue(err) {
		return clearExactErrorState(effect), NewConfigurationError("exact-error request must be the sole error channel", nil)
	}
	if isNilValue(request.underlying) {
		return clearExactErrorState(effect), NewConfigurationError("exact-error publication requires a non-nil underlying error", nil)
	}
	published := request.underlying
	sanitized, replaced := sanitizeMalformedErrorWithStatus(operation, published)
	if replaced {
		return clearExactErrorState(effect), sanitized
	}
	classification := normalizeTerminalError(operation, published)
	if classification == nil || inspectTerminalError(classification).invalid {
		return clearExactErrorState(effect), NewConfigurationError("exact-error classification is invalid", nil)
	}
	effect.exactErrorDisposition = &exactErrorDisposition{
		published: published, classification: classification,
	}
	return effect, published
}

func clearExactErrorState(effect commandEffect) commandEffect {
	effect.exactErrorRequest = nil
	effect.exactErrorPropagation = nil
	effect.exactErrorDisposition = nil
	return effect
}

func invalidExactErrorDisposition(operation string, disposition *exactErrorDisposition) bool {
	if disposition == nil || isNilValue(disposition.published) || disposition.classification == nil {
		return true
	}
	_, replaced := sanitizeMalformedErrorWithStatus(operation, disposition.published)
	return replaced || inspectTerminalError(disposition.classification).invalid
}

func invokeCompiler(ctx context.Context, compile commandCompiler, compileCtx compileContext, command model.Command) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = NewConfigurationError("handler compiler panicked", fmt.Errorf("%v", recovered))
		}
	}()
	return compile(ctx, compileCtx, command)
}

func isNilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
