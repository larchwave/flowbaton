package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch5PrivateRegistryContract(t *testing.T) {
	t.Parallel()

	specs := clipboardHandlerSpecs()
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	got := make([]string, 0, len(registry.byKeyword))
	for keyword := range registry.byKeyword {
		got = append(got, string(keyword))
	}
	sort.Strings(got)
	want := []string{"copyTextFrom", "pasteText", "setClipboard"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("private registry = %#v, want %#v", got, want)
	}
	for _, keyword := range []model.CommandKeyword{
		model.CommandCopyTextFrom,
		model.CommandPasteText,
		model.CommandSetClipboard,
	} {
		spec, ok := registry.lookup(keyword)
		if !ok {
			t.Fatalf("private registry missing %s", keyword)
		}
		if keyword == model.CommandPasteText {
			if spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle || spec.settleRequest != nil {
				t.Fatalf("pasteText spec = %#v", spec)
			}
		} else if spec.effectClass != EffectHostMutation || spec.postAction != postActionUnspecified || spec.settleRequest != nil {
			t.Fatalf("%s spec = %#v", keyword, spec)
		}
	}
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	wantProduction := productionKeywordStrings()
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry = %#v, want exact thirty %#v", got, wantProduction)
	}
	// The production registry includes the complete clipboard command family.
	for _, keyword := range []model.CommandKeyword{model.CommandCopyTextFrom, model.CommandPasteText, model.CommandSetClipboard} {
		if _, exposed := production.lookup(keyword); !exposed {
			t.Fatalf("production registry must expose %s", keyword)
		}
	}
}

func TestInteractionBatch5StrictCompileGrammar(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "literal", "${VALUE}", "line\n世界"} {
		compiled, err := compileSetClipboard(batch5SetCommand(value))
		if err != nil || compiled.(setClipboardCompiled).authored != value {
			t.Fatalf("compileSetClipboard(%q) = %#v, %v", value, compiled, err)
		}
	}
	for _, command := range []model.Command{
		{Kind: model.CommandSetClipboard, Form: model.CommandFormScalar},
		{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: true},
		{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: int64(1)},
		{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: nil},
		{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: []any{"x"}},
		{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: map[string]any{"text": "x"}},
		batch5WithEnvelope(batch5SetCommand("x"), "label"),
		batch5WithEnvelope(batch5SetCommand("x"), "optional"),
		batch5WithEnvelope(batch5SetCommand("x"), "selector"),
		batch5WithEnvelope(batch5SetCommand("x"), "children"),
		batch5WithEnvelope(batch5SetCommand("x"), "condition"),
		batch5WithEnvelope(batch5SetCommand("x"), "links"),
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "x"},
	} {
		if _, err := compileSetClipboard(command); !isConfigurationError(err) {
			t.Fatalf("compileSetClipboard(%#v) error = %T %v", command, err, err)
		}
	}

	if _, err := compilePasteText(batch5PasteCommand()); err != nil {
		t.Fatalf("compilePasteText() error = %v", err)
	}
	for _, command := range []model.Command{
		{Kind: model.CommandPasteText, Form: model.CommandFormObject, Arguments: "x"},
		{Kind: model.CommandPasteText, Form: model.CommandFormObject, Arguments: map[string]any{}},
		{Kind: model.CommandPasteText, Form: model.CommandFormScalar, Arguments: "x"},
		batch5WithEnvelope(batch5PasteCommand(), "label"),
		batch5WithEnvelope(batch5PasteCommand(), "optional"),
		batch5WithEnvelope(batch5PasteCommand(), "selector"),
		batch5WithEnvelope(batch5PasteCommand(), "children"),
		batch5WithEnvelope(batch5PasteCommand(), "condition"),
		batch5WithEnvelope(batch5PasteCommand(), "links"),
		{Kind: model.CommandInputText, Form: model.CommandFormScalar},
	} {
		if _, err := compilePasteText(command); !isConfigurationError(err) {
			t.Fatalf("compilePasteText(%#v) error = %T %v", command, err, err)
		}
	}

	validCopy := []model.Command{
		batch5CopyTextCommand("Target", nil, nil),
		batch5CopyIDCommand("target", boolPointer(false), stringPointer("")),
		batch5CopyIDCommand("${ID}", boolPointer(true), stringPointer("copy ${ID}")),
		batch5CopyNestedLabelCommand(),
	}
	for _, command := range validCopy {
		if _, err := compileCopyTextFrom(command); err != nil {
			t.Fatalf("compileCopyTextFrom(%#v) error = %v", command, err)
		}
	}
	for _, command := range batch5InvalidCopyCommands() {
		if _, err := compileCopyTextFrom(command); !isConfigurationError(err) {
			t.Fatalf("compileCopyTextFrom(%#v) error = %T %v", command, err, err)
		}
	}
}

