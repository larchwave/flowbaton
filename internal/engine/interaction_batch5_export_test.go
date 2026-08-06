package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// Batch5ReportScenario exposes one immutable private-root projection to the
// external report adapter tests.
type Batch5ReportScenario struct {
	Name     string
	Flow     FlowResult
	Events   []Event
	Config   model.Config
	Requests []device.InputTextRequest
	Err      error
}

// Batch5PublicBoundaryObservation records the public registry boundary for one
// clipboard command.
type Batch5PublicBoundaryObservation struct {
	Keyword       model.CommandKeyword
	PreparePassed bool
	Results       int
	ExecuteErr    error
	DriverActions int
	RuntimeCalls  int
	ListenerCalls int
}

// ExecuteBatch5ReportScenariosForTest exposes success, optional, Driver, and
// setter-poison roots only to the external report-projection test package.
func ExecuteBatch5ReportScenariosForTest(t testing.TB) []Batch5ReportScenario {
	t.Helper()

	return []Batch5ReportScenario{
		batch5SuccessReportScenario(t),
		batch5OptionalReportScenario(t),
		batch5DriverFailureReportScenario(t),
		batch5PoisonReportScenario(t),
	}
}

func batch5SuccessReportScenario(t testing.TB) Batch5ReportScenario {
	t.Helper()
	config := model.Config{
		Name: "Batch 5 success", AppID: "com.example.batch5.report",
		Env: map[string]string{"VALUE": "seed 世界"},
	}
	commands := []model.Command{
		batch5SetCommand("${VALUE}"),
		batch5CopyIDCommand("target", nil, stringPointer("copy target")),
		batch5PasteCommand(),
	}
	root, err := batch5CompileRoot(commands, config)
	if err != nil {
		t.Fatalf("batch5CompileRoot() error = %v", err)
	}
	driver := batch5CopyPasteDriver(map[string]string{"accessibilityText": "copied 世界"})
	events := make([]Event, 0, 8)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch5-report-success", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch5-report-success/root-run-000001")
	return Batch5ReportScenario{
		Name: "success", Flow: result, Events: append([]Event(nil), events...), Config: cloneConfig(config),
		Requests: batch5InputRequests(driver.Actions()), Err: runErr,
	}
}

func batch5OptionalReportScenario(t testing.TB) Batch5ReportScenario {
	t.Helper()
	config := model.Config{Name: "Batch 5 optional", AppID: "com.example.batch5.optional"}
	root, err := batch5CompileRoot(
		[]model.Command{batch5CopyIDCommand("missing", boolPointer(true), stringPointer("optional copy"))},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch5DeviceInfoOnlyDriver()
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch5-report-optional", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch5-report-optional/root-run-000001")
	return Batch5ReportScenario{
		Name: "optional", Flow: result, Events: append([]Event(nil), events...), Config: cloneConfig(config), Err: runErr,
	}
}

func batch5DriverFailureReportScenario(t testing.TB) Batch5ReportScenario {
	t.Helper()
	config := model.Config{Name: "Batch 5 Driver failure", AppID: "com.example.batch5.driver-failure"}
	root, err := batch5CompileRoot(
		[]model.Command{batch5SetCommand("driver failure text"), batch5PasteCommand()},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	driver := batch5PasteDriver()
	driver.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{{Err: errors.New("Batch 5 InputText failed")}}})
	events := make([]Event, 0, 6)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch5-report-driver", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch5-report-driver/root-run-000001")
	return Batch5ReportScenario{
		Name: "driver", Flow: result, Events: append([]Event(nil), events...), Config: cloneConfig(config),
		Requests: batch5InputRequests(driver.Actions()), Err: runErr,
	}
}

func batch5PoisonReportScenario(t testing.TB) Batch5ReportScenario {
	t.Helper()
	config := model.Config{Name: "Batch 5 poison", AppID: "com.example.batch5.poison-report"}
	root, err := batch5CompileRoot(
		[]model.Command{batch5SetCommand("poison value"), batch5PasteCommand()},
		config,
	)
	if err != nil {
		t.Fatal(err)
	}
	// The command performs the first copiedText write.
	base := &sessionRuntime{}
	base.SetCopiedError(errors.New("Batch 5 setter failed"))
	factory := &sessionRuntimeFactory{runtime: &batch5LiteralSessionRuntime{sessionRuntime: base}}
	driver := batch5DeviceInfoOnlyDriver()
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch5-report-poison", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: factory, Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch5-report-poison/root-run-000001")
	return Batch5ReportScenario{
		Name: "poison", Flow: result, Events: append([]Event(nil), events...), Config: cloneConfig(config), Err: runErr,
	}
}

// ObserveBatch5PublicBoundaryForTest records public execution for every
// clipboard command.
func ObserveBatch5PublicBoundaryForTest(t testing.TB) []Batch5PublicBoundaryObservation {
	t.Helper()
	commands := []model.Command{
		batch5CopyIDCommand("target", nil, nil),
		batch5SetCommand("value"),
		batch5PasteCommand(),
	}
	observations := make([]Batch5PublicBoundaryObservation, 0, len(commands))
	for _, command := range commands {
		path := "/workspace/batch5-public-" + string(command.Kind) + ".yaml"
		flowModel := model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: path,
			Config:   model.Config{AppID: "com.example.batch5.public"},
			Commands: []model.Command{command},
		}
		program, prepareErr := Prepare(
			context.Background(), model.ExecutionPlan{SelectedRoots: []string{path}},
			tapPreparedFlowLoader{flows: map[string]model.Flow{path: flowModel}},
		)
		observation := Batch5PublicBoundaryObservation{Keyword: command.Kind, PreparePassed: prepareErr == nil}
		if prepareErr != nil {
			observation.ExecuteErr = prepareErr
			observations = append(observations, observation)
			continue
		}
		driver := batch5DeviceInfoOnlyDriver()
		factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
		listenerCalls := 0
		results, executeErr := Execute(context.Background(), program, Dependencies{
			ExecutionID: "batch5-public-rejection", Driver: driver, Clock: newAdvancingClock(),
			JSFactory: factory, Controller: NoopController{},
			Listeners: []Listener{ListenerFunc(func(context.Context, Event) error {
				listenerCalls++
				return nil
			})},
		})
		observation.Results = len(results)
		observation.ExecuteErr = executeErr
		observation.DriverActions = len(driver.Actions())
		observation.RuntimeCalls = factory.calls
		observation.ListenerCalls = listenerCalls
		observations = append(observations, observation)
	}
	return observations
}
