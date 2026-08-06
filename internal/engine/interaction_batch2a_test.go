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

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch2APrivateRegistryAndCompileContract(t *testing.T) {
	t.Parallel()

	direct, err := newHandlerRegistry(
		doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec(),
		backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	wantDirect := []string{"back", "doubleTapOn", "hideKeyboard", "longPressOn", "pressKey", "scroll", "swipe"}
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
	for _, keyword := range []model.CommandKeyword{model.CommandBack, model.CommandHideKeyboard, model.CommandScroll, model.CommandPressKey} {
		spec, ok := direct.lookup(keyword)
		if !ok || spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle || spec.settleRequest != nil {
			t.Fatalf("%s spec = %#v, want device mutation/no-settle/nil factory", keyword, spec)
		}
	}

	for _, keyword := range []model.CommandKeyword{model.CommandBack, model.CommandHideKeyboard, model.CommandScroll} {
		if _, err := compileBatch2ADirect(batch2ABareCommand(keyword)); err != nil {
			t.Fatalf("compile %s bare error = %v", keyword, err)
		}
		invalid := []model.Command{
			{Kind: keyword, Form: model.CommandFormObject, Arguments: "value"},
			{Kind: keyword, Form: model.CommandFormObject, Arguments: int64(1)},
			{Kind: keyword, Form: model.CommandFormObject, Arguments: map[string]any{"extra": true}},
			{Kind: keyword, Form: model.CommandFormScalar, Arguments: "hidden"},
			{Kind: keyword, Form: model.CommandFormScalar, Children: []model.Command{batch2ABareCommand(model.CommandBack)}},
		}
		for _, command := range invalid {
			if _, err := compileBatch2ADirect(command); !isConfigurationError(err) {
				t.Fatalf("compile %s invalid %#v error = %T %v, want ConfigurationError", keyword, command, err, err)
			}
		}
	}

	accepted := map[string]device.KeyCode{
		"ENTER": "ENTER", "enter": "ENTER", "Enter": "ENTER", "BACK": "BACK", "back": "BACK",
		"HOME": "HOME", "home": "HOME", "Home": "HOME", "LOCK": "LOCK", "lock": "LOCK",
		"VOLUME_UP": "VOLUME_UP", "volume up": "VOLUME_UP", "VOLUME UP": "VOLUME_UP",
		"VOLUME_DOWN": "VOLUME_DOWN", "volume down": "VOLUME_DOWN",
		"POWER": "POWER", "power": "POWER", "backspace": "BACKSPACE", "TAB": "TAB",
		"Remote Dpad Up": "REMOTE_DPAD_UP", "remote_dpad_up": "REMOTE_DPAD_UP",
		"Remote Media Play Pause": "REMOTE_MEDIA_PLAY_PAUSE", "TV Input HDMI 1": "TV_INPUT_HDMI_1",
	}
	for authored, canonical := range accepted {
		compiled, err := compilePressKey(batch2APressKeyCommand(authored))
		if err != nil {
			t.Fatalf("compilePressKey(%q) error = %v", authored, err)
		}
		plan := compiled.(pressKeyCompiled)
		if plan.canonical != canonical || plan.requiresEvaluation {
			t.Fatalf("compilePressKey(%q) = %#v, want canonical %q", authored, plan, canonical)
		}
	}
	if compiled, err := compilePressKey(batch2APressKeyCommand("${KEY}")); err != nil || !compiled.(pressKeyCompiled).requiresEvaluation {
		t.Fatalf("dynamic pressKey compile = %#v, %v", compiled, err)
	}
	for _, invalid := range []any{"", " ", "unknown", "REMOTE_UP", "REMOTE", "TV Input HDMI 4", int64(1), true, map[string]any{"key": "HOME"}} {
		command := model.Command{Kind: model.CommandPressKey, Form: model.CommandFormObject, Arguments: invalid}
		if _, err := compilePressKey(command); !isConfigurationError(err) {
			t.Fatalf("compilePressKey(%#v) error = %T %v, want ConfigurationError", invalid, err, err)
		}
	}
}

func TestDispatcherRejectsForgedTypedGraphFields(t *testing.T) {
	t.Parallel()

	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "foreign.yaml", ResolvedPath: "/workspace/foreign.yaml",
	}
	tests := []struct {
		name    string
		command model.Command
	}{
		{name: "back condition", command: model.Command{Kind: model.CommandBack, Form: model.CommandFormScalar, Condition: &model.Condition{}}},
		{name: "back link", command: model.Command{Kind: model.CommandBack, Form: model.CommandFormScalar, Links: []model.FileLink{link}}},
		{name: "hideKeyboard condition", command: model.Command{Kind: model.CommandHideKeyboard, Form: model.CommandFormScalar, Condition: &model.Condition{}}},
		{name: "hideKeyboard link", command: model.Command{Kind: model.CommandHideKeyboard, Form: model.CommandFormScalar, Links: []model.FileLink{link}}},
		{name: "scroll condition", command: model.Command{Kind: model.CommandScroll, Form: model.CommandFormScalar, Condition: &model.Condition{}}},
		{name: "scroll link", command: model.Command{Kind: model.CommandScroll, Form: model.CommandFormScalar, Links: []model.FileLink{link}}},
		{name: "pressKey condition", command: model.Command{Kind: model.CommandPressKey, Form: model.CommandFormObject, Arguments: "HOME", Condition: &model.Condition{}}},
		{name: "pressKey link", command: model.Command{Kind: model.CommandPressKey, Form: model.CommandFormObject, Arguments: "HOME", Links: []model.FileLink{link}}},
	}
	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled, compileErr := dispatcher.compile(
				context.Background(),
				compileContext{containingFlow: "/workspace/typed-graph-dispatch.yaml"},
				test.command,
			)
			if compiled.command.Kind != "" || !isConfigurationError(compileErr) {
				t.Fatalf("dispatcher.compile(%#v) = %#v, %T %v; want zero dispatch and ConfigurationError", test.command, compiled, compileErr, compileErr)
			}
		})
	}
}

