package engine

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestCompileRepeatRequiresObjectCommandsAndRetainsCounterShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		arguments      any
		form           model.CommandForm
		children       []model.Command
		wantError      bool
		wantKind       repeatCounterKind
		wantFixed      int
		wantExpression string
	}{
		{
			name: "fixed integer",
			form: model.CommandFormObject,
			arguments: map[string]any{
				"times":    int64(2),
				"commands": []any{map[string]any{"action": "step"}},
			},
			children:  []model.Command{repeatActionCommand("step")},
			wantKind:  repeatCounterFixed,
			wantFixed: 2,
		},
		{
			name: "late string",
			form: model.CommandFormObject,
			arguments: map[string]any{
				"times":    "${COUNT}",
				"commands": []any{map[string]any{"action": "step"}},
			},
			children:       []model.Command{repeatActionCommand("step")},
			wantKind:       repeatCounterDynamic,
			wantExpression: "${COUNT}",
		},
		{
			name: "missing means unbounded ceiling",
			form: model.CommandFormObject,
			arguments: map[string]any{
				"commands": []any{map[string]any{"action": "step"}},
			},
			children: []model.Command{repeatActionCommand("step")},
			wantKind: repeatCounterMissing,
		},
		{
			name:      "scalar form",
			form:      model.CommandFormScalar,
			wantError: true,
		},
		{
			name:      "missing commands",
			form:      model.CommandFormObject,
			arguments: map[string]any{"times": int64(1)},
			wantError: true,
		},
		{
			name:      "commands not array",
			form:      model.CommandFormObject,
			arguments: map[string]any{"commands": "step"},
			wantError: true,
		},
		{
			name: "typed commands disagree",
			form: model.CommandFormObject,
			arguments: map[string]any{
				"commands": []any{map[string]any{"action": "step"}},
			},
			wantError: true,
		},
	}

	registry, err := newHandlerRegistry(repeatHandlerSpec(), repeatActionHandlerSpec(nil))
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	dispatcher := newDispatcher(registry)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled, compileErr := dispatcher.compile(context.Background(), compileContext{}, model.Command{
				Kind: model.CommandRepeat, Form: test.form, Arguments: test.arguments, Children: test.children,
			})
			if test.wantError {
				if compileErr == nil {
					t.Fatalf("compile() = %#v, nil; want error", compiled)
				}
				return
			}
			if compileErr != nil {
				t.Fatalf("compile() error = %v", compileErr)
			}
			payload, ok := compiled.value.(repeatCompiled)
			if !ok || payload.counterKind != test.wantKind || payload.fixedTimes != test.wantFixed ||
				payload.timesExpression != test.wantExpression {
				t.Fatalf("compiled payload = %#v", compiled.value)
			}
			if len(compiled.children) != len(test.children) {
				t.Fatalf("compiled children = %d, want %d", len(compiled.children), len(test.children))
			}
		})
	}
}

func TestCompileRepeatRejectsMalformedOrForgedBoundaries(t *testing.T) {
	t.Parallel()

	script := "${READY}"
	valid := func() model.Command {
		return model.Command{
			Kind: model.CommandRepeat, Form: model.CommandFormObject,
			Arguments: map[string]any{
				"times":    int64(1),
				"commands": []any{map[string]any{"action": "step"}},
			},
			Children: []model.Command{repeatActionCommand("step")},
		}
	}
	tests := []struct {
		name   string
		mutate func(*model.Command)
	}{
		{name: "unknown field", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["future"] = true
		}},
		{name: "negative integer", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["times"] = int64(-1)
		}},
		{name: "integer overflow", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["times"] = int64(math.MaxInt32) + 1
		}},
		{name: "non normalized integer", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["times"] = float64(1)
		}},
		{name: "foreign link", mutate: func(command *model.Command) {
			command.Links = []model.FileLink{{Kind: model.FileLinkFlow, Path: "child.yaml"}}
		}},
		{name: "raw while without typed condition", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["while"] = map[string]any{"true": script}
		}},
		{name: "typed condition without raw while", mutate: func(command *model.Command) {
			command.Condition = &model.Condition{ScriptCondition: &script}
		}},
		{name: "malformed while", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["while"] = script
			command.Condition = &model.Condition{ScriptCondition: &script}
		}},
		{name: "raw and typed child counts disagree", mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["commands"] = []any{}
		}},
	}
	registry, err := newHandlerRegistry(repeatHandlerSpec(), repeatActionHandlerSpec(nil))
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := valid()
			test.mutate(&command)
			if _, err := newDispatcher(registry).compile(context.Background(), compileContext{}, command); err == nil {
				t.Fatal("compile() error = nil")
			} else {
				var configuration *ConfigurationError
				if !errors.As(err, &configuration) {
					t.Fatalf("compile() error = %T %v, want *ConfigurationError", err, err)
				}
			}
		})
	}

	maximum := valid()
	maximum.Arguments.(map[string]any)["times"] = int64(math.MaxInt32)
	if _, err := newDispatcher(registry).compile(context.Background(), compileContext{}, maximum); err != nil {
		t.Fatalf("compile(MaxInt32) error = %v", err)
	}
}

