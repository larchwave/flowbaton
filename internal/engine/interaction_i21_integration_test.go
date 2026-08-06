package engine

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

var interactionI21FixtureSHA256 = map[string]string{
	"actions.yaml":             "a6e0970cbcb378d4e7952457df18ac5d4d1d74e38c5b12993dab0c676eea8f6a",
	"cancelled.yaml":           "c5cdf6ca13c519ad3ac9b158e3f23d92c9ae43c9fed328337d25df9d0549c52b",
	"clipboard.yaml":           "bf176649e70c739899ab25d3971364fe4f1e5b5bdd16b0a8952aff9ca6b38539",
	"deferred-ai-invalid.yaml": "1d45dc024ee530e61b758519d42dd9fec389366e114835a5a3780bf76b6fdd29",
	"failed.yaml":              "69ec4988abed16618b670142fc2aa184b6750b2843c7044947030958013d3859",
	"gestures.yaml":            "a2ae3b3587464569a94b056a82a095fb6470807052269ca6aed67ed430eed9f6",
	"happy.yaml":               "c444178db23f4aaf4787f82dec62bb6159217507d05a8bba5d05df65f0d337b2",
	"late-evaluation.yaml":     "817d4cb2e72f6b263f43f199fd040e00bb868bec2f936481dfd949d466091d61",
	"later-root-invalid.yaml":  "2c53c23e01f2993a5283eb63055253a89faa8c36c07d3f3e464c85ad12ea14bd",
	"scroll-search.yaml":       "81843487598ca93b74a90b19e75e5e5748e533daac1f66232162c46cad15ee8c",
	"static-invalid.yaml":      "f5e9a63658f7eeb9428df16802d2a54bc2ef28366f8056b3c2b17667b601a890",
	"text-random.yaml":         "f7f72bab21c20fb0016b93c4eebe062d46789c447ee233b52549c23777826ccc",
	"warned.yaml":              "de0cbd9ec4224c10bf0140bb857d3bc4eb4706e1bbe81858fd9e5446842ad66a",
}

var interactionI21AuthoredKeywords = map[string][]model.CommandKeyword{
	"actions.yaml":             {model.CommandSetClipboard, model.CommandBack, model.CommandAction, model.CommandHideKeyboard, model.CommandAction, model.CommandScroll, model.CommandAction, model.CommandPasteText, model.CommandAction, model.CommandPressKey},
	"cancelled.yaml":           {model.CommandBack, model.CommandScrollUntilVisible, model.CommandPressKey},
	"clipboard.yaml":           {model.CommandCopyTextFrom, model.CommandAssertTrue, model.CommandPasteText, model.CommandSetClipboard, model.CommandSetClipboard, model.CommandAssertTrue, model.CommandPasteText},
	"deferred-ai-invalid.yaml": {model.CommandBack, model.CommandInputText, model.CommandAssertWithAI},
	"failed.yaml":              {model.CommandSetClipboard, model.CommandCopyTextFrom, model.CommandPressKey},
	"gestures.yaml":            {model.CommandDoubleTapOn, model.CommandLongPressOn, model.CommandSwipe, model.CommandSwipe, model.CommandSwipe, model.CommandSwipe, model.CommandSwipe, model.CommandSwipe, model.CommandSwipe},
	"happy.yaml":               {model.CommandDoubleTapOn, model.CommandLongPressOn, model.CommandBack, model.CommandHideKeyboard, model.CommandPasteText, model.CommandScroll, model.CommandScrollUntilVisible, model.CommandInputText, model.CommandInputRandomText, model.CommandInputRandomNumber, model.CommandInputRandomEmail, model.CommandInputRandomPersonName, model.CommandInputRandomCityName, model.CommandInputRandomCountryName, model.CommandInputRandomColorName, model.CommandSwipe, model.CommandPressKey, model.CommandEraseText, model.CommandAction, model.CommandCopyTextFrom, model.CommandSetClipboard},
	"late-evaluation.yaml":     {model.CommandDoubleTapOn, model.CommandSwipe, model.CommandScrollUntilVisible, model.CommandInputText, model.CommandEraseText, model.CommandInputRandomText, model.CommandInputRandomNumber, model.CommandInputRandomEmail, model.CommandInputRandomPersonName, model.CommandInputRandomCityName, model.CommandInputRandomCountryName, model.CommandInputRandomColorName, model.CommandSetClipboard, model.CommandPasteText, model.CommandPressKey},
	"later-root-invalid.yaml":  {model.CommandAction},
	"scroll-search.yaml":       {model.CommandScrollUntilVisible, model.CommandScrollUntilVisible},
	"static-invalid.yaml":      {model.CommandAction},
	"text-random.yaml":         {model.CommandInputText, model.CommandEraseText, model.CommandInputRandomText, model.CommandInputRandomNumber, model.CommandInputRandomEmail, model.CommandInputRandomPersonName, model.CommandInputRandomCityName, model.CommandInputRandomCountryName, model.CommandInputRandomColorName},
	"warned.yaml":              {model.CommandSetClipboard, model.CommandCopyTextFrom, model.CommandPasteText, model.CommandPressKey},
}