func TestMalformedSelectedRootsHaveWholeProgramZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	valid := func(flowPath string) model.Flow {
		return model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: flowPath,
			Config:   model.Config{AppID: "com.example.typedgraph.valid"},
			Commands: []model.Command{batch2ABareCommand(model.CommandScroll)},
		}
	}
	malformed := func(flowPath string) model.Flow {
		return model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: flowPath,
			Config: model.Config{AppID: "com.example.typedgraph.malformed"},
			Commands: []model.Command{{
				Kind: model.CommandScroll, Form: model.CommandFormScalar, Condition: &model.Condition{},
			}},
		}
	}
	for _, test := range []struct {
		name  string
		flows []model.Flow
	}{
		{name: "malformed first selected root", flows: []model.Flow{malformed("/workspace/typed-graph-invalid-first.yaml"), valid("/workspace/typed-graph-valid-later.yaml")}},
		{name: "malformed later selected root", flows: []model.Flow{valid("/workspace/typed-graph-valid-first.yaml"), malformed("/workspace/typed-graph-invalid-later.yaml")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
			driver := batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{
				{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
			})
			factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
			controllerCalls := 0
			listenerCalls := 0
			dependencies := Dependencies{
				ExecutionID: "typed-graph-preflight", Driver: driver, Clock: newAdvancingClock(), JSFactory: factory,
				Controller: ControllerFunc(func(context.Context) error { controllerCalls++; return nil }),
				Listeners:  []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			}

			compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(test.flows...), registry)
			results := make([]FlowResult, 0, len(test.flows))
			if compileErr == nil {
				for index, rootPath := range compiled.Roots() {
					root, exists := compiled.Flow(rootPath)
					if !exists {
						compileErr = NewConfigurationError("compiled test root is missing", nil)
						break
					}
					result, executeErr := executeCompiledRootForRun(
						context.Background(), dependencies, root,
						fmt.Sprintf("typed-graph-preflight/root-run-%06d", index+1),
					)
					if result.Path() != "" {
						results = append(results, result)
					}
					if executeErr != nil {
						compileErr = executeErr
						break
					}
				}
			}

			if compiled != nil || !isConfigurationError(compileErr) {
				t.Errorf("whole-program preflight = %#v, %T %v; want nil compiled program and ConfigurationError", compiled, compileErr, compileErr)
			}
			if len(results) != 0 || factory.calls != 0 || controllerCalls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
				t.Errorf(
					"whole-program effects = results %d runtimes %d controllers %d listeners %d driver %#v; want all zero",
					len(results), factory.calls, controllerCalls, listenerCalls, driver.Actions(),
				)
			}
		})
	}
}