func TestEvaluateRepeatParsesDynamicTimesStrictlyAndOwnsSnapshots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value     string
		want      int
		wantError bool
	}{
		{value: "0", want: 0},
		{value: "0002", want: 2},
		{value: "2147483647", want: math.MaxInt32},
		{value: "", wantError: true},
		{value: "-1", wantError: true},
		{value: "+1", wantError: true},
		{value: " 1", wantError: true},
		{value: "1 ", wantError: true},
		{value: "1.0", wantError: true},
		{value: "1e2", wantError: true},
		{value: "2147483648", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			command := repeatCommand("${COUNT}", nil, "step")
			evaluation := evaluationContext{interpolateFn: func(context.Context, string, map[string]any) (string, error) {
				return test.value, nil
			}}
			evaluated, err := evaluateRepeat(context.Background(), evaluation, command, repeatCompiled{
				counterKind: repeatCounterDynamic, timesExpression: "${COUNT}",
			})
			if test.wantError {
				if err == nil {
					t.Fatalf("evaluateRepeat(%q) error = nil", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluateRepeat(%q) error = %v", test.value, err)
			}
			payload, ok := evaluated.value.(repeatEvaluated)
			if !ok || payload.times != test.want {
				t.Fatalf("evaluated payload = %#v, want %d", evaluated.value, test.want)
			}
			if got := evaluated.command.Arguments.(map[string]any)["times"]; got != test.value {
				t.Fatalf("evaluated command times = %#v, want %q", got, test.value)
			}
			if got := command.Arguments.(map[string]any)["times"]; got != "${COUNT}" {
				t.Fatalf("source command mutated to %#v", got)
			}
		})
	}
}

func TestRepeatDynamicTimesFailuresFinalizeExplicitZeroWithoutEffects(t *testing.T) {
	t.Parallel()

	interpolationFailure := NewConfigurationError("interpolation failed", nil)
	tests := []struct {
		name      string
		value     string
		failure   error
		wantExact error
	}{
		{name: "blank", value: ""},
		{name: "nondecimal", value: "1.5"},
		{name: "overflow", value: "2147483648"},
		{name: "interpolation failure", failure: interpolationFailure, wantExact: interpolationFailure},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			clock := newRepeatClock()
			events := make([]Event, 0, 2)
			actionCalls := 0
			dependencies := coreDependencies(enginetest.NewFakeDriver(), clock)
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})}
			core, err := newExecutorCore(
				dependencies,
				repeatHandlerSpec(),
				repeatActionHandlerSpec(func(string, *executionState) error {
					actionCalls++
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			runtime := &repeatInterpolationRuntime{
				Runtime: conditionRuntime(t, true),
				value:   test.value,
				err:     test.failure,
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }

			result, executeErr := core.execute(
				context.Background(), repeatCommand("${COUNT}", nil, "step"), 0,
			)
			if test.wantExact != nil {
				if executeErr != test.wantExact {
					t.Fatalf("execute() error = %T %v, want exact %T %v", executeErr, executeErr, test.wantExact, test.wantExact)
				}
			} else {
				var configuration *ConfigurationError
				if !errors.As(executeErr, &configuration) {
					t.Fatalf("execute() error = %T %v, want *ConfigurationError", executeErr, executeErr)
				}
			}
			metadata := result.Metadata()
			if executeErr == nil || result.ProductError() != executeErr || result.Outcome() != Failed ||
				!metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != 0 {
				t.Fatalf("repeat failure = result %#v metadata %#v error %T %v", result, metadata, executeErr, executeErr)
			}
			evaluated, exists := metadata.EvaluatedCommand()
			if !exists || evaluated.Arguments.(map[string]any)["times"] != "${COUNT}" {
				t.Fatalf("evaluated command = %#v, present %t", evaluated, exists)
			}
			if actionCalls != 0 || runtime.evaluateCalls != 0 || runtime.interpolateCalls != 1 || len(clock.Waits()) != 0 {
				t.Fatalf("side effects = actions %d condition evaluations %d interpolations %d waits %#v",
					actionCalls, runtime.evaluateCalls, runtime.interpolateCalls, clock.Waits())
			}
			if want := []EventKind{EventCommandStarted, EventCommandFinished}; !reflect.DeepEqual(repeatEventKinds(events), want) {
				t.Fatalf("event kinds = %v, want %v", repeatEventKinds(events), want)
			}
			finished := events[len(events)-1]
			if finished.ProductError() != executeErr || !finished.Metadata().HasNumberOfRuns() ||
				finished.Metadata().NumberOfRuns() != 0 {
				t.Fatalf("finished event = %#v", finished)
			}
		})
	}
}

