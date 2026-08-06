package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionI21PreparedProgramReuse(t *testing.T) {
	program, loader := interactionI21Prepare(t, "clipboard.yaml")
	before := interactionI21ProgramSnapshot(program)
	flowPaths := program.FlowPaths()
	if len(flowPaths) != 1 {
		t.Fatalf("prepared Program flow paths = %#v, want sole clipboard flow", flowPaths)
	}
	flowPath := flowPaths[0]
	preparedFlow, exists := program.Flow(flowPath)
	if !exists || len(preparedFlow.Commands) == 0 || preparedFlow.Commands[0].Kind != model.CommandCopyTextFrom || preparedFlow.Commands[0].Selector == nil || preparedFlow.Commands[0].Selector.IDRegex == nil || *preparedFlow.Commands[0].Selector.IDRegex != "copy-target" {
		t.Fatalf("prepared Program clipboard flow = %#v exists %v", preparedFlow, exists)
	}
	arguments, argumentsOK := preparedFlow.Commands[0].Arguments.(map[string]any)
	argumentID, argumentOK := arguments["id"].(string)
	if !argumentsOK || !argumentOK || argumentID != "copy-target" {
		t.Fatalf("prepared Program clipboard arguments = %#v", preparedFlow.Commands[0].Arguments)
	}
	*preparedFlow.Commands[0].Selector.IDRegex = "caller-program-flow-mutated"
	arguments["id"] = "caller-program-flow-mutated"
	freshPreparedFlow, freshExists := program.Flow(flowPath)
	if !freshExists || len(freshPreparedFlow.Commands) == 0 || freshPreparedFlow.Commands[0].Kind != model.CommandCopyTextFrom || freshPreparedFlow.Commands[0].Selector == nil || freshPreparedFlow.Commands[0].Selector.IDRegex == nil || *freshPreparedFlow.Commands[0].Selector.IDRegex != "copy-target" {
		t.Fatalf("prepared Program Flow ownership escaped: fresh flow %#v exists %v", freshPreparedFlow, freshExists)
	}
	freshArguments, freshArgumentsOK := freshPreparedFlow.Commands[0].Arguments.(map[string]any)
	freshArgumentID, freshArgumentOK := freshArguments["id"].(string)
	if !freshArgumentsOK || !freshArgumentOK || freshArgumentID != "copy-target" || !reflect.DeepEqual(interactionI21ProgramSnapshot(program), before) {
		t.Fatalf("prepared Program Flow ownership escaped: fresh arguments %#v snapshot %#v", freshPreparedFlow.Commands[0].Arguments, interactionI21ProgramSnapshot(program))
	}
	flowPaths[0] = "caller-program-flow-path-mutated"
	freshFlowPaths := program.FlowPaths()
	if len(freshFlowPaths) != 1 || freshFlowPaths[0] != flowPath || !reflect.DeepEqual(interactionI21ProgramSnapshot(program), before) {
		t.Fatalf("prepared Program Flow ownership escaped: fresh paths %#v snapshot %#v", freshFlowPaths, interactionI21ProgramSnapshot(program))
	}
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	root := interactionI21FirstRoot(t, compiled)
	for run := 1; run <= 3; run++ {
		driver := interactionI21ClipboardDriver()
		rootRunID := fmt.Sprintf("i21-reuse-%d/root-run-000001", run)
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: fmt.Sprintf("i21-reuse-%d", run), Driver: driver, Clock: newAdvancingClock(),
			JSFactory: tapJSFactory(t), Controller: NoopController{},
		}, root, rootRunID)
		if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID {
			t.Fatalf("reuse run %d = %#v, error %v", run, result, runErr)
		}
		commands := result.Commands()
		if len(commands) != 7 || commands[0].Sequence() != 1 || commands[6].Sequence() != 7 {
			t.Fatalf("reuse run %d commands = %#v", run, commands)
		}
		evaluated, ok := commands[0].Metadata().EvaluatedCommand()
		if !ok || evaluated.Selector == nil || evaluated.Selector.IDRegex == nil || *evaluated.Selector.IDRegex != "copy-target" {
			t.Fatalf("reuse run %d evaluated copy = %#v", run, evaluated)
		}
		if got := batch5InputRequests(driver.Actions()); !reflect.DeepEqual(got, []device.InputTextRequest{
			{Text: "copied-from-target", AppIDs: []string{"com.example.g003.i21.clipboard"}},
			{Text: "override-two", AppIDs: []string{"com.example.g003.i21.clipboard"}},
		}) {
			t.Fatalf("reuse run %d requests = %#v", run, got)
		}
		*evaluated.Selector.IDRegex = "caller-mutated"
		commands[0] = CommandResult{}
		freshCommands := result.Commands()
		freshEvaluated, freshOK := freshCommands[0].Metadata().EvaluatedCommand()
		if len(freshCommands) != 7 || freshCommands[0].Command().Kind != model.CommandCopyTextFrom || !freshOK || freshEvaluated.Selector == nil || freshEvaluated.Selector.IDRegex == nil || *freshEvaluated.Selector.IDRegex != "copy-target" {
			t.Fatalf("reuse run %d result.Commands ownership escaped = %#v evaluated %#v", run, freshCommands, freshEvaluated)
		}
		requests := batch5InputRequests(driver.Actions())
		requests[0].Text = "caller-mutated"
		requests[0].AppIDs[0] = "caller-mutated"
		freshRequests := batch5InputRequests(driver.Actions())
		if freshRequests[0].Text != "copied-from-target" || freshRequests[0].AppIDs[0] != "com.example.g003.i21.clipboard" {
			t.Fatalf("reuse run %d driver request ownership escaped = %#v", run, freshRequests)
		}
	}
	if after := interactionI21ProgramSnapshot(program); !reflect.DeepEqual(after, before) {
		t.Fatal("prepared Program changed across reuse")
	}
	loader.AssertUnchanged(t)
}

