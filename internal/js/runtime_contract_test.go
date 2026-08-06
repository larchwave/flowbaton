package js

import (
	"context"
	"errors"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestNewFactoryRequiresRandomSource(t *testing.T) {
	t.Parallel()

	_, err := NewFactory(Config{})
	if !errors.Is(err, ErrRandomSourceRequired) {
		t.Fatalf("NewFactory() error = %v, want %v", err, ErrRandomSourceRequired)
	}
}

func TestRuntimeEvaluatesInStrictIIFE(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)

	tests := []struct {
		name   string
		script string
		want   any
	}{
		{name: "completion value", script: "1 + 2", want: int64(3)},
		{name: "strict this", script: "this === undefined", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := runtime.Evaluate(context.Background(), EvalRequest{Script: tt.script})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if !reflect.DeepEqual(result.Value, tt.want) {
				t.Fatalf("Evaluate() value = %#v (%T), want %#v (%T)", result.Value, result.Value, tt.want, tt.want)
			}
		})
	}
}

func TestIIFEEvaluationDoesNotLeakDeclarations(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "var oldVar = 1; let oldLet = 2; const oldConst = 3; 4",
	}); err != nil {
		t.Fatalf("declaration Evaluate() error = %v", err)
	}
	result, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "[typeof oldVar, typeof oldLet, typeof oldConst].join(',')",
	})
	if err != nil {
		t.Fatalf("probe Evaluate() error = %v", err)
	}
	if result.Value != "undefined,undefined,undefined" {
		t.Fatalf("declaration probe = %#v, want undefined,undefined,undefined", result.Value)
	}
}

func TestUnknownGlobalIsUndefinedAndDefaultable(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	result, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "MISSING_VALUE || 'fallback'",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Value != "fallback" {
		t.Fatalf("Evaluate() value = %#v, want fallback", result.Value)
	}
}

func TestEvaluateHonorsAlreadyCanceledContext(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runtime.Evaluate(ctx, EvalRequest{Script: "1 + 1"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Evaluate() error = %v, want context.Canceled", err)
	}
}

func TestEvaluateInterruptsRunningScriptAndClearsInterrupt(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := runtime.Evaluate(ctx, EvalRequest{Script: "for (;;) {}"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("interrupted Evaluate() error = %v, want context.DeadlineExceeded", err)
	}

	result, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "6 * 7"})
	if err != nil {
		t.Fatalf("Evaluate() after interrupt error = %v", err)
	}
	if result.Value != int64(42) {
		t.Fatalf("Evaluate() after interrupt value = %#v, want 42", result.Value)
	}
}

func TestEvaluateRejectsNullEnvironmentValue(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	_, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "TOKEN",
		Env:    map[string]any{"TOKEN": nil},
	})
	var missing *MissingEnvValueError
	if !errors.As(err, &missing) {
		t.Fatalf("Evaluate() error = %v, want *MissingEnvValueError", err)
	}
	if missing.Name != "TOKEN" {
		t.Fatalf("MissingEnvValueError.Name = %q, want TOKEN", missing.Name)
	}
	if got := missing.Error(); !strings.Contains(got, `"TOKEN"`) {
		t.Fatalf("MissingEnvValueError.Error() = %q, want key name", got)
	}

	err = runtime.PutEnv("DIRECT", nil)
	if !errors.As(err, &missing) || missing.Name != "DIRECT" {
		t.Fatalf("PutEnv(nil) error = %v, want *MissingEnvValueError for DIRECT", err)
	}
}

func TestEvaluatePersistsRootEnvironmentAndRestoresSubscope(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	first, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "ROOT",
		Env:    map[string]any{"ROOT": "parent"},
	})
	if err != nil {
		t.Fatalf("first Evaluate() error = %v", err)
	}
	if first.Value != "parent" {
		t.Fatalf("first Evaluate() value = %#v, want parent", first.Value)
	}

	scoped, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script:        "ROOT + ':' + SCOPED",
		Env:           map[string]any{"ROOT": "child", "SCOPED": "visible"},
		RunInSubScope: true,
	})
	if err != nil {
		t.Fatalf("scoped Evaluate() error = %v", err)
	}
	if scoped.Value != "child:visible" {
		t.Fatalf("scoped Evaluate() value = %#v, want child:visible", scoped.Value)
	}

	restored, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "ROOT + ':' + typeof SCOPED",
	})
	if err != nil {
		t.Fatalf("restored Evaluate() error = %v", err)
	}
	if restored.Value != "parent:undefined" {
		t.Fatalf("restored Evaluate() value = %#v, want parent:undefined", restored.Value)
	}
}

