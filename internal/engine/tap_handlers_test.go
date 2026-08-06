package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/flow"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestTapBatch2StaticPointAndModifierMatrix(t *testing.T) {
	t.Parallel()

	invalid := []struct {
		name string
		body string
	}{
		{name: "missing coordinate", body: "point: '20'"},
		{name: "extra coordinate", body: "point: '20,30,40'"},
		{name: "decimal", body: "point: '20.0,30'"},
		{name: "non integer", body: "point: 'left,30'"},
		{name: "mixed modes", body: "point: '20%,30'"},
		{name: "negative absolute", body: "point: '-1,30'"},
		{name: "negative percentage", body: "point: '-1%,30%'"},
		{name: "percentage upper boundary", body: "point: '100%,30%'"},
		{name: "percentage beyond boundary", body: "point: '30%,101%'"},
		{name: "integer overflow", body: "point: '9223372036854775808,1'"},
		{name: "zero repeat", body: "point: '20,30'\n    repeat: 0"},
		{name: "negative repeat", body: "point: '20,30'\n    repeat: -1"},
		{name: "negative delay with repeat", body: "point: '20,30'\n    repeat: 2\n    delay: -1"},
		{name: "negative discarded delay", body: "point: '20,30'\n    delay: -1"},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			command := parsedTapCommand(t, test.body)
			if _, err := compileTapOn(command); !isConfigurationError(err) {
				t.Fatalf("compileTapOn() error = %T %v, want ConfigurationError", err, err)
			}
		})
	}

	accepted := []struct {
		name string
		body string
	}{
		{name: "screen absolute zero", body: "point: '0,0'"},
		{name: "screen percentage zero", body: "point: '0%,0%'"},
		{name: "trimmed integers", body: "point: ' 200 , 318 '"},
		{name: "selector center", body: "text: Continue"},
		{name: "selector relative", body: "text: Continue\n    point: '50%,50%'"},
		{name: "repeat default delay", body: "point: '20,30'\n    repeat: 3"},
		{name: "repeat explicit delay", body: "point: '20,30'\n    repeat: 3\n    delay: 400"},
		{name: "discarded nonnegative delay", body: "point: '20,30'\n    delay: 400"},
		{name: "negative settle accepted", body: "point: '20,30'\n    waitToSettleTimeoutMs: -1"},
	}
	for _, test := range accepted {
		t.Run(test.name, func(t *testing.T) {
			command := parsedTapCommand(t, test.body)
			if _, err := compileTapOn(command); err != nil {
				t.Fatalf("compileTapOn() error = %v", err)
			}
		})
	}
}

func TestTapBatch2PointInterpolationDetectionMatchesSinglePassGrammar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		point            string
		requiresPass     bool
		wantCompileError bool
	}{
		{name: "complete expression", point: "${POINT}", requiresPass: true},
		{name: "escaped expression is literal", point: `\${POINT}`, wantCompileError: true},
		{name: "unterminated expression is literal", point: "${POINT", wantCompileError: true},
		{name: "nested dollar exposes inner expression", point: "${OUTER${POINT}}", requiresPass: true},
		{name: "dollar inside candidate prevents expression", point: "${'$'}", wantCompileError: true},
		{name: "second dollar starts expression", point: "$${POINT}", requiresPass: true},
		{name: "escaped outer still exposes nested expression", point: `\${OUTER${POINT}}`, requiresPass: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tapPointRequiresEvaluation(test.point); got != test.requiresPass {
				t.Fatalf("tapPointRequiresEvaluation(%q) = %t, want %t", test.point, got, test.requiresPass)
			}

			command := parsedTapCommand(t, "point: '20,30'")
			point := test.point
			command.Selector.Point = &point
			command.Arguments.(map[string]any)["point"] = test.point
			_, err := compileTapOn(command)
			if test.wantCompileError {
				if !isConfigurationError(err) {
					t.Fatalf("compileTapOn(%q) error = %T %v, want ConfigurationError", test.point, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("compileTapOn(%q) error = %v", test.point, err)
			}
		})
	}
}