func TestInteractionBatch2ABackAndHideKeyboardPlatformGateAndOrder(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandBack, model.CommandHideKeyboard} {
		for _, platform := range []device.Platform{"android", "ios", "web"} {
			t.Run(string(keyword)+"/"+string(platform), func(t *testing.T) {
				driver := batch2ADriver(platform)
				effect, err := executeBatch2AForTest(context.Background(), batch2ABareCommand(keyword), nil, driver, newAdvancingClock())
				if err != nil || effect.effectClass != EffectDeviceMutation {
					t.Fatalf("execute = effect %#v error %v", effect, err)
				}
				actions := driver.Actions()
				physical := enginetest.MethodBackPress
				if keyword == model.CommandHideKeyboard {
					physical = enginetest.MethodHideKeyboard
				}
				if len(actions) < 4 || actions[0].Method != enginetest.MethodDeviceInfo || actions[1].Method != physical {
					t.Fatalf("action prefix = %#v, want DeviceInfo then %s", actions, physical)
				}
				if got := countBatch2AMethod(actions, physical); got != 1 {
					t.Fatalf("%s calls = %d, want one", physical, got)
				}
				settles := settleRequests(actions)
				if len(settles) != 2 || settles[0].AppID != "com.example.batch2a" || settles[1].AppID != "com.example.batch2a" {
					t.Fatalf("settle requests = %#v, want one successful common settle operation for active app", settles)
				}
			})
		}
	}
}

func TestInteractionBatch2APlatformAndDeviceInfoFailuresPrecedePhysicalCalls(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{model.CommandBack, model.CommandHideKeyboard} {
		for _, platform := range []device.Platform{"", "linux", "ANDROID"} {
			driver := batch2ADriver(platform)
			_, err := executeBatch2AForTest(context.Background(), batch2ABareCommand(keyword), nil, driver, newAdvancingClock())
			if !isConfigurationError(err) {
				t.Fatalf("%s platform %q error = %T %v, want ConfigurationError", keyword, platform, err, err)
			}
			if batch2APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("%s platform %q caused effects: %#v", keyword, platform, driver.Actions())
			}
		}
		deviceErr := errors.New("device info failed")
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: deviceErr}}})
		_, err := executeBatch2AForTest(context.Background(), batch2ABareCommand(keyword), nil, driver, newAdvancingClock())
		if !errors.Is(err, deviceErr) || batch2APhysicalCount(driver.Actions()) != 0 {
			t.Fatalf("%s DeviceInfo error = %v actions %#v", keyword, err, driver.Actions())
		}
	}
}

func TestInteractionBatch2AScrollAndPressKeyExactOwnedRequests(t *testing.T) {
	t.Parallel()

	driver := batch2ADriver("android")
	if _, err := executeBatch2AForTest(context.Background(), batch2ABareCommand(model.CommandScroll), nil, driver, newAdvancingClock()); err != nil {
		t.Fatalf("scroll error = %v", err)
	}
	scrolls := batch2AScrollRequests(driver.Actions())
	// Bare scroll moves down through content.
	if !reflect.DeepEqual(scrolls, []device.ScrollVerticalRequest{{Direction: "DOWN", Amount: 0.40, ElementPoint: nil}}) {
		t.Fatalf("scroll requests = %#v", scrolls)
	}

	for authored, canonical := range map[string]device.KeyCode{
		"ENTER": "ENTER", "enter": "ENTER", "BACK": "BACK", "back": "BACK", "HOME": "HOME", "home": "HOME",
		"LOCK": "LOCK", "lock": "LOCK", "VOLUME_UP": "VOLUME_UP", "volume up": "VOLUME_UP",
		"VOLUME_DOWN": "VOLUME_DOWN", "volume down": "VOLUME_DOWN", "POWER": "POWER", "power": "POWER",
	} {
		t.Run(authored, func(t *testing.T) {
			driver := batch2ADriver("android")
			if _, err := executeBatch2AForTest(context.Background(), batch2APressKeyCommand(authored), nil, driver, newAdvancingClock()); err != nil {
				t.Fatalf("pressKey error = %v", err)
			}
			requests := batch2APressKeyRequests(driver.Actions())
			want := []device.PressKeyRequest{{Code: canonical, AppIDs: []string{"com.example.batch2a"}}}
			if !reflect.DeepEqual(requests, want) {
				t.Fatalf("PressKey requests = %#v, want %#v", requests, want)
			}
		})
	}

	dynamic := batch2ADriver("android")
	_, err := executeBatch2AForTest(context.Background(), batch2APressKeyCommand("${KEY}"), map[string]string{"KEY": "volume down"}, dynamic, newAdvancingClock())
	if err != nil {
		t.Fatalf("dynamic pressKey error = %v", err)
	}
	if requests := batch2APressKeyRequests(dynamic.Actions()); len(requests) != 1 || requests[0].Code != "VOLUME_DOWN" {
		t.Fatalf("dynamic requests = %#v", requests)
	}
}

