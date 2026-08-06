package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/imagecheck"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestTapBatch2SameSessionWatermarkReducesNextAssertionBudget(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		optional      bool
		base          time.Duration
		wantOutcome   Outcome
		wantSurfaced  bool
		wantSelectors int
	}{
		{name: "required", base: LookupTimeout, wantOutcome: Failed, wantSurfaced: true, wantSelectors: 170},
		{name: "optional", optional: true, base: OptionalLookupTimeout, wantOutcome: Warned, wantSelectors: 70},
	} {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapThenMissingAssertionFlow(t, test.optional)
			clock := newTapTraceClock(time.Unix(300, 0))
			driver := newTapTraceDriver(clock)
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
				WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: nil}},
			})
			results, executeErr := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-next-budget-" + test.name,
				Driver:      driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if test.wantSurfaced != (executeErr != nil) {
				t.Fatalf("Execute() error = %v, surfaced=%t", executeErr, test.wantSurfaced)
			}
			if executeErr != nil {
				var assertion *AssertionError
				if !errors.As(executeErr, &assertion) {
					t.Fatalf("Execute() error = %T %v, want AssertionError", executeErr, executeErr)
				}
			}
			if len(results) != 1 || len(results[0].Commands()) != 2 || results[0].Commands()[1].Outcome() != test.wantOutcome {
				t.Fatalf("results = %#v, want two commands and %s assertion", results, test.wantOutcome)
			}
			waits := tapWaitDurations(clock.Trace())
			if len(waits) == 0 || waits[0] != 100*time.Millisecond {
				t.Fatalf("waits = %#v, want one leading 100ms tap-owned settle", waits)
			}
			if got, want := sumTapDurations(waits[1:]), test.base-100*time.Millisecond; got != want {
				t.Fatalf("next assertion lookup wait = %v, want exact adjusted %v", got, want)
			}
			if got := countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor); got != test.wantSelectors {
				t.Fatalf("assertion descriptor calls = %d, want %d", got, test.wantSelectors)
			}
			if got := len(settleRequests(driver.Actions())); got != 1 {
				t.Fatalf("settle calls = %d, want one tap-owned settle and no generic duplicate", got)
			}
		})
	}
}

