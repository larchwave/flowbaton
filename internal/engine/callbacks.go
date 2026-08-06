package engine

import (
	"context"
	"fmt"
)

// Listener observes immutable engine events. Listener failures are diagnostic
// only and never replace the product execution error.
type Listener interface {
	OnEvent(context.Context, Event) error
}

type ListenerFunc func(context.Context, Event) error

func (f ListenerFunc) OnEvent(ctx context.Context, event Event) error {
	return f(ctx, event)
}

// ListenerFailure records one isolated listener error or panic.
type ListenerFailure struct {
	index int
	err   error
}

func (f ListenerFailure) Index() int { return f.index }
func (f ListenerFailure) Err() error { return f.err }

// ListenerDispatchResult keeps product and observer failures in separate
// channels so observer code cannot change execution semantics.
type ListenerDispatchResult struct {
	productError error
	failures     []ListenerFailure
}

func (r ListenerDispatchResult) ProductError() error { return r.productError }

func (r ListenerDispatchResult) EffectiveError() error { return r.productError }

func (r ListenerDispatchResult) ListenerFailures() []ListenerFailure {
	return append([]ListenerFailure(nil), r.failures...)
}

// DispatchListeners invokes every listener in declaration order while
// isolating returned errors and panics.
func DispatchListeners(ctx context.Context, event Event, productError error, listeners ...Listener) ListenerDispatchResult {
	result := ListenerDispatchResult{
		productError: sanitizeMalformedError("listener dispatch product failed", productError),
	}
	for index, listener := range listeners {
		if err := invokeListener(ctx, listener, event); err != nil {
			err = sanitizeMalformedError("listener callback failed", err)
			result.failures = append(result.failures, ListenerFailure{index: index, err: err})
		}
	}
	return result
}

func invokeListener(ctx context.Context, listener Listener, event Event) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("listener panic: %v", recovered)
		}
	}()
	if listener == nil {
		return NewConfigurationError("listener must not be nil", nil)
	}
	return listener.OnEvent(ctx, event)
}

// Controller is the pause/resume boundary consulted before command dispatch.
type Controller interface {
	WaitIfPaused(context.Context) error
}

type ControllerFunc func(context.Context) error

func (f ControllerFunc) WaitIfPaused(ctx context.Context) error { return f(ctx) }

// NoopController never pauses or fails.
type NoopController struct{}

func (NoopController) WaitIfPaused(context.Context) error { return nil }

// ControllerResult preserves a pre-existing product error while separately
// exposing controller failure. Without a product error, controller failure is
// the effective execution error.
type ControllerResult struct {
	productError               error
	controllerFailure          error
	controllerFailureMalformed bool
}

func (r ControllerResult) ProductError() error      { return r.productError }
func (r ControllerResult) ControllerFailure() error { return r.controllerFailure }

func (r ControllerResult) EffectiveError() error {
	if r.productError != nil {
		return r.productError
	}
	return r.controllerFailure
}

// WaitForController safely invokes a controller without allowing a callback
// panic or error to overwrite an existing product failure.
func WaitForController(ctx context.Context, controller Controller, productError error) ControllerResult {
	result := ControllerResult{
		productError: sanitizeMalformedError("controller product failed", productError),
	}
	if controller == nil {
		return result
	}
	result.controllerFailure, result.controllerFailureMalformed = sanitizeMalformedErrorWithStatus(
		"controller wait failed",
		invokeController(ctx, controller),
	)
	return result
}

func invokeController(ctx context.Context, controller Controller) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("controller panic: %v", recovered)
		}
	}()
	return controller.WaitIfPaused(ctx)
}
