package engine

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestCompileRetryRequiresExactlyOnePrecompiledSourceAndRetainsCounterShape(t *testing.T) {
	t.Parallel()

	childFlow := &compiledFlow{path: "/workspace/child.yaml"}
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: childFlow.path,
	}
	inlineChild := retryActionCommand("step")
	tests := []struct {
		name             string
		command          model.Command
		wantError        bool
		wantRequireCalls int
		wantSource       retrySource
		wantCounter      retryCounterKind
		wantFixed        int
		wantExpression   string
	}{
		{
			name: "inline default",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{"commands": []any{map[string]any{"action": "step"}}},
				Children:  []model.Command{inlineChild},
			},
			wantSource: retryInlineSource, wantCounter: retryCounterMissing,
		},
		{
			name: "inline fixed zero",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"maxRetries": int64(0),
					"commands":   []any{map[string]any{"action": "step"}},
				},
				Children: []model.Command{inlineChild},
			},
			wantSource: retryInlineSource, wantCounter: retryCounterFixed,
		},
		{
			name: "inline dynamic",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"maxRetries": "${RETRIES}",
					"commands":   []any{map[string]any{"action": "step"}},
				},
				Children: []model.Command{inlineChild},
			},
			wantSource: retryInlineSource, wantCounter: retryCounterDynamic,
			wantExpression: "${RETRIES}",
		},
		{
			name: "linked fixed cap",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{"file": "child.yaml", "maxRetries": int64(3)},
				Links:     []model.FileLink{link},
			},
			wantRequireCalls: 1, wantSource: retryLinkedSource,
			wantCounter: retryCounterFixed, wantFixed: RetryCommandMaxRetries,
		},
		{
			name: "dual source",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{"file": "child.yaml", "commands": []any{}},
				Links:     []model.FileLink{link},
			},
			wantError: true,
		},
		{
			name: "missing source",
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{"maxRetries": int64(1)},
			},
			wantError: true,
		},
		{
			name:      "scalar form",
			command:   model.Command{Kind: model.CommandRetry, Form: model.CommandFormScalar},
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry, err := newHandlerRegistry(retryHandlerSpec(), retryActionHandlerSpec(nil))
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			requireCalls := 0
			compiled, compileErr := newDispatcher(registry).compile(
				context.Background(),
				compileContext{
					containingFlow: "/workspace/root.yaml",
					requireFlow: func(got model.FileLink) (*compiledFlow, error) {
						requireCalls++
						if got != link {
							return nil, errors.New("unexpected link")
						}
						return childFlow, nil
					},
				},
				test.command,
			)
			if test.wantError {
				if compileErr == nil {
					t.Fatalf("compile() = %#v, nil; want error", compiled)
				}
				var configuration *ConfigurationError
				if !errors.As(compileErr, &configuration) {
					t.Fatalf("compile() error = %T %v, want *ConfigurationError", compileErr, compileErr)
				}
			} else if compileErr != nil {
				t.Fatalf("compile() error = %v", compileErr)
			} else {
				payload, ok := compiled.value.(retryCompiled)
				if !ok || payload.source != test.wantSource || payload.counterKind != test.wantCounter ||
					payload.fixedMaxRetries != test.wantFixed || payload.maxRetriesExpression != test.wantExpression {
					t.Fatalf("compiled payload = %#v", compiled.value)
				}
				if test.wantSource == retryLinkedSource && payload.linked != childFlow {
					t.Fatalf("linked flow = %p, want retained %p", payload.linked, childFlow)
				}
			}
			if requireCalls != test.wantRequireCalls {
				t.Fatalf("RequireFlow calls = %d, want %d", requireCalls, test.wantRequireCalls)
			}
		})
	}
}

func TestCompileRetryRejectsForgedTypedLinkAndCounterBoundaries(t *testing.T) {
	t.Parallel()

	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: "/workspace/child.yaml",
	}
	script := "${READY}"
	validInline := func() model.Command { return retryCommand(int64(1), "step") }
	validFile := func() model.Command {
		return model.Command{
			Kind: model.CommandRetry, Form: model.CommandFormObject,
			Arguments: map[string]any{"file": "child.yaml", "maxRetries": int64(1)},
			Links:     []model.FileLink{link},
		}
	}
	tests := []struct {
		name        string
		command     func() model.Command
		mutate      func(*model.Command)
		requireNil  bool
		requireFail error
	}{
		{name: "unknown field", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["future"] = true
		}},
		{name: "commands not array", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["commands"] = "step"
		}},
		{name: "raw typed child count mismatch", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["commands"] = []any{}
		}},
		{name: "raw typed child value mismatch", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["commands"] = []any{map[string]any{"action": "forged"}}
		}},
		{name: "raw typed child form mismatch", command: validInline, mutate: func(command *model.Command) {
			command.Children[0].Form = model.CommandFormScalar
			command.Children[0].Arguments = nil
		}},
		{name: "inline foreign link", command: validInline, mutate: func(command *model.Command) {
			command.Links = []model.FileLink{link}
		}},
		{name: "negative fixed counter", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["maxRetries"] = int64(-1)
		}},
		{name: "fixed counter above cap", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["maxRetries"] = int64(4)
		}},
		{name: "nonnormalized counter", command: validInline, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["maxRetries"] = float64(1)
		}},
		{name: "forged condition", command: validInline, mutate: func(command *model.Command) {
			command.Condition = &model.Condition{ScriptCondition: &script}
		}},
		{name: "forged selector", command: validInline, mutate: func(command *model.Command) {
			command.Selector = &model.ElementSelector{}
		}},
		{name: "blank file", command: validFile, mutate: func(command *model.Command) {
			command.Arguments.(map[string]any)["file"] = "  "
			command.Links[0].Path = "  "
		}},
		{name: "file typed children", command: validFile, mutate: func(command *model.Command) {
			command.Children = []model.Command{retryActionCommand("forged")}
		}},
		{name: "file missing link", command: validFile, mutate: func(command *model.Command) {
			command.Links = nil
		}},
		{name: "file multiple links", command: validFile, mutate: func(command *model.Command) {
			command.Links = append(command.Links, link)
		}},
		{name: "wrong link kind", command: validFile, mutate: func(command *model.Command) {
			command.Links[0].Kind = model.FileLinkScript
		}},
		{name: "blank link path", command: validFile, mutate: func(command *model.Command) {
			command.Links[0].Path = ""
		}},
		{name: "mismatched link path", command: validFile, mutate: func(command *model.Command) {
			command.Links[0].Path = "other.yaml"
		}},
		{name: "nil prepared flow", command: validFile, requireNil: true},
		{name: "prepared flow failure identity", command: validFile, requireFail: errors.New("prepared flow failed")},
	}

	registry, err := newHandlerRegistry(retryHandlerSpec(), retryActionHandlerSpec(nil))
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := test.command()
			if test.mutate != nil {
				test.mutate(&command)
			}
			_, compileErr := newDispatcher(registry).compile(
				context.Background(),
				compileContext{requireFlow: func(model.FileLink) (*compiledFlow, error) {
					if test.requireFail != nil {
						return nil, test.requireFail
					}
					if test.requireNil {
						return nil, nil
					}
					return &compiledFlow{path: "/workspace/child.yaml"}, nil
				}},
				command,
			)
			if test.requireFail != nil {
				if compileErr != test.requireFail {
					t.Fatalf("compile() error = %T %v, want exact prepared failure", compileErr, compileErr)
				}
				return
			}
			var configuration *ConfigurationError
			if !errors.As(compileErr, &configuration) {
				t.Fatalf("compile() error = %T %v, want *ConfigurationError", compileErr, compileErr)
			}
		})
	}
}

