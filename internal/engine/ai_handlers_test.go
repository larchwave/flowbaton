package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// The three screenshot-based AI commands execute against an injected
// AIPredictionEngine and fail closed with ErrCloudAPIKeyNotAvailable when none
// is configured (specs/01-core-engine.md).

var aiScreenshotPNG = []byte("ai-screenshot-png")

// fakeAIEngine records the screenshot and arguments each method receives and
// answers with canned results or a canned error.
type fakeAIEngine struct {
	result AIResult
	err    error

	findDefectsScreenshot []byte
	assertScreenshot      []byte
	assertAssertion       string
	extractScreenshot     []byte
	extractQuery          string
}

func (engine *fakeAIEngine) FindDefects(_ context.Context, screenshot []byte) (AIResult, error) {
	engine.findDefectsScreenshot = append([]byte(nil), screenshot...)
	return engine.result, engine.err
}

func (engine *fakeAIEngine) PerformAssertion(_ context.Context, screenshot []byte, assertion string) (AIResult, error) {
	engine.assertScreenshot = append([]byte(nil), screenshot...)
	engine.assertAssertion = assertion
	return engine.result, engine.err
}

func (engine *fakeAIEngine) ExtractText(_ context.Context, screenshot []byte, query string) (AIResult, error) {
	engine.extractScreenshot = append([]byte(nil), screenshot...)
	engine.extractQuery = query
	return engine.result, engine.err
}

func aiScreenshotDriver(t testing.TB) *enginetest.FakeDriver {
	t.Helper()
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: aiScreenshotPNG}},
	})
	return driver
}

func aiExecutionState(engine AIPredictionEngine, driver device.Driver, putEnv func(string, string) error) *executionState {
	return &executionState{
		dependencies: Dependencies{AIEngine: engine, Driver: driver},
		putEnvFn:     putEnv,
	}
}

func TestAssertNoDefectsWithAIExecutesAgainstEngine(t *testing.T) {
	t.Parallel()

	t.Run("clean screen completes and records reasoning", func(t *testing.T) {
		engine := &fakeAIEngine{result: AIResult{Reasoning: "no defects observed"}}
		state := aiExecutionState(engine, aiScreenshotDriver(t), nil)
		effect, err := executeAssertNoDefectsWithAI(context.Background(), state,
			evaluatedDispatch{command: model.Command{Kind: model.CommandAssertNoDefectsWithAI}, value: assertNoDefectsWithAIEvaluated{}})
		if err != nil {
			t.Fatalf("execute error = %T %v, want completion", err, err)
		}
		if effect.aiReasoning != "no defects observed" {
			t.Fatalf("aiReasoning = %q, want the engine reasoning", effect.aiReasoning)
		}
		if string(engine.findDefectsScreenshot) != string(aiScreenshotPNG) {
			t.Fatalf("engine screenshot = %q, want the captured PNG", engine.findDefectsScreenshot)
		}
	})

	t.Run("defects fail as assertion and record reasoning", func(t *testing.T) {
		engine := &fakeAIEngine{result: AIResult{Reasoning: "button clipped", Defects: []string{"button clipped"}}}
		state := aiExecutionState(engine, aiScreenshotDriver(t), nil)
		effect, err := executeAssertNoDefectsWithAI(context.Background(), state,
			evaluatedDispatch{command: model.Command{Kind: model.CommandAssertNoDefectsWithAI}, value: assertNoDefectsWithAIEvaluated{}})
		var assertion *AssertionError
		if !errors.As(err, &assertion) {
			t.Fatalf("defect error = %T %v, want AssertionError", err, err)
		}
		if effect.aiReasoning != "button clipped" {
			t.Fatalf("aiReasoning = %q, want the engine reasoning on defects", effect.aiReasoning)
		}
	})
}

func TestAssertWithAIExecutesAssertion(t *testing.T) {
	t.Parallel()

	t.Run("assertion holds", func(t *testing.T) {
		engine := &fakeAIEngine{result: AIResult{Pass: true, Reasoning: "login form is visible"}}
		state := aiExecutionState(engine, aiScreenshotDriver(t), nil)
		effect, err := executeAssertWithAI(context.Background(), state,
			evaluatedDispatch{command: model.Command{Kind: model.CommandAssertWithAI}, value: assertWithAIEvaluated{assertion: "the login form is visible"}})
		if err != nil {
			t.Fatalf("execute error = %T %v, want completion", err, err)
		}
		if engine.assertAssertion != "the login form is visible" {
			t.Fatalf("engine assertion = %q, want the evaluated assertion", engine.assertAssertion)
		}
		if string(engine.assertScreenshot) != string(aiScreenshotPNG) || effect.aiReasoning != "login form is visible" {
			t.Fatalf("engine screenshot/reasoning = %q/%q", engine.assertScreenshot, effect.aiReasoning)
		}
	})

	t.Run("assertion fails", func(t *testing.T) {
		engine := &fakeAIEngine{result: AIResult{Pass: false, Reasoning: "no login form present"}}
		state := aiExecutionState(engine, aiScreenshotDriver(t), nil)
		effect, err := executeAssertWithAI(context.Background(), state,
			evaluatedDispatch{command: model.Command{Kind: model.CommandAssertWithAI}, value: assertWithAIEvaluated{assertion: "the login form is visible"}})
		var assertion *AssertionError
		if !errors.As(err, &assertion) {
			t.Fatalf("failing assertion error = %T %v, want AssertionError", err, err)
		}
		if effect.aiReasoning != "no login form present" {
			t.Fatalf("aiReasoning = %q, want the engine reasoning on failure", effect.aiReasoning)
		}
	})
}

