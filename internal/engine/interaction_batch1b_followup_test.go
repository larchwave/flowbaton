package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch1BAuthoredIntegerBoundariesHaveZeroEffects(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		field string
		value any
	}{
		{name: "duration fractional", field: "duration", value: 1.5},
		{name: "duration zero", field: "duration", value: int64(0)},
		{name: "duration negative", field: "duration", value: int64(-1)},
		{name: "duration above maximum", field: "duration", value: int64(60001)},
		{name: "duration int64 overflow control", field: "duration", value: int64(^uint64(0) >> 1)},
		{name: "duration unsigned overflow", field: "duration", value: ^uint64(0)},
		{name: "settle fractional", field: "waitToSettleTimeoutMs", value: 1.5},
		{name: "settle negative", field: "waitToSettleTimeoutMs", value: int64(-1)},
		{name: "settle above maximum", field: "waitToSettleTimeoutMs", value: int64(30001)},
		{name: "settle int64 overflow control", field: "waitToSettleTimeoutMs", value: int64(^uint64(0) >> 1)},
		{name: "settle unsigned overflow", field: "waitToSettleTimeoutMs", value: ^uint64(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			clock := newAdvancingClock()
			fields := map[string]any{"direction": "UP", test.field: test.value}
			_, err := executeSwipeForTest(context.Background(), swipeCommand(fields, nil), nil, driver, clock, NewElementLookup(driver, clock))
			if !isConfigurationError(err) {
				t.Fatalf("%s=%#v error = %T %v, want ConfigurationError", test.field, test.value, err, err)
			}
			assertNoSwipeOrSettle(t, driver)
		})
	}
}

func TestInteractionBatch1BCoordinateNegativeMatrixHasZeroEffects(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "empty", start: "", end: "1,1"},
		{name: "missing token", start: "0", end: "1,1"},
		{name: "extra token", start: "0,0,0", end: "1,1"},
		{name: "negative x", start: "-1,0", end: "1,1"},
		{name: "negative y", start: "0,-1", end: "1,1"},
		{name: "leading whitespace", start: " 0,0", end: "1,1"},
		{name: "axis whitespace", start: "0, 0", end: "1,1"},
		{name: "trailing whitespace", start: "0,0 ", end: "1,1"},
		{name: "plus sign", start: "+0,0", end: "1,1"},
		{name: "mixed axes absolute percentage", start: "0,0%", end: "1%,1%"},
		{name: "mixed axes percentage absolute", start: "0%,0", end: "1%,1%"},
		{name: "mixed endpoint modes", start: "0,0", end: "1%,1%"},
		{name: "absolute x half open edge", start: "400,0", end: "1,1"},
		{name: "absolute y half open edge", start: "0,884", end: "1,1"},
		{name: "percentage x 100", start: "100%,0%", end: "1%,1%"},
		{name: "percentage y 100", start: "0%,100%", end: "1%,1%"},
		{name: "integer overflow", start: "9223372036854775808,0", end: "1,1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			clock := newAdvancingClock()
			command := swipeCommand(map[string]any{"start": test.start, "end": test.end}, nil)
			_, err := executeSwipeForTest(context.Background(), command, nil, driver, clock, NewElementLookup(driver, clock))
			if !isConfigurationError(err) {
				t.Fatalf("coordinates %q -> %q error = %T %v, want ConfigurationError", test.start, test.end, err, err)
			}
			assertNoSwipeOrSettle(t, driver)
		})
	}
}

