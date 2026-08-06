package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/report"
)

func TestControlFlowHandlersRunPreparedGraphThroughPublicSurfaces(t *testing.T) {
	t.Parallel()

	rootPath := filepath.Join("..", "..", "testdata", "engine", "control-flow", "root.yaml")
	program, err := engine.Prepare(context.Background(), model.ExecutionPlan{
		SelectedRoots: []string{rootPath},
	}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("engine.Prepare() error: %v", err)
	}
	roots := program.Roots()
	paths := program.FlowPaths()
	if len(roots) != 1 || len(paths) != 2 || roots[0] != paths[0] {
		t.Fatalf("prepared roots/paths = %#v / %#v, want root then linked child", roots, paths)
	}
	rootBefore, rootExists := program.Flow(roots[0])
	childBefore, childExists := program.Flow(paths[1])
	if !rootExists || !childExists {
		t.Fatalf("prepared graph missing root/child: root=%t child=%t", rootExists, childExists)
	}
	assertDynamicControlFlowSources(t, rootBefore, childBefore)

	firstTapFailure := errors.New("first scripted tap failed")
	driver := enginetest.NewFakeDriver()
	tree := controlFlowTree()
	descriptors := make([]enginetest.Result[device.TreeNode], 9)
	for index := range descriptors {
		descriptors[index] = enginetest.Result[device.TreeNode]{Value: tree}
	}
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600,
		}}},
		ContentDescriptor: descriptors,
		Tap: []enginetest.Result[struct{}]{
			{Err: firstTapFailure},
			{},
		},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: ready}, {Value: ready}},
	})
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	observer := &controlFlowObserver{}
	clock := &controlFlowClock{now: time.Unix(1_700_100_000, 0).UTC(), observer: observer}

	results, err := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "control-flow-integration",
		Driver:      driver,
		Clock:       clock,
		JSFactory:   factory,
		Controller:  engine.NoopController{},
		Listeners:   []engine.Listener{observer},
	})
	if err != nil {
		t.Fatalf("engine.Execute() error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("engine.Execute() result count = %d, want 1", len(results))
	}
	result := results[0]
	rootRunID := "control-flow-integration/root-run-000001"
	if result.Path() != roots[0] || result.Outcome() != engine.Completed || result.RootRunID() != rootRunID {
		t.Fatalf("root result = path %q outcome %q root %q", result.Path(), result.Outcome(), result.RootRunID())
	}

	commands := result.Commands()
	wantKeywords := []model.CommandKeyword{
		model.CommandRunFlow,
		model.CommandAssertVisible,
		model.CommandRepeat,
		model.CommandAssertVisible,
		model.CommandAssertVisible,
		model.CommandRetry,
		model.CommandTapOn,
		model.CommandTapOn,
		model.CommandAssertVisible,
		model.CommandAssertVisible,
	}
	wantDepths := []int{0, 1, 1, 2, 2, 1, 2, 2, 1, 0}
	if len(commands) != len(wantKeywords) {
		t.Fatalf("command count = %d, want %d: %#v", len(commands), len(wantKeywords), commands)
	}
	for index, command := range commands {
		wantOutcome := engine.Completed
		if index == 6 {
			wantOutcome = engine.Failed
		}
		if command.Sequence() != uint64(index+1) || command.Depth() != wantDepths[index] ||
			command.Command().Kind != wantKeywords[index] || command.Outcome() != wantOutcome ||
			command.RootRunID() != rootRunID {
			t.Fatalf("command %d = seq %d depth %d kind %q outcome %q root %q", index,
				command.Sequence(), command.Depth(), command.Command().Kind, command.Outcome(), command.RootRunID())
		}
	}
	var tapProductError *engine.OperationError
	if !errors.As(commands[6].ProductError(), &tapProductError) ||
		!errors.Is(commands[6].ProductError(), firstTapFailure) ||
		!engine.IsRetryable(commands[6].ProductError()) || commands[7].ProductError() != nil {
		t.Fatalf("tap attempts = first %T %v second %v", commands[6].ProductError(), commands[6].ProductError(), commands[7].ProductError())
	}
	assertControlFlowMetadata(t, commands)
	assertControlFlowLifecycleAndResets(t, observer.Events(), roots[0], paths[1], rootRunID)
	assertControlFlowWaits(t, clock.Waits())
	assertControlFlowDriverActions(t, driver.Actions())

	rootAfter, _ := program.Flow(roots[0])
	childAfter, _ := program.Flow(paths[1])
	if !reflect.DeepEqual(rootAfter, rootBefore) || !reflect.DeepEqual(childAfter, childBefore) {
		t.Fatal("prepared source graph mutated during complete-program compilation or execution")
	}
	assertDynamicControlFlowSources(t, rootAfter, childAfter)

	reportResult, err := report.FromEngineFlowResult(result, rootBefore.Config)
	if err != nil {
		t.Fatalf("report.FromEngineFlowResult() error: %v", err)
	}
	assertControlFlowReport(t, reportResult, commands, rootRunID)
}