func TestTapBatch2StabilityExactTracesAndSingleResolutionPerBatch(t *testing.T) {
	t.Parallel()

	t.Run("moving bounds then two equal", func(t *testing.T) {
		flowModel := parsedTapFlow(t, "text: Continue\npoint: '50%,50%'\nrepeat: 3\nwaitToSettleTimeoutMs: 0", nil)
		clock := newTapTraceClock(time.Unix(400, 0))
		driver := newTapTraceDriver(clock)
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			ContentDescriptor: []enginetest.Result[device.TreeNode]{
				{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})},
				{Value: tapTree(device.Bounds{X: 20, Y: 20, Width: 20, Height: 20})},
				{Value: tapTree(device.Bounds{X: 30, Y: 20, Width: 20, Height: 20})},
				{Value: tapTree(device.Bounds{X: 30, Y: 20, Width: 20, Height: 20})},
			},
		})
		_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-stability-moving",
			Driver:      driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if got, want := tapWaitDurations(clock.Trace()), []time.Duration{
			100 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond,
			RepeatDelay, RepeatDelay,
		}; !reflect.DeepEqual(got, want) {
			t.Fatalf("stability waits = %#v, want %#v", got, want)
		}
		if got := countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor); got != 4 {
			t.Fatalf("descriptor calls = %d, want one lookup plus one three-poll stability resolution", got)
		}
		if got, want := tapRequests(driver.Actions()), repeatedTapRequests(device.Point{X: 40, Y: 30}, 3); !reflect.DeepEqual(got, want) {
			t.Fatalf("repeat taps = %#v, want one resolved stable point %#v", got, want)
		}
	})

	t.Run("never stable uses last known at exactly three seconds", func(t *testing.T) {
		flowModel := parsedTapFlow(t, "text: Continue\nwaitToSettleTimeoutMs: 0", nil)
		clock := newTapTraceClock(time.Unix(500, 0))
		driver := newTapTraceDriver(clock)
		descriptors := []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 0, Y: 20, Width: 20, Height: 20})}}
		for index := 1; index <= 30; index++ {
			descriptors = append(descriptors, enginetest.Result[device.TreeNode]{Value: tapTree(device.Bounds{X: index, Y: 20, Width: 20, Height: 20})})
		}
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			ContentDescriptor: descriptors,
		})
		_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-stability-deadline",
			Driver:      driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		waits := tapWaitDurations(clock.Trace())
		if len(waits) != 30 || sumTapDurations(waits) != 3*time.Second {
			t.Fatalf("stability waits = count %d total %v, want 30/3s", len(waits), sumTapDurations(waits))
		}
		if got := countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor); got != 31 {
			t.Fatalf("descriptor calls = %d, want lookup plus 30 polls", got)
		}
		if got, want := tapRequests(driver.Actions()), []device.TapRequest{{Point: device.Point{X: 40, Y: 30}}}; !reflect.DeepEqual(got, want) {
			t.Fatalf("last-known tap = %#v, want %#v", got, want)
		}
	})

	t.Run("terminal refresh error performs no tap", func(t *testing.T) {
		connection := NewDeviceConnectionError("stability disconnected", nil)
		flowModel := parsedTapFlow(t, "text: Continue\nwaitToSettleTimeoutMs: 0", nil)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			ContentDescriptor: []enginetest.Result[device.TreeNode]{
				{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})},
				{Err: connection},
			},
		})
		_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-stability-terminal",
			Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != connection || len(tapRequests(driver.Actions())) != 0 {
			t.Fatalf("Execute() = error %T %v taps %#v, want exact terminal/no tap", err, err, tapRequests(driver.Actions()))
		}
	})

	t.Run("cancellation during stability performs no tap", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := &tapCancelWaitClock{now: time.Unix(550, 0), cancel: cancel}
		flowModel := parsedTapFlow(t, "text: Continue\nwaitToSettleTimeoutMs: 0", nil)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})}},
		})
		_, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-stability-cancel",
			Driver:      driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != context.Canceled || len(tapRequests(driver.Actions())) != 0 {
			t.Fatalf("Execute() = error %T %v taps %#v, want exact cancellation/no tap", err, err, tapRequests(driver.Actions()))
		}
	})
}

func TestTapBatch2CancellationDuringDelayAndFailedTapWatermark(t *testing.T) {
	t.Parallel()

	for _, ignoreCancellation := range []bool{false, true} {
		name := "returns cancellation"
		if ignoreCancellation {
			name = "context ignoring wait returns nil"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			clock := &tapCancelWaitClock{now: time.Unix(600, 0), cancel: cancel, ignoreCancellation: ignoreCancellation}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
			lookup := NewElementLookup(driver, clock)
			_, _ = lookup.cachedDeviceInfo(context.Background())
			flowModel := parsedTapFlow(t, "point: '20,30'\nrepeat: 2", nil)
			evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
			_, err := executeTapOn(ctx, tapExecutionState(driver, clock, lookup), evaluated)
			if err != context.Canceled {
				t.Fatalf("executeTapOn() error = %T %v, want exact context.Canceled", err, err)
			}
			if len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("actions = %#v, want first tap only and no settle", driver.Actions())
			}
			if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-RepeatDelay; got != want {
				t.Fatalf("watermark after cancelled delay = %v, want %v", got, want)
			}
		})
	}

	t.Run("first tap failure leaves seeded interaction unchanged", func(t *testing.T) {
		clock := newAdvancingClock()
		driver := enginetest.NewFakeDriver()
		failure := errors.New("tap failed")
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			Tap:        []enginetest.Result[struct{}]{{Err: failure}},
		})
		lookup := NewElementLookup(driver, clock)
		_, _ = lookup.cachedDeviceInfo(context.Background())
		lookup.RecordInteraction(clock.Now().Add(-5 * time.Second))
		flowModel := parsedTapFlow(t, "point: '20,30'", nil)
		evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
		_, err := executeTapOn(context.Background(), tapExecutionState(driver, clock, lookup), evaluated)
		if !errors.Is(err, failure) {
			t.Fatalf("executeTapOn() error = %T %v, want original tap cause", err, err)
		}
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-5*time.Second; got != want {
			t.Fatalf("watermark after failed first tap = %v, want seeded %v", got, want)
		}
	})
}

