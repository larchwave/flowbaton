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

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch3PrivateRegistryAndCompileContract(t *testing.T) {
	t.Parallel()

	direct, err := newHandlerRegistry(
		doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec(),
		backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec(),
		scrollUntilVisibleHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	wantDirect := []string{"back", "doubleTapOn", "hideKeyboard", "longPressOn", "pressKey", "scroll", "scrollUntilVisible", "swipe"}
	if got := sortedHandlerKeywords(direct); !reflect.DeepEqual(got, wantDirect) {
		t.Fatalf("direct registry keys = %#v, want %#v", got, wantDirect)
	}
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	wantProduction := productionKeywordStrings()
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry keys = %#v, want %#v", got, wantProduction)
	}
	spec, ok := direct.lookup(model.CommandScrollUntilVisible)
	if !ok || spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle || spec.settleRequest != nil {
		t.Fatalf("scrollUntilVisible spec = %#v, want device mutation/no-settle/nil factory", spec)
	}
	defaultsValue, err := compileScrollUntilVisible(batch3Command("Ready", nil))
	if err != nil {
		t.Fatalf("compile defaults error = %v", err)
	}
	defaults := defaultsValue.(scrollUntilVisibleCompiled)
	if defaults.direction != "DOWN" || defaults.timeoutMillis != 20000 || defaults.speed != 40 ||
		defaults.visibilityPercentage != 100 || defaults.settleTimeoutMillis != nil || defaults.centerElement {
		t.Fatalf("compiled defaults = %#v", defaults)
	}

	accepted := []model.Command{
		batch3Command("Ready", nil),
		batch3Command("Ready", map[string]any{"direction": "UP", "timeout": int64(0), "speed": int64(1), "visibilityPercentage": int64(10), "waitToSettleTimeoutMs": int64(0), "centerElement": false}),
		batch3Command("Ready", map[string]any{"direction": "DOWN", "timeout": int64(120000), "speed": int64(100), "visibilityPercentage": int64(100), "waitToSettleTimeoutMs": int64(30000), "centerElement": true}),
		batch3Command("${TARGET}", map[string]any{"direction": "${DIRECTION}", "timeout": "${TIMEOUT}"}),
		batch3Command("Ready", map[string]any{"timeout": "00042"}),
	}
	for _, command := range accepted {
		if _, err := compileScrollUntilVisible(command); err != nil {
			t.Fatalf("compile accepted %#v error = %v", command.Arguments, err)
		}
	}

	optional := true
	notOptional := false
	invalid := []model.Command{
		{Kind: model.CommandScrollUntilVisible, Form: model.CommandFormScalar},
		{Kind: model.CommandScrollUntilVisible, Form: model.CommandFormObject, Arguments: map[string]any{}},
		batch3Command("Ready", map[string]any{"mystery": true}),
		batch3Command("Ready", map[string]any{"scrollDuration": "40"}),
		batch3Command("Ready", map[string]any{"direction": "diagonal"}),
		batch3Command("Ready", map[string]any{"direction": "sideways"}),
		batch3Command("Ready", map[string]any{"direction": "sideways"}),
		batch3Command("Ready", map[string]any{"direction": " DOWN"}),
		batch3Command("Ready", map[string]any{"direction": true}),
		batch3Command("Ready", map[string]any{"timeout": int64(-1)}),
		batch3Command("Ready", map[string]any{"timeout": int64(120001)}),
		batch3Command("Ready", map[string]any{"timeout": "-1"}),
		batch3Command("Ready", map[string]any{"timeout": ""}),
		batch3Command("Ready", map[string]any{"timeout": "+1"}),
		batch3Command("Ready", map[string]any{"timeout": " 1"}),
		batch3Command("Ready", map[string]any{"timeout": "1 "}),
		batch3Command("Ready", map[string]any{"timeout": "1.0"}),
		batch3Command("Ready", map[string]any{"timeout": "9223372036854775808"}),
		batch3Command("Ready", map[string]any{"timeout": 1.0}),
		batch3Command("Ready", map[string]any{"speed": int64(0)}),
		batch3Command("Ready", map[string]any{"speed": int64(101)}),
		batch3Command("Ready", map[string]any{"speed": "fast"}),
		batch3Command("Ready", map[string]any{"visibilityPercentage": int64(0)}),
		batch3Command("Ready", map[string]any{"visibilityPercentage": int64(9)}),
		batch3Command("Ready", map[string]any{"visibilityPercentage": int64(101)}),
		batch3Command("Ready", map[string]any{"waitToSettleTimeoutMs": int64(-1)}),
		batch3Command("Ready", map[string]any{"waitToSettleTimeoutMs": int64(30001)}),
		batch3Command("Ready", map[string]any{"centerElement": "notbool"}),
		batch3CommandWithSelector(map[string]any{"text": "Ready", "optional": true}, &model.ElementSelector{TextRegex: stringPointer("Ready"), Optional: &optional}, nil),
		batch3CommandWithSelector(map[string]any{"text": "Ready", "optional": false}, &model.ElementSelector{TextRegex: stringPointer("Ready"), Optional: &notOptional}, nil),
		batch3CommandWithSelector(
			map[string]any{"text": "Ready", "below": map[string]any{"text": "Header", "optional": true}},
			&model.ElementSelector{TextRegex: stringPointer("Ready"), Below: &model.ElementSelector{TextRegex: stringPointer("Header"), Optional: &optional}}, nil,
		),
		batch3CommandWithSelector(
			map[string]any{"text": "Ready", "below": map[string]any{"text": "Header", "optional": false}},
			&model.ElementSelector{TextRegex: stringPointer("Ready"), Below: &model.ElementSelector{TextRegex: stringPointer("Header"), Optional: &notOptional}}, nil,
		),
		batch3CommandWithSelector("Ready", &model.ElementSelector{TextRegex: stringPointer("Other")}, nil),
		batch3Command("[", nil),
	}
	for _, command := range invalid {
		if _, err := compileScrollUntilVisible(command); !isConfigurationError(err) {
			t.Fatalf("compile invalid %#v error = %T %v, want ConfigurationError", command.Arguments, err, err)
		}
	}

	forged := batch3Command("Ready", nil)
	forged.Condition = &model.Condition{}
	if _, err := compileScrollUntilVisible(forged); !isConfigurationError(err) {
		t.Fatalf("forged condition error = %T %v", err, err)
	}
	forged = batch3Command("Ready", nil)
	forged.Links = []model.FileLink{{Kind: model.FileLinkFlow, Path: "foreign.yaml"}}
	if _, err := compileScrollUntilVisible(forged); !isConfigurationError(err) {
		t.Fatalf("forged links error = %T %v", err, err)
	}
	// `optional` is universal metadata. A missing target warns instead of
	// aborting when the flag is true.
	for _, authoredOptional := range []*bool{&optional, &notOptional} {
		forged = batch3Command("Ready", nil)
		forged.Optional = authoredOptional
		if _, err := compileScrollUntilVisible(forged); err != nil {
			t.Fatalf("authored optional %t error = %T %v", *authoredOptional, err, err)
		}
	}
	forged = batch3Command("Ready", nil)
	forged.Children = []model.Command{batch2ABareCommand(model.CommandScroll)}
	if _, err := compileScrollUntilVisible(forged); !isConfigurationError(err) {
		t.Fatalf("forged children error = %T %v", err, err)
	}
}