func TestInteractionBatch5LateEvaluationMetadataAndSourceOwnership(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)

	setSource := batch5SetCommand("${flowbaton.copiedText}-${VALUE}")
	setAuthored := cloneCommand(setSource)
	setCompiled, err := dispatcher.compile(context.Background(), compileContext{}, setSource)
	if err != nil {
		t.Fatal(err)
	}
	setTrace := make([]string, 0, 1)
	setEvaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			setTrace = append(setTrace, input)
			return "prior-current", nil
		},
	}, setCompiled)
	if err != nil || !reflect.DeepEqual(setTrace, []string{"${flowbaton.copiedText}-${VALUE}"}) ||
		setEvaluated.command.Arguments != "prior-current" || !reflect.DeepEqual(setSource, setAuthored) {
		t.Fatalf("set evaluation = %#v trace %#v source %#v error %v", setEvaluated, setTrace, setSource, err)
	}

	copySource := batch5CopyNestedCommand()
	copyAuthored := cloneCommand(copySource)
	copyCompiled, err := dispatcher.compile(context.Background(), compileContext{}, copySource)
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		"${ID}": "container", "${CHILD}": "Ready", "copy ${ID}": "copy container",
	}
	trace := make([]string, 0, 3)
	copyEvaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			trace = append(trace, input)
			if replacement, ok := replacements[input]; ok {
				return replacement, nil
			}
			return input, nil
		},
	}, copyCompiled)
	if err != nil {
		t.Fatal(err)
	}
	wantArguments := map[string]any{
		"id": "container", "containsChild": map[string]any{"text": "Ready"},
		"optional": true, "label": "copy container",
	}
	if !reflect.DeepEqual(trace, []string{"${ID}", "copy ${ID}", "${CHILD}"}) ||
		!reflect.DeepEqual(copyEvaluated.command.Arguments, wantArguments) ||
		copyEvaluated.command.Selector == nil || copyEvaluated.command.Selector.IDRegex == nil || *copyEvaluated.command.Selector.IDRegex != "container" ||
		copyEvaluated.command.Optional == nil || !*copyEvaluated.command.Optional ||
		copyEvaluated.command.Label == nil || *copyEvaluated.command.Label != "copy container" ||
		!reflect.DeepEqual(copySource, copyAuthored) {
		t.Fatalf("copy evaluation = %#v trace %#v source %#v", copyEvaluated.command, trace, copySource)
	}
	copyPlan := copyEvaluated.value.(copyTextFromEvaluated)
	*copyEvaluated.command.Selector.IDRegex = "mutated"
	if copyPlan.selector == nil || copyPlan.selector.IDRegex == nil || *copyPlan.selector.IDRegex != "container" {
		t.Fatalf("copy evaluated plan aliased metadata = %#v", copyPlan)
	}

	invalid := batch5CopyTextCommand("${TARGET}", nil, nil)
	compiledInvalid, err := dispatcher.compile(context.Background(), compileContext{}, invalid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(context.Context, string, map[string]any) (string, error) { return "[", nil },
	}, compiledInvalid); !isConfigurationError(err) {
		t.Fatalf("invalid late regex error = %T %v", err, err)
	}

	pasteCompiled, err := dispatcher.compile(context.Background(), compileContext{}, batch5PasteCommand())
	if err != nil {
		t.Fatal(err)
	}
	appID := []byte("com.example.batch5")
	pasteEvaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			return string(appID), nil
		},
		activeConfig: model.Config{AppID: "${APP}"}, hasActiveConfig: true,
	}, pasteCompiled)
	if err != nil {
		t.Fatal(err)
	}
	appID[0] = 'X'
	if got := pasteEvaluated.value.(pasteTextEvaluated).appID; got != "com.example.batch5" {
		t.Fatalf("paste app ID = %q", got)
	}
	if _, err := dispatcher.evaluate(context.Background(), evaluationContext{
		interpolateFn: func(context.Context, string, map[string]any) (string, error) { return " ", nil },
		activeConfig:  model.Config{AppID: "${APP}"}, hasActiveConfig: true,
	}, pasteCompiled); !isConfigurationError(err) {
		t.Fatalf("blank paste app ID error = %T %v", err, err)
	}
}