func TestRepeatPublishesIterationMetadataAndExactChildResets(t *testing.T) {
	t.Parallel()

	clock := newRepeatClock()
	events := make([]Event, 0)
	actions := make([]string, 0)
	dependencies := coreDependencies(enginetest.NewFakeDriver(), clock)
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})}
	core, err := newExecutorCoreForRootRun(
		dependencies, "root-repeat", repeatHandlerSpec(),
		repeatActionHandlerSpec(func(name string, _ *executionState) error {
			actions = append(actions, name)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
	}
	result, err := core.execute(context.Background(), repeatCommand(int64(3), nil, "first", "second"), 2)
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if result.Outcome() != Completed || result.Depth() != 2 || !result.Metadata().HasNumberOfRuns() ||
		result.Metadata().NumberOfRuns() != 3 {
		t.Fatalf("repeat result = outcome %q depth %d metadata %#v", result.Outcome(), result.Depth(), result.Metadata())
	}
	if want := []string{"first", "second", "first", "second", "first", "second"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %#v, want %#v", actions, want)
	}
	if want := []time.Duration{RepeatDelay, RepeatDelay}; !reflect.DeepEqual(clock.Waits(), want) {
		t.Fatalf("waits = %#v, want %#v", clock.Waits(), want)
	}
	wantEventKinds := []EventKind{
		EventCommandStarted,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventCommandMetadataUpdated,
		EventCommandReset, EventCommandReset,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventCommandMetadataUpdated,
		EventCommandReset, EventCommandReset,
		EventCommandStarted, EventCommandFinished,
		EventCommandStarted, EventCommandFinished,
		EventCommandMetadataUpdated,
		EventCommandFinished,
	}
	if got := repeatEventKinds(events); !reflect.DeepEqual(got, wantEventKinds) {
		t.Fatalf("event order = %v, want %v", got, wantEventKinds)
	}

	updates := make([]Event, 0, 3)
	resets := make([]Event, 0, 4)
	for _, event := range events {
		switch event.Kind() {
		case EventCommandMetadataUpdated:
			updates = append(updates, event)
		case EventCommandReset:
			resets = append(resets, event)
		}
	}
	if len(updates) != 3 || len(resets) != 4 {
		t.Fatalf("metadata/reset events = %d/%d, want 3/4; kinds=%v", len(updates), len(resets), repeatEventKinds(events))
	}
	for index, update := range updates {
		metadata := update.Metadata()
		if update.RootRunID() != result.RootRunID() || update.Sequence() != result.Sequence() || update.Depth() != result.Depth() ||
			!metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != index+1 {
			t.Fatalf("metadata update %d = %#v", index, update)
		}
		evaluated, exists := metadata.EvaluatedCommand()
		if !exists || evaluated.Arguments.(map[string]any)["times"] != int64(3) {
			t.Fatalf("metadata update evaluated command = %#v, present %t", evaluated, exists)
		}
	}
	wantResetSequences := []uint64{2, 3, 4, 5}
	for index, reset := range resets {
		if reset.RootRunID() != result.RootRunID() || reset.Depth() != result.Depth()+1 ||
			reset.Sequence() != wantResetSequences[index] {
			t.Fatalf("reset %d identity = root %q sequence %d depth %d", index, reset.RootRunID(), reset.Sequence(), reset.Depth())
		}
	}
	if got := core.timeline.Checkpoint(); got != 7 {
		t.Fatalf("timeline checkpoint = %d, want seven command sequences", got)
	}
}

func TestRepeatWhileReevaluatesOriginalConditionAndCombinesWithTimes(t *testing.T) {
	t.Parallel()

	t.Run("while only", func(t *testing.T) {
		runtime := conditionRuntime(t, true)
		clock := newRepeatClock()
		runs := 0
		core, err := newExecutorCore(
			coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(),
			repeatActionHandlerSpec(func(string, *executionState) error {
				runs++
				if runs == 3 {
					return runtime.PutEnv("READY", false)
				}
				return nil
			}),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
		result, err := core.execute(context.Background(), repeatCommand(nil, repeatScriptCondition("${READY}"), "step"), 0)
		if err != nil || result.Outcome() != Completed || result.Metadata().NumberOfRuns() != 3 || runs != 3 {
			t.Fatalf("while repeat = result %#v error %v runs %d", result, err, runs)
		}
		if len(clock.Waits()) != 2 {
			t.Fatalf("while repeat waits = %#v, want two", clock.Waits())
		}
		evaluated, exists := result.Metadata().EvaluatedCommand()
		if !exists || evaluated.Condition == nil || evaluated.Condition.ScriptCondition == nil ||
			*evaluated.Condition.ScriptCondition != "false" {
			t.Fatalf("final evaluated condition = %#v, present %t", evaluated.Condition, exists)
		}
	})

	t.Run("times wins combined limit", func(t *testing.T) {
		runtime := conditionRuntime(t, true)
		clock := newRepeatClock()
		runs := 0
		core, err := newExecutorCore(
			coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(),
			repeatActionHandlerSpec(func(string, *executionState) error { runs++; return nil }),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
		result, err := core.execute(context.Background(), repeatCommand(int64(2), repeatScriptCondition("${READY}"), "step"), 0)
		if err != nil || result.Metadata().NumberOfRuns() != 2 || runs != 2 || len(clock.Waits()) != 1 {
			t.Fatalf("combined repeat = result %#v error %v runs %d waits %#v", result, err, runs, clock.Waits())
		}
	})
}

func TestRepeatInitialFalseAndExplicitZeroCompleteWithoutEffects(t *testing.T) {
	t.Parallel()

	t.Run("initial false", func(t *testing.T) {
		runtime := conditionRuntime(t, false)
		clock := newRepeatClock()
		actions := 0
		core, err := newExecutorCore(
			coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(),
			repeatActionHandlerSpec(func(string, *executionState) error { actions++; return nil }),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
		result, err := core.execute(context.Background(), repeatCommand(int64(3), repeatScriptCondition("${READY}"), "step"), 0)
		if err != nil || result.Outcome() != Completed || result.Metadata().NumberOfRuns() != 0 || actions != 0 || len(clock.Waits()) != 0 {
			t.Fatalf("initial false = result %#v error %v actions %d waits %#v", result, err, actions, clock.Waits())
		}
	})

	t.Run("false string is truthy", func(t *testing.T) {
		runtime := conditionRuntime(t, false)
		if err := runtime.PutEnv("READY", "false"); err != nil {
			t.Fatalf("PutEnv(false string) error = %v", err)
		}
		clock := newRepeatClock()
		actions := 0
		core, err := newExecutorCore(
			coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(),
			repeatActionHandlerSpec(func(string, *executionState) error { actions++; return nil }),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
		result, err := core.execute(context.Background(), repeatCommand(int64(2), repeatScriptCondition("${READY}"), "step"), 0)
		if err != nil || result.Outcome() != Completed || result.Metadata().NumberOfRuns() != 2 || actions != 2 {
			t.Fatalf("false-string repeat = result %#v error %v actions %d", result, err, actions)
		}
	})

	t.Run("explicit zero skips all services", func(t *testing.T) {
		platform := model.PlatformAndroid
		condition := &model.Condition{Platform: &platform}
		clock := newRepeatClock()
		events := make([]Event, 0)
		dependencies := coreDependencies(enginetest.NewFakeDriver(), clock)
		dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})}
		core, err := newExecutorCore(dependencies, repeatHandlerSpec(), repeatActionHandlerSpec(nil))
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		runtimeCalls := 0
		lookupCalls := 0
		core.state.runtimeFn = func() (js.Runtime, error) {
			runtimeCalls++
			return nil, errors.New("runtime must not be acquired")
		}
		core.state.lookupFn = func() (*ElementLookup, error) {
			lookupCalls++
			return nil, errors.New("lookup must not be acquired")
		}
		command := repeatCommand(int64(0), condition, "step")
		command.Arguments.(map[string]any)["while"] = map[string]any{"platform": "android"}
		result, err := core.execute(context.Background(), command, 0)
		if err != nil || result.Outcome() != Completed || !result.Metadata().HasNumberOfRuns() ||
			result.Metadata().NumberOfRuns() != 0 || runtimeCalls != 0 || lookupCalls != 0 || len(clock.Waits()) != 0 {
			t.Fatalf("zero repeat = result %#v error %v runtime %d lookup %d waits %#v", result, err, runtimeCalls, lookupCalls, clock.Waits())
		}
		for _, event := range events {
			if event.Kind() == EventCommandMetadataUpdated || event.Kind() == EventCommandReset {
				t.Fatalf("zero repeat emitted bookkeeping event %s", event.Kind())
			}
		}
	})
}

func TestRepeatBigIntConditionTruthiness(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		script   string
		wantRuns int
	}{
		{name: "direct zero", script: "0n"},
		{name: "direct one", script: "1n", wantRuns: 1},
		{name: "exact zero", script: "${0n}"},
		{name: "exact one", script: "${1n}", wantRuns: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := conditionRuntime(t, true)
			clock := newRepeatClock()
			runs := 0
			core, err := newExecutorCore(
				coreDependencies(enginetest.NewFakeDriver(), clock),
				repeatHandlerSpec(),
				repeatActionHandlerSpec(func(string, *executionState) error { runs++; return nil }),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
			result, err := core.execute(
				context.Background(),
				repeatCommand(int64(1), repeatScriptCondition(test.script), "step"),
				0,
			)
			if err != nil || result.Outcome() != Completed || runs != test.wantRuns || result.Metadata().NumberOfRuns() != test.wantRuns {
				t.Fatalf("repeat %q = result %#v error %v runs %d, want %d", test.script, result, err, runs, test.wantRuns)
			}
		})
	}
}

func TestRepeatDynamicTimesAndConditionReevaluateAcrossCompiledInvocations(t *testing.T) {
	t.Parallel()

	runtime := conditionRuntime(t, true)
	if err := runtime.PutEnv("COUNT", int64(2)); err != nil {
		t.Fatalf("PutEnv(COUNT=2) error = %v", err)
	}
	clock := newRepeatClock()
	compileCalls := 0
	actionCalls := 0
	actionSpec := repeatActionHandlerSpec(func(string, *executionState) error {
		actionCalls++
		return nil
	})
	baseCompile := actionSpec.compile
	actionSpec.compile = func(ctx context.Context, compileCtx compileContext, command model.Command) (any, error) {
		compileCalls++
		return baseCompile(ctx, compileCtx, command)
	}
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(), actionSpec,
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
	source := repeatCommand("${COUNT}", repeatScriptCondition("${READY}"), "step")
	compiled, err := core.dispatcher.compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	if compileCalls != 1 {
		t.Fatalf("child compiler calls = %d, want one", compileCalls)
	}

	first, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || first.Metadata().NumberOfRuns() != 2 {
		t.Fatalf("first invocation = result %#v error %v", first, err)
	}
	if err := runtime.PutEnv("COUNT", int64(1)); err != nil {
		t.Fatalf("PutEnv(COUNT=1) error = %v", err)
	}
	if err := runtime.PutEnv("READY", true); err != nil {
		t.Fatalf("PutEnv(READY=true) error = %v", err)
	}
	second, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || second.Metadata().NumberOfRuns() != 1 || actionCalls != 3 {
		t.Fatalf("second invocation = result %#v error %v action calls %d", second, err, actionCalls)
	}
	if compileCalls != 1 {
		t.Fatalf("child compiler reran %d times", compileCalls)
	}
	firstEvaluated, _ := first.Metadata().EvaluatedCommand()
	secondEvaluated, _ := second.Metadata().EvaluatedCommand()
	if firstEvaluated.Arguments.(map[string]any)["times"] != "2" ||
		secondEvaluated.Arguments.(map[string]any)["times"] != "1" ||
		compiled.command.Arguments.(map[string]any)["times"] != "${COUNT}" ||
		source.Arguments.(map[string]any)["times"] != "${COUNT}" {
		t.Fatalf("evaluated/source snapshots = first %#v second %#v compiled %#v source %#v",
			firstEvaluated.Arguments, secondEvaluated.Arguments, compiled.command.Arguments, source.Arguments)
	}
	if want := []time.Duration{RepeatDelay}; !reflect.DeepEqual(clock.Waits(), want) {
		t.Fatalf("waits = %#v, want %#v", clock.Waits(), want)
	}
}

func TestRepeatUnboundedAndDelayCancellationRemainDeterministic(t *testing.T) {
	t.Parallel()

	t.Run("unbounded child cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := newRepeatClock()
		runs := 0
		core, err := newExecutorCore(
			coreDependencies(enginetest.NewFakeDriver(), clock), repeatHandlerSpec(),
			repeatActionHandlerSpec(func(string, *executionState) error {
				runs++
				if runs == 4 {
					cancel()
				}
				return nil
			}),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		type execution struct {
			result CommandResult
			err    error
		}
		done := make(chan execution, 1)
		go func() {
			result, executeErr := core.execute(ctx, repeatCommand(nil, nil, "step"), 0)
			done <- execution{result: result, err: executeErr}
		}()
		select {
		case got := <-done:
			if got.err != context.Canceled || got.result.ProductError() != context.Canceled ||
				got.result.Outcome() != Cancelled || got.result.Metadata().NumberOfRuns() != 4 || runs != 4 {
				t.Fatalf("unbounded cancellation = result %#v error %T %v runs %d", got.result, got.err, got.err, runs)
			}
			if len(clock.Waits()) != 3 {
				t.Fatalf("unbounded waits = %#v, want three", clock.Waits())
			}
		case <-time.After(time.Second):
			t.Fatal("unbounded repeat did not honor deterministic cancellation")
		}
	})

	t.Run("cancellation during delay", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clock := newRepeatClock()
		clock.onWait = func(context.Context, int) error {
			cancel()
			return context.Canceled
		}
		events := make([]Event, 0)
		dependencies := coreDependencies(enginetest.NewFakeDriver(), clock)
		dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})}
		core, err := newExecutorCore(dependencies, repeatHandlerSpec(), repeatActionHandlerSpec(nil))
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		result, err := core.execute(ctx, repeatCommand(int64(2), nil, "step"), 0)
		if err != context.Canceled || result.ProductError() != context.Canceled ||
			result.Metadata().NumberOfRuns() != 1 || len(clock.Waits()) != 1 {
			t.Fatalf("delay cancellation = result %#v error %T %v waits %#v", result, err, err, clock.Waits())
		}
		for _, event := range events {
			if event.Kind() == EventCommandReset {
				t.Fatal("cancelled delay emitted a child reset")
			}
		}
	})
}

