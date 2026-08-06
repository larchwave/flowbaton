package engine

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch1BPrivateRegistryAndStaticContract(t *testing.T) {
	t.Parallel()

	direct, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	if got := sortedHandlerKeywords(direct); !reflect.DeepEqual(got, []string{"doubleTapOn", "longPressOn", "swipe"}) {
		t.Fatalf("direct registry keys = %#v", got)
	}
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	wantProduction := productionKeywordStrings()
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry keys = %#v, want %#v", got, wantProduction)
	}

	accepted := []model.Command{
		swipeCommand(map[string]any{"direction": "UP"}, nil),
		swipeCommand(map[string]any{"direction": "DOWN", "duration": int64(1), "waitToSettleTimeoutMs": int64(0)}, nil),
		swipeCommand(map[string]any{"direction": "LEFT", "duration": int64(60000), "waitToSettleTimeoutMs": int64(30000)}, nil),
		swipeCommand(map[string]any{"direction": "${DIRECTION}"}, nil),
		swipeCommand(map[string]any{"start": "0,0", "end": "399,883"}, nil),
		swipeCommand(map[string]any{"start": "${START}", "end": "${END}"}, nil),
		swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}, "direction": "RIGHT"}, swipeSelector("Continue", nil)),
		// Numeric string durations are coerced to milliseconds.
		swipeCommand(map[string]any{"direction": "DOWN", "duration": "400"}, nil),
	}
	for _, command := range accepted {
		if _, err := compileSwipe(command); err != nil {
			t.Fatalf("compileSwipe(%#v) error = %v", command.Arguments, err)
		}
	}
	// A numeric string duration must decode to the same millis as the native int.
	compiled, err := compileSwipe(swipeCommand(map[string]any{"direction": "DOWN", "duration": "400"}, nil))
	if err != nil {
		t.Fatalf("compileSwipe string duration error = %v", err)
	}
	if plan := compiled.(swipeCompiled); plan.durationMillis != 400 {
		t.Fatalf("string duration decoded to %d, want 400", plan.durationMillis)
	}

	invalid := []model.Command{
		swipeCommand(map[string]any{}, nil),
		swipeCommand(map[string]any{"direction": "sideways"}, nil),
		swipeCommand(map[string]any{"direction": " UP"}, nil),
		swipeCommand(map[string]any{"start": "0,0"}, nil),
		swipeCommand(map[string]any{"end": "1,1"}, nil),
		swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}}, swipeSelector("Continue", nil)),
		swipeCommand(map[string]any{"direction": "UP", "start": "0,0", "end": "1,1"}, nil),
		swipeCommand(map[string]any{"direction": "UP", "from": map[string]any{"text": "Continue"}, "start": "0,0", "end": "1,1"}, swipeSelector("Continue", nil)),
		swipeCommand(map[string]any{"direction": "UP", "unknown": true}, nil),
		swipeCommand(map[string]any{"direction": true}, nil),
		swipeCommand(map[string]any{"start": "0,0", "end": int64(1)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": float64(1.5)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": int64(0)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": int64(-1)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": int64(60001)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": "abc"}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": "${d}"}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": "0"}, nil),
		swipeCommand(map[string]any{"direction": "UP", "duration": "60001"}, nil),
		swipeCommand(map[string]any{"direction": "UP", "waitToSettleTimeoutMs": int64(-1)}, nil),
		swipeCommand(map[string]any{"direction": "UP", "waitToSettleTimeoutMs": int64(30001)}, nil),
		swipeCommand(map[string]any{"start": "0,0", "end": "1%,1%"}, nil),
		swipeCommand(map[string]any{"start": "${START}", "end": "1%,1"}, nil),
		swipeCommand(map[string]any{"start": "0%,0", "end": "1%,1%"}, nil),
		swipeCommand(map[string]any{"start": "0, 0", "end": "1,1"}, nil),
		swipeCommand(map[string]any{"start": "+0,0", "end": "1,1"}, nil),
		swipeCommand(map[string]any{"start": "9223372036854775808,0", "end": "1,1"}, nil),
		swipeCommand(map[string]any{"from": map[string]any{"text": "Continue", "optional": true}, "direction": "UP"}, swipeSelector("Continue", boolPointer(true))),
	}
	for _, command := range invalid {
		if _, err := compileSwipe(command); !isConfigurationError(err) {
			t.Fatalf("compileSwipe(%#v) error = %T %v, want ConfigurationError", command.Arguments, err, err)
		}
	}
}

