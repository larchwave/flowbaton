package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch2BEvaluationTargetNormalizationMatrix(t *testing.T) {
	t.Parallel()

	for _, alias := range batch2BAliases() {
		for _, failure := range batch2BErrorCases() {
			for _, seam := range []string{"active-config", "app-id-interpolation"} {
				t.Run(alias+"/"+seam+"/"+failure.name, func(t *testing.T) {
					directEvaluation := batch2BFailingEvaluation(seam, failure.err)
					actionEvaluation := batch2BFailingEvaluation(seam, failure.err)
					directEvaluated, directErr := batch2BEvaluateOnly(
						context.Background(), batch2BDirectCommand(alias), directEvaluation,
					)
					actionEvaluated, actionErr := batch2BEvaluateOnly(
						context.Background(), batch2BActionCommand(alias), actionEvaluation,
					)
					sentinel := failure.err
					if failure.name == "malformed graph" {
						sentinel = nil
					}
					batch2BAssertEquivalentErrors(t, directErr, actionErr, sentinel)
					if actionEvaluated.command.Kind != model.CommandAction || actionEvaluated.command.Arguments != alias {
						t.Fatalf("Action evaluated metadata = %#v", actionEvaluated.command)
					}
					if directEvaluated.command.Kind != model.CommandKeyword(alias) {
						t.Fatalf("direct evaluated metadata = %#v", directEvaluated.command)
					}
					if failure.name == "raw ordinary" {
						want := fmt.Sprintf("command %s failed: raw ordinary", alias)
						if actionErr == nil || actionErr.Error() != want {
							t.Fatalf("Action raw evaluation error = %T %v, want %q", actionErr, actionErr, want)
						}
					}
					if failure.name == "malformed graph" {
						want := fmt.Sprintf("command %s failed: malformed error graph", alias)
						if actionErr == nil || actionErr.Error() != want || !isConfigurationError(actionErr) {
							t.Fatalf("Action malformed evaluation error = %T %v, want ConfigurationError %q", actionErr, actionErr, want)
						}
					}
				})
			}
		}

		t.Run(alias+"/blank-app-id", func(t *testing.T) {
			directEvaluated, directErr := batch2BEvaluateOnly(
				context.Background(), batch2BDirectCommand(alias), batch2BEvaluation(" ", nil),
			)
			actionEvaluated, actionErr := batch2BEvaluateOnly(
				context.Background(), batch2BActionCommand(alias), batch2BEvaluation(" ", nil),
			)
			batch2BAssertEquivalentErrors(t, directErr, actionErr, nil)
			want := fmt.Sprintf("%s requires an active appId", alias)
			if !isConfigurationError(actionErr) || actionErr.Error() != want ||
				directEvaluated.command.Kind != model.CommandKeyword(alias) ||
				actionEvaluated.command.Kind != model.CommandAction || actionEvaluated.command.Arguments != alias {
				t.Fatalf("blank app ID direct %#v %v action %#v %v, want %q", directEvaluated.command, directErr, actionEvaluated.command, actionErr, want)
			}
		})
	}
}

func TestInteractionBatch2BExecutorTargetNormalizationMatrix(t *testing.T) {
	t.Parallel()

	for _, alias := range batch2BAliases() {
		for _, failure := range batch2BErrorCases() {
			t.Run(alias+"/"+failure.name, func(t *testing.T) {
				direct := batch2BDriverWithPhysicalError(alias, failure.err)
				action := batch2BDriverWithPhysicalError(alias, failure.err)
				_, directEvaluated, directErr := batch2BExecuteLeafWithFreshLookup(
					context.Background(), batch2BDirectCommand(alias), direct, "copied-"+alias,
				)
				_, actionEvaluated, actionErr := batch2BExecuteLeafWithFreshLookup(
					context.Background(), batch2BActionCommand(alias), action, "copied-"+alias,
				)
				sentinel := failure.err
				if failure.name == "malformed graph" {
					sentinel = nil
				}
				batch2BAssertEquivalentErrors(t, directErr, actionErr, sentinel)
				if batch2BPhysicalCount(direct.Actions()) != 1 || batch2BPhysicalCount(action.Actions()) != 1 ||
					len(settleRequests(direct.Actions())) != 0 || len(settleRequests(action.Actions())) != 0 {
					t.Fatalf("failure effects direct %#v action %#v", direct.Actions(), action.Actions())
				}
				if directEvaluated.command.Kind != model.CommandKeyword(alias) || actionEvaluated.command.Kind != model.CommandAction ||
					actionEvaluated.command.Arguments != alias {
					t.Fatalf("evaluated metadata direct %#v action %#v", directEvaluated.command, actionEvaluated.command)
				}
				if failure.name == "raw ordinary" {
					want := fmt.Sprintf("command %s failed: raw ordinary", alias)
					if actionErr == nil || actionErr.Error() != want {
						t.Fatalf("Action raw executor error = %T %v, want %q", actionErr, actionErr, want)
					}
				}
				if failure.name == "malformed graph" {
					want := fmt.Sprintf("command %s failed: malformed error graph", alias)
					if actionErr == nil || actionErr.Error() != want || !isConfigurationError(actionErr) {
						t.Fatalf("Action malformed executor error = %T %v", actionErr, actionErr)
					}
				}
			})
		}
	}
}

