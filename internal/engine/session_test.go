package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestNewExecutionSessionOwnsOneSeededRuntimeAndLookup(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 390, HeightGrid: 844},
	}}})
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	runtime := &sessionRuntime{}
	factory := &sessionRuntimeFactory{runtime: runtime}
	root := &compiledFlow{path: "/workspace/root.yaml"}
	dependencies := Dependencies{
		Driver: driver, Clock: clock, JSFactory: factory, Controller: NoopController{},
	}

	session, err := newExecutionSession(context.Background(), dependencies, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	if factory.Calls() != 1 {
		t.Fatalf("NewRuntime calls = %d, want one", factory.Calls())
	}
	if session.root != root || session.core == nil {
		t.Fatalf("session ownership = root %p core %p, want root %p and non-nil core", session.root, session.core, root)
	}
	if got, runtimeErr := session.jsRuntime(); runtimeErr != nil || got != runtime {
		t.Fatalf("jsRuntime() = %T, %v; want owned runtime", got, runtimeErr)
	}
	if got, lookupErr := session.elementLookup(); lookupErr != nil || got == nil || got != session.lookup {
		t.Fatalf("elementLookup() = %p, %v; want persistent lookup %p", got, lookupErr, session.lookup)
	}
	// Platform is seeded while `flowbaton.copiedText` remains undefined until a
	// command copies text, preserving the distinction between unset and empty.
	platforms, copiedValues, closeCalls := runtime.Snapshot()
	if len(platforms) != 1 || platforms[0] != "ios" || len(copiedValues) != 0 || closeCalls != 0 {
		t.Fatalf("runtime seed = platforms %v copied %v closes %d", platforms, copiedValues, closeCalls)
	}
	actions := driver.Actions()
	if len(actions) != 1 || actions[0].Method != enginetest.MethodDeviceInfo {
		t.Fatalf("driver actions = %#v, want exactly one DeviceInfo", actions)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("first Close() error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second Close() error: %v", err)
	}
	_, _, closeCalls = runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
	actions = driver.Actions()
	if len(actions) != 1 || actions[0].Method != enginetest.MethodDeviceInfo {
		t.Fatalf("driver actions after session close = %#v, want no Driver.Close", actions)
	}
	if _, err := session.jsRuntime(); err == nil {
		t.Fatal("jsRuntime after Close error = nil")
	}
	if _, err := session.elementLookup(); err == nil {
		t.Fatal("elementLookup after Close error = nil")
	}
}