func TestInteractionI21ConcurrentIsolation(t *testing.T) {
	program, loader := interactionI21Prepare(t, "late-evaluation.yaml")
	before := interactionI21ProgramSnapshot(program)
	compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	root := interactionI21FirstRoot(t, compiled)
	const workers = 12
	factories := make([]*interactionI21RecordingFactory, workers)
	clocks := make([]*interactionI21CountingClock, workers)
	for index := range factories {
		delegate, factoryErr := js.NewFactory(js.Config{Random: deterministicRandom{}, CopiedText: stringPointer(fmt.Sprintf("stale-lane-%02d", index))})
		if factoryErr != nil {
			t.Fatal(factoryErr)
		}
		factories[index] = &interactionI21RecordingFactory{delegate: delegate}
		clocks[index] = &interactionI21CountingClock{}
	}
	type observation struct {
		index        int
		rootID       string
		requests     []device.InputTextRequest
		events       []Event
		runtimeTrace []interactionI21RuntimeTrace
		runtimeCount int
		clock        *interactionI21CountingClock
		clockCalls   int
		err          error
	}
	observations := make(chan observation, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		index := index
		group.Add(1)
		go func() {
			defer group.Done()
			lane := fmt.Sprintf("lane-%02d", index)
			driver := interactionI21LateEvaluationDriver()
			generator := &batch4HSequenceGenerator{values: []string{
				lane + "-text", lane + "-number", lane + "-email", lane + "-person",
				lane + "-city", lane + "-country", lane + "-color",
			}}
			rootID := "i21-concurrent-" + lane + "/root-run-000001"
			events := make([]Event, 0, 32)
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID:         "i21-concurrent-" + lane,
				ExternalEnvironment: map[string]string{"APP_ID": "com.example.g003.i21." + lane, "CLIP": "clip-" + lane},
				Driver:              driver, Clock: clocks[index], JSFactory: factories[index], Controller: NoopController{}, InputGenerator: generator,
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootID)
			if runErr == nil && (result.Outcome() != Completed || result.RootRunID() != rootID || len(result.Commands()) != 15) {
				runErr = fmt.Errorf("unexpected result %#v", result)
			}
			observations <- observation{
				index: index, rootID: result.RootRunID(), requests: batch5InputRequests(driver.Actions()), events: cloneEvents(events),
				runtimeTrace: factories[index].Trace(), runtimeCount: factories[index].runtimeCount,
				clock: clocks[index], clockCalls: clocks[index].Calls(), err: runErr,
			}
		}()
	}
	group.Wait()
	close(observations)
	seen := make(map[string]struct{}, workers)
	seenClocks := make(map[*interactionI21CountingClock]struct{}, workers)
	for observation := range observations {
		if observation.err != nil {
			t.Fatalf("worker %d error = %v", observation.index, observation.err)
		}
		lane := fmt.Sprintf("lane-%02d", observation.index)
		wantTexts := []string{"dynamic-text", lane + "-text", lane + "-number", lane + "-email", lane + "-person", lane + "-city", lane + "-country", lane + "-color", "clip-" + lane}
		gotTexts := make([]string, len(observation.requests))
		for index, request := range observation.requests {
			gotTexts[index] = request.Text
			if !reflect.DeepEqual(request.AppIDs, []string{"com.example.g003.i21." + lane}) {
				t.Fatalf("worker %d request %d app IDs = %#v", observation.index, index, request.AppIDs)
			}
		}
		if !reflect.DeepEqual(gotTexts, wantTexts) {
			t.Fatalf("worker %d texts = %#v, want %#v", observation.index, gotTexts, wantTexts)
		}
		if len(observation.events) != 32 {
			t.Fatalf("worker %d listener events = %d, want 32", observation.index, len(observation.events))
		}
		for eventIndex, event := range observation.events {
			if event.RootRunID() != observation.rootID {
				t.Fatalf("worker %d listener event %d root = %q, want %q", observation.index, eventIndex, event.RootRunID(), observation.rootID)
			}
		}
		wantRuntimeTrace := []interactionI21RuntimeTrace{{Kind: "set", Value: "clip-" + lane}}
		if observation.runtimeCount != 1 || !reflect.DeepEqual(observation.runtimeTrace, wantRuntimeTrace) {
			t.Fatalf("worker %d runtime = count %d trace %#v, want isolated %#v", observation.index, observation.runtimeCount, observation.runtimeTrace, wantRuntimeTrace)
		}
		if observation.clock == nil || observation.clockCalls == 0 {
			t.Fatalf("worker %d clock observation = %p calls %d", observation.index, observation.clock, observation.clockCalls)
		}
		if _, duplicate := seenClocks[observation.clock]; duplicate {
			t.Fatalf("worker %d reused Clock %p", observation.index, observation.clock)
		}
		seenClocks[observation.clock] = struct{}{}
		if _, duplicate := seen[observation.rootID]; duplicate {
			t.Fatalf("duplicate root run ID %q", observation.rootID)
		}
		seen[observation.rootID] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("isolated root IDs = %d, want %d", len(seen), workers)
	}
	if len(seenClocks) != workers {
		t.Fatalf("isolated Clocks = %d, want %d", len(seenClocks), workers)
	}
	if after := interactionI21ProgramSnapshot(program); !reflect.DeepEqual(after, before) {
		t.Fatal("concurrent reuse mutated prepared Program")
	}
	loader.AssertUnchanged(t)
}

