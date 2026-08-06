package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/imagecheck"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestTapBatch3StaticFlagsAndPointWaitBoundary(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"text: Continue\nretryTapIfNoChange: false",
		"text: Continue\nretryTapIfNoChange: true",
		"text: Continue\nwaitUntilVisible: false",
		"text: Continue\nwaitUntilVisible: true",
		"text: Continue\npoint: '50%,50%'\nretryTapIfNoChange: true\nwaitUntilVisible: true",
		"point: '20,30'\nretryTapIfNoChange: true",
	} {
		if _, err := compileTapOn(parsedTapCommand(t, body)); err != nil {
			t.Fatalf("compileTapOn(%q) error = %v", body, err)
		}
	}
	for _, body := range []string{
		"point: '20,30'\nwaitUntilVisible: false",
		"point: '20,30'\nwaitUntilVisible: true",
	} {
		if _, err := compileTapOn(parsedTapCommand(t, body)); !isConfigurationError(err) {
			t.Fatalf("compileTapOn(%q) error = %T %v, want ConfigurationError", body, err, err)
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*model.Command)
	}{
		{name: "nested retry", mutate: func(command *model.Command) {
			value := false
			command.Selector.Below.RetryTapIfNoChange = &value
			command.Arguments.(map[string]any)["below"].(map[string]any)["retryTapIfNoChange"] = false
		}},
		{name: "nested wait", mutate: func(command *model.Command) {
			value := true
			command.Selector.Below.WaitUntilVisible = &value
			command.Arguments.(map[string]any)["below"].(map[string]any)["waitUntilVisible"] = true
		}},
	} {
		command := parsedTapCommand(t, "text: Continue")
		anchor := "Anchor"
		command.Selector.Below = &model.ElementSelector{TextRegex: &anchor}
		command.Arguments.(map[string]any)["below"] = map[string]any{"text": "Anchor"}
		test.mutate(&command)
		if _, err := compileTapOn(command); !isConfigurationError(err) {
			t.Fatalf("%s error = %T %v, want ConfigurationError", test.name, err, err)
		}
	}

	flowModel := parsedTapFlow(t, "point: '20,30'\nwaitUntilVisible: false", nil)
	driver := enginetest.NewFakeDriver()
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-point-wait-static", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if !isConfigurationError(err) || len(results) != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("point-only authored wait = error %T %v results %d actions %#v, want static zero effects", err, err, len(results), driver.Actions())
	}
}

func TestTapBatch3RetryDecisionMatrixAndExactOrdering(t *testing.T) {
	t.Parallel()

	expected, same, exactThreshold, aboveThreshold := tapBatch3PNGs(t)
	unchanged := tapBatch3RawTree("same")
	changed := tapBatch3RawTree("changed")
	for _, test := range []struct {
		name            string
		postHierarchy   device.TreeNode
		actual          []byte
		wantTaps        int
		wantSettles     int
		wantScreenshots int
		wantChecks      int
		wantMethods     []enginetest.Method
	}{
		{
			name: "hierarchy changed", postHierarchy: changed, actual: same,
			wantTaps: 1, wantSettles: 1, wantScreenshots: 1,
			wantMethods: []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle},
		},
		{
			name: "ratio 0.01 stops", postHierarchy: unchanged, actual: aboveThreshold,
			wantTaps: 1, wantSettles: 1, wantScreenshots: 2, wantChecks: 1,
			wantMethods: []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle, enginetest.MethodTakeScreenshot},
		},
		{
			name: "ratio 0.005 retries", postHierarchy: unchanged, actual: exactThreshold,
			wantTaps: 2, wantSettles: 2, wantScreenshots: 2, wantChecks: 1,
			wantMethods: []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle},
		},
		{
			name: "unchanged exactly two batches", postHierarchy: unchanged, actual: same,
			wantTaps: 2, wantSettles: 2, wantScreenshots: 2, wantChecks: 1,
			wantMethods: []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 400, HeightGrid: 884}}},
				ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: unchanged}},
				TakeScreenshot:    []enginetest.Result[[]byte]{{Value: expected}, {Value: test.actual}},
				WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
					{Value: &device.ViewHierarchy{Root: test.postHierarchy}}, {Value: nil},
				},
			})
			checker := &tapBatch3Checker{}
			flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch3-retry-" + test.name, Driver: driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: checker,
			})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := len(tapRequests(driver.Actions())); got != test.wantTaps {
				t.Fatalf("tap count = %d, want %d", got, test.wantTaps)
			}
			if got := len(settleRequests(driver.Actions())); got != test.wantSettles {
				t.Fatalf("settle count = %d, want %d", got, test.wantSettles)
			}
			settles := settleRequests(driver.Actions())
			if len(settles) != 0 && !reflect.DeepEqual(settles[0].InitialHierarchy, &device.ViewHierarchy{Root: unchanged}) {
				t.Fatalf("first settle initial hierarchy = %#v, want exact owned expected %#v", settles[0].InitialHierarchy, unchanged)
			}
			if len(settles) == 2 && settles[1].InitialHierarchy != nil {
				t.Fatalf("second settle initial hierarchy = %#v, want nil because no second decision", settles[1].InitialHierarchy)
			}
			if got := countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot); got != test.wantScreenshots {
				t.Fatalf("screenshot count = %d, want %d", got, test.wantScreenshots)
			}
			if got := checker.Calls(); got != test.wantChecks {
				t.Fatalf("check count = %d, want %d", got, test.wantChecks)
			}
			if got := tapBatch3RelevantMethods(driver.Actions()); !reflect.DeepEqual(got, test.wantMethods) {
				t.Fatalf("action order = %#v, want %#v", got, test.wantMethods)
			}
			for _, action := range driver.Actions() {
				if action.Method == enginetest.MethodTakeScreenshot && action.Request.(device.ScreenshotRequest).Compressed {
					t.Fatalf("retry screenshot request = %#v, want explicit uncompressed", action.Request)
				}
			}
			metadata := onlyTapCommandResult(t, results).Metadata()
			if !metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != test.wantTaps {
				t.Fatalf("batch metadata = present %t runs %d, want %d", metadata.HasNumberOfRuns(), metadata.NumberOfRuns(), test.wantTaps)
			}
			evaluated, ok := metadata.EvaluatedCommand()
			if !ok || evaluated.Selector == nil || evaluated.Selector.RetryTapIfNoChange == nil || !*evaluated.Selector.RetryTapIfNoChange ||
				evaluated.Arguments.(map[string]any)["retryTapIfNoChange"] != true || !selectorCommandSnapshotMatches(evaluated) {
				t.Fatalf("evaluated retry metadata = %#v, want synchronized authored true", evaluated)
			}
		})
	}
}