func TestNewExecutionSessionClosesRuntimeOnPartialInitializationFailure(t *testing.T) {
	t.Parallel()

	t.Run("factory returns runtime and error", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("factory failed after allocation")
		runtime := &sessionRuntime{}
		driver := enginetest.NewFakeDriver()
		session, err := newExecutionSession(context.Background(), Dependencies{
			Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
			JSFactory: &sessionRuntimeFactory{runtime: runtime, err: primary}, Controller: NoopController{},
		}, &compiledFlow{path: "root.yaml"})
		if session != nil || err != primary {
			t.Fatalf("newExecutionSession() = %#v, %v; want nil and primary factory error", session, err)
		}
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Fatalf("runtime Close calls = %d, want one for allocated failed runtime", closeCalls)
		}
		if len(driver.Actions()) != 0 {
			t.Fatalf("factory failure reached driver: %#v", driver.Actions())
		}
	})

	t.Run("device info", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("device info failed")
		driver := enginetest.NewFakeDriver()
		driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Err: primary}}})
		runtime := &sessionRuntime{closeErr: errors.New("close also failed")}
		session, err := newExecutionSession(context.Background(), Dependencies{
			Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
			JSFactory: &sessionRuntimeFactory{runtime: runtime}, Controller: NoopController{},
		}, &compiledFlow{path: "root.yaml"})
		if session != nil || err != primary {
			t.Fatalf("newExecutionSession() = %#v, %v; want nil and primary device error", session, err)
		}
		_, _, closeCalls := runtime.Snapshot()
		if closeCalls != 1 {
			t.Fatalf("runtime Close calls = %d, want one", closeCalls)
		}
	})

	t.Run("platform", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("platform seed failed")
		runtime := &sessionRuntime{platformErr: primary}
		session, err := newTestExecutionSession(t, context.Background(), runtime)
		if session != nil || err != primary {
			t.Fatalf("newExecutionSession() = %#v, %v; want nil and primary platform error", session, err)
		}
		_, copied, closeCalls := runtime.Snapshot()
		if len(copied) != 0 || closeCalls != 1 {
			t.Fatalf("runtime after platform failure = copied %v closes %d, want none/one", copied, closeCalls)
		}
	})

	t.Run("cancellation after platform", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		runtime := &sessionRuntime{afterPlatform: cancel}
		session, err := newTestExecutionSession(t, ctx, runtime)
		if session != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("newExecutionSession() = %#v, %v; want nil and context.Canceled", session, err)
		}
		_, copied, closeCalls := runtime.Snapshot()
		if len(copied) != 0 || closeCalls != 1 {
			t.Fatalf("runtime after cancellation = copied %v closes %d, want no later seed and one close", copied, closeCalls)
		}
	})

	t.Run("typed nil runtime", func(t *testing.T) {
		t.Parallel()
		var typedNil *sessionRuntime
		driver := enginetest.NewFakeDriver()
		session, err := newExecutionSession(context.Background(), Dependencies{
			Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
			JSFactory: &sessionRuntimeFactory{runtime: typedNil}, Controller: NoopController{},
		}, &compiledFlow{path: "root.yaml"})
		if session != nil || err == nil {
			t.Fatalf("newExecutionSession() = %#v, %v; want typed-nil rejection", session, err)
		}
		if len(driver.Actions()) != 0 {
			t.Fatalf("typed-nil runtime reached driver: %#v", driver.Actions())
		}
	})
}