func TestTapBatch2UnrelatedFieldsRemainStaticErrors(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"text: Continue\n    start: '0,0'",
		"text: Continue\n    end: '1,1'",
	} {
		command := parsedTapCommand(t, body)
		if _, err := compileTapOn(command); !isConfigurationError(err) {
			t.Fatalf("compileTapOn(%q) error = %T %v, want ConfigurationError", body, err, err)
		}
	}

	// CSS selectors compile for platform preflight and driver lookup.
	command := parsedTapCommand(t, "text: Continue\n    css: '#continue'")
	if _, err := compileTapOn(command); err != nil {
		t.Fatalf("compileTapOn(css) error = %v, want it to compile", err)
	}
}

func TestTapBatch2RawTypedMismatchFailsCompilation(t *testing.T) {
	t.Parallel()

	command := parsedTapCommand(t, "point: '20,30'\n    repeat: 2")
	command.Arguments.(map[string]any)["repeat"] = int64(3)
	if _, err := compileTapOn(command); !isConfigurationError(err) {
		t.Fatalf("repeat mismatch error = %T %v, want ConfigurationError", err, err)
	}

	command = parsedTapCommand(t, "text: Continue\n    point: '20,30'")
	other := "Different"
	command.Selector.TextRegex = &other
	if _, err := compileTapOn(command); !isConfigurationError(err) {
		t.Fatalf("selector mismatch error = %T %v, want ConfigurationError", err, err)
	}
}

func TestTapBatch2PointResolutionAndMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		body      string
		env       map[string]string
		width     int
		height    int
		wantPoint device.Point
		wantDelay *int
	}{
		{name: "observed absolute", body: "point: '200,318'\n    waitToSettleTimeoutMs: 0", width: 400, height: 884, wantPoint: device.Point{X: 200, Y: 318}},
		{name: "observed percent", body: "point: '50%,36%'\n    waitToSettleTimeoutMs: 0", width: 400, height: 884, wantPoint: device.Point{X: 200, Y: 318}},
		{name: "late valid", body: "point: '${POINT}'\n    waitToSettleTimeoutMs: 0", env: map[string]string{"POINT": "200,318"}, width: 400, height: 884, wantPoint: device.Point{X: 200, Y: 318}},
		{name: "repeat default delay metadata", body: "point: '20,30'\n    repeat: 3\n    waitToSettleTimeoutMs: 0", width: 400, height: 884, wantPoint: device.Point{X: 20, Y: 30}, wantDelay: intPointerForTap(100)},
		{name: "repeat explicit delay metadata", body: "point: '20,30'\n    repeat: 3\n    delay: 400\n    waitToSettleTimeoutMs: 0", width: 400, height: 884, wantPoint: device.Point{X: 20, Y: 30}, wantDelay: intPointerForTap(400)},
		{name: "delay without repeat discarded", body: "point: '20,30'\n    delay: 400\n    waitToSettleTimeoutMs: 0", width: 400, height: 884, wantPoint: device.Point{X: 20, Y: 30}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapFlow(t, test.body, test.env)
			source := cloneFlow(flowModel)
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
				Platform: "android", WidthGrid: test.width, HeightGrid: test.height,
			}}}})
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-point-" + strings.ReplaceAll(test.name, " ", "-"),
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			commandResult := onlyTapCommandResult(t, results)
			if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, repeatedTapRequests(test.wantPoint, tapRepeatCount(flowModel.Commands[0]))) {
				t.Fatalf("Tap requests = %#v, want point %+v", got, test.wantPoint)
			}
			evaluated, ok := commandResult.Metadata().EvaluatedCommand()
			if !ok || evaluated.Selector == nil {
				t.Fatalf("evaluated command = %#v, want selector", evaluated)
			}
			if !intPointersEqual(evaluated.Selector.Delay, test.wantDelay) {
				t.Fatalf("evaluated delay = %v, want %v", pointerValue(evaluated.Selector.Delay), pointerValue(test.wantDelay))
			}
			if !reflect.DeepEqual(flowModel, source) {
				t.Fatalf("authored flow mutated: got %#v want %#v", flowModel, source)
			}
		})
	}
}