// TestPublicExecuteRunsCompleteInteractionFlow drives the public Prepare and
// Execute entry points through the production registry and FakeDriver.
func TestPublicExecuteRunsCompleteInteractionFlow(t *testing.T) {
	program, loader := interactionI21Prepare(t, "happy.yaml")
	driver := interactionI21HappyDriver()
	generator := &batch4HSequenceGenerator{values: []string{
		"rnd-text", "1234", "rnd@example.test", "Random Person", "Random City", "Random Country", "Random Color",
	}}
	events := make([]Event, 0, 44)
	results, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "batch6-public-e2e", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: generator,
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	})
	loader.AssertUnchanged(t)
	if err != nil {
		t.Fatalf("public Execute(happy.yaml) error = %T %v", err, err)
	}
	if len(results) != 1 {
		t.Fatalf("public Execute results = %d, want exactly one root result", len(results))
	}
	if got := results[0].Outcome(); got != Completed {
		t.Fatalf("public Execute outcome = %s, want %s", got, Completed)
	}
	// happy.yaml authors all 21 interaction commands; each one
	// must have produced a started and a finished event through production.
	if len(results[0].Commands()) != 21 {
		t.Fatalf("public Execute command results = %d, want the authored 21", len(results[0].Commands()))
	}
	if len(driver.Actions()) == 0 {
		t.Fatal("public Execute reached no driver action")
	}
	if len(generator.Requests()) != 7 {
		t.Fatalf("input generator requests = %d, want the seven random-input handlers", len(generator.Requests()))
	}
}