func TestExtractTextWithAISetsOutputVariable(t *testing.T) {
	t.Parallel()

	engine := &fakeAIEngine{result: AIResult{Text: "$42.00", Reasoning: "read total field"}}
	var recorded [][2]string
	state := aiExecutionState(engine, aiScreenshotDriver(t), func(name, value string) error {
		recorded = append(recorded, [2]string{name, value})
		return nil
	})
	effect, err := executeExtractTextWithAI(context.Background(), state,
		evaluatedDispatch{command: model.Command{Kind: model.CommandExtractTextWithAI}, value: extractTextWithAIEvaluated{query: "the total price", outputVariable: "TOTAL"}})
	if err != nil {
		t.Fatalf("execute error = %T %v, want completion", err, err)
	}
	if engine.extractQuery != "the total price" || string(engine.extractScreenshot) != string(aiScreenshotPNG) {
		t.Fatalf("engine query/screenshot = %q/%q", engine.extractQuery, engine.extractScreenshot)
	}
	if !reflect.DeepEqual(recorded, [][2]string{{"TOTAL", "$42.00"}}) {
		t.Fatalf("putEnv calls = %#v, want the extracted text bound to TOTAL", recorded)
	}
	if effect.aiReasoning != "read total field" {
		t.Fatalf("aiReasoning = %q", effect.aiReasoning)
	}
}

func TestAICommandsFailClosedWithoutEngine(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		execute func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error)
		value   any
	}{
		{name: "assertNoDefectsWithAI", execute: executeAssertNoDefectsWithAI, value: assertNoDefectsWithAIEvaluated{}},
		{name: "assertWithAI", execute: executeAssertWithAI, value: assertWithAIEvaluated{assertion: "x"}},
		{name: "extractTextWithAI", execute: executeExtractTextWithAI, value: extractTextWithAIEvaluated{query: "x", outputVariable: "V"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := aiScreenshotDriver(t)
			state := aiExecutionState(nil, driver, func(string, string) error { return nil })
			_, err := test.execute(context.Background(), state,
				evaluatedDispatch{command: model.Command{Kind: model.CommandKeyword(test.name)}, value: test.value})
			if !errors.Is(err, ErrCloudAPIKeyNotAvailable) {
				t.Fatalf("nil-engine error = %T %v, want ErrCloudAPIKeyNotAvailable", err, err)
			}
			// The screenshot must not be spent before the engine is proven present.
			if len(driver.Actions()) != 0 {
				t.Fatalf("driver actions = %#v, want none before a configured engine", driver.Actions())
			}
		})
	}
}

func TestAICommandsDefaultToOptional(t *testing.T) {
	t.Parallel()

	optionalFalse := false
	for _, keyword := range []model.CommandKeyword{
		model.CommandAssertWithAI, model.CommandAssertNoDefectsWithAI, model.CommandExtractTextWithAI,
	} {
		if !commandIsOptional(model.Command{Kind: keyword}) {
			t.Fatalf("%s without an authored flag must default to optional=true", keyword)
		}
		if commandIsOptional(model.Command{Kind: keyword, Optional: &optionalFalse}) {
			t.Fatalf("%s with optional:false must override the default", keyword)
		}
	}
	// Non-AI commands keep the fail-loud default.
	if commandIsOptional(model.Command{Kind: model.CommandTapOn}) {
		t.Fatal("non-AI command must not become optional by default")
	}
}