func TestTapBatch2LateInvalidPointFailsBeforeTapAndPreservesSource(t *testing.T) {
	t.Parallel()

	flowModel := parsedTapFlow(t, "point: '${POINT}'", map[string]string{"POINT": "20%,30"})
	source := cloneFlow(flowModel)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
		Platform: "android", WidthGrid: 400, HeightGrid: 884,
	}}}})
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch2-late-invalid",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if !isConfigurationError(err) {
		t.Fatalf("Execute() error = %T %v, want ConfigurationError", err, err)
	}
	if got := tapRequests(driver.Actions()); len(got) != 0 {
		t.Fatalf("Tap requests = %#v, want none", got)
	}
	if len(results) != 1 || len(results[0].Commands()) != 1 {
		t.Fatalf("results = %#v, want evaluated command failure", results)
	}
	if !reflect.DeepEqual(flowModel, source) {
		t.Fatal("late invalid point mutated authored flow")
	}
}

func TestTapBatch2PointBoundariesFailBeforeTap(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		body string
	}{
		{name: "screen x upper", body: "point: '400,0'"},
		{name: "screen y upper", body: "point: '0,884'"},
		{name: "screen beyond", body: "point: '401,885'"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapFlow(t, test.body, nil)
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
				Platform: "android", WidthGrid: 400, HeightGrid: 884,
			}}}})
			_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-boundary-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if !isConfigurationError(err) || len(tapRequests(driver.Actions())) != 0 {
				t.Fatalf("Execute() = error %T %v taps %#v, want configuration/no tap", err, err, tapRequests(driver.Actions()))
			}
		})
	}
}

func TestTapBatch2SelectorRelativeAndStability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		point     string
		bounds    device.Bounds
		wantPoint device.Point
		wantError bool
	}{
		{name: "observed relative percentage", point: "50%,50%", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantPoint: device.Point{X: 40, Y: 40}},
		{name: "relative absolute zero", point: "0,0", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantPoint: device.Point{X: 10, Y: 20}},
		{name: "relative absolute", point: "59,39", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantPoint: device.Point{X: 69, Y: 59}},
		{name: "relative percent near upper", point: "99%,99%", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantPoint: device.Point{X: 69, Y: 59}},
		{name: "relative x upper", point: "60,0", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative y upper", point: "0,40", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative negative", point: "-1,0", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative mixed", point: "1%,2", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative decimal", point: "1.5,2", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative malformed", point: "1,2,3", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
		{name: "relative percent upper", point: "100%,0%", bounds: device.Bounds{X: 10, Y: 20, Width: 60, Height: 40}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapFlow(t, fmt.Sprintf("text: Continue\n    point: '%s'\n    waitToSettleTimeoutMs: 0", test.point), nil)
			tree := tapTree(test.bounds)
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
				ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}},
			})
			_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-relative-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if test.wantError {
				if !isConfigurationError(err) || len(tapRequests(driver.Actions())) != 0 {
					t.Fatalf("Execute() = error %T %v taps %#v, want configuration/no tap", err, err, tapRequests(driver.Actions()))
				}
				return
			}
			if err != nil || !reflect.DeepEqual(tapRequests(driver.Actions()), []device.TapRequest{{Point: test.wantPoint}}) {
				t.Fatalf("Execute() = error %v taps %#v, want %+v", err, tapRequests(driver.Actions()), test.wantPoint)
			}
		})
	}
}

