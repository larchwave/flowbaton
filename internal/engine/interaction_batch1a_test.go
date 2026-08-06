package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch1APrivateRegistryAndStaticContract(t *testing.T) {
	t.Parallel()

	direct, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	if got := sortedHandlerKeywords(direct); !reflect.DeepEqual(got, []string{"doubleTapOn", "longPressOn"}) {
		t.Fatalf("direct registry keys = %#v, want private Batch 1A keys", got)
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
		batch1ACommand(model.CommandDoubleTapOn, "Target", "", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "0,0", nil, nil, intPointerForTap(0), intPointerForTap(0)),
		batch1ACommand(model.CommandDoubleTapOn, "Target", "99%,99%", boolPointer(true), stringPointer("double"), intPointerForTap(30000), intPointerForTap(30000)),
		batch1ACommand(model.CommandLongPressOn, "Target", "", nil, nil, nil, nil),
		batch1ACommand(model.CommandLongPressOn, "", "20,30", boolPointer(true), stringPointer("hold"), nil, intPointerForTap(30000)),
	}
	for _, command := range accepted {
		spec, _ := direct.lookup(command.Kind)
		if _, err := spec.compile(context.Background(), compileContext{}, command); err != nil {
			t.Fatalf("compile(%s %#v) error = %v", command.Kind, command.Arguments, err)
		}
	}

	invalid := []model.Command{
		batch1ACommand(model.CommandDoubleTapOn, "", "", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20%,30", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "100%,0%", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20, 30", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", " 20,30", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30 ", nil, nil, nil, nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, intPointerForTap(-1), nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, intPointerForTap(30001), nil),
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(-1)),
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(30001)),
		batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, intPointerForTap(1), nil),
		batch1ACommand(model.CommandLongPressOn, "", "50%, 36%", nil, nil, nil, nil),
		batch1ACommand(model.CommandLongPressOn, "", "50% ,36%", nil, nil, nil, nil),
		batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, nil, intPointerForTap(-1)),
	}
	invalid = append(invalid,
		batch1ACommandWithUnsupported(model.CommandDoubleTapOn, "repeat"),
		batch1ACommandWithUnsupported(model.CommandDoubleTapOn, "retryTapIfNoChange"),
		batch1ACommandWithUnsupported(model.CommandDoubleTapOn, "waitUntilVisible"),
		batch1ACommandWithUnsupported(model.CommandLongPressOn, "repeat"),
		batch1ACommandWithUnsupported(model.CommandLongPressOn, "duration"),
		batch1ACommandWithUnsupported(model.CommandLongPressOn, "retryTapIfNoChange"),
		batch1ACommandWithUnsupported(model.CommandLongPressOn, "waitUntilVisible"),
	)
	for _, command := range invalid {
		spec, _ := direct.lookup(command.Kind)
		if _, err := spec.compile(context.Background(), compileContext{}, command); !isConfigurationError(err) {
			t.Fatalf("compile(%s %#v) error = %T %v, want ConfigurationError", command.Kind, command.Arguments, err, err)
		}
	}
}

func TestInteractionBatch1ATargetGrammarLateEvaluationAndStability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		command      model.Command
		replacements map[string]string
		driver       *enginetest.FakeDriver
		wantPoint    device.Point
	}{
		{
			name: "absolute screen point", command: batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(0)),
			driver: batch1APointDriver(400, 884), wantPoint: device.Point{X: 20, Y: 30},
		},
		{
			name: "percentage screen point", command: batch1ACommand(model.CommandDoubleTapOn, "", "50%,36%", nil, nil, nil, intPointerForTap(0)),
			driver: batch1APointDriver(400, 884), wantPoint: device.Point{X: 200, Y: 318},
		},
		{
			name: "late point", command: batch1ACommand(model.CommandDoubleTapOn, "", "${POINT}", nil, nil, nil, intPointerForTap(0)),
			replacements: map[string]string{"POINT": "40,60"}, driver: batch1APointDriver(400, 884), wantPoint: device.Point{X: 40, Y: 60},
		},
		{
			name: "moving selector center", command: batch1ACommand(model.CommandDoubleTapOn, "${TARGET}", "", nil, nil, nil, intPointerForTap(0)),
			replacements: map[string]string{"TARGET": "Continue"}, driver: batch1ASelectorDriver(
				device.Bounds{X: 10, Y: 20, Width: 20, Height: 20},
				device.Bounds{X: 100, Y: 200, Width: 40, Height: 60},
				device.Bounds{X: 100, Y: 200, Width: 40, Height: 60},
			), wantPoint: device.Point{X: 120, Y: 230},
		},
		{
			name: "selector relative point", command: batch1ACommand(model.CommandLongPressOn, "Continue", "50%,25%", nil, nil, nil, intPointerForTap(0)),
			driver: batch1ASelectorDriver(
				device.Bounds{X: 100, Y: 200, Width: 40, Height: 60},
				device.Bounds{X: 100, Y: 200, Width: 40, Height: 60},
			), wantPoint: device.Point{X: 120, Y: 215},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newAdvancingClock()
			lookup := NewElementLookup(test.driver, clock)
			effect, err := executeBatch1AForTest(context.Background(), test.command, test.replacements, test.driver, clock, lookup)
			if err != nil {
				t.Fatalf("execute() error = %v", err)
			}
			if effect.effectClass != EffectDeviceMutation {
				t.Fatalf("effect class = %v, want device mutation", effect.effectClass)
			}
			switch test.command.Kind {
			case model.CommandDoubleTapOn:
				requests := tapRequests(test.driver.Actions())
				want := []device.TapRequest{{Point: test.wantPoint}, {Point: test.wantPoint}}
				if !reflect.DeepEqual(requests, want) {
					t.Fatalf("Tap requests = %#v, want %#v", requests, want)
				}
			case model.CommandLongPressOn:
				requests := batch1ALongPressRequests(test.driver.Actions())
				want := []device.LongPressRequest{{Point: test.wantPoint, DurationMillis: 3000}}
				if !reflect.DeepEqual(requests, want) {
					t.Fatalf("LongPress requests = %#v, want %#v", requests, want)
				}
			}
		})
	}
}