func TestInteractionBatch2APressKeyLateInvalidAndDriverUnsupportedAreExact(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", " ", "unknown", "REMOTE_UP", "REMOTE", "TV Input HDMI 4"} {
		driver := batch2ADriver("android")
		_, err := executeBatch2AForTest(context.Background(), batch2APressKeyCommand("${KEY}"), map[string]string{"KEY": value}, driver, newAdvancingClock())
		if !isConfigurationError(err) || len(batch2APressKeyRequests(driver.Actions())) != 0 {
			t.Fatalf("late key %q error = %T %v actions %#v", value, err, err, driver.Actions())
		}
	}
	unsupported := errors.New("key unsupported by adapter")
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: unsupported}}})
	_, err := executeBatch2AForTest(context.Background(), batch2APressKeyCommand("LOCK"), nil, driver, newAdvancingClock())
	if err != unsupported || reflect.TypeOf(err) != reflect.TypeOf(unsupported) ||
		len(batch2APressKeyRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("direct unsupported key error = %T %v, want exact %T %v; actions %#v", err, err, unsupported, unsupported, driver.Actions())
	}

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch2a-unsupported.yaml"},
		batch2APressKeyCommand("LOCK"),
	)
	if err != nil {
		t.Fatal(err)
	}
	driver = batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: unsupported}}})
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-unsupported", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, &compiledFlow{
		path:   "/workspace/batch2a-unsupported.yaml",
		config: model.Config{AppID: "com.example.batch2a"},
		body:   []compiledDispatch{compiled},
	}, "batch2a-unsupported/root-run-000001")
	commands := result.Commands()
	if runErr != unsupported || reflect.TypeOf(runErr) != reflect.TypeOf(unsupported) ||
		result.ProductError() != unsupported || len(commands) != 1 || commands[0].ProductError() != unsupported ||
		result.Outcome() != Failed || commands[0].Outcome() != Failed {
		t.Fatalf("root unsupported result = %#v commands %#v error %T %v, want exact %T %v", result, commands, runErr, runErr, unsupported, unsupported)
	}
	if len(batch2APressKeyRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("root unsupported actions = %#v, want one PressKey and zero settle actions", driver.Actions())
	}
}

func TestInteractionBatch2APressKeyMalformedDriverErrorIsSanitized(t *testing.T) {
	t.Parallel()

	malformed := &batch2ACyclicDriverError{}
	driver := batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: malformed}}})
	_, err := executeBatch2AForTest(context.Background(), batch2APressKeyCommand("LOCK"), nil, driver, newAdvancingClock())
	if _, ok := err.(*ConfigurationError); !ok || err == malformed {
		t.Fatalf("direct malformed Driver error = %T, want safe non-aliased *ConfigurationError", err)
	}
	if len(batch2APressKeyRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("direct malformed actions = %#v, want one PressKey and zero settle actions", driver.Actions())
	}

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch2a-malformed.yaml"},
		batch2APressKeyCommand("LOCK"),
	)
	if err != nil {
		t.Fatal(err)
	}
	driver = batch2ADriver("ios")
	driver.Enqueue(enginetest.DriverScript{PressKey: []enginetest.Result[struct{}]{{Err: malformed}}})
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-malformed", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, &compiledFlow{
		path:   "/workspace/batch2a-malformed.yaml",
		config: model.Config{AppID: "com.example.batch2a"},
		body:   []compiledDispatch{compiled},
	}, "batch2a-malformed/root-run-000001")
	commands := result.Commands()
	if _, ok := runErr.(*ConfigurationError); !ok || runErr == malformed {
		t.Fatalf("root malformed Driver error = %T, want safe non-aliased *ConfigurationError", runErr)
	}
	if _, ok := result.ProductError().(*ConfigurationError); !ok || result.ProductError() == malformed ||
		len(commands) != 1 {
		t.Fatalf("root malformed result error = %T commands %d, want safe non-aliased *ConfigurationError and one command", result.ProductError(), len(commands))
	}
	if _, ok := commands[0].ProductError().(*ConfigurationError); !ok || commands[0].ProductError() == malformed {
		t.Fatalf("recorded malformed command error = %T, want safe non-aliased *ConfigurationError", commands[0].ProductError())
	}
	if runErr != result.ProductError() || runErr != commands[0].ProductError() ||
		result.Outcome() != Failed || commands[0].Outcome() != Failed || len(events) != 4 {
		t.Fatalf("malformed lifecycle = result %#v commands %#v events %#v error %T, want one shared sanitized failure and four events", result, commands, events, runErr)
	}
	for _, event := range events {
		eventErr := event.ProductError()
		if eventErr == nil {
			continue
		}
		if _, ok := eventErr.(*ConfigurationError); !ok || eventErr == malformed {
			t.Fatalf("%s event malformed error = %T, want safe non-aliased *ConfigurationError", event.Kind(), eventErr)
		}
	}
	if len(batch2APressKeyRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("root malformed actions = %#v, want one PressKey and zero settle actions", driver.Actions())
	}
}

