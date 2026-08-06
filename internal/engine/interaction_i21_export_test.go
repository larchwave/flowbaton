package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// InteractionI21ReportScenario is an owned test-only projection consumed by
// the external report adapter test. It does not expose a production registry.
type InteractionI21ReportScenario struct {
	Name              string
	Config            model.Config
	Flow              FlowResult
	Events            []Event
	DriverActions     []enginetest.Action
	GeneratorRequests []InputRequest
	Err               error
}

// ExecuteInteractionI21ReportScenariosForTest executes fixed interaction fixtures
// through the direct test registry and returns defensive scenario snapshots.
func ExecuteInteractionI21ReportScenariosForTest(t testing.TB) []InteractionI21ReportScenario {
	t.Helper()
	scenarios := []InteractionI21ReportScenario{
		interactionI21HappyReportScenario(t),
		interactionI21CompletedReportScenario(t),
		interactionI21WarnedReportScenario(t),
		interactionI21FailedReportScenario(t),
		interactionI21CancelledReportScenario(t),
	}
	for _, scenario := range scenarios {
		assertInteractionI21ReportScenarioShape(t, scenario)
	}
	return scenarios
}

func interactionI21HappyReportScenario(t testing.TB) InteractionI21ReportScenario {
	t.Helper()
	generator := &batch4HSequenceGenerator{values: []string{
		"rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color",
	}}
	return interactionI21ExecuteReportFixture(t, "happy", "happy.yaml", context.Background(), interactionI21HappyDriver(), generator)
}

func interactionI21CompletedReportScenario(t testing.TB) InteractionI21ReportScenario {
	t.Helper()
	return interactionI21ExecuteReportFixture(t, "completed", "clipboard.yaml", context.Background(), interactionI21ClipboardDriver(), nil)
}

func interactionI21WarnedReportScenario(t testing.TB) InteractionI21ReportScenario {
	t.Helper()
	driver := interactionI21MissingTargetDriver(100)
	return interactionI21ExecuteReportFixture(t, "warned", "warned.yaml", context.Background(), driver, nil)
}

func interactionI21FailedReportScenario(t testing.TB) InteractionI21ReportScenario {
	t.Helper()
	driver := interactionI21MissingTargetDriver(220)
	return interactionI21ExecuteReportFixture(t, "failed", "failed.yaml", context.Background(), driver, nil)
}

func interactionI21CancelledReportScenario(t testing.TB) InteractionI21ReportScenario {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	driver := interactionI21MissingTargetDriver(8)
	wrapped := &batch3CancelAfterScrollDriver{Driver: driver, cancel: cancel}
	scenario := interactionI21ExecuteReportFixture(t, "cancelled", "cancelled.yaml", ctx, wrapped, nil)
	scenario.DriverActions = append([]enginetest.Action(nil), driver.Actions()...)
	return scenario
}

func interactionI21ExecuteReportFixture(t testing.TB, name, fixture string, ctx context.Context, driver device.Driver, generator InputGenerator) InteractionI21ReportScenario {
	t.Helper()
	program, loader := interactionI21Prepare(t, fixture)
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatalf("compile report fixture %s: %v", fixture, err)
	}
	root := interactionI21FirstRoot(t, compiled)
	events := make([]Event, 0, 48)
	var artifactSink ArtifactSink
	if name == "warned" || name == "failed" {
		artifactSink = &failureEvidenceSink{responses: []failureEvidenceSinkResponse{{result: validFailureEvidenceResult("i21-"+name+"-failure.png", int64(len("i21-failure-png")))}}}
	}
	result, runErr := executeCompiledRootForRun(ctx, Dependencies{
		ExecutionID: "i21-report-" + name, Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: generator,
		ArtifactSink: artifactSink,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "i21-report-"+name+"/root-run-000001")
	loader.AssertUnchanged(t)
	var actions []enginetest.Action
	if recorded, ok := driver.(interface{ Actions() []enginetest.Action }); ok {
		actions = append([]enginetest.Action(nil), recorded.Actions()...)
	}
	var generatorRequests []InputRequest
	if recorded, ok := generator.(interface{ Requests() []InputRequest }); ok {
		generatorRequests = recorded.Requests()
	}
	return InteractionI21ReportScenario{
		Name: name, Config: cloneConfig(root.config), Flow: result,
		Events: cloneEvents(events), DriverActions: actions, GeneratorRequests: generatorRequests, Err: runErr,
	}
}

func interactionI21MissingTargetDriver(descriptorCount int) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 32)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	descriptors := make([]enginetest.Result[device.TreeNode], descriptorCount)
	for index := range descriptors {
		descriptors[index].Value = device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}}
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: infos, ContentDescriptor: descriptors,
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: []byte("i21-failure-png")}},
	})
	return driver
}

func cloneEvents(events []Event) []Event {
	cloned := make([]Event, len(events))
	for index, event := range events {
		cloned[index] = Event{
			rootRunID: event.RootRunID(), kind: event.Kind(), at: event.At(), sequence: event.Sequence(), depth: event.Depth(),
			flowPath: event.FlowPath(), outcome: event.Outcome(), productError: event.ProductError(), metadata: event.Metadata(), artifacts: event.Artifacts(),
		}
		if command, ok := event.Command(); ok {
			command = cloneCommand(command)
			cloned[index].command = &command
		}
	}
	return cloned
}

func assertInteractionI21ReportScenarioShape(t testing.TB, scenario InteractionI21ReportScenario) {
	t.Helper()
	wantOutcome := map[string]Outcome{"happy": Completed, "completed": Completed, "warned": Warned, "failed": Failed, "cancelled": Cancelled}[scenario.Name]
	if scenario.Flow.Outcome() != wantOutcome {
		t.Fatalf("scenario %s outcome = %s, want %s", scenario.Name, scenario.Flow.Outcome(), wantOutcome)
	}
	if scenario.Name == "happy" || scenario.Name == "completed" || scenario.Name == "warned" {
		if scenario.Err != nil {
			t.Fatalf("scenario %s error = %v", scenario.Name, scenario.Err)
		}
	} else if scenario.Err == nil {
		t.Fatalf("scenario %s error = nil", scenario.Name)
	}
	if scenario.Name == "cancelled" && !errors.Is(scenario.Err, context.Canceled) {
		t.Fatalf("cancelled error = %T %v", scenario.Err, scenario.Err)
	}
}