func TestInteractionBatch5SetOverwritePasteAndOwnedActionOrder(t *testing.T) {
	t.Parallel()

	commands := []model.Command{
		batch5SetCommand("first-${VALUE}"),
		batch5SetCommand("${flowbaton.copiedText}-second"),
		batch5PasteCommand(),
	}
	root, err := batch5CompileRoot(commands, model.Config{
		AppID: "com.example.batch5", Env: map[string]string{"VALUE": "env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	driver := batch5PasteDriver()
	events := make([]Event, 0, 8)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch5-set-paste", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch5-set-paste/root-run-000001")
	if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 3 {
		t.Fatalf("set/paste result = %#v error %v", result, runErr)
	}
	requests := batch5InputRequests(driver.Actions())
	if want := []device.InputTextRequest{{Text: "first-env-second", AppIDs: []string{"com.example.batch5"}}}; !reflect.DeepEqual(requests, want) {
		t.Fatalf("InputText requests = %#v, want %#v", requests, want)
	}
	if got := batch5Methods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{
		enginetest.MethodDeviceInfo, enginetest.MethodInputText,
		enginetest.MethodWaitForAppToSettle, enginetest.MethodWaitForAppToSettle,
	}) {
		t.Fatalf("action order = %#v", got)
	}
	if got, want := len(events), 8; got != want {
		t.Fatalf("events = %d, want %d", got, want)
	}
	for index, expected := range []string{"first-env", "first-env-second"} {
		evaluated, ok := result.Commands()[index].Metadata().EvaluatedCommand()
		if !ok || evaluated.Arguments != expected {
			t.Fatalf("set command %d metadata = %#v", index, evaluated)
		}
	}
	if !reflect.DeepEqual(commands[0], batch5SetCommand("first-${VALUE}")) ||
		!reflect.DeepEqual(commands[1], batch5SetCommand("${flowbaton.copiedText}-second")) {
		t.Fatalf("authored commands mutated = %#v", commands)
	}
}

func TestInteractionBatch5EmptyClearCopyPasteAndNestedScope(t *testing.T) {
	t.Parallel()

	t.Run("explicit empty clears stale bytes", func(t *testing.T) {
		root, err := batch5CompileRoot([]model.Command{
			batch5SetCommand("stale"), batch5SetCommand(""), batch5PasteCommand(),
		}, model.Config{AppID: "com.example.batch5.empty"})
		if err != nil {
			t.Fatal(err)
		}
		driver := batch5PasteDriver()
		result, runErr := executeCompiledRoot(context.Background(), Dependencies{
			Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		}, root)
		requests := batch5InputRequests(driver.Actions())
		if runErr != nil || result.Outcome() != Completed || len(requests) != 1 || requests[0].Text != "" {
			t.Fatalf("empty clear = result %#v requests %#v error %v", result, requests, runErr)
		}
	})

	t.Run("copy then paste", func(t *testing.T) {
		root, err := batch5CompileRoot([]model.Command{
			batch5SetCommand("prior"), batch5CopyIDCommand("target", nil, nil), batch5PasteCommand(),
		}, model.Config{AppID: "com.example.batch5.copy-paste"})
		if err != nil {
			t.Fatal(err)
		}
		driver := batch5CopyPasteDriver(map[string]string{"accessibilityText": "copied 世界"})
		result, runErr := executeCompiledRoot(context.Background(), Dependencies{
			Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		}, root)
		requests := batch5InputRequests(driver.Actions())
		if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 3 ||
			!reflect.DeepEqual(requests, []device.InputTextRequest{{Text: "copied 世界", AppIDs: []string{"com.example.batch5.copy-paste"}}}) {
			t.Fatalf("copy/paste = result %#v requests %#v error %v", result, requests, runErr)
		}
		if got := batch5Methods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{
			enginetest.MethodDeviceInfo,
			enginetest.MethodContentDescriptor, enginetest.MethodContentDescriptor,
			enginetest.MethodInputText,
			enginetest.MethodWaitForAppToSettle, enginetest.MethodWaitForAppToSettle,
		}) {
			t.Fatalf("copy/paste action order = %#v", got)
		}
	})

	t.Run("nested scope shares and restores runtime environment", func(t *testing.T) {
		registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
		if err != nil {
			t.Fatal(err)
		}
		dispatcher := newDispatcher(registry)
		compileOne := func(command model.Command) compiledDispatch {
			t.Helper()
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, command)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			return compiled
		}
		child := &compiledFlow{
			path:   "/workspace/batch5-child.yaml",
			config: model.Config{AppID: "com.example.batch5.nested", Env: map[string]string{"SCOPE": "nested"}},
			body: []compiledDispatch{
				compileOne(batch5SetCommand("${flowbaton.copiedText}-${SCOPE}")),
				compileOne(batch5PasteCommand()),
			},
		}
		outer := compiledDispatch{
			command: model.Command{Kind: model.CommandRunFlow, Form: model.CommandFormObject, Arguments: "child.yaml"},
			value:   child,
			spec: handlerSpec{
				keyword: model.CommandRunFlow, effectClass: EffectComposite, evaluate: identityEvaluator,
				execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
					_, executeErr := state.executeFlow(ctx, child, nil)
					return commandEffect{effectClass: EffectComposite}, executeErr
				},
			},
		}
		root := &compiledFlow{
			path:   "/workspace/batch5-root.yaml",
			config: model.Config{AppID: "com.example.batch5.nested", Env: map[string]string{"SCOPE": "root"}},
			body: []compiledDispatch{
				compileOne(batch5SetCommand("root")), outer, compileOne(batch5PasteCommand()),
			},
		}
		ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
		driver := batch5PasteDriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{
			{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
		})
		result, runErr := executeCompiledRoot(context.Background(), Dependencies{
			Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		}, root)
		requests := batch5InputRequests(driver.Actions())
		want := []device.InputTextRequest{
			{Text: "root-nested", AppIDs: []string{"com.example.batch5.nested"}},
			{Text: "root-nested", AppIDs: []string{"com.example.batch5.nested"}},
		}
		if runErr != nil || result.Outcome() != Completed || !reflect.DeepEqual(requests, want) {
			t.Fatalf("nested clipboard = result %#v requests %#v error %v", result, requests, runErr)
		}
	})
}