func TestInteractionBatch1BFromTimingStabilityAndFailures(t *testing.T) {
	t.Parallel()

	command := func() model.Command {
		return swipeCommand(map[string]any{
			"from": map[string]any{"text": "Continue"}, "direction": "RIGHT", "waitToSettleTimeoutMs": int64(0),
		}, swipeSelector("Continue", nil))
	}

	t.Run("appearing target", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		bounds := device.Bounds{X: 30, Y: 40, Width: 20, Height: 30}
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: batch1AEmptyTree()}, {Value: tapTree(bounds)}, {Value: tapTree(bounds)},
		}})
		clock := &batch1ATraceClock{now: time.Unix(700, 0)}
		_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("appearing target error = %v", err)
		}
		assertBatch1BSwipeAndSettle(t, driver, swipeElementRequest(device.Point{X: 40, Y: 55}, "RIGHT", 400))
	})

	t.Run("interaction adjusted required deadline", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		clock := &batch1ATraceClock{now: time.Unix(800, 0)}
		lookup := NewElementLookup(driver, clock)
		lookup.RecordInteraction(clock.now)
		clock.now = clock.now.Add(3 * time.Second)
		_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, lookup)
		if err == nil || classifyTerminalError(err) != terminalErrorRetryable {
			t.Fatalf("adjusted required lookup error = %T %v", err, err)
		}
		if got, want := sumBatch1ADurations(clock.waits), LookupTimeout-3*time.Second; got != want {
			t.Fatalf("lookup waits = %v, want %v", got, want)
		}
		assertNoSwipeOrSettle(t, driver)
	})

	t.Run("moving target latest stable bounds", func(t *testing.T) {
		initial := device.Bounds{X: 10, Y: 20, Width: 20, Height: 20}
		latest := device.Bounds{X: 100, Y: 200, Width: 40, Height: 60}
		driver := batch1ASelectorDriver(initial, latest, latest)
		clock := &batch1ATraceClock{now: time.Unix(900, 0)}
		_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("moving target error = %v", err)
		}
		assertBatch1BSwipeAndSettle(t, driver, swipeElementRequest(device.Point{X: 120, Y: 230}, "RIGHT", 400))
	})

	t.Run("ambiguous refresh retains latest refreshable bounds", func(t *testing.T) {
		bounds := device.Bounds{X: 50, Y: 60, Width: 20, Height: 20}
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: tapTree(bounds)}, {Value: batch1ADuplicateTree(bounds)}, {Value: tapTree(bounds)},
		}})
		clock := &batch1ATraceClock{now: time.Unix(1000, 0)}
		_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("ambiguous refresh error = %v", err)
		}
		assertBatch1BSwipeAndSettle(t, driver, swipeElementRequest(device.Point{X: 60, Y: 70}, "RIGHT", 400))
	})

	t.Run("stability timeout uses latest refreshable owned bounds", func(t *testing.T) {
		descriptors := []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 0, Y: 20, Width: 20, Height: 20})}}
		for index := 1; index <= 30; index++ {
			descriptors = append(descriptors, enginetest.Result[device.TreeNode]{Value: tapTree(device.Bounds{X: index, Y: 20, Width: 20, Height: 20})})
		}
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: descriptors})
		clock := &batch1ATraceClock{now: time.Unix(1100, 0)}
		_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("stability timeout error = %v", err)
		}
		if got := sumBatch1ADurations(clock.waits); got != ElementStabilityTimeout {
			t.Fatalf("stability waits = %v, want %v", got, ElementStabilityTimeout)
		}
		assertBatch1BSwipeAndSettle(t, driver, swipeElementRequest(device.Point{X: 40, Y: 30}, "RIGHT", 400))
	})

	for _, test := range []struct {
		name      string
		phase     string
		terminal  error
		wantClass terminalErrorClass
	}{
		{name: "lookup device error", phase: "lookup", terminal: NewDeviceConnectionError("descriptor disconnected", errors.New("device cause")), wantClass: terminalErrorDeviceConnection},
		{name: "stability hierarchy error", phase: "stability", terminal: NewConfigurationError("hierarchy unavailable", errors.New("hierarchy cause")), wantClass: terminalErrorConfiguration},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			descriptors := []enginetest.Result[device.TreeNode]{{Err: test.terminal}}
			if test.phase == "stability" {
				descriptors = []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})}, {Err: test.terminal}}
			}
			driver.Enqueue(enginetest.DriverScript{ContentDescriptor: descriptors})
			clock := newAdvancingClock()
			_, err := executeSwipeForTest(context.Background(), command(), nil, driver, clock, NewElementLookup(driver, clock))
			if err != test.terminal || classifyTerminalError(err) != test.wantClass || !errors.Is(err, errors.Unwrap(test.terminal)) {
				t.Fatalf("terminal error = %T %v class %v, want exact %T %v class %v", err, err, classifyTerminalError(err), test.terminal, test.terminal, test.wantClass)
			}
			assertNoSwipeOrSettle(t, driver)
		})
	}

	for _, phase := range []string{"lookup", "stability"} {
		t.Run("cancellation during "+phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			clock := &batch1ACancelWaitClock{now: time.Unix(1200, 0), cancel: cancel}
			driver := batch1APointDriver(400, 884)
			if phase == "stability" {
				driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})}}})
			}
			_, err := executeSwipeForTest(ctx, command(), nil, driver, clock, NewElementLookup(driver, clock))
			if err != context.Canceled || classifyTerminalError(err) != terminalErrorCancelled {
				t.Fatalf("%s cancellation = %T %v", phase, err, err)
			}
			assertNoSwipeOrSettle(t, driver)
		})
	}
}