func TestInteractionBatch2BRepeatedAndConcurrentIsolation(t *testing.T) {
	factory := &batch5FreshRuntimeFactory{delegate: tapJSFactory(t)}

	for iteration, alias := range batch2BAliases() {
		appID := fmt.Sprintf("com.example.batch2b.repeat.%s.%02d", strings.ToLower(alias), iteration)
		copied := fmt.Sprintf("repeat-copied-%s-%02d", alias, iteration)
		root, err := batch2BCompileRoot(batch2BRootCommands(alias, copied), model.Config{AppID: appID})
		if err != nil {
			t.Fatal(err)
		}
		driver := batch2ADriver("android")
		executionID := fmt.Sprintf("batch2b-repeat-%02d-%s", iteration, strings.ToLower(alias))
		rootRunID := executionID + "/root-run-000001"
		events := make([]Event, 0, 6)
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
			JSFactory: factory, Controller: NoopController{},
			Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})},
		}, root, rootRunID)
		batch2BAssertIsolatedRoot(t, alias, copied, appID, executionID, rootRunID, result, runErr, driver.Actions(), events)
	}

	type observation struct {
		index       int
		alias       string
		copied      string
		appID       string
		executionID string
		rootRunID   string
		result      FlowResult
		actions     []enginetest.Action
		events      []Event
		err         error
	}
	compiled := make([]*compiledFlow, len(batch2BAliases()))
	for index, alias := range batch2BAliases() {
		copied := fmt.Sprintf("concurrent-copied-%s-%02d", alias, index)
		appID := fmt.Sprintf("com.example.batch2b.concurrent.%s.%02d", strings.ToLower(alias), index)
		root, err := batch2BCompileRoot(batch2BRootCommands(alias, copied), model.Config{AppID: appID})
		if err != nil {
			t.Fatal(err)
		}
		compiled[index] = root
	}
	observations := make(chan observation, len(compiled))
	var group sync.WaitGroup
	for index, root := range compiled {
		index, root := index, root
		group.Add(1)
		go func() {
			defer group.Done()
			alias := batch2BAliases()[index]
			copied := fmt.Sprintf("concurrent-copied-%s-%02d", alias, index)
			appID := fmt.Sprintf("com.example.batch2b.concurrent.%s.%02d", strings.ToLower(alias), index)
			executionID := fmt.Sprintf("batch2b-concurrent-%02d-%s", index, strings.ToLower(alias))
			rootRunID := executionID + "/root-run-000001"
			driver := batch2ADriver("android")
			events := make([]Event, 0, 6)
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootRunID)
			observations <- observation{
				index: index, alias: alias, copied: copied, appID: appID,
				executionID: executionID, rootRunID: rootRunID,
				result: result, actions: driver.Actions(), events: append([]Event(nil), events...), err: runErr,
			}
		}()
	}
	group.Wait()
	close(observations)
	seenIndexes := make(map[int]bool, len(compiled))
	seenExecutions := make(map[string]string, len(compiled))
	seenRuns := make(map[string]bool, len(compiled))
	for observation := range observations {
		batch2BAssertIsolatedRoot(
			t, observation.alias, observation.copied, observation.appID, observation.executionID,
			observation.rootRunID, observation.result, observation.err, observation.actions, observation.events,
		)
		if seenIndexes[observation.index] || seenRuns[observation.rootRunID] {
			t.Fatalf("duplicate isolation identity = %#v", observation)
		}
		if boundRoot, exists := seenExecutions[observation.executionID]; exists {
			t.Fatalf("execution ID %q is bound to roots %q and %q", observation.executionID, boundRoot, observation.rootRunID)
		}
		seenIndexes[observation.index] = true
		seenExecutions[observation.executionID] = observation.rootRunID
		seenRuns[observation.rootRunID] = true
	}
	if len(seenIndexes) != len(compiled) || len(seenExecutions) != len(compiled) || len(seenRuns) != len(compiled) {
		t.Fatalf("observed indexes %d executions %d root runs %d, want %d", len(seenIndexes), len(seenExecutions), len(seenRuns), len(compiled))
	}
	if count, unique := factory.RuntimeSnapshot(); count != 2*len(compiled) || !unique {
		t.Fatalf("runtime snapshot = count %d unique %t, want %d unique", count, unique, 2*len(compiled))
	}
}