func TestInteractionBatch5CopyLookupStabilityPrecedenceAndOptionalPolicy(t *testing.T) {
	t.Parallel()

	precedence := []struct {
		name       string
		attributes map[string]string
		want       string
	}{
		{name: "text wins", attributes: map[string]string{"text": "text", "accessibilityText": "accessibility", "hintText": "hint"}, want: "text"},
		{name: "present empty text wins", attributes: map[string]string{"text": "", "accessibilityText": "accessibility", "hintText": "hint"}, want: ""},
		{name: "accessibility wins", attributes: map[string]string{"accessibilityText": "accessibility", "hintText": "hint"}, want: "accessibility"},
		{name: "hint fallback", attributes: map[string]string{"hintText": "hint"}, want: "hint"},
	}
	for _, test := range precedence {
		t.Run(test.name, func(t *testing.T) {
			driver, clock, lookup := batch5CopyLookup(test.attributes)
			var copied string
			calls := 0
			effect, evaluated, err := executeBatch5ForTest(
				context.Background(), batch5CopyIDCommand("target", nil, nil), nil,
				driver, clock, lookup, "seed", func(value string) error {
					calls++
					copied = strings.Clone(value)
					return nil
				},
			)
			if err != nil || effect.effectClass != EffectHostMutation || copied != test.want || calls != 1 {
				t.Fatalf("copy = effect %#v evaluated %#v copied %q calls %d error %v", effect, evaluated, copied, calls, err)
			}
			if got := batch5Methods(driver.Actions()); !reflect.DeepEqual(got, []enginetest.Method{
				enginetest.MethodDeviceInfo, enginetest.MethodContentDescriptor, enginetest.MethodContentDescriptor,
			}) {
				t.Fatalf("lookup/stability order = %#v", got)
			}
		})
	}

	t.Run("execution helper uses only accepted stable element", func(t *testing.T) {
		initial := batch5Element(map[string]string{"text": "pre-stability", "bounds": "[0,0][10,10]"})
		accepted := batch5Element(map[string]string{"text": "latest-stable", "bounds": "[5,5][15,15]"})
		selector := model.ElementSelector{IDRegex: stringPointer("owned-target"), Optional: boolPointer(true)}
		options := LookupOptions{Optional: true}
		lookup := &batch5CopyTextLookupFake{initial: initial, accepted: accepted}
		setterCalls := 0
		copied := "retained"
		err := copyTextFromLookupAndSet(context.Background(), lookup, selector, options, func(value string) error {
			setterCalls++
			copied = strings.Clone(value)
			return nil
		})
		if err != nil || setterCalls != 1 || copied != "latest-stable" {
			t.Fatalf("accepted element setter = copied %q calls %d error %v", copied, setterCalls, err)
		}
		if lookup.findCalls != 1 || !reflect.DeepEqual(lookup.selector, selector) ||
			lookup.selector.IDRegex == selector.IDRegex || lookup.selector.Optional == selector.Optional ||
			!reflect.DeepEqual(lookup.options, options) {
			t.Fatalf("Find = calls %d selector %#v options %#v", lookup.findCalls, lookup.selector, lookup.options)
		}
		if lookup.stabilityCalls != 1 || lookup.stabilityInput != initial {
			t.Fatalf("stability = calls %d input %p, want initial %p", lookup.stabilityCalls, lookup.stabilityInput, initial)
		}

		missingAccepted := &batch5CopyTextLookupFake{initial: initial}
		if err := copyTextFromLookupAndSet(context.Background(), missingAccepted, selector, options, func(string) error {
			t.Fatal("nil accepted element reached setter")
			return nil
		}); !isConfigurationError(err) {
			t.Fatalf("nil accepted element error = %T %v", err, err)
		}
	})

	// A target with no copyable attribute copies an empty string. Only a target
	// that cannot be found fails.
	for _, optional := range []bool{false, true} {
		name := "required no attribute copies the empty string"
		if optional {
			name = "optional no attribute copies the empty string"
		}
		t.Run(name, func(t *testing.T) {
			optionalFlag := (*bool)(nil)
			if optional {
				optionalFlag = boolPointer(true)
			}
			driver, clock, lookup := batch5CopyLookup(map[string]string{})
			setterCalls := 0
			copied := "retained"
			result, runErr := executeBatch5CoreCommandForTest(
				t, context.Background(), batch5CopyIDCommand("target", optionalFlag, nil),
				driver, clock, lookup, "retained", func(value string) error {
					setterCalls++
					copied = strings.Clone(value)
					return nil
				},
			)
			if runErr != nil || result.Outcome() != Completed || setterCalls != 1 || copied != "" {
				t.Fatalf("no attribute = result %#v copied %q calls %d error %v", result, copied, setterCalls, runErr)
			}
		})
	}

	for _, test := range []struct {
		name       string
		optional   bool
		attributes map[string]string
		missing    bool
		wantWarn   bool
	}{
		{name: "required missing", missing: true, wantWarn: false},
		{name: "optional missing", optional: true, missing: true, wantWarn: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			optional := (*bool)(nil)
			if test.optional {
				optional = boolPointer(true)
			}
			driver, clock, lookup := batch5CopyLookup(test.attributes)
			if test.missing {
				driver, clock, lookup = batch5MissingCopyLookup(test.optional)
			}
			setterCalls := 0
			command := batch5CopyIDCommand("target", optional, nil)
			result, runErr := executeBatch5CoreCommandForTest(t, context.Background(), command, driver, clock, lookup, "retained", func(string) error {
				setterCalls++
				return nil
			})
			if setterCalls != 0 {
				t.Fatalf("setter calls = %d, want zero", setterCalls)
			}
			if test.wantWarn {
				if runErr != nil || result.Outcome() != Warned || result.ProductError() == nil {
					t.Fatalf("optional result = %#v error %v", result, runErr)
				}
			} else if runErr == nil || result.Outcome() != Failed {
				t.Fatalf("required result = %#v error %v", result, runErr)
			}
		})
	}

	t.Run("optional terminal lookup is failed", func(t *testing.T) {
		terminal := NewConfigurationError("hierarchy unavailable", nil)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400}}},
			ContentDescriptor: []enginetest.Result[device.TreeNode]{{Err: terminal}},
		})
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		setterCalls := 0
		result, runErr := executeBatch5CoreCommandForTest(
			t, context.Background(), batch5CopyIDCommand("target", boolPointer(true), nil),
			driver, clock, lookup, "retained", func(string) error { setterCalls++; return nil },
		)
		if !isConfigurationError(runErr) || result.Outcome() != Failed || setterCalls != 0 {
			t.Fatalf("optional terminal = result %#v setter calls %d error %T %v", result, setterCalls, runErr, runErr)
		}
	})

	t.Run("optional cancellation during stability is cancelled without mutation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		driver, _, _ := batch5CopyLookup(map[string]string{"text": "never-copied"})
		clock := &batch1ACancelWaitClock{now: time.Unix(0, 0).UTC(), cancel: cancel}
		lookup := NewElementLookup(driver, clock)
		setterCalls := 0
		result, runErr := executeBatch5CoreCommandForTest(
			t, ctx, batch5CopyIDCommand("target", boolPointer(true), nil),
			driver, clock, lookup, "retained", func(string) error { setterCalls++; return nil },
		)
		if !errors.Is(runErr, context.Canceled) || result.Outcome() != Cancelled ||
			result.Outcome() == Warned || setterCalls != 0 {
			t.Fatalf("optional cancellation = result %#v setter calls %d error %T %v", result, setterCalls, runErr, runErr)
		}
	})

	t.Run("optional device failure is failed without mutation", func(t *testing.T) {
		terminal := NewDeviceConnectionError("device hierarchy unavailable", nil)
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: terminal}},
		})
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		setterCalls := 0
		result, runErr := executeBatch5CoreCommandForTest(
			t, context.Background(), batch5CopyIDCommand("target", boolPointer(true), nil),
			driver, clock, lookup, "retained", func(string) error { setterCalls++; return nil },
		)
		if runErr != terminal || result.ProductError() != terminal || result.Outcome() != Failed ||
			result.Outcome() == Warned || setterCalls != 0 {
			t.Fatalf("optional device = result %#v setter calls %d error %T %v", result, setterCalls, runErr, runErr)
		}
	})

	t.Run("optional stability failure is failed without mutation", func(t *testing.T) {
		terminal := NewConfigurationError("stability hierarchy unavailable", nil)
		driver := enginetest.NewFakeDriver()
		tree := device.TreeNode{
			Attributes: map[string]string{"bounds": "[0,0][200,400]"},
			Children: []device.TreeNode{{Attributes: map[string]string{
				"resource-id": "target", "bounds": "[10,20][80,60]", "text": "never-copied",
			}}},
		}
		driver.Enqueue(enginetest.DriverScript{
			DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
				Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400,
			}}},
			ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Err: terminal}},
		})
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		setterCalls := 0
		result, runErr := executeBatch5CoreCommandForTest(
			t, context.Background(), batch5CopyIDCommand("target", boolPointer(true), nil),
			driver, clock, lookup, "retained", func(string) error { setterCalls++; return nil },
		)
		if runErr != terminal || result.ProductError() != terminal || result.Outcome() != Failed ||
			result.Outcome() == Warned || setterCalls != 0 {
			t.Fatalf("optional stability = result %#v setter calls %d error %T %v", result, setterCalls, runErr, runErr)
		}
	})

	t.Run("optional setter integrity failure is failed", func(t *testing.T) {
		driver, clock, lookup := batch5CopyLookup(map[string]string{"text": "value"})
		primary := newSessionIntegrityError()
		result, runErr := executeBatch5CoreCommandForTest(
			t, context.Background(), batch5CopyIDCommand("target", boolPointer(true), nil),
			driver, clock, lookup, "retained", func(string) error { return primary },
		)
		if runErr != primary || result.ProductError() != primary || result.Outcome() != Failed {
			t.Fatalf("optional integrity = result %#v error %T %v", result, runErr, runErr)
		}
	})
}