func TestTapBatch2WholeProgramRawTypedMismatchHasZeroEffects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*model.Command)
	}{
		{name: "point", mutate: func(command *model.Command) { command.Arguments.(map[string]any)["point"] = "21,30" }},
		{name: "delay", mutate: func(command *model.Command) { command.Arguments.(map[string]any)["delay"] = int64(401) }},
		{name: "settle", mutate: func(command *model.Command) { command.Arguments.(map[string]any)["waitToSettleTimeoutMs"] = int64(101) }},
		{name: "optional raw", mutate: func(command *model.Command) { command.Arguments.(map[string]any)["optional"] = true }},
		{name: "label command", mutate: func(command *model.Command) { label := "other"; command.Label = &label }},
		{name: "raw deferred absent typed", mutate: func(command *model.Command) { command.Arguments.(map[string]any)["retryTapIfNoChange"] = true }},
		{name: "typed deferred absent raw", mutate: func(command *model.Command) { value := true; command.Selector.WaitUntilVisible = &value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapFlow(t, "point: '20,30'\nrepeat: 2\ndelay: 400\nwaitToSettleTimeoutMs: 100\nlabel: owned", nil)
			test.mutate(&flowModel.Commands[0])
			driver := enginetest.NewFakeDriver()
			factory := &countingRuntimeFactory{}
			listenerCalls := 0
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-mismatch-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			})
			if !isConfigurationError(err) || len(results) != 0 || factory.calls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("mismatch = error %T %v results %d runtime %d listeners %d actions %#v", err, err, len(results), factory.calls, listenerCalls, driver.Actions())
			}
		})
	}
}

func TestTapBatch2LaterInvalidRootPreventsEarlierValidRootEffects(t *testing.T) {
	t.Parallel()

	first := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: "/workspace/tap-batch2-first.yaml",
		Config:   model.Config{AppID: "com.example.first"},
		Commands: []model.Command{{Kind: model.CommandLaunchApp, Form: model.CommandFormScalar}},
	}
	second := parsedTapFlow(t, "point: '20,30'", nil)
	second.Path = "/workspace/tap-batch2-second.yaml"
	second.Commands[0].Arguments.(map[string]any)["point"] = "different"
	program := multiRootTapProgram(first, second)
	driver := enginetest.NewFakeDriver()
	factory := &countingRuntimeFactory{}
	listenerCalls := 0
	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "tap-batch2-later-invalid",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
	})
	if !isConfigurationError(err) || len(results) != 0 || factory.calls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("later invalid = error %T %v results %d runtime %d listeners %d actions %#v", err, err, len(results), factory.calls, listenerCalls, driver.Actions())
	}
}

func TestTapBatch2PrepareExecuteLaterLiteralInterpolationPointHasZeroEffects(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		point string
	}{
		{name: "escaped", point: `\${POINT}`},
		{name: "unterminated", point: "${POINT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			first := model.Flow{
				SchemaVersion: model.ASTVersionV0, Path: "/workspace/tap-batch2-prepare-first-" + test.name + ".yaml",
				Config:   model.Config{AppID: "com.example.first"},
				Commands: []model.Command{{Kind: model.CommandLaunchApp, Form: model.CommandFormScalar}},
			}
			second := parsedTapFlow(t, "point: '20,30'", nil)
			second.Path = "/workspace/tap-batch2-prepare-second-" + test.name + ".yaml"
			point := test.point
			second.Commands[0].Selector.Point = &point
			second.Commands[0].Arguments.(map[string]any)["point"] = test.point
			loader := tapPreparedFlowLoader{flows: map[string]model.Flow{
				first.Path:  first,
				second.Path: second,
			}}
			program, err := Prepare(context.Background(), model.ExecutionPlan{
				SelectedRoots: []string{first.Path, second.Path},
			}, loader)
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}

			driver := enginetest.NewFakeDriver()
			ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
			driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
				{Value: ready}, {Value: ready},
			}})
			factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
			listenerCalls := 0
			results, err := Execute(context.Background(), program, Dependencies{
				ExecutionID: "tap-batch2-prepare-later-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			})
			if !isConfigurationError(err) || len(results) != 0 || factory.calls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("later literal point = error %T %v results %d runtime %d listeners %d actions %#v",
					err, err, len(results), factory.calls, listenerCalls, driver.Actions())
			}
		})
	}
}