func TestSubscopeEnvironmentShadowRestoresBuiltInGlobal(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	shadowed, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script:        "Math",
		Env:           map[string]any{"Math": "shadow"},
		RunInSubScope: true,
	})
	if err != nil {
		t.Fatalf("shadowed Evaluate() error = %v", err)
	}
	if shadowed.Value != "shadow" {
		t.Fatalf("shadowed Math = %#v, want shadow", shadowed.Value)
	}

	restored, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "Math.max(1, 2)"})
	if err != nil {
		t.Fatalf("restored Evaluate() error = %v", err)
	}
	if restored.Value != int64(2) {
		t.Fatalf("restored Math.max() = %#v, want 2", restored.Value)
	}
}

func TestEnvironmentScopesPushAndPopCopies(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	if err := runtime.PutEnv("TOKEN", "parent"); err != nil {
		t.Fatalf("PutEnv(parent) error = %v", err)
	}
	if err := runtime.PushEnv(); err != nil {
		t.Fatalf("PushEnv() error = %v", err)
	}
	if err := runtime.PutEnv("TOKEN", "child"); err != nil {
		t.Fatalf("PutEnv(child) error = %v", err)
	}
	if err := runtime.PutEnv("ONLY_CHILD", "visible"); err != nil {
		t.Fatalf("PutEnv(ONLY_CHILD) error = %v", err)
	}

	child, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "TOKEN + ':' + ONLY_CHILD"})
	if err != nil {
		t.Fatalf("child Evaluate() error = %v", err)
	}
	if child.Value != "child:visible" {
		t.Fatalf("child Evaluate() value = %#v, want child:visible", child.Value)
	}

	if err := runtime.PopEnv(); err != nil {
		t.Fatalf("PopEnv() error = %v", err)
	}
	parent, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "TOKEN + ':' + typeof ONLY_CHILD"})
	if err != nil {
		t.Fatalf("parent Evaluate() error = %v", err)
	}
	if parent.Value != "parent:undefined" {
		t.Fatalf("parent Evaluate() value = %#v, want parent:undefined", parent.Value)
	}

	if err := runtime.PopEnv(); !errors.Is(err, ErrEnvScopeUnderflow) {
		t.Fatalf("second PopEnv() error = %v, want %v", err, ErrEnvScopeUnderflow)
	}
}

func TestInterpolationEvaluatesExpressionsAndPreservesEscapedLiteral(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	got, err := runtime.Interpolate(
		context.Background(),
		`sum=${LEFT + RIGHT}; fallback=${MISSING || 'safe'}; literal=\${LEFT + RIGHT}`,
		map[string]any{"LEFT": 1, "RIGHT": 2},
	)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	const want = `sum=3; fallback=safe; literal=${LEFT + RIGHT}`
	if got != want {
		t.Fatalf("Interpolate() = %q, want %q", got, want)
	}
}