func TestTapBatch3RetryFalseOrOmittedHasOneBatchAndNoDecisionEffects(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"", "\nretryTapIfNoChange: false"} {
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}})
		flowModel := parsedTapFlow(t, "point: '20,30'\nwaitToSettleTimeoutMs: 0"+flag, nil)
		results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch3-no-retry", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		})
		if err != nil || len(tapRequests(driver.Actions())) != 1 || countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor) != 0 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 0 {
			t.Fatalf("retry %q = err %v actions %#v", flag, err, driver.Actions())
		}
		metadata := onlyTapCommandResult(t, results).Metadata()
		if !metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != 1 {
			t.Fatalf("one-batch metadata = present %t runs %d", metadata.HasNumberOfRuns(), metadata.NumberOfRuns())
		}
	}
}

func TestTapBatch3RetryReResolvesSelectorPointPerBatch(t *testing.T) {
	t.Parallel()
	treeA := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	treeB := tapTree(device.Bounds{X: 50, Y: 60, Width: 20, Height: 20})
	expected, same, _, _ := tapBatch3PNGs(t)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: treeA}, {Value: treeA}, {Value: treeA},
			{Value: treeB}, {Value: treeB},
		},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
			{Value: &device.ViewHierarchy{Root: treeA}}, {Value: nil},
		},
	})
	flowModel := parsedTapFlow(t, "text: Continue\npoint: '50%,50%'\nrepeat: 3\ndelay: 0\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-reresolve", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		ImageChecker: &tapBatch3Checker{},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []device.TapRequest{
		{Point: device.Point{X: 20, Y: 30}}, {Point: device.Point{X: 20, Y: 30}}, {Point: device.Point{X: 20, Y: 30}},
		{Point: device.Point{X: 60, Y: 70}}, {Point: device.Point{X: 60, Y: 70}}, {Point: device.Point{X: 60, Y: 70}},
	}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap points = %#v, want fresh batch points %#v", got, want)
	}
	if metadata := onlyTapCommandResult(t, results).Metadata(); metadata.NumberOfRuns() != 2 {
		t.Fatalf("batch runs = %d, want 2", metadata.NumberOfRuns())
	}
}

func TestTapBatch3RetryServiceFailuresAndBatchMetadata(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	for _, test := range []struct {
		name         string
		screens      []enginetest.Result[[]byte]
		settles      []enginetest.Result[*device.ViewHierarchy]
		checkerError error
		wantTaps     int
		wantRuns     int
		wantScreens  int
	}{
		{name: "expected screenshot", screens: []enginetest.Result[[]byte]{{Err: errors.New("expected screenshot")}}, wantRuns: 0, wantScreens: 1},
		{name: "post screenshot", screens: []enginetest.Result[[]byte]{{Value: expected}, {Err: errors.New("post screenshot")}}, settles: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}}, wantTaps: 1, wantRuns: 1, wantScreens: 2},
		{name: "checker", screens: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, settles: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}}, checkerError: errors.New("checker"), wantTaps: 1, wantRuns: 1, wantScreens: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			primary := test.checkerError
			if primary == nil {
				primary = test.screens[len(test.screens)-1].Err
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
				ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}}, TakeScreenshot: test.screens, WaitForAppToSettle: test.settles,
			})
			flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch3-error-" + test.name, Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
				ImageChecker: &tapBatch3Checker{err: test.checkerError},
			})
			if !errors.Is(err, primary) || len(tapRequests(driver.Actions())) != test.wantTaps || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != test.wantScreens {
				t.Fatalf("Execute() = err %T %v taps %d screens %d, want cause/taps/screens %d/%d", err, err, len(tapRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot), test.wantTaps, test.wantScreens)
			}
			metadata := onlyTapCommandResult(t, results).Metadata()
			if !metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != test.wantRuns {
				t.Fatalf("failure metadata = present %t runs %d, want %d", metadata.HasNumberOfRuns(), metadata.NumberOfRuns(), test.wantRuns)
			}
		})
	}

	for _, typedNil := range []bool{false, true} {
		driver := enginetest.NewFakeDriver()
		var checker ImageChecker
		if typedNil {
			var value *tapBatch3Checker
			checker = value
		}
		flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true", nil)
		clock := newAdvancingClock()
		evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
		state := tapExecutionState(driver, clock, NewElementLookup(driver, clock))
		state.dependencies.ImageChecker = checker
		effect, err := executeTapOn(context.Background(), state, evaluated)
		if !isConfigurationError(err) || len(driver.Actions()) != 0 || !effect.numberOfRunsSet || effect.numberOfRuns != 0 {
			t.Fatalf("typedNil=%t = err %T %v actions %#v effect %#v", typedNil, err, err, driver.Actions(), effect)
		}
	}
}

func TestTapBatch3CheckerResultValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name             string
		different, total uint64
		ratio            float64
		wantError        bool
	}{
		{name: "valid zero difference", total: 200, ratio: 0},
		{name: "valid threshold", different: 1, total: 200, ratio: 0.005},
		{name: "zero total", ratio: 0, wantError: true},
		{name: "different exceeds total", different: 2, total: 1, ratio: 1, wantError: true},
		{name: "nan", total: 1, ratio: math.NaN(), wantError: true},
		{name: "positive infinity", total: 1, ratio: math.Inf(1), wantError: true},
		{name: "negative", total: 1, ratio: -0.1, wantError: true},
		{name: "above one", total: 1, ratio: 1.1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateImageCheckValues(test.different, test.total, test.ratio)
			if test.wantError != isConfigurationError(err) {
				t.Fatalf("validateImageCheckValues() error = %T %v, want config=%t", err, err, test.wantError)
			}
		})
	}
}

func TestTapBatch3EverySuccessfulRetryTapAdvancesWatermarkAndPartialFailureStops(t *testing.T) {
	t.Parallel()
	primary := errors.New("fourth tap failed")
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	clock := newTapTraceClock(time.Unix(700, 0))
	driver := newTapTraceDriver(clock)
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}}, TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}},
		Tap: []enginetest.Result[struct{}]{{}, {}, {}, {Err: primary}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}},
	})
	lookup := NewElementLookup(driver, clock)
	flowModel := parsedTapFlow(t, "point: '20,30'\nrepeat: 2\ndelay: 100\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	evaluated := evaluatedTapForTest(t, flowModel.Commands[0], flowModel.Config, clock)
	state := tapExecutionState(driver, clock, lookup)
	state.dependencies.ImageChecker = &tapBatch3Checker{}
	effect, err := executeTapOn(context.Background(), state, evaluated)
	if !errors.Is(err, primary) || !effect.numberOfRunsSet || effect.numberOfRuns != 2 {
		t.Fatalf("executeTapOn() = effect %#v error %T %v, want batch2 partial failure", effect, err, err)
	}
	if got := len(tapRequests(driver.Actions())); got != 4 || len(settleRequests(driver.Actions())) != 1 {
		t.Fatalf("partial retry actions = taps %d settles %d", got, len(settleRequests(driver.Actions())))
	}
	watermarks := 0
	for _, entry := range clock.Trace() {
		if len(entry) >= 4 && entry[:4] == "now@" {
			watermarks++
		}
	}
	if watermarks != 3 {
		t.Fatalf("watermark calls = %d trace %#v, want one per three successful taps", watermarks, clock.Trace())
	}
}

