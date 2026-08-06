package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch4APrivateRegistryAndCompileContract(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(
		doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec(),
		backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec(),
		scrollUntilVisibleHandlerSpec(), inputTextHandlerSpec(), eraseTextHandlerSpec(),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	wantPrivate := []string{
		"back", "doubleTapOn", "eraseText", "hideKeyboard", "inputText", "longPressOn",
		"pressKey", "scroll", "scrollUntilVisible", "swipe",
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, wantPrivate) {
		t.Fatalf("private registry = %#v, want %#v", got, wantPrivate)
	}
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	wantProduction := productionKeywordStrings()
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry = %#v, want exact thirty %#v", got, wantProduction)
	}
	for _, keyword := range []model.CommandKeyword{model.CommandInputText, model.CommandEraseText} {
		spec, ok := registry.lookup(keyword)
		if !ok || spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle || spec.settleRequest != nil {
			t.Fatalf("%s spec = %#v, want device mutation/no generic settle", keyword, spec)
		}
	}

	inputCases := []struct {
		name    string
		command model.Command
		text    string
		label   *string
	}{
		{name: "empty scalar", command: batch4AInputScalar(""), text: ""},
		{name: "special scalar", command: batch4AInputScalar("line\n世界 $[]{}"), text: "line\n世界 $[]{}"},
		{name: "object without label", command: batch4AInputObject("hello", nil), text: "hello"},
		{name: "object with empty label", command: batch4AInputObject("hello", stringPointer("")), text: "hello", label: stringPointer("")},
		{name: "object", command: batch4AInputObject("hello", stringPointer("typing")), text: "hello", label: stringPointer("typing")},
		{name: "dynamic object", command: batch4AInputObject("${TEXT}", stringPointer("${LABEL}")), text: "${TEXT}", label: stringPointer("${LABEL}")},
	}
	for _, test := range inputCases {
		compiledValue, compileErr := compileInputText(test.command)
		if compileErr != nil {
			t.Fatalf("compileInputText(%s) error = %v", test.name, compileErr)
		}
		compiled := compiledValue.(inputTextCompiled)
		if compiled.authoredText != test.text || !stringPointersEqual(compiled.authoredLabel, test.label) {
			t.Fatalf("compileInputText(%s) = %#v", test.name, compiled)
		}
	}

	for _, command := range []model.Command{
		{Kind: model.CommandInputText, Form: model.CommandFormScalar},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"label": "only"}, Label: stringPointer("only")},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": "ok", "paste": true}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": int64(1)}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": "ok", "label": int64(1)}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": "ok", "label": "raw"}, Label: stringPointer("typed-mismatch")},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": "ok", "label": "raw"}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"text": "ok"}, Label: stringPointer("typed-only")},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: true},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: []any{"ok"}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: nil},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "ok", Optional: boolPointer(false)},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "ok", Selector: &model.ElementSelector{}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "ok", Children: []model.Command{batch4AEraseBare()}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "ok", Condition: &model.Condition{}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "ok", Links: []model.FileLink{{Kind: model.FileLinkFlow, Path: "foreign.yaml"}}},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: "ok"},
	} {
		if _, err := compileInputText(command); !isConfigurationError(err) {
			t.Fatalf("compileInputText(%#v) error = %T %v, want ConfigurationError", command, err, err)
		}
	}

	eraseCases := []struct {
		command model.Command
		count   uint32
		dynamic bool
	}{
		{command: batch4AEraseBare(), count: 50},
		{command: batch4AErase(int64(0)), count: 0},
		{command: batch4AErase(int64(100)), count: 100},
		{command: batch4AErase("0"), count: 0},
		{command: batch4AErase("1"), count: 1},
		{command: batch4AErase("50"), count: 50},
		{command: batch4AErase("100"), count: 100},
		{command: batch4AErase("${COUNT}"), dynamic: true},
	}
	for _, test := range eraseCases {
		compiledValue, compileErr := compileEraseText(test.command)
		if compileErr != nil {
			t.Fatalf("compileEraseText(%#v) error = %v", test.command, compileErr)
		}
		compiled := compiledValue.(eraseTextCompiled)
		if compiled.count != test.count || compiled.requiresEvaluation != test.dynamic {
			t.Fatalf("compileEraseText(%#v) = %#v", test.command, compiled)
		}
	}
	for _, invalid := range []any{
		int64(-1), int64(101), "", " ", " 1", "1 ", "+1", "-1", "1.0", "1e2", "101",
		"4294967296", "18446744073709551616", float64(1), uint64(^uint64(0)), true,
		map[string]any{"count": int64(1)}, []any{int64(1)}, nil,
	} {
		if _, err := compileEraseText(batch4AErase(invalid)); !isConfigurationError(err) {
			t.Fatalf("compileEraseText(%#v) error = %T %v, want ConfigurationError", invalid, err, err)
		}
	}
	for _, command := range []model.Command{
		{Kind: model.CommandEraseText, Form: model.CommandFormScalar, Arguments: int64(1)},
		{Kind: model.CommandEraseText, Form: model.CommandFormScalar, Label: stringPointer("label")},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Optional: boolPointer(false)},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Children: []model.Command{batch4AEraseBare()}},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Label: stringPointer("label")},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Selector: &model.ElementSelector{}},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Condition: &model.Condition{}},
		{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: int64(1), Links: []model.FileLink{{Kind: model.FileLinkFlow, Path: "foreign.yaml"}}},
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: int64(1)},
	} {
		if _, err := compileEraseText(command); !isConfigurationError(err) {
			t.Fatalf("compileEraseText(%#v) error = %T %v, want ConfigurationError", command, err, err)
		}
	}
}