func TestInteractionBatch2BClipboardStateMatrixAndNestedScope(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		prefix     []model.Command
		wantText   string
		wantValues []string
	}{
		{name: "initial-unset-empty", wantText: "", wantValues: nil},
		{
			name: "explicit-empty-after-stale", wantText: "",
			prefix:     []model.Command{batch5SetCommand("stale"), batch5SetCommand("")},
			wantValues: []string{"stale", ""},
		},
		{
			name: "overwrite-latest-wins", wantText: "latest",
			prefix:     []model.Command{batch5SetCommand("first"), batch5SetCommand("latest")},
			wantValues: []string{"first", "latest"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			type observation struct {
				result  FlowResult
				actions []enginetest.Action
				events  []Event
			}
			observed := make(map[string]observation, 2)
			for _, form := range []struct {
				name    string
				command model.Command
			}{
				{name: "direct", command: batch5PasteCommand()},
				{name: "action", command: batch2BActionCommand("pasteText")},
			} {
				commands := append([]model.Command(nil), test.prefix...)
				commands = append(commands, form.command)
				appID := "com.example.batch2b.clipboard." + test.name
				root, err := batch2BCompileRoot(commands, model.Config{AppID: appID})
				if err != nil {
					t.Fatal(err)
				}
				driver := batch2ADriver("android")
				events := make([]Event, 0, 2+2*len(commands))
				executionID := "batch2b-clipboard-" + test.name + "-" + form.name
				rootRunID := executionID + "/root-run-000001"
				result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
					ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
					JSFactory: tapJSFactory(t), Controller: NoopController{},
					Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
						events = append(events, event)
						return nil
					})},
				}, root, rootRunID)
				batch2BAssertClipboardProgram(
					t, result, runErr, driver.Actions(), events, commands, rootRunID, appID,
					[]string{test.wantText},
				)
				for index, want := range test.wantValues {
					evaluated, exists := result.Commands()[index].Metadata().EvaluatedCommand()
					if !exists || evaluated.Kind != model.CommandSetClipboard || evaluated.Arguments != want {
						t.Fatalf("%s %s setClipboard %d evaluated = %#v, want %q", test.name, form.name, index, evaluated, want)
					}
				}
				observed[form.name] = observation{
					result: result, actions: driver.Actions(), events: append([]Event(nil), events...),
				}
			}
			if !reflect.DeepEqual(observed["direct"].actions, observed["action"].actions) {
				t.Fatalf("%s Driver equivalence = direct %#v action %#v", test.name, observed["direct"].actions, observed["action"].actions)
			}
			if got := observed["action"].result.Commands()[len(test.prefix)].Command(); got.Kind != model.CommandAction || got.Arguments != "pasteText" {
				t.Fatalf("%s Action result = %#v", test.name, got)
			}
		})
	}

	nestedActions := make(map[string][]enginetest.Action, 2)
	for _, form := range []struct {
		name    string
		command model.Command
	}{
		{name: "direct", command: batch5PasteCommand()},
		{name: "action", command: batch2BActionCommand("pasteText")},
	} {
		t.Run("nested-scope-sharing-and-restoration/"+form.name, func(t *testing.T) {
			root := batch2BNestedClipboardRoot(t, form.command)
			driver := batch2ADriver("android")
			events := make([]Event, 0, 16)
			executionID := "batch2b-clipboard-nested-" + form.name
			rootRunID := executionID + "/root-run-000001"
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootRunID)
			wantRequests := []device.InputTextRequest{
				{Text: "root-nested", AppIDs: []string{"com.example.batch2b.clipboard.nested"}},
				{Text: "root-nested-root", AppIDs: []string{"com.example.batch2b.clipboard.nested"}},
			}
			commands := result.Commands()
			wantKeywords := []model.CommandKeyword{
				model.CommandSetClipboard, model.CommandRunFlow, model.CommandSetClipboard,
				form.command.Kind, model.CommandSetClipboard, form.command.Kind,
			}
			wantDepths := []int{0, 0, 1, 1, 0, 0}
			if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID ||
				len(commands) != len(wantKeywords) || !reflect.DeepEqual(batch5InputRequests(driver.Actions()), wantRequests) ||
				batch2BPhysicalCount(driver.Actions()) != 2 {
				t.Fatalf("nested %s = result %#v commands %#v requests %#v Driver %#v error %v", form.name, result, commands, batch5InputRequests(driver.Actions()), driver.Actions(), runErr)
			}
			for index, command := range commands {
				if command.Sequence() != uint64(index+1) || command.Depth() != wantDepths[index] ||
					command.RootRunID() != rootRunID || command.Outcome() != Completed ||
					command.Command().Kind != wantKeywords[index] {
					t.Fatalf("nested %s command %d = %#v", form.name, index, command)
				}
			}
			wantKinds := []EventKind{
				EventFlowStarted,
				EventCommandStarted, EventCommandFinished,
				EventCommandStarted, EventFlowStarted,
				EventCommandStarted, EventCommandFinished,
				EventCommandStarted, EventCommandFinished,
				EventFlowFinished, EventCommandFinished,
				EventCommandStarted, EventCommandFinished,
				EventCommandStarted, EventCommandFinished,
				EventFlowFinished,
			}
			wantEventDepths := []int{0, 0, 0, 0, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0}
			if len(events) != len(wantKinds) {
				t.Fatalf("nested %s events = %#v, want %d", form.name, events, len(wantKinds))
			}
			for index, event := range events {
				if event.Kind() != wantKinds[index] || event.Depth() != wantEventDepths[index] || event.RootRunID() != rootRunID {
					t.Fatalf("nested %s event %d = %#v, want kind %s depth %d root %q", form.name, index, event, wantKinds[index], wantEventDepths[index], rootRunID)
				}
			}
			for _, index := range []int{3, 5} {
				evaluated, exists := commands[index].Metadata().EvaluatedCommand()
				if !exists || evaluated.Kind != form.command.Kind ||
					(form.command.Kind == model.CommandAction && evaluated.Arguments != "pasteText") {
					t.Fatalf("nested %s paste result %d = %#v evaluated %#v", form.name, index, commands[index], evaluated)
				}
			}
			nestedActions[form.name] = driver.Actions()
		})
	}
	if !reflect.DeepEqual(nestedActions["direct"], nestedActions["action"]) {
		t.Fatalf("nested Driver equivalence = direct %#v action %#v", nestedActions["direct"], nestedActions["action"])
	}
}