func TestInteractionBatch1ALateInvalidAndPointBoundsFailBeforePhysicalAction(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		command      model.Command
		replacements map[string]string
	}{
		{
			name:         "late malformed point",
			command:      batch1ACommand(model.CommandDoubleTapOn, "", "${POINT}", nil, nil, nil, intPointerForTap(0)),
			replacements: map[string]string{"POINT": "20%,30"},
		},
		{
			name:         "late double tap whitespace",
			command:      batch1ACommand(model.CommandDoubleTapOn, "", "${POINT}", nil, nil, nil, intPointerForTap(0)),
			replacements: map[string]string{"POINT": "20, 30"},
		},
		{
			name:         "late long press whitespace",
			command:      batch1ACommand(model.CommandLongPressOn, "", "${POINT}", nil, nil, nil, intPointerForTap(0)),
			replacements: map[string]string{"POINT": " 20,30"},
		},
		{
			name:    "absolute x at upper bound",
			command: batch1ACommand(model.CommandLongPressOn, "", "400,30", nil, nil, nil, intPointerForTap(0)),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			_, err := executeBatch1AForTest(context.Background(), test.command, test.replacements, driver, clock, lookup)
			if !isConfigurationError(err) {
				t.Fatalf("execute() error = %T %v, want ConfigurationError", err, err)
			}
			if len(tapRequests(driver.Actions())) != 0 || len(batch1ALongPressRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("invalid target caused physical action: %#v", driver.Actions())
			}
		})
	}
}

func TestInteractionBatch1AInvalidPostInterpolationRegexIsConfigurationErrorBeforeEffects(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
		t.Run(string(keyword), func(t *testing.T) {
			optional := true
			driver := batch1APointDriver(400, 884)
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			_, err := executeBatch1AForTest(
				context.Background(),
				batch1ACommand(keyword, "${TARGET}", "", &optional, nil, nil, intPointerForTap(0)),
				map[string]string{"TARGET": "["},
				driver,
				clock,
				lookup,
			)
			if !isConfigurationError(err) || ClassifyOutcome(err, true) != Failed {
				t.Fatalf("execute() error = %T %v outcome %q, want ConfigurationError/Failed", err, err, ClassifyOutcome(err, true))
			}
			if len(tapRequests(driver.Actions())) != 0 || len(batch1ALongPressRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("invalid evaluated regex caused effects: %#v", driver.Actions())
			}
		})
	}
}