func TestTapBatch2RepeatDelayAndSettleOrdering(t *testing.T) {
	t.Parallel()

	flowModel := parsedTapFlow(t, "point: '20,30'\n    repeat: 3\n    delay: 400\n    waitToSettleTimeoutMs: 100", nil)
	clock := newTapTraceClock(time.Unix(100, 0))
	driver := newTapTraceDriver(clock)
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: nil}},
	})
	lookup := NewElementLookup(driver, clock)
	if _, err := lookup.cachedDeviceInfo(context.Background()); err != nil {
		t.Fatalf("cachedDeviceInfo() error = %v", err)
	}
	state := tapExecutionState(driver, clock, lookup)
	evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
	_, err := executeTapOn(context.Background(), state, evaluated)
	if err != nil {
		t.Fatalf("executeTapOn() error = %v", err)
	}
	want := []string{
		"tap@0ms", "now@10ms", "wait:400ms@10ms",
		"tap@410ms", "now@420ms", "wait:400ms@420ms",
		"tap@820ms", "now@830ms", "now@830ms", "now@830ms", "now@830ms",
		"wait:100ms@830ms", "settle@930ms", "now@930ms",
	}
	if got := clock.Trace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap/clock trace = %#v, want %#v", got, want)
	}
	if got := tapRequests(driver.Actions()); len(got) != 3 {
		t.Fatalf("Tap count = %d, want 3", len(got))
	}
	settles := settleRequests(driver.Actions())
	if len(settles) != 1 || settles[0].TimeoutMillis == nil || *settles[0].TimeoutMillis != 100 {
		t.Fatalf("settle requests = %#v, want one exact 100ms request", settles)
	}
}

func TestTapBatch2TapFailureAndCancellationSkipLaterWork(t *testing.T) {
	t.Parallel()

	t.Run("partial failure keeps last watermark and skips settle", func(t *testing.T) {
		primary := errors.New("second tap failed")
		flowModel := parsedTapFlow(t, "point: '20,30'\n    repeat: 3\n    delay: 400", nil)
		clock := newTapTraceClock(time.Unix(100, 0))
		driver := newTapTraceDriver(clock)
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			Tap:        []enginetest.Result[struct{}]{{}, {Err: primary}},
		})
		lookup := NewElementLookup(driver, clock)
		_, _ = lookup.cachedDeviceInfo(context.Background())
		state := tapExecutionState(driver, clock, lookup)
		evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
		_, err := executeTapOn(context.Background(), state, evaluated)
		if !errors.Is(err, primary) {
			t.Fatalf("executeTapOn() error = %T %v, want original cause", err, err)
		}
		if len(tapRequests(driver.Actions())) != 2 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("actions = %#v, want two taps/no settle", driver.Actions())
		}
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-410*time.Millisecond; got != want {
			t.Fatalf("adjusted timeout = %v, want %v from first successful tap", got, want)
		}
	})

	t.Run("context ignoring successful tap records before cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		flowModel := parsedTapFlow(t, "point: '20,30'\n    repeat: 2", nil)
		clock := newTapTraceClock(time.Unix(200, 0))
		driver := newTapTraceDriver(clock)
		driver.cancelAfterTap = cancel
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
		lookup := NewElementLookup(driver, clock)
		_, _ = lookup.cachedDeviceInfo(context.Background())
		state := tapExecutionState(driver, clock, lookup)
		evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
		_, err := executeTapOn(ctx, state, evaluated)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("executeTapOn() error = %T %v, want context.Canceled", err, err)
		}
		if len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("actions = %#v, want one tap/no settle", driver.Actions())
		}
		if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
			t.Fatalf("adjusted timeout immediately after cancelled successful tap = %v, want %v", got, LookupTimeout)
		}
		if got := clock.Trace(); len(got) < 2 || got[0] != "tap@0ms" || got[1] != "now@10ms" {
			t.Fatalf("tap/cancel trace = %#v, want tap then watermark Now", got)
		}
	})
}