func TestRetryDefaultZeroCapAndExhaustionCountTotalAttempts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		maxRetries     any
		failuresBefore int
		wantAttempts   int
		wantResets     int
		wantError      bool
		wantInsight    string
	}{
		{name: "successful first attempt has no insight", maxRetries: int64(3), wantAttempts: 1},
		{name: "default retries once", failuresBefore: 1, wantAttempts: 2, wantResets: 1, wantInsight: retrySuccessInsight},
		{name: "zero means initial only", maxRetries: int64(0), failuresBefore: 1, wantAttempts: 1, wantError: true},
		{name: "cap permits three retries", maxRetries: int64(3), failuresBefore: 3, wantAttempts: 4, wantResets: 3, wantInsight: retrySuccessInsight},
		{name: "exhaustion returns last failure", maxRetries: int64(2), failuresBefore: 3, wantAttempts: 3, wantResets: 2, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			failure := NewOperationError("retryable failure", nil)
			resetCount := 0
			dependencies := coreDependencies(
				enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
			)
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandReset {
					resetCount++
				}
				return nil
			})}
			core, err := newExecutorCore(
				dependencies,
				retryHandlerSpec(),
				retryActionHandlerSpec(func(string, *executionState) error {
					attempts++
					if attempts <= test.failuresBefore {
						return failure
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			result, executeErr := core.execute(
				context.Background(), retryCommand(test.maxRetries, "step"), 0,
			)
			if test.wantError {
				if executeErr != failure || result.ProductError() != failure {
					t.Fatalf("retry error = returned %T %v product %T %v, want exact failure", executeErr, executeErr, result.ProductError(), result.ProductError())
				}
			} else if executeErr != nil || result.Outcome() != Completed {
				t.Fatalf("retry result = outcome %q error %v", result.Outcome(), executeErr)
			}
			metadata := result.Metadata()
			if attempts != test.wantAttempts || !metadata.HasNumberOfRuns() ||
				metadata.NumberOfRuns() != test.wantAttempts || metadata.Insight() != test.wantInsight ||
				resetCount != test.wantResets {
				t.Fatalf("retry attempts = %d metadata %#v resets %d, want attempts %d insight %q resets %d",
					attempts, metadata, resetCount, test.wantAttempts, test.wantInsight, test.wantResets)
			}
		})
	}
}

func TestEvaluateRetryParsesDynamicCounterStrictlyAndOwnsSnapshots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value     string
		want      int
		wantError bool
	}{
		{value: "0", want: 0},
		{value: "0003", want: 3},
		{value: "", wantError: true},
		{value: "-1", wantError: true},
		{value: "+1", wantError: true},
		{value: " 1", wantError: true},
		{value: "1 ", wantError: true},
		{value: "1.0", wantError: true},
		{value: "1e2", wantError: true},
		{value: "4", wantError: true},
		{value: "2147483648", wantError: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			command := retryCommand("${RETRIES}", "step")
			evaluation := evaluationContext{interpolateFn: func(context.Context, string, map[string]any) (string, error) {
				return test.value, nil
			}}
			evaluated, err := evaluateRetry(context.Background(), evaluation, command, retryCompiled{
				source: retryInlineSource, counterKind: retryCounterDynamic,
				maxRetriesExpression: "${RETRIES}",
			})
			if test.wantError {
				var configuration *ConfigurationError
				if !errors.As(err, &configuration) {
					t.Fatalf("evaluateRetry(%q) error = %T %v, want ConfigurationError", test.value, err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("evaluateRetry(%q) error = %v", test.value, err)
			}
			payload, ok := evaluated.value.(retryEvaluated)
			if !ok || payload.maxRetries != test.want {
				t.Fatalf("evaluated payload = %#v, want maxRetries %d", evaluated.value, test.want)
			}
			if got := evaluated.command.Arguments.(map[string]any)["maxRetries"]; got != test.value {
				t.Fatalf("evaluated maxRetries = %#v, want %q", got, test.value)
			}
			if got := command.Arguments.(map[string]any)["maxRetries"]; got != "${RETRIES}" {
				t.Fatalf("source command mutated to %#v", got)
			}
		})
	}
}

func TestRetryDynamicCounterFailuresFinalizeExplicitZeroWithoutEffects(t *testing.T) {
	t.Parallel()

	interpolationFailure := NewConfigurationError("interpolation failed", nil)
	tests := []struct {
		name      string
		value     string
		failure   error
		wantExact error
	}{
		{name: "blank", value: ""},
		{name: "sign", value: "+1"},
		{name: "space", value: " 1"},
		{name: "fraction", value: "1.0"},
		{name: "exponent", value: "1e2"},
		{name: "overflow", value: "2147483648"},
		{name: "above cap", value: "4"},
		{name: "interpolation identity", failure: interpolationFailure, wantExact: interpolationFailure},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			events := make([]Event, 0, 2)
			actionCalls := 0
			dependencies := coreDependencies(
				enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
			)
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})}
			core, err := newExecutorCore(
				dependencies, retryHandlerSpec(), retryActionHandlerSpec(func(string, *executionState) error {
					actionCalls++
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			runtime := &repeatInterpolationRuntime{
				Runtime: conditionRuntime(t, true), value: test.value, err: test.failure,
			}
			core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
			result, executeErr := core.execute(
				context.Background(), retryCommand("${RETRIES}", "step"), 0,
			)
			if test.wantExact != nil {
				if executeErr != test.wantExact {
					t.Fatalf("execute() error = %T %v, want exact interpolation failure", executeErr, executeErr)
				}
			} else {
				var configuration *ConfigurationError
				if !errors.As(executeErr, &configuration) {
					t.Fatalf("execute() error = %T %v, want ConfigurationError", executeErr, executeErr)
				}
			}
			metadata := result.Metadata()
			if result.ProductError() != executeErr || result.Outcome() != Failed ||
				!metadata.HasNumberOfRuns() || metadata.NumberOfRuns() != 0 || metadata.Insight() != "" {
				t.Fatalf("dynamic failure = result %#v metadata %#v error %T %v", result, metadata, executeErr, executeErr)
			}
			if actionCalls != 0 || runtime.interpolateCalls != 1 || runtime.evaluateCalls != 0 {
				t.Fatalf("effects = actions %d interpolate %d evaluate %d", actionCalls, runtime.interpolateCalls, runtime.evaluateCalls)
			}
			if got, want := retryEventKinds(events), []EventKind{EventCommandStarted, EventCommandFinished}; !reflect.DeepEqual(got, want) {
				t.Fatalf("event kinds = %v, want %v", got, want)
			}
		})
	}
}