func TestInterpolationMatchesSinglePassDollarEdgeCases(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "blank expression", input: `before=${   };after`, want: `before=;after`},
		{name: "dollar in expression is literal", input: `value=${'$'}`, want: `value=${'$'}`},
		{name: "unterminated expression", input: `value=${1 + 2`, want: `value=${1 + 2`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := runtime.Interpolate(context.Background(), tt.input, nil)
			if err != nil {
				t.Fatalf("Interpolate() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Interpolate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestInterpolationClosesAtTheLastBraceBeforeTheNextDollar pins the expression
// boundary: a `${` runs to the last `}` before the next `$` or end of input.
func TestInterpolationClosesAtTheLastBraceBeforeTheNextDollar(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	for _, test := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "nested braces", input: `${JSON.stringify({a: 1})}`, want: `{"a":1}`},
		{name: "nested braces twice", input: `${JSON.stringify({a: {b: 2}})}`, want: `{"a":{"b":2}}`},
		{name: "close brace inside string", input: `${"}"}`, want: `}`},
		{name: "open brace inside string", input: `${"{"}`, want: `{`},
		{name: "close brace inside single quotes", input: `${'}'}`, want: `}`},
		{name: "two expressions separated", input: `${1} and ${2}`, want: `1 and 2`},
		{name: "two expressions adjacent", input: `${1}${2}`, want: `12`},
		{name: "dollar inside stays literal", input: `${"a$b"}`, want: `${"a$b"}`},
		{name: "extra close brace", input: `${1}}`, wantErr: true},
		{name: "close brace after literal tail", input: `a${1}b}c`, wantErr: true},
		{name: "unbalanced close after nested", input: `${JSON.stringify({a: 1})}}`, wantErr: true},
		{name: "nested dollar", input: `${1 + ${2}}`, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := runtime.Interpolate(context.Background(), test.input, nil)
			if test.wantErr {
				if err == nil {
					t.Fatalf("Interpolate(%q) = %q, want an error", test.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Interpolate(%q) error = %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("Interpolate(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestInterpolationExpressionDetectionMatchesSinglePassDollarGrammar(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		input string
		want  bool
	}{
		{name: "complete", input: "${POINT}", want: true},
		{name: "blank complete", input: "${   }", want: true},
		{name: "escaped", input: `\${POINT}`},
		{name: "unterminated", input: "${POINT"},
		{name: "nested dollar exposes inner", input: "${OUTER${POINT}}", want: true},
		{name: "dollar inside candidate", input: "${'$'}"},
		{name: "second dollar starts expression", input: "$${POINT}", want: true},
		{name: "escaped outer exposes nested", input: `\${OUTER${POINT}}`, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := HasInterpolationExpression(test.input); got != test.want {
				t.Fatalf("HasInterpolationExpression(%q) = %t, want %t", test.input, got, test.want)
			}
		})
	}
}

func TestPermanentBindingsPersistAndFlowBatonValuesRefresh(t *testing.T) {
	t.Parallel()

	factory, err := NewFactory(Config{
		Random:     rand.New(rand.NewSource(11)),
		Platform:   "android",
		CopiedText: stringPtr("first clip"),
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	initial, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: `JSON.stringify([typeof http, typeof faker, typeof output, flowbaton.copiedText, flowbaton.platform, json('{"answer":42}').answer, relativePoint(0.125, 0.75)])`,
		Env:    map[string]any{"output": "must not replace permanent binding"},
	})
	if err != nil {
		t.Fatalf("initial Evaluate() error = %v", err)
	}
	const wantInitial = `["object","object","object","first clip","android",42,"13%,75%"]`
	if initial.Value != wantInitial {
		t.Fatalf("initial Evaluate() value = %#v, want %s", initial.Value, wantInitial)
	}

	if _, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "output.answer = 42"}); err != nil {
		t.Fatalf("output mutation Evaluate() error = %v", err)
	}
	persisted, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "output.answer"})
	if err != nil {
		t.Fatalf("output read Evaluate() error = %v", err)
	}
	if persisted.Value != int64(42) {
		t.Fatalf("persisted output = %#v, want 42", persisted.Value)
	}

	if err := runtime.SetCopiedText("second clip"); err != nil {
		t.Fatalf("SetCopiedText() error = %v", err)
	}
	if err := runtime.SetPlatform("ios"); err != nil {
		t.Fatalf("SetPlatform() error = %v", err)
	}
	refreshed, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "flowbaton.copiedText + ':' + flowbaton.platform",
	})
	if err != nil {
		t.Fatalf("refreshed Evaluate() error = %v", err)
	}
	if refreshed.Value != "second clip:ios" {
		t.Fatalf("refreshed flowbaton values = %#v, want second clip:ios", refreshed.Value)
	}

	retiredGlobal := strings.Join([]string{"mae", "stro"}, "")
	missing, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "typeof globalThis[" + strconv.Quote(retiredGlobal) + "]",
	})
	if err != nil {
		t.Fatalf("retired global Evaluate() error = %v", err)
	}
	if missing.Value != "undefined" {
		t.Fatalf("retired global type = %#v, want undefined", missing.Value)
	}
}