func TestInteractionBatch2BCompiledProgramReuseOwnsFreshExecutionState(t *testing.T) {
	t.Parallel()

	specs := batch2BHandlerSpecs()
	if len(specs) == 0 || specs[len(specs)-1].keyword != model.CommandAction {
		t.Fatalf("Batch 2B private registry order = %#v", specs)
	}
	originalExecute := specs[len(specs)-1].execute
	var lookupMu sync.Mutex
	lookups := make([]*ElementLookup, 0, 4)
	specs[len(specs)-1].execute = func(
		ctx context.Context,
		state *executionState,
		evaluated evaluatedDispatch,
	) (commandEffect, error) {
		lookup, err := state.elementLookup()
		if err != nil {
			return commandEffect{}, err
		}
		lookupMu.Lock()
		lookups = append(lookups, lookup)
		lookupMu.Unlock()
		return originalExecute(ctx, state, evaluated)
	}
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatal(err)
	}
	authored := []model.Command{
		batch5SetCommand("${RUN_VALUE}"),
		batch2BActionCommand("pasteText"),
	}
	compiled, err := newDispatcher(registry).compileSequence(
		context.Background(), compileContext{containingFlow: "/workspace/batch2b-reuse.yaml"}, authored,
	)
	if err != nil {
		t.Fatal(err)
	}
	root := &compiledFlow{
		path: "/workspace/batch2b-reuse.yaml",
		config: model.Config{
			AppID: "${APP_ID}",
		},
		body: compiled,
	}
	if got := root.body[1].value.(actionAliasCompiled).target; got != model.CommandPasteText {
		t.Fatalf("compiled reusable Action target = %q", got)
	}

	factory := &batch5FreshRuntimeFactory{delegate: tapJSFactory(t)}
	seenExecutions := make(map[string]bool, 4)
	seenRoots := make(map[string]bool, 4)
	seenDrivers := make(map[uintptr]bool, 4)
	for iteration := 0; iteration < 4; iteration++ {
		copied := fmt.Sprintf("reuse-copied-%02d", iteration)
		appID := fmt.Sprintf("com.example.batch2b.reuse.%02d", iteration)
		executionID := fmt.Sprintf("batch2b-reuse-%02d", iteration)
		rootRunID := executionID + "/root-run-000001"
		driver := batch2ADriver("android")
		driverID := reflect.ValueOf(driver).Pointer()
		events := make([]Event, 0, 6)
		result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
			ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
			JSFactory: factory, Controller: NoopController{},
			ExternalEnvironment: map[string]string{
				"RUN_VALUE": copied,
				"APP_ID":    appID,
			},
			Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})},
		}, root, rootRunID)
		commands := result.Commands()
		wantKinds := []EventKind{
			EventFlowStarted,
			EventCommandStarted, EventCommandFinished,
			EventCommandStarted, EventCommandFinished,
			EventFlowFinished,
		}
		wantRequest := []device.InputTextRequest{{Text: copied, AppIDs: []string{appID}}}
		if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID ||
			len(commands) != 2 || len(events) != len(wantKinds) ||
			!reflect.DeepEqual(batch5InputRequests(driver.Actions()), wantRequest) || batch2BPhysicalCount(driver.Actions()) != 1 {
			t.Fatalf("reused execution %d = result %#v commands %#v events %#v requests %#v Driver %#v error %v", iteration, result, commands, events, batch5InputRequests(driver.Actions()), driver.Actions(), runErr)
		}
		if commands[0].Sequence() != 1 || commands[0].Depth() != 0 || commands[0].RootRunID() != rootRunID ||
			!commands[0].Command().Equivalent(authored[0]) || commands[1].Sequence() != 2 || commands[1].Depth() != 0 ||
			commands[1].RootRunID() != rootRunID || !commands[1].Command().Equivalent(authored[1]) {
			t.Fatalf("reused execution %d command ownership = %#v", iteration, commands)
		}
		setEvaluated, setExists := commands[0].Metadata().EvaluatedCommand()
		actionEvaluated, actionExists := commands[1].Metadata().EvaluatedCommand()
		if !setExists || setEvaluated.Kind != model.CommandSetClipboard || setEvaluated.Arguments != copied ||
			!actionExists || actionEvaluated.Kind != model.CommandAction || actionEvaluated.Arguments != "pasteText" {
			t.Fatalf("reused execution %d evaluated metadata = set %#v Action %#v", iteration, setEvaluated, actionEvaluated)
		}
		for index, event := range events {
			if event.Kind() != wantKinds[index] || event.RootRunID() != rootRunID || event.Depth() != 0 {
				t.Fatalf("reused execution %d event %d = %#v", iteration, index, event)
			}
		}
		if seenExecutions[executionID] || seenRoots[rootRunID] || seenDrivers[driverID] {
			t.Fatalf("reused execution identity collision = execution %q root %q Driver %x", executionID, rootRunID, driverID)
		}
		seenExecutions[executionID] = true
		seenRoots[rootRunID] = true
		seenDrivers[driverID] = true

		requestSnapshot := batch5InputRequests(driver.Actions())
		requestSnapshot[0].Text = "mutated"
		requestSnapshot[0].AppIDs[0] = "mutated"
		setEvaluated.Arguments = "mutated"
		actionEvaluated.Arguments = "mutated"
		eventCommand, eventExists := events[3].Command()
		if !eventExists {
			t.Fatalf("reused execution %d Action start has no command", iteration)
		}
		eventCommand.Arguments = "mutated"
		freshSet, _ := result.Commands()[0].Metadata().EvaluatedCommand()
		freshAction, _ := result.Commands()[1].Metadata().EvaluatedCommand()
		freshEvent, _ := events[3].Command()
		if !reflect.DeepEqual(batch5InputRequests(driver.Actions()), wantRequest) || freshSet.Arguments != copied ||
			freshAction.Arguments != "pasteText" || freshEvent.Arguments != "pasteText" {
			t.Fatalf("reused execution %d snapshot mutation escaped = requests %#v set %#v Action %#v event %#v", iteration, batch5InputRequests(driver.Actions()), freshSet, freshAction, freshEvent)
		}
	}
	if len(seenExecutions) != 4 || len(seenRoots) != 4 || len(seenDrivers) != 4 {
		t.Fatalf("reused identities = executions %d roots %d Drivers %d, want four each", len(seenExecutions), len(seenRoots), len(seenDrivers))
	}
	if count, unique := factory.RuntimeSnapshot(); count != 4 || !unique {
		t.Fatalf("reused runtimes = count %d unique %t, want four unique", count, unique)
	}
	lookupMu.Lock()
	defer lookupMu.Unlock()
	seenLookups := make(map[uintptr]bool, len(lookups))
	for _, lookup := range lookups {
		identity := reflect.ValueOf(lookup).Pointer()
		if identity == 0 || seenLookups[identity] {
			t.Fatalf("reused lookup identity collision = %#v", lookups)
		}
		seenLookups[identity] = true
	}
	if len(lookups) != 4 || len(seenLookups) != 4 {
		t.Fatalf("reused lookups = %#v, want four unique", lookups)
	}
	if got := root.body[1].value.(actionAliasCompiled).target; got != model.CommandPasteText ||
		!root.body[0].command.Equivalent(authored[0]) || !root.body[1].command.Equivalent(authored[1]) {
		t.Fatalf("compiled reusable root mutated = %#v", root)
	}
}