func TestInteractionI21RegistryManifestAndPrepare(t *testing.T) {
	registry := interactionI21HandlerRegistry(t)
	wantProduction := productionKeywordStrings()
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry = %#v, want %#v", got, wantProduction)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("I21 registry = %#v, want the production set %#v", got, wantProduction)
	}
	registry.byKeyword[model.CommandBack] = handlerSpec{}
	if spec, ok := production.lookup(model.CommandAssertTrue); !ok || spec.keyword != model.CommandAssertTrue {
		t.Fatal("direct registry mutation escaped into production registry")
	}

	names := interactionI21FixtureNames()
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			path := interactionI21FixturePath(t, name)
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read fixed fixture %s: %v", path, readErr)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != interactionI21FixtureSHA256[name] {
				t.Fatalf("fixture %s SHA-256 = %s, want %s", name, got, interactionI21FixtureSHA256[name])
			}
			loader := &interactionI21CountingLoader{delegate: capability.FileLoader{}}
			flow, loadErr := loader.Load(context.Background(), path)
			if loadErr != nil {
				t.Fatalf("FileLoader.Load(%s) error = %v", name, loadErr)
			}
			if flow.Path != path || flow.Config.AppID == "" || flow.Config.Name == "" {
				t.Fatalf("loaded fixture identity = path %q config %#v", flow.Path, flow.Config)
			}
			gotKinds := interactionI21CommandKinds(flow.Commands)
			if !reflect.DeepEqual(gotKinds, interactionI21AuthoredKeywords[name]) {
				t.Fatalf("%s authored kinds = %#v, want %#v", name, gotKinds, interactionI21AuthoredKeywords[name])
			}
			before := cloneFlow(flow)
			program, prepareErr := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{path}}, loader)
			// Every interaction fixture prepares through the production registry.
			if prepareErr != nil || program == nil {
				t.Fatalf("Prepare(%s) = %#v, %v", name, program, prepareErr)
			}
			fresh, freshErr := capability.FileLoader{}.Load(context.Background(), path)
			if freshErr != nil || !reflect.DeepEqual(before, fresh) {
				t.Fatalf("fixture ownership changed: fresh %#v error %v", fresh, freshErr)
			}
		})
	}
}