func TestRepeatPreservesAttemptFailureOverBookkeepingAndStopsImmediately(t *testing.T) {
	t.Parallel()

	bookkeepingFailure := errors.New("metadata failed")
	child := repeatActionCommand("step")
	failures := []error{
		NewOperationError("child failed", nil),
		context.Canceled,
		NewDeviceConnectionError("device disconnected", nil),
	}
	for _, childFailure := range failures {
		childFailure := childFailure
		t.Run(reflect.TypeOf(childFailure).String(), func(t *testing.T) {
			t.Parallel()
			var updated CommandMetadata
			state := &executionState{
				compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
				executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
					return []CommandResult{}, childFailure
				},
				metadataUpdatedFn: func(_ context.Context, metadata CommandMetadata) error {
					updated = metadata
					return bookkeepingFailure
				},
			}
			effect, err := executeRepeat(context.Background(), state, evaluatedDispatch{
				command: model.Command{Kind: model.CommandRepeat, Children: []model.Command{child}},
				value:   repeatEvaluated{times: 2},
			})
			if err != childFailure || effect.numberOfRuns != 1 || !effect.numberOfRunsSet ||
				!updated.HasNumberOfRuns() || updated.NumberOfRuns() != 1 {
				t.Fatalf("executeRepeat() = effect %#v error %T %v update %#v", effect, err, err, updated)
			}
		})
	}

	successState := &executionState{
		executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
			return []CommandResult{}, nil
		},
		metadataUpdatedFn: func(context.Context, CommandMetadata) error { return bookkeepingFailure },
	}
	effect, err := executeRepeat(context.Background(), successState, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRepeat}, value: repeatEvaluated{times: 1},
	})
	if err != bookkeepingFailure || effect.numberOfRuns != 1 {
		t.Fatalf("successful attempt bookkeeping = effect %#v error %T %v", effect, err, err)
	}
}