func TestInteractionBatch2ACancellationDriverErrorsAndSettlePrecedence(t *testing.T) {
	t.Parallel()

	for _, command := range []model.Command{
		batch2ABareCommand(model.CommandBack), batch2ABareCommand(model.CommandHideKeyboard),
		batch2ABareCommand(model.CommandScroll), batch2APressKeyCommand("HOME"),
	} {
		t.Run(string(command.Kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			driver := batch2ADriver("android")
			_, err := executeBatch2AForTest(ctx, command, nil, driver, newAdvancingClock())
			if !errors.Is(err, context.Canceled) || batch2APhysicalCount(driver.Actions()) != 0 {
				t.Fatalf("pre-cancel error = %v actions %#v", err, driver.Actions())
			}

			driverErr := errors.New("physical failed")
			driver = batch2ADriver("android")
			driver.Enqueue(batch2AScriptFor(command.Kind, driverErr))
			_, err = executeBatch2AForTest(context.Background(), command, nil, driver, newAdvancingClock())
			if !errors.Is(err, driverErr) || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("driver error = %v actions %#v", err, driver.Actions())
			}

			ordinary := NewOperationError("ordinary settle", nil)
			driver = batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: ordinary}})
			if _, err = executeBatch2AForTest(context.Background(), command, nil, driver, newAdvancingClock()); err != nil {
				t.Fatalf("ordinary settle error = %v, want ignored", err)
			}

			terminal := NewDeviceConnectionError("settle disconnected", nil)
			driver = batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: terminal}})
			_, err = executeBatch2AForTest(context.Background(), command, nil, driver, newAdvancingClock())
			if classifyTerminalError(err) != terminalErrorDeviceConnection {
				t.Fatalf("terminal settle error = %T %v", err, err)
			}

			configuration := NewConfigurationError("settle configuration", nil)
			driver = batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: configuration}})
			_, err = executeBatch2AForTest(context.Background(), command, nil, driver, newAdvancingClock())
			if classifyTerminalError(err) != terminalErrorConfiguration {
				t.Fatalf("configuration settle error = %T %v", err, err)
			}

			settleCtx, settleCancel := context.WithCancel(context.Background())
			base := batch2ADriver("android")
			cancelSettle := &batch1ACancelSettleDriver{Driver: base, cancel: settleCancel}
			_, err = executeBatch2AForTest(settleCtx, command, nil, cancelSettle, newAdvancingClock())
			if !errors.Is(err, context.Canceled) || classifyTerminalError(err) != terminalErrorCancelled || batch2APhysicalCount(base.Actions()) != 1 {
				t.Fatalf("settle cancellation error = %T %v actions %#v", err, err, base.Actions())
			}
		})
	}
}

func TestInteractionBatch2APostCallCancellationRecordsWatermarkAndCutsOffSettle(t *testing.T) {
	t.Parallel()

	for _, command := range []model.Command{
		batch2ABareCommand(model.CommandBack), batch2ABareCommand(model.CommandHideKeyboard),
		batch2ABareCommand(model.CommandScroll), batch2APressKeyCommand("HOME"),
	} {
		t.Run(string(command.Kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			base := batch2ADriver("android")
			driver := &batch2ACancelAfterActionDriver{Driver: base, cancel: cancel}
			clock := &batch1ATraceClock{now: time.Unix(1700, 0).UTC()}
			lookup := NewElementLookup(driver, clock)
			_, err := executeBatch2AWithLookupForTest(ctx, command, nil, driver, clock, lookup)
			if !errors.Is(err, context.Canceled) || batch2APhysicalCount(base.Actions()) != 1 || len(settleRequests(base.Actions())) != 0 {
				t.Fatalf("post-call cancellation error = %v actions %#v", err, base.Actions())
			}
			clock.now = clock.now.Add(time.Second)
			if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-time.Second; got != want {
				t.Fatalf("watermark-adjusted timeout = %v, want %v", got, want)
			}
		})
	}
}