func TestInteractionBatch3LateEvaluationAndInvalidDynamicValuesPrecedeEffects(t *testing.T) {
	t.Parallel()

	command := batch3Command("${TARGET}", map[string]any{"direction": "${DIRECTION}", "timeout": "${TIMEOUT}"})
	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Ready", device.Bounds{Width: 100, Height: 100})}, nil, nil)
	clock := newBatch3Clock(time.Unix(100, 0), true)
	effect, evaluated, err := executeBatch3ForTest(context.Background(), command, map[string]string{
		"TARGET": "Ready", "DIRECTION": "UP", "TIMEOUT": "120000",
	}, driver, clock)
	if err != nil || effect.effectClass != EffectDeviceMutation {
		t.Fatalf("execute dynamic = effect %#v error %v", effect, err)
	}
	arguments := evaluated.command.Arguments.(map[string]any)
	if arguments["direction"] != "UP" || arguments["timeout"] != "120000" || arguments["element"] != "Ready" {
		t.Fatalf("evaluated arguments = %#v", arguments)
	}
	if command.Selector == nil || *command.Selector.TextRegex != "${TARGET}" || command.Arguments.(map[string]any)["direction"] != "${DIRECTION}" {
		t.Fatalf("authored command mutated = %#v", command)
	}

	for _, replacements := range []map[string]string{
		{"TARGET": "[", "DIRECTION": "UP", "TIMEOUT": "1"},
		{"TARGET": "Ready", "DIRECTION": "sideways", "TIMEOUT": "1"},
		{"TARGET": "Ready", "DIRECTION": "UP", "TIMEOUT": " 1"},
		{"TARGET": "Ready", "DIRECTION": "UP", "TIMEOUT": "120001"},
	} {
		driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		_, _, err := executeBatch3ForTest(context.Background(), command, replacements, driver, newBatch3Clock(time.Unix(100, 0), true))
		if !isConfigurationError(err) || len(driver.Actions()) != 0 {
			t.Fatalf("dynamic invalid %#v = %T %v actions %#v, want pre-effect ConfigurationError", replacements, err, err, driver.Actions())
		}
	}
}

func TestInteractionBatch3ImmediateDelayedThresholdAndRequestOrder(t *testing.T) {
	t.Parallel()

	t.Run("immediate default success", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
		}, nil, nil)
		clock := newBatch3Clock(time.Unix(200, 0), true)
		effect, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, clock)
		if err != nil || effect.effectClass != EffectDeviceMutation {
			t.Fatalf("immediate execute = effect %#v error %v", effect, err)
		}
		if got := batch3Methods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor}) {
			t.Fatalf("immediate actions = %#v", driver.Actions())
		}
		if waits := clock.Waits(); len(waits) != 0 {
			t.Fatalf("immediate waits = %#v, want none", waits)
		}
	})

	for _, test := range []struct {
		name      string
		direction string
		speed     int64
		want      device.ScrollVerticalRequest
	}{
		{name: "up minimum speed", direction: "UP", speed: 1, want: device.ScrollVerticalRequest{Direction: "UP", Amount: 0.01}},
		{name: "down maximum speed", direction: "DOWN", speed: 100, want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: 1}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
				batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
				batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
			}, []error{nil}, nil)
			_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
				"direction": test.direction, "speed": test.speed,
			}), nil, driver, newBatch3Clock(time.Unix(215, 0), true))
			if err != nil {
				t.Fatalf("direction/speed execute error = %v", err)
			}
			requests := batch3ScrollRequests(driver.Actions())
			if len(requests) != 1 || !reflect.DeepEqual(requests[0], test.want) {
				t.Fatalf("direction/speed requests = %#v, want %#v", requests, test.want)
			}
		})
	}

	t.Run("delayed default success", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
			batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
		}, []error{nil}, nil)
		clock := newBatch3Clock(time.Unix(210, 0), true)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, clock)
		if err != nil {
			t.Fatalf("delayed execute error = %v", err)
		}
		wantMethods := []enginetest.Method{
			enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor,
			enginetest.MethodScrollVertical, enginetest.MethodContentDescriptor,
		}
		if got := batch3Methods(driver.Actions()); !reflect.DeepEqual(got, wantMethods) {
			t.Fatalf("delayed methods = %#v, want %#v", got, wantMethods)
		}
		requests := batch3ScrollRequests(driver.Actions())
		if len(requests) != 1 || requests[0].Direction != "DOWN" || requests[0].Amount != 0.40 || requests[0].ElementPoint != nil {
			t.Fatalf("default scroll request = %#v", requests)
		}
		if waits := clock.Waits(); !reflect.DeepEqual(waits, []time.Duration{100 * time.Millisecond}) {
			t.Fatalf("delayed waits = %#v", waits)
		}
	})

	for _, test := range []struct {
		name      string
		threshold int64
		bounds    device.Bounds
		wantOK    bool
	}{
		{name: "exact fifty", threshold: 50, bounds: device.Bounds{Y: 50, Width: 100, Height: 100}, wantOK: true},
		{name: "below fifty one", threshold: 51, bounds: device.Bounds{Y: 50, Width: 100, Height: 100}, wantOK: false},
		{name: "unrounded above sixty six", threshold: 66, bounds: device.Bounds{X: -1, Width: 3, Height: 100}, wantOK: true},
		{name: "unrounded below sixty seven", threshold: 67, bounds: device.Bounds{X: -1, Width: 3, Height: 100}, wantOK: false},
		{name: "missing bounds", threshold: 10, bounds: device.Bounds{}, wantOK: false},
		{name: "zero width", threshold: 10, bounds: device.Bounds{Width: 0, Height: 100}, wantOK: false},
		{name: "zero height", threshold: 10, bounds: device.Bounds{Width: 100, Height: 0}, wantOK: false},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := batch3Tree("Ready", test.bounds)
			if test.name == "missing bounds" {
				root = batch3TreeWithoutTargetBounds("Ready")
			}
			driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{root}, nil, nil)
			command := batch3Command("Ready", map[string]any{"timeout": int64(0), "visibilityPercentage": test.threshold})
			_, _, err := executeBatch3ForTest(context.Background(), command, nil, driver, newBatch3Clock(time.Unix(220, 0), true))
			if test.wantOK && err != nil {
				t.Fatalf("threshold success error = %v", err)
			}
			if !test.wantOK {
				var assertion *AssertionError
				if !errors.As(err, &assertion) {
					t.Fatalf("threshold failure error = %T %v, want AssertionError", err, err)
				}
			}
			if len(batch3ScrollRequests(driver.Actions())) != 0 {
				t.Fatalf("timeout-zero threshold case scrolled: %#v", driver.Actions())
			}
		})
	}

	t.Run("negative normalized bounds never satisfy threshold", func(t *testing.T) {
		viewport := device.Bounds{Width: 100, Height: 100}
		for _, bounds := range []device.Bounds{
			{Width: -1, Height: 100},
			{Width: 100, Height: -1},
		} {
			element := &hierarchy.Element{Bounds: bounds, HasBounds: true}
			if scrollUntilVisibleThresholdSatisfied(element, viewport, 0.10) {
				t.Fatalf("non-positive bounds %#v satisfied visibility threshold", bounds)
			}
		}
	})
}

