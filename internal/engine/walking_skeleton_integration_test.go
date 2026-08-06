package engine_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
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

func TestWalkingSkeletonRunsPreparedYAMLThroughFakeDriverAndReportAdapter(t *testing.T) {
	t.Parallel()

	fixture := filepath.Join("..", "..", "testdata", "engine", "walking-skeleton.yaml")
	program, err := engine.Prepare(context.Background(), model.ExecutionPlan{
		SelectedRoots: []string{fixture, fixture},
	}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("engine.Prepare() error: %v", err)
	}
	roots := program.Roots()
	if len(roots) != 2 || roots[0] != roots[1] {
		t.Fatalf("prepared roots = %#v, want ordered duplicate selections", roots)
	}

	driver := enginetest.NewFakeDriver()
	tree := walkingSkeletonTree()
	settle := walkingSkeletonSettleResults()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
			{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 300, HeightGrid: 600}},
		},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: tree}, {Value: tree}, {Value: tree},
			{Value: tree}, {Value: tree}, {Value: tree},
		},
		WaitForAppToSettle: append(append(
			[]enginetest.Result[*device.ViewHierarchy]{}, settle...), settle...,
		),
	})
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}
	clock := &integrationClock{now: time.Unix(1_700_000_000, 0).UTC()}
	events := make([]engine.Event, 0, 16)

	results, err := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "walking-skeleton-execution",
		Driver:      driver, Clock: clock, JSFactory: factory, Controller: engine.NoopController{},
		Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
			events = append(events, event)
			return nil
		})},
	})
	if err != nil {
		t.Fatalf("engine.Execute() error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("engine.Execute() result count = %d, want 2", len(results))
	}
	wantRootRunIDs := []string{
		"walking-skeleton-execution/root-run-000001",
		"walking-skeleton-execution/root-run-000002",
	}
	for index, result := range results {
		if result.Path() != roots[index] || result.Outcome() != engine.Completed {
			t.Fatalf("result %d = path %q outcome %q, want %q Completed", index, result.Path(), result.Outcome(), roots[index])
		}
		if result.RootRunID() != wantRootRunIDs[index] {
			t.Fatalf("result %d root run ID = %q, want %q", index, result.RootRunID(), wantRootRunIDs[index])
		}
		commands := result.Commands()
		if len(commands) != 3 {
			t.Fatalf("result %d command count = %d, want 3", index, len(commands))
		}
		if got := []model.CommandKeyword{
			commands[0].Command().Kind, commands[1].Command().Kind, commands[2].Command().Kind,
		}; !reflect.DeepEqual(got, []model.CommandKeyword{
			model.CommandLaunchApp, model.CommandTapOn, model.CommandAssertVisible,
		}) {
			t.Fatalf("result %d command order = %#v", index, got)
		}
		for commandIndex, command := range commands {
			if command.Outcome() != engine.Completed {
				t.Fatalf("result %d command %d outcome = %q, want Completed", index, commandIndex, command.Outcome())
			}
			if command.RootRunID() != wantRootRunIDs[index] {
				t.Fatalf("result %d command %d root run ID = %q", index, commandIndex, command.RootRunID())
			}
		}
		if source := commands[0].Command(); source.Form != model.CommandFormScalar || source.Arguments != nil {
			t.Fatalf("source launchApp command was rewritten: %#v", source)
		}
		launchEvaluated, exists := commands[0].Metadata().EvaluatedCommand()
		launchArguments, argumentsOK := launchEvaluated.Arguments.(map[string]any)
		if !exists || launchEvaluated.Form != model.CommandFormObject || !argumentsOK || launchArguments["appId"] != "com.example.walking" {
			t.Fatalf("evaluated launchApp arguments = %#v, want resolved inherited appId", launchEvaluated.Arguments)
		}
		assertEvaluatedSelector(t, commands[1], "Continue")
		assertEvaluatedSelector(t, commands[2], "Ready")

		flow, exists := program.Flow(result.Path())
		if !exists {
			t.Fatalf("prepared flow %q missing", result.Path())
		}
		reportResult, reportErr := report.FromEngineFlowResult(result, flow.Config)
		if reportErr != nil {
			t.Fatalf("report.FromEngineFlowResult() error: %v", reportErr)
		}
		if reportResult.Status != report.Completed || reportResult.Name != "Walking Skeleton" {
			t.Fatalf("report result %d = status %q name %q", index, reportResult.Status, reportResult.Name)
		}
		if reportResult.Metadata["rootRunId"] != wantRootRunIDs[index] {
			t.Fatalf("report result %d rootRunId = %q", index, reportResult.Metadata["rootRunId"])
		}
		if got := []string{
			reportResult.Commands[0].Keyword,
			reportResult.Commands[1].Keyword,
			reportResult.Commands[2].Keyword,
		}; !reflect.DeepEqual(got, []string{"launchApp", "tapOn", "assertVisible"}) {
			t.Fatalf("report command order = %#v", got)
		}
		if reportResult.Commands[1].Description != "Tap Continue" {
			t.Fatalf("report evaluated description = %q, want Tap Continue", reportResult.Commands[1].Description)
		}
		if reportResult.Commands[0].Keyword != "launchApp" || reportResult.Commands[0].Description != "launchApp" {
			t.Fatalf("report evaluated launch identity = %#v", reportResult.Commands[0])
		}
		wantLogs := [][]string{{"app evaluation"}, {"tap evaluation", "app evaluation"}, {"assert evaluation"}}
		for commandIndex, command := range commands {
			evaluated, exists := command.Metadata().EvaluatedCommand()
			if !exists {
				t.Fatalf("result %d command %d has no evaluated snapshot", index, commandIndex)
			}
			encoded, encodeErr := json.Marshal(evaluated)
			if encodeErr != nil {
				t.Fatalf("json.Marshal(evaluated) error: %v", encodeErr)
			}
			metadata := reportResult.Commands[commandIndex].Metadata
			if metadata["rootRunId"] != wantRootRunIDs[index] || metadata["evaluatedCommand"] != string(encoded) {
				t.Fatalf("report command %d metadata = %#v", commandIndex, metadata)
			}
			encodedLogs, _ := json.Marshal(wantLogs[commandIndex])
			if metadata["logMessages"] != string(encodedLogs) {
				t.Fatalf("report command %d logs = %q, want %q", commandIndex, metadata["logMessages"], encodedLogs)
			}
		}
	}
	if len(events) != 16 {
		t.Fatalf("listener event count = %d, want 16", len(events))
	}
	for index, event := range events {
		want := wantRootRunIDs[index/8]
		if event.RootRunID() != want {
			t.Fatalf("event %d root run ID = %q, want %q", index, event.RootRunID(), want)
		}
	}

	actions := driver.Actions()
	// launchApp stops the app before launching: stopApp defaults to true
	// (specs/06-launch-app-semantics.md section 1), so every launch is a
	// StopApp followed by a LaunchApp.
	wantMethods := []enginetest.Method{
		enginetest.MethodDeviceInfo,
		enginetest.MethodStopApp,
		enginetest.MethodLaunchApp,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodTap,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodContentDescriptor,
		enginetest.MethodDeviceInfo,
		enginetest.MethodStopApp,
		enginetest.MethodLaunchApp,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodTap,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodWaitForAppToSettle,
		enginetest.MethodContentDescriptor,
	}
	gotMethods := make([]enginetest.Method, len(actions))
	for index, action := range actions {
		gotMethods[index] = action.Method
		if action.Sequence != uint64(index+1) {
			t.Fatalf("action %d sequence = %d, want %d", index, action.Sequence, index+1)
		}
	}
	if !reflect.DeepEqual(gotMethods, wantMethods) {
		t.Fatalf("driver action methods = %#v, want %#v", gotMethods, wantMethods)
	}
	for _, index := range []int{2, 17} {
		request, ok := actions[index].Request.(device.LaunchAppRequest)
		if !ok || request.AppID != "com.example.walking" {
			t.Fatalf("launch action %d request = %#v", index, actions[index].Request)
		}
	}
	for _, index := range []int{9, 24} {
		request, ok := actions[index].Request.(device.TapRequest)
		if !ok || request.Point != (device.Point{X: 40, Y: 40}) {
			t.Fatalf("tap action %d request = %#v", index, actions[index].Request)
		}
	}
	for _, indexes := range [][2]int{{3, 6}, {18, 21}} {
		for index := indexes[0]; index <= indexes[1]; index++ {
			request := actions[index].Request.(device.SettleRequest)
			if request.AppID != "com.example.walking" || request.TimeoutMillis != nil {
				t.Fatalf("launch settle action %d request = %#v", index, request)
			}
		}
	}
	for _, indexes := range [][2]int{{10, 13}, {25, 28}} {
		for index := indexes[0]; index <= indexes[1]; index++ {
			request := actions[index].Request.(device.SettleRequest)
			if request.AppID != "com.example.walking" || request.TimeoutMillis == nil || *request.TimeoutMillis != 900 {
				t.Fatalf("tap settle action %d request = %#v", index, request)
			}
		}
	}
}