func TestInteractionBatch2BWholeProgramInvalidityAndClearKeychainZeroEffects(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	valid := func(path string) model.Flow {
		return model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.batch2b.valid"},
			Commands: []model.Command{batch2BActionCommand("back")},
		}
	}
	invalid := func(path, value string) model.Flow {
		return model.Flow{
			SchemaVersion: model.ASTVersionV0, Path: path, Config: model.Config{AppID: "com.example.batch2b.invalid"},
			Commands: []model.Command{batch2BActionCommand(value)},
		}
	}
	for _, test := range []struct {
		name  string
		flows []model.Flow
	}{
		{name: "invalid first", flows: []model.Flow{invalid("/workspace/invalid-first.yaml", "clearKeychain"), valid("/workspace/valid-later.yaml")}},
		{name: "invalid later", flows: []model.Flow{valid("/workspace/valid-first.yaml"), invalid("/workspace/invalid-later.yaml", "${ACTION}")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch2ADriver("android")
			factory := &tapCountingRuntimeFactory{delegate: tapJSFactory(t)}
			listenerCalls := 0
			compiled, compileErr := compileProgram(context.Background(), multiRootTapProgram(test.flows...), registry)
			if compiled != nil || !isConfigurationError(compileErr) {
				t.Fatalf("compileProgram() = %#v, %T %v; want nil ConfigurationError", compiled, compileErr, compileErr)
			}
			_ = Dependencies{
				Driver: driver, JSFactory: factory,
				Listeners: []Listener{ListenerFunc(func(context.Context, Event) error { listenerCalls++; return nil })},
			}
			if len(driver.Actions()) != 0 || factory.calls != 0 || listenerCalls != 0 {
				t.Fatalf("preflight effects = Driver %#v runtime %d listeners %d", driver.Actions(), factory.calls, listenerCalls)
			}
		})
	}

	private, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(private)
	if _, err := dispatcher.compile(context.Background(), compileContext{}, batch2BActionCommand("clearKeychain")); !isConfigurationError(err) {
		t.Fatalf("Action clearKeychain error = %T %v", err, err)
	}
	if _, err := dispatcher.compile(context.Background(), compileContext{}, batch2ABareCommand(model.CommandClearKeychain)); !isConfigurationError(err) {
		t.Fatalf("direct clearKeychain error = %T %v", err, err)
	}
	if countBatch2AMethod(batch2ADriver("android").Actions(), enginetest.MethodClearKeychain) != 0 {
		t.Fatal("clearKeychain effect unexpectedly observed")
	}
}

func TestInteractionBatch2BPostSpanFailureAndPanicOwnExactlyOneActionResult(t *testing.T) {
	t.Parallel()

	type scenario struct {
		name        string
		alias       string
		pairHealthy bool
		mutate      func(handlerSpec) handlerSpec
	}
	var typedNil *ConfigurationError
	scenarios := []scenario{
		{name: "evaluator failure", alias: "back", mutate: func(spec handlerSpec) handlerSpec {
			spec.evaluate = func(_ context.Context, _ evaluationContext, command model.Command, _ any) (evaluatedDispatch, error) {
				return evaluatedDispatch{command: cloneCommand(command), value: actionAliasEvaluated{}}, errors.New("evaluator failed")
			}
			return spec
		}},
		{name: "evaluator panic", alias: "hideKeyboard", pairHealthy: true, mutate: func(spec handlerSpec) handlerSpec {
			spec.evaluate = func(context.Context, evaluationContext, model.Command, any) (evaluatedDispatch, error) {
				panic("evaluator panic")
			}
			return spec
		}},
		{name: "executor failure", alias: "scroll", mutate: func(spec handlerSpec) handlerSpec {
			spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectDeviceMutation}, errors.New("executor failed")
			}
			return spec
		}},
		{name: "executor panic", alias: "pasteText", pairHealthy: true, mutate: func(spec handlerSpec) handlerSpec {
			spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				panic("executor panic")
			}
			return spec
		}},
		{name: "typed nil executor error", alias: "back", pairHealthy: true, mutate: func(spec handlerSpec) handlerSpec {
			spec.execute = func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectDeviceMutation}, typedNil
			}
			return spec
		}},
	}
	for _, test := range scenarios {
		t.Run(test.name, func(t *testing.T) {
			spec := test.mutate(actionHandlerSpec())
			registry, err := newHandlerRegistry(spec)
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{}, batch2BActionCommand(test.alias))
			if err != nil {
				t.Fatal(err)
			}
			slug := strings.ReplaceAll(test.name, " ", "-")
			root := &compiledFlow{
				path:   "/workspace/batch2b-phase-" + slug + ".yaml",
				config: model.Config{AppID: "com.example.batch2b.phase." + slug},
				body:   []compiledDispatch{compiled},
			}
			factory := &batch5FreshRuntimeFactory{delegate: tapJSFactory(t)}
			failedDriver := batch2ADriver("android")
			events := make([]Event, 0, 4)
			executionID := "batch2b-phase-" + slug + "-failed"
			rootRunID := executionID + "/root-run-000001"
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: executionID, Driver: failedDriver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootRunID)
			commands := result.Commands()
			if runErr == nil || result.Outcome() != Failed || len(commands) != 1 || commands[0].Outcome() != Failed ||
				result.ProductError() != runErr || result.RootRunID() != rootRunID ||
				commands[0].Command().Kind != model.CommandAction || commands[0].Command().Arguments != test.alias ||
				commands[0].Depth() != 0 || commands[0].Sequence() != 1 || commands[0].ProductError() != runErr ||
				commands[0].RootRunID() != rootRunID || len(events) != 4 {
				t.Fatalf("phase result = %#v commands %#v events %#v error %T %v", result, commands, events, runErr, runErr)
			}
			wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
			gotKinds := make([]EventKind, len(events))
			for index, event := range events {
				gotKinds[index] = event.Kind()
			}
			for index, event := range events {
				if event.RootRunID() != rootRunID || event.Depth() != 0 {
					t.Fatalf("phase %s event %d escaped root = %#v", test.name, index, event)
				}
			}
			startedCommand, startedExists := events[1].Command()
			finishedCommand, finishedExists := events[2].Command()
			if !reflect.DeepEqual(gotKinds, wantKinds) || !startedExists || !finishedExists ||
				startedCommand.Kind != model.CommandAction || startedCommand.Arguments != test.alias ||
				finishedCommand.Kind != model.CommandAction || finishedCommand.Arguments != test.alias ||
				events[1].Sequence() != 1 || events[2].Sequence() != 1 ||
				events[2].Outcome() != Failed || events[2].ProductError() != runErr ||
				events[3].Outcome() != Failed || events[3].ProductError() != runErr {
				t.Fatalf("phase lifecycle = %#v", events)
			}

			if !test.pairHealthy {
				return
			}
			healthyAlias := "back"
			if test.alias == healthyAlias {
				healthyAlias = "scroll"
			}
			healthyRoot, err := batch2BCompileRoot(
				[]model.Command{batch2BActionCommand(healthyAlias)},
				model.Config{AppID: "com.example.batch2b.phase.healthy." + slug},
			)
			if err != nil {
				t.Fatal(err)
			}
			healthyDriver := batch2ADriver("android")
			healthyEvents := make([]Event, 0, 4)
			healthyExecutionID := "batch2b-phase-" + slug + "-healthy"
			healthyRootRunID := healthyExecutionID + "/root-run-000001"
			healthyResult, healthyErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: healthyExecutionID, Driver: healthyDriver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					healthyEvents = append(healthyEvents, event)
					return nil
				})},
			}, healthyRoot, healthyRootRunID)
			batch2BAssertIsolatedRoot(
				t, healthyAlias, "", "com.example.batch2b.phase.healthy."+slug,
				healthyExecutionID, healthyRootRunID, healthyResult, healthyErr,
				healthyDriver.Actions(), healthyEvents,
			)
			if reflect.ValueOf(failedDriver).Pointer() == reflect.ValueOf(healthyDriver).Pointer() ||
				executionID == healthyExecutionID || rootRunID == healthyRootRunID {
				t.Fatalf("paired root identity collision = failed %q/%q/%p healthy %q/%q/%p", executionID, rootRunID, failedDriver, healthyExecutionID, healthyRootRunID, healthyDriver)
			}
			for _, event := range healthyEvents {
				if event.RootRunID() != healthyRootRunID {
					t.Fatalf("paired healthy event escaped root = %#v", event)
				}
				if authored, exists := event.Command(); exists && authored.Kind == model.CommandAction && authored.Arguments != healthyAlias {
					t.Fatalf("paired healthy root received foreign Action = %#v", event)
				}
			}
			if count, unique := factory.RuntimeSnapshot(); count != 2 || !unique {
				t.Fatalf("paired runtimes = count %d unique %t, want two unique", count, unique)
			}
			freshFailed := result.Commands()
			if len(freshFailed) != 1 || freshFailed[0].ProductError() != runErr ||
				freshFailed[0].Command().Kind != model.CommandAction || freshFailed[0].Command().Arguments != test.alias {
				t.Fatalf("paired healthy root corrupted failed root = %#v", freshFailed)
			}
		})
	}
}

