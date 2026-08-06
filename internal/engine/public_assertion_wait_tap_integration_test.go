package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/imagecheck"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
	"github.com/larchwave/flowbaton/internal/report"
)

func TestPublicAssertionWaitTapHappyFixtureRunsThroughEveryPublicSurface(t *testing.T) {
	t.Parallel()

	program, source, before := prepareG003PublicFixture(t, "happy.yaml")
	driver := enginetest.NewFakeDriver()
	ready := g003PublicTree("Ready")
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready}},
	})
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	trace := &g003PublicTrace{}
	clock := newG003PublicClock(time.Unix(1_700_500_000, 0), trace)
	sink := &g003PublicArtifactSink{}
	checker := &g003PublicChecker{trace: trace}
	events := make([]engine.Event, 0, 10)
	results, err := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "g003-batch5-public-happy", Driver: driver,
		Clock: clock, JSFactory: factory, Controller: engine.NoopController{}, ArtifactSink: sink, ImageChecker: checker,
		Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
			if command, exists := event.Command(); exists {
				command.Source.Path = "listener-mutated.yaml"
				if fields, ok := command.Arguments.(map[string]any); ok {
					fields["listenerMutation"] = true
				}
			}
			if evaluated, exists := event.Metadata().EvaluatedCommand(); exists {
				evaluated.Source.Path = "listener-mutated-evaluated.yaml"
			}
			events = append(events, event)
			return nil
		})},
	})
	if err != nil {
		t.Fatalf("engine.Execute() error = %T %v", err, err)
	}
	if len(results) != 1 || results[0].Outcome() != engine.Completed {
		t.Fatalf("engine.Execute() results = %#v", results)
	}
	root := results[0]
	commands := root.Commands()
	wantKeywords := []model.CommandKeyword{
		model.CommandAssertVisible,
		model.CommandAssertNotVisible,
		model.CommandAssertTrue,
		model.CommandExtendedWaitUntil,
	}
	gotKeywords := make([]model.CommandKeyword, len(commands))
	for index, command := range commands {
		gotKeywords[index] = command.Command().Kind
		if command.Outcome() != engine.Completed || len(command.Artifacts()) != 0 {
			t.Fatalf("command %d = outcome %s artifacts %#v", index, command.Outcome(), command.Artifacts())
		}
	}
	if !reflect.DeepEqual(gotKeywords, wantKeywords) {
		t.Fatalf("command order = %#v, want %#v", gotKeywords, wantKeywords)
	}
	actions := driver.Actions()
	methods := make([]enginetest.Method, len(actions))
	for index, action := range actions {
		methods[index] = action.Method
		if action.Sequence != uint64(index+1) {
			t.Fatalf("happy action %d sequence = %d, want %d", index, action.Sequence, index+1)
		}
		if index == 0 && action.Request != nil {
			t.Fatalf("happy DeviceInfo request = %#v, want nil", action.Request)
		}
		// Every hierarchy request is scoped to the flow's app ID.
		if index > 0 && !reflect.DeepEqual(action.Request, device.ContentDescriptorRequest{
			AppIDs: []string{source.Config.AppID},
		}) {
			t.Fatalf("happy hierarchy request %d = %#v, want the flow's app %q",
				index, action.Request, source.Config.AppID)
		}
	}
	wantMethods := []enginetest.Method{
		enginetest.MethodDeviceInfo,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
		enginetest.MethodContentDescriptor,
	}
	if !reflect.DeepEqual(methods, wantMethods) || len(clock.Waits()) != 0 || len(sink.Requests()) != 0 || len(checker.Requests()) != 0 {
		t.Fatalf("happy effects = methods %#v waits %#v sink %#v checker %#v", methods, clock.Waits(), sink.Requests(), checker.Requests())
	}
	reportResult, err := report.FromEngineFlowResult(root, source.Config)
	if err != nil || reportResult.Status != report.Completed || len(reportResult.Artifacts) != 0 || len(reportResult.Commands) != len(commands) {
		t.Fatalf("report = %#v error %v", reportResult, err)
	}
	if len(events) != 10 {
		t.Fatalf("happy listener events = %d, want ten: %#v", len(events), events)
	}
	for index, command := range commands {
		finished := findG003PublicEvent(t, events, engine.EventCommandFinished, command.Sequence())
		assertG003PublicProjectionAgreement(t, command, finished, reportResult.Commands[index])
	}
	flowFinished := findG003PublicEvent(t, events, engine.EventFlowFinished, 0)
	if flowFinished.RootRunID() != root.RootRunID() || flowFinished.Outcome() != root.Outcome() || flowFinished.ProductError() != root.ProductError() || flowFinished.FlowPath() != root.Path() {
		t.Fatalf("happy flow result/event disagree: result=%#v event=%#v", root, flowFinished)
	}
	assertG003PublicProgramUnchanged(t, program, before)
}