func TestTapBatch2PointOnlyRejectsUnrelatedFields(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"point: '20,30'\nindex: 0",
		"point: '20,30'\ntolerance: 5",
		"point: '20,30'\ntraits: []",
		"point: '20,30'\ncontainsDescendants: []",
		"text: Continue\ntolerance: 5",
	} {
		command := parsedTapCommand(t, body)
		if _, err := compileTapOn(command); !isConfigurationError(err) {
			t.Fatalf("compileTapOn(%q) error = %T %v, want fail-closed ConfigurationError", body, err, err)
		}
	}
}

func TestTapBatch2OddFloorAndOverflowSafePercentageMath(t *testing.T) {
	t.Parallel()

	point, err := parseTapPoint("50%,36%")
	if err != nil {
		t.Fatalf("parseTapPoint() error = %v", err)
	}
	gotScreen, err := point.resolveScreen(101, 99)
	if err != nil {
		t.Fatalf("resolveScreen() error = %v", err)
	}
	if got, want := gotScreen, (device.Point{X: 50, Y: 35}); got != want {
		t.Fatalf("odd screen floor = %+v, want %+v", got, want)
	}
	gotRelative, err := point.resolveRelative(device.Bounds{X: 7, Y: 11, Width: 101, Height: 99})
	if err != nil {
		t.Fatalf("resolveRelative() error = %v", err)
	}
	if got, want := gotRelative, (device.Point{X: 57, Y: 46}); got != want {
		t.Fatalf("odd relative floor = %+v, want %+v", got, want)
	}

	maximumInt := int(^uint(0) >> 1)
	nearUpper, err := parseTapPoint("99%,99%")
	if err != nil {
		t.Fatalf("parseTapPoint(99%%) error = %v", err)
	}
	resolved, err := nearUpper.resolveScreen(maximumInt, maximumInt)
	if err != nil {
		t.Fatalf("max-int resolve error = %v", err)
	}
	wantAxis := floorTapPercentage(int64(maximumInt), 99)
	if resolved != (device.Point{X: float64(wantAxis), Y: float64(wantAxis)}) {
		t.Fatalf("max-int floor = %+v, want %d without multiplication overflow", resolved, wantAxis)
	}
}

func TestTapBatch2LatePointInterpolatesOnceAndSynchronizesSnapshots(t *testing.T) {
	t.Parallel()

	command := parsedTapCommand(t, "point: '${POINT}'\nrepeat: 3")
	source := cloneCommand(command)
	compiled, err := compileTapOn(command)
	if err != nil {
		t.Fatalf("compileTapOn() error = %v", err)
	}
	inputs := make([]string, 0, 2)
	evaluation := evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			inputs = append(inputs, input)
			if input == "${POINT}" {
				return "20,30", nil
			}
			return input, nil
		},
		activeConfig: model.Config{AppID: "com.example.tap"}, hasActiveConfig: true,
	}
	evaluated, err := evaluateTapOn(context.Background(), evaluation, command, compiled)
	if err != nil {
		t.Fatalf("evaluateTapOn() error = %v", err)
	}
	pointCalls := 0
	for _, input := range inputs {
		if input == "${POINT}" {
			pointCalls++
		}
	}
	if pointCalls != 1 {
		t.Fatalf("point interpolation calls = %d inputs %#v, want exactly one", pointCalls, inputs)
	}
	if evaluated.command.Selector == nil || evaluated.command.Selector.Point == nil || *evaluated.command.Selector.Point != "20,30" || evaluated.command.Selector.Delay == nil || *evaluated.command.Selector.Delay != 100 {
		t.Fatalf("evaluated typed selector = %#v, want point 20,30 and default delay 100", evaluated.command.Selector)
	}
	arguments := evaluated.command.Arguments.(map[string]any)
	if arguments["point"] != "20,30" || arguments["delay"] != int64(100) || !selectorCommandSnapshotMatches(evaluated.command) {
		t.Fatalf("evaluated raw snapshot = %#v, want synchronized point/delay", arguments)
	}
	if !reflect.DeepEqual(command, source) {
		t.Fatalf("evaluation mutated source: got %#v want %#v", command, source)
	}
}

