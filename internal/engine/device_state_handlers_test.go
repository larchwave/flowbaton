package engine

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// These tests cover the five device-state handlers that map directly onto Driver
// methods. Travel uses its dedicated scheduled-location handler.

func TestDeviceStateHandlerSpecsComposeExactFive(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(deviceStateHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(device state) error = %v", err)
	}
	want := []string{"setAirplaneMode", "setLocation", "setOrientation", "setPermissions", "toggleAirplaneMode"}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, want) {
		t.Fatalf("device state registry = %#v, want %#v", got, want)
	}
	for _, keyword := range want {
		spec, _ := registry.lookup(model.CommandKeyword(keyword))
		if spec.effectClass != EffectDeviceMutation || spec.postAction != postActionNoSettle {
			t.Fatalf("%s spec = %#v, want device mutation with no settle", keyword, spec)
		}
	}
}

func TestSetLocationCompilesAndSendsExactCoordinates(t *testing.T) {
	t.Parallel()

	driver := deviceStateDriver(nil, false)
	command := model.Command{
		Kind: model.CommandSetLocation, Form: model.CommandFormObject,
		Arguments: map[string]any{"latitude": 43.6501, "longitude": -79.3801},
	}
	if _, err := runDeviceStateCommand(t, driver, command); err != nil {
		t.Fatalf("execute(setLocation) error = %T %v", err, err)
	}
	request, ok := deviceStateAction(t, driver).Request.(device.Location)
	if !ok {
		t.Fatalf("request = %#v, want device.Location", deviceStateAction(t, driver).Request)
	}
	if request.Latitude != 43.6501 || request.Longitude != -79.3801 {
		t.Fatalf("location = %#v, want the authored coordinates", request)
	}
}

func TestSetLocationAcceptsIntegerCoordinates(t *testing.T) {
	t.Parallel()

	// YAML integers decode as int64; a whole-number coordinate must still work.
	driver := deviceStateDriver(nil, false)
	command := model.Command{
		Kind: model.CommandSetLocation, Form: model.CommandFormObject,
		Arguments: map[string]any{"latitude": int64(43), "longitude": int64(-79)},
	}
	if _, err := runDeviceStateCommand(t, driver, command); err != nil {
		t.Fatalf("execute(setLocation) error = %v", err)
	}
	request := deviceStateAction(t, driver).Request.(device.Location)
	if request.Latitude != 43 || request.Longitude != -79 {
		t.Fatalf("location = %#v, want the integer coordinates", request)
	}
}

func TestSetOrientationSendsExactCanonicalValue(t *testing.T) {
	t.Parallel()

	for _, orientation := range []string{"PORTRAIT", "LANDSCAPE_LEFT", "LANDSCAPE_RIGHT", "UPSIDE_DOWN"} {
		t.Run(orientation, func(t *testing.T) {
			driver := deviceStateDriver(nil, false)
			command := model.Command{Kind: model.CommandSetOrientation, Form: model.CommandFormObject, Arguments: orientation}
			if _, err := runDeviceStateCommand(t, driver, command); err != nil {
				t.Fatalf("execute(setOrientation %s) error = %v", orientation, err)
			}
			got, ok := deviceStateAction(t, driver).Request.(device.Orientation)
			if !ok || string(got) != orientation {
				t.Fatalf("orientation = %#v, want %s", deviceStateAction(t, driver).Request, orientation)
			}
		})
	}
}

func TestSetAirplaneModeMapsEnableAndDisable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		authored string
		want     bool
	}{
		{authored: "enabled", want: true},
		{authored: "disabled", want: false},
	} {
		t.Run(test.authored, func(t *testing.T) {
			driver := deviceStateDriver(nil, false)
			command := model.Command{Kind: model.CommandSetAirplaneMode, Form: model.CommandFormObject, Arguments: test.authored}
			if _, err := runDeviceStateCommand(t, driver, command); err != nil {
				t.Fatalf("execute(setAirplaneMode %s) error = %v", test.authored, err)
			}
			request := deviceStateAction(t, driver).Request.(device.AirplaneModeRequest)
			if request.Enabled != test.want {
				t.Fatalf("airplane mode enabled = %v, want %v", request.Enabled, test.want)
			}
		})
	}
}