func TestRetryDynamicCounterReevaluatesAcrossCompiledInvocationsWithoutRecompiling(t *testing.T) {
	t.Parallel()

	runtime := &repeatInterpolationRuntime{Runtime: conditionRuntime(t, true), value: "1"}
	compileCalls := 0
	actionCalls := 0
	failure := NewOperationError("retryable", nil)
	actionSpec := retryActionHandlerSpec(func(string, *executionState) error {
		actionCalls++
		if actionCalls == 1 || actionCalls == 3 {
			return failure
		}
		return nil
	})
	baseCompile := actionSpec.compile
	actionSpec.compile = func(ctx context.Context, compileCtx compileContext, command model.Command) (any, error) {
		compileCalls++
		return baseCompile(ctx, compileCtx, command)
	}
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		retryHandlerSpec(), actionSpec,
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
	source := retryCommand("${RETRIES}", "step")
	compiled, err := core.dispatcher.compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	first, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != nil || first.Metadata().NumberOfRuns() != 2 || first.Metadata().Insight() != retrySuccessInsight {
		t.Fatalf("first invocation = result %#v error %v", first, err)
	}
	runtime.value = "0"
	second, err := core.executeCompiled(context.Background(), compiled, 0)
	if err != failure || second.ProductError() != failure || second.Metadata().NumberOfRuns() != 1 {
		t.Fatalf("second invocation = result %#v error %T %v", second, err, err)
	}
	firstEvaluated, _ := first.Metadata().EvaluatedCommand()
	secondEvaluated, _ := second.Metadata().EvaluatedCommand()
	if firstEvaluated.Arguments.(map[string]any)["maxRetries"] != "1" ||
		secondEvaluated.Arguments.(map[string]any)["maxRetries"] != "0" ||
		compiled.command.Arguments.(map[string]any)["maxRetries"] != "${RETRIES}" ||
		source.Arguments.(map[string]any)["maxRetries"] != "${RETRIES}" {
		t.Fatalf("snapshots mutated = first %#v second %#v compiled %#v source %#v",
			firstEvaluated.Arguments, secondEvaluated.Arguments, compiled.command.Arguments, source.Arguments)
	}
	if runtime.interpolateCalls != 2 || compileCalls != 1 || actionCalls != 3 {
		t.Fatalf("calls = interpolation %d compile %d action %d, want 2/1/3", runtime.interpolateCalls, compileCalls, actionCalls)
	}
}