func TestInteractionI21StaticInvalidCompilesBeforeEffects(t *testing.T) {
	program, loader := interactionI21Prepare(t, "static-invalid.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if compiled != nil || !isConfigurationError(err) {
		t.Fatalf("compile static invalid = %#v, %T %v", compiled, err, err)
	}
	if loader.Calls() == 0 {
		t.Fatal("expected ordinary FileLoader calls during Prepare")
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21LaterRootInvalidCompilesBeforeEarlierEffects(t *testing.T) {
	program, loader := interactionI21Prepare(t, "happy.yaml", "later-root-invalid.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if compiled != nil || !isConfigurationError(err) {
		t.Fatalf("compile valid+later-invalid = %#v, %T %v", compiled, err, err)
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21RejectedProgramsHaveNoReportableFlowResult(t *testing.T) {
	for _, fixture := range []string{"static-invalid.yaml", "later-root-invalid.yaml"} {
		t.Run(fixture, func(t *testing.T) {
			program, loader := interactionI21Prepare(t, fixture)
			compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
			if !isConfigurationError(err) || compiled != nil {
				t.Fatalf("rejected %s = compiled %#v error %T %v, want nil/no reportable FlowResult", fixture, compiled, err, err)
			}
			loader.AssertUnchanged(t)
		})
	}
}

func TestInteractionI21GesturesExactModesSettleAndOwnership(t *testing.T) {
	program, loader := interactionI21Prepare(t, "gestures.yaml")
	before := interactionI21ProgramSnapshot(program)
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatalf("compile gestures error = %v", err)
	}
	driver := interactionI21GestureDriver()
	clock := &batch1ATraceClock{now: time.Unix(1_700_000_000, 0).UTC()}
	events := make([]Event, 0, 20)
	peerEvents := make([]Event, 0, 20)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-gestures", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{
			ListenerFunc(func(_ context.Context, event Event) error { events = append(events, event); return nil }),
			ListenerFunc(func(_ context.Context, event Event) error { peerEvents = append(peerEvents, event); return nil }),
		},
	}, interactionI21FirstRoot(t, compiled), "i21-gestures/root-run-000001")
	if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 9 {
		t.Fatalf("gestures result = %#v, error %v", result, runErr)
	}
	if len(events) != 20 || len(peerEvents) != 20 {
		t.Fatalf("gesture events = %d peer events = %d, want 20 each", len(events), len(peerEvents))
	}
	elementSwipeFinished := events[18]
	peerElementSwipeFinished := peerEvents[18]
	elementSwipeEventCommand, eventOK := elementSwipeFinished.Command()
	peerElementSwipeEventCommand, peerEventOK := peerElementSwipeFinished.Command()
	if elementSwipeFinished.Kind() != EventCommandFinished || elementSwipeFinished.Sequence() != 9 || elementSwipeFinished.RootRunID() != "i21-gestures/root-run-000001" ||
		!eventOK || elementSwipeEventCommand.Kind != model.CommandSwipe || elementSwipeEventCommand.Selector == nil || elementSwipeEventCommand.Selector.TextRegex == nil || *elementSwipeEventCommand.Selector.TextRegex != "${CARD}" ||
		peerElementSwipeFinished.Kind() != EventCommandFinished || peerElementSwipeFinished.Sequence() != 9 || peerElementSwipeFinished.RootRunID() != "i21-gestures/root-run-000001" ||
		!peerEventOK || peerElementSwipeEventCommand.Kind != model.CommandSwipe || peerElementSwipeEventCommand.Selector == nil || peerElementSwipeEventCommand.Selector.TextRegex == nil || *peerElementSwipeEventCommand.Selector.TextRegex != "${CARD}" {
		t.Fatalf("gesture element-swipe finished events = %#v peer %#v", elementSwipeFinished, peerElementSwipeFinished)
	}
	*elementSwipeEventCommand.Selector.TextRegex = "caller-event-mutated"
	freshElementSwipeEventCommand, freshEventOK := events[18].Command()
	freshPeerElementSwipeEventCommand, freshPeerEventOK := peerEvents[18].Command()
	freshResultElementSwipeCommand := result.Commands()[8].Command()
	if !freshEventOK || freshElementSwipeEventCommand.Selector == nil || freshElementSwipeEventCommand.Selector.TextRegex == nil || *freshElementSwipeEventCommand.Selector.TextRegex != "${CARD}" ||
		!freshPeerEventOK || freshPeerElementSwipeEventCommand.Selector == nil || freshPeerElementSwipeEventCommand.Selector.TextRegex == nil || *freshPeerElementSwipeEventCommand.Selector.TextRegex != "${CARD}" ||
		freshResultElementSwipeCommand.Selector == nil || freshResultElementSwipeCommand.Selector.TextRegex == nil || *freshResultElementSwipeCommand.Selector.TextRegex != "${CARD}" {
		t.Fatalf("gesture event command ownership escaped: event %#v peer %#v result %#v", freshElementSwipeEventCommand, freshPeerElementSwipeEventCommand, freshResultElementSwipeCommand)
	}
	elementSwipeResult := result.Commands()[8]
	authoredElementSwipe := elementSwipeResult.Command()
	if authoredElementSwipe.Kind != model.CommandSwipe || authoredElementSwipe.Selector == nil || authoredElementSwipe.Selector.TextRegex == nil || *authoredElementSwipe.Selector.TextRegex != "${CARD}" {
		t.Fatalf("gesture element authored/evaluated selector: authored = %#v, want owned ${CARD}", authoredElementSwipe)
	}
	elementSwipeMetadata := elementSwipeResult.Metadata()
	evaluatedElementSwipe, ok := elementSwipeMetadata.EvaluatedCommand()
	if !ok || evaluatedElementSwipe.Selector == nil || evaluatedElementSwipe.Selector.TextRegex == nil || *evaluatedElementSwipe.Selector.TextRegex != "Gesture Card" {
		t.Fatalf("gesture element authored/evaluated selector: evaluated = %#v, want owned Gesture Card", evaluatedElementSwipe)
	}
	*authoredElementSwipe.Selector.TextRegex = "caller-authored-mutated"
	sameResultAuthoredElementSwipe := elementSwipeResult.Command()
	if sameResultAuthoredElementSwipe.Selector == nil || sameResultAuthoredElementSwipe.Selector.TextRegex == nil || *sameResultAuthoredElementSwipe.Selector.TextRegex != "${CARD}" {
		t.Fatalf("gesture CommandResult.Command ownership escaped: %#v", sameResultAuthoredElementSwipe)
	}
	*evaluatedElementSwipe.Selector.TextRegex = "caller-evaluated-mutated"
	sameMetadataEvaluatedElementSwipe, sameMetadataOK := elementSwipeMetadata.EvaluatedCommand()
	if !sameMetadataOK || sameMetadataEvaluatedElementSwipe.Selector == nil || sameMetadataEvaluatedElementSwipe.Selector.TextRegex == nil || *sameMetadataEvaluatedElementSwipe.Selector.TextRegex != "Gesture Card" {
		t.Fatalf("gesture CommandMetadata.EvaluatedCommand ownership escaped: %#v", sameMetadataEvaluatedElementSwipe)
	}
	freshElementSwipeResult := result.Commands()[8]
	freshAuthoredElementSwipe := freshElementSwipeResult.Command()
	freshEvaluatedElementSwipe, freshOK := freshElementSwipeResult.Metadata().EvaluatedCommand()
	if !freshOK || freshAuthoredElementSwipe.Selector == nil || freshAuthoredElementSwipe.Selector.TextRegex == nil || *freshAuthoredElementSwipe.Selector.TextRegex != "${CARD}" || freshEvaluatedElementSwipe.Selector == nil || freshEvaluatedElementSwipe.Selector.TextRegex == nil || *freshEvaluatedElementSwipe.Selector.TextRegex != "Gesture Card" {
		t.Fatalf("gesture element authored/evaluated selector snapshot ownership escaped: authored %#v evaluated %#v", freshAuthoredElementSwipe, freshEvaluatedElementSwipe)
	}
	if got, want := tapRequests(driver.Actions()), []device.TapRequest{{Point: device.Point{X: 10, Y: 20}}, {Point: device.Point{X: 10, Y: 20}}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("double-tap requests = %#v, want %#v", got, want)
	}
	if got, want := batch1ALongPressRequests(driver.Actions()), []device.LongPressRequest{{Point: device.Point{X: 120, Y: 353}, DurationMillis: 3000}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("long-press requests = %#v, want %#v", got, want)
	}
	requests := swipeRequests(driver.Actions())
	want := []device.SwipeRequest{
		{Direction: "UP", DurationMillis: 401},
		{Direction: "DOWN", DurationMillis: 402},
		{Direction: "LEFT", DurationMillis: 403},
		{Direction: "RIGHT", DurationMillis: 404},
		swipePointRequest(device.Point{X: 10, Y: 20}, device.Point{X: 90, Y: 80}, 405),
		swipePointRequest(device.Point{X: 40, Y: 176}, device.Point{X: 360, Y: 707}, 406),
		swipeElementRequest(device.Point{X: 140, Y: 260}, "LEFT", 407),
	}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("gesture swipe requests = %#v, want %#v", requests, want)
	}
	physicalAndSettle := make([]enginetest.Method, 0, 19)
	for _, action := range driver.Actions() {
		switch action.Method {
		case enginetest.MethodTap, enginetest.MethodLongPress, enginetest.MethodSwipe, enginetest.MethodWaitForAppToSettle:
			physicalAndSettle = append(physicalAndSettle, action.Method)
		}
	}
	wantPhysicalAndSettle := []enginetest.Method{
		enginetest.MethodTap, enginetest.MethodTap, enginetest.MethodWaitForAppToSettle,
		enginetest.MethodLongPress, enginetest.MethodWaitForAppToSettle,
	}
	for range 7 {
		wantPhysicalAndSettle = append(wantPhysicalAndSettle, enginetest.MethodSwipe, enginetest.MethodWaitForAppToSettle)
	}
	if !reflect.DeepEqual(physicalAndSettle, wantPhysicalAndSettle) {
		t.Fatalf("gesture per-command physical/settle order = %#v, want %#v", physicalAndSettle, wantPhysicalAndSettle)
	}
	settles := settleRequests(driver.Actions())
	if len(settles) != 9 {
		t.Fatalf("gesture settles = %#v, want nine", settles)
	}
	for index, settle := range settles {
		if settle.TimeoutMillis == nil || *settle.TimeoutMillis != 0 {
			t.Fatalf("settle %d timeout = %#v, want non-nil zero", index, settle.TimeoutMillis)
		}
	}
	if got := append([]time.Duration(nil), clock.waits...); !reflect.DeepEqual(got, []time.Duration{125 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond}) {
		t.Fatalf("gesture Clock waits = %#v, want authored inter-tap plus moving/stable element polls and no settle polls", got)
	}
	if len(events) != 20 {
		t.Fatalf("gesture events = %d, want 20", len(events))
	}
	if after := interactionI21ProgramSnapshot(program); !reflect.DeepEqual(after, before) {
		t.Fatal("gesture execution mutated prepared Program")
	}
	loader.AssertUnchanged(t)
	requests[6].ElementPoint.X = -1
	fresh := swipeRequests(driver.Actions())
	if fresh[6].ElementPoint == nil || fresh[6].ElementPoint.X != 140 {
		t.Fatalf("element point ownership escaped: %#v", fresh[6])
	}
}