func TestInteractionBatch1ADirectRegistryProducesOneCommandResult(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
		t.Run(string(keyword), func(t *testing.T) {
			driver := batch1APointDriver(400, 884)
			clock := newAdvancingClock()
			dependencies := coreDependencies(driver, clock)
			dependencies.JSFactory = tapJSFactory(t)
			core, err := newExecutorCore(dependencies, doubleTapOnHandlerSpec(), longPressOnHandlerSpec())
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			runtime, err := dependencies.JSFactory.NewRuntime()
			if err != nil {
				t.Fatalf("NewRuntime() error = %v", err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			lookup := NewElementLookup(driver, clock)
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
			core.state.currentConfigFn = func() (model.Config, error) { return model.Config{AppID: "com.example.batch1a"}, nil }
			core.state.lookupFn = func() (*ElementLookup, error) { return lookup, nil }

			result, err := core.execute(context.Background(), batch1ACommand(keyword, "", "20,30", nil, nil, nil, intPointerForTap(0)), 0)
			if err != nil || result.Outcome() != Completed || result.Sequence() != 1 || result.Command().Kind != keyword {
				t.Fatalf("execute() = outcome %q sequence %d kind %q error %v", result.Outcome(), result.Sequence(), result.Command().Kind, err)
			}
		})
	}
}

func TestInteractionBatch1ACF012SelectorPolicyMatrix(t *testing.T) {
	t.Parallel()

	t.Run("required and optional absence use exact adjusted deadlines", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			keyword  model.CommandKeyword
			optional bool
			wantWait time.Duration
			want     Outcome
		}{
			{name: "required double tap 17s", keyword: model.CommandDoubleTapOn, wantWait: 17 * time.Second, want: Failed},
			{name: "optional long press 7s", keyword: model.CommandLongPressOn, optional: true, wantWait: 7 * time.Second, want: Warned},
		} {
			t.Run(test.name, func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				clock := &batch1ATraceClock{now: time.Unix(800, 0)}
				lookup := NewElementLookup(driver, clock)
				lookup.RecordInteraction(clock.now)
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(test.keyword, "Missing", "", &test.optional, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				var operation *OperationError
				if ClassifyOutcome(err, test.optional) != test.want || classifyTerminalError(err) != terminalErrorRetryable ||
					!errors.As(err, &operation) || errors.Unwrap(operation) != nil {
					t.Fatalf("absence error = %T %v class %v outcome %q, want OperationError(nil cause)/retryable/%q",
						err, err, classifyTerminalError(err), ClassifyOutcome(err, test.optional), test.want)
				}
				if got := sumBatch1ADurations(clock.waits); got != test.wantWait {
					t.Fatalf("lookup waits = %v, want exact adjusted deadline %v", got, test.wantWait)
				}
				assertBatch1AZeroPhysicalAndSettle(t, driver)
			})
		}
	})

	t.Run("selector appearance resolves once for both handlers", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			t.Run(string(keyword), func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				bounds := device.Bounds{X: 30, Y: 40, Width: 20, Height: 30}
				driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{
					{Value: batch1AEmptyTree()}, {Value: tapTree(bounds)}, {Value: tapTree(bounds)},
				}})
				clock := newAdvancingClock()
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(keyword, "Continue", "", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if err != nil {
					t.Fatalf("appearance execute() error = %v", err)
				}
				wantPoint := device.Point{X: 40, Y: 55}
				assertBatch1AActionPoint(t, keyword, driver, wantPoint)
			})
		}
	})

	t.Run("ambiguous stability refresh retains latest unique bounds", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		initial := device.Bounds{X: 50, Y: 60, Width: 20, Height: 20}
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: tapTree(initial)}, {Value: batch1ADuplicateTree(initial)}, {Value: tapTree(initial)},
		}})
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "Continue", "", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if err != nil {
			t.Fatalf("ambiguous refresh execute() error = %v", err)
		}
		assertBatch1AActionPoint(t, model.CommandLongPressOn, driver, device.Point{X: 60, Y: 70})
	})

	t.Run("stability deadline uses latest refreshable bounds", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		descriptors := []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 0, Y: 20, Width: 20, Height: 20})}}
		for index := 1; index <= 30; index++ {
			descriptors = append(descriptors, enginetest.Result[device.TreeNode]{Value: tapTree(device.Bounds{X: index, Y: 20, Width: 20, Height: 20})})
		}
		driver.Enqueue(enginetest.DriverScript{ContentDescriptor: descriptors})
		clock := &batch1ATraceClock{now: time.Unix(900, 0)}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandDoubleTapOn, "Continue", "", nil, nil, intPointerForTap(0), intPointerForTap(0)), nil, driver, clock, lookup)
		if err != nil {
			t.Fatalf("stability deadline execute() error = %v", err)
		}
		if got := sumBatch1ADurations(clock.waits); got != 3*time.Second {
			t.Fatalf("stability waits = %v, want 3s", got)
		}
		assertBatch1AActionPoint(t, model.CommandDoubleTapOn, driver, device.Point{X: 40, Y: 30})
	})

	t.Run("terminal lookup and hierarchy errors preserve class and cause", func(t *testing.T) {
		for _, test := range []struct {
			name  string
			err   error
			class terminalErrorClass
		}{
			{name: "connection", err: NewDeviceConnectionError("descriptor disconnected", nil), class: terminalErrorDeviceConnection},
			{name: "configuration", err: NewConfigurationError("hierarchy unavailable", nil), class: terminalErrorConfiguration},
		} {
			t.Run(test.name, func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: test.err}}})
				clock := newAdvancingClock()
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "Continue", "", boolPointer(true), nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if !errors.Is(err, test.err) || classifyTerminalError(err) != test.class || ClassifyOutcome(err, true) != Failed {
					t.Fatalf("terminal error = %T %v class %v outcome %q", err, err, classifyTerminalError(err), ClassifyOutcome(err, true))
				}
				assertBatch1AZeroPhysicalAndSettle(t, driver)
			})
		}
	})

	t.Run("lookup and stability cancellation have exact cutoff", func(t *testing.T) {
		for _, phase := range []string{"lookup", "stability"} {
			t.Run(phase, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				clock := &batch1ACancelWaitClock{now: time.Unix(950, 0), cancel: cancel}
				driver := batch1APointDriver(400, 884)
				if phase == "stability" {
					driver.Enqueue(enginetest.DriverScript{ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})}}})
				}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(ctx, batch1ACommand(model.CommandDoubleTapOn, "Continue", "", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if !errors.Is(err, context.Canceled) || classifyTerminalError(err) != terminalErrorCancelled {
					t.Fatalf("%s cancellation = %T %v", phase, err, err)
				}
				assertBatch1AZeroPhysicalAndSettle(t, driver)
			})
		}
	})
}