func TestPublicAssertionWaitTapRetryFixtureHasExactOneAndTwoBatchTrace(t *testing.T) {
	t.Parallel()

	program, source, before := prepareG003PublicFixture(t, "retry.yaml")
	trace := &g003PublicTrace{}
	clock := newG003PublicClock(time.Unix(1_700_500_100, 0), trace)
	baseDriver := enginetest.NewFakeDriver()
	firstExpected := g003PublicTree("First expected")
	firstChanged := g003PublicTree("First changed")
	secondExpected := g003PublicTree("Second expected")
	expectedPNG, thresholdPNG := g003PublicThresholdPNGs(t)
	expectedSnapshot := append([]byte(nil), expectedPNG...)
	thresholdSnapshot := append([]byte(nil), thresholdPNG...)
	baseDriver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: firstExpected}, {Value: secondExpected}},
		TakeScreenshot: []enginetest.Result[[]byte]{
			{Value: expectedPNG}, {Value: expectedPNG}, {Value: thresholdPNG},
		},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{
			{Value: &device.ViewHierarchy{Root: firstChanged}},
			{Value: &device.ViewHierarchy{Root: secondExpected}},
			{Value: nil},
		},
	})
	driver := &g003PublicTraceDriver{Driver: baseDriver, trace: trace}
	checker := &g003PublicChecker{trace: trace}
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	results, err := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "g003-batch5-public-retry", Driver: driver, Clock: clock,
		JSFactory: factory, Controller: engine.NoopController{}, ImageChecker: checker,
	})
	if err != nil || len(results) != 1 || results[0].Outcome() != engine.Completed {
		t.Fatalf("engine.Execute() = results %#v error %T %v", results, err, err)
	}
	commands := results[0].Commands()
	if len(commands) != 2 {
		t.Fatalf("retry commands = %#v, want two", commands)
	}
	for index, wantRuns := range []int{1, 2} {
		metadata := commands[index].Metadata()
		if commands[index].Command().Kind != model.CommandTapOn || commands[index].Outcome() != engine.Completed ||
			!metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != wantRuns || len(commands[index].Artifacts()) != 0 {
			t.Fatalf("retry command %d = kind %s outcome %s runs %d/%t artifacts %#v", index,
				commands[index].Command().Kind, commands[index].Outcome(), metadata.NumberOfRuns(), metadata.HasNumberOfRuns(), commands[index].Artifacts())
		}
	}
	wantTrace := []string{
		"driver:device-info",
		"driver:hierarchy",
		"driver:screenshot:false",
		"driver:tap:10,20",
		"driver:settle:0:present",
		"driver:hierarchy",
		"driver:screenshot:false",
		"driver:tap:30,40",
		"driver:settle:0:present",
		"driver:screenshot:false",
		"checker:check",
		"driver:tap:30,40",
		"driver:settle:0:none",
	}
	if got := trace.Snapshot(); !reflect.DeepEqual(got, wantTrace) {
		t.Fatalf("retry trace = %#v, want %#v", got, wantTrace)
	}
	if waits := clock.Waits(); len(waits) != 0 {
		t.Fatalf("retry clock waits = %#v, want none", waits)
	}
	actions := baseDriver.Actions()
	settles := make([]device.SettleRequest, 0, 3)
	for _, action := range actions {
		if action.Method == enginetest.MethodTakeScreenshot && action.Request != (device.ScreenshotRequest{Compressed: false}) {
			t.Fatalf("retry screenshot request = %#v, want uncompressed", action.Request)
		}
		if action.Method == enginetest.MethodWaitForAppToSettle {
			settles = append(settles, action.Request.(device.SettleRequest))
		}
	}
	if len(settles) != 3 || settles[0].InitialHierarchy == nil || !reflect.DeepEqual(settles[0].InitialHierarchy.Root, firstExpected) ||
		settles[1].InitialHierarchy == nil || !reflect.DeepEqual(settles[1].InitialHierarchy.Root, secondExpected) || settles[2].InitialHierarchy != nil {
		t.Fatalf("retry settle ownership = %#v", settles)
	}
	expectedPNG[0] ^= 0xff
	thresholdPNG[0] ^= 0xff
	requests := checker.Requests()
	if len(requests) != 1 || !bytes.Equal(requests[0].Expected, expectedSnapshot) || !bytes.Equal(requests[0].Actual, thresholdSnapshot) || requests[0].Crop != nil {
		t.Fatalf("checker requests = %#v, want one exact owned threshold check", requests)
	}
	check, checkErr := imagecheck.Check(requests[0].Expected, requests[0].Actual, nil)
	if checkErr != nil || check.DifferentPixels() != 1 || check.TotalPixels() != 200 || check.DifferenceRatio() != 0.005 {
		t.Fatalf("threshold check = %d/%d ratio %v error %v, want 1/200 and 0.005",
			check.DifferentPixels(), check.TotalPixels(), check.DifferenceRatio(), checkErr)
	}
	reportResult, err := report.FromEngineFlowResult(results[0], source.Config)
	if err != nil || reportResult.Status != report.Completed || len(reportResult.Commands) != 2 ||
		reportResult.Commands[0].Metadata["numberOfRuns"] != "1" || reportResult.Commands[1].Metadata["numberOfRuns"] != "2" {
		t.Fatalf("retry report = %#v error %v", reportResult, err)
	}
	assertG003PublicProgramUnchanged(t, program, before)
}