// TestPublicExecuteRegistersAllInteractionCommands ensures every interaction
// fixture reaches runtime through the production registry. Driver-level success
// is outside this registry boundary and has dedicated handler coverage.
func TestPublicExecuteRegistersAllInteractionCommands(t *testing.T) {
	commands := interactionI21PrivateCommandsFromFixtures(t)
	if len(commands) != 21 {
		t.Fatalf("exposed command samples = %d, want 21", len(commands))
	}
	for _, keyword := range interactionI21PrivateKeywords() {
		t.Run(string(keyword), func(t *testing.T) {
			command, ok := commands[keyword]
			if !ok {
				t.Fatalf("missing authored sample for %s", keyword)
			}
			path := "/workspace/i21-public-" + string(keyword) + ".yaml"
			flow := model.Flow{SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.g003.i21.public"}, Commands: []model.Command{command}}
			program, prepareErr := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{path}}, tapPreparedFlowLoader{flows: map[string]model.Flow{path: flow}})
			if prepareErr != nil {
				t.Fatalf("Prepare(%s) error = %v", keyword, prepareErr)
			}
			// Polling handlers need an advancing clock so lookup deadlines expire.
			driver := interactionI21MissingTargetDriver(64)
			generator := &interactionI21CountingGenerator{}
			artifact := &interactionI21CountingArtifactSink{}
			factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
			_, err := Execute(context.Background(), program, Dependencies{
				ExecutionID: "i21-public-admission", Driver: driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{}, InputGenerator: generator, ArtifactSink: artifact,
				FailureResolver: FailureResolverFunc(func(context.Context, CommandResult) FailureDecision { return FailureDecisionFail }),
				Listeners:       []Listener{ListenerFunc(func(context.Context, Event) error { return nil })},
			})
			// ConfigurationError here means the production registry lacks the command.
			// Device-level failures are outside this test boundary.
			if isConfigurationError(err) {
				t.Fatalf("public execution %s = ConfigurationError %v", keyword, err)
			}
			if factory.calls == 0 {
				t.Fatalf("public execution %s created no JavaScript runtime", keyword)
			}
		})
	}
}

func TestInteractionI21RegistryNegativeOwnershipAndSourceClosure(t *testing.T) {
	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	keys := sortedHandlerKeywords(production)
	source := make([]handlerSpec, 0, len(keys))
	for _, key := range keys {
		source = append(source, production.byKeyword[model.CommandKeyword(key)])
	}
	registry, err := newHandlerRegistry(source...)
	if err != nil {
		t.Fatal(err)
	}
	source[0] = handlerSpec{}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, keys) {
		t.Fatalf("source-slice mutation escaped = %#v", got)
	}
	registry.byKeyword[model.CommandAssertTrue] = handlerSpec{}
	fresh, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedHandlerKeywords(fresh); !reflect.DeepEqual(got, keys) {
		t.Fatalf("direct-map mutation escaped into production = %#v", got)
	}
	allowed := map[string]bool{
		"interaction_i21_integration_test.go": true, "interaction_i21_robustness_test.go": true,
		"interaction_i21_export_test.go": true, "interaction_i21_report_test.go": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		contents, readErr := os.ReadFile(entry.Name())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if (strings.Contains(string(contents), "interactionI21HandlerRegistry") || strings.Contains(string(contents), "ExecuteInteractionI21ReportScenariosForTest")) && !allowed[entry.Name()] {
			t.Fatalf("I21 harness symbol reachable from non-authorized source %s", entry.Name())
		}
	}
}