func TestInteractionI21GestureWatermarkAndSettleErrorPolicy(t *testing.T) {
	t.Run("successful physical action records interaction before cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		base := batch1APointDriver(400, 884)
		driver := &batch1BCancelSwipeDriver{Driver: base, cancel: cancel}
		clock := newAdvancingClock()
		lookup := NewElementLookup(driver, clock)
		_, err := executeSwipeForTest(ctx, swipeCommand(map[string]any{"direction": "UP"}, nil), nil, driver, clock, lookup)
		if err != context.Canceled || !lookup.hasInteraction || !lookup.latestInteraction.Equal(clock.Now()) || len(swipeRequests(base.Actions())) != 1 || len(settleRequests(base.Actions())) != 0 {
			t.Fatalf("watermark/cancellation = error %v watermark %v/%t actions %#v", err, lookup.latestInteraction, lookup.hasInteraction, base.Actions())
		}
	})

	for _, test := range []struct {
		name      string
		settle    error
		wantClass terminalErrorClass
		ignored   bool
	}{
		{name: "ordinary ignored", settle: NewOperationError("not settled", errors.New("ordinary cause")), ignored: true},
		{name: "context cancellation propagated", settle: context.Canceled, wantClass: terminalErrorCancelled},
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
			} else if err != test.settle || classifyTerminalError(err) != test.wantClass {
				t.Fatalf("terminal settle = %T %v class %v, want exact %T %v class %v", err, err, classifyTerminalError(err), test.settle, test.settle, test.wantClass)
			}
			if got := driver.Actions(); len(got) != 2 || got[0].Method != enginetest.MethodSwipe || got[1].Method != enginetest.MethodWaitForAppToSettle {
				t.Fatalf("physical/settle ordering = %#v", got)
			}
		})
	}
}