func TestInteractionBatch2BClipboardPoisonCutsOffActionAndLaterRootIsClean(t *testing.T) {
	t.Parallel()

	var observedMessages []string
	for _, form := range []struct {
		name    string
		command model.Command
	}{
		{name: "direct", command: batch5PasteCommand()},
		{name: "action", command: batch2BActionCommand("pasteText")},
	} {
		t.Run(form.name, func(t *testing.T) {
			rawSetter := errors.New("runtime setter poisoned")
			// The command performs the first copiedText write.
			baseRuntime := &sessionRuntime{}
			baseRuntime.SetCopiedError(rawSetter)
			factory := &sessionRuntimeFactory{runtime: &batch5LiteralSessionRuntime{sessionRuntime: baseRuntime}}
			root, err := batch2BCompileRoot(
				[]model.Command{batch5SetCommand("poison value"), form.command},
				model.Config{AppID: "com.example.batch2b.poison." + form.name},
			)
			if err != nil {
				t.Fatal(err)
			}
			driver := batch2ADriver("android")
			events := make([]Event, 0, 4)
			executionID := "batch2b-poison-" + form.name
			rootRunID := executionID + "/root-run-000001"
			result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: executionID, Driver: driver, Clock: newAdvancingClock(),
				JSFactory: factory, Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					events = append(events, event)
					return nil
				})},
			}, root, rootRunID)
			commands := result.Commands()
			wantKinds := []EventKind{EventFlowStarted, EventCommandStarted, EventCommandFinished, EventFlowFinished}
			if runErr == nil || result.Outcome() != Failed || result.ProductError() != runErr ||
				result.RootRunID() != rootRunID || len(commands) != 1 || commands[0].Sequence() != 1 ||
				commands[0].Depth() != 0 || commands[0].RootRunID() != rootRunID ||
				commands[0].Command().Kind != model.CommandSetClipboard || commands[0].Outcome() != Failed ||
				commands[0].ProductError() != runErr || len(events) != len(wantKinds) ||
				len(batch5InputRequests(driver.Actions())) != 0 || batch2BPhysicalCount(driver.Actions()) != 0 {
				t.Fatalf("poison %s = result %#v commands %#v events %#v requests %#v Driver %#v error %T %v", form.name, result, commands, events, batch5InputRequests(driver.Actions()), driver.Actions(), runErr, runErr)
			}
			for index, event := range events {
				if event.Kind() != wantKinds[index] || event.RootRunID() != rootRunID || event.Depth() != 0 {
					t.Fatalf("poison %s event %d = %#v", form.name, index, event)
				}
				if authored, exists := event.Command(); exists && authored.Kind == form.command.Kind {
					t.Fatalf("poison %s emitted later paste event = %#v", form.name, event)
				}
			}
			assertBatch5CoreIntegrityPrimary(t, runErr, rawSetter)
			observedMessages = append(observedMessages, runErr.Error())

			later, err := batch2BCompileRoot(
				[]model.Command{form.command},
				model.Config{AppID: "com.example.batch2b.later." + form.name},
			)
			if err != nil {
				t.Fatal(err)
			}
			laterDriver := batch2ADriver("android")
			laterEvents := make([]Event, 0, 4)
			laterExecutionID := "batch2b-poison-later-" + form.name
			laterRootRunID := laterExecutionID + "/root-run-000001"
			laterResult, laterErr := executeCompiledRootForRun(context.Background(), Dependencies{
				ExecutionID: laterExecutionID, Driver: laterDriver, Clock: newAdvancingClock(),
				JSFactory: tapJSFactory(t), Controller: NoopController{},
				Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
					laterEvents = append(laterEvents, event)
					return nil
				})},
			}, later, laterRootRunID)
			want := []device.InputTextRequest{{Text: "", AppIDs: []string{"com.example.batch2b.later." + form.name}}}
			laterCommands := laterResult.Commands()
			if laterErr != nil || laterResult.Outcome() != Completed || laterResult.RootRunID() != laterRootRunID ||
				len(laterCommands) != 1 || laterCommands[0].Command().Kind != form.command.Kind ||
				laterCommands[0].RootRunID() != laterRootRunID || laterCommands[0].Outcome() != Completed ||
				!reflect.DeepEqual(batch5InputRequests(laterDriver.Actions()), want) ||
				len(laterEvents) != 4 || batch2BPhysicalCount(laterDriver.Actions()) != 1 {
				t.Fatalf("later %s root = %#v commands %#v events %#v requests %#v error %v", form.name, laterResult, laterCommands, laterEvents, batch5InputRequests(laterDriver.Actions()), laterErr)
			}
			for _, event := range laterEvents {
				if event.RootRunID() != laterRootRunID {
					t.Fatalf("later %s event escaped root = %#v", form.name, event)
				}
			}
		})
	}
	if len(observedMessages) != 2 || observedMessages[0] != sessionIntegrityErrorMessage ||
		observedMessages[1] != sessionIntegrityErrorMessage {
		t.Fatalf("direct/Action poison messages = %#v", observedMessages)
	}
}