func TestTapBatch3WaitUntilVisibleMatrixAndOutcome(t *testing.T) {
	t.Parallel()
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	empty := tapBatch3RawTree("empty")
	for _, test := range []struct {
		name        string
		optional    bool
		waitFlag    string
		probes      []device.TreeNode
		wantOutcome Outcome
		wantError   bool
		wantProbes  int
		wantOneSec  int
	}{
		{name: "omitted", waitFlag: "", wantOutcome: Completed},
		{name: "false", waitFlag: "\nwaitUntilVisible: false", wantOutcome: Completed},
		{name: "immediate", waitFlag: "\nwaitUntilVisible: true", probes: []device.TreeNode{target}, wantOutcome: Completed, wantProbes: 1},
		{name: "delayed", waitFlag: "\nwaitUntilVisible: true", probes: []device.TreeNode{empty, empty, target}, wantOutcome: Completed, wantProbes: 3, wantOneSec: 2},
		{name: "required exhaustion", waitFlag: "\nwaitUntilVisible: true", probes: tapBatch3RepeatedTrees(empty, 10), wantOutcome: Failed, wantError: true, wantProbes: 10, wantOneSec: 9},
		{name: "optional exhaustion", optional: true, waitFlag: "\nwaitUntilVisible: true", probes: tapBatch3RepeatedTrees(empty, 10), wantOutcome: Warned, wantProbes: 10, wantOneSec: 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := "text: Continue\nwaitToSettleTimeoutMs: 0" + test.waitFlag
			if test.optional {
				body += "\noptional: true"
			}
			flowModel := parsedTapFlow(t, body, nil)
			clock := newTapTraceClock(time.Unix(800, 0))
			driver := newTapTraceDriver(clock)
			descriptors := []enginetest.Result[device.TreeNode]{{Value: target}, {Value: target}}
			for _, probe := range test.probes {
				descriptors = append(descriptors, enginetest.Result[device.TreeNode]{Value: probe})
			}
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: descriptors})
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch3-wait-" + test.name, Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
			})
			if test.wantError != (err != nil) {
				t.Fatalf("Execute() error = %T %v, want surfaced=%t", err, err, test.wantError)
			}
			result := onlyTapCommandResult(t, results)
			if result.Outcome() != test.wantOutcome || result.Metadata().NumberOfRuns() != 1 {
				t.Fatalf("result outcome/runs = %s/%d, want %s/1", result.Outcome(), result.Metadata().NumberOfRuns(), test.wantOutcome)
			}
			if test.wantOutcome == Failed || test.wantOutcome == Warned {
				var assertion *AssertionError
				if !errors.As(result.ProductError(), &assertion) {
					t.Fatalf("wait exhaustion error = %T %v, want AssertionError", result.ProductError(), result.ProductError())
				}
			}
			if got := countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor) - 2; got != test.wantProbes {
				t.Fatalf("wait probe count = %d, want %d actions %#v", got, test.wantProbes, driver.Actions())
			}
			oneSecond := 0
			for _, duration := range tapWaitDurations(clock.Trace()) {
				if duration == time.Second {
					oneSecond++
				}
			}
			if oneSecond != test.wantOneSec {
				t.Fatalf("one-second waits = %d trace %#v, want %d", oneSecond, clock.Trace(), test.wantOneSec)
			}
			if len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 1 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 0 {
				t.Fatalf("wait work leaked taps/settle/screens: %#v", driver.Actions())
			}
		})
	}
}

func TestTapBatch3RetryAndWaitComposeWithoutRecursionOrEvidence(t *testing.T) {
	t.Parallel()
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	empty := tapBatch3RawTree("empty")
	expected, same, _, _ := tapBatch3PNGs(t)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: target}, {Value: target}, {Value: target},
			{Value: target}, {Value: target},
			{Value: empty}, {Value: target},
		},
		TakeScreenshot:     []enginetest.Result[[]byte]{{Value: expected}, {Value: same}},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: target}}, {Value: nil}},
	})
	sink := &tapNoEvidenceSink{}
	flowModel := parsedTapFlow(t, "text: Continue\nretryTapIfNoChange: true\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-retry-wait", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		ImageChecker: &tapBatch3Checker{}, ArtifactSink: sink,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(tapRequests(driver.Actions())) != 2 || len(settleRequests(driver.Actions())) != 2 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 2 || sink.Calls() != 0 {
		t.Fatalf("combined work = taps %d settles %d screenshots %d sink %d actions %#v", len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot), sink.Calls(), driver.Actions())
	}
	if metadata := onlyTapCommandResult(t, results).Metadata(); metadata.NumberOfRuns() != 2 {
		t.Fatalf("combined batch metadata = %d, want 2", metadata.NumberOfRuns())
	}
}

func TestTapBatch3RawTypedFlagsMetadataOwnershipAndParallelReuse(t *testing.T) {
	command := parsedTapCommand(t, "text: Continue\nretryTapIfNoChange: true\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 0")
	for _, mutate := range []func(*model.Command){
		func(value *model.Command) { value.Arguments.(map[string]any)["retryTapIfNoChange"] = false },
		func(value *model.Command) { flag := false; value.Selector.WaitUntilVisible = &flag },
	} {
		forged := cloneCommand(command)
		mutate(&forged)
		if _, err := compileTapOn(forged); !isConfigurationError(err) {
			t.Fatalf("raw/typed forged flags error = %T %v, want ConfigurationError", err, err)
		}
	}
	compiledAny, err := compileTapOn(command)
	if err != nil {
		t.Fatalf("compileTapOn() error = %v", err)
	}
	compiled := compiledAny.(tapOnCompiled)
	*command.Selector.RetryTapIfNoChange = false
	*command.Selector.WaitUntilVisible = false
	if !compiled.retryTapIfNoChange || !compiled.waitUntilVisible {
		t.Fatalf("compiled flags aliased authored mutation: %#v", compiled)
	}

	flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	source := cloneFlow(flowModel)
	program := singleCompileProgram(flowModel)
	prepared, _ := program.Flow(flowModel.Path)
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	const runs = 8
	var wait sync.WaitGroup
	errorsCh := make(chan error, runs)
	start := make(chan struct{})
	for index := 0; index < runs; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			factory, factoryErr := js.NewFactory(js.Config{Random: deterministicRandom{}})
			if factoryErr != nil {
				errorsCh <- factoryErr
				return
			}
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
				TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}, {Value: nil}},
			})
			results, executeErr := Execute(context.Background(), program, Dependencies{
				ExecutionID: fmt.Sprintf("tap-batch3-concurrent-%d", index), Driver: driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
			})
			if executeErr != nil || len(results) != 1 || len(results[0].Commands()) != 1 || results[0].Commands()[0].Metadata().NumberOfRuns() != 2 {
				errorsCh <- fmt.Errorf("run %d error=%v results=%#v", index, executeErr, results)
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
	if !reflect.DeepEqual(flowModel, source) || !reflect.DeepEqual(prepared, preparedAfter) {
		t.Fatal("parallel retry mutated authored or prepared flow")
	}
}