func TestTapBatch2ConcurrentTemplateAndCompiledPlanOwnership(t *testing.T) {
	flowModel := parsedTapFlow(t, "point: '20,30'\nrepeat: 2\nwaitToSettleTimeoutMs: 0", nil)
	source := cloneFlow(flowModel)
	program := singleCompileProgram(flowModel)
	preparedSource, _ := program.Flow(flowModel.Path)

	const runs = 12
	start := make(chan struct{})
	errorsCh := make(chan error, runs)
	var wait sync.WaitGroup
	for index := 0; index < runs; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
			if err != nil {
				errorsCh <- err
				return
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
			results, executeErr := Execute(context.Background(), program, Dependencies{
				ExecutionID: fmt.Sprintf("tap-batch2-concurrent-%d", index),
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
			})
			if executeErr != nil || len(results) != 1 || len(tapRequests(driver.Actions())) != 2 {
				errorsCh <- fmt.Errorf("run %d = results %d taps %d error %v", index, len(results), len(tapRequests(driver.Actions())), executeErr)
				return
			}
			errorsCh <- nil
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	preparedAfter, _ := program.Flow(flowModel.Path)
	if !reflect.DeepEqual(flowModel, source) || !reflect.DeepEqual(preparedAfter, preparedSource) {
		t.Fatal("concurrent execution mutated authored or prepared template")
	}

	failureFlow := parsedTapFlow(t, "point: '400,0'", nil)
	failureSource := cloneFlow(failureFlow)
	failureDriver := enginetest.NewFakeDriver()
	failureDriver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
	_, failureErr := Execute(context.Background(), singleCompileProgram(failureFlow), Dependencies{
		ExecutionID: "tap-batch2-failure-ownership",
		Driver:      failureDriver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			command, ok := event.Command()
			if ok && command.Selector != nil && command.Selector.Point != nil {
				*command.Selector.Point = "listener-mutated"
			}
			return nil
		})},
	})
	if !isConfigurationError(failureErr) || !reflect.DeepEqual(failureFlow, failureSource) {
		t.Fatalf("failure/listener path = error %T %v source mutated=%t", failureErr, failureErr, !reflect.DeepEqual(failureFlow, failureSource))
	}

	command := parsedTapCommand(t, "point: '20,30'\nrepeat: 2\ndelay: 400\nwaitToSettleTimeoutMs: 100")
	compiledAny, err := compileTapOn(command)
	if err != nil {
		t.Fatalf("compileTapOn() error = %v", err)
	}
	compiled := compiledAny.(tapOnCompiled)
	*command.Selector.Point = "99,99"
	*command.Selector.Repeat = 9
	*command.Selector.Delay = 900
	*command.Selector.WaitToSettleTimeoutMS = 900
	if compiled.point == nil || compiled.point.x != 20 || compiled.repeat != 2 || compiled.delayMillis != 400 || compiled.settleTimeoutMillis == nil || *compiled.settleTimeoutMillis != 100 {
		t.Fatalf("compiled plan aliased source mutation: %#v", compiled)
	}
}

func TestTapBatch2MutatingSettleRequestCannotAliasLaterPollOrMetadata(t *testing.T) {
	t.Parallel()

	flowModel := parsedTapFlow(t, "point: '20,30'\nwaitToSettleTimeoutMs: 400", nil)
	driver := &tapMutatingSettleDriver{FakeDriver: enginetest.NewFakeDriver()}
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch2-mutating-settle",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := driver.Seen(), []int64{400, 400}; !reflect.DeepEqual(got, want) {
		t.Fatalf("settle timeouts observed across polls = %#v, want fresh owned copies %#v", got, want)
	}
	evaluated, _ := onlyTapCommandResult(t, results).Metadata().EvaluatedCommand()
	if evaluated.Selector == nil || evaluated.Selector.WaitToSettleTimeoutMS == nil || *evaluated.Selector.WaitToSettleTimeoutMS != 400 {
		t.Fatalf("mutating driver changed evaluated metadata: %#v", evaluated.Selector)
	}
}