func TestExecutionSessionCloseReturnsRuntimeErrorOnce(t *testing.T) {
	t.Parallel()

	closeErr := errors.New("runtime close failed")
	runtime := &sessionRuntime{closeErr: closeErr}
	session, err := newTestExecutionSession(t, context.Background(), runtime)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	if err := session.Close(); err != closeErr {
		t.Fatalf("first Close() error = %v, want %v", err, closeErr)
	}
	if err := session.Close(); err != closeErr {
		t.Fatalf("second Close() error = %v, want same error", err)
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecutionSessionFlowScopesApplyEnvironmentAndRestoreConfig(t *testing.T) {
	t.Parallel()

	runtime := &sessionRuntime{}
	root := &compiledFlow{
		path: "/workspace/root.yaml",
		config: model.Config{
			AppID: "com.example.root",
			Env:   map[string]string{"Z_LAST": "root-z", "A_FIRST": "root-a"},
		},
	}
	session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}

	rootScope, err := session.enterFlow(context.Background(), root, map[string]string{
		"B_SECOND": "overlay-b", "A_FIRST": "overlay-a",
	})
	if err != nil {
		t.Fatalf("enter root flow error: %v", err)
	}
	if got, err := session.currentAppID(); err != nil || got != "com.example.root" {
		t.Fatalf("root currentAppID() = %q, %v", got, err)
	}
	config, err := session.currentConfig()
	if err != nil || config.Env["A_FIRST"] != "root-a" {
		t.Fatalf("root currentConfig() = %#v, %v", config, err)
	}
	config.Env["A_FIRST"] = "caller-mutated"
	config, _ = session.currentConfig()
	if config.Env["A_FIRST"] != "root-a" {
		t.Fatalf("currentConfig exposed frame storage: %#v", config.Env)
	}

	child := &compiledFlow{
		path:   "/workspace/child.yaml",
		config: model.Config{AppID: "com.example.child", Env: map[string]string{"CHILD": "yes"}},
	}
	childScope, err := session.enterFlow(context.Background(), child, map[string]string{"CHILD": "overridden"})
	if err != nil {
		t.Fatalf("enter child flow error: %v", err)
	}
	if got, err := session.currentAppID(); err != nil || got != "com.example.child" {
		t.Fatalf("child currentAppID() = %q, %v", got, err)
	}
	if err := childScope.Close(); err != nil {
		t.Fatalf("child scope Close() error: %v", err)
	}
	if err := childScope.Close(); err != nil {
		t.Fatalf("second child scope Close() error: %v", err)
	}
	if got, err := session.currentAppID(); err != nil || got != "com.example.root" {
		t.Fatalf("restored currentAppID() = %q, %v", got, err)
	}
	if err := rootScope.Close(); err != nil {
		t.Fatalf("root scope Close() error: %v", err)
	}
	if _, err := session.currentConfig(); err == nil {
		t.Fatal("currentConfig without active flow error = nil")
	}

	wantEnvCalls := concatEnvCalls(
		// The overlay goes on first and the flow's own env last, so a flow's
		// `env:` wins. See env_precedence_test.go.
		flowEnvCalls("/workspace/root.yaml",
			"put:A_FIRST=overlay-a", "put:B_SECOND=overlay-b",
			"put:A_FIRST=root-a", "put:Z_LAST=root-z"),
		flowEnvCalls("/workspace/child.yaml", "put:CHILD=overridden", "put:CHILD=yes"),
		[]string{"pop", "pop"},
	)
	if got := runtime.EnvCalls(); !reflect.DeepEqual(got, wantEnvCalls) {
		t.Fatalf("runtime env calls = %#v, want %#v", got, wantEnvCalls)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("session Close() error: %v", err)
	}
}

func TestExecutionSessionFlowScopeRollsBackAndEnforcesLIFO(t *testing.T) {
	t.Parallel()

	t.Run("put failure keeps primary and rolls back", func(t *testing.T) {
		t.Parallel()
		primary := errors.New("put B failed")
		runtime := &sessionRuntime{failPutName: "B", putErr: primary, popErr: errors.New("pop also failed")}
		flow := &compiledFlow{path: "root.yaml", config: model.Config{Env: map[string]string{
			"C": "three", "A": "one", "B": "two",
		}}}
		session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, flow)
		if err != nil {
			t.Fatalf("newExecutionSession() error: %v", err)
		}
		scope, enterErr := session.enterFlow(context.Background(), flow, nil)
		if scope != nil || enterErr != primary {
			t.Fatalf("enterFlow() = %#v, %v; want nil and primary PutEnv error", scope, enterErr)
		}
		if _, configErr := session.currentConfig(); configErr == nil {
			t.Fatal("failed flow left an active config frame")
		}
		want := []string{"push", "put:A=one", "put:B=two", "pop"}
		if got := runtime.EnvCalls(); !reflect.DeepEqual(got, want) {
			t.Fatalf("env calls = %#v, want %#v", got, want)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("session Close() error: %v", err)
		}
	})

	t.Run("post put cancellation prevents later variables", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(context.Background())
		runtime := &sessionRuntime{afterPutName: "A", afterPut: cancel}
		flow := &compiledFlow{path: "root.yaml", config: model.Config{Env: map[string]string{
			"B": "two", "A": "one",
		}}}
		session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, flow)
		if err != nil {
			t.Fatalf("newExecutionSession() error: %v", err)
		}
		scope, enterErr := session.enterFlow(ctx, flow, nil)
		if scope != nil || !errors.Is(enterErr, context.Canceled) {
			t.Fatalf("enterFlow() = %#v, %v; want nil and context.Canceled", scope, enterErr)
		}
		want := []string{"push", "put:A=one", "pop"}
		if got := runtime.EnvCalls(); !reflect.DeepEqual(got, want) {
			t.Fatalf("env calls = %#v, want %#v", got, want)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("session Close() error: %v", err)
		}
	})

	t.Run("cancellation before frame append rolls back", func(t *testing.T) {
		t.Parallel()
		runtime := &sessionRuntime{}
		flow := &compiledFlow{path: "root.yaml", config: model.Config{Env: map[string]string{"A": "one"}}}
		session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, flow)
		if err != nil {
			t.Fatalf("newExecutionSession() error: %v", err)
		}
		ctx := &errAfterContext{allowCalls: 4}
		scope, enterErr := session.enterFlow(ctx, flow, nil)
		if scope != nil || !errors.Is(enterErr, context.Canceled) {
			t.Fatalf("enterFlow() = %#v, %v; want cancellation before frame append", scope, enterErr)
		}
		want := []string{"push", "put:A=one", "pop"}
		if got := runtime.EnvCalls(); !reflect.DeepEqual(got, want) {
			t.Fatalf("env calls = %#v, want %#v", got, want)
		}
		if _, configErr := session.currentConfig(); configErr == nil {
			t.Fatal("cancelled flow left an active config frame")
		}
		if err := session.Close(); err != nil {
			t.Fatalf("session Close() error: %v", err)
		}
	})

	t.Run("out of order close is retryable", func(t *testing.T) {
		t.Parallel()
		runtime := &sessionRuntime{}
		root := &compiledFlow{path: "root.yaml"}
		child := &compiledFlow{path: "child.yaml"}
		session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, root)
		if err != nil {
			t.Fatalf("newExecutionSession() error: %v", err)
		}
		rootScope, err := session.enterFlow(context.Background(), root, nil)
		if err != nil {
			t.Fatalf("enter root error: %v", err)
		}
		childScope, err := session.enterFlow(context.Background(), child, nil)
		if err != nil {
			t.Fatalf("enter child error: %v", err)
		}
		if err := rootScope.Close(); err == nil {
			t.Fatal("out-of-order root Close() error = nil")
		}
		// The invariant is that no scope was popped, not a call count: the
		// engine also injects a FLOWBATON_FILENAME put per scope, and counting
		// made this assertion break for a reason it does not care about.
		if calls := runtime.EnvCalls(); slices.Contains(calls, "pop") {
			t.Fatalf("out-of-order close reached runtime: %#v", calls)
		}
		if err := childScope.Close(); err != nil {
			t.Fatalf("child Close() error: %v", err)
		}
		if err := rootScope.Close(); err != nil {
			t.Fatalf("retried root Close() error: %v", err)
		}
		want := concatEnvCalls(
			flowEnvCalls("root.yaml"), flowEnvCalls("child.yaml"), []string{"pop", "pop"})
		if got := runtime.EnvCalls(); !reflect.DeepEqual(got, want) {
			t.Fatalf("env calls = %#v, want %#v", got, want)
		}
		if err := session.Close(); err != nil {
			t.Fatalf("session Close() error: %v", err)
		}
	})
}