func TestPublicAssertionWaitTapWaitVisibleFixtureHasExactBoundedTrace(t *testing.T) {
	t.Parallel()

	program, source, before := prepareG003PublicFixture(t, "wait-visible.yaml")
	trace := &g003PublicTrace{}
	clock := newG003PublicClock(time.Unix(1_700_500_200, 0), trace)
	baseDriver := enginetest.NewFakeDriver()
	target := g003PublicTree("Awaited")
	empty := g003PublicTree("Other")
	baseDriver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{
			{Value: target}, {Value: target},
			{Value: empty}, {Value: empty}, {Value: target},
		},
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: nil}},
	})
	driver := &g003PublicTraceDriver{Driver: baseDriver, trace: trace}
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	results, err := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "g003-batch5-public-wait-visible", Driver: driver, Clock: clock,
		JSFactory: factory, Controller: engine.NoopController{},
	})
	if err != nil || len(results) != 1 || results[0].Outcome() != engine.Completed {
		t.Fatalf("engine.Execute() = results %#v error %T %v", results, err, err)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Command().Kind != model.CommandTapOn || commands[0].Outcome() != engine.Completed ||
		!commands[0].Metadata().HasNumberOfRuns() || commands[0].Metadata().NumberOfRuns() != 1 {
		t.Fatalf("wait-visible commands = %#v", commands)
	}
	wantTrace := []string{
		"driver:device-info",
		"driver:hierarchy",
		"clock:wait:100ms",
		"driver:hierarchy",
		"driver:tap:60,30",
		"driver:settle:0:none",
		"driver:hierarchy",
		"clock:wait:1s",
		"driver:hierarchy",
		"clock:wait:1s",
		"driver:hierarchy",
	}
	if got := trace.Snapshot(); !reflect.DeepEqual(got, wantTrace) {
		t.Fatalf("wait-visible trace = %#v, want %#v", got, wantTrace)
	}
	if got, want := clock.Waits(), []time.Duration{100 * time.Millisecond, time.Second, time.Second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("wait-visible waits = %#v, want %#v", got, want)
	}
	reportResult, err := report.FromEngineFlowResult(results[0], source.Config)
	if err != nil || reportResult.Status != report.Completed || len(reportResult.Commands) != 1 || reportResult.Commands[0].Status != report.Completed {
		t.Fatalf("wait-visible report = %#v error %v", reportResult, err)
	}
	assertG003PublicProgramUnchanged(t, program, before)
}

