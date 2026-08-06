package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// These tests cover the four app-lifecycle handlers. Each maps one-to-one onto an
// existing Driver method, so the surface under test is the compile grammar, the
// appId resolution, and the exact driver call.

func TestLifecycleHandlerSpecsComposeExactFour(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(lifecycleHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(lifecycle) error = %v", err)
	}
	want := []string{"clearKeychain", "clearState", "killApp", "stopApp"}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, want) {
		t.Fatalf("lifecycle registry = %#v, want %#v", got, want)
	}
	for _, keyword := range []model.CommandKeyword{
		model.CommandStopApp, model.CommandKillApp, model.CommandClearState, model.CommandClearKeychain,
	} {
		spec, ok := registry.lookup(keyword)
		if !ok {
			t.Fatalf("lifecycle registry missing %s", keyword)
		}
		if spec.effectClass != EffectDeviceMutation {
			t.Fatalf("%s effect class = %v, want device mutation", keyword, spec.effectClass)
		}
		// These commands do not settle; only launchApp owns a settle policy.
		if spec.postAction != postActionNoSettle || spec.settleRequest != nil {
			t.Fatalf("%s post action = %v factory %v, want no-settle", keyword, spec.postAction, spec.settleRequest != nil)
		}
	}
}

func TestLifecycleCompileAcceptsAuthoredForms(t *testing.T) {
	t.Parallel()

	for _, keyword := range []model.CommandKeyword{
		model.CommandStopApp, model.CommandKillApp, model.CommandClearState,
	} {
		t.Run(string(keyword)+" bare", func(t *testing.T) {
			compiled, err := compileLifecycle(model.Command{Kind: keyword, Form: model.CommandFormScalar})
			if err != nil {
				t.Fatalf("compileLifecycle(%s bare) error = %v", keyword, err)
			}
			payload, ok := compiled.(lifecycleCompiled)
			if !ok || payload.keyword != keyword || payload.explicitAppID != nil {
				t.Fatalf("compiled = %#v, want bare %s with no explicit appId", compiled, keyword)
			}
		})
		t.Run(string(keyword)+" explicit appId", func(t *testing.T) {
			compiled, err := compileLifecycle(model.Command{
				Kind: keyword, Form: model.CommandFormObject, Arguments: "com.example.explicit",
			})
			if err != nil {
				t.Fatalf("compileLifecycle(%s explicit) error = %v", keyword, err)
			}
			payload, ok := compiled.(lifecycleCompiled)
			if !ok || payload.explicitAppID == nil || *payload.explicitAppID != "com.example.explicit" {
				t.Fatalf("compiled = %#v, want explicit appId", compiled)
			}
		})
	}

	t.Run("clearKeychain bare", func(t *testing.T) {
		compiled, err := compileLifecycle(model.Command{Kind: model.CommandClearKeychain, Form: model.CommandFormScalar})
		if err != nil {
			t.Fatalf("compileLifecycle(clearKeychain) error = %v", err)
		}
		payload, ok := compiled.(lifecycleCompiled)
		if !ok || payload.keyword != model.CommandClearKeychain || payload.explicitAppID != nil {
			t.Fatalf("compiled = %#v, want bare clearKeychain", compiled)
		}
	})
}

func TestLifecycleCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	link := model.FileLink{Path: "/workspace/other.yaml"}
	optional := true
	label := "labelled"
	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
		{name: "clearKeychain with a value", command: model.Command{Kind: model.CommandClearKeychain, Form: model.CommandFormObject, Arguments: "com.example.app"}},
		{name: "stopApp with children", command: model.Command{Kind: model.CommandStopApp, Form: model.CommandFormScalar, Children: []model.Command{{Kind: model.CommandBack, Form: model.CommandFormScalar}}}},
		{name: "killApp with condition", command: model.Command{Kind: model.CommandKillApp, Form: model.CommandFormScalar, Condition: &model.Condition{}}},
		{name: "clearState with link", command: model.Command{Kind: model.CommandClearState, Form: model.CommandFormScalar, Links: []model.FileLink{link}}},
		{name: "stopApp with selector", command: model.Command{Kind: model.CommandStopApp, Form: model.CommandFormScalar, Selector: &model.ElementSelector{}}},
		{name: "killApp with label", command: model.Command{Kind: model.CommandKillApp, Form: model.CommandFormScalar, Label: &label}},
		{name: "clearState with optional", command: model.Command{Kind: model.CommandClearState, Form: model.CommandFormScalar, Optional: &optional}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileLifecycle(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileLifecycle(%#v) = %#v, %T %v; want nil and ConfigurationError", test.command, compiled, err, err)
			}
		})
	}
}

