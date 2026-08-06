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
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch1BActualRootDynamicFailurePreservesCompletedPrefix(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	commands := []model.Command{
		batch1ACommand(model.CommandDoubleTapOn, "", "20,30", nil, stringPointer("completed-prefix"), intPointerForTap(0), intPointerForTap(0)),
		swipeCommand(map[string]any{"direction": "${DIRECTION}", "waitToSettleTimeoutMs": int64(0)}, nil),
		batch1ACommand(model.CommandLongPressOn, "", "40,50", nil, stringPointer("must-not-run"), nil, intPointerForTap(0)),
	}
	compiled, err := newDispatcher(registry).compileSequence(context.Background(), compileContext{containingFlow: "/workspace/comp-008.yaml"}, commands)
	if err != nil {
		t.Fatalf("compileSequence() error = %v", err)
	}
	driver := batch1APointDriver(400, 884)
	clock := &batch1ATraceClock{now: time.Unix(1500, 0).UTC()}
	events := make([]Event, 0, 6)
	dependencies := Dependencies{
		ExecutionID: "comp-008", Driver: driver, Clock: clock,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	root := &compiledFlow{
		path:   "/workspace/comp-008.yaml",
		config: model.Config{AppID: "com.example.comp008", Env: map[string]string{"DIRECTION": "sideways"}},
		body:   compiled,
	}
	result, runErr := executeCompiledRootForRun(context.Background(), dependencies, root, "comp-008/root-run-000001")
	var configuration *ConfigurationError
	if !errors.As(runErr, &configuration) || classifyTerminalError(runErr) != terminalErrorConfiguration ||
		runErr.Error() != "command swipe direction must be exactly UP, DOWN, LEFT, or RIGHT" || errors.Unwrap(runErr) != nil {
		t.Fatalf("dynamic swipe error = %T %v class %v cause %v", runErr, runErr, classifyTerminalError(runErr), errors.Unwrap(runErr))
	}
	results := result.Commands()
	if result.Outcome() != Failed || result.ProductError() != runErr || len(results) != 2 ||
		results[0].Outcome() != Completed || results[0].ProductError() != nil ||
		results[1].Outcome() != Failed || results[1].ProductError() != runErr {
		t.Fatalf("root/result cutoff = outcome %q error %T commands %#v", result.Outcome(), result.ProductError(), results)
	}
	firstEvaluated, firstEvaluatedOK := results[0].Metadata().EvaluatedCommand()
	if !firstEvaluatedOK || firstEvaluated.Label == nil || *firstEvaluated.Label != "completed-prefix" {
		t.Fatalf("completed prefix evaluated snapshot = %#v", firstEvaluated)
	}
	firstEvaluated.Label = stringPointer("caller-mutated")
	freshFirstEvaluated, freshFirstEvaluatedOK := result.Commands()[0].Metadata().EvaluatedCommand()
	if !freshFirstEvaluatedOK || freshFirstEvaluated.Label == nil || *freshFirstEvaluated.Label != "completed-prefix" {
		t.Fatalf("completed prefix snapshot mutation escaped = %#v", freshFirstEvaluated)
	}
	wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventCommandStarted, EventCommandFinished, EventFlowFinished}
	gotKinds := make([]EventKind, len(events))
	for index := range events {
		gotKinds[index] = events[index].Kind()
		if events[index].RootRunID() != "comp-008/root-run-000001" {
			t.Fatalf("event %d root run = %q", index, events[index].RootRunID())
		}
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) || events[2].Outcome() != Completed || events[4].Outcome() != Failed || events[4].ProductError() != runErr {
		t.Fatalf("listener cutoff = %#v", events)
	}
	prefixEvent, prefixEventOK := events[2].Command()
	if !prefixEventOK || prefixEvent.Label == nil || *prefixEvent.Label != "completed-prefix" {
		t.Fatalf("completed event snapshot = %#v", prefixEvent)
	}
	prefixEvent.Label = stringPointer("caller-mutated")
	freshPrefixEvent, freshPrefixEventOK := events[2].Command()
	if !freshPrefixEventOK || freshPrefixEvent.Label == nil || *freshPrefixEvent.Label != "completed-prefix" {
		t.Fatalf("completed event mutation escaped = %#v", freshPrefixEvent)
	}
	if got := len(tapRequests(driver.Actions())); got != 2 {
		t.Fatalf("completed physical prefix taps = %d, want 2", got)
	}
	if len(swipeRequests(driver.Actions())) != 0 || len(batch1ALongPressRequests(driver.Actions())) != 0 || len(settleRequests(driver.Actions())) != 1 {
		t.Fatalf("physical cutoff actions = %#v", driver.Actions())
	}
}