func TestTapBatch3WholeProgramStaticControlsHaveZeroEffects(t *testing.T) {
	t.Parallel()
	first := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: "/workspace/tap-batch3-earlier.yaml",
		Config: model.Config{AppID: "com.example.first"}, Commands: []model.Command{{Kind: model.CommandLaunchApp, Form: model.CommandFormScalar}},
	}
	for _, test := range []struct {
		name   string
		second func() model.Flow
	}{
		{name: "point wait false", second: func() model.Flow { return parsedTapFlow(t, "point: '20,30'\nwaitUntilVisible: false", nil) }},
		{name: "point wait true", second: func() model.Flow { return parsedTapFlow(t, "point: '20,30'\nwaitUntilVisible: true", nil) }},
		{name: "raw retry mismatch", second: func() model.Flow {
			value := parsedTapFlow(t, "text: Continue\nretryTapIfNoChange: true", nil)
			value.Commands[0].Arguments.(map[string]any)["retryTapIfNoChange"] = false
			return value
		}},
		{name: "typed wait mismatch", second: func() model.Flow {
			value := parsedTapFlow(t, "text: Continue\nwaitUntilVisible: true", nil)
			flag := false
			value.Commands[0].Selector.WaitUntilVisible = &flag
			return value
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			second := test.second()
			second.Path = "/workspace/tap-batch3-later-" + test.name + ".yaml"
			driver := enginetest.NewFakeDriver()
			factory := &countingRuntimeFactory{}
			listenerCalls := 0
			results, err := Execute(context.Background(), multiRootTapProgram(first, second), Dependencies{
				ExecutionID: "tap-batch3-static-" + test.name, Driver: driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			})
			if !isConfigurationError(err) || len(results) != 0 || factory.calls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("static control = err %T %v results %d runtime %d listeners %d actions %#v", err, err, len(results), factory.calls, listenerCalls, driver.Actions())
			}
		})
	}
}

func TestTapBatch3FlagPresenceAndEvaluatedMetadataMatrix(t *testing.T) {
	t.Parallel()
	type authoredFlag struct {
		name    string
		present bool
		value   bool
	}
	flags := []authoredFlag{{name: "omitted"}, {name: "false", present: true}, {name: "true", present: true, value: true}}
	for _, retry := range flags {
		for _, waitFlag := range flags {
			t.Run("retry-"+retry.name+"-wait-"+waitFlag.name, func(t *testing.T) {
				body := "text: Continue"
				if retry.present {
					body += fmt.Sprintf("\nretryTapIfNoChange: %t", retry.value)
				}
				if waitFlag.present {
					body += fmt.Sprintf("\nwaitUntilVisible: %t", waitFlag.value)
				}
				command := parsedTapCommand(t, body)
				source := cloneCommand(command)
				compiledAny, err := compileTapOn(command)
				if err != nil {
					t.Fatalf("compileTapOn() error = %v", err)
				}
				compiled := compiledAny.(tapOnCompiled)
				if compiled.retryTapIfNoChange != retry.value || compiled.waitUntilVisible != waitFlag.value {
					t.Fatalf("compiled flags = retry %t wait %t", compiled.retryTapIfNoChange, compiled.waitUntilVisible)
				}
				evaluated := evaluatedTapForTest(t, command, model.Config{AppID: "com.example.flags"}, newAdvancingClock())
				plan := evaluated.value.(tapOnEvaluated)
				if plan.retryTapIfNoChange != retry.value || plan.waitUntilVisible != waitFlag.value || plan.targetSelector == nil {
					t.Fatalf("evaluated plan = %#v", plan)
				}
				arguments := evaluated.command.Arguments.(map[string]any)
				retryRaw, retryRawPresent := arguments["retryTapIfNoChange"]
				waitRaw, waitRawPresent := arguments["waitUntilVisible"]
				if retryRawPresent != retry.present || waitRawPresent != waitFlag.present ||
					(retry.present && retryRaw != retry.value) || (waitFlag.present && waitRaw != waitFlag.value) ||
					(evaluated.command.Selector.RetryTapIfNoChange != nil) != retry.present ||
					(evaluated.command.Selector.WaitUntilVisible != nil) != waitFlag.present || !selectorCommandSnapshotMatches(evaluated.command) {
					t.Fatalf("evaluated raw/typed flags = %#v / %#v", arguments, evaluated.command.Selector)
				}
				if !reflect.DeepEqual(command, source) {
					t.Fatal("flag evaluation mutated authored command")
				}
			})
		}
	}
}

func TestTapBatch3FirstSettleFallbackAndInitialHierarchyOwnership(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("expected")
	for _, test := range []struct {
		name   string
		settle enginetest.Result[*device.ViewHierarchy]
	}{
		{name: "nil inconclusive", settle: enginetest.Result[*device.ViewHierarchy]{Value: nil}},
		{name: "ordinary error", settle: enginetest.Result[*device.ViewHierarchy]{Err: errors.New("settle inconclusive")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
				TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{test.settle, {Value: nil}},
			})
			flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
			results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
				ExecutionID: "tap-batch3-settle-fallback-" + test.name, Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
			})
			if err != nil || len(tapRequests(driver.Actions())) != 2 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 2 || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 2 {
				t.Fatalf("fallback = err %v taps %d screens %d result %#v actions %#v", err, len(tapRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot), results, driver.Actions())
			}
			settles := settleRequests(driver.Actions())
			if len(settles) != 2 || !reflect.DeepEqual(settles[0].InitialHierarchy, &device.ViewHierarchy{Root: tree}) || settles[1].InitialHierarchy != nil {
				t.Fatalf("fallback settle requests = %#v", settles)
			}
		})
	}

	driver := &tapBatch3MutatingInitialSettleDriver{FakeDriver: enginetest.NewFakeDriver(), returned: &device.ViewHierarchy{Root: tree}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}},
	})
	flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-settle-mutation", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
	})
	if err != nil || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 2 || len(driver.InitialStates()) != 2 || driver.InitialStates()[0] != "expected" || driver.InitialStates()[1] != "" {
		t.Fatalf("mutating settle = err %v states %#v results %#v", err, driver.InitialStates(), results)
	}
}