func TestInteractionBatch1AStrictAuthoredFormsAndSettlePrecedence(t *testing.T) {
	t.Parallel()

	t.Run("string fractional overflow and unknown authored fields reject before effects", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			for _, field := range []string{"delay", "duration", "waitToSettleTimeoutMs", "unknown"} {
				for _, value := range []any{"1", 1.5, int64(^uint64(0) >> 1)} {
					name := string(keyword) + "/" + field
					t.Run(name, func(t *testing.T) {
						command := batch1ACommand(keyword, "", "20,30", nil, nil, nil, nil)
						command.Arguments.(map[string]any)[field] = value
						driver := batch1APointDriver(400, 884)
						clock := newAdvancingClock()
						lookup := NewElementLookup(driver, clock)
						_, err := executeBatch1AForTest(context.Background(), command, nil, driver, clock, lookup)
						if !isConfigurationError(err) {
							t.Fatalf("authored %s=%#v error = %T %v, want ConfigurationError", field, value, err, err)
						}
						assertBatch1AZeroPhysicalAndSettle(t, driver)
					})
				}
			}
		}
	})

	t.Run("omitted settle makes exactly ten attempts", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			t.Run(string(keyword), func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				clock := &batch1ATraceClock{now: time.Unix(1000, 0)}
				lookup := NewElementLookup(driver, clock)
				var delay *int
				if keyword == model.CommandDoubleTapOn {
					delay = intPointerForTap(0)
				}
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(keyword, "", "20,30", nil, nil, delay, nil), nil, driver, clock, lookup)
				if err != nil {
					t.Fatalf("omitted settle execute() error = %v", err)
				}
				if got := len(settleRequests(driver.Actions())); got != HierarchySettleAttempts {
					t.Fatalf("omitted settle calls = %d, want %d", got, HierarchySettleAttempts)
				}
				wantWait := time.Duration(HierarchySettleAttempts) * HierarchySettlePollInterval
				if got := sumBatch1ADurations(clock.waits); got != wantWait {
					t.Fatalf("omitted settle waits = %v, want %v", got, wantWait)
				}
			})
		}
	})

	t.Run("explicit zero and boundary settle preserve exact requests", func(t *testing.T) {
		for _, timeout := range []int{0, 30000} {
			t.Run((time.Duration(timeout) * time.Millisecond).String(), func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				if timeout == 30000 {
					ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
					driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}}})
				}
				clock := &batch1ATraceClock{now: time.Unix(1050, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, nil, intPointerForTap(timeout)), nil, driver, clock, lookup)
				if err != nil {
					t.Fatalf("explicit settle execute() error = %v", err)
				}
				requests := settleRequests(driver.Actions())
				wantCalls := 1
				if timeout == 30000 {
					wantCalls = 2
				}
				if len(requests) != wantCalls {
					t.Fatalf("settle requests = %d, want %d", len(requests), wantCalls)
				}
				for _, request := range requests {
					if request.TimeoutMillis == nil || *request.TimeoutMillis != int64(timeout) {
						t.Fatalf("settle request = %#v, want timeout %d", request, timeout)
					}
				}
				wantWait := time.Duration(0)
				if timeout == 30000 {
					wantWait = 2 * HierarchySettlePollInterval
				}
				if got := sumBatch1ADurations(clock.waits); got != wantWait {
					t.Fatalf("settle wait trace = %v, want %v", got, wantWait)
				}
			})
		}
	})

	t.Run("settle cancellation and configuration outrank optional policy", func(t *testing.T) {
		configuration := NewConfigurationError("settle configuration", nil)
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: configuration}}})
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", boolPointer(true), nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if !errors.Is(err, configuration) || classifyTerminalError(err) != terminalErrorConfiguration || ClassifyOutcome(err, true) != Failed {
			t.Fatalf("configuration settle = %T %v class %v outcome %q", err, err, classifyTerminalError(err), ClassifyOutcome(err, true))
		}

		ctx, cancel := context.WithCancel(context.Background())
		base := batch1APointDriver(400, 884)
		cancelDriver := &batch1ACancelSettleDriver{Driver: base, cancel: cancel}
		cancelClock := newAdvancingClock()
		cancelLookup := NewElementLookup(cancelDriver, cancelClock)
		_, err = executeBatch1AForTest(ctx, batch1ACommand(model.CommandDoubleTapOn, "", "20,30", boolPointer(true), nil, intPointerForTap(0), intPointerForTap(0)), nil, cancelDriver, cancelClock, cancelLookup)
		if !errors.Is(err, context.Canceled) || classifyTerminalError(err) != terminalErrorCancelled || ClassifyOutcome(err, true) != Cancelled {
			t.Fatalf("cancel settle = %T %v class %v outcome %q", err, err, classifyTerminalError(err), ClassifyOutcome(err, true))
		}
	})
}