func TestInteractionBatch3CenteringImmediateDelayedDirectionsAndCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bounds device.Bounds
		center bool
		want   *device.ScrollVerticalRequest
	}{
		{name: "omitted", bounds: device.Bounds{Y: 80, Width: 100, Height: 20}},
		{name: "false", bounds: device.Bounds{Y: 80, Width: 100, Height: 20}, center: false},
		{name: "zero amount", bounds: device.Bounds{Y: 40, Width: 100, Height: 20}, center: true},
		{name: "below midpoint maps up", bounds: device.Bounds{Y: 80, Width: 100, Height: 20}, center: true, want: &device.ScrollVerticalRequest{Direction: "UP", Amount: 0.4, ElementPoint: &device.Point{X: 50, Y: 90}}},
		{name: "above midpoint maps down", bounds: device.Bounds{Y: -20, Width: 100, Height: 40}, center: true, want: &device.ScrollVerticalRequest{Direction: "DOWN", Amount: 0.5, ElementPoint: &device.Point{X: 50, Y: 0}}},
		{name: "half-pixel center remains exact", bounds: device.Bounds{X: 1, Y: 75, Width: 99, Height: 31}, center: true, want: &device.ScrollVerticalRequest{Direction: "UP", Amount: 0.405, ElementPoint: &device.Point{X: 50.5, Y: 90.5}}},
		{name: "amount capped", bounds: device.Bounds{Width: 100, Height: 1000}, center: true, want: &device.ScrollVerticalRequest{Direction: "UP", Amount: 1, ElementPoint: &device.Point{X: 50, Y: 500}}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			fields := map[string]any{"visibilityPercentage": int64(10)}
			if test.name != "omitted" {
				fields["centerElement"] = test.center
			}
			driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Ready", test.bounds)}, []error{nil}, nil)
			_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", fields), nil, driver, newBatch3Clock(time.Unix(300, 0), true))
			if err != nil {
				t.Fatalf("centering execute error = %v", err)
			}
			requests := batch3ScrollRequests(driver.Actions())
			if test.want == nil {
				if len(requests) != 0 {
					t.Fatalf("centering requests = %#v, want none", requests)
				}
				return
			}
			if len(requests) != 1 || !reflect.DeepEqual(requests[0], *test.want) {
				t.Fatalf("centering requests = %#v, want %#v", requests, *test.want)
			}
			if countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 {
				t.Fatalf("successful centering re-observed hierarchy: %#v", driver.Actions())
			}
		})
	}

	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
	}, []error{nil, nil}, nil)
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
		"visibilityPercentage": int64(10), "centerElement": true,
	}), nil, driver, newBatch3Clock(time.Unix(310, 0), true))
	if err != nil {
		t.Fatalf("delayed centering error = %v", err)
	}
	requests := batch3ScrollRequests(driver.Actions())
	if len(requests) != 2 || requests[0].Direction != "DOWN" || requests[0].Amount != 0.4 || requests[0].ElementPoint != nil ||
		requests[1].Direction != "UP" || requests[1].Amount != 0.4 || requests[1].ElementPoint == nil || *requests[1].ElementPoint != (device.Point{X: 50, Y: 90}) {
		t.Fatalf("delayed ordinary/final requests = %#v", requests)
	}
}

func TestInteractionBatch3DeadlineWaitClampingAndHardGuard(t *testing.T) {
	t.Parallel()

	t.Run("zero timeout still observes once", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Other", device.Bounds{Width: 100, Height: 100})}, nil, nil)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"timeout": int64(0)}), nil, driver, newBatch3Clock(time.Unix(400, 0), true))
		var assertion *AssertionError
		if !errors.As(err, &assertion) || countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 || len(batch3ScrollRequests(driver.Actions())) != 0 {
			t.Fatalf("zero-timeout error = %T %v actions %#v", err, err, driver.Actions())
		}
	})

	t.Run("single deadline clamps waits", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), nil, []error{nil, nil, nil}, nil)
		clock := newBatch3Clock(time.Unix(410, 0), true)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"timeout": int64(250)}), nil, driver, clock)
		var assertion *AssertionError
		if !errors.As(err, &assertion) {
			t.Fatalf("deadline error = %T %v, want AssertionError", err, err)
		}
		if got, want := clock.Waits(), []time.Duration{100 * time.Millisecond, 100 * time.Millisecond, 50 * time.Millisecond}; !reflect.DeepEqual(got, want) {
			t.Fatalf("deadline waits = %#v, want %#v", got, want)
		}
		if got := len(batch3ScrollRequests(driver.Actions())); got != 3 {
			t.Fatalf("deadline scrolls = %d, want 3", got)
		}
		if got := countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor); got != 4 {
			t.Fatalf("deadline observations = %d, want 4", got)
		}
	})

	t.Run("omitted timeout uses one twenty-second deadline", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		clock := newBatch3Clock(time.Unix(415, 0), true)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, clock)
		var assertion *AssertionError
		if !errors.As(err, &assertion) {
			t.Fatalf("default deadline error = %T %v, want AssertionError", err, err)
		}
		if got := len(clock.Waits()); got != 200 {
			t.Fatalf("default deadline waits = %d, want 200", got)
		}
		if got := len(batch3ScrollRequests(driver.Actions())); got != 200 {
			t.Fatalf("default deadline scrolls = %d, want 200", got)
		}
		if got := countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor); got != 201 {
			t.Fatalf("default deadline observations = %d, want 201", got)
		}
	})

	t.Run("non-advancing clock guard", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		clock := newBatch3Clock(time.Unix(420, 0), false)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"timeout": int64(120000)}), nil, driver, clock)
		var assertion *AssertionError
		if !errors.As(err, &assertion) || !strings.Contains(err.Error(), "1201") {
			t.Fatalf("guard error = %T %v, want 1201-observation AssertionError", err, err)
		}
		if got := countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor); got != 1201 {
			t.Fatalf("guard observations = %d, want 1201", got)
		}
		if got := len(batch3ScrollRequests(driver.Actions())); got != 1200 {
			t.Fatalf("guard scrolls = %d, want 1200", got)
		}
		if got := len(clock.Waits()); got != 1200 {
			t.Fatalf("guard waits = %d, want 1200", got)
		}
	})
}