func TestRetryInlineResetsExactPartialAttemptBeforeSequentialSuccess(t *testing.T) {
	t.Parallel()

	failure := NewAssertionError("second action failed", nil)
	actions := make([]string, 0, 5)
	events := make([]Event, 0)
	dependencies := coreDependencies(
		enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
	)
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})}
	core, err := newExecutorCoreForRootRun(
		dependencies, "retry-inline-root", retryHandlerSpec(),
		retryActionHandlerSpec(func(name string, _ *executionState) error {
			actions = append(actions, name)
			if len(actions) == 2 {
				return failure
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error = %v", err)
	}
	result, err := core.execute(
		context.Background(), retryCommand(int64(1), "first", "second", "third"), 2,
	)
	if err != nil || result.Outcome() != Completed || result.Depth() != 2 ||
		result.Metadata().NumberOfRuns() != 2 || result.Metadata().Insight() != retrySuccessInsight {
		t.Fatalf("retry result = %#v error %v", result, err)
	}
	if want := []string{"first", "second", "first", "second", "third"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %#v, want %#v", actions, want)
	}

	updates := make([]Event, 0, 2)
	resets := make([]Event, 0, 2)
	firstAttemptFinished := make([]Event, 0, 2)
	for _, event := range events {
		switch event.Kind() {
		case EventCommandFinished:
			if event.Depth() == result.Depth()+1 && len(firstAttemptFinished) < 2 {
				firstAttemptFinished = append(firstAttemptFinished, event)
			}
		case EventCommandMetadataUpdated:
			updates = append(updates, event)
		case EventCommandReset:
			resets = append(resets, event)
		}
	}
	if len(firstAttemptFinished) != 2 || len(updates) != 2 || len(resets) != 2 {
		t.Fatalf("finished/update/reset counts = %d/%d/%d; events %v",
			len(firstAttemptFinished), len(updates), len(resets), retryEventKinds(events))
	}
	for index := range resets {
		if resets[index].RootRunID() != firstAttemptFinished[index].RootRunID() ||
			resets[index].Sequence() != firstAttemptFinished[index].Sequence() ||
			resets[index].Depth() != firstAttemptFinished[index].Depth() {
			t.Fatalf("reset %d identity = %#v, want first-attempt child %#v", index, resets[index], firstAttemptFinished[index])
		}
	}
	if updates[0].Metadata().NumberOfRuns() != 1 || updates[0].Metadata().Insight() != "" ||
		updates[1].Metadata().NumberOfRuns() != 2 || updates[1].Metadata().Insight() != retrySuccessInsight {
		t.Fatalf("metadata updates = %#v", updates)
	}
	firstResetIndex := retryEventIndex(events, EventCommandReset, 0)
	thirdChildStartIndex := retryEventIndexAtDepth(events, EventCommandStarted, result.Depth()+1, 2)
	if firstResetIndex < 0 || thirdChildStartIndex < 0 || firstResetIndex >= thirdChildStartIndex {
		t.Fatalf("reset/start ordering invalid: reset index %d next attempt index %d events %v",
			firstResetIndex, thirdChildStartIndex, retryEventKinds(events))
	}
}

func TestRetryExhaustionDoesNotResetFinalAttempt(t *testing.T) {
	t.Parallel()

	failure := NewOperationError("always fails", nil)
	events := make([]Event, 0)
	dependencies := coreDependencies(
		enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
	)
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})}
	core, err := newExecutorCore(
		dependencies, retryHandlerSpec(), retryActionHandlerSpec(func(string, *executionState) error {
			return failure
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	result, err := core.execute(context.Background(), retryCommand(int64(2), "step"), 0)
	if err != failure || result.ProductError() != failure || result.Metadata().NumberOfRuns() != 3 {
		t.Fatalf("exhaustion = result %#v error %T %v", result, err, err)
	}
	resets := 0
	for _, event := range events {
		if event.Kind() == EventCommandReset {
			resets++
		}
	}
	if resets != 2 {
		t.Fatalf("reset count = %d, want one after each retryable nonfinal attempt; events %v", resets, retryEventKinds(events))
	}
}

func TestRetryExhaustionReturnsExactLastRetryableError(t *testing.T) {
	t.Parallel()

	failures := []error{
		NewOperationError("first failure", nil),
		NewAssertionError("second failure", nil),
		NewOperationError("final failure", nil),
	}
	attempt := 0
	core, err := newExecutorCore(
		coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
		retryHandlerSpec(), retryActionHandlerSpec(func(string, *executionState) error {
			failure := failures[attempt]
			attempt++
			return failure
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	result, err := core.execute(context.Background(), retryCommand(int64(2), "step"), 0)
	if err != failures[2] || result.ProductError() != failures[2] || attempt != 3 ||
		result.Metadata().NumberOfRuns() != 3 || result.Metadata().Insight() != "" {
		t.Fatalf("last error = result %#v returned %T %v attempts %d, want exact final pointer", result, err, err, attempt)
	}
}

func TestRetryDirectSkipStopsRetryWithoutMaskingLaterAttemptError(t *testing.T) {
	t.Parallel()

	skipped := NewCommandSkippedError("direct child skipped", nil)
	tests := []struct {
		name  string
		later error
	}{
		{name: "operation", later: NewOperationError("later product failure", nil)},
		{name: "assertion", later: NewAssertionError("later assertion failure", nil)},
		{name: "terminal device", later: NewDeviceConnectionError("later device failure", nil)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			actions := make([]string, 0, 2)
			resets := 0
			dependencies := coreDependencies(
				enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
			)
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				if event.Kind() == EventCommandReset {
					resets++
				}
				return nil
			})}
			core, err := newExecutorCore(
				dependencies, retryHandlerSpec(),
				retryActionHandlerSpec(func(name string, _ *executionState) error {
					actions = append(actions, name)
					if name == "skip" {
						return skipped
					}
					return test.later
				}),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			result, executeErr := core.execute(
				context.Background(), retryCommand(int64(1), "skip", "later"), 0,
			)
			if executeErr != test.later || result.ProductError() != test.later || result.Outcome() != Failed {
				t.Fatalf("mixed attempt = outcome %q returned %T %v product %T %v, want exact later failure",
					result.Outcome(), executeErr, executeErr, result.ProductError(), result.ProductError())
			}
			if !result.Metadata().HasNumberOfRuns() || result.Metadata().NumberOfRuns() != 1 ||
				result.Metadata().Insight() != "" || resets != 0 {
				t.Fatalf("mixed metadata = %#v resets %d, want one run without insight or reset", result.Metadata(), resets)
			}
			if want := []string{"skip", "later"}; !reflect.DeepEqual(actions, want) {
				t.Fatalf("actions = %#v, want one exact attempt %#v", actions, want)
			}
			commands := core.ledger.snapshot()
			if len(commands) != 3 || commands[0].ProductError() != test.later ||
				commands[1].Outcome() != Skipped || commands[1].ProductError() != skipped ||
				commands[2].Outcome() != Failed || commands[2].ProductError() != test.later {
				t.Fatalf("mixed ledger = %#v", commands)
			}
		})
	}
}

func TestRetryDirectWarningDoesNotBlockLaterRetryableFailure(t *testing.T) {
	t.Parallel()

	warning := NewOperationError("optional child warning", nil)
	laterFailure := NewAssertionError("later retryable failure", nil)
	actions := make([]string, 0, 4)
	laterCalls := 0
	resets := 0
	dependencies := coreDependencies(
		enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
	)
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandReset {
			resets++
		}
		return nil
	})}
	core, err := newExecutorCore(
		dependencies, retryHandlerSpec(),
		retryActionHandlerSpec(func(name string, _ *executionState) error {
			actions = append(actions, name)
			if name == "warned" {
				return warning
			}
			laterCalls++
			if laterCalls == 1 {
				return laterFailure
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	command := retryCommand(int64(1), "warned", "later")
	optional := true
	command.Children[0].Optional = &optional
	result, executeErr := core.execute(context.Background(), command, 0)
	if executeErr != nil || result.Outcome() != Completed || result.ProductError() != nil ||
		result.Metadata().NumberOfRuns() != 2 || result.Metadata().Insight() != retrySuccessInsight {
		t.Fatalf("warned retry = result %#v error %v", result, executeErr)
	}
	if want := []string{"warned", "later", "warned", "later"}; !reflect.DeepEqual(actions, want) {
		t.Fatalf("actions = %#v, want retry order %#v", actions, want)
	}
	if resets != 2 {
		t.Fatalf("reset count = %d, want both immediate first-attempt results", resets)
	}
	commands := core.ledger.snapshot()
	if len(commands) != 5 || commands[1].Outcome() != Warned || commands[1].ProductError() != warning ||
		commands[2].Outcome() != Failed || commands[2].ProductError() != laterFailure ||
		commands[3].Outcome() != Warned || commands[3].ProductError() != warning ||
		commands[4].Outcome() != Completed {
		t.Fatalf("warned retry ledger = %#v", commands)
	}
}

func TestRetryLinkedReplaysCompleteLifecycleAndResetsOnlyImmediateDepth(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/retry-root.yaml"
	childPath := "/workspace/retry-child.yaml"
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "retry-child.yaml", ResolvedPath: childPath,
	}
	retryFile := retryFileCommand(int64(1), link)
	deepAction := retryActionCommand("child-body")
	childInline := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"commands": []any{map[string]any{"action": "child-body"}},
		},
		Children: []model.Command{deepAction},
		Source: model.SourceInfo{
			Path: childPath, Start: model.Position{Line: 9, Column: 3, Offset: 70},
		},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config: model.Config{
			AppID: "com.example.retry-root",
			Env:   map[string]string{"COLLIDE": "root", "ROOT": "yes"},
		},
		Commands: []model.Command{retryFile},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config: model.Config{
			AppID:          "com.example.retry-child",
			Env:            map[string]string{"CHILD": "yes", "COLLIDE": "child"},
			OnFlowStart:    []model.Command{retryActionCommand("child-start")},
			OnFlowComplete: []model.Command{retryActionCommand("child-complete")},
		},
		Commands: []model.Command{childInline},
	}

	compileCalls := 0
	bodyCalls := 0
	order := make([]string, 0, 6)
	type observation struct {
		name  string
		appID string
		env   map[string]string
	}
	observations := make([]observation, 0, 6)
	actionSpec := retryActionHandlerSpec(func(name string, state *executionState) error {
		config, err := state.activeConfig()
		if err != nil {
			return err
		}
		active, err := state.jsRuntime()
		if err != nil {
			return err
		}
		runtime, ok := active.(interface{ CurrentEnvironment() map[string]string })
		if !ok {
			return NewConfigurationError("retry test runtime cannot expose its environment", nil)
		}
		order = append(order, name)
		observations = append(observations, observation{
			name: name, appID: config.AppID, env: runtime.CurrentEnvironment(),
		})
		if name == "child-body" {
			bodyCalls++
			if bodyCalls == 1 {
				return NewOperationError("first nested body failed", nil)
			}
		}
		return nil
	})
	baseCompile := actionSpec.compile
	actionSpec.compile = func(ctx context.Context, compileCtx compileContext, command model.Command) (any, error) {
		compileCalls++
		return baseCompile(ctx, compileCtx, command)
	}
	registry, err := newHandlerRegistry(retryHandlerSpec(), runFlowHandlerSpec(), actionSpec)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, exists := compiled.Flow(rootPath)
	if !exists {
		t.Fatal("compiled root missing")
	}
	events := make([]Event, 0)
	runtimeState := &sessionRuntime{}
	dependencies := flowExecutorDependencies(runtimeState, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		events = append(events, event)
		return nil
	})})
	dependencies.ExternalEnvironment = map[string]string{"COLLIDE": "external", "EXTERNAL": "yes"}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "retry-file-root")
	if err != nil || result.Outcome() != Completed {
		t.Fatalf("executeCompiledRoot() = outcome %q error %v", result.Outcome(), err)
	}
	if want := []string{
		"child-start", "child-body", "child-complete",
		"child-start", "child-body", "child-complete",
	}; !reflect.DeepEqual(order, want) {
		t.Fatalf("linked lifecycle = %#v, want %#v", order, want)
	}
	wantEnvironment := withFileName(childPath, map[string]string{
		"CHILD": "yes", "COLLIDE": "child", "EXTERNAL": "yes", "ROOT": "yes",
	})
	if len(observations) != 6 {
		t.Fatalf("lifecycle observations = %#v", observations)
	}
	for index, got := range observations {
		if got.appID != "com.example.retry-child" || !reflect.DeepEqual(got.env, wantEnvironment) {
			t.Fatalf("observation %d = %#v, want child config and lexical environment %#v", index, got, wantEnvironment)
		}
	}
	if compileCalls != 3 {
		t.Fatalf("action compiler calls = %d, want exactly the three retained action nodes", compileCalls)
	}
	commands := result.Commands()
	if len(commands) == 0 || commands[0].Command().Kind != model.CommandRetry ||
		commands[0].Metadata().NumberOfRuns() != 2 || commands[0].Metadata().Insight() != retrySuccessInsight {
		t.Fatalf("root retry command = %#v", commands)
	}

	firstImmediate := make([]Event, 0, 3)
	resets := make([]Event, 0, 3)
	for _, event := range events {
		if event.Kind() == EventCommandReset {
			resets = append(resets, event)
			continue
		}
		if len(resets) == 0 && event.Kind() == EventCommandFinished && event.Depth() == 1 {
			firstImmediate = append(firstImmediate, event)
		}
	}
	if len(firstImmediate) != 3 || len(resets) != 3 {
		t.Fatalf("first immediate/reset counts = %d/%d; events %v", len(firstImmediate), len(resets), retryEventKinds(events))
	}
	for index := range resets {
		if resets[index].Depth() != 1 || resets[index].Sequence() != firstImmediate[index].Sequence() ||
			resets[index].RootRunID() != firstImmediate[index].RootRunID() {
			t.Fatalf("reset %d = %#v, want immediate result %#v", index, resets[index], firstImmediate[index])
		}
	}
	for _, event := range events {
		if event.Kind() == EventCommandReset && event.Depth() != 1 {
			t.Fatalf("retry reset deeper descendant = %#v", event)
		}
	}
}