func TestInteractionBatch1AExactActionTimingWatermarkAndSettle(t *testing.T) {
	t.Parallel()

	t.Run("double tap default delay and one settle", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		clock := &batch1ATraceClock{now: time.Unix(100, 0)}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if err != nil {
			t.Fatalf("execute() error = %v", err)
		}
		if !reflect.DeepEqual(clock.waits, []time.Duration{100 * time.Millisecond}) {
			t.Fatalf("Clock waits = %#v, want exactly one 100ms inter-tap wait", clock.waits)
		}
		if got := batch1ARelevantMethods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodTap, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle}) {
			t.Fatalf("method order = %#v", got)
		}
		if got := len(settleRequests(driver.Actions())); got != 1 {
			t.Fatalf("settle calls = %d, want one", got)
		}
		if got := lookup.AdjustedTimeout(LookupOptions{Optional: true}); got != OptionalLookupTimeout {
			t.Fatalf("watermark-adjusted timeout = %v, want %v at latest successful tap", got, OptionalLookupTimeout)
		}
	})

	t.Run("authored double tap delay reaches injected clock exactly", func(t *testing.T) {
		for _, delayMillis := range []int{0, 275, 30000} {
			t.Run((time.Duration(delayMillis) * time.Millisecond).String(), func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				clock := &batch1ATraceClock{now: time.Unix(150, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(
					context.Background(),
					batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, intPointerForTap(delayMillis), intPointerForTap(0)),
					nil, driver, clock, lookup,
				)
				if err != nil {
					t.Fatalf("execute() error = %v", err)
				}
				want := []time.Duration{time.Duration(delayMillis) * time.Millisecond}
				if !reflect.DeepEqual(clock.waits, want) {
					t.Fatalf("Clock waits = %#v, want exact authored delay %#v", clock.waits, want)
				}
			})
		}
	})

	t.Run("long press exact duration and one settle", func(t *testing.T) {
		driver := batch1APointDriver(400, 884)
		clock := &batch1ATraceClock{now: time.Unix(200, 0)}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if err != nil {
			t.Fatalf("execute() error = %v", err)
		}
		want := []device.LongPressRequest{{Point: device.Point{X: 20, Y: 30}, DurationMillis: 3000}}
		if got := batch1ALongPressRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
			t.Fatalf("LongPress requests = %#v, want %#v", got, want)
		}
		if len(clock.waits) != 0 || len(settleRequests(driver.Actions())) != 1 {
			t.Fatalf("waits/settles = %#v/%d, want none/one", clock.waits, len(settleRequests(driver.Actions())))
		}
		if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
			t.Fatalf("watermark-adjusted timeout = %v, want %v", got, LookupTimeout)
		}
	})

	t.Run("explicit settle timeout is preserved in one request", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			t.Run(string(keyword), func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				clock := &batch1ATraceClock{now: time.Unix(250, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(
					context.Background(),
					batch1ACommand(keyword, "", "20,30", nil, nil, nil, intPointerForTap(1)),
					nil, driver, clock, lookup,
				)
				if err != nil {
					t.Fatalf("execute() error = %v", err)
				}
				requests := settleRequests(driver.Actions())
				if len(requests) != 1 || requests[0].TimeoutMillis == nil || *requests[0].TimeoutMillis != 1 {
					t.Fatalf("settle requests = %#v, want one request preserving 1ms", requests)
				}
			})
		}
	})
}