func TestTapBatch2SettlePolicy(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		timeout    string
		wantMillis *int64
		wantCalls  int
	}{
		{name: "omitted", wantCalls: HierarchySettleAttempts},
		{name: "zero", timeout: "\n    waitToSettleTimeoutMs: 0", wantMillis: int64PointerForTap(0), wantCalls: 1},
		{name: "negative normalized", timeout: "\n    waitToSettleTimeoutMs: -1", wantMillis: int64PointerForTap(0), wantCalls: 1},
		{name: "positive", timeout: "\n    waitToSettleTimeoutMs: 100", wantMillis: int64PointerForTap(100), wantCalls: 1},
		{name: "capped", timeout: "\n    waitToSettleTimeoutMs: 30001", wantMillis: int64PointerForTap(30000), wantCalls: 150},
	} {
		t.Run(test.name, func(t *testing.T) {
			flowModel := parsedTapFlow(t, "point: '20,30'"+test.timeout, nil)
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}}})
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-settle-" + test.name,
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			result := onlyTapCommandResult(t, results)
			settles := settleRequests(driver.Actions())
			if len(settles) != test.wantCalls {
				t.Fatalf("settle calls = %d, want %d", len(settles), test.wantCalls)
			}
			for _, request := range settles {
				if !int64PointersEqual(request.TimeoutMillis, test.wantMillis) {
					t.Fatalf("settle timeout = %v, want %v", pointerValue(request.TimeoutMillis), pointerValue(test.wantMillis))
				}
			}
			evaluated, _ := result.Metadata().EvaluatedCommand()
			if test.name == "negative normalized" && (evaluated.Selector == nil || evaluated.Selector.WaitToSettleTimeoutMS == nil || *evaluated.Selector.WaitToSettleTimeoutMS != -1) {
				t.Fatalf("evaluated metadata lost authored negative settle: %#v", evaluated.Selector)
			}
			if test.name == "capped" && (evaluated.Selector == nil || evaluated.Selector.WaitToSettleTimeoutMS == nil || *evaluated.Selector.WaitToSettleTimeoutMS != 30001) {
				t.Fatalf("evaluated metadata lost authored above-cap settle: %#v", evaluated.Selector)
			}
		})
	}

	t.Run("ordinary settle failure is best effort", func(t *testing.T) {
		flowModel := parsedTapFlow(t, "point: '20,30'\n    waitToSettleTimeoutMs: 100", nil)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
			WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: errors.New("ordinary settle failure")}},
		})
		results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch2-settle-best-effort",
			Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != nil || onlyTapCommandResult(t, results).Outcome() != Completed {
			t.Fatalf("best-effort settle = error %v results %#v", err, results)
		}
	})

	for _, terminal := range []error{
		context.Canceled,
		NewDeviceConnectionError("settle disconnected", nil),
		NewConfigurationError("settle misconfigured", nil),
	} {
		t.Run(fmt.Sprintf("terminal %T", terminal), func(t *testing.T) {
			flowModel := parsedTapFlow(t, "point: '20,30'\n    waitToSettleTimeoutMs: 100", nil)
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
				WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: terminal}},
			})
			_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch2-settle-terminal",
				Driver:      driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if err != terminal {
				t.Fatalf("Execute() error = %T %v, want exact %T %v", err, err, terminal, terminal)
			}
		})
	}
}

func TestTapBatch5ProductionRegistryIsNineAndTapRemainsOwned(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	want := productionKeywords()
	if len(registry.byKeyword) != len(want) {
		t.Fatalf("production registry keys = %v, want exactly thirty", sortedRegistryKeywords(registry))
	}
	for _, keyword := range want {
		if _, ok := registry.lookup(keyword); !ok {
			t.Fatalf("production registry missing %s", keyword)
		}
	}
	spec, _ := registry.lookup(model.CommandTapOn)
	if spec.postAction != postActionNoSettle || spec.settleRequest != nil {
		t.Fatalf("tapOn post action = %v factory %v, want tap-owned no-settle", spec.postAction, spec.settleRequest != nil)
	}
}