func TestRetryLinkedDirectSkipStopsRetryWithoutMaskingLaterFailure(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/retry-mixed-root.yaml"
	childPath := "/workspace/retry-mixed-child.yaml"
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "retry-mixed-child.yaml", ResolvedPath: childPath,
	}
	skipped := NewCommandSkippedError("linked direct child skipped", nil)
	later := NewOperationError("linked later failure", nil)
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config:   model.Config{AppID: "com.example.retry-mixed"},
		Commands: []model.Command{retryFileCommand(int64(1), link)},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config:   model.Config{AppID: "com.example.retry-mixed-child"},
		Commands: []model.Command{retryActionCommand("skip"), retryActionCommand("later")},
	}
	actions := make([]string, 0, 2)
	registry, err := newHandlerRegistry(
		retryHandlerSpec(), retryActionHandlerSpec(func(name string, _ *executionState) error {
			actions = append(actions, name)
			if name == "skip" {
				return skipped
			}
			return later
		}),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, _ := compiled.Flow(rootPath)
	resets := 0
	dependencies := flowExecutorDependencies(&sessionRuntime{}, []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandReset {
			resets++
		}
		return nil
	})})
	result, executeErr := executeCompiledRoot(context.Background(), dependencies, root)
	if executeErr != later || result.ProductError() != later || result.Outcome() != Failed {
		t.Fatalf("linked mixed attempt = outcome %q returned %T %v product %T %v",
			result.Outcome(), executeErr, executeErr, result.ProductError(), result.ProductError())
	}
	if want := []string{"skip", "later"}; !reflect.DeepEqual(actions, want) || resets != 0 {
		t.Fatalf("linked actions = %#v resets %d, want one exact attempt and no reset", actions, resets)
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].Metadata().NumberOfRuns() != 1 ||
		commands[0].Metadata().Insight() != "" || commands[0].ProductError() != later ||
		commands[1].Outcome() != Skipped || commands[1].ProductError() != skipped ||
		commands[2].Outcome() != Failed || commands[2].ProductError() != later {
		t.Fatalf("linked mixed ledger = %#v", commands)
	}
}

func TestRetryLinkedDoesNotLeakSkippedGrandchildThroughCompletedComposite(t *testing.T) {
	t.Parallel()

	rootPath := "/workspace/retry-skip-root.yaml"
	childPath := "/workspace/retry-skip-child.yaml"
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "retry-skip-child.yaml", ResolvedPath: childPath,
	}
	skipped := NewCommandSkippedError("deep child skipped", nil)
	deep := retryActionCommand("deep-skipped")
	inline := model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"commands": []any{map[string]any{"action": "deep-skipped"}},
		},
		Children: []model.Command{deep},
		Source: model.SourceInfo{
			Path: childPath, Start: model.Position{Line: 5, Column: 3, Offset: 40},
		},
	}
	rootFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: rootPath,
		Config:   model.Config{AppID: "com.example.retry-skip"},
		Commands: []model.Command{retryFileCommand(int64(1), link)},
	}
	childFlow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: childPath,
		Config:   model.Config{AppID: "com.example.retry-skip-child"},
		Commands: []model.Command{inline},
	}
	actionCalls := 0
	registry, err := newHandlerRegistry(
		retryHandlerSpec(), runFlowHandlerSpec(),
		retryActionHandlerSpec(func(string, *executionState) error {
			actionCalls++
			return skipped
		}),
	)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := compileProgram(
		context.Background(), runFlowLinkedProgram(rootFlow, childFlow, link), registry,
	)
	if err != nil {
		t.Fatalf("compileProgram() error = %v", err)
	}
	root, _ := compiled.Flow(rootPath)
	result, err := executeCompiledRoot(
		context.Background(), flowExecutorDependencies(&sessionRuntime{}, nil), root,
	)
	if err != nil || result.Outcome() != Completed || actionCalls != 1 {
		t.Fatalf("encapsulated skip = outcome %q error %v action calls %d", result.Outcome(), err, actionCalls)
	}
	commands := result.Commands()
	if len(commands) != 3 || commands[0].Command().Kind != model.CommandRetry ||
		commands[0].Metadata().NumberOfRuns() != 1 || commands[0].Metadata().Insight() != "" ||
		commands[1].Command().Kind != model.CommandRunFlow || commands[1].Depth() != 1 || commands[1].Outcome() != Completed ||
		commands[2].Depth() != 2 || commands[2].Outcome() != Skipped || commands[2].ProductError() != skipped {
		t.Fatalf("encapsulated skip ledger = %#v", commands)
	}
}