func TestInteractionBatch1AFailureCancellationAndOptionalCutoffs(t *testing.T) {
	t.Parallel()

	t.Run("pre-cancel produces zero action", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			t.Run(string(keyword), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				driver := batch1APointDriver(400, 884)
				clock := &batch1ATraceClock{now: time.Unix(275, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(ctx, batch1ACommand(keyword, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if !errors.Is(err, context.Canceled) || len(driver.Actions()) != 0 || len(clock.waits) != 0 {
					t.Fatalf("execute() = %v actions %#v waits %#v, want cancellation with zero action", err, driver.Actions(), clock.waits)
				}
			})
		}
	})

	t.Run("first tap failure prevents second and settle", func(t *testing.T) {
		failure := errors.New("first tap failed")
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{Tap: []enginetest.Result[struct{}]{{Err: failure}}})
		clock := &batch1ATraceClock{now: time.Unix(300, 0)}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if !errors.Is(err, failure) || len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 || len(clock.waits) != 0 {
			t.Fatalf("result = %v taps=%d settles=%d waits=%#v", err, len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())), clock.waits)
		}
	})

	t.Run("long press driver failure preserves watermark and prevents settle", func(t *testing.T) {
		failure := errors.New("long press failed")
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{LongPress: []enginetest.Result[struct{}]{{Err: failure}}})
		clock := &batch1ATraceClock{now: time.Unix(350, 0)}
		lookup := NewElementLookup(driver, clock)
		lookup.RecordInteraction(clock.now.Add(-time.Second))
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if !errors.Is(err, failure) || len(batch1ALongPressRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 || len(clock.waits) != 0 {
			t.Fatalf("result = %v longPress=%d settles=%d waits=%#v", err, len(batch1ALongPressRequests(driver.Actions())), len(settleRequests(driver.Actions())), clock.waits)
		}
		if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout-time.Second {
			t.Fatalf("watermark moved after failed long press: adjusted timeout = %v, want %v", got, LookupTimeout-time.Second)
		}
	})

	t.Run("second tap failure preserves first watermark and prevents settle", func(t *testing.T) {
		failure := errors.New("second tap failed")
		driver := batch1APointDriver(400, 884)
		driver.Enqueue(enginetest.DriverScript{Tap: []enginetest.Result[struct{}]{{}, {Err: failure}}})
		clock := &batch1ATraceClock{now: time.Unix(400, 0)}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, intPointerForTap(10), intPointerForTap(0)), nil, driver, clock, lookup)
		if !errors.Is(err, failure) || len(tapRequests(driver.Actions())) != 2 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("result = %v taps=%d settles=%d", err, len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())))
		}
		if got := lookup.AdjustedTimeout(LookupOptions{Optional: true}); got != OptionalLookupTimeout-10*time.Millisecond {
			t.Fatalf("first watermark after second failure gives timeout %v", got)
		}
	})

	t.Run("cancellation during inter-tap wait cuts off second", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		driver := batch1APointDriver(400, 884)
		clock := &batch1ATraceClock{now: time.Unix(500, 0), cancel: cancel, cancelWait: 1}
		lookup := NewElementLookup(driver, clock)
		_, err := executeBatch1AForTest(ctx, batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
		if !errors.Is(err, context.Canceled) || len(tapRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("result = %v taps=%d settles=%d", err, len(tapRequests(driver.Actions())), len(settleRequests(driver.Actions())))
		}
	})

	t.Run("context-ignoring successful action records watermark before cancellation cutoff", func(t *testing.T) {
		for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
			t.Run(string(keyword), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				base := batch1APointDriver(400, 884)
				driver := &batch1ACancelAfterActionDriver{Driver: base, cancel: cancel}
				clock := &batch1ATraceClock{now: time.Unix(550, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(ctx, batch1ACommand(keyword, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if !errors.Is(err, context.Canceled) || len(settleRequests(base.Actions())) != 0 || len(clock.waits) != 0 {
					t.Fatalf("execute() = %v actions %#v waits %#v", err, base.Actions(), clock.waits)
				}
				if got := lookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
					t.Fatalf("watermark-adjusted timeout = %v, want %v", got, LookupTimeout)
				}
				if keyword == model.CommandDoubleTapOn && len(tapRequests(base.Actions())) != 1 {
					t.Fatalf("tap actions = %#v, want first successful tap only", base.Actions())
				}
				if keyword == model.CommandLongPressOn && len(batch1ALongPressRequests(base.Actions())) != 1 {
					t.Fatalf("long-press actions = %#v, want one successful action", base.Actions())
				}
			})
		}
	})

	t.Run("optional miss warns while terminal lookup failure stays failed", func(t *testing.T) {
		optional := true
		missingDriver := batch1APointDriver(400, 884)
		missingClock := newAdvancingClock()
		missingLookup := NewElementLookup(missingDriver, missingClock)
		_, missingErr := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "Missing", "", &optional, nil, nil, intPointerForTap(0)), nil, missingDriver, missingClock, missingLookup)
		if ClassifyOutcome(missingErr, true) != Warned || len(batch1ALongPressRequests(missingDriver.Actions())) != 0 {
			t.Fatalf("optional miss = %T %v outcome %q actions %#v", missingErr, missingErr, ClassifyOutcome(missingErr, true), missingDriver.Actions())
		}

		terminal := NewDeviceConnectionError("device info failed", nil)
		terminalDriver := enginetest.NewFakeDriver()
		terminalDriver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: terminal}}})
		terminalClock := newAdvancingClock()
		terminalLookup := NewElementLookup(terminalDriver, terminalClock)
		_, terminalErr := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", &optional, nil, nil, intPointerForTap(0)), nil, terminalDriver, terminalClock, terminalLookup)
		if !errors.Is(terminalErr, terminal) || ClassifyOutcome(terminalErr, true) != Failed {
			t.Fatalf("optional terminal = %T %v outcome %q", terminalErr, terminalErr, ClassifyOutcome(terminalErr, true))
		}
	})

	t.Run("ordinary settle is ignored and terminal settle propagates", func(t *testing.T) {
		for _, test := range []struct {
			name string
			err  error
			want error
		}{
			{name: "ordinary", err: NewOperationError("not settled", nil)},
			{name: "terminal", err: NewDeviceConnectionError("settle disconnected", nil), want: NewDeviceConnectionError("settle disconnected", nil)},
		} {
			t.Run(test.name, func(t *testing.T) {
				driver := batch1APointDriver(400, 884)
				driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Err: test.err}}})
				clock := &batch1ATraceClock{now: time.Unix(600, 0)}
				lookup := NewElementLookup(driver, clock)
				_, err := executeBatch1AForTest(context.Background(), batch1ACommand(model.CommandLongPressOn, "", "20,30", nil, nil, nil, intPointerForTap(0)), nil, driver, clock, lookup)
				if test.want == nil && err != nil {
					t.Fatalf("ordinary settle error = %v, want ignored", err)
				}
				if test.want != nil && classifyTerminalError(err) != terminalErrorDeviceConnection {
					t.Fatalf("terminal settle error = %T %v, want DeviceConnectionError", err, err)
				}
			})
		}
	})
}