func TestTapBatch3InvalidZeroCheckerResultFailsBeforeBatchTwo(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}},
	})
	flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-zero-result", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapNoChecker{},
	})
	result := onlyTapCommandResult(t, results)
	if !isConfigurationError(err) || result.ProductError() != err || result.Outcome() != Failed || result.Metadata().NumberOfRuns() != 1 || len(tapRequests(driver.Actions())) != 1 {
		t.Fatalf("zero result = err %T %v outcome %s product same %t runs %d taps %d", err, err, result.Outcome(), result.ProductError() == err, result.Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())))
	}
}

func TestTapBatch3SharedSemanticTraceLocksOrder(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	clock := newTapTraceClock(time.Unix(950, 0))
	driver := newTapBatch3SemanticTraceDriver(clock)
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}, {Value: nil}},
	})
	flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-semantic-trace", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3TraceChecker{clock: clock},
	})
	if err != nil || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 2 {
		t.Fatalf("Execute() error/results = %v %#v", err, results)
	}
	want := []string{
		"info@0ms", "now@0ms", "now@0ms", "hierarchy@0ms", "screenshot@0ms", "tap@0ms", "now@10ms", "settle@10ms",
		"screenshot@10ms", "check@10ms", "tap@10ms", "now@20ms", "settle@20ms",
		"now@20ms", "now@20ms",
	}
	if got := clock.Trace(); !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic trace = %#v, want %#v", got, want)
	}
}

func TestTapBatch3CancellationWinsContextIgnoringCheckerAndDescriptorErrors(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")

	t.Run("checker", func(t *testing.T) {
		for primaryIndex, primary := range []error{
			errors.New("check failed"),
			errors.Join(errors.New("check failed"), NewDeviceConnectionError("check connection", nil)),
		} {
			for _, cancelCall := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				checker := &tapBatch3CancellingChecker{err: primary}
				if cancelCall {
					checker.cancel = cancel
				}
				driver := enginetest.NewFakeDriver()
				driver.Enqueue(enginetest.DriverScript{
					DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
					ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}}, TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}},
					WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}},
				})
				flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
				results, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
					ExecutionID: fmt.Sprintf("tap-batch3-checker-%d-cancel-%t", primaryIndex, cancelCall), Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: checker,
				})
				cancel()
				result := onlyTapCommandResult(t, results)
				matched := errors.Is(err, primary)
				wantOutcome := Failed
				if primaryIndex == 1 {
					matched = err == primary
				}
				if cancelCall {
					matched = err == context.Canceled
					wantOutcome = Cancelled
				}
				if !matched || result.ProductError() != err || result.Outcome() != wantOutcome || result.Metadata().NumberOfRuns() != 1 || len(tapRequests(driver.Actions())) != 1 {
					t.Fatalf("primary=%d cancel=%t Execute() = err %T %v outcome %s productSame %t runs %d taps %d", primaryIndex, cancelCall, err, err, result.Outcome(), result.ProductError() == err, result.Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())))
				}
			}
		}
	})

	t.Run("expected descriptor", func(t *testing.T) {
		for _, cancelCall := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			primary := errors.New("descriptor failed")
			driver := &tapBatch3DescriptorDriver{FakeDriver: enginetest.NewFakeDriver(), failCall: 1, err: primary}
			if cancelCall {
				driver.cancel = cancel
			}
			driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}})
			flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true", nil)
			results, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
				ExecutionID: fmt.Sprintf("tap-batch3-descriptor-cancel-%t", cancelCall), Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
			})
			cancel()
			matches := errors.Is(err, primary)
			if cancelCall {
				matches = err == context.Canceled
			}
			if !matches || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 0 || len(tapRequests(driver.Actions())) != 0 {
				t.Fatalf("cancel=%t Execute() = err %T %v runs %d taps %d, want cancellation or original live cause", cancelCall, err, err, onlyTapCommandResult(t, results).Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())))
			}
		}
	})

	t.Run("device info", func(t *testing.T) {
		for _, cancelCall := range []bool{false, true} {
			ctx, cancel := context.WithCancel(context.Background())
			primary := errors.New("device info failed")
			driver := &tapBatch3DeviceInfoDriver{FakeDriver: enginetest.NewFakeDriver(), err: primary}
			if cancelCall {
				driver.cancel = cancel
			}
			lookup := NewElementLookup(driver, newAdvancingClock())
			_, err := lookup.cachedDeviceInfo(ctx)
			cancel()
			matches := errors.Is(err, primary)
			if cancelCall {
				matches = err == context.Canceled
			}
			if !matches || len(tapRequests(driver.Actions())) != 0 {
				t.Fatalf("cancel=%t cachedDeviceInfo() = err %T %v taps %d, want cancellation or original live cause", cancelCall, err, err, len(tapRequests(driver.Actions())))
			}
		}
	})
}

func TestTapBatch3ScreenshotCancellationAndLiveErrorIdentity(t *testing.T) {
	t.Parallel()
	expected, _, _, _ := tapBatch3PNGs(t)
	tree := tapBatch3RawTree("same")
	for _, primary := range []error{
		errors.New("screenshot failed"),
		errors.Join(errors.New("screenshot failed"), NewDeviceConnectionError("screenshot disconnected", nil)),
	} {
		for _, failCall := range []int{1, 2} {
			for _, cancelCall := range []bool{false, true} {
				ctx, cancel := context.WithCancel(context.Background())
				driver := &tapBatch3ScreenshotErrorDriver{FakeDriver: enginetest.NewFakeDriver(), failCall: failCall, err: primary}
				if cancelCall {
					driver.cancel = cancel
				}
				driver.Enqueue(enginetest.DriverScript{
					DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
					WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}},
				})
				flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
				results, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
					ExecutionID: fmt.Sprintf("tap-batch3-screenshot-%d-%t", failCall, cancelCall), Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
				})
				cancel()
				result := onlyTapCommandResult(t, results)
				wantRuns, wantTaps, wantOutcome := 0, 0, Failed
				if failCall == 2 {
					wantRuns, wantTaps = 1, 1
				}
				matched := errors.Is(err, primary)
				if cancelCall {
					matched, wantOutcome = err == context.Canceled, Cancelled
				}
				if !matched || result.ProductError() != err || result.Outcome() != wantOutcome || result.Metadata().NumberOfRuns() != wantRuns || len(tapRequests(driver.Actions())) != wantTaps || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != failCall {
					t.Fatalf("fail=%d cancel=%t = err %T %v outcome %s productSame %t runs %d taps %d screens %d", failCall, cancelCall, err, err, result.Outcome(), result.ProductError() == err, result.Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot))
				}
				_ = expected
			}
		}
	}
}