func TestInteractionI21ClipboardJavaScriptSessionAndTrace(t *testing.T) {
	program, loader := interactionI21Prepare(t, "clipboard.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := interactionI21ClipboardDriver()
	delegate, err := js.NewFactory(js.Config{Random: deterministicRandom{}, CopiedText: stringPointer("global-poison")})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &interactionI21RecordingFactory{delegate: delegate}
	events := make([]Event, 0, 16)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-clipboard", Driver: driver, Clock: newAdvancingClock(), JSFactory: recorder, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error { events = append(events, event); return nil })},
	}, interactionI21FirstRoot(t, compiled), "i21-clipboard/root-run-000001")
	if runErr != nil || result.Outcome() != Completed {
		t.Fatalf("clipboard result = %#v, error %v", result, runErr)
	}
	commands := result.Commands()
	if len(commands) != 7 || !reflect.DeepEqual(interactionI21ResultKinds(commands), interactionI21AuthoredKeywords["clipboard.yaml"]) {
		t.Fatalf("clipboard command results = %#v", commands)
	}
	if got := batch5InputRequests(driver.Actions()); !reflect.DeepEqual(got, []device.InputTextRequest{
		{Text: "copied-from-target", AppIDs: []string{"com.example.g003.i21.clipboard"}},
		{Text: "override-two", AppIDs: []string{"com.example.g003.i21.clipboard"}},
	}) {
		t.Fatalf("clipboard paste requests = %#v", got)
	}
	if len(events) != 16 {
		t.Fatalf("clipboard events = %d, want 16", len(events))
	}
	wantTrace := []interactionI21RuntimeTrace{
		{Kind: "set", Value: "copied-from-target"},
		{Kind: "eval", Value: `flowbaton.copiedText === "copied-from-target"`, Result: "true"},
		{Kind: "set", Value: "override-one"},
		{Kind: "set", Value: "override-two"},
		{Kind: "eval", Value: `flowbaton.copiedText === "override-two"`, Result: "true"},
	}
	if recorder.runtimeCount != 1 || !reflect.DeepEqual(recorder.Trace(), wantTrace) {
		t.Fatalf("clipboard runtime trace/count = %#v/%d, want %#v/1", recorder.Trace(), recorder.runtimeCount, wantTrace)
	}
	for index, command := range commands {
		if command.Sequence() != uint64(index+1) || command.RootRunID() != result.RootRunID() || command.Outcome() != Completed {
			t.Fatalf("clipboard command %d = %#v", index, command)
		}
	}
	trace := recorder.Trace()
	trace[0].Value = "mutated"
	if got := recorder.Trace(); got[0].Value != "copied-from-target" {
		t.Fatalf("clipboard runtime trace ownership escaped = %#v", got)
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21HappyCompleteExact21Trace(t *testing.T) {
	program, loader := interactionI21Prepare(t, "happy.yaml")
	before := interactionI21ProgramSnapshot(program)
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := interactionI21HappyDriver()
	generator := &batch4HSequenceGenerator{values: []string{
		"rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color",
	}}
	events := make([]Event, 0, 44)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-happy", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: generator,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, interactionI21FirstRoot(t, compiled), "i21-happy/root-run-000001")
	if runErr != nil || result.Outcome() != Completed {
		t.Fatalf("happy root = %#v, error %v", result, runErr)
	}
	commands := result.Commands()
	wantKinds := interactionI21AuthoredKeywords["happy.yaml"]
	if len(commands) != 21 || !reflect.DeepEqual(interactionI21ResultKinds(commands), wantKinds) {
		t.Fatalf("happy exact-21 command order = %#v, want %#v", interactionI21ResultKinds(commands), wantKinds)
	}
	if len(events) != 44 {
		t.Fatalf("happy events = %d, want 44", len(events))
	}
	for index, command := range commands {
		started := events[1+2*index]
		finished := events[2+2*index]
		if command.Sequence() != uint64(index+1) || command.Depth() != 0 || command.RootRunID() != result.RootRunID() || command.Outcome() != Completed ||
			started.Kind() != EventCommandStarted || finished.Kind() != EventCommandFinished || started.Sequence() != command.Sequence() || finished.Sequence() != command.Sequence() || finished.Outcome() != command.Outcome() {
			t.Fatalf("happy command/event %d = command %#v started %#v finished %#v", index, command, started, finished)
		}
	}
	wantGenerated := []InputRequest{
		{Kind: InputText, Length: 3}, {Kind: InputNumber, Length: 4},
		{Kind: InputEmail, Length: inputNamedLengthSentinel}, {Kind: InputPersonName, Length: inputNamedLengthSentinel},
		{Kind: InputCityName, Length: inputNamedLengthSentinel}, {Kind: InputCountryName, Length: inputNamedLengthSentinel},
		{Kind: InputColorName, Length: inputNamedLengthSentinel},
	}
	if got := generator.Requests(); !reflect.DeepEqual(got, wantGenerated) {
		t.Fatalf("happy generator requests = %#v, want %#v", got, wantGenerated)
	}
	wantInputs := []string{"", "happy-text", "rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color"}
	inputs := batch5InputRequests(driver.Actions())
	gotInputs := make([]string, len(inputs))
	for index, input := range inputs {
		gotInputs[index] = input.Text
		if !reflect.DeepEqual(input.AppIDs, []string{"com.example.g003.i21.happy"}) {
			t.Fatalf("happy input %d app IDs = %#v", index, input.AppIDs)
		}
	}
	if !reflect.DeepEqual(gotInputs, wantInputs) {
		t.Fatalf("happy input trace = %#v, want %#v", gotInputs, wantInputs)
	}
	if got := swipeRequests(driver.Actions()); !reflect.DeepEqual(got, []device.SwipeRequest{{Direction: "RIGHT", DurationMillis: 450}}) {
		t.Fatalf("happy swipe trace = %#v", got)
	}
	if countInteractionI21Method(driver.Actions(), enginetest.MethodTap) != 2 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodLongPress) != 1 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodBackPress) != 2 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodHideKeyboard) != 1 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodScrollVertical) != 1 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodPressKey) != 1 ||
		countInteractionI21Method(driver.Actions(), enginetest.MethodEraseText) != 1 {
		t.Fatalf("happy physical trace = %#v", driver.Actions())
	}
	if after := interactionI21ProgramSnapshot(program); !reflect.DeepEqual(after, before) {
		t.Fatal("happy execution mutated prepared Program")
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21LateEvaluationCompleteSnapshots(t *testing.T) {
	program, loader := interactionI21Prepare(t, "late-evaluation.yaml")
	before := interactionI21ProgramSnapshot(program)
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := interactionI21LateEvaluationDriver()
	generator := &batch4HSequenceGenerator{values: []string{"rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color"}}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-late-snapshots", ExternalEnvironment: map[string]string{"APP_ID": "com.example.g003.i21.external", "CLIP": "external-clipboard"},
		Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: generator,
	}, interactionI21FirstRoot(t, compiled), "i21-late-snapshots/root-run-000001")
	if runErr != nil || result.Outcome() != Completed || len(result.Commands()) != 15 {
		t.Fatalf("late-evaluation result = %#v error %v", result, runErr)
	}
	evaluated := make([]model.Command, 15)
	for index, command := range result.Commands() {
		value, ok := command.Metadata().EvaluatedCommand()
		if !ok || strings.Contains(fmt.Sprintf("%#v", value), "${") {
			t.Fatalf("late evaluated command %d = %#v", index, value)
		}
		evaluated[index] = value
	}
	if fields := evaluated[0].Arguments.(map[string]any); fields["point"] != "15,25" || fields["delay"] != int64(10) || fields["waitToSettleTimeoutMs"] != int64(0) {
		t.Fatalf("late doubleTap snapshot = %#v", fields)
	}
	if fields := evaluated[1].Arguments.(map[string]any); fields["start"] != "10,20" || fields["end"] != "90,80" || fields["duration"] != int64(410) {
		t.Fatalf("late swipe snapshot = %#v", fields)
	}
	if fields := evaluated[2].Arguments.(map[string]any); fields["direction"] != "DOWN" || fields["timeout"] != "1000" || evaluated[2].Selector == nil || evaluated[2].Selector.TextRegex == nil || *evaluated[2].Selector.TextRegex != "Dynamic Target" {
		t.Fatalf("late scroll snapshot = %#v selector %#v", fields, evaluated[2].Selector)
	}
	if evaluated[3].Arguments != "dynamic-text" || evaluated[4].Arguments != "2" || evaluated[12].Arguments != "external-clipboard" || evaluated[14].Arguments != "HOME" {
		t.Fatalf("late scalar snapshots = input %#v erase %#v clipboard %#v key %#v", evaluated[3], evaluated[4], evaluated[12], evaluated[14])
	}
	if fields := evaluated[5].Arguments.(map[string]any); fields["length"] != int64(3) {
		t.Fatalf("late random text snapshot = %#v", fields)
	}
	if fields := evaluated[6].Arguments.(map[string]any); fields["length"] != int64(4) {
		t.Fatalf("late random number snapshot = %#v", fields)
	}
	wantNamedKinds := []model.CommandKeyword{
		model.CommandInputRandomEmail, model.CommandInputRandomPersonName, model.CommandInputRandomCityName,
		model.CommandInputRandomCountryName, model.CommandInputRandomColorName,
	}
	for offset, kind := range wantNamedKinds {
		command := evaluated[7+offset]
		if command.Kind != kind || command.Form != model.CommandFormScalar || command.Arguments != nil {
			t.Fatalf("late named random %d snapshot = %#v, want scalar %s with nil arguments", offset, command, kind)
		}
	}
	wantGeneratorRequests := []InputRequest{
		{Kind: InputText, Length: 3}, {Kind: InputNumber, Length: 4},
		{Kind: InputEmail, Length: inputNamedLengthSentinel}, {Kind: InputPersonName, Length: inputNamedLengthSentinel},
		{Kind: InputCityName, Length: inputNamedLengthSentinel}, {Kind: InputCountryName, Length: inputNamedLengthSentinel},
		{Kind: InputColorName, Length: inputNamedLengthSentinel},
	}
	if got := generator.Requests(); !reflect.DeepEqual(got, wantGeneratorRequests) {
		t.Fatalf("late generator requests = %#v, want %#v", got, wantGeneratorRequests)
	}
	inputs := batch5InputRequests(driver.Actions())
	wantTexts := []string{"dynamic-text", "rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color", "external-clipboard"}
	for index, input := range inputs {
		if input.Text != wantTexts[index] || !reflect.DeepEqual(input.AppIDs, []string{"com.example.g003.i21.external"}) {
			t.Fatalf("late input %d = %#v, want text %q/external app", index, input, wantTexts[index])
		}
	}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, []device.TapRequest{{Point: device.Point{X: 15, Y: 25}}, {Point: device.Point{X: 15, Y: 25}}}) {
		t.Fatalf("late tap trace = %#v", got)
	}
	if got := swipeRequests(driver.Actions()); !reflect.DeepEqual(got, []device.SwipeRequest{swipePointRequest(device.Point{X: 10, Y: 20}, device.Point{X: 90, Y: 80}, 410)}) {
		t.Fatalf("late swipe trace = %#v", got)
	}
	if after := interactionI21ProgramSnapshot(program); !reflect.DeepEqual(after, before) {
		t.Fatal("late evaluation mutated prepared Program")
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21ActionsDirectAliasPhysicalEquivalence(t *testing.T) {
	program, loader := interactionI21Prepare(t, "actions.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := interactionI21ActionsDriver()
	events := make([]Event, 0, 22)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-actions", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, interactionI21FirstRoot(t, compiled), "i21-actions/root-run-000001")
	if runErr != nil || result.Outcome() != Completed {
		t.Fatalf("actions root = %#v, error %v", result, runErr)
	}
	commands := result.Commands()
	if !reflect.DeepEqual(interactionI21ResultKinds(commands), interactionI21AuthoredKeywords["actions.yaml"]) || len(events) != 22 {
		t.Fatalf("actions results/events = kinds %#v events %d", interactionI21ResultKinds(commands), len(events))
	}
	actions := driver.Actions()
	for _, method := range []enginetest.Method{enginetest.MethodBackPress, enginetest.MethodHideKeyboard, enginetest.MethodScrollVertical, enginetest.MethodInputText} {
		requests := interactionI21RequestsForMethod(actions, method)
		if len(requests) != 2 || !reflect.DeepEqual(requests[0], requests[1]) {
			t.Fatalf("direct/action %s requests = %#v, want exact pair", method, requests)
		}
	}
	if got := batch5InputRequests(actions); !reflect.DeepEqual(got, []device.InputTextRequest{
		{Text: "action-seed", AppIDs: []string{"com.example.g003.i21.actions"}},
		{Text: "action-seed", AppIDs: []string{"com.example.g003.i21.actions"}},
	}) {
		t.Fatalf("direct/action paste requests = %#v", got)
	}
	for _, index := range []int{2, 4, 6, 8} {
		evaluated, ok := commands[index].Metadata().EvaluatedCommand()
		if !ok || evaluated.Kind != model.CommandAction || commands[index].Command().Kind != model.CommandAction {
			t.Fatalf("action projection %d = command %#v evaluated %#v", index, commands[index].Command(), evaluated)
		}
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21ScrollSearchExactTrace(t *testing.T) {
	program, loader := interactionI21Prepare(t, "scroll-search.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := enginetest.NewFakeDriver()
	info := device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	immediate := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"text": "Immediate Target", "bounds": "[0,0][400,884]"}}}}
	missing := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}}
	delayed := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"text": "Delayed Target", "bounds": "[0,100][400,300]"}}}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: info}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: immediate}, {Value: missing}, {Value: delayed}},
	})
	clock := newBatch3Clock(time.Unix(2_000, 0).UTC(), true)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-scroll-search", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, interactionI21FirstRoot(t, compiled), "i21-scroll-search/root-run-000001")
	if runErr != nil || result.Outcome() != Completed || !reflect.DeepEqual(interactionI21ResultKinds(result.Commands()), interactionI21AuthoredKeywords["scroll-search.yaml"]) {
		t.Fatalf("scroll-search result = %#v, error %v", result, runErr)
	}
	requests := batch3ScrollRequests(driver.Actions())
	if len(requests) != 2 || requests[0].Direction != "DOWN" || requests[0].Amount != 0.6 || requests[0].ElementPoint != nil ||
		requests[1].Direction != "DOWN" || requests[1].ElementPoint == nil || *requests[1].ElementPoint != (device.Point{X: 200, Y: 200}) {
		t.Fatalf("scroll-search requests = %#v", requests)
	}
	wantCenterAmount := float64(242) / float64(884)
	if requests[1].Amount != wantCenterAmount {
		t.Fatalf("center amount = %v, want %v", requests[1].Amount, wantCenterAmount)
	}
	settles := batch3SettleRequests(driver.Actions())
	if len(settles) != 2 {
		t.Fatalf("scroll-search settles = %#v, want two", settles)
	}
	for _, settle := range settles {
		if settle.TimeoutMillis == nil || *settle.TimeoutMillis != 0 || settle.AppID != "com.example.g003.i21.scroll-search" {
			t.Fatalf("scroll-search settle = %#v", settle)
		}
	}
	if waits := clock.Waits(); len(waits) != 0 {
		t.Fatalf("explicit-zero scroll settles polled Clock: %#v", waits)
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21CancelledScrollStopsBeforePoll(t *testing.T) {
	program, loader := interactionI21Prepare(t, "cancelled.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	base := interactionI21MissingTargetDriver(8)
	clock := &interactionI21PostScrollWaitClock{Clock: newAdvancingClock()}
	driver := &interactionI21CancelAfterScrollDriver{Driver: base, clock: clock, cancel: cancel}
	result, runErr := executeCompiledRootForRun(ctx, Dependencies{
		ExecutionID: "i21-cancelled-scroll", Driver: driver, Clock: clock, JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, interactionI21FirstRoot(t, compiled), "i21-cancelled-scroll/root-run-000001")
	commands := result.Commands()
	if runErr != context.Canceled || result.Outcome() != Cancelled || result.ProductError() != runErr || len(commands) != 2 ||
		commands[0].Command().Kind != model.CommandBack || commands[0].Outcome() != Completed ||
		commands[1].Command().Kind != model.CommandScrollUntilVisible || commands[1].Outcome() != Cancelled || commands[1].ProductError() != runErr {
		t.Fatalf("cancelled scroll cutoff = error %p %v result %#v", runErr, runErr, result)
	}
	methods := batch3Methods(base.Actions())
	wantMethods := []enginetest.Method{enginetest.MethodDeviceInfo, enginetest.MethodBackPress}
	for range 10 {
		wantMethods = append(wantMethods, enginetest.MethodWaitForAppToSettle)
	}
	wantMethods = append(wantMethods, enginetest.MethodContentDescriptor, enginetest.MethodScrollVertical)
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("cancelled scroll Driver trace = %#v, want %#v", methods, wantMethods)
	}
	if waits := clock.PostScrollWaits(); len(waits) != 0 {
		t.Fatalf("cancelled scroll post-Driver Clock.Wait attempts = %#v, want none", waits)
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21TextRandomExactGeneratorAndDriverTrace(t *testing.T) {
	program, loader := interactionI21Prepare(t, "text-random.yaml")
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	driver := batch4ADriver()
	generator := &batch4HSequenceGenerator{values: []string{"abcde", "123456", "mail@test", "Person", "City", "Country", "Color"}}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "i21-text-random", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: generator,
	}, interactionI21FirstRoot(t, compiled), "i21-text-random/root-run-000001")
	if runErr != nil || result.Outcome() != Completed || !reflect.DeepEqual(interactionI21ResultKinds(result.Commands()), interactionI21AuthoredKeywords["text-random.yaml"]) {
		t.Fatalf("text-random result = %#v, error %v", result, runErr)
	}
	if got := generator.Requests(); !reflect.DeepEqual(got, []InputRequest{
		{Kind: InputText, Length: 5}, {Kind: InputNumber, Length: 6},
		{Kind: InputEmail, Length: inputNamedLengthSentinel}, {Kind: InputPersonName, Length: inputNamedLengthSentinel},
		{Kind: InputCityName, Length: inputNamedLengthSentinel}, {Kind: InputCountryName, Length: inputNamedLengthSentinel},
		{Kind: InputColorName, Length: inputNamedLengthSentinel},
	}) {
		t.Fatalf("text-random generator requests = %#v", got)
	}
	inputs := batch4AInputRequests(driver.Actions())
	wantTexts := []string{"authored-text", "abcde", "123456", "mail@test", "Person", "City", "Country", "Color"}
	gotTexts := make([]string, len(inputs))
	for index, input := range inputs {
		gotTexts[index] = input.Text
	}
	if !reflect.DeepEqual(gotTexts, wantTexts) || !reflect.DeepEqual(batch4AEraseRequests(driver.Actions()), []device.EraseTextRequest{{CharactersToErase: 3, AppIDs: []string{"com.example.g003.i21.text-random"}}}) {
		t.Fatalf("text-random Driver trace = inputs %#v erase %#v", inputs, batch4AEraseRequests(driver.Actions()))
	}
	loader.AssertUnchanged(t)
}

func interactionI21HandlerRegistry(t testing.TB) handlerRegistry {
	t.Helper()
	// Use the production registry directly and assert its complete command set.
	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, productionKeywordStrings()) {
		t.Fatalf("production registry = %#v, want the complete set", got)
	}
	return registry
}

func interactionI21FixtureNames() []string {
	names := make([]string, 0, len(interactionI21FixtureSHA256))
	for name := range interactionI21FixtureSHA256 {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func interactionI21FixturePath(t testing.TB, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "flows", "g003-interaction", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

type interactionI21PostScrollWaitClock struct {
	Clock
	postScroll bool
	waits      []time.Duration
}

func (clock *interactionI21PostScrollWaitClock) Wait(ctx context.Context, delay time.Duration) error {
	if clock.postScroll {
		clock.waits = append(clock.waits, delay)
	}
	return clock.Clock.Wait(ctx, delay)
}

func (clock *interactionI21PostScrollWaitClock) PostScrollWaits() []time.Duration {
	return append([]time.Duration(nil), clock.waits...)
}

type interactionI21CancelAfterScrollDriver struct {
	device.Driver
	clock  *interactionI21PostScrollWaitClock
	cancel context.CancelFunc
}

func (driver *interactionI21CancelAfterScrollDriver) ScrollVertical(_ context.Context, request device.ScrollVerticalRequest) error {
	err := driver.Driver.ScrollVertical(context.Background(), request)
	driver.clock.postScroll = true
	driver.cancel()
	return err
}

func interactionI21Prepare(t testing.TB, names ...string) (*Program, *interactionI21CountingLoader) {
	t.Helper()
	selected := make([]string, len(names))
	for index, name := range names {
		selected[index] = interactionI21FixturePath(t, name)
	}
	loader := &interactionI21CountingLoader{delegate: capability.FileLoader{}}
	program, err := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: selected}, loader)
	if err != nil {
		t.Fatalf("Prepare(%v) error = %v", names, err)
	}
	loader.Freeze()
	return program, loader
}

type interactionI21CountingLoader struct {
	delegate capability.FileLoader
	mu       sync.Mutex
	calls    int
	frozen   int
}

func (loader *interactionI21CountingLoader) Canonical(ctx context.Context, path string) (string, error) {
	loader.mu.Lock()
	loader.calls++
	loader.mu.Unlock()
	return loader.delegate.Canonical(ctx, path)
}

func (loader *interactionI21CountingLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	loader.mu.Lock()
	loader.calls++
	loader.mu.Unlock()
	return loader.delegate.Load(ctx, path)
}

func (loader *interactionI21CountingLoader) Calls() int {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.calls
}
func (loader *interactionI21CountingLoader) Freeze() {
	loader.mu.Lock()
	loader.frozen = loader.calls
	loader.mu.Unlock()
}
func (loader *interactionI21CountingLoader) AssertUnchanged(t testing.TB) {
	t.Helper()
	loader.mu.Lock()
	defer loader.mu.Unlock()
	if loader.calls != loader.frozen {
		t.Fatalf("loader calls after Prepare = %d, frozen at %d", loader.calls, loader.frozen)
	}
}

func interactionI21CommandKinds(commands []model.Command) []model.CommandKeyword {
	kinds := make([]model.CommandKeyword, len(commands))
	for index, command := range commands {
		kinds[index] = command.Kind
	}
	return kinds
}

func interactionI21ResultKinds(commands []CommandResult) []model.CommandKeyword {
	kinds := make([]model.CommandKeyword, len(commands))
	for index, command := range commands {
		kinds[index] = command.Command().Kind
	}
	return kinds
}

func interactionI21FirstRoot(t testing.TB, compiled *compiledProgram) *compiledFlow {
	t.Helper()
	if compiled == nil || len(compiled.Roots()) != 1 {
		t.Fatalf("compiled roots = %#v", compiled)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	return root
}

func interactionI21ProgramSnapshot(program *Program) []model.Flow {
	paths := program.FlowPaths()
	flows := make([]model.Flow, len(paths))
	for index, path := range paths {
		flows[index], _ = program.Flow(path)
	}
	return flows
}

func interactionI21GestureDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 32)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	initial := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"text": "Gesture Card", "bounds": "[100,200][140,240]"}}}}
	moved := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"text": "Gesture Card", "bounds": "[100,200][180,320]"}}}}
	descriptors := []enginetest.Result[device.TreeNode]{{Value: initial}, {Value: moved}, {Value: moved}}
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: infos, ContentDescriptor: descriptors})
	return driver
}