func TestInteractionI21CompleteProgramHarnessOrderingAndControls(t *testing.T) {
	t.Run("prepared graph is defensive and preserves whole-program closure", func(t *testing.T) {
		program, loader := interactionI21Prepare(t, "happy.yaml", "later-root-invalid.yaml", "happy.yaml")
		wantRoots := []string{
			interactionI21FixturePath(t, "happy.yaml"),
			interactionI21FixturePath(t, "later-root-invalid.yaml"),
			interactionI21FixturePath(t, "happy.yaml"),
		}
		wantNodes := []capability.GraphNode{{Path: wantRoots[0]}, {Path: wantRoots[1]}}
		graph := program.Graph()
		if !reflect.DeepEqual(graph.Roots, wantRoots) || !reflect.DeepEqual(graph.Nodes, wantNodes) || len(graph.Edges) != 0 {
			t.Fatalf("prepared Program graph = %#v, want roots %#v nodes %#v and no edges", graph, wantRoots, wantNodes)
		}
		graph.Roots[0] = "caller-program-graph-root-mutated"
		graph.Nodes[0].Path = "caller-program-graph-node-mutated"
		freshGraph := program.Graph()
		if !reflect.DeepEqual(freshGraph.Roots, wantRoots) || !reflect.DeepEqual(freshGraph.Nodes, wantNodes) || len(freshGraph.Edges) != 0 {
			t.Fatalf("prepared Program Graph ownership escaped: fresh graph %#v", freshGraph)
		}
		compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
		if compiled != nil || !isConfigurationError(err) {
			t.Fatalf("compile graph-owned duplicate-valid plus later-invalid roots = %#v, %T %v", compiled, err, err)
		}
		freshGraph = program.Graph()
		if !reflect.DeepEqual(freshGraph.Roots, wantRoots) || !reflect.DeepEqual(freshGraph.Nodes, wantNodes) || len(freshGraph.Edges) != 0 {
			t.Fatalf("prepared Program Graph changed after whole-program compile: %#v", freshGraph)
		}
		loader.AssertUnchanged(t)
	})
	t.Run("prepared roots are defensive and preserve whole-program closure", func(t *testing.T) {
		program, loader := interactionI21Prepare(t, "happy.yaml", "later-root-invalid.yaml", "happy.yaml")
		wantRoots := []string{
			interactionI21FixturePath(t, "happy.yaml"),
			interactionI21FixturePath(t, "later-root-invalid.yaml"),
			interactionI21FixturePath(t, "happy.yaml"),
		}
		roots := program.Roots()
		if !reflect.DeepEqual(roots, wantRoots) {
			t.Fatalf("prepared Program roots = %#v, want %#v", roots, wantRoots)
		}
		roots[0] = "caller-program-roots-mutated"
		if freshRoots := program.Roots(); !reflect.DeepEqual(freshRoots, wantRoots) {
			t.Fatalf("prepared Program Roots ownership escaped: fresh roots %#v, want %#v", freshRoots, wantRoots)
		}
		compiled, err := compileProgram(context.Background(), program, interactionI21HandlerRegistry(t))
		if compiled != nil || !isConfigurationError(err) {
			t.Fatalf("compile duplicate-valid plus later-invalid roots = %#v, %T %v", compiled, err, err)
		}
		if freshRoots := program.Roots(); !reflect.DeepEqual(freshRoots, wantRoots) {
			t.Fatalf("prepared Program Roots changed after whole-program compile: %#v, want %#v", freshRoots, wantRoots)
		}
		loader.AssertUnchanged(t)
	})
	t.Run("later invalid compiles once before dependencies", func(t *testing.T) {
		observation := interactionI21RunHarness(t, context.Background(), []string{"happy.yaml", "later-root-invalid.yaml"}, nil)
		if observation.compileCalls != 1 || !isConfigurationError(observation.err) || observation.dependencyCalls != 0 || len(observation.results) != 0 || len(observation.events) != 0 {
			t.Fatalf("later-invalid harness = %#v", observation)
		}
	})
	t.Run("duplicate roots ordered and isolated", func(t *testing.T) {
		factories := make([]*interactionI21RecordingFactory, 2)
		for index := range factories {
			delegate, err := js.NewFactory(js.Config{Random: deterministicRandom{}, CopiedText: stringPointer(fmt.Sprintf("stale-root-%d", index))})
			if err != nil {
				t.Fatal(err)
			}
			factories[index] = &interactionI21RecordingFactory{delegate: delegate}
		}
		observation := interactionI21RunHarness(t, context.Background(), []string{"clipboard.yaml", "clipboard.yaml"}, func(index int, _ string) Dependencies {
			return Dependencies{ExecutionID: "i21-harness-duplicate", Driver: interactionI21ClipboardDriver(), Clock: newAdvancingClock(), JSFactory: factories[index], Controller: NoopController{}}
		})
		if observation.err != nil || observation.compileCalls != 1 || observation.dependencyCalls != 2 || len(observation.results) != 2 || observation.results[0].RootRunID() != "i21-harness-duplicate/root-run-000001" || observation.results[1].RootRunID() != "i21-harness-duplicate/root-run-000002" {
			t.Fatalf("duplicate harness = %#v", observation)
		}
		for index, factory := range factories {
			trace := factory.Trace()
			if factory.runtimeCount != 1 || len(trace) < 1 || trace[0] != (interactionI21RuntimeTrace{Kind: "set", Value: "copied-from-target"}) {
				t.Fatalf("duplicate root %d fresh clipboard session = runtime %d trace %#v", index, factory.runtimeCount, trace)
			}
		}
	})
	t.Run("failed first root cuts off later root", func(t *testing.T) {
		observation := interactionI21RunHarness(t, context.Background(), []string{"failed.yaml", "actions.yaml"}, func(index int, _ string) Dependencies {
			return Dependencies{ExecutionID: "i21-harness-failfast", Driver: interactionI21MissingTargetDriver(220), Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}}
		})
		if observation.err == nil || observation.compileCalls != 1 || observation.dependencyCalls != 1 || len(observation.results) != 1 || observation.results[0].Outcome() != Failed {
			t.Fatalf("fail-fast harness = %#v", observation)
		}
	})
	t.Run("nil and pre-cancel never construct dependencies", func(t *testing.T) {
		var nilContext context.Context
		nilObservation := interactionI21RunHarness(t, nilContext, []string{"clipboard.yaml"}, nil)
		if !isConfigurationError(nilObservation.err) || nilObservation.dependencyCalls != 0 || len(nilObservation.results) != 0 {
			t.Fatalf("nil harness = %#v", nilObservation)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		cancelled := interactionI21RunHarness(t, ctx, []string{"clipboard.yaml"}, nil)
		if !errors.Is(cancelled.err, context.Canceled) || cancelled.dependencyCalls != 0 || len(cancelled.results) != 0 {
			t.Fatalf("pre-cancel harness = %#v", cancelled)
		}
	})
	t.Run("missing generator in later root preserves earlier result", func(t *testing.T) {
		observation := interactionI21RunHarness(t, context.Background(), []string{"clipboard.yaml", "text-random.yaml"}, func(index int, _ string) Dependencies {
			if index == 0 {
				return Dependencies{ExecutionID: "i21-harness-generator", Driver: interactionI21ClipboardDriver(), Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}}
			}
			return Dependencies{ExecutionID: "i21-harness-generator", Driver: batch4ADriver(), Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}}
		})
		if !isConfigurationError(observation.err) || observation.compileCalls != 1 || observation.dependencyCalls != 2 || len(observation.results) != 2 || observation.results[0].Outcome() != Completed || observation.results[1].Outcome() != Failed {
			t.Fatalf("later missing-generator harness = %#v", observation)
		}
	})
}