func TestTapBatch2ContextIgnoringSettleCancellationAndJoinedTerminal(t *testing.T) {
	t.Parallel()

	t.Run("context ignoring settle rechecks cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		driver := &tapCancelSettleDriver{FakeDriver: enginetest.NewFakeDriver(), cancel: cancel}
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
		flowModel := parsedTapFlow(t, "point: '20,30'\nwaitToSettleTimeoutMs: 0", nil)
		_, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-cancel-settle",
			Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != context.Canceled || len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 1 {
			t.Fatalf("Execute() = error %T %v taps %d settles %d, want exact cancellation after one each", err, err, len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())))
		}
	})

	t.Run("joined terminal branch is not swallowed", func(t *testing.T) {
		connection := NewDeviceConnectionError("settle disconnected", nil)
		joined := errors.Join(NewOperationError("retryable settle", nil), connection)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: joined}},
		})
		flowModel := parsedTapFlow(t, "point: '20,30'\nwaitToSettleTimeoutMs: 100", nil)
		_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-joined-settle",
			Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != joined {
			t.Fatalf("Execute() error = %T %v, want exact joined terminal", err, err)
		}
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "ordinary driver error", err: errors.New("ordinary settle failure")},
		{name: "joined driver error", err: errors.Join(errors.New("settle failure"), NewDeviceConnectionError("settle disconnected", nil))},
	} {
		t.Run("handler cancellation precedence over "+test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			driver := &tapCancelErrorSettleDriver{
				FakeDriver: enginetest.NewFakeDriver(), cancel: cancel, err: test.err,
			}
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
				Platform: "android", WidthGrid: 400, HeightGrid: 884,
			}}}})
			flowModel := parsedTapFlow(t, "point: '20,30'\nwaitToSettleTimeoutMs: 0", nil)
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
			_, err := executeTapOn(ctx, tapExecutionState(driver, clock, lookup), evaluated)
			if err != context.Canceled {
				t.Fatalf("executeTapOn() error = %T %v, want exact context.Canceled", err, err)
			}
			if got := countTapDriverMethod(driver.Actions(), enginetest.MethodTap); got != 1 {
				t.Fatalf("tap calls = %d, want 1", got)
			}
			if got := countTapDriverMethod(driver.Actions(), enginetest.MethodWaitForAppToSettle); got != 1 {
				t.Fatalf("settle calls = %d, want 1 and no later poll", got)
			}
		})
	}
}

func TestTapBatch2FailureAttemptsEmptyEvidenceWithoutChecker(t *testing.T) {
	t.Parallel()

	sink := &tapNoEvidenceSink{}
	checker := &tapNoChecker{}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
		Tap:        []enginetest.Result[struct{}]{{Err: errors.New("tap failed")}},
	})
	flowModel := parsedTapFlow(t, "point: '20,30'", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch2-no-deferred-evidence",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		ArtifactSink: sink, ImageChecker: checker,
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want tap failure")
	}
	result := onlyTapCommandResult(t, results)
	actions := driver.Actions()
	if result.Outcome() != Failed || len(result.Artifacts()) != 0 || sink.Calls() != 0 || checker.Calls() != 0 || countTapDriverMethod(actions, enginetest.MethodTakeScreenshot) != 1 {
		t.Fatalf("deferred services used: sink=%d checker=%d actions=%#v", sink.Calls(), checker.Calls(), driver.Actions())
	}
	for _, action := range actions {
		if action.Method == enginetest.MethodTakeScreenshot && action.Request != (device.ScreenshotRequest{Compressed: true}) {
			t.Fatalf("failure screenshot request = %#v, want compressed", action.Request)
		}
	}
}

func parsedTapThenMissingAssertionFlow(t testing.TB, optional bool) model.Flow {
	t.Helper()
	optionalLine := ""
	if optional {
		optionalLine = "\n    optional: true"
	}
	contents := "appId: com.example.tap-budget\n---\n" +
		"- tapOn:\n    point: '20,30'\n    waitToSettleTimeoutMs: 100\n" +
		"- assertVisible:\n    text: Missing" + optionalLine + "\n"
	parsed, err := flow.ParseBytes("/workspace/tap-budget.yaml", []byte(contents))
	if err != nil {
		t.Fatalf("flow.ParseBytes() error = %v\n%s", err, contents)
	}
	return parsed
}