func TestInteractionBatch1BActionAndSettlePrecedence(t *testing.T) {
	t.Parallel()

	t.Run("driver failure preserves prior watermark", func(t *testing.T) {
		failure := errors.New("swipe failed")
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{Swipe: []enginetest.Result[struct{}]{{Err: failure}}})
		clock := &batch1ATraceClock{now: time.Unix(1300, 0)}
		lookup := NewElementLookup(driver, clock)
		lookup.RecordInteraction(clock.now)
		clock.now = clock.now.Add(5 * time.Second)
		_, err := executeSwipeForTest(context.Background(), swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, lookup)
		if classifyTerminalError(err) != terminalErrorRetryable || errors.Unwrap(err) != failure || !errors.Is(err, failure) ||
			len(swipeRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("driver failure = %T %v actions %#v", err, err, driver.Actions())
		}
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-5*time.Second; got != want {
			t.Fatalf("watermark changed after failed Swipe: adjusted timeout %v, want %v", got, want)
		}
	})

	for _, test := range []struct {
		name      string
		settle    error
		wantClass terminalErrorClass
		ignored   bool
	}{
		{name: "ordinary ignored", settle: NewOperationError("not settled", errors.New("ordinary cause")), ignored: true},
		{name: "device propagated", settle: NewDeviceConnectionError("settle disconnected", errors.New("device cause")), wantClass: terminalErrorDeviceConnection},
		{name: "configuration propagated", settle: NewConfigurationError("bad settle", errors.New("configuration cause")), wantClass: terminalErrorConfiguration},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: test.settle}}})
			clock := newAdvancingClock()
			_, err := executeSwipeForTest(context.Background(), swipeCommand(map[string]any{"direction": "DOWN", "waitToSettleTimeoutMs": int64(0)}, nil), nil, driver, clock, NewElementLookup(driver, clock))
			if test.ignored {
				if err != nil {
					t.Fatalf("ordinary settle error = %v, want ignored", err)
				}
			} else if err != test.settle || classifyTerminalError(err) != test.wantClass || !errors.Is(err, errors.Unwrap(test.settle)) {
				t.Fatalf("terminal settle = %T %v class %v, want exact %T %v class %v", err, err, classifyTerminalError(err), test.settle, test.settle, test.wantClass)
			}
			requests := swipeRequests(driver.Actions())
			settles := settleRequests(driver.Actions())
			if len(requests) != 1 || len(settles) != 1 || settles[0].AppID != "com.example.batch1a" || len(driver.Actions()) != 2 {
				t.Fatalf("settle ordering/evaluated app = actions %#v", driver.Actions())
			}
		})
	}

	t.Run("settle cancellation wins and cuts off later effects", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch1APointDriver(400, 884)
		driver := &batch1BCancelSettleDriver{Driver: base, cancel: cancel}
		clock := newAdvancingClock()
		_, err := executeSwipeForTest(ctx, swipeCommand(map[string]any{"direction": "LEFT", "waitToSettleTimeoutMs": int64(0)}, nil), nil, driver, clock, NewElementLookup(driver, clock))
		if err != context.Canceled || classifyTerminalError(err) != terminalErrorCancelled || len(swipeRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 1 || len(base.Actions()) != 2 {
			t.Fatalf("settle cancellation = %T %v actions %#v", err, err, base.Actions())
		}
	})
}