func batch2BEvaluateOnly(
	ctx context.Context,
	command model.Command,
	evaluation evaluationContext,
) (evaluatedDispatch, error) {
	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		return evaluatedDispatch{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return evaluatedDispatch{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, evaluation, compiled)
	return evaluated, normalizeTerminalError(fmt.Sprintf("command %s failed", command.Kind), err)
}

func batch2BFailingEvaluation(seam string, failure error) evaluationContext {
	evaluation := batch2BEvaluation("com.example.batch2b", nil)
	switch seam {
	case "active-config":
		evaluation.hasActiveConfig = false
		evaluation.activeConfig = model.Config{}
		evaluation.activeConfigFn = func() (model.Config, error) { return model.Config{}, failure }
	case "app-id-interpolation":
		evaluation.interpolateFn = func(context.Context, string, map[string]any) (string, error) { return "", failure }
	}
	return evaluation
}

type batch2BErrorCase struct {
	name string
	err  error
}

func batch2BErrorCases() []batch2BErrorCase {
	return []batch2BErrorCase{
		{name: "raw ordinary", err: errors.New("raw ordinary")},
		{name: "malformed graph", err: &batch2BCyclicError{}},
		{name: "cancellation", err: context.Canceled},
		{name: "configuration", err: NewConfigurationError("configuration failure", nil)},
		{name: "device connection", err: NewDeviceConnectionError("device disconnected", nil)},
	}
}

type batch2BCyclicError struct{}

func (*batch2BCyclicError) Error() string { return "cyclic Batch 2B error" }

func (err *batch2BCyclicError) Unwrap() error { return err }

func batch2BDriverWithPhysicalError(alias string, failure error) *enginetest.FakeDriver {
	driver := batch2ADriver("android")
	if alias == "pasteText" {
		driver.Enqueue(enginetest.DriverScript{InputText: []enginetest.Result[struct{}]{{Err: failure}}})
	} else {
		driver.Enqueue(batch2AScriptFor(model.CommandKeyword(alias), failure))
	}
	return driver
}

func batch2BCompileRoot(commands []model.Command, config model.Config) (*compiledFlow, error) {
	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		return nil, err
	}
	compiled, err := newDispatcher(registry).compileSequence(
		context.Background(), compileContext{containingFlow: "/workspace/batch2b.yaml"}, commands,
	)
	if err != nil {
		return nil, err
	}
	return &compiledFlow{path: "/workspace/batch2b.yaml", config: cloneConfig(config), body: compiled}, nil
}

func batch2BRootCommands(alias, copied string) []model.Command {
	if alias == "pasteText" {
		return []model.Command{batch5SetCommand(copied), batch2BActionCommand(alias)}
	}
	return []model.Command{batch2BActionCommand(alias)}
}