func TestExecutionSessionCloseWaitsForActiveFlowScopes(t *testing.T) {
	t.Parallel()

	runtime := &sessionRuntime{}
	root := &compiledFlow{path: "root.yaml"}
	session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	scope, err := session.enterFlow(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("enterFlow() error: %v", err)
	}
	if err := session.Close(); err == nil {
		t.Fatal("Close with active flow error = nil")
	}
	_, _, closeCalls := runtime.Snapshot()
	if closeCalls != 0 {
		t.Fatalf("runtime Close calls with active flow = %d, want zero", closeCalls)
	}
	if err := scope.Close(); err != nil {
		t.Fatalf("flow scope Close() error: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("retry session Close() error: %v", err)
	}
	_, _, closeCalls = runtime.Snapshot()
	if closeCalls != 1 {
		t.Fatalf("runtime Close calls = %d, want one", closeCalls)
	}
}

func TestExecutionSessionExposesRootStateToHandlersAndMirrorsCopiedText(t *testing.T) {
	t.Parallel()

	runtime := &sessionRuntime{}
	root := &compiledFlow{path: "root.yaml", config: model.Config{AppID: "com.example.root"}}
	session, err := newExecutionSessionWithRoot(t, context.Background(), runtime, root)
	if err != nil {
		t.Fatalf("newExecutionSession() error: %v", err)
	}
	if got, err := session.copiedTextValue(); err != nil || got != "" {
		t.Fatalf("initial copiedTextValue() = %q, %v", got, err)
	}
	scope, err := session.enterFlow(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("enterFlow() error: %v", err)
	}

	state := &session.core.state
	if got, err := state.jsRuntime(); err != nil || got != runtime {
		t.Fatalf("state.jsRuntime() = %T, %v; want session runtime", got, err)
	}
	if got, err := state.elementLookup(); err != nil || got != session.lookup {
		t.Fatalf("state.elementLookup() = %p, %v; want session lookup %p", got, err, session.lookup)
	}
	if got, err := state.activeAppID(); err != nil || got != "com.example.root" {
		t.Fatalf("state.activeAppID() = %q, %v", got, err)
	}
	config, err := state.activeConfig()
	if err != nil || config.AppID != "com.example.root" {
		t.Fatalf("state.activeConfig() = %#v, %v", config, err)
	}
	if err := state.setCopiedText("checkout code"); err != nil {
		t.Fatalf("state.setCopiedText() error: %v", err)
	}
	if got, err := state.copiedTextValue(); err != nil || got != "checkout code" {
		t.Fatalf("state.copiedTextValue() = %q, %v", got, err)
	}
	if got, err := session.copiedTextValue(); err != nil || got != "checkout code" {
		t.Fatalf("session copiedTextValue() = %q, %v", got, err)
	}

	runtime.SetCopiedError(errors.New("copied text update failed"))
	poison := state.setCopiedText("must not stick")
	if poison == nil {
		t.Fatal("failed state.setCopiedText() error = nil")
	}
	session.mu.Lock()
	retained := session.copiedText
	session.mu.Unlock()
	if retained != "checkout code" {
		t.Fatalf("failed update changed copied text to %q", retained)
	}
	if _, err := state.copiedTextValue(); err != poison {
		t.Fatalf("copiedTextValue() error = %T %v, want exact poison", err, err)
	}
	if err := scope.Close(); err != poison {
		t.Fatalf("scope Close() error = %T %v, want exact poison", err, err)
	}
	if _, err := state.activeConfig(); err != poison {
		t.Fatalf("activeConfig after poison error = %T %v, want exact poison", err, err)
	}
	runtime.SetCopiedError(nil)
	if err := session.Close(); err != poison {
		t.Fatalf("session Close() error = %T %v, want exact poison", err, err)
	}
	if _, err := state.jsRuntime(); err != poison {
		t.Fatalf("state.jsRuntime after poison error = %T %v, want exact poison", err, err)
	}
	if _, err := state.copiedTextValue(); err != poison {
		t.Fatalf("state.copiedTextValue after poison error = %T %v, want exact poison", err, err)
	}
}

func newTestExecutionSession(t *testing.T, ctx context.Context, runtime *sessionRuntime) (*executionSession, error) {
	t.Helper()
	return newExecutionSessionWithRoot(t, ctx, runtime, &compiledFlow{path: "root.yaml"})
}

func newExecutionSessionWithRoot(
	t *testing.T,
	ctx context.Context,
	runtime *sessionRuntime,
	root *compiledFlow,
) (*executionSession, error) {
	t.Helper()
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
		Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 1080, HeightGrid: 1920},
	}}})
	return newExecutionSession(ctx, Dependencies{
		Driver: driver, Clock: enginetest.NewFakeClock(time.Unix(0, 0)),
		JSFactory: &sessionRuntimeFactory{runtime: runtime}, Controller: NoopController{},
	}, root)
}