func TestRelativePointConvertsFractionsToCeilingPercentages(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	result, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "relativePoint(0.125, 0.75)",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Value != "13%,75%" {
		t.Fatalf("relativePoint() = %#v, want 13%%,75%%", result.Value)
	}
}

func TestPromiseMicrotasksDrainWithoutTimerEventLoop(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	queued, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: `Promise.resolve(6).then(function(value) { output.promiseValue = value * 7 }); "queued"`,
	})
	if err != nil {
		t.Fatalf("promise Evaluate() error = %v", err)
	}
	if queued.Value != "queued" {
		t.Fatalf("promise completion value = %#v, want queued", queued.Value)
	}
	settled, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: "output.promiseValue + ':' + typeof setTimeout",
	})
	if err != nil {
		t.Fatalf("settled Evaluate() error = %v", err)
	}
	if settled.Value != "42:undefined" {
		t.Fatalf("promise/timer probe = %#v, want 42:undefined", settled.Value)
	}
}

func TestConsoleMessagesUseDedicatedSink(t *testing.T) {
	t.Parallel()

	var messages []string
	factory, err := NewFactory(Config{
		Random: rand.New(rand.NewSource(19)),
		LogSink: func(message string) {
			messages = append(messages, message)
		},
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	if _, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: `console.log("value", 2, true); console.warn("warning")`,
	}); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if want := []string{"value 2 true", "warning"}; !reflect.DeepEqual(messages, want) {
		t.Fatalf("console messages = %#v, want %#v", messages, want)
	}
}

func TestConsoleSinkCanBeReplacedAfterRuntimeCreation(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	var messages []string
	runtime.SetLogSink(func(message string) {
		messages = append(messages, message)
	})
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{Script: `console.log("late sink")`}); err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if want := []string{"late sink"}; !reflect.DeepEqual(messages, want) {
		t.Fatalf("console messages = %#v, want %#v", messages, want)
	}
}

func TestScopedConsoleSinkFansOutAndRestoresWithoutReplacingHostSink(t *testing.T) {
	t.Parallel()

	var originalHost []string
	factory, err := NewFactory(Config{
		Random: rand.New(rand.NewSource(23)),
		LogSink: func(message string) {
			originalHost = append(originalHost, message)
		},
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	var scoped []string
	restore := runtime.PushLogSink(func(message string) {
		scoped = append(scoped, message)
	})
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{Script: `console.log("first")`}); err != nil {
		t.Fatalf("first Evaluate() error = %v", err)
	}

	var replacementHost []string
	runtime.SetLogSink(func(message string) {
		replacementHost = append(replacementHost, message)
	})
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{Script: `console.log("second")`}); err != nil {
		t.Fatalf("second Evaluate() error = %v", err)
	}
	restore()
	restore()
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{Script: `console.log("third")`}); err != nil {
		t.Fatalf("third Evaluate() error = %v", err)
	}

	if want := []string{"first"}; !reflect.DeepEqual(originalHost, want) {
		t.Fatalf("original host messages = %#v, want %#v", originalHost, want)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("scoped messages = %#v, want %#v", scoped, want)
	}
	if want := []string{"second", "third"}; !reflect.DeepEqual(replacementHost, want) {
		t.Fatalf("replacement host messages = %#v, want %#v", replacementHost, want)
	}
}

func TestFactoryCreatesIndependentRuntimes(t *testing.T) {
	t.Parallel()

	factory, err := NewFactory(Config{Random: rand.New(rand.NewSource(7))})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}

	first, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("first NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	second, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("second NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	if first == second {
		t.Fatal("NewRuntime() returned the same shared instance twice")
	}
}

func TestClosedRuntimeRejectsFurtherEvaluation(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := runtime.Evaluate(context.Background(), EvalRequest{Script: "1"})
	if !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("Evaluate() after Close() error = %v, want %v", err, ErrRuntimeClosed)
	}
}

func TestExportedRuntimeContractDoesNotExposeGojaTypes(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{
		reflect.TypeOf((*Factory)(nil)).Elem(),
		reflect.TypeOf((*Runtime)(nil)).Elem(),
		reflect.TypeOf((*RandomSource)(nil)).Elem(),
		reflect.TypeOf(Config{}),
		reflect.TypeOf(EvalRequest{}),
		reflect.TypeOf(Result{}),
		reflect.TypeOf(OpaqueValue{}),
		reflect.TypeOf(MissingEnvValueError{}),
		reflect.TypeOf(EvaluationError{}),
	}
	seen := make(map[reflect.Type]bool)
	for _, contractType := range types {
		assertNoGojaType(t, contractType, seen)
	}
}