func TestToggleAirplaneModeInvertsTheReadState(t *testing.T) {
	t.Parallel()

	for _, current := range []bool{true, false} {
		t.Run(map[bool]string{true: "on to off", false: "off to on"}[current], func(t *testing.T) {
			driver := deviceStateDriver(nil, current)
			command := model.Command{Kind: model.CommandToggleAirplaneMode, Form: model.CommandFormScalar}
			if _, err := runDeviceStateCommand(t, driver, command); err != nil {
				t.Fatalf("execute(toggleAirplaneMode) error = %v", err)
			}
			// Toggle must read before writing, so it makes exactly two calls.
			calls := deviceStateActions(driver)
			if len(calls) != 2 ||
				calls[0].Method != enginetest.MethodIsAirplaneModeEnabled ||
				calls[1].Method != enginetest.MethodSetAirplaneMode {
				t.Fatalf("toggle calls = %#v, want a read then a write", calls)
			}
			request := calls[1].Request.(device.AirplaneModeRequest)
			if request.Enabled == current {
				t.Fatalf("toggle wrote %v while current was %v, want the inverse", request.Enabled, current)
			}
		})
	}
}

func TestSetPermissionsSendsActiveAppAndExactMap(t *testing.T) {
	t.Parallel()

	driver := deviceStateDriver(nil, false)
	command := model.Command{
		Kind: model.CommandSetPermissions, Form: model.CommandFormObject,
		Arguments: map[string]any{"permissions": map[string]any{"camera": "allow", "location": "unset", "notifications": "deny"}},
	}
	if _, err := runDeviceStateCommand(t, driver, command); err != nil {
		t.Fatalf("execute(setPermissions) error = %T %v", err, err)
	}
	request, ok := deviceStateAction(t, driver).Request.(device.PermissionsRequest)
	if !ok {
		t.Fatalf("request = %#v, want device.PermissionsRequest", deviceStateAction(t, driver).Request)
	}
	if request.AppID != deviceStateActiveAppID {
		t.Fatalf("setPermissions appId = %q, want the active %q", request.AppID, deviceStateActiveAppID)
	}
	want := map[string]string{"camera": "allow", "location": "unset", "notifications": "deny"}
	if !reflect.DeepEqual(request.Permissions, want) {
		t.Fatalf("permissions = %#v, want %#v", request.Permissions, want)
	}
}

func TestSetPermissionsDoesNotAliasTheAuthoredMap(t *testing.T) {
	t.Parallel()

	driver := deviceStateDriver(nil, false)
	grants := map[string]any{"camera": "allow"}
	authored := map[string]any{"permissions": grants}
	command := model.Command{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: authored}
	if _, err := runDeviceStateCommand(t, driver, command); err != nil {
		t.Fatalf("execute error = %v", err)
	}
	request := deviceStateAction(t, driver).Request.(device.PermissionsRequest)
	request.Permissions["camera"] = "mutated"
	if grants["camera"] != "allow" {
		t.Fatalf("authored map was mutated through the request: %#v", authored)
	}
}

func TestDeviceStateCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "setLocation missing longitude", command: model.Command{Kind: model.CommandSetLocation, Form: model.CommandFormObject, Arguments: map[string]any{"latitude": 1.0}}},
		{name: "setLocation unknown field", command: model.Command{Kind: model.CommandSetLocation, Form: model.CommandFormObject, Arguments: map[string]any{"latitude": 1.0, "longitude": 2.0, "altitude": 3.0}}},
		// Coordinate strings reach runtime numeric conversion. A boolean remains
		// invalid during compilation; the string failure path has separate coverage.
		{name: "setLocation wrong type", command: model.Command{Kind: model.CommandSetLocation, Form: model.CommandFormObject, Arguments: map[string]any{"latitude": true, "longitude": 2.0}}},
		{name: "setOrientation unknown value", command: model.Command{Kind: model.CommandSetOrientation, Form: model.CommandFormObject, Arguments: "SIDEWAYS"}},
		{name: "setOrientation unknown value", command: model.Command{Kind: model.CommandSetOrientation, Form: model.CommandFormObject, Arguments: "sideways"}},
		{name: "setAirplaneMode unknown value", command: model.Command{Kind: model.CommandSetAirplaneMode, Form: model.CommandFormObject, Arguments: "on"}},
		{name: "toggleAirplaneMode with a value", command: model.Command{Kind: model.CommandToggleAirplaneMode, Form: model.CommandFormObject, Arguments: "enable"}},
		{name: "setPermissions unknown grant", command: model.Command{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: map[string]any{"permissions": map[string]any{"camera": "maybe"}}}},
		{name: "setPermissions non-string grant", command: model.Command{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: map[string]any{"permissions": map[string]any{"camera": int64(1)}}}},
		{name: "setPermissions empty grant map", command: model.Command{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: map[string]any{"permissions": map[string]any{}}}},
		{name: "setPermissions missing permissions key", command: model.Command{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: map[string]any{}}},
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormScalar}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileDeviceState(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileDeviceState(%#v) = %#v, %T %v; want nil and ConfigurationError", test.command, compiled, err, err)
			}
		})
	}
}

func TestDeviceStatePropagatesDriverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("device state driver refused")
	for _, command := range []model.Command{
		{Kind: model.CommandSetLocation, Form: model.CommandFormObject, Arguments: map[string]any{"latitude": 1.0, "longitude": 2.0}},
		{Kind: model.CommandSetOrientation, Form: model.CommandFormObject, Arguments: "PORTRAIT"},
		{Kind: model.CommandSetAirplaneMode, Form: model.CommandFormObject, Arguments: "enabled"},
		{Kind: model.CommandToggleAirplaneMode, Form: model.CommandFormScalar},
		{Kind: model.CommandSetPermissions, Form: model.CommandFormObject, Arguments: map[string]any{"permissions": map[string]any{"camera": "allow"}}},
	} {
		t.Run(string(command.Kind), func(t *testing.T) {
			driver := deviceStateDriver(sentinel, false)
			_, err := runDeviceStateCommand(t, driver, command)
			if !errors.Is(err, sentinel) {
				t.Fatalf("execute(%s) error = %T %v, want the exact driver cause", command.Kind, err, err)
			}
		})
	}
}

const deviceStateActiveAppID = "com.example.devicestate"

func deviceStateDriver(failure error, airplaneOn bool) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	one := []enginetest.Result[struct{}]{{Err: failure}}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:            []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}}},
		SetLocation:           one,
		SetOrientation:        one,
		SetPermissions:        one,
		SetAirplaneMode:       one,
		IsAirplaneModeEnabled: []enginetest.Result[bool]{{Value: airplaneOn, Err: failure}},
	})
	return driver
}

func deviceStateActions(driver *enginetest.FakeDriver) []enginetest.Action {
	var found []enginetest.Action
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodDeviceInfo {
			continue
		}
		found = append(found, action)
	}
	return found
}

func deviceStateAction(t testing.TB, driver *enginetest.FakeDriver) enginetest.Action {
	t.Helper()
	found := deviceStateActions(driver)
	if len(found) != 1 {
		t.Fatalf("device state driver calls = %#v, want exactly one", found)
	}
	return found[0]
}

func runDeviceStateCommand(t testing.TB, driver *enginetest.FakeDriver, command model.Command) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(deviceStateHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(device state) error = %v", err)
	}
	return runSingleCommandFlowForApp(t, driver, registry, command, "devicestate", deviceStateActiveAppID)
}

// setLocation coordinates are string-typed and resolved at runtime, so
// interpolation must survive compilation.
func TestSetLocationInterpolatesItsCoordinates(t *testing.T) {
	t.Parallel()

	driver := deviceStateDriver(nil, false)
	command := model.Command{
		Kind: model.CommandSetLocation, Form: model.CommandFormObject,
		Arguments: map[string]any{"latitude": "${'48.5'}", "longitude": "${'2.25'}"},
	}
	if _, err := runDeviceStateCommand(t, driver, command); err != nil {
		t.Fatalf("execute(setLocation) error = %T %v", err, err)
	}
	request, ok := deviceStateAction(t, driver).Request.(device.Location)
	if !ok {
		t.Fatalf("request = %#v, want device.Location", deviceStateAction(t, driver).Request)
	}
	if request.Latitude != 48.5 || request.Longitude != 2.25 {
		t.Fatalf("location = %#v, want the interpolated coordinates", request)
	}
}

// Non-numeric interpolated coordinates fail before any driver request.
func TestSetLocationFailsOnACoordinateThatIsNotANumber(t *testing.T) {
	t.Parallel()

	for _, arguments := range []map[string]any{
		{"latitude": "abc", "longitude": "2.0"},
		{"latitude": "48.0", "longitude": "${'north'}"},
	} {
		command := model.Command{
			Kind: model.CommandSetLocation, Form: model.CommandFormObject, Arguments: arguments,
		}
		if _, err := runDeviceStateCommand(t, deviceStateDriver(nil, false), command); err == nil {
			t.Fatalf("setLocation(%v) error = nil, want a runtime failure", arguments)
		}
	}
}