func TestInteractionBatch3ExplicitSettleOwnershipClampingAndErrors(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		timeout       int64
		settle        int64
		wantRequested int64
	}{
		{name: "explicit zero", timeout: 100, settle: 0, wantRequested: 0},
		{name: "positive", timeout: 100, settle: 25, wantRequested: 25},
		{name: "deadline clamp", timeout: 50, settle: 30000, wantRequested: 50},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
				batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
				batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
			}, []error{nil}, []enginetest.Result[*device.ViewHierarchy]{{Value: nil}})
			clock := newBatch3Clock(time.Unix(500, 0), true)
			_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
				"timeout": test.timeout, "waitToSettleTimeoutMs": test.settle,
			}), nil, driver, clock)
			if err != nil {
				t.Fatalf("settle execute error = %v", err)
			}
			requests := batch3SettleRequests(driver.Actions())
			if len(requests) != 1 || requests[0].TimeoutMillis == nil || *requests[0].TimeoutMillis != test.wantRequested || requests[0].AppID != "com.example.batch3" {
				t.Fatalf("settle requests = %#v, want one timeout %d", requests, test.wantRequested)
			}
		})
	}

	for _, terminal := range []error{
		NewDeviceConnectionError("connection", errors.New("transport")),
		NewConfigurationError("configuration", errors.New("config")),
	} {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Other", device.Bounds{Width: 100, Height: 100})}, []error{nil}, []enginetest.Result[*device.ViewHierarchy]{{Err: terminal}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
			"timeout": int64(100), "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, newBatch3Clock(time.Unix(510, 0), true))
		if err != terminal {
			t.Fatalf("terminal settle error = %T %v, want identity %T %v", err, err, terminal, terminal)
		}
	}

	for _, ordinary := range []*OperationError{
		NewOperationError("inconclusive", nil),
		NewOperationError("hierarchy did not settle before timeout", nil),
	} {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
			batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
		}, []error{nil}, []enginetest.Result[*device.ViewHierarchy]{{Err: ordinary}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
			"timeout": int64(100), "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, newBatch3Clock(time.Unix(520, 0), true))
		if err != nil {
			t.Fatalf("ordinary settle error %q was not ignored: %v", ordinary, err)
		}
	}

	t.Run("remaining deadline shrinks across repeated settles", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), nil, nil, []enginetest.Result[*device.ViewHierarchy]{{Value: nil}, {Value: nil}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
			"timeout": int64(50), "waitToSettleTimeoutMs": int64(30),
		}), nil, driver, newBatch3Clock(time.Unix(525, 0), true))
		var assertion *AssertionError
		if !errors.As(err, &assertion) {
			t.Fatalf("shrinking settle deadline error = %T %v, want AssertionError", err, err)
		}
		requests := batch3SettleRequests(driver.Actions())
		if len(requests) != 2 || requests[0].TimeoutMillis == nil || *requests[0].TimeoutMillis != 30 ||
			requests[1].TimeoutMillis == nil || *requests[1].TimeoutMillis != 20 {
			t.Fatalf("shrinking settle requests = %#v, want timeouts 30 then 20", requests)
		}
	})

	t.Run("positive settle remains bounded with non-advancing clock", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
			batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
		}, []error{nil}, []enginetest.Result[*device.ViewHierarchy]{{Value: nil}, {Value: nil}})
		clock := newBatch3Clock(time.Unix(527, 0), false)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
			"timeout": int64(500), "waitToSettleTimeoutMs": int64(250),
		}), nil, driver, clock)
		if err != nil || len(batch3ScrollRequests(driver.Actions())) != 1 || len(batch3SettleRequests(driver.Actions())) != 2 ||
			countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 2 ||
			!reflect.DeepEqual(clock.Waits(), []time.Duration{200 * time.Millisecond, 50 * time.Millisecond}) {
			t.Fatalf("non-advancing settle error = %v actions %#v waits %#v", err, driver.Actions(), clock.Waits())
		}
	})

	t.Run("final centering owns one settle", func(t *testing.T) {
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
		}, []error{nil}, []enginetest.Result[*device.ViewHierarchy]{{Value: nil}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{
			"visibilityPercentage": int64(10), "centerElement": true, "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, newBatch3Clock(time.Unix(530, 0), true))
		if err != nil || len(batch3ScrollRequests(driver.Actions())) != 1 || len(batch3SettleRequests(driver.Actions())) != 1 || countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("centering settle error = %v actions %#v", err, driver.Actions())
		}
	})

	t.Run("cancellation during settle is terminal", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		}, []error{nil}, nil)
		driver := &batch1ACancelSettleDriver{Driver: base, cancel: cancel}
		_, _, err := executeBatch3ForTest(ctx, batch3Command("Ready", map[string]any{
			"timeout": int64(100), "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, newBatch3Clock(time.Unix(540, 0), true))
		if !errors.Is(err, context.Canceled) || len(batch3ScrollRequests(base.Actions())) != 1 || countBatch3Method(base.Actions(), enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("settle cancellation error = %v actions %#v", err, base.Actions())
		}
	})
}

func TestInteractionBatch3CancellationAndDriverErrorCutoffs(t *testing.T) {
	t.Parallel()

	t.Run("pre-cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		trace := &batch3OperationTrace{}
		clock := newBatch3Clock(time.Unix(600, 0), true)
		clock.trace = trace
		_, _, err := executeBatch3ForTest(ctx, batch3Command("Ready", nil), nil, driver, clock)
		if !errors.Is(err, context.Canceled) || len(driver.Actions()) != 0 || len(trace.Snapshot()) != 0 {
			t.Fatalf("pre-cancel error = %v actions %#v trace %#v", err, driver.Actions(), trace.Snapshot())
		}
	})

	t.Run("device info error", func(t *testing.T) {
		primary := NewDeviceConnectionError("device info", errors.New("offline"))
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: primary}}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(610, 0), true))
		if err != primary || len(batch3ScrollRequests(driver.Actions())) != 0 {
			t.Fatalf("device info error = %T %v actions %#v", err, err, driver.Actions())
		}
	})

	t.Run("context-ignoring successful device info cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		driver := &cancelDuringDeviceInfoDriver{FakeDriver: base, cancel: cancel}
		_, _, err := executeBatch3ForTest(ctx, batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(615, 0), true))
		if !errors.Is(err, context.Canceled) || countBatch3Method(base.Actions(), enginetest.MethodDeviceInfo) != 1 ||
			countBatch3Method(base.Actions(), enginetest.MethodContentDescriptor) != 0 || len(batch3ScrollRequests(base.Actions())) != 0 {
			t.Fatalf("device-info cancellation error = %v actions %#v", err, base.Actions())
		}
	})

	t.Run("descriptor error", func(t *testing.T) {
		primary := errors.New("descriptor failed")
		driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: primary}}})
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(620, 0), true))
		if !errors.Is(err, primary) || len(batch3ScrollRequests(driver.Actions())) != 0 {
			t.Fatalf("descriptor error = %T %v actions %#v", err, err, driver.Actions())
		}
	})

	t.Run("context-ignoring successful descriptor cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		}, nil, nil)
		driver := &batch3CancelAfterDescriptorDriver{Driver: base, cancel: cancel}
		_, _, err := executeBatch3ForTest(ctx, batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(625, 0), true))
		if !errors.Is(err, context.Canceled) || countBatch3Method(base.Actions(), enginetest.MethodContentDescriptor) != 1 || len(batch3ScrollRequests(base.Actions())) != 0 {
			t.Fatalf("descriptor cancellation error = %v actions %#v", err, base.Actions())
		}
	})

	t.Run("scroll error has no later action", func(t *testing.T) {
		primary := errors.New("scroll failed")
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Other", device.Bounds{Width: 100, Height: 100})}, []error{primary}, nil)
		clock := newBatch3Clock(time.Unix(630, 0), true)
		lookup := NewElementLookup(driver, clock)
		_, _, err := executeBatch3WithLookupForTest(context.Background(), batch3Command("Ready", nil), nil, driver, clock, lookup)
		if !errors.Is(err, primary) || len(batch3ScrollRequests(driver.Actions())) != 1 || countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 || len(clock.Waits()) != 0 {
			t.Fatalf("scroll error = %T %v actions %#v waits %#v", err, err, driver.Actions(), clock.Waits())
		}
		clock.Advance(time.Second)
		if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
			t.Fatalf("failed scroll advanced watermark: adjusted timeout = %v", got)
		}
	})

	t.Run("centering error has no watermark or later action", func(t *testing.T) {
		primary := errors.New("centering failed")
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
		}, []error{primary}, nil)
		clock := newBatch3Clock(time.Unix(635, 0), true)
		lookup := NewElementLookup(driver, clock)
		_, _, err := executeBatch3WithLookupForTest(context.Background(), batch3Command("Ready", map[string]any{
			"visibilityPercentage": int64(10), "centerElement": true, "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, clock, lookup)
		requests := batch3ScrollRequests(driver.Actions())
		wantMethods := []enginetest.Method{
			enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodScrollVertical,
		}
		operation, isOperation := err.(*OperationError)
		if !isOperation || operation.cause != primary || !errors.Is(err, primary) || len(requests) != 1 || requests[0].ElementPoint == nil ||
			!reflect.DeepEqual(batch3Methods(driver.Actions()), wantMethods) ||
			len(batch3SettleRequests(driver.Actions())) != 0 || len(clock.Waits()) != 0 {
			t.Fatalf("centering error = %T %v actions %#v waits %#v", err, err, driver.Actions(), clock.Waits())
		}
		clock.Advance(time.Second)
		if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
			t.Fatalf("failed centering advanced watermark: adjusted timeout = %v", got)
		}
	})

	t.Run("cancellation during wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		clock := newBatch3Clock(time.Unix(640, 0), true)
		clock.onWait = cancel
		driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Other", device.Bounds{Width: 100, Height: 100})}, []error{nil}, nil)
		_, _, err := executeBatch3ForTest(ctx, batch3Command("Ready", nil), nil, driver, clock)
		if !errors.Is(err, context.Canceled) || len(batch3ScrollRequests(driver.Actions())) != 1 || countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 {
			t.Fatalf("wait cancellation error = %v actions %#v", err, driver.Actions())
		}
	})

	t.Run("post-scroll cancellation records watermark", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch3Driver(batch3Info(100, 100), []device.TreeNode{batch3Tree("Other", device.Bounds{Width: 100, Height: 100})}, []error{nil}, nil)
		driver := &batch3CancelAfterScrollDriver{Driver: base, cancel: cancel}
		clock := newBatch3Clock(time.Unix(650, 0), true)
		lookup := NewElementLookup(driver, clock)
		_, _, err := executeBatch3WithLookupForTest(ctx, batch3Command("Ready", nil), nil, driver, clock, lookup)
		if !errors.Is(err, context.Canceled) || len(batch3ScrollRequests(base.Actions())) != 1 || len(clock.Waits()) != 0 {
			t.Fatalf("post-scroll cancellation error = %v actions %#v waits %#v", err, base.Actions(), clock.Waits())
		}
		clock.Advance(time.Second)
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-time.Second; got != want {
			t.Fatalf("successful scroll watermark = %v, want %v", got, want)
		}
	})

	t.Run("post-centering cancellation records watermark and stops settle", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
		}, []error{nil}, nil)
		driver := &batch3CancelAfterScrollDriver{Driver: base, cancel: cancel}
		clock := newBatch3Clock(time.Unix(655, 0), true)
		lookup := NewElementLookup(driver, clock)
		_, _, err := executeBatch3WithLookupForTest(ctx, batch3Command("Ready", map[string]any{
			"visibilityPercentage": int64(10), "centerElement": true, "waitToSettleTimeoutMs": int64(0),
		}), nil, driver, clock, lookup)
		requests := batch3ScrollRequests(base.Actions())
		if !errors.Is(err, context.Canceled) || len(requests) != 1 || requests[0].ElementPoint == nil ||
			len(batch3SettleRequests(base.Actions())) != 0 || len(clock.Waits()) != 0 {
			t.Fatalf("post-centering cancellation error = %v actions %#v waits %#v", err, base.Actions(), clock.Waits())
		}
		clock.Advance(time.Second)
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-time.Second; got != want {
			t.Fatalf("successful centering watermark = %v, want %v", got, want)
		}
	})
}