func TestInteractionBatch1BActualRootPreparationOwnsFromSelectorAndConfig(t *testing.T) {
	t.Parallel()

	const path = "/workspace/owned-root.yaml"
	from := map[string]any{"text": "Continue"}
	selectorText := "Continue"
	selector := &model.ElementSelector{TextRegex: &selectorText}
	sourceCommand := model.Command{
		Kind: model.CommandSwipe, Form: model.CommandFormObject,
		Arguments: map[string]any{"from": from, "direction": "RIGHT", "duration": int64(777), "waitToSettleTimeoutMs": int64(0)},
		Selector:  selector,
	}
	sourceConfig := model.Config{Name: "owned-config", AppID: "com.example.owned", Env: map[string]string{"OWNER": "original"}}
	sourceFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          path,
		Config:        sourceConfig,
		Commands:      []model.Command{sourceCommand},
	}
	program := singleCompileProgram(sourceFlow)
	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileProgram(context.Background(), program, registry)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	roots := compiled.Roots()
	if !reflect.DeepEqual(roots, []string{path}) {
		t.Fatalf("compiled roots = %#v, want %#v", roots, []string{path})
	}
	root, exists := compiled.Flow(roots[0])
	if !exists {
		t.Fatalf("compiled root %q missing", roots[0])
	}

	from["text"] = "Mutated"
	selectorText = "Mutated"
	selector.TextRegex = stringPointer("Replaced")
	sourceCommand.Kind = model.CommandDoubleTapOn
	sourceCommand.Arguments.(map[string]any)["direction"] = "LEFT"
	sourceConfig.Name = "mutated-config"
	sourceConfig.AppID = "com.example.mutated"
	sourceConfig.Env["OWNER"] = "mutated"
	sourceFlow.Commands[0].Kind = model.CommandLongPressOn
	sourceFlow.Config.Name = "mutated-flow-config"
	sourceFlow.Config.AppID = "com.example.mutated-flow"

	preparedFlow := program.flows[path]
	preparedFlow.Config.Name = "mutated-prepared-config"
	preparedFlow.Config.AppID = "com.example.mutated-prepared"
	preparedFlow.Config.Env["OWNER"] = "mutated-prepared"
	preparedFlow.Commands[0].Kind = model.CommandDoubleTapOn
	preparedFlow.Commands[0].Arguments.(map[string]any)["direction"] = "DOWN"
	preparedFlow.Commands[0].Arguments.(map[string]any)["duration"] = int64(1)
	preparedFlow.Commands[0].Arguments.(map[string]any)["from"].(map[string]any)["text"] = "Mutated prepared"
	preparedFlow.Commands[0].Selector.TextRegex = stringPointer("Mutated prepared")
	program.flows[path] = preparedFlow

	bounds := device.Bounds{X: 10, Y: 20, Width: 40, Height: 60}
	driver := batch1ASelectorDriver(bounds, bounds)
	clock := newAdvancingClock()
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "owned-root", Driver: driver, Clock: clock,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "owned-root/root-run-000001")
	if runErr != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", runErr)
	}
	want := swipeElementRequest(device.Point{X: 30, Y: 50}, "RIGHT", 777)
	if got := swipeRequests(driver.Actions()); !reflect.DeepEqual(got, []device.SwipeRequest{want}) {
		t.Fatalf("owned selector request = %#v, want %#v", got, want)
	}
	settles := settleRequests(driver.Actions())
	if len(settles) != 1 || settles[0].AppID != "com.example.owned" || root.config.Name != "owned-config" || root.config.Env["OWNER"] != "original" {
		t.Fatalf("owned config = %#v settles %#v", root.config, settles)
	}
	if len(root.body) != 1 {
		t.Fatalf("compiled root body = %#v, want one command", root.body)
	}
	compiledCommand := root.body[0].command
	compiledArguments := compiledCommand.Arguments.(map[string]any)
	compiledFrom := compiledArguments["from"].(map[string]any)
	if compiledCommand.Kind != model.CommandSwipe || compiledCommand.Form != model.CommandFormObject || compiledFrom["text"] != "Continue" ||
		compiledArguments["direction"] != "RIGHT" || compiledArguments["duration"] != int64(777) ||
		compiledArguments["waitToSettleTimeoutMs"] != int64(0) ||
		compiledCommand.Selector == nil || compiledCommand.Selector.TextRegex == nil || *compiledCommand.Selector.TextRegex != "Continue" {
		t.Fatalf("compiled command snapshot = %#v from %#v", compiledCommand, compiledFrom)
	}
	evaluated, exists := result.Commands()[0].Metadata().EvaluatedCommand()
	if !exists {
		t.Fatal("evaluated command missing")
	}
	evaluatedArguments := evaluated.Arguments.(map[string]any)
	evaluatedFrom := evaluatedArguments["from"].(map[string]any)
	if evaluated.Kind != model.CommandSwipe || evaluated.Form != model.CommandFormObject || evaluatedFrom["text"] != "Continue" ||
		evaluatedArguments["direction"] != "RIGHT" || evaluatedArguments["duration"] != int64(777) ||
		evaluatedArguments["waitToSettleTimeoutMs"] != int64(0) ||
		evaluated.Selector == nil || evaluated.Selector.TextRegex == nil || *evaluated.Selector.TextRegex != "Continue" {
		t.Fatalf("evaluated selector snapshot = %#v", evaluated)
	}
}