func TestInteractionBatch2AWholeSequenceStaticInvalidAndDynamicFailureCutOffLaterEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, commands := range [][]model.Command{
		{batch2APressKeyCommand("unknown"), batch2ABareCommand(model.CommandScroll)},
		{batch2ABareCommand(model.CommandScroll), batch2APressKeyCommand("unknown")},
	} {
		if _, err := dispatcher.compileSequence(context.Background(), compileContext{}, commands); !isConfigurationError(err) {
			t.Fatalf("compileSequence(%#v) error = %T %v, want ConfigurationError", commands, err, err)
		}
	}

	commands := []model.Command{
		batch2APressKeyCommand("${KEY}"),
		batch2ABareCommand(model.CommandScroll),
	}
	compiled, err := dispatcher.compileSequence(context.Background(), compileContext{containingFlow: "/workspace/batch2a-cutoff.yaml"}, commands)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch2ADriver("android")
	events := make([]Event, 0, 4)
	root := &compiledFlow{
		path:   "/workspace/batch2a-cutoff.yaml",
		config: model.Config{AppID: "com.example.cutoff", Env: map[string]string{"KEY": "unknown"}},
		body:   compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch2a-cutoff", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch2a-cutoff/root-run-000001")
	if !isConfigurationError(runErr) || result.Outcome() != Failed || len(result.Commands()) != 1 || result.Commands()[0].Outcome() != Failed {
		t.Fatalf("dynamic invalid result = %#v error %T %v", result, runErr, runErr)
	}
	if batch2APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("dynamic invalid or later command caused effects: %#v", driver.Actions())
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Failed {
		t.Fatalf("dynamic failure lifecycle = %#v", events)
	}
}

