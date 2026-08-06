package engine

import (
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// specs/06-launch-app-semantics.md defines the authored surface, step order,
// and driver mapping for launchApp.

func TestLaunchAppRunsItsStepsInContractOrder(t *testing.T) {
	t.Parallel()

	driver := launchAppDriver(nil)
	command := fullLaunchAppCommand()
	if _, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), command, "launch-order"); err != nil {
		t.Fatalf("execute(launchApp) error = %T %v", err, err)
	}
	// spec 06 section 2: keychain, then state, then permissions, then the
	// launch — which stops the app first because stopApp defaults to true.
	want := []enginetest.Method{
		enginetest.MethodClearKeychain,
		enginetest.MethodClearAppState,
		enginetest.MethodSetPermissions,
		enginetest.MethodStopApp,
		enginetest.MethodLaunchApp,
	}
	if got := launchAppMethods(driver); !reflect.DeepEqual(got, want) {
		t.Fatalf("driver calls = %v, want %v", got, want)
	}
}

func TestLaunchAppSkipsEveryUnauthoredStep(t *testing.T) {
	t.Parallel()

	// An unauthored permissions map means no permission call at all, and
	// clearState/clearKeychain default to false. stopApp is the one that
	// defaults ON, so a bare launchApp still stops before launching.
	driver := launchAppDriver(nil)
	command := model.Command{Kind: model.CommandLaunchApp, Form: model.CommandFormScalar}
	if _, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), command, "launch-bare"); err != nil {
		t.Fatalf("execute(launchApp) error = %T %v", err, err)
	}
	want := []enginetest.Method{enginetest.MethodStopApp, enginetest.MethodLaunchApp}
	if got := launchAppMethods(driver); !reflect.DeepEqual(got, want) {
		t.Fatalf("driver calls = %v, want %v", got, want)
	}
}

func TestLaunchAppHonoursStopAppFalse(t *testing.T) {
	t.Parallel()

	driver := launchAppDriver(nil)
	command := launchAppCommand(map[string]any{"stopApp": false})
	if _, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), command, "launch-no-stop"); err != nil {
		t.Fatalf("execute(launchApp) error = %T %v", err, err)
	}
	want := []enginetest.Method{enginetest.MethodLaunchApp}
	if got := launchAppMethods(driver); !reflect.DeepEqual(got, want) {
		t.Fatalf("driver calls = %v, want %v", got, want)
	}
}

func TestLaunchAppSendsTypedArgumentsSortedByKey(t *testing.T) {
	t.Parallel()

	driver := launchAppDriver(nil)
	command := launchAppCommand(map[string]any{"arguments": map[string]any{
		"mode": "probe", "count": int64(7), "ratio": 1.5, "flag": true,
	}})
	if _, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), command, "launch-args"); err != nil {
		t.Fatalf("execute(launchApp) error = %T %v", err, err)
	}
	request := launchAppRequest(t, driver)
	// Sorted by key, because the parser hands over an unordered map and map
	// iteration order must never reach a driver request. The type vocabulary
	// is the one the public page names.
	want := []device.LaunchArgument{
		{Key: "count", Value: "7", Type: "integer"},
		{Key: "flag", Value: "true", Type: "boolean"},
		{Key: "mode", Value: "probe", Type: "string"},
		{Key: "ratio", Value: "1.5", Type: "double"},
	}
	if !reflect.DeepEqual(request.Arguments, want) {
		t.Fatalf("arguments = %#v, want %#v", request.Arguments, want)
	}
}

func TestLaunchAppTargetsTheExplicitAppIDEverywhere(t *testing.T) {
	t.Parallel()

	// Every step targets the same app: an appId that reached the launch but
	// not the state clear would clear the wrong app's data.
	driver := launchAppDriver(nil)
	command := launchAppCommand(map[string]any{
		"appId": "com.example.${'explicit'}", "clearState": true,
		"permissions": map[string]any{"camera": "allow"},
	})
	if _, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), command, "launch-appid"); err != nil {
		t.Fatalf("execute(launchApp) error = %T %v", err, err)
	}
	for _, action := range driver.Actions() {
		switch request := action.Request.(type) {
		case device.AppRequest:
			if request.AppID != "com.example.explicit" {
				t.Fatalf("%s app id = %q, want the interpolated explicit id", action.Method, request.AppID)
			}
		case device.PermissionsRequest:
			if request.AppID != "com.example.explicit" {
				t.Fatalf("setPermissions app id = %q, want the interpolated explicit id", request.AppID)
			}
		case device.LaunchAppRequest:
			if request.AppID != "com.example.explicit" {
				t.Fatalf("launchApp app id = %q, want the interpolated explicit id", request.AppID)
			}
		}
	}
}

func TestLaunchAppCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "unknown field", command: launchAppCommand(map[string]any{"mystery": true})},
		{name: "clearState wrong type", command: launchAppCommand(map[string]any{"clearState": "yes"})},
		{name: "permissions not an object", command: launchAppCommand(map[string]any{"permissions": "camera"})},
		{name: "permissions empty", command: launchAppCommand(map[string]any{"permissions": map[string]any{}})},
		{
			name:    "permission grant outside the exact set",
			command: launchAppCommand(map[string]any{"permissions": map[string]any{"camera": "maybe"}}),
		},
		{name: "arguments not an object", command: launchAppCommand(map[string]any{"arguments": "mode"})},
		{
			name:    "argument value of an unsupported type",
			command: launchAppCommand(map[string]any{"arguments": map[string]any{"list": []any{1}}}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileLaunchApp(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileLaunchApp() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

func TestLaunchAppPropagatesEachStepFailure(t *testing.T) {
	t.Parallel()

	// Every step is a real device mutation, so none of them may be swallowed.
	sentinel := errors.New("launch step boundary refused")
	for _, method := range []enginetest.Method{
		enginetest.MethodClearKeychain, enginetest.MethodClearAppState,
		enginetest.MethodSetPermissions, enginetest.MethodStopApp, enginetest.MethodLaunchApp,
	} {
		t.Run(string(method), func(t *testing.T) {
			driver := launchAppDriver(map[enginetest.Method]error{method: sentinel})
			_, err := runSingleCommandFlow(t, driver, launchAppRegistry(t), fullLaunchAppCommand(), "launch-fail")
			if !errors.Is(err, sentinel) {
				t.Fatalf("error = %T %v, want the exact %s cause", err, err, method)
			}
		})
	}
}

func fullLaunchAppCommand() model.Command {
	return launchAppCommand(map[string]any{
		"clearKeychain": true, "clearState": true, "stopApp": true,
		"permissions": map[string]any{"camera": "allow", "notifications": "deny"},
		"arguments":   map[string]any{"mode": "probe"},
	})
}

func launchAppCommand(arguments map[string]any) model.Command {
	return model.Command{Kind: model.CommandLaunchApp, Form: model.CommandFormObject, Arguments: arguments}
}

func launchAppRegistry(t testing.TB) handlerRegistry {
	t.Helper()
	registry, err := newHandlerRegistry(handlerSpec{
		keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation,
		postAction: postActionSettle, settleRequest: launchAppSettleRequest,
		compile: pureCompiler(compileLaunchApp), evaluate: evaluateLaunchApp, execute: executeLaunchApp,
	})
	if err != nil {
		t.Fatalf("newHandlerRegistry(launchApp) error = %v", err)
	}
	return registry
}

func launchAppDriver(failures map[enginetest.Method]error) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	settled := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"state": "ready"}}}
	fail := func(method enginetest.Method) []enginetest.Result[struct{}] {
		return []enginetest.Result[struct{}]{{Err: failures[method]}}
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
		}}},
		ClearKeychain:      fail(enginetest.MethodClearKeychain),
		ClearAppState:      fail(enginetest.MethodClearAppState),
		SetPermissions:     fail(enginetest.MethodSetPermissions),
		StopApp:            fail(enginetest.MethodStopApp),
		LaunchApp:          fail(enginetest.MethodLaunchApp),
		WaitForAppToSettle: []enginetest.Result[*device.ViewHierarchy]{{Value: settled}, {Value: settled}},
	})
	return driver
}

func launchAppMethods(driver *enginetest.FakeDriver) []enginetest.Method {
	var methods []enginetest.Method
	for _, action := range driver.Actions() {
		switch action.Method {
		case enginetest.MethodDeviceInfo, enginetest.MethodWaitForAppToSettle:
			continue
		}
		methods = append(methods, action.Method)
	}
	return methods
}

func launchAppRequest(t testing.TB, driver *enginetest.FakeDriver) device.LaunchAppRequest {
	t.Helper()
	for _, action := range driver.Actions() {
		if request, ok := action.Request.(device.LaunchAppRequest); ok {
			return request
		}
	}
	t.Fatal("driver was never asked to launch")
	return device.LaunchAppRequest{}
}