type sessionRuntimeFactory struct {
	mu      sync.Mutex
	runtime js.Runtime
	err     error
	calls   int
}

func (factory *sessionRuntimeFactory) NewRuntime() (js.Runtime, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	factory.calls++
	return factory.runtime, factory.err
}

func (factory *sessionRuntimeFactory) Calls() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return factory.calls
}

type sessionRuntime struct {
	mu               sync.Mutex
	platforms        []string
	copiedValues     []string
	closeCalls       int
	platformErr      error
	copiedErr        error
	closeErr         error
	afterPlatform    func()
	envCalls         []string
	failPutName      string
	putErr           error
	afterPutName     string
	afterPut         func()
	popErr           error
	afterCopiedValue string
	afterCopied      func()
	environment      map[string]string
	envStack         []map[string]string
}

func (runtime *sessionRuntime) Evaluate(context.Context, js.EvalRequest) (js.Result, error) {
	return js.Result{}, nil
}

func (runtime *sessionRuntime) Interpolate(context.Context, string, map[string]any) (string, error) {
	return "", nil
}

func (runtime *sessionRuntime) PushEnv() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.envCalls = append(runtime.envCalls, "push")
	runtime.envStack = append(runtime.envStack, cloneStringMap(runtime.environment))
	return nil
}