func TestInteractionBatch3DeviceInfoGridValidationPrecedesHierarchyAndScroll(t *testing.T) {
	t.Parallel()

	for _, info := range []device.DeviceInfo{
		{},
		{Platform: "android", WidthGrid: 0, HeightGrid: 100},
		{Platform: "android", WidthGrid: 100, HeightGrid: 0},
		{Platform: "android", WidthGrid: -1, HeightGrid: 100},
		{Platform: "android", WidthGrid: 100, HeightGrid: -1},
	} {
		driver := batch3Driver(info, nil, nil, nil)
		_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(700, 0), true))
		if !isConfigurationError(err) {
			t.Fatalf("grid %#v error = %T %v, want ConfigurationError", info, err, err)
		}
		if got := batch3Methods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{enginetest.MethodDeviceInfo}) {
			t.Fatalf("grid %#v actions = %#v, want DeviceInfo only", info, driver.Actions())
		}
	}
}

func TestInteractionBatch3DeadlineIsAnchoredBeforeDeviceInfoAndObservation(t *testing.T) {
	t.Parallel()

	trace := &batch3OperationTrace{}
	base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
	}, nil, nil)
	driver := &batch3TracingDriver{Driver: base, trace: trace}
	clock := newBatch3Clock(time.Unix(750, 0), true)
	clock.trace = trace
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, clock)
	if err != nil {
		t.Fatalf("trace execute error = %v", err)
	}
	if got, want := trace.Snapshot(), []string{"clock.now", "driver.deviceInfo", "driver.contentDescriptor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deadline/device/observation order = %#v, want %#v", got, want)
	}
}

func TestInteractionBatch3CompiledOwnershipRepeatedAndConcurrentExecution(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	source := batch3Command("${TARGET}", map[string]any{"direction": "${DIRECTION}", "timeout": "${TIMEOUT}", "speed": int64(40)})
	original := cloneCommand(source)
	compiled, err := dispatcher.compile(context.Background(), compileContext{containingFlow: "/workspace/batch3.yaml"}, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Arguments.(map[string]any)["direction"] = "MUTATED"
	*source.Selector.TextRegex = "MUTATED"

	const executions = 32
	var group sync.WaitGroup
	errs := make(chan error, executions)
	for index := range executions {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			direction := "DOWN"
			if index%2 == 1 {
				direction = "UP"
			}
			evaluated, evaluateErr := dispatcher.evaluate(context.Background(), batch2AEvaluation(map[string]string{
				"TARGET": "Ready", "DIRECTION": direction, "TIMEOUT": "100",
			}, fmt.Sprintf("com.example.batch3.%d", index)), compiled)
			if evaluateErr != nil {
				errs <- evaluateErr
				return
			}
			base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
				batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
				batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
			}, []error{nil}, nil)
			driver := &batch3MutatingDriver{Driver: base}
			clock := newBatch3Clock(time.Unix(int64(800+index), 0), true)
			lookup := NewElementLookup(driver, clock)
			state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
			if _, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated); executeErr != nil {
				errs <- executeErr
				return
			}
			args := evaluated.command.Arguments.(map[string]any)
			requests := driver.Snapshot()
			if len(requests) != 1 || requests[0].Direction != device.Direction(direction) || requests[0].Amount != 0.4 || requests[0].ElementPoint != nil {
				errs <- fmt.Errorf("owned request mutated: %#v", requests)
				return
			}
			if args["element"] != "Ready" || args["direction"] != direction || args["timeout"] != "100" {
				errs <- fmt.Errorf("evaluated command ownership escaped: %#v", args)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if source.Arguments.(map[string]any)["element"] != "${TARGET}" || source.Arguments.(map[string]any)["direction"] != "MUTATED" || *source.Selector.TextRegex != "MUTATED" {
		t.Fatalf("test source mutation shape changed unexpectedly: %#v", source)
	}
	if original.Arguments.(map[string]any)["direction"] != "${DIRECTION}" || *original.Selector.TextRegex != "${TARGET}" {
		t.Fatalf("original snapshot mutated = %#v", original)
	}

	t.Run("centering request owns the exact element point", func(t *testing.T) {
		base := batch3Driver(batch3Info(100, 100), []device.TreeNode{
			batch3Tree("Ready", device.Bounds{X: 1, Y: 75, Width: 99, Height: 31}),
		}, []error{nil}, nil)
		driver := &batch3MutatingDriver{Driver: base}
		_, _, err := executeBatch3ForTest(
			context.Background(),
			batch3Command("Ready", map[string]any{"visibilityPercentage": int64(10), "centerElement": true}),
			nil,
			driver,
			newBatch3Clock(time.Unix(823, 0), true),
		)
		requests := driver.Snapshot()
		if err != nil || len(requests) != 1 || requests[0].Direction != "UP" || requests[0].Amount != 0.405 ||
			requests[0].ElementPoint == nil || *requests[0].ElementPoint != (device.Point{X: 50.5, Y: 90.5}) {
			t.Fatalf("owned centering request error = %v requests %#v", err, requests)
		}
	})

	t.Run("reused hierarchy source is refreshed without retaining mutable state", func(t *testing.T) {
		base := batch3Driver(batch3Info(100, 100), nil, []error{nil}, nil)
		driver := &batch3ReusedHierarchyDriver{
			Driver: base,
			root:   batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		}
		_, _, err := executeBatch3ForTest(
			context.Background(),
			batch3Command("Ready", map[string]any{"timeout": int64(100)}),
			nil,
			driver,
			newBatch3Clock(time.Unix(824, 0), true),
		)
		if err != nil || len(batch3ScrollRequests(base.Actions())) != 1 || countBatch3Method(base.Actions(), enginetest.MethodContentDescriptor) != 2 {
			t.Fatalf("reused hierarchy execution error = %v actions %#v", err, base.Actions())
		}
	})
}