func tapWaitDurations(trace []string) []time.Duration {
	waits := make([]time.Duration, 0)
	for _, entry := range trace {
		if !strings.HasPrefix(entry, "wait:") {
			continue
		}
		var milliseconds int64
		if _, err := fmt.Sscanf(entry, "wait:%dms@", &milliseconds); err == nil {
			waits = append(waits, time.Duration(milliseconds)*time.Millisecond)
		}
	}
	return waits
}

func sumTapDurations(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total
}

func countTapDriverMethod(actions []enginetest.Action, method enginetest.Method) int {
	count := 0
	for _, action := range actions {
		if action.Method == method {
			count++
		}
	}
	return count
}

func multiRootTapProgram(flows ...model.Flow) *Program {
	roots := make([]string, len(flows))
	paths := make([]string, len(flows))
	flowMap := make(map[string]model.Flow, len(flows))
	aliases := make(map[string]string, len(flows))
	nodes := make([]capability.GraphNode, len(flows))
	for index, flowModel := range flows {
		roots[index] = flowModel.Path
		paths[index] = flowModel.Path
		flowMap[flowModel.Path] = cloneFlow(flowModel)
		aliases[flowModel.Path] = flowModel.Path
		nodes[index] = capability.GraphNode{Path: flowModel.Path}
	}
	return &Program{
		roots: roots, paths: paths, flows: flowMap, aliases: aliases,
		graph: capability.Report{Roots: append([]string(nil), roots...), Nodes: nodes},
	}
}

type tapCancelWaitClock struct {
	now                time.Time
	cancel             context.CancelFunc
	ignoreCancellation bool
}

func (clock *tapCancelWaitClock) Now() time.Time { return clock.now }

func (clock *tapCancelWaitClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.now = clock.now.Add(delay)
	clock.cancel()
	if clock.ignoreCancellation {
		return nil
	}
	return ctx.Err()
}

type tapMutatingSettleDriver struct {
	*enginetest.FakeDriver
	mu   sync.Mutex
	seen []int64
}

func (driver *tapMutatingSettleDriver) WaitForAppToSettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	driver.mu.Lock()
	if request.TimeoutMillis != nil {
		driver.seen = append(driver.seen, *request.TimeoutMillis)
		*request.TimeoutMillis = 999
	}
	driver.mu.Unlock()
	return driver.FakeDriver.WaitForAppToSettle(ctx, request)
}

func (driver *tapMutatingSettleDriver) Seen() []int64 {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]int64(nil), driver.seen...)
}

type tapCancelSettleDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
}

type tapCancelErrorSettleDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
	err    error
}

func (driver *tapCancelErrorSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	result, _ := driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	driver.cancel()
	return result, driver.err
}

type tapPreparedFlowLoader struct {
	flows map[string]model.Flow
}

func (loader tapPreparedFlowLoader) Canonical(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, exists := loader.flows[path]; !exists {
		return "", fmt.Errorf("missing test flow %q", path)
	}
	return path, nil
}

func (loader tapPreparedFlowLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	if err := ctx.Err(); err != nil {
		return model.Flow{}, err
	}
	flowModel, exists := loader.flows[path]
	if !exists {
		return model.Flow{}, fmt.Errorf("missing test flow %q", path)
	}
	return cloneFlow(flowModel), nil
}

type tapCountingRuntimeFactory struct {
	delegate js.Factory
	calls    int
}

func (factory *tapCountingRuntimeFactory) NewRuntime() (js.Runtime, error) {
	factory.calls++
	return factory.delegate.NewRuntime()
}

func (driver *tapCancelSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	result, err := driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	driver.cancel()
	return result, err
}

type tapNoEvidenceSink struct {
	mu    sync.Mutex
	calls int
}

func (sink *tapNoEvidenceSink) Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.mu.Lock()
	sink.calls++
	sink.mu.Unlock()
	return ArtifactWriteResult{}, nil
}

func (sink *tapNoEvidenceSink) Calls() int {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.calls
}

type tapNoChecker struct {
	mu    sync.Mutex
	calls int
}

func (checker *tapNoChecker) Check(context.Context, ImageCheckRequest) (imagecheck.Result, error) {
	checker.mu.Lock()
	checker.calls++
	checker.mu.Unlock()
	return imagecheck.Result{}, nil
}

func (checker *tapNoChecker) Calls() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}