func TestControlFlowDualSourceIsRejectedBeforePublicExecutionEffects(t *testing.T) {
	t.Parallel()

	fixtures := []string{
		"invalid-run-flow-dual-source.yaml",
		"invalid-retry-dual-source.yaml",
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("..", "..", "testdata", "engine", "control-flow", fixture)
			program, prepareErr := engine.Prepare(context.Background(), model.ExecutionPlan{
				SelectedRoots: []string{path},
			}, capability.FileLoader{})
			if prepareErr != nil {
				t.Fatalf("engine.Prepare() rejected authored graph before structural compilation: %v", prepareErr)
			}
			driver := enginetest.NewFakeDriver()
			delegate, factoryErr := js.NewFactory(js.Config{Random: integrationRandom{}})
			if factoryErr != nil {
				t.Fatalf("js.NewFactory() error: %v", factoryErr)
			}
			factory := &countingIntegrationFactory{delegate: delegate}
			var events []engine.Event
			results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
				ExecutionID: "invalid-control-flow",
				Driver:      driver,
				Clock:       &integrationClock{now: time.Unix(1_700_200_000, 0).UTC()},
				JSFactory:   factory,
				Controller:  engine.NoopController{},
				Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
					events = append(events, event)
					return nil
				})},
			})
			var configuration *engine.ConfigurationError
			if !errors.As(executeErr, &configuration) || !strings.Contains(executeErr.Error(), "requires exactly one of file or commands") {
				t.Fatalf("engine.Execute() error = %T %v, want dual-source configuration error", executeErr, executeErr)
			}
			if len(results) != 0 || factory.Calls() != 0 || len(driver.Actions()) != 0 || len(events) != 0 {
				t.Fatalf("invalid graph reached effects: results=%#v runtime=%d actions=%#v events=%#v",
					results, factory.Calls(), driver.Actions(), events)
			}
		})
	}
}