func interactionI21ClipboardDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 16)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	element := device.TreeNode{Attributes: map[string]string{"resource-id": "copy-target", "text": "copied-from-target", "accessibilityText": "wrong-lower-precedence", "bounds": "[10,20][80,60]"}}
	tree := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{element}}
	descriptors := make([]enginetest.Result[device.TreeNode], 4)
	for index := range descriptors {
		descriptors[index].Value = tree
	}
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: infos, ContentDescriptor: descriptors})
	return driver
}

type interactionI21RuntimeTrace struct {
	Kind   string
	Value  string
	Result string
}

type interactionI21RecordingFactory struct {
	delegate     js.Factory
	runtimeCount int
	trace        []interactionI21RuntimeTrace
}

func (factory *interactionI21RecordingFactory) NewRuntime() (js.Runtime, error) {
	runtime, err := factory.delegate.NewRuntime()
	if err != nil {
		return nil, err
	}
	factory.runtimeCount++
	return &interactionI21RecordingRuntime{Runtime: runtime, factory: factory}, nil
}

func (factory *interactionI21RecordingFactory) Trace() []interactionI21RuntimeTrace {
	return append([]interactionI21RuntimeTrace(nil), factory.trace...)
}

type interactionI21RecordingRuntime struct {
	js.Runtime
	factory *interactionI21RecordingFactory
}