func TestInteractionI21HarnessTypedNilDependenciesFailClosed(t *testing.T) {
	var nilDriver *enginetest.FakeDriver
	var nilClock *batch1ATraceClock
	var nilFactory *tapCountingRuntimeFactory
	var nilGenerator *batch4HSequenceGenerator
	for _, test := range []struct {
		name    string
		fixture string
		late    bool
		deps    func(*enginetest.FakeDriver) Dependencies
	}{
		{name: "driver", fixture: "clipboard.yaml", deps: func(_ *enginetest.FakeDriver) Dependencies {
			return Dependencies{ExecutionID: "i21-typed-nil-driver", Driver: nilDriver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}}
		}},
		{name: "clock", fixture: "clipboard.yaml", deps: func(driver *enginetest.FakeDriver) Dependencies {
			return Dependencies{ExecutionID: "i21-typed-nil-clock", Driver: driver, Clock: nilClock, JSFactory: tapJSFactory(t), Controller: NoopController{}}
		}},
		{name: "javascript factory", fixture: "clipboard.yaml", deps: func(driver *enginetest.FakeDriver) Dependencies {
			return Dependencies{ExecutionID: "i21-typed-nil-js", Driver: driver, Clock: newAdvancingClock(), JSFactory: nilFactory, Controller: NoopController{}}
		}},
		{name: "input generator", fixture: "text-random.yaml", late: true, deps: func(driver *enginetest.FakeDriver) Dependencies {
			return Dependencies{ExecutionID: "i21-typed-nil-generator", Driver: driver, Clock: newAdvancingClock(), JSFactory: tapJSFactory(t), Controller: NoopController{}, InputGenerator: nilGenerator}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := interactionI21ClipboardDriver()
			observation := interactionI21RunHarness(t, context.Background(), []string{test.fixture}, func(_ int, _ string) Dependencies { return test.deps(driver) })
			if !isConfigurationError(observation.err) || observation.compileCalls != 1 || observation.dependencyCalls != 1 || len(observation.results) > 1 {
				t.Fatalf("typed-nil %s = error %T %v compile/deps/results %d/%d/%d driver %#v", test.name, observation.err, observation.err, observation.compileCalls, observation.dependencyCalls, len(observation.results), driver.Actions())
			}
			if test.late {
				if len(observation.results) != 1 || observation.results[0].Outcome() != Failed || countInteractionI21Method(driver.Actions(), enginetest.MethodInputText) != 1 {
					t.Fatalf("typed-nil late generator cutoff = results %#v driver %#v", observation.results, driver.Actions())
				}
			} else if len(driver.Actions()) != 0 {
				t.Fatalf("typed-nil %s leaked driver effects = %#v", test.name, driver.Actions())
			}
		})
	}
}