func TestInteractionBatch1BDirectionCoordinateAndElementRequests(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		command      model.Command
		replacements map[string]string
		driver       *enginetest.FakeDriver
		want         device.SwipeRequest
	}{
		{name: "up default", command: swipeCommand(map[string]any{"direction": "UP", "waitToSettleTimeoutMs": int64(0)}, nil), driver: batch1APointDriver(400, 884), want: device.SwipeRequest{Direction: "UP", DurationMillis: 400}},
		{name: "down one", command: swipeCommand(map[string]any{"direction": "DOWN", "duration": int64(1), "waitToSettleTimeoutMs": int64(0)}, nil), driver: batch1APointDriver(400, 884), want: device.SwipeRequest{Direction: "DOWN", DurationMillis: 1}},
		{name: "late left max", command: swipeCommand(map[string]any{"direction": "${DIRECTION}", "duration": int64(60000), "waitToSettleTimeoutMs": int64(0)}, nil), replacements: map[string]string{"DIRECTION": "LEFT"}, driver: batch1APointDriver(400, 884), want: device.SwipeRequest{Direction: "LEFT", DurationMillis: 60000}},
		{name: "right exact", command: swipeCommand(map[string]any{"direction": "RIGHT", "duration": int64(1234), "waitToSettleTimeoutMs": int64(0)}, nil), driver: batch1APointDriver(400, 884), want: device.SwipeRequest{Direction: "RIGHT", DurationMillis: 1234}},
		{name: "absolute", command: swipeCommand(map[string]any{"start": "0,1", "end": "399,883", "waitToSettleTimeoutMs": int64(0)}, nil), driver: batch1APointDriver(400, 884), want: swipePointRequest(device.Point{X: 0, Y: 1}, device.Point{X: 399, Y: 883}, 400)},
		{name: "late percentage floor", command: swipeCommand(map[string]any{"start": "${START}", "end": "${END}", "waitToSettleTimeoutMs": int64(0)}, nil), replacements: map[string]string{"START": "50%,36%", "END": "99%,99%"}, driver: batch1APointDriver(400, 884), want: swipePointRequest(device.Point{X: 200, Y: 318}, device.Point{X: 396, Y: 875}, 400)},
		{name: "moving element latest center", command: swipeCommand(map[string]any{"from": map[string]any{"text": "${TARGET}"}, "direction": "RIGHT", "waitToSettleTimeoutMs": int64(0)}, swipeSelector("${TARGET}", nil)), replacements: map[string]string{"TARGET": "Continue"}, driver: batch1ASelectorDriver(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20}, device.Bounds{X: 100, Y: 200, Width: 40, Height: 60}, device.Bounds{X: 100, Y: 200, Width: 40, Height: 60}), want: swipeElementRequest(device.Point{X: 120, Y: 230}, "RIGHT", 400)},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := newAdvancingClock()
			lookup := NewElementLookup(test.driver, clock)
			effect, err := executeSwipeForTest(context.Background(), test.command, test.replacements, test.driver, clock, lookup)
			if err != nil {
				t.Fatalf("execute swipe error = %v", err)
			}
			if effect.effectClass != EffectDeviceMutation {
				t.Fatalf("effect class = %v", effect.effectClass)
			}
			requests := swipeRequests(test.driver.Actions())
			if !reflect.DeepEqual(requests, []device.SwipeRequest{test.want}) {
				t.Fatalf("Swipe requests = %#v, want %#v", requests, []device.SwipeRequest{test.want})
			}
			if got := len(settleRequests(test.driver.Actions())); got != 1 {
				t.Fatalf("settle calls = %d, want one", got)
			}
		})
	}
}