func TestInteractionBatch4AEvaluationOrderMetadataAndExactRequests(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	input := batch4AInputObject("${TEXT}", stringPointer("${LABEL}"))
	compiledInput, err := dispatcher.compile(context.Background(), compileContext{}, input)
	if err != nil {
		t.Fatal(err)
	}
	trace := make([]string, 0, 3)
	evaluation := batch4AEvaluationTrace(&trace, map[string]string{
		"${APP}": "com.example.batch4a.dynamic", "${TEXT}": "line\n世界", "${LABEL}": "typing 世界",
	}, "${APP}")
	evaluatedInput, err := dispatcher.evaluate(context.Background(), evaluation, compiledInput)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(trace, []string{"${APP}", "${TEXT}", "${LABEL}"}) {
		t.Fatalf("input interpolation order = %#v", trace)
	}
	wantArguments := map[string]any{"text": "line\n世界", "label": "typing 世界"}
	if !reflect.DeepEqual(evaluatedInput.command.Arguments, wantArguments) || evaluatedInput.command.Label == nil || *evaluatedInput.command.Label != "typing 世界" {
		t.Fatalf("evaluated input command = %#v", evaluatedInput.command)
	}
	driver := batch4ADriver()
	clock := &batch1ATraceClock{now: time.Unix(2000, 0).UTC()}
	lookup := NewElementLookup(driver, clock)
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	effect, err := dispatcher.execute(context.Background(), state, compiledInput, evaluatedInput)
	if err != nil || effect.effectClass != EffectDeviceMutation {
		t.Fatalf("input execute = effect %#v error %v", effect, err)
	}
	if got, want := batch4AInputRequests(driver.Actions()), []device.InputTextRequest{{Text: "line\n世界", AppIDs: []string{"com.example.batch4a.dynamic"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("InputText requests = %#v, want %#v", got, want)
	}
	if settles := settleRequests(driver.Actions()); len(settles) == 0 || settles[0].AppID != "com.example.batch4a.dynamic" || settles[0].TimeoutMillis != nil {
		t.Fatalf("input settle requests = %#v", settles)
	}

	compiledErase, err := dispatcher.compile(context.Background(), compileContext{}, batch4AErase("${COUNT}"))
	if err != nil {
		t.Fatal(err)
	}
	trace = trace[:0]
	evaluatedErase, err := dispatcher.evaluate(context.Background(), batch4AEvaluationTrace(&trace, map[string]string{
		"${APP}": "com.example.batch4a.erase", "${COUNT}": "100",
	}, "${APP}"), compiledErase)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(trace, []string{"${APP}", "${COUNT}"}) || evaluatedErase.command.Arguments != "100" {
		t.Fatalf("erase evaluation = trace %#v command %#v", trace, evaluatedErase.command)
	}
	driver = batch4ADriver()
	clock = &batch1ATraceClock{now: time.Unix(2001, 0).UTC()}
	lookup = NewElementLookup(driver, clock)
	state = &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	effect, err = dispatcher.execute(context.Background(), state, compiledErase, evaluatedErase)
	if err != nil || effect.effectClass != EffectDeviceMutation {
		t.Fatalf("erase execute = effect %#v error %v", effect, err)
	}
	if got, want := batch4AEraseRequests(driver.Actions()), []device.EraseTextRequest{{CharactersToErase: 100, AppIDs: []string{"com.example.batch4a.erase"}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("EraseText requests = %#v, want %#v", got, want)
	}
}

func TestInteractionBatch4AEmptySpecialTextAndEraseBoundaries(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "   ", "café 世界", "line\nnext\t\\$[]{}", "🙂"} {
		driver := batch4ADriver()
		effect, evaluated, err := executeBatch4AForTest(context.Background(), batch4AInputScalar(text), nil, driver, newAdvancingClock())
		if err != nil || effect.effectClass != EffectDeviceMutation {
			t.Fatalf("input %q = effect %#v error %v", text, effect, err)
		}
		requests := batch4AInputRequests(driver.Actions())
		if len(requests) != 1 || requests[0].Text != text || !reflect.DeepEqual(requests[0].AppIDs, []string{"com.example.batch4a"}) || evaluated.command.Arguments != text {
			t.Fatalf("input %q request/evaluated = %#v / %#v", text, requests, evaluated.command)
		}
	}
	for _, test := range []struct {
		argument     any
		replacements map[string]string
		count        uint32
	}{
		{argument: batch4ABareSentinel{}, count: 50},
		{argument: int64(0), count: 0},
		{argument: int64(1), count: 1},
		{argument: int64(50), count: 50},
		{argument: int64(100), count: 100},
		{argument: "0", count: 0},
		{argument: "1", count: 1},
		{argument: "50", count: 50},
		{argument: "100", count: 100},
		{argument: "${COUNT}", replacements: map[string]string{"COUNT": "0"}, count: 0},
		{argument: "${COUNT}", replacements: map[string]string{"COUNT": "1"}, count: 1},
		{argument: "${COUNT}", replacements: map[string]string{"COUNT": "50"}, count: 50},
		{argument: "${COUNT}", replacements: map[string]string{"COUNT": "100"}, count: 100},
	} {
		command := batch4AErase(test.argument)
		if _, ok := test.argument.(batch4ABareSentinel); ok {
			command = batch4AEraseBare()
		}
		driver := batch4ADriver()
		_, _, err := executeBatch4AForTest(context.Background(), command, test.replacements, driver, newAdvancingClock())
		requests := batch4AEraseRequests(driver.Actions())
		if err != nil || len(requests) != 1 || requests[0].CharactersToErase != test.count || !reflect.DeepEqual(requests[0].AppIDs, []string{"com.example.batch4a"}) {
			t.Fatalf("erase %#v = requests %#v error %v", test.argument, requests, err)
		}
	}
}

func TestInteractionBatch4AStaticAndDynamicInvalidValuesCutOffEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	for _, commands := range [][]model.Command{
		{batch4AErase(int64(101)), batch4AInputScalar("later")},
		{batch4AInputScalar("first"), batch4AErase(int64(101))},
		{{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: map[string]any{"label": "missing text"}}, batch4AEraseBare()},
	} {
		if _, err := dispatcher.compileSequence(context.Background(), compileContext{}, commands); !isConfigurationError(err) {
			t.Fatalf("compileSequence(%#v) error = %T %v", commands, err, err)
		}
	}

	for _, value := range []string{"", " ", "+1", "-1", "1.0", "1e2", "101", "4294967296", "999999999999999999999999"} {
		driver := batch4ADriver()
		_, _, err := executeBatch4AForTest(
			context.Background(), batch4AErase("${COUNT}"), map[string]string{"COUNT": value}, driver, newAdvancingClock(),
		)
		if !isConfigurationError(err) || batch4APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
			t.Fatalf("dynamic erase %q error = %T %v actions %#v", value, err, err, driver.Actions())
		}
	}
	for _, command := range []model.Command{
		batch4AInputScalar("${FAIL}"),
		batch4AInputObject("ok", stringPointer("${FAIL}")),
	} {
		driver := batch4ADriver()
		registry, registryErr := newHandlerRegistry(inputTextHandlerSpec())
		if registryErr != nil {
			t.Fatal(registryErr)
		}
		compiled, compileErr := newDispatcher(registry).compile(context.Background(), compileContext{}, command)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		calls := 0
		evaluation := evaluationContext{
			interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
				calls++
				if input == "${FAIL}" {
					return "", errors.New("interpolation failed")
				}
				return input, nil
			},
			activeConfig: model.Config{AppID: "com.example.batch4a"}, hasActiveConfig: true,
		}
		if _, evaluateErr := newDispatcher(registry).evaluate(context.Background(), evaluation, compiled); evaluateErr == nil || calls < 2 {
			t.Fatalf("input interpolation failure = calls %d error %v", calls, evaluateErr)
		}
		if batch4APhysicalCount(driver.Actions()) != 0 {
			t.Fatalf("input interpolation failure actions = %#v", driver.Actions())
		}
	}
}

