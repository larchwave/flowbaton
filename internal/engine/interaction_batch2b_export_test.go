package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// Batch2BReportScenario exposes one immutable private Action root projection
// to the external report adapter tests.
type Batch2BReportScenario struct {
	Name             string
	Alias            string
	Flow             FlowResult
	Events           []Event
	Config           model.Config
	ExpectedCommands []model.Command
	FailureClass     string
	Err              error
}

// Batch2BPublicBoundaryObservation records the public registry boundary for
// action and clearKeychain.
type Batch2BPublicBoundaryObservation struct {
	Keyword       model.CommandKeyword
	PreparePassed bool
	Results       int
	ExecuteErr    error
	DriverActions int
	RuntimeCalls  int
	ListenerCalls int
}

// ExecuteBatch2BReportScenariosForTest returns success plus evaluator,
// executor, panic, typed-nil, terminal-settle, post-call cancellation, and
// session-integrity projections.
func ExecuteBatch2BReportScenariosForTest(t testing.TB) []Batch2BReportScenario {
	t.Helper()
	var typedNil *ConfigurationError
	var typedNilErr error = typedNil
	if typedNilErr == nil || !isNilValue(typedNilErr) {
		t.Fatalf("typed-nil fixture = %T %v, want non-nil error interface with nil pointer payload", typedNilErr, typedNilErr)
	}

	return []Batch2BReportScenario{
		batch2BReportScenario(t, "success", "back", model.Config{Name: "Batch 2B success", AppID: "com.example.batch2b.report.success"}, nil, nil),
		batch2BReportScenario(t, "evaluator-failure", "back", model.Config{Name: "Batch 2B evaluator failure", AppID: " "}, nil, nil),
		batch2BReportScenario(t, "executor-failure", "scroll", model.Config{Name: "Batch 2B executor failure", AppID: "com.example.batch2b.report.executor"}, nil, errors.New("Batch 2B Driver failed")),
		batch2BReportScenario(t, "evaluator-panic", "hideKeyboard", model.Config{Name: "Batch 2B evaluator panic", AppID: "com.example.batch2b.report.evaluator-panic"}, func(spec handlerSpec) handlerSpec {
			spec.evaluate = func(context.Context, evaluationContext, model.Command, any) (evaluatedDispatch, error) {
				panic("Batch 2B evaluator panic")
			}
			return spec
		}, nil),
		batch2BReportScenario(t, "executor-panic", "pasteText", model.Config{Name: "Batch 2B executor panic", AppID: "com.example.batch2b.report.executor-panic"}, func(spec handlerSpec) handlerSpec {
			spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				panic("Batch 2B executor panic")
			}
			return spec
		}, nil),
		batch2BReportScenario(t, "typed-nil-executor-failure", "back", model.Config{Name: "Batch 2B typed-nil executor failure", AppID: "com.example.batch2b.report.typed-nil"}, func(spec handlerSpec) handlerSpec {
			spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectDeviceMutation}, typedNilErr
			}
			return spec
		}, nil),
		batch2BTerminalSettleReportScenario(t),
		batch2BPostCallCancellationReportScenario(t),
		batch2BSessionIntegrityReportScenario(t),
	}
}

func batch2BReportScenario(
	t testing.TB,
	name string,
	alias string,
	config model.Config,
	mutate func(handlerSpec) handlerSpec,
	driverErr error,
) Batch2BReportScenario {
	t.Helper()
	spec := actionHandlerSpec()
	if mutate != nil {
		spec = mutate(spec)
	}
	root := batch2BReportRoot(t, name, config, []handlerSpec{spec}, []model.Command{batch2BActionCommand(alias)})
	driver := batch2ADriver("android")
	if driverErr != nil {
		driver.Enqueue(batch2AScriptFor(model.CommandKeyword(alias), driverErr))
	}
	return batch2BRunReportRoot(t, context.Background(), name, alias, config, root, Dependencies{
		Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, []model.Command{batch2BActionCommand(alias)})
}

func batch2BTerminalSettleReportScenario(t testing.TB) Batch2BReportScenario {
	t.Helper()
	const name = "terminal-settle-failure"
	const alias = "back"
	config := model.Config{Name: "Batch 2B terminal settle failure", AppID: "com.example.batch2b.report.terminal-settle"}
	root := batch2BReportRoot(t, name, config, []handlerSpec{actionHandlerSpec()}, []model.Command{batch2BActionCommand(alias)})
	terminal := NewConfigurationError("Batch 2B terminal settle failed", nil)
	driver := batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: terminal}})
	return batch2BRunReportRoot(t, context.Background(), name, alias, config, root, Dependencies{
		Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, []model.Command{batch2BActionCommand(alias)})
}