func assertDynamicControlFlowSources(t *testing.T, root, child model.Flow) {
	t.Helper()
	if len(root.Commands) != 1 || root.Commands[0].Kind != model.CommandRunFlow {
		t.Fatalf("root source commands = %#v", root.Commands)
	}
	runFlow := root.Commands[0]
	if runFlow.Form != model.CommandFormObject || len(runFlow.Links) != 1 ||
		runFlow.Links[0].Kind != model.FileLinkFlow || runFlow.Links[0].Path != "child.yaml" ||
		len(runFlow.Children) != 0 {
		t.Fatalf("runFlow parser views = form %q links %#v children %#v", runFlow.Form, runFlow.Links, runFlow.Children)
	}
	runArguments, ok := runFlow.Arguments.(map[string]any)
	if !ok {
		t.Fatalf("runFlow source arguments = %#v", root.Commands[0].Arguments)
	}
	runEnvironment, ok := runArguments["env"].(map[string]any)
	if !ok || runEnvironment["REPEAT_TIMES"] != "${ROOT_REPEAT_TIMES}" || runEnvironment["RETRY_MAX"] != "${ROOT_RETRY_MAX}" {
		t.Fatalf("runFlow source environment = %#v", runEnvironment)
	}
	if len(child.Commands) != 2 || child.Commands[0].Kind != model.CommandRepeat || child.Commands[1].Kind != model.CommandRetry {
		t.Fatalf("child source commands = %#v", child.Commands)
	}
	repeatArguments := child.Commands[0].Arguments.(map[string]any)
	retryArguments := child.Commands[1].Arguments.(map[string]any)
	repeatRawChildren, repeatRawOK := repeatArguments["commands"].([]any)
	retryRawChildren, retryRawOK := retryArguments["commands"].([]any)
	if child.Commands[0].Form != model.CommandFormObject || !repeatRawOK || len(repeatRawChildren) != 1 ||
		len(child.Commands[0].Children) != 1 || child.Commands[0].Children[0].Kind != model.CommandAssertVisible ||
		child.Commands[1].Form != model.CommandFormObject || !retryRawOK || len(retryRawChildren) != 1 ||
		len(child.Commands[1].Children) != 1 || child.Commands[1].Children[0].Kind != model.CommandTapOn {
		t.Fatalf("repeat/retry parser views = repeat raw %#v typed %#v retry raw %#v typed %#v",
			repeatArguments["commands"], child.Commands[0].Children, retryArguments["commands"], child.Commands[1].Children)
	}
	if repeatArguments["times"] != "${REPEAT_TIMES}" || retryArguments["maxRetries"] != "${RETRY_MAX}" {
		t.Fatalf("dynamic source counters = repeat %#v retry %#v", repeatArguments["times"], retryArguments["maxRetries"])
	}
}

func assertControlFlowMetadata(t *testing.T, commands []engine.CommandResult) {
	t.Helper()
	assertRawCompositeArguments(t, commands[0].Command(), "env", map[string]any{
		"REPEAT_TIMES": "${ROOT_REPEAT_TIMES}", "RETRY_MAX": "${ROOT_RETRY_MAX}", "TARGET": "Overlay Start",
	})
	assertRawCompositeArguments(t, commands[2].Command(), "times", "${REPEAT_TIMES}")
	assertRawCompositeArguments(t, commands[5].Command(), "maxRetries", "${RETRY_MAX}")
	if metadata := commands[2].Metadata(); !metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != 2 || metadata.Insight() != "" {
		t.Fatalf("repeat metadata = runs %d present %t insight %q", metadata.NumberOfRuns(), metadata.HasNumberOfRuns(), metadata.Insight())
	}
	if metadata := commands[5].Metadata(); !metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != 2 || metadata.Insight() != "Command succeeded after retry" {
		t.Fatalf("retry metadata = runs %d present %t insight %q", metadata.NumberOfRuns(), metadata.HasNumberOfRuns(), metadata.Insight())
	}
	assertEvaluatedArgument(t, commands[0], "env", map[string]any{
		"REPEAT_TIMES": "2", "RETRY_MAX": "1", "TARGET": "Overlay Start",
	})
	assertEvaluatedArgument(t, commands[2], "times", "2")
	assertEvaluatedArgument(t, commands[5], "maxRetries", "1")
	wantSelectors := map[int]string{
		// The child declares TARGET itself, and a flow's own `env:` beats the
		// one runFlow handed it — so the child's value is what resolves here,
		// not the parent's "Overlay Start". See env_precedence_test.go.
		1: "Child Shadow",
		3: "Repeat Target",
		4: "Repeat Target",
		6: "Retry Tap",
		7: "Retry Tap",
		8: "Child Complete",
		9: "Root Complete",
	}
	for index, want := range wantSelectors {
		assertEvaluatedSelector(t, commands[index], want)
	}
	insights := 0
	for _, command := range commands {
		if command.Metadata().Insight() != "" {
			insights++
		}
	}
	if insights != 1 {
		t.Fatalf("non-empty command insights = %d, want exactly retry success", insights)
	}
}

func assertRawCompositeArguments(t *testing.T, command model.Command, key string, want any) {
	t.Helper()
	arguments, ok := command.Arguments.(map[string]any)
	if command.Form != model.CommandFormObject || !ok || !reflect.DeepEqual(arguments[key], want) {
		t.Fatalf("raw %s %s = %#v, want %#v", command.Kind, key, command.Arguments, want)
	}
}