func TestInteractionBatch4AStaticInvalidFirstAndLaterRootsHaveWholeProgramZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	valid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.valid"}, Commands: []model.Command{batch4AInputScalar("valid")}}
	}
	invalid := func(path string) model.Flow {
		return model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.invalid"}, Commands: []model.Command{batch4AErase(int64(101))}}
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
			factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
			controllerCalls := 0
			listenerCalls := 0
			dependencies := Dependencies{
				ExecutionID: "batch4a-static-preflight", Driver: driver, Clock: newAdvancingClock(), JSFactory: factory,
				Controller: ControllerFunc(func(context.Context) error { controllerCalls++; return nil }),
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error {
					listenerCalls++
					return nil
				})},
			}

			compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(test.flows...), registry)
			results := make([]FlowResult, 0, len(test.flows))
			if compileErr == nil {
				for index, rootPath := range compiled.Roots() {
					root, exists := compiled.Flow(rootPath)
					if !exists {
						compileErr = NewConfigurationError("compiled Batch 4A test root is missing", nil)
						break
					}
					result, executeErr := executeCompiledRootForRun(
						context.Background(), dependencies, root,
						fmt.Sprintf("batch4a-static-preflight/root-run-%06d", index+1),
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
				t.Fatalf("whole-program preflight = %#v, %T %v", compiled, compileErr, compileErr)
			}
			if len(results) != 0 || factory.calls != 0 || controllerCalls != 0 || listenerCalls != 0 || len(driver.Actions()) != 0 {
				t.Fatalf(
					"whole-program effects = results %d runtime %d controller %d listeners %d driver %#v",
					len(results), factory.calls, controllerCalls, listenerCalls, driver.Actions(),
				)
			}
		})
	}
}