func TestTapBatch3WaitCancellationAfterContextIgnoringClockStopsLaterProbe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	clock := &tapBatch3WaitCancelClock{tapTraceClock: newTapTraceClock(time.Unix(900, 0)), cancel: cancel}
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	empty := tapBatch3RawTree("empty")
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: target}, {Value: target}, {Value: empty}, {Value: target}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-wait-cancel", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if err != context.Canceled || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 1 || len(tapRequests(driver.Actions())) != 1 || countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor) != 3 {
		t.Fatalf("Execute() = err %T %v runs %d taps %d descriptors %d actions %#v", err, err, onlyTapCommandResult(t, results).Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor), driver.Actions())
	}
}

func TestTapBatch3WaitDescriptorCancellationWinsAndStopsLaterProbe(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	primary := errors.New("wait descriptor failed")
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	driver := &tapBatch3DescriptorDriver{FakeDriver: enginetest.NewFakeDriver(), failCall: 3, cancel: cancel, err: primary}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: target}, {Value: target}, {Value: tapBatch3RawTree("unused")}, {Value: target}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(ctx, singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-wait-descriptor-cancel", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if err != context.Canceled || onlyTapCommandResult(t, results).Outcome() != Cancelled || len(tapRequests(driver.Actions())) != 1 || countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor) != 3 {
		t.Fatalf("Execute() = err %T %v result %#v actions %#v", err, err, results, driver.Actions())
	}
}

func TestTapBatch3OwnedHierarchyAndScreenshotExpectationsResistProviderAliasMutation(t *testing.T) {
	t.Parallel()
	expected, _, _, above := tapBatch3PNGs(t)

	t.Run("hierarchy", func(t *testing.T) {
		driver := &tapBatch3HierarchyAliasDriver{
			FakeDriver: enginetest.NewFakeDriver(), hierarchy: tapBatch3RawTree("before"), screenshot: expected,
		}
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}})
		checker := &tapBatch3Checker{}
		flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
		results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch3-owned-hierarchy", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: checker,
		})
		if err != nil || len(tapRequests(driver.Actions())) != 1 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 1 || checker.Calls() != 0 || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 1 {
			t.Fatalf("owned hierarchy = err %v taps %d screens %d check %d result %#v actions %#v", err, len(tapRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot), checker.Calls(), results, driver.Actions())
		}
	})

	t.Run("screenshot", func(t *testing.T) {
		driver := &tapBatch3ScreenshotAliasDriver{
			FakeDriver: enginetest.NewFakeDriver(), screenshot: []byte{1},
		}
		tree := tapBatch3RawTree("same")
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}},
			WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: tree}}},
		})
		checker := &tapBatch3OwnershipChecker{same: expected, changed: above}
		flowModel := parsedTapFlow(t, "point: '20,30'\nretryTapIfNoChange: true\nwaitToSettleTimeoutMs: 0", nil)
		results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
			ExecutionID: "tap-batch3-owned-screenshot", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: checker,
		})
		if err != nil || len(tapRequests(driver.Actions())) != 1 || checker.Calls() != 1 || onlyTapCommandResult(t, results).Metadata().NumberOfRuns() != 1 {
			t.Fatalf("owned screenshot = err %v taps %d check %d result %#v actions %#v", err, len(tapRequests(driver.Actions())), checker.Calls(), results, driver.Actions())
		}
	})
}

func TestTapBatch3CheckerCannotMutateCallerInputsAndWaitSelectorStripsTapModifiers(t *testing.T) {
	t.Parallel()
	expected, same, _, _ := tapBatch3PNGs(t)
	checker := &tapBatch3MutatingChecker{}
	state := &executionState{dependencies: Dependencies{ImageChecker: checker}}
	expectedSource := append([]byte(nil), expected...)
	actualSource := append([]byte(nil), same...)
	crop := image.Rect(0, 0, 200, 1)
	if _, err := state.checkImages(context.Background(), ImageCheckRequest{Expected: expectedSource, Actual: actualSource, Crop: &crop}); err != nil {
		t.Fatalf("checkImages() error = %v", err)
	}
	if !bytes.Equal(expectedSource, expected) || !bytes.Equal(actualSource, same) || crop != image.Rect(0, 0, 200, 1) {
		t.Fatal("mutating checker aliased caller-owned image bytes/crop")
	}

	command := parsedTapCommand(t, "text: Continue\npoint: '50%,50%'\nrepeat: 2\ndelay: 400\nretryTapIfNoChange: true\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 100\noptional: true\nlabel: tap")
	selector, err := tapTargetOnlySelector(command)
	if err != nil {
		t.Fatalf("tapTargetOnlySelector() error = %v", err)
	}
	if selector.TextRegex == nil || *selector.TextRegex != "Continue" || selector.Point != nil || selector.Repeat != nil || selector.Delay != nil || selector.RetryTapIfNoChange != nil || selector.WaitUntilVisible != nil || selector.WaitToSettleTimeoutMS != nil || selector.Optional != nil || selector.Label != nil {
		t.Fatalf("wait selector = %#v, want only target predicates", selector)
	}
	anchor := "Anchor"
	command.Selector.Below = &model.ElementSelector{TextRegex: &anchor}
	command.Arguments.(map[string]any)["below"] = map[string]any{"text": "Anchor"}
	relational, err := tapTargetOnlySelector(command)
	if err != nil || relational.Below == nil || relational.Below.TextRegex == nil || *relational.Below.TextRegex != "Anchor" {
		t.Fatalf("relational target = %#v error %v", relational, err)
	}
	*relational.Below.TextRegex = "mutated"
	if command.Selector.Below.TextRegex == nil || *command.Selector.Below.TextRegex != "Anchor" {
		t.Fatal("target-only selector aliased evaluated relational predicate")
	}
}