func assertEvaluatedArgument(t *testing.T, result engine.CommandResult, key string, want any) {
	t.Helper()
	evaluated, exists := result.Metadata().EvaluatedCommand()
	arguments, ok := evaluated.Arguments.(map[string]any)
	if !exists || !ok || !reflect.DeepEqual(arguments[key], want) {
		t.Fatalf("%s evaluated arguments = %#v, want %#v", result.Command().Kind, evaluated.Arguments, want)
	}
}

func assertControlFlowLifecycleAndResets(t *testing.T, events []engine.Event, rootPath, childPath, rootRunID string) {
	t.Helper()
	flowEvents := make([]string, 0, 4)
	flowDepths := make([]int, 0, 4)
	resets := make([]engine.Event, 0, 2)
	finished := make(map[uint64]engine.Event)
	updates := make(map[uint64][]int)
	for _, event := range events {
		if event.RootRunID() != rootRunID {
			t.Fatalf("event rootRunId = %q, want %q", event.RootRunID(), rootRunID)
		}
		switch event.Kind() {
		case engine.EventFlowStarted, engine.EventFlowFinished:
			flowEvents = append(flowEvents, string(event.Kind())+":"+event.FlowPath())
			flowDepths = append(flowDepths, event.Depth())
		case engine.EventCommandFinished:
			finished[event.Sequence()] = event
		case engine.EventCommandReset:
			resets = append(resets, event)
		case engine.EventCommandMetadataUpdated:
			updates[event.Sequence()] = append(updates[event.Sequence()], event.Metadata().NumberOfRuns())
		}
	}
	wantFlows := []string{
		"FlowStarted:" + rootPath,
		"FlowStarted:" + childPath,
		"FlowFinished:" + childPath,
		"FlowFinished:" + rootPath,
	}
	if !reflect.DeepEqual(flowEvents, wantFlows) {
		t.Fatalf("flow lifecycle = %#v, want %#v", flowEvents, wantFlows)
	}
	if !reflect.DeepEqual(flowDepths, []int{0, 1, 1, 0}) {
		t.Fatalf("flow lifecycle depths = %#v, want root/child/child/root", flowDepths)
	}
	if !reflect.DeepEqual(updates[3], []int{1, 2}) || !reflect.DeepEqual(updates[6], []int{1, 2}) {
		t.Fatalf("composite attempt updates = repeat %#v retry %#v", updates[3], updates[6])
	}
	wantResetSequences := []uint64{4, 7}
	if len(resets) != len(wantResetSequences) {
		t.Fatalf("reset events = %#v, want exact repeat/retry resets", resets)
	}
	for index, reset := range resets {
		prior, exists := finished[wantResetSequences[index]]
		resetCommand, resetHasCommand := reset.Command()
		priorCommand, priorHasCommand := prior.Command()
		if !exists || !resetHasCommand || !priorHasCommand || reset.Sequence() != prior.Sequence() ||
			reset.Depth() != prior.Depth() || reset.RootRunID() != prior.RootRunID() ||
			!resetCommand.Equivalent(priorCommand) {
			t.Fatalf("reset %d = seq %d depth %d command %#v, want prior result identity %#v", index,
				reset.Sequence(), reset.Depth(), resetCommand, prior)
		}
	}
	assertEventOrder(t, events, engine.EventCommandFinished, 4, engine.EventCommandReset, 4)
	assertEventOrder(t, events, engine.EventCommandReset, 4, engine.EventCommandStarted, 5)
	assertEventOrder(t, events, engine.EventCommandFinished, 7, engine.EventCommandReset, 7)
	assertEventOrder(t, events, engine.EventCommandReset, 7, engine.EventCommandStarted, 8)
}