func TestTapBatch2StaticInvalidProgramHasNoEffects(t *testing.T) {
	t.Parallel()

	flowModel := parsedTapFlow(t, "point: '20%,30'", nil)
	driver := enginetest.NewFakeDriver()
	factory := &countingRuntimeFactory{}
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch2-static-no-effects",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if !isConfigurationError(err) || len(results) != 0 || factory.calls != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("static invalid = error %T %v results %d runtime %d actions %#v", err, err, len(results), factory.calls, driver.Actions())
	}
}

func parsedTapCommand(t testing.TB, body string) model.Command {
	t.Helper()
	flowModel := parsedTapFlow(t, body, nil)
	if len(flowModel.Commands) != 1 {
		t.Fatalf("parsed commands = %d, want 1", len(flowModel.Commands))
	}
	return flowModel.Commands[0]
}

func parsedTapFlow(t testing.TB, body string, env map[string]string) model.Flow {
	t.Helper()
	var config strings.Builder
	config.WriteString("appId: com.example.tap-batch2\n")
	if len(env) != 0 {
		config.WriteString("env:\n")
		for key, value := range env {
			fmt.Fprintf(&config, "  %s: %q\n", key, value)
		}
	}
	var command strings.Builder
	command.WriteString("---\n- tapOn:\n")
	for _, line := range strings.Split(body, "\n") {
		command.WriteString("    ")
		command.WriteString(strings.TrimSpace(line))
		command.WriteByte('\n')
	}
	contents := config.String() + command.String()
	flowModel, err := flow.ParseBytes("/workspace/tap-batch2.yaml", []byte(contents))
	if err != nil {
		t.Fatalf("flow.ParseBytes() error = %v\n%s", err, contents)
	}
	return flowModel
}

func tapJSFactory(t testing.TB) js.Factory {
	t.Helper()
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	return factory
}

func onlyTapCommandResult(t testing.TB, results []FlowResult) CommandResult {
	t.Helper()
	if len(results) != 1 || len(results[0].Commands()) != 1 {
		t.Fatalf("flow/command results = %d/%d, want 1/1", len(results), commandResultCount(results))
	}
	result := results[0].Commands()[0]
	if result.Command().Kind != model.CommandTapOn {
		t.Fatalf("command kind = %s, want tapOn", result.Command().Kind)
	}
	return result
}

func commandResultCount(results []FlowResult) int {
	if len(results) == 0 {
		return 0
	}
	return len(results[0].Commands())
}

func tapRequests(actions []enginetest.Action) []device.TapRequest {
	requests := make([]device.TapRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodTap {
			requests = append(requests, action.Request.(device.TapRequest))
		}
	}
	return requests
}

func settleRequests(actions []enginetest.Action) []device.SettleRequest {
	requests := make([]device.SettleRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodWaitForAppToSettle {
			requests = append(requests, action.Request.(device.SettleRequest))
		}
	}
	return requests
}

func repeatedTapRequests(point device.Point, count int) []device.TapRequest {
	requests := make([]device.TapRequest, count)
	for index := range requests {
		requests[index] = device.TapRequest{Point: point}
	}
	return requests
}

func tapRepeatCount(command model.Command) int {
	if command.Selector == nil || command.Selector.Repeat == nil {
		return 1
	}
	return *command.Selector.Repeat
}

func tapTree(bounds device.Bounds) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][400,884]"},
		Children: []device.TreeNode{{Attributes: map[string]string{
			"text": "Continue", "bounds": fmt.Sprintf("[%d,%d][%d,%d]", bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height),
		}}},
	}
}

