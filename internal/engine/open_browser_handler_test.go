package engine

import (
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// specs/05-command-semantics-addendum.md §1.2 defines openBrowser as openLink
// with the boolean browser flag enabled.

func TestOpenBrowserHandlerSpecShape(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(openBrowserHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(openBrowser) error = %v", err)
	}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, []string{"openBrowser"}) {
		t.Fatalf("registry = %#v, want exactly openBrowser", got)
	}
	spec, _ := registry.lookup(model.CommandOpenBrowser)
	if spec.effectClass != EffectDeviceMutation {
		t.Fatalf("effect class = %v, want %v", spec.effectClass, EffectDeviceMutation)
	}
	// openLink does not settle, and openBrowser is openLink with one flag set.
	if spec.postAction != postActionNoSettle {
		t.Fatalf("post action = %v, want no-settle", spec.postAction)
	}
}

func TestOpenBrowserForcesTheBrowserFlagAndNothingElse(t *testing.T) {
	t.Parallel()

	driver := openBrowserDriver(nil)
	command := model.Command{
		Kind: model.CommandOpenBrowser, Form: model.CommandFormObject,
		Arguments: "https://example.com/${'docs'}",
	}
	result, err := runSingleCommandFlow(t, driver, openBrowserRegistry(t), command, "open-browser")
	if err != nil {
		t.Fatalf("execute(openBrowser) error = %T %v", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	request, ok := openBrowserAction(t, driver).Request.(device.OpenLinkRequest)
	if !ok {
		t.Fatalf("request = %#v, want device.OpenLinkRequest", openBrowserAction(t, driver).Request)
	}
	want := device.OpenLinkRequest{Link: "https://example.com/docs", Browser: browserForced}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("request = %#v, want %#v", request, want)
	}
}

func TestOpenLinkCarriesTheAuthoredBrowserFlag(t *testing.T) {
	t.Parallel()

	// spec 05 §3.1 requires an authored browser flag to reach the driver.
	for _, test := range []struct {
		name      string
		arguments any
		want      device.Browser
	}{
		{
			name:      "browser true",
			arguments: map[string]any{"link": "https://example.com", "browser": true},
			want:      browserForced,
		},
		{
			name:      "browser false",
			arguments: map[string]any{"link": "https://example.com", "browser": false},
			want:      "",
		},
		{
			name:      "browser unauthored",
			arguments: map[string]any{"link": "https://example.com"},
			want:      "",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := openBrowserDriver(nil)
			registry, err := newHandlerRegistry(navigationHandlerSpecs()...)
			if err != nil {
				t.Fatalf("newHandlerRegistry(navigation) error = %v", err)
			}
			command := model.Command{
				Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: test.arguments,
			}
			if _, err := runSingleCommandFlow(t, driver, registry, command, "open-link-"+test.name); err != nil {
				t.Fatalf("execute(openLink) error = %T %v", err, err)
			}
			request := openBrowserAction(t, driver).Request.(device.OpenLinkRequest)
			if request.Browser != test.want {
				t.Fatalf("browser = %q, want %q", request.Browser, test.want)
			}
		})
	}
}

func TestOpenLinkLeavesTheBrowserFlagUnset(t *testing.T) {
	t.Parallel()

	// A scalar openLink request leaves the browser flag unset.
	driver := openBrowserDriver(nil)
	registry, err := newHandlerRegistry(navigationHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(navigation) error = %v", err)
	}
	command := model.Command{
		Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://example.com",
	}
	if _, err := runSingleCommandFlow(t, driver, registry, command, "open-link"); err != nil {
		t.Fatalf("execute(openLink) error = %T %v", err, err)
	}
	request := openBrowserAction(t, driver).Request.(device.OpenLinkRequest)
	if request.Browser != "" {
		t.Fatalf("openLink browser = %q, want the unset zero value", request.Browser)
	}
}

func TestOpenBrowserCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormScalar}},
		{name: "bare scalar", command: model.Command{Kind: model.CommandOpenBrowser, Form: model.CommandFormScalar}},
		{name: "blank link", command: model.Command{
			Kind: model.CommandOpenBrowser, Form: model.CommandFormObject, Arguments: "  ",
		}},
		// openBrowser requires a scalar URL.
		{name: "object form", command: model.Command{
			Kind: model.CommandOpenBrowser, Form: model.CommandFormObject,
			Arguments: map[string]any{"link": "https://example.com"},
		}},
		{name: "with children", command: model.Command{
			Kind: model.CommandOpenBrowser, Form: model.CommandFormObject, Arguments: "https://example.com",
			Children: []model.Command{{Kind: model.CommandBack, Form: model.CommandFormScalar}},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileOpenBrowser(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileOpenBrowser() = %#v, %T %v; want nil and ConfigurationError", compiled, err, err)
			}
		})
	}
}

func TestOpenBrowserPropagatesDriverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("open link boundary refused")
	command := model.Command{
		Kind: model.CommandOpenBrowser, Form: model.CommandFormObject, Arguments: "https://example.com",
	}
	_, err := runSingleCommandFlow(t, openBrowserDriver(sentinel), openBrowserRegistry(t), command, "open-browser-fail")
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %T %v, want the exact driver cause", err, err)
	}
}

func openBrowserRegistry(t testing.TB) handlerRegistry {
	t.Helper()
	registry, err := newHandlerRegistry(openBrowserHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry(openBrowser) error = %v", err)
	}
	return registry
}

func openBrowserDriver(failure error) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{
			Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884,
		}}},
		OpenLink: []enginetest.Result[struct{}]{{Err: failure}},
	})
	return driver
}

func openBrowserAction(t testing.TB, driver *enginetest.FakeDriver) enginetest.Action {
	t.Helper()
	var found []enginetest.Action
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodDeviceInfo {
			continue
		}
		found = append(found, action)
	}
	if len(found) != 1 {
		t.Fatalf("driver calls = %#v, want exactly one", found)
	}
	return found[0]
}