func TestInteractionBatch4ASelectedRootLateInvalidAllowsOnlySessionDeviceInfo(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compileSequence(
		context.Background(), compileContext{containingFlow: "/workspace/batch4a-late-invalid.yaml"},
		[]model.Command{batch4AErase("${COUNT}"), batch4AInputScalar("later")},
	)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch4ADriver()
	events := make([]Event, 0, 4)
	root := &compiledFlow{
		path:   "/workspace/batch4a-late-invalid.yaml",
		config: model.Config{AppID: "com.example.batch4a", Env: map[string]string{"COUNT": "101"}},
		body:   compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-late-invalid", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error { events = append(events, event); return nil })},
	}, root, "batch4a-late-invalid/root-run-000001")
	commands := result.Commands()
	if !isConfigurationError(runErr) || result.Outcome() != Failed || len(commands) != 1 ||
		commands[0].Command().Kind != model.CommandEraseText || commands[0].Outcome() != Failed {
		t.Fatalf("late-invalid root = result %#v commands %#v error %T %v", result, commands, runErr, runErr)
	}
	if got := batch4AMethods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{enginetest.MethodDeviceInfo}) {
		t.Fatalf("late-invalid actions = %#v, want DeviceInfo only", driver.Actions())
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Failed || events[3].Outcome() != Failed {
		t.Fatalf("late-invalid lifecycle = %#v", events)
	}
}