func TestRepeatResetFailureStopsBeforeReexecution(t *testing.T) {
	t.Parallel()

	resetFailure := errors.New("reset failed")
	clock := newRepeatClock()
	child := repeatActionCommand("step")
	executeCalls := 0
	resetCalls := 0
	state := &executionState{
		dependencies:     Dependencies{Clock: clock},
		compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
		executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
			executeCalls++
			return []CommandResult{{sequence: uint64(executeCalls)}}, nil
		},
		metadataUpdatedFn: func(context.Context, CommandMetadata) error { return nil },
		commandResetFn: func(context.Context, CommandResult) error {
			resetCalls++
			return resetFailure
		},
	}
	effect, err := executeRepeat(context.Background(), state, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRepeat, Children: []model.Command{child}},
		value:   repeatEvaluated{times: 2},
	})
	if err != resetFailure || effect.numberOfRuns != 1 || executeCalls != 1 || resetCalls != 1 ||
		!reflect.DeepEqual(clock.Waits(), []time.Duration{RepeatDelay}) {
		t.Fatalf("reset failure = effect %#v error %T %v executes %d resets %d waits %#v",
			effect, err, err, executeCalls, resetCalls, clock.Waits())
	}
}

func TestRepeatRootPolicyFinalizesOnlyOuterFailure(t *testing.T) {
	t.Parallel()

	ordinary := NewOperationError("ordinary child failure", nil)
	deviceFailure := NewDeviceConnectionError("device disconnected", nil)
	tests := []struct {
		name              string
		failure           error
		wantResolverCalls int
		wantAfter         bool
	}{
		{name: "ordinary reaches outer resolver", failure: ordinary, wantResolverCalls: 1, wantAfter: true},
		{name: "cancellation bypasses resolver", failure: context.Canceled},
		{name: "device bypasses resolver", failure: deviceFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			order := make([]string, 0, 2)
			actionSpec := repeatActionHandlerSpec(func(name string, _ *executionState) error {
				order = append(order, name)
				if name == "child" {
					return test.failure
				}
				return nil
			})
			registry, err := newHandlerRegistry(repeatHandlerSpec(), actionSpec)
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			dispatcher := newDispatcher(registry)
			repeatDispatch, err := dispatcher.compile(
				context.Background(), compileContext{}, repeatCommand(int64(2), nil, "child"),
			)
			if err != nil {
				t.Fatalf("compile(repeat) error = %v", err)
			}
			afterDispatch, err := dispatcher.compile(context.Background(), compileContext{}, repeatActionCommand("after"))
			if err != nil {
				t.Fatalf("compile(after) error = %v", err)
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
			dependencies.Clock = newRepeatClock()
			dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
				resolverCalls++
				if result.Command().Kind != model.CommandRepeat || result.Depth() != 0 || result.ProductError() != test.failure {
					t.Fatalf("resolver received non-outer failure: %#v", result)
				}
				return FailureDecisionContinue
			})
			result, err := executeCompiledRoot(context.Background(), dependencies, &compiledFlow{
				path: "/workspace/repeat-policy.yaml", config: model.Config{AppID: "com.example.app"},
				body: []compiledDispatch{repeatDispatch, afterDispatch},
			})
			if err != test.failure || result.ProductError() != test.failure || resolverCalls != test.wantResolverCalls {
				t.Fatalf("root policy = error %T %v product %T calls %d", err, err, result.ProductError(), resolverCalls)
			}
			wantOrder := []string{"child"}
			if test.wantAfter {
				wantOrder = append(wantOrder, "after")
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("execution order = %#v, want %#v", order, wantOrder)
			}
			commands := result.Commands()
			if len(commands) < 2 || commands[0].Command().Kind != model.CommandRepeat ||
				commands[1].Depth() != 1 || commands[1].ProductError() != test.failure {
				t.Fatalf("root ledger = %#v", commands)
			}
		})
	}
}

