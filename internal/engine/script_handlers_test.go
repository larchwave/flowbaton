package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

// These tests cover the two scripting commands. specs/01-core-engine.md:98 is the
// whole contract: runScript evaluates the contracted file with
// runInSubScope=true and scriptDir set, gated by `when`; evalScript is an
// interpolation of the script string. Both count as mutating, so both run their
// effect in execute rather than during evaluation.

func TestScriptHandlerSpecsComposeExactTwo(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(scriptHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(script) error = %v", err)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, []string{"evalScript", "runScript"}) {
		t.Fatalf("script registry = %#v, want evalScript and runScript", got)
	}
	for _, keyword := range []model.CommandKeyword{model.CommandRunScript, model.CommandEvalScript} {
		spec, _ := registry.lookup(keyword)
		if spec.effectClass != EffectHostMutation {
			t.Fatalf("%s effect class = %v, want %v", keyword, spec.effectClass, EffectHostMutation)
		}
		if spec.postAction != postActionUnspecified || spec.settleRequest != nil {
			t.Fatalf("%s must not declare a post-action policy: %#v", keyword, spec)
		}
	}
}

func TestEvalScriptInterpolatesTheAuthoredScriptDuringExecution(t *testing.T) {
	t.Parallel()

	runtime := &recordingJSFactory{}
	command := model.Command{
		Kind: model.CommandEvalScript, Form: model.CommandFormObject,
		Arguments: "output.value = 'ok'",
	}
	result, err := runScriptCommand(t, scriptServices{factory: runtime}, command, nil)
	if err != nil {
		t.Fatalf("execute(evalScript) error = %T %v", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	// The script is interpolated verbatim; the engine never pre-interpolates it
	// during evaluation, which would run the mutation a phase too early.
	if !runtime.interpolated("output.value = 'ok'") {
		t.Fatalf("interpolations = %#v, want the authored script", runtime.interpolations)
	}
	if len(runtime.evaluations) != 0 {
		t.Fatalf("evaluations = %#v, want none for evalScript", runtime.evaluations)
	}
}

func TestRunScriptEvaluatesTheResolvedFileInASubScope(t *testing.T) {
	t.Parallel()

	factory := &recordingJSFactory{}
	reader := &stubResourceReader{data: []byte("output.ready = true")}
	command := scriptFileCommand(map[string]any{
		"file": "scripts/setup.js",
		"env":  map[string]any{"ROLE": "tester"},
	}, "/workspace/scripts/setup.js")
	if _, err := runScriptCommand(t, scriptServices{factory: factory, reader: reader}, command, nil); err != nil {
		t.Fatalf("execute(runScript) error = %T %v", err, err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Path != "/workspace/scripts/setup.js" {
		t.Fatalf("resource reads = %#v, want the resolved script path", reader.requests)
	}
	if len(factory.evaluations) != 1 {
		t.Fatalf("evaluations = %#v, want exactly one", factory.evaluations)
	}
	request := factory.evaluations[0]
	if request.Script != "output.ready = true" {
		t.Fatalf("script = %q, want the resolved file contents", request.Script)
	}
	if !request.RunInSubScope {
		t.Fatal("runScript must evaluate with runInSubScope=true")
	}
	if request.ScriptDir != "/workspace/scripts" {
		t.Fatalf("scriptDir = %q, want the script's own directory", request.ScriptDir)
	}
	if request.SourceName != "/workspace/scripts/setup.js" {
		t.Fatalf("sourceName = %q, want the resolved script path", request.SourceName)
	}
	if !reflect.DeepEqual(request.Env, map[string]any{"ROLE": "tester"}) {
		t.Fatalf("env = %#v, want the authored overlay", request.Env)
	}
}

func TestRunScriptInterpolatesItsEnvironmentValues(t *testing.T) {
	t.Parallel()

	factory := &recordingJSFactory{}
	command := scriptFileCommand(map[string]any{
		"file": "scripts/setup.js",
		"env":  map[string]any{"ROLE": "${'tester'}"},
	}, "/workspace/scripts/setup.js")
	if _, err := runScriptCommand(t,
		scriptServices{factory: factory, reader: &stubResourceReader{}}, command, nil); err != nil {
		t.Fatalf("execute(runScript) error = %T %v", err, err)
	}
	if got := factory.evaluations[0].Env; !reflect.DeepEqual(got, map[string]any{"ROLE": "tester"}) {
		t.Fatalf("env = %#v, want the interpolated overlay", got)
	}
}

func TestRunScriptSkipsWithoutAnyEffectWhenItsConditionIsFalse(t *testing.T) {
	t.Parallel()

	factory := &recordingJSFactory{}
	reader := &stubResourceReader{}
	command := scriptFileCommand(map[string]any{
		"file": "scripts/setup.js",
		"when": map[string]any{"platform": "ios"},
	}, "/workspace/scripts/setup.js")
	command.Condition = &model.Condition{Platform: platformPointerForScript("ios")}
	// The executor absorbs CommandSkippedError into a Skipped outcome rather
	// than surfacing it, so the outcome is what the flow actually reports.
	result, err := runScriptCommand(t, scriptServices{factory: factory, reader: reader}, command, nil)
	if err != nil {
		t.Fatalf("false condition error = %T %v, want a clean skip", err, err)
	}
	if got := result.Commands()[0].Outcome(); got != Skipped {
		t.Fatalf("outcome = %s, want %s", got, Skipped)
	}
	if len(reader.requests) != 0 || len(factory.evaluations) != 0 {
		t.Fatalf("skipped runScript still read %#v and evaluated %#v", reader.requests, factory.evaluations)
	}
}

func TestRunScriptFailsClosedWithoutAResourceReader(t *testing.T) {
	t.Parallel()

	command := scriptFileCommand(map[string]any{"file": "scripts/setup.js"}, "/workspace/scripts/setup.js")
	_, err := runScriptCommand(t, scriptServices{factory: &recordingJSFactory{}}, command, nil)
	if !isConfigurationError(err) {
		t.Fatalf("error = %T %v, want ConfigurationError", err, err)
	}
}

func TestRunScriptPropagatesBoundaryFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("script boundary refused")
	command := scriptFileCommand(map[string]any{"file": "scripts/setup.js"}, "/workspace/scripts/setup.js")
	t.Run("resource read", func(t *testing.T) {
		_, err := runScriptCommand(t,
			scriptServices{factory: &recordingJSFactory{}, reader: &stubResourceReader{err: sentinel}}, command, nil)
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %T %v, want the exact reader cause", err, err)
		}
	})
	t.Run("script evaluation", func(t *testing.T) {
		_, err := runScriptCommand(t,
			scriptServices{factory: &recordingJSFactory{evaluateErr: sentinel}, reader: &stubResourceReader{}}, command, nil)
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %T %v, want the exact runtime cause", err, err)
		}
	})
}

func TestScriptCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	forged := scriptFileCommand(map[string]any{"file": "scripts/setup.js"}, "/workspace/scripts/setup.js")
	forged.Links[0].Path = "scripts/other.js"

	wrongKind := scriptFileCommand(map[string]any{"file": "scripts/setup.js"}, "/workspace/scripts/setup.js")
	wrongKind.Links[0].Kind = model.FileLinkFlow

	withChildren := scriptFileCommand(map[string]any{"file": "scripts/setup.js"}, "/workspace/scripts/setup.js")
	withChildren.Children = []model.Command{{Kind: model.CommandBack, Form: model.CommandFormScalar}}

	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
		{name: "evalScript bare", command: model.Command{Kind: model.CommandEvalScript, Form: model.CommandFormScalar}},
		{name: "evalScript blank", command: model.Command{
			Kind: model.CommandEvalScript, Form: model.CommandFormObject, Arguments: "  ",
		}},
		{name: "evalScript wrong type", command: model.Command{
			Kind: model.CommandEvalScript, Form: model.CommandFormObject, Arguments: int64(1),
		}},
		{name: "runScript missing file", command: model.Command{
			Kind: model.CommandRunScript, Form: model.CommandFormObject, Arguments: map[string]any{},
		}},
		{name: "runScript unknown key", command: model.Command{
			Kind: model.CommandRunScript, Form: model.CommandFormObject,
			Arguments: map[string]any{"file": "scripts/setup.js", "timeout": int64(1)},
		}},
		{name: "runScript without a link", command: model.Command{
			Kind: model.CommandRunScript, Form: model.CommandFormObject,
			Arguments: map[string]any{"file": "scripts/setup.js"},
		}},
		{name: "runScript link path mismatch", command: forged},
		{name: "runScript link is a flow", command: wrongKind},
		{name: "runScript with children", command: withChildren},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileScript(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileScript() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

func platformPointerForScript(value model.Platform) *model.Platform { return &value }

// scriptDriver answers the one DeviceInfo call the engine makes before any
// command runs; scripting never touches the device beyond that.
func scriptDriver() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
		}}},
	})
	return driver
}