func TestInteractionBatch4ACancellationDriverFailureWatermarkAndSettlePrecedence(t *testing.T) {
	t.Parallel()

	commands := []model.Command{batch4AInputScalar("hello"), batch4AErase(int64(7))}
	for _, command := range commands {
		t.Run(string(command.Kind), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			driver := batch4ADriver()
			_, _, err := executeBatch4AForTest(ctx, command, nil, driver, newAdvancingClock())
			if !errors.Is(err, context.Canceled) || batch4APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("pre-cancel = error %v actions %#v", err, driver.Actions())
			}

			physicalErr := errors.New("physical input failed")
			driver = batch4ADriver()
			if command.Kind == model.CommandInputText {
				driver.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{{Err: physicalErr}}})
			} else {
				driver.Enqueue(enginetest.DriverScript{EraseText: []enginetest.Result[struct{}]{{Err: physicalErr}}})
			}
			failureClock := &batch1ATraceClock{now: time.Unix(2050, 0).UTC()}
			failureLookup := NewElementLookup(driver, failureClock)
			_, _, err = executeBatch4AWithLookupForTest(context.Background(), command, nil, driver, failureClock, failureLookup)
			if !errors.Is(err, physicalErr) || batch4APhysicalCount(driver.Actions()) != 1 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("Driver failure = error %T %v actions %#v", err, err, driver.Actions())
			}
			failureClock.now = failureClock.now.Add(time.Second)
			if got := failureLookup.AdjustedTimeout(LookupOptions{}); got != LookupTimeout {
				t.Fatalf("failed-action watermark timeout = %v, want unchanged %v", got, LookupTimeout)
			}

			ordinary := NewOperationError("ordinary settle", nil)
			driver = batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: ordinary}})
			if _, _, err = executeBatch4AForTest(context.Background(), command, nil, driver, newAdvancingClock()); err != nil {
				t.Fatalf("ordinary settle error = %v, want ignored", err)
			}

			terminal := NewDeviceConnectionError("settle disconnected", nil)
			driver = batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: terminal}})
			_, _, err = executeBatch4AForTest(context.Background(), command, nil, driver, newAdvancingClock())
			if classifyTerminalError(err) != terminalErrorDeviceConnection {
				t.Fatalf("terminal settle error = %T %v", err, err)
			}

			configuration := NewConfigurationError("settle configuration", nil)
			driver = batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: configuration}})
			_, _, err = executeBatch4AForTest(context.Background(), command, nil, driver, newAdvancingClock())
			if classifyTerminalError(err) != terminalErrorConfiguration {
				t.Fatalf("configuration settle error = %T %v", err, err)
			}

			settleCtx, settleCancel := context.WithCancel(context.Background())
			base := batch4ADriver()
			cancelSettle := &batch4ACancelSettleDriver{Driver: base, cancel: settleCancel}
			_, _, err = executeBatch4AForTest(settleCtx, command, nil, cancelSettle, newAdvancingClock())
			if !errors.Is(err, context.Canceled) || batch4APhysicalCount(base.Actions()) != 1 ||
				cancelSettle.SettleCalls() != 1 || len(settleRequests(base.Actions())) != 1 {
				t.Fatalf(
					"settle cancellation = error %T %v attempts %d actions %#v",
					err, err, cancelSettle.SettleCalls(), base.Actions(),
				)
			}

			postCtx, postCancel := context.WithCancel(context.Background())
			base = batch4ADriver()
			postDriver := &batch4ACancelAfterInputDriver{Driver: base, cancel: postCancel}
			clock := &batch1ATraceClock{now: time.Unix(2100, 0).UTC()}
			lookup := NewElementLookup(postDriver, clock)
			_, _, err = executeBatch4AWithLookupForTest(postCtx, command, nil, postDriver, clock, lookup)
			if !errors.Is(err, context.Canceled) || batch4APhysicalCount(base.Actions()) != 1 || len(settleRequests(base.Actions())) != 0 {
				t.Fatalf("post-call cancellation = error %v actions %#v", err, base.Actions())
			}
			clock.now = clock.now.Add(time.Second)
			if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-time.Second; got != want {
				t.Fatalf("watermark timeout = %v, want %v", got, want)
			}

			waitCtx, waitCancel := context.WithCancel(context.Background())
			driver = batch4ADriver()
			waitClock := &batch1ACancelWaitClock{now: time.Unix(2150, 0).UTC(), cancel: waitCancel}
			waitLookup := NewElementLookup(driver, waitClock)
			_, _, err = executeBatch4AWithLookupForTest(waitCtx, command, nil, driver, waitClock, waitLookup)
			if !errors.Is(err, context.Canceled) || batch4APhysicalCount(driver.Actions()) != 1 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("settle Clock-wait cancellation = error %T %v actions %#v", err, err, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch4AFailClosedInternalPayloadsAndDependencies(t *testing.T) {
	t.Parallel()

	if _, err := compileInputText(batch4AEraseBare()); !isConfigurationError(err) {
		t.Fatalf("input wrong keyword error = %T %v", err, err)
	}
	if _, err := compileEraseText(batch4AInputScalar("x")); !isConfigurationError(err) {
		t.Fatalf("erase wrong keyword error = %T %v", err, err)
	}
	if _, err := evaluateInputText(context.Background(), batch2AEvaluation(nil, "app"), batch4AInputScalar("x"), struct{}{}); !isConfigurationError(err) {
		t.Fatalf("input invalid compiled payload error = %T %v", err, err)
	}
	if _, err := evaluateEraseText(context.Background(), batch2AEvaluation(nil, "app"), batch4AEraseBare(), struct{}{}); !isConfigurationError(err) {
		t.Fatalf("erase invalid compiled payload error = %T %v", err, err)
	}
	compiledInput := inputTextCompiled{authoredText: "x"}
	if _, err := evaluateInputText(context.Background(), evaluationContext{}, batch4AInputScalar("x"), compiledInput); !isConfigurationError(err) {
		t.Fatalf("missing active config error = %T %v", err, err)
	}
	if _, err := evaluateInputText(context.Background(), batch2AEvaluation(nil, " "), batch4AInputScalar("x"), compiledInput); !isConfigurationError(err) {
		t.Fatalf("blank active app error = %T %v", err, err)
	}
	if _, err := evaluateEraseText(
		context.Background(), batch2AEvaluation(nil, " "), batch4AEraseBare(), eraseTextCompiled{count: 50},
	); !isConfigurationError(err) {
		t.Fatalf("erase blank active app error = %T %v", err, err)
	}

	driver := batch4ADriver()
	clock := newAdvancingClock()
	lookup := NewElementLookup(driver, clock)
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	if _, err := executeInputText(context.Background(), state, evaluatedDispatch{value: struct{}{}}); !isConfigurationError(err) {
		t.Fatalf("input invalid evaluated payload error = %T %v", err, err)
	}
	if _, err := executeEraseText(context.Background(), state, evaluatedDispatch{value: struct{}{}}); !isConfigurationError(err) {
		t.Fatalf("erase invalid evaluated payload error = %T %v", err, err)
	}
	if _, err := executeInputText(context.Background(), state, evaluatedDispatch{value: inputTextEvaluated{text: "x", appID: " "}}); !isConfigurationError(err) {
		t.Fatalf("input blank app payload error = %T %v", err, err)
	}
	if _, err := executeEraseText(context.Background(), state, evaluatedDispatch{value: eraseTextEvaluated{count: 101, appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("erase out-of-range payload error = %T %v", err, err)
	}
	if _, err := executeInputText(context.Background(), nil, evaluatedDispatch{value: inputTextEvaluated{text: "x", appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("nil state error = %T %v", err, err)
	}
	contextLookupCalls := 0
	contextState := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn: func() (*ElementLookup, error) {
			contextLookupCalls++
			return lookup, nil
		},
	}
	var nilContext context.Context
	if _, err := executeInputText(nilContext, contextState, evaluatedDispatch{value: inputTextEvaluated{text: "x", appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("nil input context error = %T %v", err, err)
	}
	if _, err := executeEraseText(nilContext, contextState, evaluatedDispatch{value: eraseTextEvaluated{count: 1, appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("nil erase context error = %T %v", err, err)
	}
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executeInputText(cancelledCtx, contextState, evaluatedDispatch{value: inputTextEvaluated{text: "x", appID: "app"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled input error = %T %v", err, err)
	}
	if _, err := executeEraseText(cancelledCtx, contextState, evaluatedDispatch{value: eraseTextEvaluated{count: 1, appID: "app"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled erase error = %T %v", err, err)
	}
	if contextLookupCalls != 0 {
		t.Fatalf("nil/pre-cancelled contexts reached lookup %d times", contextLookupCalls)
	}
	var typedNilDriver *enginetest.FakeDriver
	typedNilState := &executionState{dependencies: Dependencies{Driver: typedNilDriver, Clock: clock}}
	if _, err := executeEraseText(context.Background(), typedNilState, evaluatedDispatch{value: eraseTextEvaluated{count: 1, appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Driver error = %T %v", err, err)
	}
	var typedNilClock *batch1ATraceClock
	typedNilState = &executionState{dependencies: Dependencies{Driver: driver, Clock: typedNilClock}}
	if _, err := executeInputText(context.Background(), typedNilState, evaluatedDispatch{value: inputTextEvaluated{text: "x", appID: "app"}}); !isConfigurationError(err) {
		t.Fatalf("typed-nil Clock error = %T %v", err, err)
	}
	if batch4APhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("invalid payload effects = %#v", driver.Actions())
	}
}

func TestInteractionBatch4ACompiledOwnershipAndConcurrentRequestStress(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	label := "${LABEL}"
	source := batch4AInputObject("${TEXT}", &label)
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Arguments.(map[string]any)["text"] = "MUTATED"
	*source.Label = "MUTATED"

	const executions = 48
	var group sync.WaitGroup
	errs := make(chan error, executions)
	for index := range executions {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			appID := fmt.Sprintf("com.example.batch4a.%02d", index)
			text := fmt.Sprintf("text-%02d", index)
			label := fmt.Sprintf("label-%02d", index)
			evaluated, evaluateErr := dispatcher.evaluate(context.Background(), batch2AEvaluation(map[string]string{
				"TEXT": text, "LABEL": label,
			}, appID), compiled)
			if evaluateErr != nil {
				errs <- evaluateErr
				return
			}
			base := batch4ADriver()
			driver := &batch4AMutatingRequestDriver{Driver: base}
			clock := newAdvancingClock()
			lookup := NewElementLookup(driver, clock)
			state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
			if _, executeErr := dispatcher.execute(context.Background(), state, compiled, evaluated); executeErr != nil {
				errs <- executeErr
				return
			}
			requests := batch4AInputRequests(base.Actions())
			arguments, ok := evaluated.command.Arguments.(map[string]any)
			if len(requests) != 1 || requests[0].Text != text || !reflect.DeepEqual(requests[0].AppIDs, []string{appID}) ||
				!ok || arguments["text"] != text || arguments["label"] != label || evaluated.command.Label == nil || *evaluated.command.Label != label {
				errs <- fmt.Errorf("ownership escaped for %d: request %#v command %#v", index, requests, evaluated.command)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func TestInteractionBatch4ANestedFlowAppIDPrecedenceAndRestoration(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/batch4a-root.yaml"
	childPath := "/workspace/batch4a-child.yaml"
	link := model.FileLink{Kind: model.FileLinkFlow, Path: "batch4a-child.yaml", ResolvedPath: childPath}
	runChild := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{"file": "batch4a-child.yaml"},
		Links:     []model.FileLink{link},
		Source:    model.SourceInfo{Path: rootPath},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config:   model.Config{AppID: "com.example.batch4a.root"},
		Commands: []model.Command{runChild, batch4AInputScalar("root")},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config:   model.Config{AppID: "com.example.batch4a.child"},
		Commands: []model.Command{batch4AInputScalar("child")},
	}
	registry, err := newHandlerRegistry(runFlowHandlerSpec(), inputTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry)
	if err != nil {
		t.Fatal(err)
	}
	root, exists := compiled.Flow(rootPath)
	if !exists {
		t.Fatal("compiled Batch 4A root is missing")
	}
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	driver := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{
		{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
	})
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-nested", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch4a-nested/root-run-000001")
	requests := batch4AInputRequests(driver.Actions())
	want := []device.InputTextRequest{
		{Text: "child", AppIDs: []string{"com.example.batch4a.child"}},
		{Text: "root", AppIDs: []string{"com.example.batch4a.root"}},
	}
	if runErr != nil || result.Outcome() != Completed || !reflect.DeepEqual(requests, want) {
		t.Fatalf("nested execution = outcome %s error %v requests %#v, want %#v", result.Outcome(), runErr, requests, want)
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].Sequence() != 1 || commands[0].Depth() != 0 ||
		commands[1].Sequence() != 2 || commands[1].Depth() != 1 || commands[1].Command().Kind != model.CommandInputText ||
		commands[2].Sequence() != 3 || commands[2].Depth() != 0 || commands[2].Command().Kind != model.CommandInputText {
		t.Fatalf("nested result ledger = %#v", commands)
	}
}

func TestInteractionBatch4ADriverFailureProjectsEvidenceAndStopsLaterCommand(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compileSequence(context.Background(), compileContext{containingFlow: "/workspace/batch4a-failure.yaml"}, []model.Command{
		batch4AInputScalar("fail"), batch4AErase(int64(7)),
	})
	if err != nil {
		t.Fatal(err)
	}
	primary := errors.New("input adapter failed")
	driver := batch4ADriver()
	driver.Enqueue(enginetest.DriverScript{
		InputText:      []enginetest.Result[struct{}]{{Err: primary}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: []byte("batch4a-png")}},
	})
	sink := &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("batch4a/failure.png", int64(len("batch4a-png")))}}}
	events := make([]Event, 0, 4)
	root := &compiledFlow{
		path: "/workspace/batch4a-failure.yaml", config: model.Config{Name: "Batch 4A failure", AppID: "com.example.batch4a.failure"},
		body: compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-failure", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{}, ArtifactSink: sink,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error { events = append(events, event); return nil })},
	}, root, "batch4a-failure/root-run-000001")
	commands := result.Commands()
	var operation *OperationError
	if !errors.As(runErr, &operation) || errors.Unwrap(runErr) != primary || classifyTerminalError(runErr) != terminalErrorRetryable ||
		result.Outcome() != Failed || result.ProductError() != runErr || len(commands) != 1 ||
		commands[0].Command().Kind != model.CommandInputText || commands[0].Outcome() != Failed || commands[0].ProductError() != runErr {
		t.Fatalf("failure projection = result %#v commands %#v error %T %v", result, commands, runErr, runErr)
	}
	assertFailureEvidenceArtifacts(t, commands[0].Artifacts(), "batch4a/failure.png")
	if len(batch4AInputRequests(driver.Actions())) != 1 || len(batch4AEraseRequests(driver.Actions())) != 0 ||
		len(settleRequests(driver.Actions())) != 0 || countBatch2AMethod(driver.Actions(), enginetest.MethodTakeScreenshot) != 1 || len(sink.Requests()) != 1 {
		t.Fatalf("failure effects = actions %#v writes %#v", driver.Actions(), sink.Requests())
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Failed || events[2].ProductError() != runErr ||
		events[3].Outcome() != Failed || events[3].ProductError() != runErr {
		t.Fatalf("failure lifecycle = %#v", events)
	}
}

type batch4ABareSentinel struct{}

func batch4AInputScalar(text string) model.Command {
	return model.Command{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: text}
}

func batch4AInputObject(text string, label *string) model.Command {
	arguments := map[string]any{"text": text}
	if label != nil {
		arguments["label"] = *label
	}
	return model.Command{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: arguments, Label: clonePointer(label)}
}

func batch4AEraseBare() model.Command {
	return model.Command{Kind: model.CommandEraseText, Form: model.CommandFormScalar}
}

func batch4AErase(value any) model.Command {
	return model.Command{Kind: model.CommandEraseText, Form: model.CommandFormObject, Arguments: value}
}

func batch4AEvaluationTrace(trace *[]string, replacements map[string]string, appID string) evaluationContext {
	return evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			*trace = append(*trace, input)
			if replacement, ok := replacements[input]; ok {
				return replacement, nil
			}
			return input, nil
		},
		activeConfig: model.Config{AppID: appID}, hasActiveConfig: true,
	}
}

func executeBatch4AForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
) (commandEffect, evaluatedDispatch, error) {
	return executeBatch4AWithLookupForTest(ctx, command, replacements, driver, clock, NewElementLookup(driver, clock))
}

func executeBatch4AWithLookupForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
) (commandEffect, evaluatedDispatch, error) {
	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{containingFlow: "/workspace/batch4a.yaml"}, command)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, batch2AEvaluation(replacements, "com.example.batch4a"), compiled)
	if err != nil {
		return commandEffect{}, evaluated, err
	}
	state := &executionState{dependencies: Dependencies{Driver: driver, Clock: clock}, lookupFn: func() (*ElementLookup, error) { return lookup, nil }}
	effect, err := dispatcher.execute(ctx, state, compiled, evaluated)
	return effect, evaluated, err
}

func batch4ADriver() *enginetest.FakeDriver {
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	return batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}})
}

func batch4ADriverWithSettle(settle []enginetest.Result[*device.ViewHierarchy]) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 100, HeightGrid: 100}}},
		WaitForAppToSettle: settle,
	})
	return driver
}