func TestInteractionBatch2ACompileProgramFirstAndLaterInvalidRootsHaveZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	valid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.valid"}, Commands: []model.Command{
			batch2APressKeyCommand("HOME"),
		}}
	}
	invalid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.invalid"}, Commands: []model.Command{
			batch2APressKeyCommand("unknown"),
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
			_ = Dependencies{
				Driver: driver, JSFactory: factory,
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			}
			compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(test.flows...), registry)
			if compiled != nil || !isConfigurationError(compileErr) {
				t.Fatalf("compileProgram() = %#v, %T %v; want nil ConfigurationError", compiled, compileErr, compileErr)
			}
			if listenerCalls != 0 || factory.calls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf("preflight effects = listeners %d runtimes %d driver %#v", listenerCalls, factory.calls, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch2ACompiledOwnershipAndConcurrentRequestStress(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	source := batch2APressKeyCommand("${KEY}")
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Arguments = "MUTATED"

	const executions = 64
	var group sync.WaitGroup
	errs := make(chan error, executions)
	for index := range executions {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			appID := "com.example.batch2a." + string(rune('a'+index%26))
			evaluation := batch2AEvaluation(map[string]string{"KEY": "HOME"}, appID)
			evaluated, evaluateErr := dispatcher.evaluate(context.Background(), evaluation, compiled)
			if evaluateErr != nil {
				errs <- evaluateErr
				return
			}
			base := batch2ADriver("android")
			driver := &batch2AMutatingPressKeyDriver{Driver: base}
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
			if _, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated); executeErr != nil {
				errs <- executeErr
				return
			}
			request := batch2APressKeyRequests(base.Actions())[0]
			if !reflect.DeepEqual(request.AppIDs, []string{appID}) || request.Code != "HOME" {
				errs <- errors.New("request ownership escaped")
				return
			}
			if evaluated.command.Arguments != "HOME" {
				errs <- errors.New("evaluated command mutated")
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestInteractionBatch2ARepeatedConcurrentRootLifecycleIsolation(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch2a-concurrent.yaml"},
		batch2APressKeyCommand("HOME"),
	)
	if err != nil {
		t.Fatal(err)
	}
	root := &compiledFlow{
		path:   "/workspace/batch2a-concurrent.yaml",
		config: model.Config{AppID: "com.example.concurrent"},
		body:   []compiledDispatch{compiled},
	}

	const roots = 32
	var group sync.WaitGroup
	errs := make(chan error, roots)
	for index := range roots {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			base := batch2ADriver("android")
			driver := &batch2AMutatingPressKeyDriver{Driver: base}
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "batch2a-concurrent", Driver: driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
			}, root, fmt.Sprintf("batch2a-concurrent/root-run-%06d", index+1))
			if runErr != nil {
				errs <- runErr
				return
			}
			commands := result.Commands()
			requests := batch2APressKeyRequests(base.Actions())
			if result.Outcome() != Completed || len(commands) != 1 || commands[0].Outcome() != Completed ||
				len(requests) != 1 || requests[0].Code != "HOME" || !reflect.DeepEqual(requests[0].AppIDs, []string{"com.example.concurrent"}) {
				errs <- errors.New("concurrent root state or request ownership escaped")
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestInteractionBatch2AFailClosedInternalPayloadsAndEvaluationDependencies(t *testing.T) {
	t.Parallel()

	if _, err := compileBatch2ADirect(batch2APressKeyCommand("HOME")); !isConfigurationError(err) || err.Error() != "pressKey is not a direct interaction command" {
		t.Fatalf("direct compiler wrong keyword error = %T %v", err, err)
	}
	if _, err := compilePressKey(batch2ABareCommand(model.CommandBack)); !isConfigurationError(err) {
		t.Fatalf("pressKey compiler wrong keyword error = %T %v", err, err)
	}
	if _, err := evaluateBatch2ADirect(context.Background(), batch2AEvaluation(nil, "app"), batch2ABareCommand(model.CommandBack), struct{}{}); !isConfigurationError(err) {
		t.Fatalf("direct evaluator invalid payload error = %T %v", err, err)
	}
	if _, err := evaluateBatch2ADirect(context.Background(), batch2AEvaluation(nil, "app"), batch2ABareCommand(model.CommandBack), batch2ADirectCompiled{keyword: model.CommandScroll}); !isConfigurationError(err) {
		t.Fatalf("direct evaluator keyword mismatch error = %T %v", err, err)
	}
	if _, err := evaluatePressKey(context.Background(), batch2AEvaluation(nil, "app"), batch2APressKeyCommand("HOME"), struct{}{}); !isConfigurationError(err) {
		t.Fatalf("pressKey evaluator invalid payload error = %T %v", err, err)
	}

	compiledDirect := batch2ADirectCompiled{keyword: model.CommandBack}
	if _, err := evaluateBatch2ADirect(context.Background(), evaluationContext{}, batch2ABareCommand(model.CommandBack), compiledDirect); !isConfigurationError(err) {
		t.Fatalf("missing active config error = %T %v", err, err)
	}
	blankEvaluation := batch2AEvaluation(nil, " ")
	if _, err := evaluateBatch2ADirect(context.Background(), blankEvaluation, batch2ABareCommand(model.CommandBack), compiledDirect); !isConfigurationError(err) {
		t.Fatalf("blank active app error = %T %v", err, err)
	}
	interpolationErr := errors.New("interpolation failed")
	failingEvaluation := evaluationContext{
		interpolateFn: func(context.Context, string, map[string]any) (string, error) { return "", interpolationErr },
		activeConfig:  model.Config{AppID: "app"}, hasActiveConfig: true,
	}
	if _, err := evaluateBatch2ADirect(context.Background(), failingEvaluation, batch2ABareCommand(model.CommandBack), compiledDirect); !errors.Is(err, interpolationErr) {
		t.Fatalf("active app interpolation error = %v", err)
	}
	dynamicCompiled := pressKeyCompiled{authored: "${KEY}", requiresEvaluation: true}
	if _, err := evaluatePressKey(context.Background(), failingEvaluation, batch2APressKeyCommand("${KEY}"), dynamicCompiled); !errors.Is(err, interpolationErr) {
		t.Fatalf("key interpolation error = %v", err)
	}

	driver := batch2ADriver("android")
	clock := newAdvancingClock()
	lookup := NewElementLookup(driver, clock)
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	if _, err := executeBatch2ADirect(context.Background(), state, evaluatedDispatch{
		command: batch2ABareCommand(model.CommandBack), value: struct{}{},
	}); !isConfigurationError(err) || err.Error() != "back received an invalid evaluated payload" {
		t.Fatalf("direct executor invalid payload error = %T %v", err, err)
	}
	if _, err := executeBatch2ADirect(context.Background(), state, evaluatedDispatch{
		command: batch2APressKeyCommand("HOME"),
		value:   batch2ADirectEvaluated{keyword: model.CommandPressKey, appID: "app"},
	}); !isConfigurationError(err) || err.Error() != "pressKey is not executable as a direct interaction command" {
		t.Fatalf("direct executor invalid keyword error = %T %v", err, err)
	}
	if _, err := executePressKey(context.Background(), state, evaluatedDispatch{value: struct{}{}}); !isConfigurationError(err) {
		t.Fatalf("pressKey executor invalid payload error = %T %v", err, err)
	}
	if _, err := executePressKey(context.Background(), state, evaluatedDispatch{value: pressKeyEvaluated{appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("pressKey executor blank code error = %T %v", err, err)
	}
	if _, err := executePressKey(context.Background(), state, evaluatedDispatch{value: pressKeyEvaluated{appID: "app", code: "INVALID"}}); !isConfigurationError(err) {
		t.Fatalf("pressKey executor invalid canonical code error = %T %v", err, err)
	}
	if _, err := batch2AExecutionLookup(nil, model.CommandBack, "app"); !isConfigurationError(err) {
		t.Fatalf("nil state error = %T %v", err, err)
	}
	if _, err := batch2AExecutionLookup(state, model.CommandBack, " "); !isConfigurationError(err) {
		t.Fatalf("blank execution app error = %T %v", err, err)
	}
	if batch2APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("invalid internal payload caused effects: %#v", driver.Actions())
	}
}

func executeBatch2AForTest(ctx context.Context, command model.Command, replacements map[string]string, driver device.Driver, clock Clock) (commandEffect, error) {
	return executeBatch2AWithLookupForTest(ctx, command, replacements, driver, clock, NewElementLookup(driver, clock))
}

func executeBatch2AWithLookupForTest(ctx context.Context, command model.Command, replacements map[string]string, driver device.Driver, clock Clock, lookup *ElementLookup) (commandEffect, error) {
	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		return commandEffect{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return commandEffect{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, batch2AEvaluation(replacements, "com.example.batch2a"), compiled)
	if err != nil {
		return commandEffect{}, err
	}
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	return dispatcher.execute(ctx, state, compiled, evaluated)
}

func batch2AEvaluation(replacements map[string]string, appID string) evaluationContext {
	return evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			for name, value := range replacements {
				input = strings.ReplaceAll(input, "${"+name+"}", value)
			}
			return input, nil
		},
		activeConfig: model.Config{AppID: appID}, hasActiveConfig: true,
	}
}

func batch2ABareCommand(keyword model.CommandKeyword) model.Command {
	return model.Command{Kind: keyword, Form: model.CommandFormScalar}
}

func batch2APressKeyCommand(value string) model.Command {
	return model.Command{Kind: model.CommandPressKey, Form: model.CommandFormObject, Arguments: value}
}

func batch2ADriver(platform device.Platform) *enginetest.FakeDriver {
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	return batch2ADriverWithSettle(platform, []enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}})
}

func batch2ADriverWithSettle(platform device.Platform, settle []enginetest.Result[*device.ViewHierarchy]) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: platform}}},
		WaitForAppToSettle: settle,
	})
	return driver
}