func TestTapBatch3RetryWaitWatermarkReducesNextAssertionBudget(t *testing.T) {
	t.Parallel()
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	empty := tapBatch3RawTree("empty")
	expected, same, _, _ := tapBatch3PNGs(t)
	contents := "appId: com.example.tap-budget\n---\n" +
		"- tapOn:\n    text: Continue\n    retryTapIfNoChange: true\n    waitUntilVisible: true\n    waitToSettleTimeoutMs: 0\n" +
		"- assertVisible:\n    text: Missing\n"
	flowModel, err := flow.ParseBytes("/workspace/tap-batch3-budget.yaml", []byte(contents))
	if err != nil {
		t.Fatalf("flow.ParseBytes() error = %v", err)
	}
	clock := newTapTraceClock(time.Unix(1000, 0))
	driver := newTapTraceDriver(clock)
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: target}, {Value: target}, {Value: target}, {Value: target}, {Value: target},
			{Value: empty}, {Value: empty}, {Value: target},
		},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: target}}, {Value: nil}},
	})
	results, executeErr := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-next-budget", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: &tapBatch3Checker{},
	})
	if executeErr == nil || len(results) != 1 || len(results[0].Commands()) != 2 || results[0].Commands()[0].Metadata().NumberOfRuns() != 2 {
		t.Fatalf("Execute() = err %v results %#v", executeErr, results)
	}
	waits := tapWaitDurations(clock.Trace())
	if len(waits) < 5 || !reflect.DeepEqual(waits[:4], []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, time.Second, time.Second}) || sumTapDurations(waits[4:]) != LookupTimeout-2*time.Second {
		t.Fatalf("retry/wait/next waits = %#v trace %#v, want 100ms,100ms,1s,1s then adjusted 15s", waits, clock.Trace())
	}
	trace := clock.Trace()
	for index, entry := range trace {
		if len(entry) >= 4 && entry[:4] == "tap@" {
			if index+1 >= len(trace) || len(trace[index+1]) < 4 || trace[index+1][:4] != "now@" {
				t.Fatalf("Tap was not immediately followed by watermark: %#v", trace)
			}
		}
	}
}

func TestTapBatch3RetryWaitExhaustionIsBoundedAndEmptyDiagnosticStaysSilent(t *testing.T) {
	t.Parallel()
	target := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	empty := tapBatch3RawTree("empty")
	expected, same, _, _ := tapBatch3PNGs(t)
	descriptors := []enginetest.Result[device.TreeNode]{
		{Value: target}, {Value: target}, {Value: target}, {Value: target}, {Value: target},
	}
	for range WaitUntilVisibleAttempts {
		descriptors = append(descriptors, enginetest.Result[device.TreeNode]{Value: empty})
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}}, ContentDescriptor: descriptors,
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: expected}, {Value: same}}, WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: &device.ViewHierarchy{Root: target}}, {Value: nil}},
	})
	sink := &tapNoEvidenceSink{}
	checker := &tapBatch3Checker{}
	flowModel := parsedTapFlow(t, "text: Continue\nretryTapIfNoChange: true\nwaitUntilVisible: true\nwaitToSettleTimeoutMs: 0", nil)
	results, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-batch3-retry-wait-exhausted", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, ImageChecker: checker, ArtifactSink: sink,
	})
	result := onlyTapCommandResult(t, results)
	var assertion *AssertionError
	if !errors.As(err, &assertion) || result.Outcome() != Failed || result.Metadata().NumberOfRuns() != 2 || len(tapRequests(driver.Actions())) != 2 || len(settleRequests(driver.Actions())) != 2 || countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 3 || checker.Calls() != 1 || sink.Calls() != 0 || len(result.Artifacts()) != 0 || countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor) != 5+WaitUntilVisibleAttempts {
		t.Fatalf("exhausted = err %T %v outcome %s runs %d taps %d settles %d screens %d check %d sink %d descriptors %d", err, err, result.Outcome(), result.Metadata().NumberOfRuns(), len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())), countTapDriverMethod(driver.Actions(), enginetest.MethodTakeScreenshot), checker.Calls(), sink.Calls(), countTapDriverMethod(driver.Actions(), enginetest.MethodContentDescriptor))
	}
}

type tapBatch3Checker struct {
	mu       sync.Mutex
	err      error
	requests []ImageCheckRequest
}

type tapBatch3CancellingChecker struct {
	cancel context.CancelFunc
	err    error
}

func (checker *tapBatch3CancellingChecker) Check(context.Context, ImageCheckRequest) (imagecheck.Result, error) {
	if checker.cancel != nil {
		checker.cancel()
	}
	return imagecheck.Result{}, checker.err
}

type tapBatch3DescriptorDriver struct {
	*enginetest.FakeDriver
	mu       sync.Mutex
	calls    int
	failCall int
	cancel   context.CancelFunc
	err      error
}

func (driver *tapBatch3DescriptorDriver) ContentDescriptor(_ context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	driver.mu.Lock()
	driver.calls++
	call := driver.calls
	driver.mu.Unlock()
	value, err := driver.FakeDriver.ContentDescriptor(context.Background(), request)
	if call == driver.failCall {
		if driver.cancel != nil {
			driver.cancel()
		}
		return device.TreeNode{}, driver.err
	}
	return value, err
}

type tapBatch3DeviceInfoDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
	err    error
}

func (driver *tapBatch3DeviceInfoDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	_, _ = driver.FakeDriver.DeviceInfo(context.Background())
	if driver.cancel != nil {
		driver.cancel()
	}
	return device.DeviceInfo{}, driver.err
}

type tapBatch3WaitCancelClock struct {
	*tapTraceClock
	cancel context.CancelFunc
}

func (clock *tapBatch3WaitCancelClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := clock.tapTraceClock.Wait(ctx, delay); err != nil {
		return err
	}
	if delay == WaitUntilVisiblePollInterval {
		clock.cancel()
	}
	return nil
}

type tapBatch3HierarchyAliasDriver struct {
	*enginetest.FakeDriver
	hierarchy  device.TreeNode
	screenshot []byte
}

func (driver *tapBatch3HierarchyAliasDriver) ContentDescriptor(_ context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	_, _ = driver.FakeDriver.ContentDescriptor(context.Background(), request)
	return driver.hierarchy, nil
}

func (driver *tapBatch3HierarchyAliasDriver) TakeScreenshot(_ context.Context, request device.ScreenshotRequest) ([]byte, error) {
	_, _ = driver.FakeDriver.TakeScreenshot(context.Background(), request)
	return driver.screenshot, nil
}

func (driver *tapBatch3HierarchyAliasDriver) Tap(_ context.Context, request device.TapRequest) error {
	if err := driver.FakeDriver.Tap(context.Background(), request); err != nil {
		return err
	}
	driver.hierarchy.Attributes["state"] = "after"
	return nil
}