func TestInteractionBatch3FailClosedInternalPayloadsAndEvaluationDependencies(t *testing.T) {
	t.Parallel()

	if _, err := compileScrollUntilVisible(batch2ABareCommand(model.CommandScroll)); !isConfigurationError(err) {
		t.Fatalf("wrong-keyword compiler error = %T %v", err, err)
	}
	command := batch3Command("Ready", nil)
	compiledValue, err := compileScrollUntilVisible(command)
	if err != nil {
		t.Fatal(err)
	}
	compiled := compiledValue.(scrollUntilVisibleCompiled)
	if _, err := evaluateScrollUntilVisible(context.Background(), batch2AEvaluation(nil, "app"), command, struct{}{}); !isConfigurationError(err) {
		t.Fatalf("invalid compiled payload error = %T %v", err, err)
	}
	if _, err := evaluateScrollUntilVisible(context.Background(), evaluationContext{}, command, compiled); !isConfigurationError(err) {
		t.Fatalf("missing active config error = %T %v", err, err)
	}
	if _, err := evaluateScrollUntilVisible(context.Background(), batch2AEvaluation(nil, " "), command, compiled); !isConfigurationError(err) {
		t.Fatalf("blank active app error = %T %v", err, err)
	}
	invalidArguments := cloneCommand(command)
	invalidArguments.Arguments = "not-an-object"
	if _, err := evaluateScrollUntilVisible(context.Background(), batch2AEvaluation(nil, "app"), invalidArguments, compiled); !isConfigurationError(err) {
		t.Fatalf("invalid evaluated arguments error = %T %v", err, err)
	}

	interpolationErr := errors.New("interpolation failed")
	failingEvaluation := evaluationContext{
		interpolateFn: func(context.Context, string, map[string]any) (string, error) { return "", interpolationErr },
		activeConfig:  model.Config{AppID: "app"}, hasActiveConfig: true,
	}
	dynamicCommand := batch3Command("${TARGET}", map[string]any{"direction": "${DIRECTION}", "timeout": "${TIMEOUT}"})
	dynamicValue, err := compileScrollUntilVisible(dynamicCommand)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluateScrollUntilVisible(context.Background(), failingEvaluation, dynamicCommand, dynamicValue); !errors.Is(err, interpolationErr) {
		t.Fatalf("late interpolation error = %T %v", err, err)
	}

	driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
	clock := newBatch3Clock(time.Unix(825, 0), true)
	lookup := NewElementLookup(driver, clock)
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
	}
	valid := scrollUntilVisibleEvaluated{
		selector: &model.ElementSelector{TextRegex: stringPointer("Ready")}, appID: "app", direction: "DOWN",
		timeoutMillis: 1, speed: 40, visibilityPercentage: 100,
	}
	var nilContext context.Context
	if _, err := executeScrollUntilVisible(nilContext, state, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
		t.Fatalf("nil execution context error = %T %v, want ConfigurationError", err, err)
	}
	preCancelled, cancel := context.WithCancel(context.Background())
	cancel()
	preCancelTrace := &batch3OperationTrace{}
	preCancelClock := newBatch3Clock(time.Unix(825, 0), true)
	preCancelClock.trace = preCancelTrace
	preCancelDriver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
	preCancelLookup := NewElementLookup(preCancelDriver, preCancelClock)
	preCancelState := &executionState{
		dependencies: Dependencies{Driver: preCancelDriver, Clock: preCancelClock},
		lookupFn:     func() (*ElementLookup, error) { return preCancelLookup, nil },
	}
	if _, err := executeScrollUntilVisible(preCancelled, preCancelState, evaluatedDispatch{value: valid}); !errors.Is(err, context.Canceled) {
		t.Fatalf("direct pre-cancel error = %T %v, want context.Canceled", err, err)
	}
	if len(preCancelDriver.Actions()) != 0 || len(preCancelTrace.Snapshot()) != 0 {
		t.Fatalf("direct pre-cancel crossed service boundary: actions %#v trace %#v", preCancelDriver.Actions(), preCancelTrace.Snapshot())
	}
	var typedNilDriver *enginetest.FakeDriver
	typedNilDriverState := &executionState{dependencies: Dependencies{Driver: typedNilDriver, Clock: clock}}
	if _, err := executeScrollUntilVisible(context.Background(), typedNilDriverState, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Driver error = %T %v, want ConfigurationError", err, err)
	}
	var typedNilClock *batch3Clock
	typedNilClockState := &executionState{dependencies: Dependencies{Driver: driver, Clock: typedNilClock}}
	if _, err := executeScrollUntilVisible(context.Background(), typedNilClockState, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Clock error = %T %v, want ConfigurationError", err, err)
	}
	negativeSettle := int64(-1)
	invalidPlans := []any{
		struct{}{},
		scrollUntilVisibleEvaluated{appID: "app", direction: "DOWN", timeoutMillis: 1, speed: 40, visibilityPercentage: 100},
		func() scrollUntilVisibleEvaluated { plan := valid; plan.appID = " "; return plan }(),
		func() scrollUntilVisibleEvaluated { plan := valid; plan.direction = "diagonal"; return plan }(),
		func() scrollUntilVisibleEvaluated { plan := valid; plan.timeoutMillis = -1; return plan }(),
		func() scrollUntilVisibleEvaluated { plan := valid; plan.speed = 0; return plan }(),
		func() scrollUntilVisibleEvaluated { plan := valid; plan.visibilityPercentage = 9; return plan }(),
		func() scrollUntilVisibleEvaluated {
			plan := valid
			plan.settleTimeoutMillis = &negativeSettle
			return plan
		}(),
		func() scrollUntilVisibleEvaluated {
			plan := valid
			plan.selector = &model.ElementSelector{TextRegex: stringPointer("[")}
			return plan
		}(),
	}
	for _, invalid := range invalidPlans {
		if _, err := executeScrollUntilVisible(context.Background(), state, evaluatedDispatch{value: invalid}); !isConfigurationError(err) {
			t.Fatalf("invalid evaluated payload %#v error = %T %v", invalid, err, err)
		}
	}
	if _, err := executeScrollUntilVisible(context.Background(), nil, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
		t.Fatalf("nil state error = %T %v", err, err)
	}
	missingServices := &executionState{}
	if _, err := executeScrollUntilVisible(context.Background(), missingServices, evaluatedDispatch{value: valid}); !isConfigurationError(err) {
		t.Fatalf("missing services error = %T %v", err, err)
	}
	lookupErr := errors.New("lookup unavailable")
	lookupFailure := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return nil, lookupErr },
	}
	if _, err := executeScrollUntilVisible(context.Background(), lookupFailure, evaluatedDispatch{value: valid}); !errors.Is(err, lookupErr) {
		t.Fatalf("lookup error = %T %v", err, err)
	}
	if len(driver.Actions()) != 0 {
		t.Fatalf("invalid internal payload caused Driver effects: %#v", driver.Actions())
	}
}