func TestInteractionBatch5PasteInitialEmptyDriverCancellationAndSettlePolicy(t *testing.T) {
	t.Parallel()

	for _, initial := range []string{"", "copied 世界"} {
		driver := batch5PasteDriver()
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		effect, _, err := executeBatch5ForTest(
			context.Background(), batch5PasteCommand(), nil, driver, clock, lookup, initial, nil,
		)
		requests := batch5InputRequests(driver.Actions())
		if err != nil || effect.effectClass != EffectDeviceMutation || len(requests) != 1 ||
			requests[0].Text != initial || !reflect.DeepEqual(requests[0].AppIDs, []string{"com.example.batch5"}) {
			t.Fatalf("paste %q = effect %#v requests %#v error %v", initial, effect, requests, err)
		}
		if got, want := lookup.AdjustedTimeout(LookupOptions{}), LookupTimeout-2*HierarchySettlePollInterval; got != want {
			t.Fatalf("paste watermark adjusted timeout = %v, want %v", got, want)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	driver := batch5PasteDriver()
	clock := newAdvancingClock()
	lookup := NewElementLookup(driver, clock)
	_, _, err := executeBatch5ForTest(cancelled, batch5PasteCommand(), nil, driver, clock, lookup, "never", nil)
	if !errors.Is(err, context.Canceled) || len(batch5InputRequests(driver.Actions())) != 0 {
		t.Fatalf("pre-cancel = actions %#v error %T %v", driver.Actions(), err, err)
	}

	ctx, cancelAfterInput := context.WithCancel(context.Background())
	base := batch5PasteDriver()
	cancelDriver := &batch4ACancelAfterInputDriver{Driver: base, cancel: cancelAfterInput}
	clock = newAdvancingClock()
	lookup = NewElementLookup(cancelDriver, clock)
	_, _, err = executeBatch5ForTest(ctx, batch5PasteCommand(), nil, cancelDriver, clock, lookup, "value", nil)
	if !errors.Is(err, context.Canceled) || len(batch5InputRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 0 {
		t.Fatalf("post-InputText cancellation = actions %#v error %T %v", base.Actions(), err, err)
	}

	driverFailure := errors.New("input failed")
	driver = batch5PasteDriver()
	driver.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{{Err: driverFailure}}})
	clock = newAdvancingClock()
	lookup = NewElementLookup(driver, clock)
	_, _, err = executeBatch5ForTest(context.Background(), batch5PasteCommand(), nil, driver, clock, lookup, "value", nil)
	if !errors.Is(err, driverFailure) || len(batch5InputRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("Driver failure = actions %#v error %T %v", driver.Actions(), err, err)
	}

	settleFailure := NewConfigurationError("terminal settle failed", nil)
	driver = batch5PasteDriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Err: settleFailure}})
	clock = newAdvancingClock()
	lookup = NewElementLookup(driver, clock)
	_, _, err = executeBatch5ForTest(context.Background(), batch5PasteCommand(), nil, driver, clock, lookup, "value", nil)
	if !isConfigurationError(err) || len(batch5InputRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != 1 {
		t.Fatalf("terminal settle = actions %#v error %T %v", driver.Actions(), err, err)
	}

	driver = batch5PasteDriverWithSettle(make([]enginetest.Result[*device.ViewHierarchy], HierarchySettleAttempts))
	clock = newAdvancingClock()
	lookup = NewElementLookup(driver, clock)
	_, _, err = executeBatch5ForTest(context.Background(), batch5PasteCommand(), nil, driver, clock, lookup, "value", nil)
	if err != nil || len(batch5InputRequests(driver.Actions())) != 1 || len(settleRequests(driver.Actions())) != HierarchySettleAttempts {
		t.Fatalf("best-effort settle = actions %#v error %v", driver.Actions(), err)
	}
}

