package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestDispatchListenersIsolatesErrorsAndPanicsWithoutReplacingProductError(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(10, 0))
	timeline, err := NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	_, event, err := timeline.BeginCommand(model.Command{
		Kind: model.CommandLaunchApp, Arguments: map[string]any{"appId": "com.example"},
	}, 0)
	if err != nil {
		t.Fatalf("BeginCommand error: %v", err)
	}

	listenerErr := errors.New("listener failed")
	productErr := NewDeviceConnectionError("device disconnected", errors.New("transport"))
	thirdCalled := false
	result := DispatchListeners(context.Background(), event, productErr,
		ListenerFunc(func(context.Context, Event) error { return listenerErr }),
		ListenerFunc(func(context.Context, Event) error { panic("listener panic") }),
		ListenerFunc(func(_ context.Context, received Event) error {
			thirdCalled = true
			command, ok := received.Command()
			if !ok {
				t.Fatal("listener event did not contain command")
			}
			command.Arguments.(map[string]any)["appId"] = "listener-mutated"
			return nil
		}),
	)
	if !thirdCalled {
		t.Fatal("listener after panic was not called")
	}
	if result.ProductError() != productErr || result.EffectiveError() != productErr {
		t.Fatalf("product error was replaced: product=%v effective=%v", result.ProductError(), result.EffectiveError())
	}
	failures := result.ListenerFailures()
	if len(failures) != 2 {
		t.Fatalf("listener failures = %#v, want two", failures)
	}
	if failures[0].Index() != 0 || !errors.Is(failures[0].Err(), listenerErr) {
		t.Fatalf("first listener failure = %#v", failures[0])
	}
	if failures[1].Index() != 1 || !strings.Contains(failures[1].Err().Error(), "listener panic") {
		t.Fatalf("second listener failure = %#v", failures[1])
	}
	command, ok := event.Command()
	if !ok || command.Arguments.(map[string]any)["appId"] != "com.example" {
		t.Fatalf("listener mutated event command: %#v, %v", command, ok)
	}
	failures[0] = ListenerFailure{}
	if len(result.ListenerFailures()) != 2 {
		t.Fatal("ListenerFailures exposed backing storage")
	}
}

func TestWaitForControllerPreservesProductErrorAndIsolatesControllerFailure(t *testing.T) {
	t.Parallel()

	controllerErr := errors.New("controller failed")
	productErr := NewOperationError("command failed", errors.New("product"))
	result := WaitForController(context.Background(), ControllerFunc(func(context.Context) error {
		return controllerErr
	}), productErr)
	if result.ProductError() != productErr || result.EffectiveError() != productErr {
		t.Fatalf("product error was replaced: %#v", result)
	}
	if !errors.Is(result.ControllerFailure(), controllerErr) {
		t.Fatalf("controller failure = %v, want controller error", result.ControllerFailure())
	}

	panicResult := WaitForController(context.Background(), ControllerFunc(func(context.Context) error {
		panic("controller panic")
	}), nil)
	if panicResult.ProductError() != nil || panicResult.EffectiveError() == nil || !strings.Contains(panicResult.EffectiveError().Error(), "controller panic") {
		t.Fatalf("panic controller result = %#v", panicResult)
	}
	if result := WaitForController(context.Background(), NoopController{}, nil); result.EffectiveError() != nil {
		t.Fatalf("noop controller error: %v", result.EffectiveError())
	}
}

func TestCallbackResultsSanitizeMalformedStoredErrors(t *testing.T) {
	t.Parallel()

	var malformed *OperationError
	dispatch := DispatchListeners(
		context.Background(),
		Event{},
		malformed,
		ListenerFunc(func(context.Context, Event) error { return malformed }),
	)
	assertSafeConfigurationError(t, dispatch.ProductError())
	failures := dispatch.ListenerFailures()
	if len(failures) != 1 {
		t.Fatalf("listener failures = %d, want 1", len(failures))
	}
	assertSafeConfigurationError(t, failures[0].Err())

	controller := WaitForController(
		context.Background(),
		ControllerFunc(func(context.Context) error { return malformed }),
		malformed,
	)
	assertSafeConfigurationError(t, controller.ProductError())
	assertSafeConfigurationError(t, controller.ControllerFailure())
	assertSafeConfigurationError(t, controller.EffectiveError())
}