func TestInteractionBatch3ConcurrentSelectedRootLifecycleIsolation(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch3-concurrent.yaml"},
		batch3Command("Ready", nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := &compiledFlow{
		path:   "/workspace/batch3-concurrent.yaml",
		config: model.Config{AppID: "com.example.batch3.concurrent"},
		body:   []compiledDispatch{compiled},
	}

	const roots = 16
	var group sync.WaitGroup
	errs := make(chan error, roots)
	for index := range roots {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
				batch3Tree("Ready", device.Bounds{Width: 100, Height: 100}),
			}, nil, nil)
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch3-concurrent", Driver: driver,
				Clock:     newBatch3Clock(time.Unix(int64(850+index), 0), true),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
			}, root, fmt.Sprintf("batch3-concurrent/root-run-%06d", index+1))
			if runErr != nil {
				errs <- runErr
				return
			}
			commands := result.Commands()
			if result.Outcome() != Completed || len(commands) != 1 || commands[0].Outcome() != Completed ||
				countBatch3Method(driver.Actions(), enginetest.MethodDeviceInfo) != 1 ||
				countBatch3Method(driver.Actions(), enginetest.MethodContentDescriptor) != 1 ||
				len(batch3ScrollRequests(driver.Actions())) != 0 {
				errs <- fmt.Errorf("concurrent root %d state escaped: result %#v actions %#v", index, result, driver.Actions())
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestInteractionBatch3StaticInvalidFirstAndLaterRootsHaveWholeProgramZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	valid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.valid"}, Commands: []model.Command{batch3Command("Ready", nil)}}
	}
	invalid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.invalid"}, Commands: []model.Command{batch3Command("Ready", map[string]any{"speed": int64(0)})}}
	}
	for _, flows := range [][]model.Flow{
		{invalid("/workspace/invalid-first.yaml"), valid("/workspace/valid-later.yaml")},
		{valid("/workspace/valid-first.yaml"), invalid("/workspace/invalid-later.yaml")},
	} {
		driver := enginetest.NewFakeDriver()
		factory := &countingRuntimeFactory{}
		listenerCalls := 0
		compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(flows...), registry)
		if compiled != nil || !isConfigurationError(compileErr) {
			t.Fatalf("compileProgram() = %#v, %T %v; want nil ConfigurationError", compiled, compileErr, compileErr)
		}
		if factory.calls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
			t.Fatalf("preflight effects = runtime %d listeners %d driver %#v", factory.calls, listenerCalls, driver.Actions())
		}
	}
}

func TestInteractionBatch3SelectedRootLateInvalidAllowsOnlySessionDeviceInfo(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec(), scrollHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	command := batch3Command("${TARGET}", map[string]any{"direction": "${DIRECTION}"})
	command.Source.Path = "/workspace/batch3-late-invalid.yaml"
	compiled, err := newDispatcher(registry).compileSequence(
		context.Background(),
		compileContext{containingFlow: command.Source.Path},
		[]model.Command{command, batch2ABareCommand(model.CommandScroll)},
	)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch3Driver(batch3Info(100, 100), nil, nil, nil)
	clock := newBatch3Clock(time.Unix(875, 0), true)
	events := make([]Event, 0, 4)
	root := &compiledFlow{
		path: command.Source.Path,
		config: model.Config{
			AppID: "com.example.batch3.late-invalid",
			Env:   map[string]string{"TARGET": "Ready", "DIRECTION": "sideways"},
		},
		body: compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch3-late-invalid", Driver: driver, Clock: clock,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch3-late-invalid/root-run-000001")
	commands := result.Commands()
	if !isConfigurationError(runErr) || result.Outcome() != Failed || len(commands) != 1 ||
		commands[0].Command().Kind != model.CommandScrollUntilVisible || commands[0].Outcome() != Failed {
		t.Fatalf("late-invalid selected root = result %#v commands %#v error %T %v", result, commands, runErr, runErr)
	}
	wantMethods := []enginetest.Method{enginetest.MethodDeviceInfo}
	if !reflect.DeepEqual(batch3Methods(driver.Actions()), wantMethods) || len(clock.Waits()) != 0 {
		t.Fatalf("late-invalid selected-root effects = actions %#v waits %#v", driver.Actions(), clock.Waits())
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Failed || events[3].Outcome() != Failed {
		t.Fatalf("late-invalid selected-root lifecycle = %#v", events)
	}
}

func TestInteractionBatch3RequiredExhaustionProjectsFailureEvidenceAndStopsLaterCommand(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec(), scrollHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	search := batch3Command("Ready", map[string]any{"timeout": int64(0)})
	search.Source.Path = "/workspace/batch3-failure.yaml"
	compiled, err := dispatcher.compileSequence(context.Background(), compileContext{containingFlow: search.Source.Path}, []model.Command{
		search,
		batch2ABareCommand(model.CommandScroll),
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
	}, nil, nil)
	driver.Enqueue(enginetest.DriverScript{TakeScreenshot: []enginetest.Result[[]byte]{{Value: []byte("batch3-png")}}})
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("batch3/failure.png", int64(len("batch3-png")))}}}
	events := make([]Event, 0, 4)
	root := &compiledFlow{
		path:   search.Source.Path,
		config: model.Config{Name: "Batch 3 failure", AppID: "com.example.batch3.failure"},
		body:   compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch3-failure", Driver: driver, Clock: newBatch3Clock(time.Unix(900, 0), true),
		JSFactory: tapJSFactory(t), Controller: NoopController{}, ArtifactSink: sink,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch3-failure/root-run-000001")
	var assertion *AssertionError
	if !errors.As(runErr, &assertion) || result.Outcome() != Failed || result.ProductError() != runErr {
		t.Fatalf("failure root = outcome %s product %T %v returned %T %v", result.Outcome(), result.ProductError(), result.ProductError(), runErr, runErr)
	}
	commands := result.Commands()
	if len(commands) != 1 || commands[0].Command().Kind != model.CommandScrollUntilVisible || commands[0].Outcome() != Failed || commands[0].ProductError() != runErr {
		t.Fatalf("failure commands = %#v", commands)
	}
	assertFailureEvidenceArtifacts(t, commands[0].Artifacts(), "batch3/failure.png")
	if countBatch3Method(driver.Actions(), enginetest.MethodTakeScreenshot) != 1 || len(sink.Requests()) != 1 || len(batch3ScrollRequests(driver.Actions())) != 0 {
		t.Fatalf("failure evidence/cutoff actions = %#v writes %#v", driver.Actions(), sink.Requests())
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Failed || events[2].ProductError() != runErr || events[3].ProductError() != runErr {
		t.Fatalf("failure lifecycle = %#v", events)
	}
}

func executeBatch3ForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
) (commandEffect, evaluatedDispatch, error) {
	return executeBatch3WithLookupForTest(ctx, command, replacements, driver, clock, NewElementLookup(driver, clock))
}