func walkingSkeletonSettleResults() []enginetest.Result[*device.ViewHierarchy] {
	loading := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{
		"state": "loading", "is-loading": "true",
	}}}
	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
	return []enginetest.Result[*device.ViewHierarchy]{
		{Value: nil}, {Value: loading}, {Value: ready}, {Value: ready},
		{Value: nil}, {Value: loading}, {Value: ready}, {Value: ready},
	}
}

func walkingSkeletonTree() device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][300,600]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"text": "Continue", "bounds": "[10,20][70,60]"}},
			{Attributes: map[string]string{"text": "Ready", "bounds": "[20,100][120,140]"}},
		},
	}
}

func assertEvaluatedSelector(t *testing.T, result engine.CommandResult, want string) {
	t.Helper()
	evaluated, exists := result.Metadata().EvaluatedCommand()
	if !exists || evaluated.Selector == nil || evaluated.Selector.TextRegex == nil || *evaluated.Selector.TextRegex != want {
		t.Fatalf("evaluated selector = %#v, want text %q", evaluated.Selector, want)
	}
}

type integrationClock struct {
	now time.Time
}

func (clock *integrationClock) Now() time.Time {
	return clock.now
}

func (clock *integrationClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay > 0 {
		clock.now = clock.now.Add(delay)
	}
	return nil
}

type integrationRandom struct{}

func (integrationRandom) Intn(int) int { return 0 }

func (integrationRandom) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