type scriptServices struct {
	factory *recordingJSFactory
	reader  ResourceReader
}

// recordingJSFactory wraps the real goja runtime so interpolation still behaves,
// while recording what the handlers asked it to run.
type recordingJSFactory struct {
	base           js.Factory
	interpolations []string
	evaluations    []js.EvalRequest
	evaluateErr    error
}

func (factory *recordingJSFactory) NewRuntime() (js.Runtime, error) {
	if factory.base == nil {
		base, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
		if err != nil {
			return nil, err
		}
		factory.base = base
	}
	runtime, err := factory.base.NewRuntime()
	if err != nil {
		return nil, err
	}
	return &recordingRuntime{Runtime: runtime, factory: factory}, nil
}

func (factory *recordingJSFactory) interpolated(script string) bool {
	for _, recorded := range factory.interpolations {
		if recorded == script {
			return true
		}
	}
	return false
}

type recordingRuntime struct {
	js.Runtime
	factory *recordingJSFactory
}

func (runtime *recordingRuntime) Interpolate(
	ctx context.Context,
	input string,
	env map[string]any,
) (string, error) {
	runtime.factory.interpolations = append(runtime.factory.interpolations, input)
	return runtime.Runtime.Interpolate(ctx, input, env)
}

func (runtime *recordingRuntime) Evaluate(ctx context.Context, request js.EvalRequest) (js.Result, error) {
	runtime.factory.evaluations = append(runtime.factory.evaluations, request)
	if runtime.factory.evaluateErr != nil {
		return js.Result{}, runtime.factory.evaluateErr
	}
	return runtime.Runtime.Evaluate(ctx, request)
}

func scriptFileCommand(arguments map[string]any, resolved string) model.Command {
	file, _ := arguments["file"].(string)
	return model.Command{
		Kind: model.CommandRunScript, Form: model.CommandFormObject, Arguments: arguments,
		Links: []model.FileLink{{
			Kind: model.FileLinkScript, Path: file, ResolvedPath: resolved,
		}},
	}
}

func runScriptCommand(
	t testing.TB,
	services scriptServices,
	command model.Command,
	environment map[string]string,
) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(scriptHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(script) error = %v", err)
	}
	path := "/workspace/script-" + string(command.Kind) + ".yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: "com.example.script", Env: environment},
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
		t.Fatalf("compileProgram(%s) error = %v", command.Kind, compileErr)
	}
	root, ok := compiled.Flow(compiled.Roots()[0])
	if !ok {
		t.Fatal("compiled root missing")
	}
	dependencies := Dependencies{
		ExecutionID: "script", Driver: scriptDriver(), Clock: newAdvancingClock(),
		JSFactory: services.factory, Controller: NoopController{},
	}
	if services.reader != nil {
		dependencies.ResourceReader = services.reader
	}
	return executeCompiledRootForRun(context.Background(), dependencies, root, "script/root-run-000001")
}