func batch4AInputRequests(actions []enginetest.Action) []device.InputTextRequest {
	requests := make([]device.InputTextRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodInputText {
			requests = append(requests, action.Request.(device.InputTextRequest))
		}
	}
	return requests
}

func batch4AEraseRequests(actions []enginetest.Action) []device.EraseTextRequest {
	requests := make([]device.EraseTextRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodEraseText {
			requests = append(requests, action.Request.(device.EraseTextRequest))
		}
	}
	return requests
}

func batch4APhysicalCount(actions []enginetest.Action) int {
	return countBatch2AMethod(actions, enginetest.MethodInputText) + countBatch2AMethod(actions, enginetest.MethodEraseText)
}

func batch4AMethods(actions []enginetest.Action) []enginetest.Method {
	methods := make([]enginetest.Method, len(actions))
	for index := range actions {
		methods[index] = actions[index].Method
	}
	return methods
}

type batch4ACancelAfterInputDriver struct {
	device.Driver
	cancel context.CancelFunc
}

type batch4ACancelSettleDriver struct {
	device.Driver
	cancel context.CancelFunc
	mu     sync.Mutex
	calls  int
}

func (driver *batch4ACancelSettleDriver) WaitForAppToSettle(
	ctx context.Context,
	request device.SettleRequest,
) (*device.ViewHierarchy, error) {
	driver.mu.Lock()
	driver.calls++
	driver.mu.Unlock()
	hierarchy, err := driver.Driver.WaitForAppToSettle(ctx, request)
	driver.cancel()
	return hierarchy, err
}