func TestRetryTaxonomyRetriesOnlyProductAndAssertionFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		failure       error
		wantAttempts  int
		wantResets    int
		wantCompleted bool
		wantSkipped   bool
		wantMalformed bool
	}{
		{name: "operation", failure: NewOperationError("product", nil), wantAttempts: 2, wantResets: 1, wantCompleted: true},
		{name: "assertion", failure: NewAssertionError("assertion", nil), wantAttempts: 2, wantResets: 1, wantCompleted: true},
		{name: "cancelled", failure: context.Canceled, wantAttempts: 1},
		{name: "deadline", failure: context.DeadlineExceeded, wantAttempts: 1},
		{name: "device", failure: NewDeviceConnectionError("device", nil), wantAttempts: 1},
		{name: "configuration", failure: NewConfigurationError("configuration", nil), wantAttempts: 1},
		{name: "skipped", failure: NewCommandSkippedError("skipped", nil), wantAttempts: 1, wantSkipped: true},
		{name: "malformed", failure: &panickingAsError{}, wantAttempts: 1, wantMalformed: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			events := make([]Event, 0)
			dependencies := coreDependencies(
				enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
			)
			dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
				events = append(events, event)
				return nil
			})}
			core, err := newExecutorCore(
				dependencies, retryHandlerSpec(), retryActionHandlerSpec(func(string, *executionState) error {
					attempts++
					if attempts == 1 {
						return test.failure
					}
					return nil
				}),
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error = %v", err)
			}
			result, executeErr := core.execute(context.Background(), retryCommand(int64(1), "step"), 0)
			if attempts != test.wantAttempts || result.Metadata().NumberOfRuns() != test.wantAttempts {
				t.Fatalf("attempts = %d metadata %#v, want %d", attempts, result.Metadata(), test.wantAttempts)
			}
			resetCount := 0
			for _, event := range events {
				if event.Kind() == EventCommandReset {
					resetCount++
				}
			}
			if resetCount != test.wantResets {
				t.Fatalf("reset count = %d, want %d; events %v", resetCount, test.wantResets, retryEventKinds(events))
			}
			switch {
			case test.wantCompleted:
				if executeErr != nil || result.Outcome() != Completed || result.Metadata().Insight() != retrySuccessInsight {
					t.Fatalf("retryable result = outcome %q error %v metadata %#v", result.Outcome(), executeErr, result.Metadata())
				}
			case test.wantSkipped:
				if executeErr != nil || result.Outcome() != Skipped || result.ProductError() != test.failure {
					t.Fatalf("skipped result = outcome %q returned %v product %T %v", result.Outcome(), executeErr, result.ProductError(), result.ProductError())
				}
			case test.wantMalformed:
				var configuration *ConfigurationError
				if !errors.As(result.ProductError(), &configuration) || executeErr != result.ProductError() {
					t.Fatalf("malformed result = returned %T %v product %T %v", executeErr, executeErr, result.ProductError(), result.ProductError())
				}
			default:
				if executeErr != test.failure || result.ProductError() != test.failure || result.Metadata().Insight() != "" {
					t.Fatalf("terminal identity = returned %T %v product %T %v", executeErr, executeErr, result.ProductError(), result.ProductError())
				}
			}
		})
	}
}

func TestRetryMetadataCancellationDominatesRetryableAttemptBeforeReset(t *testing.T) {
	t.Parallel()

	t.Run("pre-attempt cancellation does not count a phantom run", func(t *testing.T) {
		t.Parallel()
		ctx := &retryMutableContext{Context: context.Background(), err: context.Canceled}
		child := retryActionCommand("step")
		attempts := 0
		state := &executionState{
			compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
			executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
				attempts++
				return nil, nil
			},
		}
		effect, err := executeRetry(ctx, state, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
			value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
		})
		if err != context.Canceled || attempts != 0 || effect.numberOfRuns != 0 || !effect.numberOfRunsSet {
			t.Fatalf("pre-attempt cancellation = effect %#v error %T %v attempts %d", effect, err, err, attempts)
		}
	})

	for _, terminal := range []error{context.Canceled, context.DeadlineExceeded} {
		terminal := terminal
		t.Run(terminal.Error(), func(t *testing.T) {
			t.Parallel()
			ctx := &retryMutableContext{Context: context.Background()}
			child := retryActionCommand("step")
			attemptFailure := NewOperationError("retryable", nil)
			attempts := 0
			resets := 0
			state := &executionState{
				compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
				executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
					attempts++
					return []CommandResult{{depth: 1}}, attemptFailure
				},
				metadataUpdatedFn: func(context.Context, CommandMetadata) error {
					ctx.err = terminal
					return nil
				},
				commandResetFn: func(context.Context, CommandResult) error {
					resets++
					return nil
				},
			}
			effect, err := executeRetry(ctx, state, evaluatedDispatch{
				command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
				value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
			})
			if err != terminal || attempts != 1 || resets != 0 || effect.numberOfRuns != 1 || effect.insight != "" {
				t.Fatalf("metadata cancellation = effect %#v error %T %v attempts %d resets %d", effect, err, err, attempts, resets)
			}
		})
	}
}

func TestRetryResetCancellationStopsAfterFirstResetAndDominatesResetError(t *testing.T) {
	t.Parallel()

	t.Run("listener cancellation after first exact reset", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		events := make([]Event, 0)
		dependencies := coreDependencies(
			enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
		)
		dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			if event.Kind() == EventCommandReset {
				cancel()
			}
			return nil
		})}
		actions := 0
		failure := NewOperationError("second child failed", nil)
		core, err := newExecutorCore(
			dependencies, retryHandlerSpec(), retryActionHandlerSpec(func(string, *executionState) error {
				actions++
				if actions == 2 {
					return failure
				}
				return nil
			}),
		)
		if err != nil {
			t.Fatalf("newExecutorCore() error = %v", err)
		}
		result, err := core.execute(ctx, retryCommand(int64(1), "first", "second", "third"), 0)
		if err != context.Canceled || result.ProductError() != context.Canceled || result.Outcome() != Cancelled ||
			result.Metadata().NumberOfRuns() != 1 || actions != 2 {
			t.Fatalf("reset cancellation = result %#v error %T %v actions %d", result, err, err, actions)
		}
		resets := 0
		childStarts := 0
		for _, event := range events {
			if event.Kind() == EventCommandReset {
				resets++
			}
			if event.Kind() == EventCommandStarted && event.Depth() == 1 {
				childStarts++
			}
		}
		if resets != 1 || childStarts != 2 {
			t.Fatalf("reset/start counts = %d/%d, want 1/2; events %v", resets, childStarts, retryEventKinds(events))
		}
	})

	t.Run("terminal context dominates simultaneous reset error", func(t *testing.T) {
		t.Parallel()
		ctx := &retryMutableContext{Context: context.Background()}
		child := retryActionCommand("step")
		attemptFailure := NewOperationError("retryable", nil)
		resetFailure := NewConfigurationError("reset failed", nil)
		state := &executionState{
			compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
			executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
				return []CommandResult{{depth: 1}}, attemptFailure
			},
			metadataUpdatedFn: func(context.Context, CommandMetadata) error { return nil },
			commandResetFn: func(context.Context, CommandResult) error {
				ctx.err = context.Canceled
				return resetFailure
			},
		}
		effect, err := executeRetry(ctx, state, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
			value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
		})
		if err != context.Canceled || effect.numberOfRuns != 1 {
			t.Fatalf("simultaneous reset failure = effect %#v error %T %v", effect, err, err)
		}
	})
}