func TestPublicAssertionWaitTapWarnedAndFailedArtifactsAgreeAcrossSurfaces(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name             string
		wantOutcome      engine.Outcome
		wantReportStatus report.Status
		wantCommands     int
		wantSurfaceError bool
	}{
		{name: "warned", wantOutcome: engine.Warned, wantReportStatus: report.Warned, wantCommands: 2},
		{name: "failed", wantOutcome: engine.Failed, wantReportStatus: report.Failed, wantCommands: 1, wantSurfaceError: true},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			program, source, before := prepareG003PublicFixture(t, test.name+".yaml")
			driver := enginetest.NewFakeDriver()
			diagnostic := enginetest.PNGFixture("g003-public-failure-png")
			driver.Enqueue(enginetest.DriverScript{
				DeviceInfo:     []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 200, HeightGrid: 400}}},
				TakeScreenshot: []enginetest.Result[[]byte]{{Value: diagnostic}},
			})
			artifactPath := "sink/g003-" + test.name + ".png"
			sink := &g003PublicArtifactSink{result: engine.ArtifactWriteResult{
				Artifact:     device.Artifact{Kind: "failure-screenshot", Path: artifactPath, Metadata: map[string]string{"provider": "public"}},
				BytesWritten: int64(len(diagnostic)),
			}}
			factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
			if err != nil {
				t.Fatalf("js.NewFactory() error = %v", err)
			}
			events := make([]engine.Event, 0, 6)
			results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
				ExecutionID: "g003-batch5-public-" + test.name, Driver: driver,
				Clock: &integrationClock{now: time.Unix(1_700_500_300, 0)}, JSFactory: factory, Controller: engine.NoopController{},
				ArtifactSink: sink,
				Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
					if command, exists := event.Command(); exists {
						command.Source.Path = "listener-mutated.yaml"
					}
					artifacts := event.Artifacts()
					if len(artifacts) != 0 {
						artifacts[0].Path = "listener-mutated.png"
						artifacts[0].Metadata["provider"] = "listener-mutated"
					}
					events = append(events, event)
					return nil
				})},
			})
			if len(results) != 1 {
				t.Fatalf("engine.Execute() results = %d error %T %v", len(results), executeErr, executeErr)
			}
			root := results[0]
			commands := root.Commands()
			if root.Outcome() != test.wantOutcome || len(commands) != test.wantCommands {
				t.Fatalf("flow = outcome %s commands %d, want %s/%d", root.Outcome(), len(commands), test.wantOutcome, test.wantCommands)
			}
			first := commands[0]
			var assertion *engine.AssertionError
			if first.Outcome() != test.wantOutcome || !errors.As(first.ProductError(), &assertion) {
				t.Fatalf("first command = outcome %s error %T %v", first.Outcome(), first.ProductError(), first.ProductError())
			}
			commandFinished := findG003PublicEvent(t, events, engine.EventCommandFinished, 1)
			flowFinished := findG003PublicEvent(t, events, engine.EventFlowFinished, 0)
			if commandFinished.ProductError() != first.ProductError() || commandFinished.Outcome() != first.Outcome() ||
				commandFinished.RootRunID() != first.RootRunID() || commandFinished.Sequence() != first.Sequence() || commandFinished.Depth() != first.Depth() ||
				!reflect.DeepEqual(commandFinished.Metadata(), first.Metadata()) {
				t.Fatalf("command result/event disagree: result=%#v event=%#v", first, commandFinished)
			}
			if test.wantSurfaceError {
				if executeErr != first.ProductError() || root.ProductError() != first.ProductError() || flowFinished.ProductError() != first.ProductError() {
					t.Fatalf("failed primary identity = returned %T %v command %T %v flow %T %v event %T %v",
						executeErr, executeErr, first.ProductError(), first.ProductError(), root.ProductError(), root.ProductError(), flowFinished.ProductError(), flowFinished.ProductError())
				}
			} else {
				if executeErr != nil || root.ProductError() != nil || flowFinished.ProductError() != nil || flowFinished.Outcome() != engine.Warned {
					t.Fatalf("warned flow surfaced error = returned %T %v flow %T %v event %T %v/%s",
						executeErr, executeErr, root.ProductError(), root.ProductError(), flowFinished.ProductError(), flowFinished.ProductError(), flowFinished.Outcome())
				}
				if commands[1].Command().Kind != model.CommandAssertTrue || commands[1].Outcome() != engine.Completed || commands[1].ProductError() != nil || len(commands[1].Artifacts()) != 0 {
					t.Fatalf("warned flow did not continue cleanly: %#v", commands[1])
				}
			}
			assertG003PublicFailureArtifact(t, first.Artifacts(), artifactPath)
			assertG003PublicFailureArtifact(t, commandFinished.Artifacts(), artifactPath)
			assertG003PublicFailureArtifact(t, root.Commands()[0].Artifacts(), artifactPath)

			actions := driver.Actions()
			if len(actions) != 2 || actions[0].Method != enginetest.MethodDeviceInfo || actions[1].Method != enginetest.MethodTakeScreenshot ||
				actions[1].Request != (device.ScreenshotRequest{Compressed: true}) {
				t.Fatalf("failure screenshot actions = %#v, want device info then one compressed capture", actions)
			}
			requests := sink.Requests()
			wantMetadata := map[string]string{
				"keyword": "assertTrue", "rootRunId": first.RootRunID(), "sequence": "1", "depth": "0",
				"outcome": string(test.wantOutcome), "sourcePath": source.Path,
			}
			if len(requests) != 1 || requests[0].Owner != first.RootRunID() || requests[0].Kind != "failure-screenshot" ||
				requests[0].SuggestedName != "failure-000001.png" || !bytes.Equal(requests[0].Data, diagnostic) || !reflect.DeepEqual(requests[0].Metadata, wantMetadata) {
				t.Fatalf("failure artifact request = %#v, want metadata %#v", requests, wantMetadata)
			}

			reportResult, reportErr := report.FromEngineFlowResult(root, source.Config)
			if reportErr != nil || reportResult.Status != test.wantReportStatus || len(reportResult.Commands) != test.wantCommands {
				t.Fatalf("failure report = %#v error %v", reportResult, reportErr)
			}
			for index, command := range commands {
				finished := findG003PublicEvent(t, events, engine.EventCommandFinished, command.Sequence())
				assertG003PublicProjectionAgreement(t, command, finished, reportResult.Commands[index])
			}
			wantReportArtifact := report.Artifact{Kind: "failure-screenshot", Path: artifactPath}
			if !reflect.DeepEqual(reportResult.Artifacts, []report.Artifact{wantReportArtifact}) ||
				!reflect.DeepEqual(reportResult.Commands[0].Artifacts, []report.Artifact{wantReportArtifact}) ||
				reportResult.Commands[0].Failure == nil || reportResult.Commands[0].Failure.Message != fmt.Sprint(first.ProductError()) {
				t.Fatalf("failure report projections = %#v", reportResult)
			}
			if test.wantSurfaceError {
				if reportResult.Failure == nil || reportResult.Failure.Message != fmt.Sprint(first.ProductError()) {
					t.Fatalf("failed flow report failure = %#v", reportResult.Failure)
				}
			} else if reportResult.Failure != nil {
				t.Fatalf("warned flow report failure = %#v, want nil", reportResult.Failure)
			}
			assertG003PublicProgramUnchanged(t, program, before)
		})
	}
}