func TestInteractionBatch1BLateValidationGridAndFailuresHaveZeroSwipe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      model.Command
		replacements map[string]string
		width        int
		height       int
	}{
		{name: "late direction case", command: swipeCommand(map[string]any{"direction": "${D}"}, nil), replacements: map[string]string{"D": "sideways"}, width: 400, height: 884},
		{name: "late direction whitespace", command: swipeCommand(map[string]any{"direction": "${D}"}, nil), replacements: map[string]string{"D": " RIGHT"}, width: 400, height: 884},
		{name: "late mixed axes", command: swipeCommand(map[string]any{"start": "${START}", "end": "1%,1%"}, nil), replacements: map[string]string{"START": "0%,0"}, width: 400, height: 884},
		{name: "mixed endpoints", command: swipeCommand(map[string]any{"start": "${START}", "end": "${END}"}, nil), replacements: map[string]string{"START": "0,0", "END": "1%,1%"}, width: 400, height: 884},
		{name: "absolute x edge", command: swipeCommand(map[string]any{"start": "${START}", "end": "1,1"}, nil), replacements: map[string]string{"START": "400,0"}, width: 400, height: 884},
		{name: "percentage edge", command: swipeCommand(map[string]any{"start": "${START}", "end": "1%,1%"}, nil), replacements: map[string]string{"START": "100%,0%"}, width: 400, height: 884},
		{name: "blank grid", command: swipeCommand(map[string]any{"start": "0,0", "end": "1,1"}, nil), width: 0, height: 0},
		{name: "nonpositive grid", command: swipeCommand(map[string]any{"start": "0,0", "end": "1,1"}, nil), width: -1, height: 884},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(test.width, test.height)
			clock := newAdvancingClock()
			_, err := executeSwipeForTest(context.Background(), test.command, test.replacements, driver, clock, NewElementLookup(driver, clock))
			if !isConfigurationError(err) {
				t.Fatalf("execute error = %T %v, want ConfigurationError", err, err)
			}
			assertNoSwipeOrSettle(t, driver)
		})
	}

	driver := batch1APointDriver(400, 884)
	driver.Enqueue(enginetest.DriverScript{Swipe: []enginetest.Result[struct{}]{{Err: errors.New("swipe failed")}}})
	clock := newAdvancingClock()
	_, err := executeSwipeForTest(context.Background(), swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, NewElementLookup(driver, clock))
	if err == nil || len(swipeRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("driver failure = error %v actions %#v", err, driver.Actions())
	}
}