func interactionI21LateEvaluationDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	infos := make([]enginetest.Result[device.DeviceInfo], 48)
	for index := range infos {
		infos[index].Value = device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	}
	target := device.TreeNode{Attributes: map[string]string{"text": "Dynamic Target", "bounds": "[0,0][400,884]"}}
	tree := device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][400,884]"}, Children: []device.TreeNode{target}}
	descriptors := make([]enginetest.Result[device.TreeNode], 4)
	for index := range descriptors {
		descriptors[index].Value = tree
	}
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: infos, ContentDescriptor: descriptors})
	return driver
}

type interactionI21HarnessObservation struct {
	compileCalls    int
	dependencyCalls int
	results         []FlowResult
	events          []Event
	err             error
}

func interactionI21RunHarness(
	t testing.TB,
	ctx context.Context,
	names []string,
	dependencies func(int, string) Dependencies,
) interactionI21HarnessObservation {
	t.Helper()
	program, loader := interactionI21Prepare(t, names...)
	observation := interactionI21HarnessObservation{}
	observation.compileCalls++
	compiled, err := compileProgram(ctx, program, interactionI21HandlerRegistry(t))
	if err != nil {
		observation.err = err
		loader.AssertUnchanged(t)
		return observation
	}
	if compiled == nil {
		observation.err = NewConfigurationError("I21 harness compilation returned nil", nil)
		return observation
	}
	for index, path := range compiled.Roots() {
		root, ok := compiled.Flow(path)
		if !ok {
			observation.err = NewConfigurationError("I21 harness compiled root missing", nil)
			break
		}
		if dependencies == nil {
			observation.err = NewConfigurationError("I21 harness dependency factory missing", nil)
			break
		}
		observation.dependencyCalls++
		deps := dependencies(index, path)
		rootRunID := fmt.Sprintf("%s/root-run-%06d", deps.ExecutionID, index+1)
		deps.Listeners = append(deps.Listeners, ListenerFunc(func(_ context.Context, event Event) error {
			observation.events = append(observation.events, event)
			return nil
		}))
		result, runErr := executeCompiledRootForRun(ctx, deps, root, rootRunID)
		if result.Path() != "" {
			observation.results = append(observation.results, result)
		}
		if runErr != nil {
			observation.err = runErr
			break
		}
	}
	loader.AssertUnchanged(t)
	return observation
}