func TestPublicAssertionWaitTapInvalidFixturesCompileBeforeEveryEffect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		fixtures []string
	}{
		{name: "static invalid", fixtures: []string{"static-invalid.yaml"}},
		{name: "later root invalid", fixtures: []string{"happy.yaml", "later-root-invalid.yaml"}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			program, _, before := prepareG003PublicProgram(t, test.fixtures...)
			driver := enginetest.NewFakeDriver()
			delegate, err := js.NewFactory(js.Config{Random: integrationRandom{}})
			if err != nil {
				t.Fatalf("js.NewFactory() error = %v", err)
			}
			factory := &countingIntegrationFactory{delegate: delegate}
			clock := newG003PublicClock(time.Unix(1_700_500_400, 0), &g003PublicTrace{})
			sink := &g003PublicArtifactSink{}
			checker := &g003PublicChecker{trace: &g003PublicTrace{}}
			var events []engine.Event
			results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
				ExecutionID: "g003-batch5-public-invalid", Driver: driver, Clock: clock, JSFactory: factory,
				Controller: engine.NoopController{}, ArtifactSink: sink, ImageChecker: checker,
				Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
					events = append(events, event)
					return nil
				})},
			})
			var configuration *engine.ConfigurationError
			if !errors.As(executeErr, &configuration) {
				t.Fatalf("engine.Execute() error = %T %v, want *ConfigurationError", executeErr, executeErr)
			}
			if len(results) != 0 || factory.Calls() != 0 || len(driver.Actions()) != 0 || len(events) != 0 ||
				len(sink.Requests()) != 0 || len(checker.Requests()) != 0 || len(clock.Waits()) != 0 {
				t.Fatalf("invalid graph reached effects: results=%#v runtime=%d driver=%#v events=%#v sink=%#v check=%#v waits=%#v",
					results, factory.Calls(), driver.Actions(), events, sink.Requests(), checker.Requests(), clock.Waits())
			}
			assertG003PublicProgramUnchanged(t, program, before)
		})
	}
}

func TestPublicAssertionWaitTapPreparedProgramIsReadFreeAndOwnedAcrossReuse(t *testing.T) {
	t.Parallel()

	loader := &g003CountingLoader{delegate: capability.FileLoader{}}
	fixture := g003PublicFixture("happy.yaml")
	program, err := engine.Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{fixture}}, loader)
	if err != nil {
		t.Fatalf("engine.Prepare() error = %v", err)
	}
	canonicalCalls, loadCalls := loader.Counts()
	if canonicalCalls != 1 || loadCalls != 1 {
		t.Fatalf("Prepare loader calls = canonical %d load %d, want 1/1", canonicalCalls, loadCalls)
	}
	before := snapshotG003PublicProgram(t, program)
	if len(before.roots) != 1 {
		t.Fatalf("prepared roots = %#v, want one", before.roots)
	}
	source := before.flows[before.roots[0]]

	for index := 0; index < 2; index++ {
		if runErr := runG003PublicHappyReuse(program, source, fmt.Sprintf("sequential-%d", index)); runErr != nil {
			t.Fatalf("sequential reuse %d: %v", index, runErr)
		}
		assertG003PublicProgramUnchanged(t, program, before)
	}

	const workers = 12
	var wait sync.WaitGroup
	errCh := make(chan error, workers)
	for index := 0; index < workers; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			errCh <- runG003PublicHappyReuse(program, source, fmt.Sprintf("concurrent-%02d", index))
		}()
	}
	wait.Wait()
	close(errCh)
	for runErr := range errCh {
		if runErr != nil {
			t.Error(runErr)
		}
	}
	assertG003PublicProgramUnchanged(t, program, before)
	afterCanonical, afterLoad := loader.Counts()
	if afterCanonical != canonicalCalls || afterLoad != loadCalls {
		t.Fatalf("Execute performed file reads: loader calls before %d/%d after %d/%d", canonicalCalls, loadCalls, afterCanonical, afterLoad)
	}
}

func g003PublicFixture(name string) string {
	return filepath.Join("..", "..", "testdata", "flows", "g003-assertion-wait-tap", name)
}

func g003PublicTree(labels ...string) device.TreeNode {
	root := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][200,400]"}}
	for index, label := range labels {
		top := 20 + index*40
		root.Children = append(root.Children, device.TreeNode{Attributes: map[string]string{
			"text": label, "bounds": fmt.Sprintf("[10,%d][110,%d]", top, top+20),
		}})
	}
	return root
}

type g003PublicProgramSnapshot struct {
	roots []string
	paths []string
	flows map[string]model.Flow
}

type g003CountingLoader struct {
	mu             sync.Mutex
	delegate       capability.FileLoader
	canonicalCalls int
	loadCalls      int
}

var _ capability.FlowLoader = (*g003CountingLoader)(nil)

func (loader *g003CountingLoader) Canonical(ctx context.Context, path string) (string, error) {
	loader.mu.Lock()
	loader.canonicalCalls++
	loader.mu.Unlock()
	return loader.delegate.Canonical(ctx, path)
}

func (loader *g003CountingLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	loader.mu.Lock()
	loader.loadCalls++
	loader.mu.Unlock()
	return loader.delegate.Load(ctx, path)
}

func (loader *g003CountingLoader) Counts() (int, int) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	return loader.canonicalCalls, loader.loadCalls
}