func (driver *batch4ACancelSettleDriver) SettleCalls() int {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.calls
}

func (driver *batch4ACancelAfterInputDriver) InputText(ctx context.Context, request device.InputTextRequest) error {
	err := driver.Driver.InputText(ctx, request)
	driver.cancel()
	return err
}

func (driver *batch4ACancelAfterInputDriver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	err := driver.Driver.EraseText(ctx, request)
	driver.cancel()
	return err
}

type batch4AMutatingRequestDriver struct{ device.Driver }

func (driver *batch4AMutatingRequestDriver) InputText(ctx context.Context, request device.InputTextRequest) error {
	err := driver.Driver.InputText(ctx, request)
	request.Text = "MUTATED"
	if len(request.AppIDs) > 0 {
		request.AppIDs[0] = "mutated.by.driver"
	}
	return err
}

func (driver *batch4AMutatingRequestDriver) EraseText(ctx context.Context, request device.EraseTextRequest) error {
	err := driver.Driver.EraseText(ctx, request)
	request.CharactersToErase = 0
	if len(request.AppIDs) > 0 {
		request.AppIDs[0] = "mutated.by.driver"
	}
	return err
}

// scrollUntilVisible.speed is string-typed, so interpolation must survive
// compilation. The [1,100] bound is enforced after resolution.
func TestScrollUntilVisibleSpeedSurvivesCompilationAsText(t *testing.T) {
	t.Parallel()

	for _, speed := range []any{"${'40'}", "40", int64(40)} {
		compiled, err := compileScrollUntilVisible(batch3Command("Ready", map[string]any{"speed": speed}))
		if err != nil {
			t.Fatalf("compileScrollUntilVisible(speed=%v) error = %v", speed, err)
		}
		if compiled == nil {
			t.Fatalf("compileScrollUntilVisible(speed=%v) returned nil", speed)
		}
	}
}

// Scroll speed remains bounded to [1,100].
func TestScrollUntilVisibleSpeedIsStillBounded(t *testing.T) {
	t.Parallel()

	for _, speed := range []any{int64(0), int64(101), "0", "101"} {
		if _, err := compileScrollUntilVisible(
			batch3Command("Ready", map[string]any{"speed": speed})); err == nil {
			t.Fatalf("compileScrollUntilVisible(speed=%v) error = nil, want the [1,100] bound", speed)
		}
	}
}