func TestInteractionBatch1BCancellationSettleWatermarkAndOwnership(t *testing.T) {
	t.Parallel()

	t.Run("pre-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		driver := batch1APointDriver(400, 884)
		clock := newAdvancingClock()
		_, err := executeSwipeForTest(ctx, swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, NewElementLookup(driver, clock))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
		assertNoSwipeOrSettle(t, driver)
	})

	t.Run("cancel after context-ignoring success", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch1APointDriver(400, 884)
		driver := &batch1BCancelSwipeDriver{Driver: base, cancel: cancel}
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		_, err := executeSwipeForTest(ctx, swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, lookup)
		if !errors.Is(err, context.Canceled) || len(swipeRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 0 {
			t.Fatalf("post-success cancel = error %v actions %#v", err, base.Actions())
		}
		if !lookup.hasInteraction || !lookup.latestInteraction.Equal(clock.Now()) {
			t.Fatalf("watermark = %v/%t, want %v", lookup.latestInteraction, lookup.hasInteraction, clock.Now())
		}
	})

	for _, timeout := range []*int64{nil, int64Pointer(0), int64Pointer(30000)} {
		driver := batch1APointDriver(400, 884)
		if timeout != nil && *timeout > 0 {
			ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
			driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}}})
		}
		clock := newAdvancingClock()
		fields := map[string]any{"direction": "DOWN"}
		if timeout != nil {
			fields["waitToSettleTimeoutMs"] = *timeout
		}
		_, err := executeSwipeForTest(context.Background(), swipeCommand(fields, nil), nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("settle timeout %v error = %v", timeout, err)
		}
		settles := settleRequests(driver.Actions())
		wantCalls := 1
		if timeout == nil {
			wantCalls = 10
		} else if *timeout > 0 {
			wantCalls = 2
		}
		if len(settles) != wantCalls || (timeout == nil) != (settles[0].TimeoutMillis == nil) || timeout != nil && *settles[0].TimeoutMillis != *timeout {
			t.Fatalf("settle timeout %v requests = %#v", timeout, settles)
		}
	}

	t.Run("ordinary settle ignored terminal propagated", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			settle  error
			wantErr bool
		}{
			{name: "ordinary", settle: NewOperationError("not settled", nil)},
			{name: "configuration", settle: NewConfigurationError("bad settle", nil), wantErr: true},
		} {
			driver := batch1APointDriver(400, 884)
			driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: test.settle}}})
			clock := newAdvancingClock()
			_, err := executeSwipeForTest(context.Background(), swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, NewElementLookup(driver, clock))
			if (err != nil) != test.wantErr {
				t.Fatalf("%s settle error = %v", test.name, err)
			}
		}
	})

	t.Run("source request repeat and concurrent ownership", func(t *testing.T) {
		command := swipeCommand(map[string]any{"start": "10,20", "end": "30,40", "duration": int64(60000), "waitToSettleTimeoutMs": int64(0)}, nil)
		sourceBeforeCompile := cloneCommand(command)
		registry, err := newHandlerRegistry(swipeHandlerSpec())
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, command)
		if err != nil {
			t.Fatal(err)
		}
		if !command.Equivalent(sourceBeforeCompile) {
			t.Fatalf("compile mutated source = %#v, want %#v", command, sourceBeforeCompile)
		}
		compiledPlan, ok := compiled.value.(swipeCompiled)
		if !ok || compiledPlan.startPoint == nil || compiledPlan.endPoint == nil ||
			compiledPlan.startPoint.x != 10 || compiledPlan.startPoint.y != 20 ||
			compiledPlan.endPoint.x != 30 || compiledPlan.endPoint.y != 40 {
			t.Fatalf("compiled snapshot = %#v, want original 10,20 -> 30,40", compiled.value)
		}
		command.Arguments.(map[string]any)["start"] = "300,400"
		if command.Arguments.(map[string]any)["start"] != "300,400" {
			t.Fatal("source mutation did not take effect")
		}
		var wg sync.WaitGroup
		requests := make(chan device.SwipeRequest, 8)
		for index := 0; index < 8; index++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				driver := &batch1BMutatingSwipeDriver{Driver: batch1APointDriver(400, 884), requests: requests}
				clock := newAdvancingClock()
				dispatcher := newDispatcher(registry)
				evaluated, evaluateErr := dispatcher.evaluate(context.Background(), batch1AEvaluation(nil), compiled)
				if evaluateErr != nil {
					t.Errorf("evaluate error = %v", evaluateErr)
					return
				}
				evaluatedBeforeExecute := evaluatedDispatch{command: cloneCommand(evaluated.command), value: evaluated.value}
				evaluatedPlan, valid := evaluated.value.(swipeEvaluated)
				if !valid || evaluatedPlan.start == nil || evaluatedPlan.end == nil || evaluatedPlan.appID != "com.example.batch1a" {
					t.Errorf("evaluated snapshot = %#v", evaluated.value)
					return
				}
				state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return NewElementLookup(driver, clock), nil }}
				if _, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated); executeErr != nil {
					t.Errorf("execute error = %v", executeErr)
					return
				}
				if !evaluated.command.Equivalent(evaluatedBeforeExecute.command) || !reflect.DeepEqual(evaluated.value, evaluatedBeforeExecute.value) {
					t.Errorf("execute mutated evaluated snapshot: before %#v after %#v", evaluatedBeforeExecute, evaluated)
				}
			}()
		}
		wg.Wait()
		close(requests)
		for request := range requests {
			want := swipePointRequest(device.Point{X: 10, Y: 20}, device.Point{X: 30, Y: 40}, 60000)
			if !reflect.DeepEqual(request, want) {
				t.Fatalf("owned request = %#v, want %#v", request, want)
			}
		}
		if got := command.Arguments.(map[string]any)["start"]; got != "300,400" {
			t.Fatalf("compiled execution rewrote caller-owned source = %#v", command)
		}
		compiledAfter, ok := compiled.value.(swipeCompiled)
		if !ok || !reflect.DeepEqual(compiledAfter, compiledPlan) || !compiled.command.Equivalent(sourceBeforeCompile) {
			t.Fatalf("compiled snapshot changed: before %#v after %#v command %#v", compiledPlan, compiled.value, compiled.command)
		}
	})
}