func runG003PublicHappyReuse(program *engine.Program, source model.Flow, suffix string) error {
	driver := enginetest.NewFakeDriver()
	ready := g003PublicTree("Ready")
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: "android", WidthGrid: 200, HeightGrid: 400}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready}},
	})
	factory, err := js.NewFactory(js.Config{Random: integrationRandom{}})
	if err != nil {
		return fmt.Errorf("factory: %w", err)
	}
	eventCount := 0
	results, executeErr := engine.Execute(context.Background(), program, engine.Dependencies{
		ExecutionID: "g003-batch5-reuse-" + suffix, Driver: driver,
		Clock: &integrationClock{now: time.Unix(1_700_500_500, 0)}, JSFactory: factory, Controller: engine.NoopController{},
		Listeners: []engine.Listener{engine.ListenerFunc(func(_ context.Context, event engine.Event) error {
			eventCount++
			if command, exists := event.Command(); exists {
				command.Source.Path = "listener-mutated.yaml"
				if fields, ok := command.Arguments.(map[string]any); ok {
					fields["listenerMutation"] = suffix
				}
			}
			if artifacts := event.Artifacts(); len(artifacts) != 0 {
				artifacts[0].Path = "listener-mutated.png"
			}
			return nil
		})},
	})
	if executeErr != nil || len(results) != 1 || results[0].Outcome() != engine.Completed || len(results[0].Commands()) != 4 {
		return fmt.Errorf("Execute: results %d outcome %v error %w", len(results), func() engine.Outcome {
			if len(results) == 0 {
				return ""
			}
			return results[0].Outcome()
		}(), executeErr)
	}
	if eventCount != 10 {
		return fmt.Errorf("listener events = %d, want 10", eventCount)
	}
	root := results[0]
	commands := root.Commands()
	mutated := commands[0].Command()
	mutated.Source.Path = "caller-mutated.yaml"
	if fields, ok := mutated.Arguments.(map[string]any); ok {
		fields["callerMutation"] = true
	}
	metadata := commands[0].Metadata()
	if evaluated, exists := metadata.EvaluatedCommand(); exists {
		evaluated.Source.Path = "caller-mutated-evaluated.yaml"
	}
	fresh := root.Commands()[0]
	if fresh.Command().Source.Path != source.Commands[0].Source.Path {
		return fmt.Errorf("command accessor mutation escaped: %q", fresh.Command().Source.Path)
	}
	if evaluated, exists := fresh.Metadata().EvaluatedCommand(); !exists || evaluated.Source.Path != source.Commands[0].Source.Path {
		return fmt.Errorf("metadata accessor mutation escaped: %#v exists=%t", evaluated, exists)
	}

	reportResult, reportErr := report.FromEngineFlowResult(root, source.Config)
	if reportErr != nil {
		return fmt.Errorf("report: %w", reportErr)
	}
	reportResult.Name = "caller-mutated"
	reportResult.Metadata["callerMutation"] = suffix
	reportResult.Commands[0].Keyword = "caller-mutated"
	reportResult.Commands[0].Metadata["callerMutation"] = suffix
	reportResult.Commands[0].Artifacts = append(reportResult.Commands[0].Artifacts, report.Artifact{Kind: "mutated", Path: "mutated"})
	freshReport, reportErr := report.FromEngineFlowResult(root, source.Config)
	if reportErr != nil || freshReport.Name != source.Config.Name || freshReport.Commands[0].Keyword != "assertVisible" ||
		freshReport.Metadata["callerMutation"] != "" || freshReport.Commands[0].Metadata["callerMutation"] != "" || len(freshReport.Commands[0].Artifacts) != 0 {
		return fmt.Errorf("report mutation escaped: report=%#v error=%v", freshReport, reportErr)
	}
	return nil
}

func prepareG003PublicFixture(t *testing.T, name string) (*engine.Program, model.Flow, g003PublicProgramSnapshot) {
	t.Helper()
	program, sources, snapshot := prepareG003PublicProgram(t, name)
	return program, sources[name], snapshot
}

func prepareG003PublicProgram(t *testing.T, names ...string) (*engine.Program, map[string]model.Flow, g003PublicProgramSnapshot) {
	t.Helper()
	loader := capability.FileLoader{}
	selected := make([]string, len(names))
	canonicalByName := make(map[string]string, len(names))
	sources := make(map[string]model.Flow, len(names))
	for index, name := range names {
		selected[index] = g003PublicFixture(name)
		canonical, err := loader.Canonical(context.Background(), selected[index])
		if err != nil {
			t.Fatalf("FileLoader.Canonical(%s) error = %v", name, err)
		}
		source, err := loader.Load(context.Background(), canonical)
		if err != nil {
			t.Fatalf("FileLoader.Load(%s) error = %v", name, err)
		}
		canonicalByName[name] = canonical
		sources[name] = source
	}
	program, err := engine.Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: selected}, loader)
	if err != nil {
		t.Fatalf("engine.Prepare(%v) error = %v", names, err)
	}
	snapshot := snapshotG003PublicProgram(t, program)
	for _, name := range names {
		prepared, exists := snapshot.flows[canonicalByName[name]]
		if !exists || !reflect.DeepEqual(prepared, sources[name]) {
			t.Fatalf("prepared/source %s differ: prepared=%#v source=%#v", name, prepared, sources[name])
		}
	}
	return program, sources, snapshot
}

func snapshotG003PublicProgram(t *testing.T, program *engine.Program) g003PublicProgramSnapshot {
	t.Helper()
	snapshot := g003PublicProgramSnapshot{
		roots: program.Roots(), paths: program.FlowPaths(), flows: make(map[string]model.Flow),
	}
	for _, path := range snapshot.paths {
		flowModel, exists := program.Flow(path)
		if !exists {
			t.Fatalf("prepared flow %q missing", path)
		}
		snapshot.flows[path] = flowModel
	}
	return snapshot
}