func batch2BAssertIsolatedRoot(
	t testing.TB,
	alias, copied, appID, executionID, rootRunID string,
	result FlowResult,
	runErr error,
	actions []enginetest.Action,
	events []Event,
) {
	t.Helper()
	commands := result.Commands()
	wantCommands := []model.Command{batch2BActionCommand(alias)}
	actionIndex := 0
	if alias == "pasteText" {
		wantCommands = []model.Command{batch5SetCommand(copied), batch2BActionCommand(alias)}
		actionIndex = 1
	}
	if rootRunID != executionID+"/root-run-000001" {
		t.Fatalf("isolated %s root %q is not derived from execution %q", alias, rootRunID, executionID)
	}
	if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID || len(commands) != len(wantCommands) {
		t.Fatalf("isolated %s result = %#v commands %#v error %v", alias, result, commands, runErr)
	}
	for index, command := range commands {
		if command.RootRunID() != rootRunID || command.Depth() != 0 || command.Outcome() != Completed ||
			!command.Command().Equivalent(wantCommands[index]) {
			t.Fatalf("isolated %s command %d = %#v, want root %q command %#v", alias, index, command, rootRunID, wantCommands[index])
		}
	}
	action := commands[actionIndex]
	evaluated, ok := action.Metadata().EvaluatedCommand()
	if action.Command().Kind != model.CommandAction || action.Depth() != 0 || action.RootRunID() != rootRunID ||
		action.Outcome() != Completed || !ok || evaluated.Kind != model.CommandAction || evaluated.Arguments != alias {
		t.Fatalf("isolated %s Action result = %#v evaluated %#v", alias, action, evaluated)
	}
	if batch2BPhysicalCount(actions) != 1 {
		t.Fatalf("isolated %s physical actions = %#v", alias, actions)
	}
	wantEventCount := 2 + 2*len(wantCommands)
	if len(events) != wantEventCount || events[0].Kind() != EventFlowStarted ||
		events[len(events)-1].Kind() != EventFlowFinished || events[len(events)-1].Outcome() != Completed {
		t.Fatalf("isolated %s lifecycle = %#v, want %d events", alias, events, wantEventCount)
	}
	actionEventPairs := 0
	for index, event := range events {
		if event.RootRunID() != rootRunID {
			t.Fatalf("isolated %s event %d escaped root %q: %#v", alias, index, rootRunID, event)
		}
	}
	for index, wantCommand := range wantCommands {
		command := commands[index]
		started := events[1+2*index]
		finished := events[2+2*index]
		startedCommand, startedExists := started.Command()
		finishedCommand, finishedExists := finished.Command()
		if started.Kind() != EventCommandStarted || finished.Kind() != EventCommandFinished ||
			!startedExists || !finishedExists || !startedCommand.Equivalent(wantCommand) || !finishedCommand.Equivalent(wantCommand) ||
			started.Sequence() != command.Sequence() || finished.Sequence() != command.Sequence() ||
			started.Depth() != 0 || finished.Depth() != 0 || finished.Outcome() != Completed ||
			finished.ProductError() != nil || !reflect.DeepEqual(finished.Metadata(), command.Metadata()) {
			t.Fatalf("isolated %s command/event %d mismatch = command %#v started %#v finished %#v", alias, index, command, started, finished)
		}
		if wantCommand.Kind == model.CommandAction {
			actionEventPairs++
			if startedCommand.Arguments != alias || finishedCommand.Arguments != alias {
				t.Fatalf("isolated %s foreign Action alias in events = started %#v finished %#v", alias, startedCommand, finishedCommand)
			}
		} else if wantCommand.Kind != model.CommandSetClipboard || wantCommand.Arguments != copied {
			t.Fatalf("isolated %s unexpected preceding command = %#v", alias, wantCommand)
		}
	}
	if actionEventPairs != 1 {
		t.Fatalf("isolated %s Action event pairs = %d, want one", alias, actionEventPairs)
	}
	if alias == "pasteText" {
		want := []device.InputTextRequest{{Text: copied, AppIDs: []string{appID}}}
		if got := batch5InputRequests(actions); !reflect.DeepEqual(got, want) {
			t.Fatalf("isolated paste requests = %#v, want %#v", got, want)
		}
	}
}

func batch2BAssertClipboardProgram(
	t testing.TB,
	result FlowResult,
	runErr error,
	actions []enginetest.Action,
	events []Event,
	authored []model.Command,
	rootRunID string,
	appID string,
	wantTexts []string,
) {
	t.Helper()
	commands := result.Commands()
	if runErr != nil || result.Outcome() != Completed || result.RootRunID() != rootRunID || len(commands) != len(authored) {
		t.Fatalf("clipboard result = %#v commands %#v error %v", result, commands, runErr)
	}
	wantKinds := make([]EventKind, 0, 2+2*len(authored))
	wantKinds = append(wantKinds, EventFlowStarted)
	for index, want := range authored {
		command := commands[index]
		if command.Sequence() != uint64(index+1) || command.Depth() != 0 || command.RootRunID() != rootRunID ||
			command.Outcome() != Completed || command.ProductError() != nil || !command.Command().Equivalent(want) {
			t.Fatalf("clipboard command %d = %#v, want %#v", index, command, want)
		}
		wantKinds = append(wantKinds, EventCommandStarted, EventCommandFinished)
	}
	wantKinds = append(wantKinds, EventFlowFinished)
	if len(events) != len(wantKinds) {
		t.Fatalf("clipboard events = %#v, want %d", events, len(wantKinds))
	}
	for index, event := range events {
		if event.Kind() != wantKinds[index] || event.RootRunID() != rootRunID || event.Depth() != 0 {
			t.Fatalf("clipboard event %d = %#v, want kind %s root %q depth zero", index, event, wantKinds[index], rootRunID)
		}
	}
	wantRequests := make([]device.InputTextRequest, len(wantTexts))
	for index, text := range wantTexts {
		wantRequests[index] = device.InputTextRequest{Text: text, AppIDs: []string{appID}}
	}
	if got := batch5InputRequests(actions); !reflect.DeepEqual(got, wantRequests) ||
		batch2BPhysicalCount(actions) != len(wantRequests) {
		t.Fatalf("clipboard Driver = requests %#v physical %d actions %#v, want requests %#v", got, batch2BPhysicalCount(actions), actions, wantRequests)
	}
}

func batch2BNestedClipboardRoot(t testing.TB, paste model.Command) *compiledFlow {
	t.Helper()
	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compileOne := func(command model.Command) compiledDispatch {
		compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, command)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		return compiled
	}
	child := &compiledFlow{
		path:   "/workspace/batch2b-clipboard-child.yaml",
		config: model.Config{AppID: "com.example.batch2b.clipboard.nested", Env: map[string]string{"SCOPE": "nested"}},
		body: []compiledDispatch{
			compileOne(batch5SetCommand("${flowbaton.copiedText}-${SCOPE}")),
			compileOne(paste),
		},
	}
	runChild := compiledDispatch{
		command: model.Command{Kind: model.CommandRunFlow, Form: model.CommandFormObject, Arguments: "batch2b-clipboard-child.yaml"},
		value:   child,
		spec: handlerSpec{
			keyword: model.CommandRunFlow, effectClass: EffectComposite, evaluate: identityEvaluator,
			execute: func(ctx context.Context, state *executionState, _ evaluatedDispatch) (commandEffect, error) {
				_, executeErr := state.executeFlow(ctx, child, nil)
				return commandEffect{effectClass: EffectComposite}, executeErr
			},
		},
	}
	return &compiledFlow{
		path:   "/workspace/batch2b-clipboard-root.yaml",
		config: model.Config{AppID: "com.example.batch2b.clipboard.nested", Env: map[string]string{"SCOPE": "root"}},
		body: []compiledDispatch{
			compileOne(batch5SetCommand("root")),
			runChild,
			compileOne(batch5SetCommand("${flowbaton.copiedText}-${SCOPE}")),
			compileOne(paste),
		},
	}
}