func (runtime *sessionRuntime) PopEnv() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.envCalls = append(runtime.envCalls, "pop")
	if last := len(runtime.envStack) - 1; last >= 0 {
		runtime.environment = runtime.envStack[last]
		runtime.envStack = runtime.envStack[:last]
	}
	return runtime.popErr
}

func (runtime *sessionRuntime) PutEnv(name string, value any) error {
	runtime.mu.Lock()
	runtime.envCalls = append(runtime.envCalls, "put:"+name+"="+value.(string))
	if runtime.environment == nil {
		runtime.environment = make(map[string]string)
	}
	runtime.environment[name] = value.(string)
	err := error(nil)
	if runtime.failPutName == name {
		err = runtime.putErr
	}
	after := func() {}
	if runtime.afterPutName == name && runtime.afterPut != nil {
		after = runtime.afterPut
	}
	runtime.mu.Unlock()
	after()
	return err
}

func (runtime *sessionRuntime) CurrentEnvironment() map[string]string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return cloneStringMap(runtime.environment)
}

func (runtime *sessionRuntime) SetCopiedText(value string) error {
	runtime.mu.Lock()
	runtime.copiedValues = append(runtime.copiedValues, value)
	err := runtime.copiedErr
	after := func() {}
	if runtime.afterCopiedValue == value && runtime.afterCopied != nil {
		after = runtime.afterCopied
	}
	runtime.mu.Unlock()
	after()
	return err
}

func (runtime *sessionRuntime) SetPlatform(value string) error {
	runtime.mu.Lock()
	runtime.platforms = append(runtime.platforms, value)
	err := runtime.platformErr
	after := runtime.afterPlatform
	runtime.mu.Unlock()
	if after != nil {
		after()
	}
	return err
}

func (runtime *sessionRuntime) SetLogSink(func(string)) {}

func (runtime *sessionRuntime) PushLogSink(func(string)) func() { return func() {} }

func (runtime *sessionRuntime) Close() error {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.closeCalls++
	return runtime.closeErr
}

func (runtime *sessionRuntime) Snapshot() ([]string, []string, int) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]string(nil), runtime.platforms...), append([]string(nil), runtime.copiedValues...), runtime.closeCalls
}

func (runtime *sessionRuntime) EnvCalls() []string {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return append([]string(nil), runtime.envCalls...)
}

func (runtime *sessionRuntime) SetCopiedError(err error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	runtime.copiedErr = err
}

type errAfterContext struct {
	context.Context
	allowCalls int64
	calls      atomic.Int64
}

func (ctx *errAfterContext) Deadline() (time.Time, bool) { return time.Time{}, false }

func (ctx *errAfterContext) Done() <-chan struct{} { return nil }

func (ctx *errAfterContext) Err() error {
	if ctx.calls.Add(1) > ctx.allowCalls {
		return context.Canceled
	}
	return nil
}

func (ctx *errAfterContext) Value(any) any { return nil }