func TestInteractionBatch1ACompiledOwnershipAndConcurrentReuse(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	dispatcher := newDispatcher(registry)
	for _, keyword := range []model.CommandKeyword{model.CommandDoubleTapOn, model.CommandLongPressOn} {
		t.Run(string(keyword), func(t *testing.T) {
			optional := true
			label := "owned"
			delay := 7
			settle := 9
			source := batch1ACommand(keyword, "", "20,30", &optional, &label, nil, &settle)
			if keyword == model.CommandDoubleTapOn {
				source.Selector.Delay = &delay
				source.Arguments.(map[string]any)["delay"] = int64(delay)
			}
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, source)
			if compileErr != nil {
				t.Fatalf("compile() error = %v", compileErr)
			}

			*source.Selector.Point = "300,400"
			source.Arguments.(map[string]any)["point"] = "300,400"
			*source.Label = "mutated"
			*source.Selector.Label = "mutated"
			*source.Optional = false
			*source.Selector.Optional = false
			*source.Selector.WaitToSettleTimeoutMS = 30000
			source.Arguments.(map[string]any)["waitToSettleTimeoutMs"] = int64(30000)
			if source.Selector.Delay != nil {
				*source.Selector.Delay = 30000
				source.Arguments.(map[string]any)["delay"] = int64(30000)
			}
			source.Arguments.(map[string]any)["caller"] = map[string]any{"mutated": true}

			const executions = 16
			var group sync.WaitGroup
			errorsSeen := make(chan error, executions)
			for range executions {
				group.Add(1)
				go func() {
					defer group.Done()
					base := batch1APointDriver(400, 884)
					driver := &batch1AMutatingDriver{Driver: base}
					clock := newAdvancingClock()
					lookup := NewElementLookup(driver, clock)
					evaluated, evaluateErr := dispatcher.evaluate(context.Background(), batch1AEvaluation(nil), compiled)
					if evaluateErr != nil {
						errorsSeen <- evaluateErr
						return
					}
					state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
					_, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated)
					if executeErr != nil {
						errorsSeen <- executeErr
						return
					}
					points, durations, settles := driver.Snapshot()
					wantPoints := []device.Point{{X: 20, Y: 30}}
					wantDurations := []int64(nil)
					if keyword == model.CommandDoubleTapOn {
						wantPoints = []device.Point{{X: 20, Y: 30}, {X: 20, Y: 30}}
					} else {
						wantDurations = []int64{3000}
					}
					if !reflect.DeepEqual(points, wantPoints) || !reflect.DeepEqual(durations, wantDurations) || !reflect.DeepEqual(settles, []int64{9}) {
						errorsSeen <- errors.New("compiled execution observed source mutation or a Driver mutation escaped into reusable state")
						return
					}
					evaluatedCommand, exists := evaluated.command.Selector, evaluated.command.Label != nil && evaluated.command.Optional != nil
					if !exists || evaluatedCommand == nil || evaluatedCommand.Point == nil || *evaluatedCommand.Point != "20,30" ||
						*evaluated.command.Label != "owned" || !*evaluated.command.Optional || evaluatedCommand.WaitToSettleTimeoutMS == nil || *evaluatedCommand.WaitToSettleTimeoutMS != 9 {
						errorsSeen <- errors.New("evaluated metadata observed post-compile source mutation")
					}
				}()
			}
			group.Wait()
			close(errorsSeen)
			for executionErr := range errorsSeen {
				t.Fatal(executionErr)
			}
		})
	}
}

func executeBatch1AForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
) (commandEffect, error) {
	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec())
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
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
	}
	return dispatcher.execute(ctx, state, compiled, evaluated)
}

func batch1AEvaluation(replacements map[string]string) evaluationContext {
	return evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			for name, value := range replacements {
				input = strings.ReplaceAll(input, "${"+name+"}", value)
			}
			return input, nil
		},
		activeConfig: model.Config{AppID: "com.example.batch1a"}, hasActiveConfig: true,
	}
}

func batch1ACommand(
	keyword model.CommandKeyword,
	text string,
	point string,
	optional *bool,
	label *string,
	delay *int,
	settle *int,
) model.Command {
	selector := &model.ElementSelector{}
	arguments := make(map[string]any)
	if text != "" {
		selector.TextRegex = stringPointer(text)
		arguments["text"] = text
	}
	if point != "" {
		selector.Point = stringPointer(point)
		arguments["point"] = point
	}
	if optional != nil {
		selector.Optional = clonePointer(optional)
		arguments["optional"] = *optional
	}
	if label != nil {
		selector.Label = clonePointer(label)
		arguments["label"] = *label
	}
	if delay != nil {
		selector.Delay = clonePointer(delay)
		arguments["delay"] = int64(*delay)
	}
	if settle != nil {
		selector.WaitToSettleTimeoutMS = clonePointer(settle)
		arguments["waitToSettleTimeoutMs"] = int64(*settle)
	}
	return model.Command{
		Kind: keyword, Form: model.CommandFormObject, Arguments: arguments, Selector: selector,
		Optional: clonePointer(optional), Label: clonePointer(label),
	}
}

func batch1ACommandWithUnsupported(keyword model.CommandKeyword, field string) model.Command {
	command := batch1ACommand(keyword, "", "20,30", nil, nil, nil, nil)
	switch field {
	case "repeat":
		command.Selector.Repeat = intPointerForTap(2)
		command.Arguments.(map[string]any)[field] = int64(2)
	case "retryTapIfNoChange":
		command.Selector.RetryTapIfNoChange = boolPointer(true)
		command.Arguments.(map[string]any)[field] = true
	case "waitUntilVisible":
		command.Selector.WaitUntilVisible = boolPointer(true)
		command.Arguments.(map[string]any)[field] = true
	default:
		command.Arguments.(map[string]any)[field] = int64(3000)
	}
	return command
}

func sortedHandlerKeywords(registry handlerRegistry) []string {
	keys := make([]string, 0, len(registry.byKeyword))
	for keyword := range registry.byKeyword {
		keys = append(keys, string(keyword))
	}
	sort.Strings(keys)
	return keys
}

func batch1APointDriver(width, height int) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: width, HeightGrid: height}}}})
	return driver
}

func batch1ASelectorDriver(bounds ...device.Bounds) *enginetest.FakeDriver {
	driver := batch1APointDriver(400, 884)
	results := make([]enginetest.Result[device.TreeNode], len(bounds))
	for index, value := range bounds {
		results[index].Value = tapTree(value)
	}
	driver.Enqueue(enginetest.DriverScript{ContentDescriptor: results})
	return driver
}