func (driver *tapBatch3HierarchyAliasDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	_, _ = driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	return &device.ViewHierarchy{Root: driver.hierarchy}, nil
}

type tapBatch3ScreenshotAliasDriver struct {
	*enginetest.FakeDriver
	screenshot []byte
}

func (driver *tapBatch3ScreenshotAliasDriver) TakeScreenshot(_ context.Context, request device.ScreenshotRequest) ([]byte, error) {
	_, _ = driver.FakeDriver.TakeScreenshot(context.Background(), request)
	return driver.screenshot, nil
}

func (driver *tapBatch3ScreenshotAliasDriver) Tap(_ context.Context, request device.TapRequest) error {
	if err := driver.FakeDriver.Tap(context.Background(), request); err != nil {
		return err
	}
	driver.screenshot[0] = 2
	return nil
}

type tapBatch3OwnershipChecker struct {
	mu      sync.Mutex
	calls   int
	same    []byte
	changed []byte
}

func (checker *tapBatch3OwnershipChecker) Check(_ context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	checker.mu.Lock()
	checker.calls++
	checker.mu.Unlock()
	actual := checker.same
	if !bytes.Equal(request.Expected, request.Actual) {
		actual = checker.changed
	}
	return imagecheck.Check(checker.same, actual, nil)
}

func (checker *tapBatch3OwnershipChecker) Calls() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return checker.calls
}

type tapBatch3MutatingChecker struct{}

func (*tapBatch3MutatingChecker) Check(_ context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	result, err := imagecheck.Check(request.Expected, request.Actual, request.Crop)
	if len(request.Expected) != 0 {
		request.Expected[0] ^= 0xff
	}
	if len(request.Actual) != 0 {
		request.Actual[0] ^= 0xff
	}
	if request.Crop != nil {
		request.Crop.Min.X = 99
	}
	return result, err
}

type tapBatch3MutatingInitialSettleDriver struct {
	*enginetest.FakeDriver
	mu       sync.Mutex
	states   []string
	returned *device.ViewHierarchy
}

func (driver *tapBatch3MutatingInitialSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	_, _ = driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	state := ""
	if request.InitialHierarchy != nil {
		state = request.InitialHierarchy.Root.Attributes["state"]
		request.InitialHierarchy.Root.Attributes["state"] = "provider-mutated"
	}
	driver.mu.Lock()
	driver.states = append(driver.states, state)
	driver.mu.Unlock()
	if state == "" {
		return nil, nil
	}
	return cloneSettleHierarchy(driver.returned), nil
}

func (driver *tapBatch3MutatingInitialSettleDriver) InitialStates() []string {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]string(nil), driver.states...)
}

type tapBatch3SemanticTraceDriver struct {
	*tapTraceDriver
}

func newTapBatch3SemanticTraceDriver(clock *tapTraceClock) *tapBatch3SemanticTraceDriver {
	return &tapBatch3SemanticTraceDriver{tapTraceDriver: newTapTraceDriver(clock)}
}

func (driver *tapBatch3SemanticTraceDriver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	driver.clock.record("info")
	return driver.FakeDriver.DeviceInfo(ctx)
}

func (driver *tapBatch3SemanticTraceDriver) ContentDescriptor(ctx context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	driver.clock.record("hierarchy")
	return driver.FakeDriver.ContentDescriptor(ctx, request)
}

func (driver *tapBatch3SemanticTraceDriver) TakeScreenshot(ctx context.Context, request device.ScreenshotRequest) ([]byte, error) {
	driver.clock.record("screenshot")
	return driver.FakeDriver.TakeScreenshot(ctx, request)
}

type tapBatch3TraceChecker struct {
	clock *tapTraceClock
}

func (checker *tapBatch3TraceChecker) Check(_ context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	checker.clock.record("check")
	return imagecheck.Check(request.Expected, request.Actual, request.Crop)
}

type tapBatch3ScreenshotErrorDriver struct {
	*enginetest.FakeDriver
	mu       sync.Mutex
	calls    int
	failCall int
	cancel   context.CancelFunc
	err      error
}

func (driver *tapBatch3ScreenshotErrorDriver) TakeScreenshot(_ context.Context, request device.ScreenshotRequest) ([]byte, error) {
	_, _ = driver.FakeDriver.TakeScreenshot(context.Background(), request)
	driver.mu.Lock()
	driver.calls++
	call := driver.calls
	driver.mu.Unlock()
	if call == driver.failCall {
		if driver.cancel != nil {
			driver.cancel()
		}
		return nil, driver.err
	}
	return []byte("unused"), nil
}

func (checker *tapBatch3Checker) Check(_ context.Context, request ImageCheckRequest) (imagecheck.Result, error) {
	checker.mu.Lock()
	checker.requests = append(checker.requests, cloneImageCheckRequest(request))
	checker.mu.Unlock()
	if checker.err != nil {
		return imagecheck.Result{}, checker.err
	}
	return imagecheck.Check(request.Expected, request.Actual, request.Crop)
}

func (checker *tapBatch3Checker) Calls() int {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	return len(checker.requests)
}

func tapBatch3PNGs(t testing.TB) (expected, same, exactThreshold, aboveThreshold []byte) {
	t.Helper()
	expected = tapBatch3PNG(t, nil)
	same = append([]byte(nil), expected...)
	exactThreshold = tapBatch3PNG(t, map[int]color.NRGBA{0: {R: 255, A: 255}})
	aboveThreshold = tapBatch3PNG(t, map[int]color.NRGBA{0: {R: 255, A: 255}, 1: {G: 255, A: 255}})
	return expected, same, exactThreshold, aboveThreshold
}

func tapBatch3PNG(t testing.TB, pixels map[int]color.NRGBA) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, 200, 1))
	for x := 0; x < 200; x++ {
		value.SetNRGBA(x, 0, color.NRGBA{A: 255})
	}
	for x, pixel := range pixels {
		value.SetNRGBA(x, 0, pixel)
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return output.Bytes()
}

func tapBatch3RawTree(state string) device.TreeNode {
	return device.TreeNode{Attributes: map[string]string{"state": state, "bounds": "[0,0][400,884]"}}
}

func tapBatch3RelevantMethods(actions []enginetest.Action) []enginetest.Method {
	methods := make([]enginetest.Method, 0, len(actions))
	for _, action := range actions {
		switch action.Method {
		case enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTakeScreenshot, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle:
			methods = append(methods, action.Method)
		}
	}
	return methods
}

func tapBatch3RepeatedTrees(tree device.TreeNode, count int) []device.TreeNode {
	values := make([]device.TreeNode, count)
	for index := range values {
		values[index] = cloneSettleTreeNode(tree)
	}
	return values
}