func batch5SetCommand(value string) model.Command {
	return model.Command{Kind: model.CommandSetClipboard, Form: model.CommandFormObject, Arguments: value}
}

func batch5PasteCommand() model.Command {
	return model.Command{Kind: model.CommandPasteText, Form: model.CommandFormScalar}
}

func batch5CopyTextCommand(text string, optional *bool, label *string) model.Command {
	command := model.Command{
		Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
		Arguments: text, Selector: &model.ElementSelector{TextRegex: stringPointer(text)},
	}
	if optional != nil || label != nil {
		fields := map[string]any{"text": text}
		if optional != nil {
			fields["optional"] = *optional
			command.Optional = clonePointer(optional)
			command.Selector.Optional = clonePointer(optional)
		}
		if label != nil {
			fields["label"] = *label
			command.Label = clonePointer(label)
			command.Selector.Label = clonePointer(label)
		}
		command.Arguments = fields
	}
	return command
}

func batch5CopyIDCommand(id string, optional *bool, label *string) model.Command {
	fields := map[string]any{"id": id}
	selector := &model.ElementSelector{IDRegex: stringPointer(id)}
	command := model.Command{Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject, Arguments: fields, Selector: selector}
	if optional != nil {
		fields["optional"] = *optional
		selector.Optional = clonePointer(optional)
		command.Optional = clonePointer(optional)
	}
	if label != nil {
		fields["label"] = *label
		selector.Label = clonePointer(label)
		command.Label = clonePointer(label)
	}
	return command
}