func interactionI21PrivateKeywords() []model.CommandKeyword {
	return []model.CommandKeyword{
		model.CommandDoubleTapOn, model.CommandLongPressOn, model.CommandBack, model.CommandHideKeyboard,
		model.CommandPasteText, model.CommandScroll, model.CommandScrollUntilVisible, model.CommandInputText,
		model.CommandInputRandomText, model.CommandInputRandomNumber, model.CommandInputRandomEmail,
		model.CommandInputRandomPersonName, model.CommandInputRandomCityName, model.CommandInputRandomCountryName,
		model.CommandInputRandomColorName, model.CommandSwipe, model.CommandPressKey, model.CommandEraseText,
		model.CommandAction, model.CommandCopyTextFrom, model.CommandSetClipboard,
	}
}

func interactionI21PrivateCommandsFromFixtures(t testing.TB) map[model.CommandKeyword]model.Command {
	t.Helper()
	commands := make(map[model.CommandKeyword]model.Command, 21)
	loader := capability.FileLoader{}
	for _, name := range interactionI21FixtureNames() {
		if name == "deferred-ai-invalid.yaml" || name == "static-invalid.yaml" || name == "later-root-invalid.yaml" {
			continue
		}
		flow, err := loader.Load(context.Background(), interactionI21FixturePath(t, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, command := range flow.Commands {
			for _, keyword := range interactionI21PrivateKeywords() {
				if command.Kind == keyword {
					if _, exists := commands[keyword]; !exists {
						commands[keyword] = cloneCommand(command)
					}
				}
			}
		}
	}
	return commands
}

type interactionI21CountingClock struct {
	mu    sync.Mutex
	calls int
}

func (clock *interactionI21CountingClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls++
	return time.Unix(1_700_000_000, 0).UTC()
}

func (clock *interactionI21CountingClock) Wait(context.Context, time.Duration) error {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.calls++
	return nil
}

func (clock *interactionI21CountingClock) Calls() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.calls
}

type interactionI21CountingGenerator struct{ calls int }

func (generator *interactionI21CountingGenerator) Generate(context.Context, InputRequest) (string, error) {
	generator.calls++
	return "forbidden", nil
}

type interactionI21CountingArtifactSink struct{ calls int }

func (sink *interactionI21CountingArtifactSink) Write(context.Context, ArtifactWriteRequest) (ArtifactWriteResult, error) {
	sink.calls++
	return ArtifactWriteResult{}, nil
}