func TestRetryBookkeepingFailurePrecedenceIsFailClosedAndIdentityPreserving(t *testing.T) {
	t.Parallel()

	child := retryActionCommand("step")
	attemptFailure := NewOperationError("attempt failed", nil)
	metadataFailure := NewConfigurationError("metadata failed", nil)
	resetFailure := NewConfigurationError("reset failed", nil)

	t.Run("attempt error beats same-attempt metadata failure", func(t *testing.T) {
		t.Parallel()
		resets := 0
		state := &executionState{
			compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
			executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
				return []CommandResult{{depth: 1}}, attemptFailure
			},
			metadataUpdatedFn: func(context.Context, CommandMetadata) error { return metadataFailure },
			commandResetFn: func(context.Context, CommandResult) error {
				resets++
				return nil
			},
		}
		effect, err := executeRetry(context.Background(), state, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
			value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
		})
		if err != attemptFailure || effect.numberOfRuns != 1 || resets != 0 {
			t.Fatalf("precedence = effect %#v error %T %v resets %d", effect, err, err, resets)
		}
	})

	t.Run("successful retry metadata failure clears final insight", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		updates := 0
		seenSuccessInsight := ""
		state := &executionState{
			compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
			executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
				attempts++
				if attempts == 1 {
					return []CommandResult{{depth: 1}}, attemptFailure
				}
				return []CommandResult{{depth: 1}}, nil
			},
			metadataUpdatedFn: func(_ context.Context, metadata CommandMetadata) error {
				updates++
				if updates == 2 {
					seenSuccessInsight = metadata.Insight()
					return metadataFailure
				}
				return nil
			},
			commandResetFn: func(context.Context, CommandResult) error { return nil },
		}
		effect, err := executeRetry(context.Background(), state, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
			value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
		})
		if err != metadataFailure || effect.numberOfRuns != 2 || effect.insight != "" ||
			seenSuccessInsight != retrySuccessInsight {
			t.Fatalf("success bookkeeping = effect %#v error %T %v update insight %q", effect, err, err, seenSuccessInsight)
		}
	})

	t.Run("reset failure surfaces before another attempt", func(t *testing.T) {
		t.Parallel()
		attempts := 0
		resets := 0
		state := &executionState{
			compiledChildren: []compiledDispatch{{command: cloneCommand(child)}},
			executeCompiledSequence: func(context.Context, []compiledDispatch, int) ([]CommandResult, error) {
				attempts++
				return []CommandResult{{depth: 1}}, attemptFailure
			},
			metadataUpdatedFn: func(context.Context, CommandMetadata) error { return nil },
			commandResetFn: func(context.Context, CommandResult) error {
				resets++
				return resetFailure
			},
		}
		effect, err := executeRetry(context.Background(), state, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry, Children: []model.Command{child}},
			value:   retryEvaluated{source: retryInlineSource, maxRetries: 1},
		})
		if err != resetFailure || attempts != 1 || resets != 1 || effect.numberOfRuns != 1 {
			t.Fatalf("reset failure = effect %#v error %T %v attempts/resets %d/%d", effect, err, err, attempts, resets)
		}
	})
}

func TestRetryRootPolicyFinalizesOnlyOuterFailureAndNeverResolvesSuccess(t *testing.T) {
	t.Parallel()

	ordinary := NewOperationError("ordinary child failure", nil)
	deviceFailure := NewDeviceConnectionError("device disconnected", nil)
	tests := []struct {
		name              string
		failure           error
		failuresBefore    int
		wantResolverCalls int
		wantAfter         bool
		wantError         error
	}{
		{name: "exhausted product reaches outer resolver", failure: ordinary, failuresBefore: 2, wantResolverCalls: 1, wantAfter: true, wantError: ordinary},
		{name: "eventual success never resolves", failure: ordinary, failuresBefore: 1, wantAfter: true},
		{name: "cancellation bypasses resolver", failure: context.Canceled, failuresBefore: 1, wantError: context.Canceled},
		{name: "device bypasses resolver", failure: deviceFailure, failuresBefore: 1, wantError: deviceFailure},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			childAttempts := 0
			order := make([]string, 0, 4)
			actionSpec := retryActionHandlerSpec(func(name string, _ *executionState) error {
				order = append(order, name)
				if name == "child" {
					childAttempts++
					if childAttempts <= test.failuresBefore {
						return test.failure
					}
				}
				return nil
			})
			registry, err := newHandlerRegistry(retryHandlerSpec(), actionSpec)
			if err != nil {
				t.Fatalf("newHandlerRegistry() error = %v", err)
			}
			dispatcher := newDispatcher(registry)
			retryDispatch, err := dispatcher.compile(
				context.Background(), compileContext{}, retryCommand(int64(1), "child"),
			)
			if err != nil {
				t.Fatalf("compile(retry) error = %v", err)
			}
			afterDispatch, err := dispatcher.compile(context.Background(), compileContext{}, retryActionCommand("after"))
			if err != nil {
				t.Fatalf("compile(after) error = %v", err)
			}
			resolverCalls := 0
			dependencies := flowExecutorDependencies(&sessionRuntime{}, nil)
			dependencies.FailureResolver = FailureResolverFunc(func(_ context.Context, result CommandResult) FailureDecision {
				resolverCalls++
				if result.Command().Kind != model.CommandRetry || result.Depth() != 0 || result.ProductError() != test.wantError {
					t.Fatalf("resolver received non-outer result %#v", result)
				}
				return FailureDecisionContinue
			})
			result, executeErr := executeCompiledRoot(context.Background(), dependencies, &compiledFlow{
				path: "/workspace/retry-policy.yaml", config: model.Config{AppID: "com.example.retry"},
				body: []compiledDispatch{retryDispatch, afterDispatch},
			})
			if executeErr != test.wantError || result.ProductError() != test.wantError || resolverCalls != test.wantResolverCalls {
				t.Fatalf("root policy = returned %T %v product %T %v resolver calls %d",
					executeErr, executeErr, result.ProductError(), result.ProductError(), resolverCalls)
			}
			wantOrder := make([]string, test.failuresBefore)
			for index := range wantOrder {
				wantOrder[index] = "child"
			}
			if test.failuresBefore == 1 && test.wantError == nil {
				wantOrder = append(wantOrder, "child")
			}
			if test.wantAfter {
				wantOrder = append(wantOrder, "after")
			}
			if !reflect.DeepEqual(order, wantOrder) {
				t.Fatalf("order = %#v, want %#v", order, wantOrder)
			}
			commands := result.Commands()
			if len(commands) < 2 || commands[0].Command().Kind != model.CommandRetry || commands[1].Depth() != 1 {
				t.Fatalf("root ledger = %#v", commands)
			}
		})
	}
}