func executeBatch3WithLookupForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
) (commandEffect, evaluatedDispatch, error) {
	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{containingFlow: "/workspace/batch3.yaml"}, command)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, batch2AEvaluation(replacements, "com.example.batch3"), compiled)
	if err != nil {
		return commandEffect{}, evaluated, err
	}
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
	}
	effect, err := dispatcher.execute(ctx, state, compiled, evaluated)
	return effect, evaluated, err
}

func batch3Command(element string, fields map[string]any) model.Command {
	return batch3CommandWithSelector(element, &model.ElementSelector{TextRegex: stringPointer(element)}, fields)
}

func batch3CommandWithSelector(element any, selector *model.ElementSelector, fields map[string]any) model.Command {
	arguments := map[string]any{"element": cloneDynamic(element)}
	for name, value := range fields {
		arguments[name] = cloneDynamic(value)
	}
	return model.Command{
		Kind: model.CommandScrollUntilVisible, Form: model.CommandFormObject,
		Arguments: arguments, Selector: cloneSelector(selector),
	}
}

func batch3Info(width, height int) device.DeviceInfo {
	return device.DeviceInfo{Platform: "android", WidthGrid: width, HeightGrid: height}
}

func batch3Tree(text string, bounds device.Bounds) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][100,100]"},
		Children: []device.TreeNode{{Attributes: map[string]string{
			"text": text, "bounds": fmt.Sprintf("[%d,%d][%d,%d]", bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height),
		}}},
	}
}

func batch3TreeWithoutTargetBounds(text string) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][100,100]"},
		Children:   []device.TreeNode{{Attributes: map[string]string{"text": text}}},
	}
}

func batch3Driver(
	info device.DeviceInfo,
	descriptors []device.TreeNode,
	scrollErrors []error,
	settle []enginetest.Result[*device.ViewHierarchy],
) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	scrolls := make([]enginetest.Result[struct{}], len(scrollErrors))
	for index, err := range scrollErrors {
		scrolls[index].Err = err
	}
	descriptorResults := make([]enginetest.Result[device.TreeNode], len(descriptors))
	for index, descriptor := range descriptors {
		descriptorResults[index].Value = descriptor
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: info}},
		ContentDescriptor:  descriptorResults,
		ScrollVertical:     scrolls,
		WaitForAppToSettle: settle,
	})
	return driver
}

func batch3Methods(actions []enginetest.Action) []enginetest.Method {
	methods := make([]enginetest.Method, len(actions))
	for index, action := range actions {
		methods[index] = action.Method
	}
	return methods
}

func batch3ScrollRequests(actions []enginetest.Action) []device.ScrollVerticalRequest {
	requests := make([]device.ScrollVerticalRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodScrollVertical {
			requests = append(requests, action.Request.(device.ScrollVerticalRequest))
		}
	}
	return requests
}

func batch3SettleRequests(actions []enginetest.Action) []device.SettleRequest {
	requests := make([]device.SettleRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodWaitForAppToSettle {
			requests = append(requests, action.Request.(device.SettleRequest))
		}
	}
	return requests
}

func countBatch3Method(actions []enginetest.Action, method enginetest.Method) int {
	count := 0
	for _, action := range actions {
		if action.Method == method {
			count++
		}
	}
	return count
}

type batch3Clock struct {
	mu      sync.Mutex
	now     time.Time
	advance bool
	waits   []time.Duration
	onWait  func()
	trace   *batch3OperationTrace
}

func newBatch3Clock(now time.Time, advance bool) *batch3Clock {
	return &batch3Clock{now: now, advance: advance}
}

func (clock *batch3Clock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.trace.Add("clock.now")
	return clock.now
}

func (clock *batch3Clock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.waits = append(clock.waits, delay)
	if clock.advance {
		clock.now = clock.now.Add(delay)
	}
	onWait := clock.onWait
	clock.mu.Unlock()
	if onWait != nil {
		onWait()
	}
	return ctx.Err()
}

func (clock *batch3Clock) Waits() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]time.Duration(nil), clock.waits...)
}

func (clock *batch3Clock) Advance(delay time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(delay)
	clock.mu.Unlock()
}

type batch3CancelAfterScrollDriver struct {
	device.Driver
	cancel context.CancelFunc
}

type batch3CancelAfterDescriptorDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch3CancelAfterDescriptorDriver) ContentDescriptor(_ context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	descriptor, err := driver.Driver.ContentDescriptor(context.Background(), request)
	driver.cancel()
	return descriptor, err
}

func (driver *batch3CancelAfterScrollDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	err := driver.Driver.ScrollVertical(context.Background(), request)
	driver.cancel()
	return err
}

type batch3MutatingDriver struct {
	device.Driver
	mu       sync.Mutex
	requests []device.ScrollVerticalRequest
}

func (driver *batch3MutatingDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	owned := request
	if request.ElementPoint != nil {
		point := *request.ElementPoint
		owned.ElementPoint = &point
	}
	driver.mu.Lock()
	driver.requests = append(driver.requests, owned)
	driver.mu.Unlock()
	request.Direction = "MUTATED"
	request.Amount = -1
	if request.ElementPoint != nil {
		request.ElementPoint.X = -1
	}
	return driver.Driver.ScrollVertical(ctx, request)
}

func (driver *batch3MutatingDriver) Snapshot() []device.ScrollVerticalRequest {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	requests := make([]device.ScrollVerticalRequest, len(driver.requests))
	for index, request := range driver.requests {
		requests[index] = request
		if request.ElementPoint != nil {
			point := *request.ElementPoint
			requests[index].ElementPoint = &point
		}
	}
	return requests
}

type batch3ReusedHierarchyDriver struct {
	device.Driver
	mu   sync.Mutex
	root device.TreeNode
}

func (driver *batch3ReusedHierarchyDriver) ContentDescriptor(ctx context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	if _, err := driver.Driver.ContentDescriptor(ctx, request); err != nil {
		return device.TreeNode{}, err
	}
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.root, nil
}

func (driver *batch3ReusedHierarchyDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	if err := driver.Driver.ScrollVertical(ctx, request); err != nil {
		return err
	}
	driver.mu.Lock()
	driver.root.Children[0].Attributes["text"] = "Ready"
	driver.mu.Unlock()
	return nil
}

type batch3OperationTrace struct {
	mu      sync.Mutex
	entries []string
}

func (trace *batch3OperationTrace) Add(entry string) {
	if trace == nil {
		return
	}
	trace.mu.Lock()
	trace.entries = append(trace.entries, entry)
	trace.mu.Unlock()
}

func (trace *batch3OperationTrace) Snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.entries...)
}

type batch3TracingDriver struct {
	device.Driver
	trace *batch3OperationTrace
}

func (driver *batch3TracingDriver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	driver.trace.Add("driver.deviceInfo")
	return driver.Driver.DeviceInfo(ctx)
}

func (driver *batch3TracingDriver) ContentDescriptor(ctx context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	driver.trace.Add("driver.contentDescriptor")
	return driver.Driver.ContentDescriptor(ctx, request)
}