func assertG003PublicProgramUnchanged(t *testing.T, program *engine.Program, before g003PublicProgramSnapshot) {
	t.Helper()
	after := snapshotG003PublicProgram(t, program)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("prepared program mutated:\nbefore=%#v\nafter=%#v", before, after)
	}
}

type g003PublicTrace struct {
	mu      sync.Mutex
	entries []string
}

func (trace *g003PublicTrace) Add(entry string) {
	trace.mu.Lock()
	trace.entries = append(trace.entries, entry)
	trace.mu.Unlock()
}

func (trace *g003PublicTrace) Snapshot() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]string(nil), trace.entries...)
}

type g003PublicClock struct {
	mu    sync.Mutex
	now   time.Time
	waits []time.Duration
	trace *g003PublicTrace
}

func newG003PublicClock(now time.Time, trace *g003PublicTrace) *g003PublicClock {
	return &g003PublicClock{now: now, trace: trace}
}

func (clock *g003PublicClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *g003PublicClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.trace.Add("clock:wait:" + delay.String())
	clock.mu.Lock()
	clock.waits = append(clock.waits, delay)
	clock.now = clock.now.Add(delay)
	clock.mu.Unlock()
	return ctx.Err()
}

func (clock *g003PublicClock) Waits() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]time.Duration(nil), clock.waits...)
}

type g003PublicTraceDriver struct {
	device.Driver
	trace *g003PublicTrace
}

func (driver *g003PublicTraceDriver) DeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	driver.trace.Add("driver:device-info")
	return driver.Driver.DeviceInfo(ctx)
}

func (driver *g003PublicTraceDriver) ContentDescriptor(ctx context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	driver.trace.Add("driver:hierarchy")
	return driver.Driver.ContentDescriptor(ctx, request)
}

func (driver *g003PublicTraceDriver) TakeScreenshot(ctx context.Context, request device.ScreenshotRequest) ([]byte, error) {
	driver.trace.Add(fmt.Sprintf("driver:screenshot:%t", request.Compressed))
	return driver.Driver.TakeScreenshot(ctx, request)
}

func (driver *g003PublicTraceDriver) Tap(ctx context.Context, request device.TapRequest) error {
	driver.trace.Add(fmt.Sprintf("driver:tap:%.0f,%.0f", request.Point.X, request.Point.Y))
	return driver.Driver.Tap(ctx, request)
}

func (driver *g003PublicTraceDriver) WaitForAppToSettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	timeout := "nil"
	if request.TimeoutMillis != nil {
		timeout = fmt.Sprint(*request.TimeoutMillis)
	}
	initial := "none"
	if request.InitialHierarchy != nil {
		initial = "present"
	}
	driver.trace.Add(fmt.Sprintf("driver:settle:%s:%s", timeout, initial))
	return driver.Driver.WaitForAppToSettle(ctx, request)
}

type g003PublicChecker struct {
	mu       sync.Mutex
	requests []engine.ImageCheckRequest
	trace    *g003PublicTrace
}

func (checker *g003PublicChecker) Check(ctx context.Context, request engine.ImageCheckRequest) (imagecheck.Result, error) {
	if err := ctx.Err(); err != nil {
		return imagecheck.Result{}, err
	}
	checker.trace.Add("checker:check")
	cloned := engine.ImageCheckRequest{
		Expected: append([]byte(nil), request.Expected...),
		Actual:   append([]byte(nil), request.Actual...),
	}
	if request.Crop != nil {
		crop := *request.Crop
		cloned.Crop = &crop
	}
	checker.mu.Lock()
	checker.requests = append(checker.requests, cloned)
	checker.mu.Unlock()
	return imagecheck.Check(request.Expected, request.Actual, request.Crop)
}

func (checker *g003PublicChecker) Requests() []engine.ImageCheckRequest {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	requests := make([]engine.ImageCheckRequest, len(checker.requests))
	for index, request := range checker.requests {
		requests[index] = engine.ImageCheckRequest{
			Expected: append([]byte(nil), request.Expected...),
			Actual:   append([]byte(nil), request.Actual...),
		}
		if request.Crop != nil {
			crop := *request.Crop
			requests[index].Crop = &crop
		}
	}
	return requests
}

func g003PublicThresholdPNGs(t *testing.T) ([]byte, []byte) {
	t.Helper()
	expected := image.NewRGBA(image.Rect(0, 0, 20, 10))
	actual := image.NewRGBA(image.Rect(0, 0, 20, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 20; x++ {
			value := color.RGBA{R: 40, G: 80, B: 120, A: 255}
			expected.Set(x, y, value)
			actual.Set(x, y, value)
		}
	}
	actual.Set(19, 9, color.RGBA{R: 41, G: 80, B: 120, A: 255})
	return encodeG003PublicPNG(t, expected), encodeG003PublicPNG(t, actual)
}

func encodeG003PublicPNG(t *testing.T, frame image.Image) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, frame); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return encoded.Bytes()
}

type g003PublicArtifactSink struct {
	mu       sync.Mutex
	requests []engine.ArtifactWriteRequest
	result   engine.ArtifactWriteResult
	err      error
}