func TestRepeatMalformedRuntimeBoundariesFailClosedAfterAtomicRegistration(t *testing.T) {
	t.Parallel()

	if _, err := evaluateRepeat(context.Background(), evaluationContext{}, model.Command{Kind: model.CommandRepeat}, struct{}{}); err == nil {
		t.Fatal("evaluateRepeat accepted foreign compiled payload")
	}
	if _, err := executeRepeat(context.Background(), &executionState{}, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRepeat}, value: struct{}{},
	}); err == nil {
		t.Fatal("executeRepeat accepted foreign evaluated payload")
	}
	if _, err := executeRepeat(nil, nil, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRepeat}, value: repeatEvaluated{times: 1},
	}); err == nil {
		t.Fatal("executeRepeat accepted nil context")
	}
	zero, err := executeRepeat(context.Background(), nil, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRepeat}, value: repeatEvaluated{times: 0},
	})
	if err != nil || !zero.numberOfRunsSet || zero.numberOfRuns != 0 {
		t.Fatalf("zero repeat with no state = effect %#v error %v", zero, err)
	}

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	if len(registry.byKeyword) != len(productionKeywords()) {
		t.Fatalf("production registry size = %d, want the complete set", len(registry.byKeyword))
	}
	if _, exists := registry.lookup(model.CommandRepeat); !exists {
		t.Fatal("repeat missing after atomic control-flow registration")
	}
}