func TestLifecycleExecuteCallsExactDriverMethod(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		keyword model.CommandKeyword
		method  enginetest.Method
	}{
		{keyword: model.CommandStopApp, method: enginetest.MethodStopApp},
		{keyword: model.CommandKillApp, method: enginetest.MethodKillApp},
		{keyword: model.CommandClearState, method: enginetest.MethodClearAppState},
		{keyword: model.CommandClearKeychain, method: enginetest.MethodClearKeychain},
	} {
		t.Run(string(test.keyword), func(t *testing.T) {
			driver := lifecycleDriver(nil)
			result, runErr := runLifecycleCommand(t, driver, model.Command{Kind: test.keyword, Form: model.CommandFormScalar})
			if runErr != nil {
				t.Fatalf("execute(%s) error = %T %v", test.keyword, runErr, runErr)
			}
			if result.Outcome() != Completed {
				t.Fatalf("execute(%s) outcome = %s, want %s", test.keyword, result.Outcome(), Completed)
			}
			// The engine reads cached DeviceInfo first; only the lifecycle
			// call itself is under test here.
			if got := lifecycleAction(t, driver); got.Method != test.method {
				t.Fatalf("lifecycle action = %#v, want %s", got, test.method)
			}
		})
	}
}

func TestLifecycleExecuteUsesActiveAppIDAndExplicitOverride(t *testing.T) {
	t.Parallel()

	t.Run("falls back to the active appId", func(t *testing.T) {
		driver := lifecycleDriver(nil)
		if _, err := runLifecycleCommand(t, driver, model.Command{Kind: model.CommandStopApp, Form: model.CommandFormScalar}); err != nil {
			t.Fatalf("execute error = %v", err)
		}
		if got := lifecycleAppRequest(t, driver); got.AppID != lifecycleActiveAppID {
			t.Fatalf("StopApp appId = %q, want the active %q", got.AppID, lifecycleActiveAppID)
		}
	})

	t.Run("explicit appId overrides the active one", func(t *testing.T) {
		driver := lifecycleDriver(nil)
		command := model.Command{Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: "com.example.override"}
		if _, err := runLifecycleCommand(t, driver, command); err != nil {
			t.Fatalf("execute error = %v", err)
		}
		if got := lifecycleAppRequest(t, driver); got.AppID != "com.example.override" {
			t.Fatalf("StopApp appId = %q, want the explicit override", got.AppID)
		}
	})

	t.Run("explicit appId is interpolated", func(t *testing.T) {
		driver := lifecycleDriver(nil)
		command := model.Command{Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: "com.example.${'interp'}"}
		if _, err := runLifecycleCommand(t, driver, command); err != nil {
			t.Fatalf("execute error = %v", err)
		}
		if got := lifecycleAppRequest(t, driver); got.AppID != "com.example.interp" {
			t.Fatalf("StopApp appId = %q, want the interpolated value", got.AppID)
		}
	})

	t.Run("clearKeychain takes no appId", func(t *testing.T) {
		driver := lifecycleDriver(nil)
		if _, err := runLifecycleCommand(t, driver, model.Command{Kind: model.CommandClearKeychain, Form: model.CommandFormScalar}); err != nil {
			t.Fatalf("execute error = %v", err)
		}
		got := lifecycleAction(t, driver)
		if got.Method != enginetest.MethodClearKeychain || got.Request != nil {
			t.Fatalf("ClearKeychain action = %#v, want one request-free call", got)
		}
	})
}

func TestLifecycleExecutePropagatesDriverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("lifecycle driver refused")
	driver := lifecycleDriver(sentinel)
	result, runErr := runLifecycleCommand(t, driver, model.Command{Kind: model.CommandStopApp, Form: model.CommandFormScalar})
	if runErr == nil {
		t.Fatal("execute(stopApp) error = nil, want the driver failure")
	}
	if !errors.Is(runErr, sentinel) {
		t.Fatalf("execute(stopApp) error = %T %v, want the exact driver cause", runErr, runErr)
	}
	if result.Outcome() != Failed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Failed)
	}
}

const lifecycleActiveAppID = "com.example.lifecycle"

func lifecycleDriver(failure error) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	one := []enginetest.Result[struct{}]{{Err: failure}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:    []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}}},
		StopApp:       one,
		KillApp:       one,
		ClearAppState: one,
		ClearKeychain: one,
	})
	return driver
}

// lifecycleAction returns the single non-DeviceInfo driver call, failing if the
// handler made anything other than exactly one lifecycle call.
func lifecycleAction(t testing.TB, driver *enginetest.FakeDriver) enginetest.Action {
	t.Helper()
	var found []enginetest.Action
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodDeviceInfo {
			continue
		}
		found = append(found, action)
	}
	if len(found) != 1 {
		t.Fatalf("lifecycle driver calls = %#v, want exactly one", found)
	}
	return found[0]
}

func lifecycleAppRequest(t testing.TB, driver *enginetest.FakeDriver) device.AppRequest {
	t.Helper()
	action := lifecycleAction(t, driver)
	request, ok := action.Request.(device.AppRequest)
	if !ok {
		t.Fatalf("driver action request = %#v, want device.AppRequest", action.Request)
	}
	return request
}

func runLifecycleCommand(t testing.TB, driver *enginetest.FakeDriver, command model.Command) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(lifecycleHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(lifecycle) error = %v", err)
	}
	return runSingleCommandFlow(t, driver, registry, command, "lifecycle")
}

// runSingleCommandFlow executes one authored command as a whole single-root
// program against the supplied registry, so the command travels the real
// compile/evaluate/execute path rather than a hand-built dispatch.
func runSingleCommandFlow(
	t testing.TB,
	driver *enginetest.FakeDriver,
	registry handlerRegistry,
	command model.Command,
	name string,
) (FlowResult, error) {
	t.Helper()
	return runSingleCommandFlowForApp(t, driver, registry, command, name, lifecycleActiveAppID)
}

func runSingleCommandFlowForApp(
	t testing.TB,
	driver *enginetest.FakeDriver,
	registry handlerRegistry,
	command model.Command,
	name string,
	activeAppID string,
) (FlowResult, error) {
	t.Helper()
	return runSingleCommandFlowForAppWithClock(t, driver, registry, command, name, activeAppID, nil)
}

func runSingleCommandFlowForAppWithClock(
	t testing.TB,
	driver *enginetest.FakeDriver,
	registry handlerRegistry,
	command model.Command,
	name string,
	activeAppID string,
	clock Clock,
) (FlowResult, error) {
	t.Helper()
	path := "/workspace/" + name + "-" + string(command.Kind) + ".yaml"
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: path,
		Config:   model.Config{AppID: activeAppID},
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
	return executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "lifecycle", Driver: driver, Clock: singleCommandFlowClock(clock),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "lifecycle/root-run-000001")
}

// singleCommandFlowClock keeps the default harness on the advancing clock while
// letting a caller substitute one that records waits.
func singleCommandFlowClock(clock Clock) Clock {
	if clock == nil {
		return newAdvancingClock()
	}
	return clock
}

// runSingleCommandFlowWithClock is runSingleCommandFlow with an injected clock,
// for handlers whose schedule is part of their contract.
func runSingleCommandFlowWithClock(
	t testing.TB,
	driver *enginetest.FakeDriver,
	registry handlerRegistry,
	command model.Command,
	name string,
	clock Clock,
) (FlowResult, error) {
	t.Helper()
	return runSingleCommandFlowForAppWithClock(t, driver, registry, command, name, "com.example.lifecycle", clock)
}