func evaluatedTapForTest(t testing.TB, command model.Command, config model.Config, clock Clock) evaluatedDispatch {
	t.Helper()
	compiled, err := compileTapOn(command)
	if err != nil {
		t.Fatalf("compileTapOn() error = %v", err)
	}
	evaluation := evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			for key, value := range config.Env {
				input = strings.ReplaceAll(input, "${"+key+"}", value)
			}
			return input, nil
		},
		activeConfig: config, hasActiveConfig: true,
	}
	evaluated, err := evaluateTapOn(context.Background(), evaluation, command, compiled)
	if err != nil {
		t.Fatalf("evaluateTapOn() error = %v", err)
	}
	_ = clock
	return evaluated
}

func tapExecutionState(driver device.Driver, clock Clock, lookup *ElementLookup) *executionState {
	return &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
	}
}

type tapTraceClock struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
	trace []string
}

func newTapTraceClock(start time.Time) *tapTraceClock {
	return &tapTraceClock{start: start, now: start}
}

func (clock *tapTraceClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.trace = append(clock.trace, fmt.Sprintf("now@%dms", clock.now.Sub(clock.start).Milliseconds()))
	return clock.now
}

func (clock *tapTraceClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.trace = append(clock.trace, fmt.Sprintf("wait:%dms@%dms", delay.Milliseconds(), clock.now.Sub(clock.start).Milliseconds()))
	clock.now = clock.now.Add(delay)
	clock.mu.Unlock()
	return ctx.Err()
}

func (clock *tapTraceClock) advance(delta time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delta)
	clock.mu.Unlock()
}

func (clock *tapTraceClock) record(label string) {
	clock.mu.Lock()
	clock.trace = append(clock.trace, fmt.Sprintf("%s@%dms", label, clock.now.Sub(clock.start).Milliseconds()))
	clock.mu.Unlock()
}

func (clock *tapTraceClock) Trace() []string {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]string(nil), clock.trace...)
}

type tapTraceDriver struct {
	*enginetest.FakeDriver
	clock          *tapTraceClock
	cancelAfterTap context.CancelFunc
}

func newTapTraceDriver(clock *tapTraceClock) *tapTraceDriver {
	return &tapTraceDriver{FakeDriver: enginetest.NewFakeDriver(), clock: clock}
}

func (driver *tapTraceDriver) Tap(ctx context.Context, request device.TapRequest) error {
	driver.clock.record("tap")
	driver.clock.advance(10 * time.Millisecond)
	err := driver.FakeDriver.Tap(context.Background(), request)
	if driver.cancelAfterTap != nil {
		driver.cancelAfterTap()
	}
	if err != nil {
		return err
	}
	return nil
}

func (driver *tapTraceDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	driver.clock.record("settle")
	return driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
}

func isConfigurationError(err error) bool {
	var configuration *ConfigurationError
	return errors.As(err, &configuration)
}

func intPointerForTap(value int) *int       { return &value }
func int64PointerForTap(value int64) *int64 { return &value }

func intPointersEqual(left, right *int) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func int64PointersEqual(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func pointerValue[T any](value *T) any {
	if value == nil {
		return nil
	}
	return *value
}

func sortedRegistryKeywords(registry handlerRegistry) []model.CommandKeyword {
	keywords := make([]model.CommandKeyword, 0, len(registry.byKeyword))
	for keyword := range registry.byKeyword {
		keywords = append(keywords, keyword)
	}
	sort.Slice(keywords, func(left, right int) bool { return keywords[left] < keywords[right] })
	return keywords
}

// A CSS-only selector is a complete target resolved by a capable driver.
func TestCSSAloneIsATargetPredicate(t *testing.T) {
	t.Parallel()

	command := parsedTapCommand(t, "css: '#login'")
	if _, err := compileTapOn(command); err != nil {
		t.Errorf("tapOn with a css-only selector did not compile: %v", err)
	}
	css := "#login"
	assertion := model.Command{
		Kind:     model.CommandAssertVisible,
		Form:     model.CommandFormObject,
		Selector: &model.ElementSelector{CSS: &css},
	}
	if _, err := compileAssertVisible(assertion); err != nil {
		t.Errorf("assertVisible with a css-only selector did not compile: %v", err)
	}
}