func batch1ALongPressRequests(actions []enginetest.Action) []device.LongPressRequest {
	requests := make([]device.LongPressRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodLongPress {
			requests = append(requests, action.Request.(device.LongPressRequest))
		}
	}
	return requests
}

func batch1ARelevantMethods(actions []enginetest.Action) []enginetest.Method {
	methods := make([]enginetest.Method, 0, len(actions))
	for _, action := range actions {
		switch action.Method {
		case enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodTap, enginetest.MethodLongPress, enginetest.MethodWaitForAppToSettle:
			methods = append(methods, action.Method)
		}
	}
	return methods
}

func batch1AEmptyTree() device.TreeNode {
	return device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}}
}

func batch1ADuplicateTree(bounds device.Bounds) device.TreeNode {
	boundsText := "[" + intString(bounds.X) + "," + intString(bounds.Y) + "][" +
		intString(bounds.X+bounds.Width) + "," + intString(bounds.Y+bounds.Height) + "]"
	child := device.TreeNode{Attributes: map[string]string{
		"text": "Continue", "bounds": boundsText,
	}}
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][400,884]"},
		Children:   []device.TreeNode{child, child},
	}
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func assertBatch1AZeroPhysicalAndSettle(t testing.TB, driver *enginetest.FakeDriver) {
	t.Helper()
	if len(tapRequests(driver.Actions())) != 0 || len(batch1ALongPressRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("pre-effect failure caused physical/settle effects: %#v", driver.Actions())
	}
}

func assertBatch1AActionPoint(t testing.TB, keyword model.CommandKeyword, driver *enginetest.FakeDriver, want device.Point) {
	t.Helper()
	switch keyword {
	case model.CommandDoubleTapOn:
		requests := tapRequests(driver.Actions())
		if len(requests) != 2 || requests[0].Point != want || requests[1].Point != want {
			t.Fatalf("double tap points = %#v, want two at %+v", requests, want)
		}
	case model.CommandLongPressOn:
		requests := batch1ALongPressRequests(driver.Actions())
		if len(requests) != 1 || requests[0].Point != want || requests[0].DurationMillis != 3000 {
			t.Fatalf("long press requests = %#v, want one at %+v/3000ms", requests, want)
		}
	default:
		t.Fatalf("unsupported Batch 1A keyword %q", keyword)
	}
}

func sumBatch1ADurations(values []time.Duration) time.Duration {
	var total time.Duration
	for _, value := range values {
		total += value
	}
	return total
}

type batch1ATraceClock struct {
	now        time.Time
	waits      []time.Duration
	cancel     context.CancelFunc
	cancelWait int
}

func (clock *batch1ATraceClock) Now() time.Time { return clock.now }

func (clock *batch1ATraceClock) Wait(ctx context.Context, delay time.Duration) error {
	clock.waits = append(clock.waits, delay)
	if clock.cancel != nil && len(clock.waits) == clock.cancelWait {
		clock.cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.now = clock.now.Add(delay)
	return nil
}

func boolPointer(value bool) *bool { return &value }

type batch1ACancelWaitClock struct {
	now    time.Time
	cancel context.CancelFunc
}

func (clock *batch1ACancelWaitClock) Now() time.Time { return clock.now }

func (clock *batch1ACancelWaitClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.now = clock.now.Add(delay)
	clock.cancel()
	return ctx.Err()
}

type batch1ACancelSettleDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch1ACancelSettleDriver) WaitForAppToSettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	driver.cancel()
	return nil, errors.New("ignored settle error")
}

type batch1AMutatingDriver struct {
	device.Driver
	mu        sync.Mutex
	points    []device.Point
	durations []int64
	settles   []int64
}

func (driver *batch1AMutatingDriver) Tap(ctx context.Context, request device.TapRequest) error {
	driver.mu.Lock()
	driver.points = append(driver.points, request.Point)
	driver.mu.Unlock()
	request.Point = device.Point{X: 999, Y: 999}
	return driver.Driver.Tap(ctx, request)
}

func (driver *batch1AMutatingDriver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	driver.mu.Lock()
	driver.points = append(driver.points, request.Point)
	driver.durations = append(driver.durations, request.DurationMillis)
	driver.mu.Unlock()
	request.Point = device.Point{X: 999, Y: 999}
	request.DurationMillis = 1
	return driver.Driver.LongPress(ctx, request)
}

func (driver *batch1AMutatingDriver) WaitForAppToSettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	driver.mu.Lock()
	if request.TimeoutMillis != nil {
		driver.settles = append(driver.settles, *request.TimeoutMillis)
		*request.TimeoutMillis = 999
	}
	driver.mu.Unlock()
	return driver.Driver.WaitForAppToSettle(ctx, request)
}

func (driver *batch1AMutatingDriver) Snapshot() ([]device.Point, []int64, []int64) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]device.Point(nil), driver.points...), append([]int64(nil), driver.durations...), append([]int64(nil), driver.settles...)
}

type batch1ACancelAfterActionDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch1ACancelAfterActionDriver) Tap(ctx context.Context, request device.TapRequest) error {
	err := driver.Driver.Tap(ctx, request)
	driver.cancel()
	return err
}

func (driver *batch1ACancelAfterActionDriver) LongPress(ctx context.Context, request device.LongPressRequest) error {
	err := driver.Driver.LongPress(ctx, request)
	driver.cancel()
	return err
}