func TestInteractionBatch1BStaticInvalidWholeProgramHasZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(swipeHandlerSpec(), doubleTapOnHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, commands := range [][]model.Command{
		{swipeCommand(map[string]any{"direction": "sideways"}, nil)},
		{batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), swipeCommand(map[string]any{"start": "0,0"}, nil)},
	} {
		if _, compileErr := dispatcher.compileSequence(context.Background(), compileContext{}, commands); !isConfigurationError(compileErr) {
			t.Fatalf("compileSequence(%#v) error = %T %v, want ConfigurationError", commands, compileErr, compileErr)
		}
		// Whole-sequence compilation failed before any runtime dependencies exist.
	}
}

func TestInteractionBatch1BSelectorFailuresAndDefensiveBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("late invalid regex", func(t *testing.T) {
		driver := batch1ASelectorDriver(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
		clock := newAdvancingClock()
		command := swipeCommand(map[string]any{"from": map[string]any{"text": "${TARGET}"}, "direction": "UP"}, swipeSelector("${TARGET}", nil))
		_, err := executeSwipeForTest(context.Background(), command, map[string]string{"TARGET": "["}, driver, clock, NewElementLookup(driver, clock))
		if !isConfigurationError(err) {
			t.Fatalf("invalid regex error = %T %v", err, err)
		}
		assertNoSwipeOrSettle(t, driver)
	})

	t.Run("missing target", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: batch1AEmptyTree()}}})
		clock := newAdvancingClock()
		command := swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}, "direction": "UP"}, swipeSelector("Continue", nil))
		_, err := executeSwipeForTest(context.Background(), command, nil, driver, clock, NewElementLookup(driver, clock))
		if err == nil {
			t.Fatal("missing target error = nil")
		}
		assertNoSwipeOrSettle(t, driver)
	})

	t.Run("ambiguous stability refresh uses latest unique bounds", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		bounds := device.Bounds{X: 10, Y: 20, Width: 20, Height: 20}
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: tapTree(bounds)}, {Value: batch1ADuplicateTree(bounds)}, {Value: tapTree(bounds)},
		}})
		clock := newAdvancingClock()
		command := swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}, "direction": "UP", "waitToSettleTimeoutMs": int64(0)}, swipeSelector("Continue", nil))
		_, err := executeSwipeForTest(context.Background(), command, nil, driver, clock, NewElementLookup(driver, clock))
		if err != nil {
			t.Fatalf("ambiguous refresh error = %v", err)
		}
		if got := swipeRequests(driver.Actions()); len(got) != 1 || got[0].ElementPoint == nil || *got[0].ElementPoint != (device.Point{X: 20, Y: 30}) {
			t.Fatalf("ambiguous refresh request = %#v", got)
		}
	})

	t.Run("compile guards", func(t *testing.T) {
		wrongKeyword := swipeCommand(map[string]any{"direction": "UP"}, nil)
		wrongKeyword.Kind = model.CommandTapOn
		withChildren := swipeCommand(map[string]any{"direction": "UP"}, nil)
		withChildren.Children = []model.Command{{Kind: model.CommandLaunchApp}}
		// `label` is universal metadata. A sibling `when:` remains invalid.
		withCondition := swipeCommand(map[string]any{"direction": "UP"}, nil)
		withCondition.Condition = &model.Condition{}
		missingTypedFrom := swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}, "direction": "UP"}, nil)
		mismatchedFrom := swipeCommand(map[string]any{"from": map[string]any{"text": "Continue"}, "direction": "UP"}, swipeSelector("Other", nil))
		unsupportedFrom := swipeCommand(map[string]any{"from": map[string]any{"point": "1,1", "text": "Continue"}, "direction": "UP"}, &model.ElementSelector{TextRegex: stringPointer("Continue"), Point: stringPointer("1,1")})
		for _, command := range []model.Command{wrongKeyword, withChildren, withCondition, missingTypedFrom, mismatchedFrom, unsupportedFrom} {
			if _, err := compileSwipe(command); !isConfigurationError(err) {
				t.Fatalf("compile guard %#v error = %T %v", command, err, err)
			}
		}
	})

	t.Run("evaluate guards", func(t *testing.T) {
		command := swipeCommand(map[string]any{"direction": "UP"}, nil)
		if _, err := evaluateSwipe(context.Background(), batch1AEvaluation(nil), command, struct{}{}); !isConfigurationError(err) {
			t.Fatalf("wrong payload error = %T %v", err, err)
		}
		compiled := swipeCompiled{mode: swipeModeDirection, direction: "UP", durationMillis: 400}
		if _, err := evaluateSwipe(context.Background(), evaluationContext{}, command, compiled); !isConfigurationError(err) {
			t.Fatalf("missing active config error = %T %v", err, err)
		}
		failing := batch1AEvaluation(nil)
		failing.interpolateFn = func(context.Context, string, map[string]any) (string, error) { return "", errors.New("interpolate") }
		if _, err := evaluateSwipe(context.Background(), failing, command, compiled); err == nil {
			t.Fatal("interpolation error = nil")
		}
		blank := batch1AEvaluation(nil)
		blank.activeConfig.AppID = " "
		if _, err := evaluateSwipe(context.Background(), blank, command, compiled); !isConfigurationError(err) {
			t.Fatalf("blank app error = %T %v", err, err)
		}
		badArguments := command
		badArguments.Arguments = "UP"
		if _, err := evaluateSwipe(context.Background(), batch1AEvaluation(nil), badArguments, compiled); !isConfigurationError(err) {
			t.Fatalf("bad arguments error = %T %v", err, err)
		}
	})

	t.Run("execute and request guards", func(t *testing.T) {
		effect, err := executeSwipe(context.Background(), nil, evaluatedDispatch{value: struct{}{}})
		if effect.effectClass != EffectDeviceMutation || !isConfigurationError(err) {
			t.Fatalf("wrong execute payload = effect %#v error %T %v", effect, err, err)
		}
		valid := swipeEvaluated{mode: swipeModeDirection, appID: "app", direction: "UP", durationMillis: 400}
		if _, err := executeSwipe(context.Background(), nil, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
			t.Fatalf("nil state error = %T %v", err, err)
		}
		driver := batch1APointDriver(400, 884)
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
		for _, plan := range []swipeEvaluated{
			{mode: swipeModeDirection, appID: "app", direction: "UP", durationMillis: 0},
			{mode: swipeModeDirection, appID: " ", direction: "UP", durationMillis: 400},
			{mode: swipeMode(99), appID: "app", durationMillis: 400},
			{mode: swipeModeDirection, appID: "app", direction: "UP", start: &tapPointPlan{}, durationMillis: 400},
			{mode: swipeModePoints, appID: "app", durationMillis: 400},
			{mode: swipeModeElement, appID: "app", direction: "UP", durationMillis: 400},
		} {
			if _, err := executeSwipe(context.Background(), state, evaluatedDispatch{value: plan}); !isConfigurationError(err) {
				t.Fatalf("execute plan %#v error = %T %v", plan, err, err)
			}
		}
		assertNoSwipeOrSettle(t, driver)
	})
}