func repeatActionCommand(name string) model.Command {
	return model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: name}
}

func repeatActionHandlerSpec(execute func(string, *executionState) error) handlerSpec {
	return handlerSpec{
		keyword: model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(command model.Command) (any, error) {
			return decodeString(command)
		}),
		evaluate: identityEvaluator,
		execute: func(_ context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
			name, ok := evaluated.value.(string)
			if !ok {
				return commandEffect{}, NewConfigurationError("repeat test action payload is invalid", nil)
			}
			if execute != nil {
				if err := execute(name, state); err != nil {
					return commandEffect{effectClass: EffectObserved}, err
				}
			}
			return commandEffect{effectClass: EffectObserved}, nil
		},
	}
}

func repeatCommand(times any, condition *model.Condition, actions ...string) model.Command {
	rawChildren := make([]any, len(actions))
	children := make([]model.Command, len(actions))
	for index, action := range actions {
		rawChildren[index] = map[string]any{"action": action}
		children[index] = repeatActionCommand(action)
	}
	arguments := map[string]any{"commands": rawChildren}
	if times != nil {
		arguments["times"] = times
	}
	if condition != nil {
		raw := make(map[string]any)
		if condition.ScriptCondition != nil {
			raw["true"] = *condition.ScriptCondition
		}
		arguments["while"] = raw
	}
	return model.Command{
		Kind: model.CommandRepeat, Form: model.CommandFormObject, Arguments: arguments,
		Condition: cloneCondition(condition), Children: children,
	}
}