func (sink *g003PublicArtifactSink) Write(_ context.Context, request engine.ArtifactWriteRequest) (engine.ArtifactWriteResult, error) {
	sink.mu.Lock()
	sink.requests = append(sink.requests, cloneG003PublicArtifactRequest(request))
	sink.mu.Unlock()
	return sink.result, sink.err
}

func (sink *g003PublicArtifactSink) Requests() []engine.ArtifactWriteRequest {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	requests := make([]engine.ArtifactWriteRequest, len(sink.requests))
	for index, request := range sink.requests {
		requests[index] = cloneG003PublicArtifactRequest(request)
	}
	return requests
}

func cloneG003PublicArtifactRequest(request engine.ArtifactWriteRequest) engine.ArtifactWriteRequest {
	request.Data = append([]byte(nil), request.Data...)
	if request.Metadata != nil {
		metadata := make(map[string]string, len(request.Metadata))
		for key, value := range request.Metadata {
			metadata[key] = value
		}
		request.Metadata = metadata
	}
	return request
}

func findG003PublicEvent(t *testing.T, events []engine.Event, kind engine.EventKind, sequence uint64) engine.Event {
	t.Helper()
	for _, event := range events {
		if event.Kind() == kind && event.Sequence() == sequence {
			return event
		}
	}
	t.Fatalf("event %s sequence %d missing from %#v", kind, sequence, events)
	return engine.Event{}
}

func assertG003PublicFailureArtifact(t *testing.T, artifacts []device.Artifact, path string) {
	t.Helper()
	if len(artifacts) != 1 || artifacts[0].Kind != "failure-screenshot" || artifacts[0].Path != path || artifacts[0].Metadata["provider"] != "public" {
		t.Fatalf("failure artifacts = %#v, want one finalized %q", artifacts, path)
	}
}

func assertG003PublicProjectionAgreement(
	t *testing.T,
	result engine.CommandResult,
	event engine.Event,
	reportCommand report.CommandResult,
) {
	t.Helper()
	eventCommand, exists := event.Command()
	if !exists || !reflect.DeepEqual(eventCommand, result.Command()) || event.Kind() != engine.EventCommandFinished ||
		event.RootRunID() != result.RootRunID() || event.Sequence() != result.Sequence() || event.Depth() != result.Depth() ||
		event.Outcome() != result.Outcome() || event.ProductError() != result.ProductError() ||
		!reflect.DeepEqual(event.Metadata(), result.Metadata()) || !reflect.DeepEqual(event.Artifacts(), result.Artifacts()) {
		t.Fatalf("command result/event disagree: result=%#v event=%#v", result, event)
	}
	wantStatus := map[engine.Outcome]report.Status{
		engine.Completed: report.Completed,
		engine.Skipped:   report.Skipped,
		engine.Warned:    report.Warned,
		engine.Failed:    report.Failed,
		engine.Cancelled: report.Cancelled,
	}[result.Outcome()]
	wantArtifacts := make([]report.Artifact, len(result.Artifacts()))
	for index, artifact := range result.Artifacts() {
		wantArtifacts[index] = report.Artifact{Kind: artifact.Kind, Path: artifact.Path}
	}
	if reportCommand.Sequence != int64(result.Sequence()) || reportCommand.Depth != result.Depth() ||
		reportCommand.Keyword != string(result.Command().Kind) || reportCommand.Status != wantStatus ||
		!reflect.DeepEqual(reportCommand.Artifacts, wantArtifacts) ||
		!reflect.DeepEqual(reportCommand.Metadata, g003ExpectedReportMetadata(t, result)) {
		t.Fatalf("command result/report disagree: result=%#v report=%#v", result, reportCommand)
	}
	if result.ProductError() == nil {
		if reportCommand.Failure != nil {
			t.Fatalf("successful command report failure = %#v", reportCommand.Failure)
		}
	} else if reportCommand.Failure == nil || reportCommand.Failure.Message != fmt.Sprint(result.ProductError()) {
		t.Fatalf("failed command report failure = %#v, want %q", reportCommand.Failure, result.ProductError())
	}
}

func g003ExpectedReportMetadata(t *testing.T, result engine.CommandResult) map[string]string {
	t.Helper()
	metadata := result.Metadata()
	want := map[string]string{"rootRunId": result.RootRunID()}
	if metadata.HasNumberOfRuns() {
		want["numberOfRuns"] = strconv.Itoa(metadata.NumberOfRuns())
	}
	if evaluated, exists := metadata.EvaluatedCommand(); exists {
		encoded, err := json.Marshal(evaluated)
		if err != nil {
			t.Fatalf("json.Marshal(evaluated) error = %v", err)
		}
		want["evaluatedCommand"] = string(encoded)
	}
	if logs := metadata.LogMessages(); len(logs) != 0 {
		encoded, err := json.Marshal(logs)
		if err != nil {
			t.Fatalf("json.Marshal(logs) error = %v", err)
		}
		want["logMessages"] = string(encoded)
	}
	if metadata.Insight() != "" {
		want["insight"] = metadata.Insight()
	}
	if metadata.AIReasoning() != "" {
		want["aiReasoning"] = metadata.AIReasoning()
	}
	return want
}