func TestInteractionBatch1BCompileProgramFirstAndLaterInvalidRootsHaveZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	valid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.valid"}, Commands: []model.Command{
			swipeCommand(map[string]any{"direction": "UP"}, nil),
		}}
	}
	invalid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.invalid"}, Commands: []model.Command{
			swipeCommand(map[string]any{"direction": "sideways"}, nil),
		}}
	}
	for _, test := range []struct {
		name  string
		flows []model.Flow
	}{
		{name: "invalid first root", flows: []model.Flow{invalid("/workspace/invalid-first.yaml"), valid("/workspace/valid-later.yaml")}},
		{name: "invalid later root", flows: []model.Flow{valid("/workspace/valid-first.yaml"), invalid("/workspace/invalid-later.yaml")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := enginetest.NewFakeDriver()
			factory := &countingRuntimeFactory{}
			listenerCalls := 0
			resultCalls := 0
			reportCalls := 0
			listeners := []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })}
			_ = Dependencies{Driver: driver, JSFactory: factory, Listeners: listeners}
			var results []FlowResult
			compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(test.flows...), registry)
			if compiled != nil || !isConfigurationError(compileErr) {
				t.Fatalf("compileProgram() = %#v, %T %v; want nil ConfigurationError", compiled, compileErr, compileErr)
			}
			if len(results) != 0 || resultCalls != 0 || reportCalls != 0 || listenerCalls != 0 || factory.calls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("preflight effects = results %d result calls %d report calls %d listeners %d runtimes %d driver %#v",
					len(results), resultCalls, reportCalls, listenerCalls, factory.calls, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch1BHandlerOwnsSettleAndRequestUnion(t *testing.T) {
	t.Parallel()

	spec := swipeHandlerSpec()
	if spec.postAction != postActionNoSettle || spec.settleRequest != nil {
		t.Fatalf("swipe settle placement = postAction %v settleRequest nil=%t, want handler-owned", spec.postAction, spec.settleRequest == nil)
	}
	requests := []device.SwipeRequest{
		ownedSwipeDirectionRequest("UP", 400),
		ownedSwipePointRequest(device.Point{X: 1, Y: 2}, device.Point{X: 3, Y: 4}, 1),
		ownedSwipeElementRequest(device.Point{X: 5, Y: 6}, "LEFT", 60000),
	}
	want := []device.SwipeRequest{
		{Direction: "UP", DurationMillis: 400},
		swipePointRequest(device.Point{X: 1, Y: 2}, device.Point{X: 3, Y: 4}, 1),
		swipeElementRequest(device.Point{X: 5, Y: 6}, "LEFT", 60000),
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("exact request union = %#v, want %#v", requests, want)
	}
}

func assertBatch1BSwipeAndSettle(t testing.TB, driver *enginetest.FakeDriver, want device.SwipeRequest) {
	t.Helper()
	if got := swipeRequests(driver.Actions()); !reflect.DeepEqual(got, []device.SwipeRequest{want}) {
		t.Fatalf("Swipe requests = %#v, want one %#v", got, want)
	}
	settles := settleRequests(driver.Actions())
	if len(settles) != 1 || settles[0].AppID != "com.example.batch1a" {
		t.Fatalf("settle requests = %#v, want one for evaluated active app", settles)
	}
}

type batch1BCancelSettleDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch1BCancelSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	result, err := driver.Driver.WaitForAppToSettle(context.Background(), request)
	driver.cancel()
	return result, err
}