func batch5CopyNestedCommand() model.Command {
	optional := true
	label := "copy ${ID}"
	child := "${CHILD}"
	id := "${ID}"
	return model.Command{
		Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"id": id, "containsChild": map[string]any{"text": child},
			"optional": optional, "label": label,
		},
		Selector: &model.ElementSelector{
			IDRegex: stringPointer(id), ContainsChild: &model.ElementSelector{TextRegex: stringPointer(child)},
			Optional: &optional, Label: &label,
		},
		Optional: &optional, Label: &label,
	}
}

func batch5CopyNestedLabelCommand() model.Command {
	nestedLabel := "nested"
	return model.Command{
		Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
		Arguments: map[string]any{"id": "target", "containsChild": map[string]any{"text": "child", "label": nestedLabel}},
		Selector: &model.ElementSelector{
			IDRegex:       stringPointer("target"),
			ContainsChild: &model.ElementSelector{TextRegex: stringPointer("child"), Label: &nestedLabel},
		},
	}
}

func batch5WithEnvelope(command model.Command, field string) model.Command {
	switch field {
	case "label":
		command.Label = stringPointer("label")
	case "optional":
		command.Optional = boolPointer(true)
	case "selector":
		command.Selector = &model.ElementSelector{TextRegex: stringPointer("target")}
	case "children":
		command.Children = []model.Command{batch5PasteCommand()}
	case "condition":
		command.Condition = &model.Condition{}
	case "links":
		command.Links = []model.FileLink{{Kind: model.FileLinkFlow, Path: "foreign.yaml"}}
	}
	return command
}

func batch5InvalidCopyCommands() []model.Command {
	point := "10,20"
	start := "0,0"
	end := "100,100"
	settle := 1
	retry := true
	wait := true
	repeat := 2
	delay := 1
	nestedOptional := true
	return []model.Command{
		{Kind: model.CommandCopyTextFrom, Form: model.CommandFormScalar},
		{Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject, Arguments: true},
		{Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject, Arguments: map[string]any{}},
		{Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject, Arguments: "Target"},
		{Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject, Arguments: "Raw", Selector: &model.ElementSelector{TextRegex: stringPointer("Typed")}},
		batch5WithEnvelope(batch5CopyTextCommand("Target", nil, nil), "condition"),
		batch5WithEnvelope(batch5CopyTextCommand("Target", nil, nil), "links"),
		{Kind: model.CommandInputText, Form: model.CommandFormObject, Arguments: "Target", Selector: &model.ElementSelector{TextRegex: stringPointer("Target")}},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "point": point},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), Point: &point},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "start": start, "end": end},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), Start: &start, End: &end},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "waitToSettleTimeoutMs": int64(settle)},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), WaitToSettleTimeoutMS: &settle},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "retryTapIfNoChange": true},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), RetryTapIfNoChange: &retry},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "waitUntilVisible": true},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), WaitUntilVisible: &wait},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "repeat": int64(repeat)},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), Repeat: &repeat},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "delay": int64(delay)},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), Delay: &delay},
		},
		{
			Kind: model.CommandCopyTextFrom, Form: model.CommandFormObject,
			Arguments: map[string]any{"id": "target", "containsChild": map[string]any{"text": "child", "optional": true}},
			Selector:  &model.ElementSelector{IDRegex: stringPointer("target"), ContainsChild: &model.ElementSelector{TextRegex: stringPointer("child"), Optional: &nestedOptional}},
		},
	}
}

func batch5CompileRoot(commands []model.Command, config model.Config) (*compiledFlow, error) {
	registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
	if err != nil {
		return nil, err
	}
	compiled, err := newDispatcher(registry).compileSequence(context.Background(), compileContext{containingFlow: "/workspace/batch5.yaml"}, commands)
	if err != nil {
		return nil, err
	}
	return &compiledFlow{path: "/workspace/batch5.yaml", config: cloneConfig(config), body: compiled}, nil
}