func assertEventOrder(t *testing.T, events []engine.Event, firstKind engine.EventKind, firstSequence uint64, secondKind engine.EventKind, secondSequence uint64) {
	t.Helper()
	firstIndex, secondIndex := -1, -1
	for index, event := range events {
		if firstIndex < 0 && event.Kind() == firstKind && event.Sequence() == firstSequence {
			firstIndex = index
		}
		if event.Kind() == secondKind && event.Sequence() == secondSequence {
			secondIndex = index
			if firstIndex >= 0 {
				break
			}
		}
	}
	if firstIndex < 0 || secondIndex <= firstIndex {
		t.Fatalf("event order %s/%d -> %s/%d not preserved", firstKind, firstSequence, secondKind, secondSequence)
	}
}

func assertControlFlowWaits(t *testing.T, waits []controlFlowWait) {
	t.Helper()
	var repeatWaits []time.Duration
	var tapWaits []time.Duration
	for _, wait := range waits {
		switch wait.keyword {
		case model.CommandRepeat:
			repeatWaits = append(repeatWaits, wait.delay)
		case model.CommandTapOn:
			tapWaits = append(tapWaits, wait.delay)
		default:
			t.Fatalf("wait attributed outside repeat/tap: %#v", wait)
		}
	}
	if !reflect.DeepEqual(repeatWaits, []time.Duration{engine.RepeatDelay}) {
		t.Fatalf("repeat waits = %#v, want exactly one RepeatDelay", repeatWaits)
	}
	if !reflect.DeepEqual(tapWaits, []time.Duration{
		engine.ElementStabilityPollInterval,
		engine.ElementStabilityPollInterval,
		engine.HierarchySettlePollInterval,
		engine.HierarchySettlePollInterval,
	}) {
		t.Fatalf("tap waits = %#v", tapWaits)
	}
}

func assertControlFlowDriverActions(t *testing.T, actions []enginetest.Action) {
	t.Helper()
	wantMethods := []enginetest.Method{
		enginetest.MethodDeviceInfo,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodTap,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodTap,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
	}
	gotMethods := make([]enginetest.Method, len(actions))
	for index, action := range actions {
		gotMethods[index] = action.Method
		if action.Sequence != uint64(index+1) {
			t.Fatalf("driver action %d sequence = %d, want %d", index, action.Sequence, index+1)
		}
	}
	if !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("driver methods = %#v, want sequential %#v", gotMethods, wantMethods)
	}
	for _, index := range []int{6, 9} {
		request, ok := actions[index].Request.(device.TapRequest)
		if !ok || request.Point != (device.Point{X: 40, Y: 40}) {
			t.Fatalf("tap action %d = %#v", index, actions[index])
		}
	}
}

func assertControlFlowReport(t *testing.T, got report.FlowResult, commands []engine.CommandResult, rootRunID string) {
	t.Helper()
	if got.Status != report.Completed || got.Name != "Control Flow Root" || got.Metadata["rootRunId"] != rootRunID {
		t.Fatalf("report root = status %q name %q metadata %#v", got.Status, got.Name, got.Metadata)
	}
	if len(got.Commands) != len(commands) {
		t.Fatalf("report command count = %d, want %d", len(got.Commands), len(commands))
	}
	insights := 0
	for index, command := range got.Commands {
		if command.Sequence != int64(index+1) || command.Depth != commands[index].Depth() ||
			command.Keyword != string(commands[index].Command().Kind) || command.Metadata["rootRunId"] != rootRunID {
			t.Fatalf("report command %d = %#v", index, command)
		}
		if command.Metadata["evaluatedCommand"] == "" {
			t.Fatalf("report command %d omitted evaluatedCommand", index)
		}
		if command.Metadata["insight"] != "" {
			insights++
		}
	}
	if got.Commands[2].Metadata["numberOfRuns"] != "2" || got.Commands[5].Metadata["numberOfRuns"] != "2" ||
		got.Commands[5].Metadata["insight"] != "Command succeeded after retry" || insights != 1 {
		t.Fatalf("report composite metadata = repeat %#v retry %#v insights %d", got.Commands[2].Metadata, got.Commands[5].Metadata, insights)
	}
	var runFlowEvaluated model.Command
	if err := json.Unmarshal([]byte(got.Commands[0].Metadata["evaluatedCommand"]), &runFlowEvaluated); err != nil {
		t.Fatalf("decode report evaluated runFlow: %v", err)
	}
	if environment, ok := runFlowEvaluated.Arguments.(map[string]any)["env"].(map[string]any); !ok || !reflect.DeepEqual(environment, map[string]any{
		"REPEAT_TIMES": "2", "RETRY_MAX": "1", "TARGET": "Overlay Start",
	}) {
		t.Fatalf("report evaluated runFlow env = %#v", runFlowEvaluated.Arguments)
	}
	for _, index := range []int{2, 5} {
		var evaluated model.Command
		if err := json.Unmarshal([]byte(got.Commands[index].Metadata["evaluatedCommand"]), &evaluated); err != nil {
			t.Fatalf("decode report evaluated command %d: %v", index, err)
		}
		arguments := evaluated.Arguments.(map[string]any)
		key, want := "times", "2"
		if index == 5 {
			key, want = "maxRetries", "1"
		}
		if arguments[key] != want {
			t.Fatalf("report evaluated %s = %#v, want %q", key, arguments[key], want)
		}
	}
}