func (runtime *interactionI21RecordingRuntime) SetCopiedText(value string) error {
	if err := runtime.Runtime.SetCopiedText(value); err != nil {
		return err
	}
	runtime.factory.trace = append(runtime.factory.trace, interactionI21RuntimeTrace{Kind: "set", Value: value})
	return nil
}

func (runtime *interactionI21RecordingRuntime) Evaluate(ctx context.Context, request js.EvalRequest) (js.Result, error) {
	result, err := runtime.Runtime.Evaluate(ctx, request)
	if err == nil {
		runtime.factory.trace = append(runtime.factory.trace, interactionI21RuntimeTrace{Kind: "eval", Value: request.Script, Result: result.Text})
	}
	return result, err
}

func interactionI21HappyDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 64)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	ready := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"text": "Ready", "bounds": "[0,0][400,884]"}}}}
	copyTarget := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{{Attributes: map[string]string{"resource-id": "copy-target", "text": "happy-copy", "bounds": "[10,20][80,60]"}}}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        infos,
		IsKeyboardVisible: []enginetest.Result[bool]{{Value: true}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: ready}, {Value: ready}, {Value: copyTarget}, {Value: copyTarget}},
	})
	return driver
}

func interactionI21ActionsDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 32)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: infos, IsKeyboardVisible: []enginetest.Result[bool]{{Value: true}, {Value: true}}})
	return driver
}

func countInteractionI21Method(actions []enginetest.Action, method enginetest.Method) int {
	count := 0
	for _, action := range actions {
		if action.Method == method {
			count++
		}
	}
	return count
}

func interactionI21RequestsForMethod(actions []enginetest.Action, method enginetest.Method) []any {
	requests := make([]any, 0, 2)
	for _, action := range actions {
		if action.Method == method {
			requests = append(requests, action.Request)
		}
	}
	return requests
}