func executeBatch5ForTest(
	ctx context.Context,
	command model.Command,
	replacements map[string]string,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
	copied string,
	setter func(string) error,
) (commandEffect, evaluatedDispatch, error) {
	registry, err := newHandlerRegistry(clipboardHandlerSpecs()...)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	evaluation := evaluationContext{
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			if replacement, ok := replacements[input]; ok {
				return replacement, nil
			}
			return input, nil
		},
		activeConfig: model.Config{AppID: "com.example.batch5"}, hasActiveConfig: true,
	}
	evaluated, err := dispatcher.evaluate(ctx, evaluation, compiled)
	if err != nil {
		return commandEffect{}, evaluated, err
	}
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		copiedTextFn: func() (string, error) { return strings.Clone(copied), nil },
		setCopiedTextFn: func(value string) error {
			if setter == nil {
				return NewConfigurationError("unexpected copied text mutation", nil)
			}
			return setter(strings.Clone(value))
		},
	}
	effect, err := dispatcher.execute(ctx, state, compiled, evaluated)
	return effect, evaluated, err
}

func executeBatch5CoreCommandForTest(
	t testing.TB,
	ctx context.Context,
	command model.Command,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
	copied string,
	setter func(string) error,
) (CommandResult, error) {
	t.Helper()
	dependencies := Dependencies{
		Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	core, err := newExecutorCore(dependencies, clipboardHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.lookupFn = func() (*ElementLookup, error) { return lookup, nil }
	core.state.copiedTextFn = func() (string, error) { return strings.Clone(copied), nil }
	core.state.setCopiedTextFn = setter
	core.state.currentConfigFn = func() (model.Config, error) { return model.Config{AppID: "com.example.batch5"}, nil }
	runtime, err := tapJSFactory(t).NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
	return core.execute(ctx, command, 0)
}

func batch5CopyLookup(attributes map[string]string) (*enginetest.FakeDriver, *advancingClock, *ElementLookup) {
	driver := enginetest.NewFakeDriver()
	owned := cloneStringMap(attributes)
	if owned == nil {
		owned = make(map[string]string)
	}
	owned["resource-id"] = "target"
	owned["bounds"] = "[10,20][80,60]"
	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][200,400]"},
		Children:   []device.TreeNode{{Attributes: owned}},
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}},
	})
	clock := newAdvancingClock()
	return driver, clock, NewElementLookup(driver, clock)
}

func batch5MissingCopyLookup(optional bool) (*enginetest.FakeDriver, *advancingClock, *ElementLookup) {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][200,400]"}}}},
	})
	clock := newAdvancingClock()
	lookup := NewElementLookup(driver, clock)
	budget := LookupTimeout
	if optional {
		budget = OptionalLookupTimeout
	}
	lookup.RecordInteraction(clock.Now().Add(-budget))
	return driver, clock, lookup
}

func batch5Element(attributes map[string]string) *hierarchy.Element {
	root, err := hierarchy.New(device.TreeNode{Attributes: cloneStringMap(attributes)})
	if err != nil {
		panic(err)
	}
	return root
}

type batch5CopyTextLookupFake struct {
	initial        *hierarchy.Element
	accepted       *hierarchy.Element
	findCalls      int
	selector       model.ElementSelector
	options        LookupOptions
	stabilityCalls int
	stabilityInput *hierarchy.Element
}

func (lookup *batch5CopyTextLookupFake) Find(
	_ context.Context,
	selector model.ElementSelector,
	options LookupOptions,
) (*hierarchy.Element, error) {
	lookup.findCalls++
	lookup.selector = selector
	lookup.options = options
	return lookup.initial, nil
}

func (lookup *batch5CopyTextLookupFake) WaitForElementStability(
	_ context.Context,
	element *hierarchy.Element,
) (ElementStabilityResult, error) {
	lookup.stabilityCalls++
	lookup.stabilityInput = element
	return ElementStabilityResult{Element: lookup.accepted}, nil
}

func batch5PasteDriver() *enginetest.FakeDriver {
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	return batch5PasteDriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}})
}

func batch5PasteDriverWithSettle(settle []enginetest.Result[*device.ViewHierarchy]) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:         []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 200, HeightGrid: 400}}},
		WaitForAppToSettle: settle,
	})
	return driver
}

func batch5CopyPasteDriver(attributes map[string]string) *enginetest.FakeDriver {
	driver := batch5PasteDriver()
	owned := cloneStringMap(attributes)
	if owned == nil {
		owned = make(map[string]string)
	}
	owned["resource-id"] = "target"
	owned["bounds"] = "[10,20][80,60]"
	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][200,400]"},
		Children:   []device.TreeNode{{Attributes: owned}},
	}
	driver.Enqueue(enginetest.DriverScript{
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}},
	})
	return driver
}

func batch5InputRequests(actions []enginetest.Action) []device.InputTextRequest {
	requests := make([]device.InputTextRequest, 0)
	for _, action := range actions {
		if action.Method == enginetest.MethodInputText {
			requests = append(requests, action.Request.(device.InputTextRequest))
		}
	}
	return requests
}

func batch5Methods(actions []enginetest.Action) []enginetest.Method {
	methods := make([]enginetest.Method, 0, len(actions))
	for _, action := range actions {
		methods = append(methods, action.Method)
	}
	return methods
}