func repeatScriptCondition(script string) *model.Condition {
	return &model.Condition{ScriptCondition: &script}
}

func repeatEventKinds(events []Event) []EventKind {
	kinds := make([]EventKind, len(events))
	for index := range events {
		kinds[index] = events[index].Kind()
	}
	return kinds
}

type repeatClock struct {
	mu     sync.Mutex
	now    time.Time
	waits  []time.Duration
	onWait func(context.Context, int) error
}

type repeatInterpolationRuntime struct {
	js.Runtime
	value            string
	err              error
	interpolateCalls int
	evaluateCalls    int
}

func (runtime *repeatInterpolationRuntime) Interpolate(
	context.Context,
	string,
	map[string]any,
) (string, error) {
	runtime.interpolateCalls++
	return runtime.value, runtime.err
}

func (runtime *repeatInterpolationRuntime) Evaluate(
	ctx context.Context,
	request js.EvalRequest,
) (js.Result, error) {
	runtime.evaluateCalls++
	return runtime.Runtime.Evaluate(ctx, request)
}

func newRepeatClock() *repeatClock { return &repeatClock{now: time.Unix(700, 0)} }

func (clock *repeatClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *repeatClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.mu.Lock()
	clock.waits = append(clock.waits, delay)
	clock.now = clock.now.Add(delay)
	index := len(clock.waits)
	onWait := clock.onWait
	clock.mu.Unlock()
	if onWait != nil {
		return onWait(ctx, index)
	}
	return ctx.Err()
}

func (clock *repeatClock) Waits() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return append([]time.Duration(nil), clock.waits...)
}