func executeSwipeForTest(ctx context.Context, command model.Command, replacements map[string]string, driver device.Driver, clock Clock, lookup *ElementLookup) (commandEffect, error) {
	registry, err := newHandlerRegistry(swipeHandlerSpec())
	if err != nil {
		return commandEffect{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return commandEffect{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, batch1AEvaluation(replacements), compiled)
	if err != nil {
		return commandEffect{}, err
	}
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	return dispatcher.execute(ctx, state, compiled, evaluated)
}

func swipeCommand(fields map[string]any, selector *model.ElementSelector) model.Command {
	return model.Command{Kind: model.CommandSwipe, Form: model.CommandFormObject, Arguments: cloneDynamic(fields), Selector: cloneSelector(selector)}
}

func swipeSelector(text string, optional *bool) *model.ElementSelector {
	return &model.ElementSelector{TextRegex: stringPointer(text), Optional: clonePointer(optional)}
}

func swipePointRequest(start, end device.Point, duration int64) device.SwipeRequest {
	return device.SwipeRequest{Start: &start, End: &end, DurationMillis: duration}
}

func swipeElementRequest(point device.Point, direction device.Direction, duration int64) device.SwipeRequest {
	return device.SwipeRequest{ElementPoint: &point, Direction: direction, DurationMillis: duration}
}

func swipeRequests(actions []enginetest.Action) []device.SwipeRequest {
	requests := make([]device.SwipeRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodSwipe {
			requests = append(requests, action.Request.(device.SwipeRequest))
		}
	}
	return requests
}

func assertNoSwipeOrSettle(t testing.TB, driver *enginetest.FakeDriver) {
	t.Helper()
	if len(swipeRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("unexpected swipe/settle effects: %#v", driver.Actions())
	}
}

type batch1BCancelSwipeDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch1BCancelSwipeDriver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	err := driver.Driver.Swipe(ctx, request)
	driver.cancel()
	return err
}

type batch1BMutatingSwipeDriver struct {
	device.Driver
	requests chan<- device.SwipeRequest
}

func (driver *batch1BMutatingSwipeDriver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	owned := request
	if request.Start != nil {
		start := *request.Start
		owned.Start = &start
		request.Start.X = 999
	}
	if request.End != nil {
		end := *request.End
		owned.End = &end
		request.End.Y = 999
	}
	driver.requests <- owned
	return driver.Driver.Swipe(ctx, request)
}

func TestInteractionBatch1BRequestUnionPureHelper(t *testing.T) {
	t.Parallel()

	requests := []device.SwipeRequest{
		ownedSwipeDirectionRequest("UP", 400),
		ownedSwipePointRequest(device.Point{X: 1, Y: 2}, device.Point{X: 3, Y: 4}, 1),
		ownedSwipeElementRequest(device.Point{X: 5, Y: 6}, "LEFT", 60000),
	}
	if requests[0].Direction != "UP" || requests[0].Start != nil || requests[0].End != nil || requests[0].ElementPoint != nil ||
		requests[1].Direction != "" || requests[1].Start == nil || requests[1].End == nil || requests[1].ElementPoint != nil ||
		requests[2].Direction != "LEFT" || requests[2].Start != nil || requests[2].End != nil || requests[2].ElementPoint == nil {
		t.Fatalf("request union ownership = %#v", requests)
	}
}