func TestInteractionBatch1BPreparedPrivateSwipeRootConcurrentOwnership(t *testing.T) {
	t.Parallel()

	source := swipeCommand(map[string]any{
		"start": "10,20", "end": "30,40", "duration": int64(2345), "waitToSettleTimeoutMs": int64(0),
	}, nil)
	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec(), swipeHandlerSpec())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{containingFlow: "/workspace/concurrent-swipe.yaml"}, source)
	if err != nil {
		t.Fatal(err)
	}
	root := &compiledFlow{
		path:   "/workspace/concurrent-swipe.yaml",
		config: model.Config{AppID: "com.example.concurrent"},
		body:   []compiledDispatch{compiled},
	}
	source.Arguments.(map[string]any)["start"] = "300,400"

	const runs = 8
	type observation struct {
		rootRunID        string
		result           FlowResult
		events           []Event
		request          device.SwipeRequest
		requestStart     *device.Point
		requestEnd       *device.Point
		startedCommand   model.Command
		evaluatedCommand model.Command
	}
	observations := make([]observation, runs)
	jsFactories := make([]js.Factory, runs)
	for index := range jsFactories {
		jsFactories[index] = tapJSFactory(t)
	}
	var wg sync.WaitGroup
	for index := 0; index < runs; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := batch1APointDriver(400, 884)
			capture := &batch1BRequestPointerDriver{Driver: base}
			clock := newAdvancingClock()
			events := make([]Event, 0, 4)
			rootRunID := fmt.Sprintf("concurrent-swipe/root-run-%06d", index+1)
			result, executeErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: "concurrent-swipe", Driver: capture, Clock: clock,
				JSFactory: jsFactories[index], Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootRunID)
			if executeErr != nil {
				t.Errorf("run %d error = %v", index, executeErr)
				return
			}
			requests := swipeRequests(base.Actions())
			if len(requests) != 1 || len(events) != 4 || len(result.Commands()) != 1 {
				t.Errorf("run %d requests/events/results = %d/%d/%d", index, len(requests), len(events), len(result.Commands()))
				return
			}
			started, _ := events[1].Command()
			evaluated, _ := result.Commands()[0].Metadata().EvaluatedCommand()
			startPointer, endPointer := capture.snapshotPointers()
			observations[index] = observation{
				rootRunID: rootRunID, result: result, events: events, request: requests[0],
				requestStart: startPointer, requestEnd: endPointer,
				startedCommand: started, evaluatedCommand: evaluated,
			}
		}()
	}
	wg.Wait()

	sourceMap := mapPointer(source.Arguments)
	compiledMap := mapPointer(compiled.command.Arguments)
	seenRequestPointers := make(map[*device.Point]string, runs*2)
	seenCommandMaps := make(map[uintptr]string, runs*2)
	seenRootRuns := make(map[string]struct{}, runs)
	wantRequest := swipePointRequest(device.Point{X: 10, Y: 20}, device.Point{X: 30, Y: 40}, 2345)
	for index := range observations {
		observation := &observations[index]
		if observation.result.Outcome() != Completed || observation.result.RootRunID() != observation.rootRunID ||
			!reflect.DeepEqual(observation.request, wantRequest) || observation.requestStart == nil || observation.requestEnd == nil {
			t.Fatalf("run %d observation = %#v", index, observation)
		}
		if *observation.requestStart != (device.Point{X: 10, Y: 20}) || *observation.requestEnd != (device.Point{X: 30, Y: 40}) {
			t.Fatalf("run %d request pointers = start %#v end %#v", index, observation.requestStart, observation.requestEnd)
		}
		if _, duplicate := seenRootRuns[observation.rootRunID]; duplicate {
			t.Fatalf("duplicate root run identity %q", observation.rootRunID)
		}
		seenRootRuns[observation.rootRunID] = struct{}{}
		for label, pointer := range map[string]*device.Point{
			"request start": observation.requestStart, "request end": observation.requestEnd,
		} {
			if previous, duplicate := seenRequestPointers[pointer]; duplicate {
				t.Fatalf("run %d %s aliases %s", index, label, previous)
			}
			seenRequestPointers[pointer] = fmt.Sprintf("run %d %s", index, label)
		}
		for label, command := range map[string]model.Command{
			"started command": observation.startedCommand, "evaluated command": observation.evaluatedCommand,
		} {
			pointer := mapPointer(command.Arguments)
			if pointer == 0 || pointer == sourceMap || pointer == compiledMap {
				t.Fatalf("run %d %s aliases source/compiled map", index, label)
			}
			if previous, duplicate := seenCommandMaps[pointer]; duplicate {
				t.Fatalf("run %d %s aliases %s", index, label, previous)
			}
			seenCommandMaps[pointer] = fmt.Sprintf("run %d %s", index, label)
		}
	}
	firstCommand := observations[0].result.Commands()[0].Command()
	firstCommand.Arguments.(map[string]any)["start"] = "caller-mutated"
	if got := observations[1].result.Commands()[0].Command().Arguments.(map[string]any)["start"]; got != "10,20" {
		t.Fatalf("result alias escaped across roots: %v", got)
	}
	firstEventCommand, _ := observations[0].events[1].Command()
	firstEventCommand.Arguments.(map[string]any)["end"] = "caller-mutated"
	otherEventCommand, _ := observations[1].events[1].Command()
	if got := otherEventCommand.Arguments.(map[string]any)["end"]; got != "30,40" {
		t.Fatalf("event alias escaped across roots: %v", got)
	}
	if got := compiled.command.Arguments.(map[string]any)["start"]; got != "10,20" {
		t.Fatalf("compiled root mutated after repeated/concurrent execution: %v", got)
	}
}

type batch1BRequestPointerDriver struct {
	device.Driver
	mu    sync.Mutex
	start *device.Point
	end   *device.Point
}

func (driver *batch1BRequestPointerDriver) Swipe(ctx context.Context, request device.SwipeRequest) error {
	driver.mu.Lock()
	driver.start = request.Start
	driver.end = request.End
	driver.mu.Unlock()
	return driver.Driver.Swipe(ctx, request)
}

func (driver *batch1BRequestPointerDriver) snapshotPointers() (*device.Point, *device.Point) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return driver.start, driver.end
}

func mapPointer(value any) uintptr {
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Map || reflected.IsNil() {
		return 0
	}
	return reflected.Pointer()
}