func TestAICompileExtractsArgumentsAndDefaults(t *testing.T) {
	t.Parallel()

	t.Run("assertWithAI scalar and object", func(t *testing.T) {
		scalar, err := compileAssertWithAI(model.Command{
			Kind: model.CommandAssertWithAI, Form: model.CommandFormObject, Arguments: "no overlap",
		})
		if err != nil || scalar.(assertWithAICompiled).assertion != "no overlap" {
			t.Fatalf("scalar compile = %#v, %v", scalar, err)
		}
		object, err := compileAssertWithAI(model.Command{
			Kind: model.CommandAssertWithAI, Form: model.CommandFormObject,
			Arguments: map[string]any{"assertion": "screen is correct"},
		})
		if err != nil || object.(assertWithAICompiled).assertion != "screen is correct" {
			t.Fatalf("object compile = %#v, %v", object, err)
		}
	})

	t.Run("extractTextWithAI requires query, outputVariable optional (operation contract)", func(t *testing.T) {
		compiled, err := compileExtractTextWithAI(model.Command{
			Kind: model.CommandExtractTextWithAI, Form: model.CommandFormObject,
			Arguments: map[string]any{"query": "the total", "outputVariable": "TOTAL"},
		})
		if err != nil {
			t.Fatalf("compile error = %v", err)
		}
		payload := compiled.(extractTextWithAICompiled)
		if payload.query != "the total" || payload.outputVariable != "TOTAL" {
			t.Fatalf("payload = %#v", payload)
		}
		// query is required; outputVariable is optional.
		noOut, err := compileExtractTextWithAI(model.Command{
			Kind: model.CommandExtractTextWithAI, Form: model.CommandFormObject,
			Arguments: map[string]any{"query": "x"},
		})
		if err != nil {
			t.Fatalf("query-only compile error = %v, want nil (operation accepts)", err)
		}
		if noOut.(extractTextWithAICompiled).outputVariable != "" {
			t.Fatalf("query-only payload = %#v, want empty outputVariable", noOut)
		}
		// query is still required; a blank query or blank explicit outputVariable stays invalid.
		for _, bad := range []map[string]any{{"outputVariable": "V"}, {"query": " ", "outputVariable": "V"}, {"query": "x", "outputVariable": " "}} {
			if _, err := compileExtractTextWithAI(model.Command{
				Kind: model.CommandExtractTextWithAI, Form: model.CommandFormObject, Arguments: bad,
			}); !isConfigurationError(err) {
				t.Fatalf("compile(%#v) error = %T %v, want ConfigurationError", bad, err, err)
			}
		}
	})

	t.Run("assertNoDefectsWithAI bare form", func(t *testing.T) {
		if _, err := compileAssertNoDefectsWithAI(model.Command{
			Kind: model.CommandAssertNoDefectsWithAI, Form: model.CommandFormScalar,
		}); err != nil {
			t.Fatalf("bare compile error = %v", err)
		}
	})
}

// TestAICommandsRunThroughProductionRegistry proves the handlers are registered
// and wired end to end: a configured engine drives a passing assertWithAI to
// Completed with the reasoning surfaced on metadata, while a nil engine warns
// (the AI default optional=true) rather than failing the flow.
func TestAICommandsRunThroughProductionRegistry(t *testing.T) {
	t.Parallel()

	t.Run("configured engine completes and surfaces reasoning", func(t *testing.T) {
		engine := &fakeAIEngine{result: AIResult{Pass: true, Reasoning: "form present"}}
		result, err := runAIFlow(t, aiFlowCommand(), engine)
		if err != nil {
			t.Fatalf("run error = %T %v", err, err)
		}
		if result.Outcome() != Completed {
			t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
		}
		if got := result.Commands()[0].Metadata().AIReasoning(); got != "form present" {
			t.Fatalf("metadata reasoning = %q, want the engine reasoning", got)
		}
	})

	t.Run("missing engine warns under default optional", func(t *testing.T) {
		result, err := runAIFlow(t, aiFlowCommand(), nil)
		if err != nil {
			t.Fatalf("run error = %T %v, want a warned (non-fatal) outcome", err, err)
		}
		if result.Outcome() != Warned {
			t.Fatalf("outcome = %s, want %s (default optional=true fails closed as a warning)", result.Outcome(), Warned)
		}
	})
}

func aiFlowCommand() model.Command {
	return model.Command{
		Kind: model.CommandAssertWithAI, Form: model.CommandFormObject, Arguments: "the login form is visible",
	}
}

func runAIFlow(t testing.TB, command model.Command, engine AIPredictionEngine) (FlowResult, error) {
	t.Helper()
	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	driver := enginetest.NewFakeDriver()
	info := device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:     []enginetest.Result[device.DeviceInfo]{{Value: info}, {Value: info}},
		TakeScreenshot: []enginetest.Result[[]byte]{{Value: aiScreenshotPNG}},
	})
	path := "/workspace/ai.yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.ai"},
		Commands: []model.Command{command},
	}
	program := &Program{
		roots: []string{path}, paths: []string{path},
		flows:   map[string]model.Flow{path: flow},
		aliases: map[string]string{path: path},
		graph: capability.Report{
			Roots: []string{path},
			Nodes: []capability.GraphNode{{Path: path}},
		},
	}
	compiled, compileErr := compileProgram(context.Background(), program, registry)
	if compileErr != nil {
		t.Fatalf("compileProgram(ai) error = %v", compileErr)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	dependencies := Dependencies{
		ExecutionID: "ai", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}
	if engine != nil {
		dependencies.AIEngine = engine
	}
	return executeCompiledRootForRun(context.Background(), dependencies, root, "ai/root-run-000001")
}