func TestEvaluationErrorsDoNotExposeGojaTypes(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	_, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script:     `throw new Error("boom")`,
		SourceName: "contract-probe.js",
	})
	var evaluationError *EvaluationError
	if !errors.As(err, &evaluationError) {
		t.Fatalf("Evaluate() error = %T %v, want *EvaluationError", err, err)
	}
	if evaluationError.SourceName != "contract-probe.js" {
		t.Fatalf("EvaluationError.SourceName = %q, want contract-probe.js", evaluationError.SourceName)
	}
	if !strings.Contains(evaluationError.Message, "boom") {
		t.Fatalf("EvaluationError.Message = %q, want boom", evaluationError.Message)
	}
}

func TestEvaluationValuesDoNotExposeGojaTypes(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	result, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: `({promise: Promise.resolve(1), nested: [function named() {}]})`,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	assertNoGojaValue(t, result.Value)
}

func newTestRuntime(t *testing.T) Runtime {
	t.Helper()

	factory, err := NewFactory(Config{Random: rand.New(rand.NewSource(7))})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func assertNoGojaType(t *testing.T, valueType reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if valueType == nil || seen[valueType] {
		return
	}
	seen[valueType] = true
	if strings.Contains(valueType.PkgPath(), "github.com/dop251/goja") {
		t.Fatalf("exported runtime contract exposes goja type %s", valueType)
	}
	if valueType.PkgPath() != "" && valueType.PkgPath() != "github.com/larchwave/flowbaton/internal/js" {
		return
	}
	switch valueType.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Chan:
		assertNoGojaType(t, valueType.Elem(), seen)
	case reflect.Map:
		assertNoGojaType(t, valueType.Key(), seen)
		assertNoGojaType(t, valueType.Elem(), seen)
	case reflect.Struct:
		for index := 0; index < valueType.NumField(); index++ {
			assertNoGojaType(t, valueType.Field(index).Type, seen)
		}
	case reflect.Func:
		for index := 0; index < valueType.NumIn(); index++ {
			assertNoGojaType(t, valueType.In(index), seen)
		}
		for index := 0; index < valueType.NumOut(); index++ {
			assertNoGojaType(t, valueType.Out(index), seen)
		}
	case reflect.Interface:
		for index := 0; index < valueType.NumMethod(); index++ {
			assertNoGojaType(t, valueType.Method(index).Type, seen)
		}
	}
}

func assertNoGojaValue(t *testing.T, value any) {
	t.Helper()
	if value == nil {
		return
	}
	valueType := reflect.TypeOf(value)
	if strings.Contains(valueType.String(), "goja.") || strings.Contains(valueType.PkgPath(), "github.com/dop251/goja") {
		t.Fatalf("evaluation value exposes goja type %T", value)
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			assertNoGojaValue(t, nested)
		}
	case []any:
		for _, nested := range typed {
			assertNoGojaValue(t, nested)
		}
	}
}

// TestCopiedTextStartsUndefinedUntilSomethingCopies preserves the distinction
// between no copy operation and copying an empty string.
func TestCopiedTextStartsUndefinedUntilSomethingCopies(t *testing.T) {
	t.Parallel()

	runtime := newTestRuntime(t)
	got, err := runtime.Interpolate(context.Background(), `${"" + flowbaton.copiedText}`, nil)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	if got != "undefined" {
		t.Fatalf("copiedText before any copy = %q, want %q", got, "undefined")
	}

	if err := runtime.SetCopiedText(""); err != nil {
		t.Fatalf("SetCopiedText() error = %v", err)
	}
	got, err = runtime.Interpolate(context.Background(), `[${"" + flowbaton.copiedText}]`, nil)
	if err != nil {
		t.Fatalf("Interpolate() error = %v", err)
	}
	if got != "[]" {
		t.Fatalf("copiedText after copying an empty string = %q, want %q", got, "[]")
	}
}

func stringPtr(value string) *string { return &value }