func batch2AScriptFor(keyword model.CommandKeyword, err error) enginetest.DriverScript {
	result := []enginetest.Result[struct{}]{{Err: err}}
	switch keyword {
	case model.CommandBack:
		return enginetest.DriverScript{BackPress: result}
	case model.CommandHideKeyboard:
		return enginetest.DriverScript{HideKeyboard: result}
	case model.CommandScroll:
		return enginetest.DriverScript{ScrollVertical: result}
	case model.CommandPressKey:
		return enginetest.DriverScript{PressKey: result}
	default:
		return enginetest.DriverScript{}
	}
}

func batch2APressKeyRequests(actions []enginetest.Action) []device.PressKeyRequest {
	requests := make([]device.PressKeyRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodPressKey {
			requests = append(requests, action.Request.(device.PressKeyRequest))
		}
	}
	return requests
}

func batch2AScrollRequests(actions []enginetest.Action) []device.ScrollVerticalRequest {
	requests := make([]device.ScrollVerticalRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodScrollVertical {
			requests = append(requests, action.Request.(device.ScrollVerticalRequest))
		}
	}
	return requests
}

func countBatch2AMethod(actions []enginetest.Action, method enginetest.Method) int {
	count := 0
	for _, action := range actions {
		if action.Method == method {
			count++
		}
	}
	return count
}

func batch2APhysicalCount(actions []enginetest.Action) int {
	return countBatch2AMethod(actions, enginetest.MethodBackPress) +
		countBatch2AMethod(actions, enginetest.MethodHideKeyboard) +
		countBatch2AMethod(actions, enginetest.MethodScrollVertical) +
		countBatch2AMethod(actions, enginetest.MethodPressKey)
}

type batch2AMutatingPressKeyDriver struct{ device.Driver }

type batch2ACyclicDriverError struct{}

func (*batch2ACyclicDriverError) Error() string { return "cyclic Driver error" }

func (err *batch2ACyclicDriverError) Unwrap() error { return err }

func (driver *batch2AMutatingPressKeyDriver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	err := driver.Driver.PressKey(ctx, request)
	if len(request.AppIDs) > 0 {
		request.AppIDs[0] = "mutated.by.driver"
	}
	return err
}

type batch2ACancelAfterActionDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch2ACancelAfterActionDriver) BackPress(ctx context.Context) error {
	err := driver.Driver.BackPress(ctx)
	driver.cancel()
	return err
}

func (driver *batch2ACancelAfterActionDriver) HideKeyboard(ctx context.Context) error {
	err := driver.Driver.HideKeyboard(ctx)
	driver.cancel()
	return err
}

func (driver *batch2ACancelAfterActionDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	err := driver.Driver.ScrollVertical(ctx, request)
	driver.cancel()
	return err
}

func (driver *batch2ACancelAfterActionDriver) PressKey(ctx context.Context, request device.PressKeyRequest) error {
	err := driver.Driver.PressKey(ctx, request)
	driver.cancel()
	return err
}