func TestRetryScopedLogsStayWithAttemptsAndRestoreHostSink(t *testing.T) {
	t.Parallel()

	hostLogs := make([]string, 0, 3)
	factory, err := js.NewFactory(js.Config{
		Random: rand.New(rand.NewSource(83)),
		LogSink: func(message string) {
			hostLogs = append(hostLogs, message)
		},
	})
	if err != nil {
		t.Fatalf("js.NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("factory.NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	finishedChildren := make([]Event, 0, 2)
	dependencies := coreDependencies(
		enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)),
	)
	dependencies.Listeners = []Listener{ListenerFunc(func(_ context.Context, event Event) error {
		if event.Kind() == EventCommandFinished && event.Depth() == 1 {
			finishedChildren = append(finishedChildren, event)
		}
		return nil
	})}
	attempt := 0
	core, err := newExecutorCore(
		dependencies, retryHandlerSpec(), retryActionHandlerSpec(func(_ string, state *executionState) error {
			attempt++
			active, runtimeErr := state.jsRuntime()
			if runtimeErr != nil {
				return runtimeErr
			}
			if _, evalErr := active.Evaluate(context.Background(), js.EvalRequest{
				Script: fmt.Sprintf("console.log('attempt-%d')", attempt),
			}); evalErr != nil {
				return evalErr
			}
			if attempt == 1 {
				return NewOperationError("retryable", nil)
			}
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("newExecutorCore() error = %v", err)
	}
	core.state.runtimeFn = func() (js.Runtime, error) { return runtime, nil }
	source := retryCommand(int64(1), "step")
	result, err := core.execute(context.Background(), source, 0)
	if err != nil || result.Metadata().Insight() != retrySuccessInsight || len(result.Metadata().LogMessages()) != 0 {
		t.Fatalf("retry logs result = %#v error %v", result, err)
	}
	if len(finishedChildren) != 2 ||
		!reflect.DeepEqual(finishedChildren[0].Metadata().LogMessages(), []string{"attempt-1"}) ||
		!reflect.DeepEqual(finishedChildren[1].Metadata().LogMessages(), []string{"attempt-2"}) {
		t.Fatalf("child logs = %#v", finishedChildren)
	}
	if !reflect.DeepEqual(hostLogs, []string{"attempt-1", "attempt-2"}) {
		t.Fatalf("host logs = %#v", hostLogs)
	}
	if _, err := runtime.Evaluate(context.Background(), js.EvalRequest{Script: "console.log('after')"}); err != nil {
		t.Fatalf("post-retry log error = %v", err)
	}
	if !reflect.DeepEqual(hostLogs, []string{"attempt-1", "attempt-2", "after"}) {
		t.Fatalf("restored host logs = %#v", hostLogs)
	}
	if source.Arguments.(map[string]any)["maxRetries"] != int64(1) {
		t.Fatalf("source command mutated = %#v", source)
	}
}

func TestRetryMalformedRuntimeBoundariesFailClosedAfterAtomicRegistration(t *testing.T) {
	t.Parallel()

	if _, err := evaluateRetry(
		context.Background(), evaluationContext{}, model.Command{Kind: model.CommandRetry}, struct{}{},
	); err == nil {
		t.Fatal("evaluateRetry accepted foreign compiled payload")
	}
	invalidCompiled := []retryCompiled{
		{source: retryLinkedSource, counterKind: retryCounterMissing},
		{source: retryInlineSource, linked: &compiledFlow{}, counterKind: retryCounterMissing},
		{source: retryInlineSource, counterKind: 99},
		{source: retryInlineSource, counterKind: retryCounterFixed, fixedMaxRetries: 4},
	}
	for _, payload := range invalidCompiled {
		if _, err := evaluateRetry(
			context.Background(), evaluationContext{}, model.Command{Kind: model.CommandRetry}, payload,
		); err == nil {
			t.Fatalf("evaluateRetry accepted invalid compiled payload %#v", payload)
		}
	}
	if _, err := executeRetry(context.Background(), &executionState{}, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRetry}, value: struct{}{},
	}); err == nil {
		t.Fatal("executeRetry accepted foreign evaluated payload")
	}
	invalidEvaluated := []retryEvaluated{
		{source: retryLinkedSource, maxRetries: 1},
		{source: retryInlineSource, linked: &compiledFlow{}, maxRetries: 1},
		{source: retryInlineSource, maxRetries: -1},
		{source: retryInlineSource, maxRetries: 4},
	}
	for _, payload := range invalidEvaluated {
		effect, err := executeRetry(context.Background(), &executionState{}, evaluatedDispatch{
			command: model.Command{Kind: model.CommandRetry}, value: payload,
		})
		if err == nil || !effect.numberOfRunsSet || effect.numberOfRuns != 0 {
			t.Fatalf("executeRetry invalid payload = effect %#v error %v", effect, err)
		}
	}
	if effect, err := executeRetry(nil, &executionState{}, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRetry},
		value:   retryEvaluated{source: retryInlineSource, maxRetries: 0},
	}); err == nil || !effect.numberOfRunsSet || effect.numberOfRuns != 0 {
		t.Fatalf("nil context = effect %#v error %v", effect, err)
	}
	if effect, err := executeRetry(context.Background(), nil, evaluatedDispatch{
		command: model.Command{Kind: model.CommandRetry},
		value:   retryEvaluated{source: retryInlineSource, maxRetries: 0},
	}); err == nil || !effect.numberOfRunsSet || effect.numberOfRuns != 0 {
		t.Fatalf("nil state = effect %#v error %v", effect, err)
	}

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	if len(registry.byKeyword) != len(productionKeywords()) {
		t.Fatalf("production registry size = %d, want the complete set", len(registry.byKeyword))
	}
	if _, exists := registry.lookup(model.CommandRetry); !exists {
		t.Fatal("retry missing after atomic control-flow registration")
	}
}

func retryCommand(maxRetries any, actions ...string) model.Command {
	rawChildren := make([]any, len(actions))
	children := make([]model.Command, len(actions))
	for index, action := range actions {
		rawChildren[index] = map[string]any{"action": action}
		children[index] = retryActionCommand(action)
	}
	arguments := map[string]any{"commands": rawChildren}
	if maxRetries != nil {
		arguments["maxRetries"] = maxRetries
	}
	return model.Command{
		Kind: model.CommandRetry, Form: model.CommandFormObject,
		Arguments: arguments, Children: children,
	}
}

func retryFileCommand(maxRetries any, link model.FileLink) model.Command {
	arguments := map[string]any{"file": link.Path}
	if maxRetries != nil {
		arguments["maxRetries"] = maxRetries
	}
	return model.Command{
		Kind: model.CommandRetry, Form: model.CommandFormObject,
		Arguments: arguments, Links: []model.FileLink{link},
	}
}

func retryEventKinds(events []Event) []EventKind {
	kinds := make([]EventKind, len(events))
	for index := range events {
		kinds[index] = events[index].Kind()
	}
	return kinds
}

func retryEventIndex(events []Event, kind EventKind, occurrence int) int {
	seen := 0
	for index, event := range events {
		if event.Kind() != kind {
			continue
		}
		if seen == occurrence {
			return index
		}
		seen++
	}
	return -1
}

func retryEventIndexAtDepth(events []Event, kind EventKind, depth, occurrence int) int {
	seen := 0
	for index, event := range events {
		if event.Kind() != kind || event.Depth() != depth {
			continue
		}
		if seen == occurrence {
			return index
		}
		seen++
	}
	return -1
}

type retryMutableContext struct {
	context.Context
	err error
}

func (ctx *retryMutableContext) Err() error { return ctx.err }

func retryActionCommand(name string) model.Command {
	return model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: name}
}

func retryActionHandlerSpec(execute func(string, *executionState) error) handlerSpec {
	return handlerSpec{
		keyword: model.CommandAction, effectClass: EffectHostMutation,
		compile: pureCompiler(func(command model.Command) (any, error) {
			return decodeString(command)
		}),
		evaluate: identityEvaluator,
		execute: func(_ context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
			name, ok := evaluated.value.(string)
			if !ok {
				return commandEffect{}, NewConfigurationError("retry test action payload is invalid", nil)
			}
			if execute != nil {
				if err := execute(name, state); err != nil {
					return commandEffect{effectClass: EffectHostMutation}, err
				}
			}
			return commandEffect{effectClass: EffectHostMutation}, nil
		},
	}
}