func batch2BPostCallCancellationReportScenario(t testing.TB) Batch2BReportScenario {
	t.Helper()
	const name = "post-call-cancellation"
	const alias = "pasteText"
	config := model.Config{Name: "Batch 2B post-call cancellation", AppID: "com.example.batch2b.report.post-call-cancellation"}
	root := batch2BReportRoot(t, name, config, []handlerSpec{actionHandlerSpec()}, []model.Command{batch2BActionCommand(alias)})
	ctx, cancel := context.WithCancel(context.Background())
	base := batch2ADriver("android")
	driver := &batch2BCancelAfterDriver{Driver: base, cancel: cancel}
	scenario := batch2BRunReportRoot(t, ctx, name, alias, config, root, Dependencies{
		Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, []model.Command{batch2BActionCommand(alias)})
	cancel()
	return scenario
}

func batch2BSessionIntegrityReportScenario(t testing.TB) Batch2BReportScenario {
	t.Helper()
	const name = "session-integrity-failure"
	const alias = "pasteText"
	config := model.Config{Name: "Batch 2B session integrity failure", AppID: "com.example.batch2b.report.session-integrity"}
	commands := []model.Command{batch5SetCommand("poison report value"), batch2BActionCommand(alias)}
	specs := append(clipboardHandlerSpecs(), actionHandlerSpec())
	root := batch2BReportRoot(t, name, config, specs, commands)
	// The command performs the first copiedText write.
	base := &sessionRuntime{}
	base.SetCopiedError(errors.New("Batch 2B report setter failed"))
	factory := &sessionRuntimeFactory{runtime: &batch5LiteralSessionRuntime{sessionRuntime: base}}
	return batch2BRunReportRoot(t, context.Background(), name, alias, config, root, Dependencies{
		Driver: batch2ADriver("android"), Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	}, []model.Command{batch5SetCommand("poison report value")})
}

func batch2BReportRoot(
	t testing.TB,
	name string,
	config model.Config,
	specs []handlerSpec,
	commands []model.Command,
) *compiledFlow {
	t.Helper()
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := newDispatcher(registry).compileSequence(
		context.Background(), compileContext{containingFlow: "/workspace/batch2b-report.yaml"}, commands,
	)
	if err != nil {
		t.Fatalf("compile Batch 2B report %q error = %v", name, err)
	}
	return &compiledFlow{
		path: "/workspace/batch2b-report-" + name + ".yaml", config: cloneConfig(config), body: compiled,
	}
}

func batch2BRunReportRoot(
	t testing.TB,
	ctx context.Context,
	name string,
	alias string,
	config model.Config,
	root *compiledFlow,
	dependencies Dependencies,
	expected []model.Command,
) Batch2BReportScenario {
	t.Helper()
	events := make([]Event, 0, 4)
	dependencies.ExecutionID = "batch2b-report-" + name
	dependencies.Listeners = append(dependencies.Listeners, ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	}))
	result, runErr := executeCompiledRootForRun(
		ctx, dependencies, root, "batch2b-report-"+name+"/root-run-000001",
	)
	return Batch2BReportScenario{
		Name: name, Alias: alias, Flow: result, Events: append([]Event(nil), events...), Config: cloneConfig(config),
		ExpectedCommands: cloneCommands(expected), FailureClass: batch2BFailureClass(runErr), Err: runErr,
	}
}

func batch2BFailureClass(err error) string {
	switch classifyTerminalError(err) {
	case terminalErrorNone:
		return "none"
	case terminalErrorSkipped:
		return "skipped"
	case terminalErrorCancelled:
		return "cancelled"
	case terminalErrorDeviceConnection:
		return "device-connection"
	case terminalErrorConfiguration:
		return "configuration"
	case terminalErrorRetryable:
		return "retryable"
	case terminalErrorFailure:
		return "failure"
	default:
		return "invalid"
	}
}

// ObserveBatch2BPublicBoundaryForTest records public execution for action and
// clearKeychain.
func ObserveBatch2BPublicBoundaryForTest(t testing.TB) []Batch2BPublicBoundaryObservation {
	t.Helper()
	commands := []model.Command{
		batch2BActionCommand("back"),
		batch2ABareCommand(model.CommandClearKeychain),
	}
	observations := make([]Batch2BPublicBoundaryObservation, 0, len(commands))
	for _, command := range commands {
		path := "/workspace/batch2b-public-" + string(command.Kind) + ".yaml"
		flowModel := model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: path,
			Config: model.Config{AppID: "com.example.batch2b.public"}, Commands: []model.Command{command},
		}
		program, prepareErr := Prepare(
			context.Background(), model.ExecutionPlan{SelectedRoots: []string{path}},
			tapPreparedFlowLoader{flows: map[string]model.Flow{path: flowModel}},
		)
		observation := Batch2BPublicBoundaryObservation{Keyword: command.Kind, PreparePassed: prepareErr == nil}
		if prepareErr != nil {
			observation.ExecuteErr = prepareErr
			observations = append(observations, observation)
			continue
		}
		driver := enginetest.NewFakeDriver()
		factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
		listenerCalls := 0
		results, executeErr := Execute(context.Background(), program, Dependencies{
			ExecutionID: "batch2b-public-rejection", Driver: driver, Clock: newAdvancingClock(),
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