func controlFlowTree() device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][300,600]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"text": "Child Shadow", "bounds": "[10,10][70,70]"}},
			{Attributes: map[string]string{"text": "Repeat Target", "bounds": "[10,80][70,140]"}},
			{Attributes: map[string]string{"text": "Retry Tap", "bounds": "[10,10][70,70]"}},
			{Attributes: map[string]string{"text": "Child Complete", "bounds": "[10,150][70,210]"}},
			{Attributes: map[string]string{"text": "Root Complete", "bounds": "[10,220][70,280]"}},
		},
	}
}

type controlFlowObserver struct {
	mu     sync.Mutex
	events []engine.Event
	active []observedCommand
}

type observedCommand struct {
	sequence uint64
	depth    int
	keyword  model.CommandKeyword
}

func (observer *controlFlowObserver) OnEvent(_ context.Context, event engine.Event) error {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
	command, hasCommand := event.Command()
	switch event.Kind() {
	case engine.EventCommandStarted:
		if hasCommand {
			observer.active = append(observer.active, observedCommand{
				sequence: event.Sequence(), depth: event.Depth(), keyword: command.Kind,
			})
		}
	case engine.EventCommandFinished:
		for index := len(observer.active) - 1; index >= 0; index-- {
			if observer.active[index].sequence == event.Sequence() {
				observer.active = append(observer.active[:index], observer.active[index+1:]...)
				break
			}
		}
	}
	return nil
}

func (observer *controlFlowObserver) Events() []engine.Event {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	return append([]engine.Event(nil), observer.events...)
}

func (observer *controlFlowObserver) current() observedCommand {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.active) == 0 {
		return observedCommand{}
	}
	return observer.active[len(observer.active)-1]
}

type controlFlowWait struct {
	keyword model.CommandKeyword
	depth   int
	delay   time.Duration
}

type controlFlowClock struct {
	mu       sync.Mutex
	now      time.Time
	observer *controlFlowObserver
	waits    []controlFlowWait
}

func (clock *controlFlowClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *controlFlowClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	active := clock.observer.current()
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.waits = append(clock.waits, controlFlowWait{keyword: active.keyword, depth: active.depth, delay: delay})
	if delay > 0 {
		clock.now = clock.now.Add(delay)
	}
	return nil
}

func (clock *controlFlowClock) Waits() []controlFlowWait {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]controlFlowWait(nil), clock.waits...)
}

type countingIntegrationFactory struct {
	mu       sync.Mutex
	delegate js.Factory
	calls    int
}

func (factory *countingIntegrationFactory) NewRuntime() (js.Runtime, error) {
	factory.mu.Lock()
	factory.calls++
	factory.mu.Unlock()
	return factory.delegate.NewRuntime()
}

func (factory *countingIntegrationFactory) Calls() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.calls
}

var _ engine.Listener = (*controlFlowObserver)(nil)
var _ engine.Clock = (*controlFlowClock)(nil)
var _ js.Factory = (*countingIntegrationFactory)(nil)
