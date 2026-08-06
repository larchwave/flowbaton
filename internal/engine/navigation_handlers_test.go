package engine

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// Navigation handlers cover links, browser links, and animation settling.

func TestNavigationHandlerSpecsComposeExactThree(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(navigationHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(navigation) error = %v", err)
	}
	// openBrowser is openLink with the browser flag enabled.
	want := []string{"openBrowser", "openLink", "waitForAnimationToEnd"}
	if got := sortedHandlerKeywords(registry); !reflect.DeepEqual(got, want) {
		t.Fatalf("navigation registry = %#v, want %#v", got, want)
	}
	openLink, _ := registry.lookup(model.CommandOpenLink)
	if openLink.effectClass != EffectDeviceMutation || openLink.postAction != postActionNoSettle {
		t.Fatalf("openLink spec = %#v, want device mutation with no settle", openLink)
	}
	// Waiting for animation observes the screen; it mutates nothing, so it must
	// not declare a post-action policy at all.
	wait, _ := registry.lookup(model.CommandWaitForAnimationToEnd)
	if wait.effectClass != EffectObserved || wait.postAction != postActionUnspecified || wait.settleRequest != nil {
		t.Fatalf("waitForAnimationToEnd spec = %#v, want observed with no post action", wait)
	}
}

func TestOpenLinkCompileAcceptsStringAndObjectForms(t *testing.T) {
	t.Parallel()

	t.Run("string form carries only the contract", func(t *testing.T) {
		compiled, err := compileOpenLink(model.Command{
			Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://example.invalid/path",
		})
		if err != nil {
			t.Fatalf("compileOpenLink(string) error = %v", err)
		}
		payload := compiled.(openLinkCompiled)
		if payload.link != "https://example.invalid/path" || payload.autoVerify {
			t.Fatalf("compiled = %#v, want link only", payload)
		}
	})

	t.Run("object form carries every field", func(t *testing.T) {
		compiled, err := compileOpenLink(model.Command{
			Kind: model.CommandOpenLink, Form: model.CommandFormObject,
			Arguments: map[string]any{
				"link": "https://example.invalid/deep", "autoVerify": true, "browser": true,
			},
		})
		if err != nil {
			t.Fatalf("compileOpenLink(object) error = %v", err)
		}
		payload := compiled.(openLinkCompiled)
		if payload.link != "https://example.invalid/deep" || !payload.autoVerify || payload.browser != browserForced {
			t.Fatalf("compiled = %#v, want every authored field", payload)
		}
	})
}

func TestOpenLinkCompileRejectsMalformedCommands(t *testing.T) {
	t.Parallel()

	label := "labelled"
	for _, test := range []struct {
		name    string
		command model.Command
	}{
		{name: "wrong keyword", command: model.Command{Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: "https://x.invalid"}},
		{name: "bare form", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormScalar}},
		{name: "missing link", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: map[string]any{"autoVerify": true}}},
		{name: "blank link", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: ""}},
		{name: "unknown field", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: map[string]any{"link": "https://x.invalid", "browser": "chrome"}}},
		// appId is the driver method's parameter, not an authored field; the
		// contract refuses it as an unknown property.
		{name: "driver-level appId", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: map[string]any{"link": "https://x.invalid", "appId": "com.example.open"}}},
		{name: "link wrong type", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: map[string]any{"link": int64(1)}}},
		{name: "autoVerify wrong type", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: map[string]any{"link": "https://x.invalid", "autoVerify": "yes"}}},
		{name: "with children", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://x.invalid", Children: []model.Command{{Kind: model.CommandBack, Form: model.CommandFormScalar}}}},
		{name: "with label", command: model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://x.invalid", Label: &label}},
	} {
		t.Run(test.name, func(t *testing.T) {
			compiled, err := compileOpenLink(test.command)
			if compiled != nil || !isConfigurationError(err) {
				t.Fatalf("compileOpenLink(%#v) = %#v, %T %v; want nil and ConfigurationError", test.command, compiled, err, err)
			}
		})
	}
}

func TestOpenLinkExecuteSendsExactRequestAndInterpolates(t *testing.T) {
	t.Parallel()

	driver := navigationDriver(nil, true)
	command := model.Command{
		Kind: model.CommandOpenLink, Form: model.CommandFormObject,
		Arguments: map[string]any{
			"link": "https://example.invalid/${'deep'}", "autoVerify": true,
		},
	}
	if _, err := runNavigationCommand(t, driver, command); err != nil {
		t.Fatalf("execute(openLink) error = %T %v", err, err)
	}
	action := navigationAction(t, driver)
	request, ok := action.Request.(device.OpenLinkRequest)
	if !ok {
		t.Fatalf("openLink request = %#v, want device.OpenLinkRequest", action.Request)
	}
	want := device.OpenLinkRequest{
		Link: "https://example.invalid/deep", AutoVerify: true,
	}
	if request != want {
		t.Fatalf("openLink request = %#v, want %#v", request, want)
	}
}

func TestOpenLinkNeverCarriesAnAppID(t *testing.T) {
	t.Parallel()

	// The driver request has an optional app id (specs/02-device-drivers.md:9),
	// but the authored command does not expose it. The request must keep it empty.
	driver := navigationDriver(nil, true)
	command := model.Command{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://example.invalid/plain"}
	if _, err := runNavigationCommand(t, driver, command); err != nil {
		t.Fatalf("execute(openLink) error = %v", err)
	}
	request := navigationAction(t, driver).Request.(device.OpenLinkRequest)
	if request.AppID != "" {
		t.Fatalf("openLink appId = %q, want empty when unauthored", request.AppID)
	}
}

func TestWaitForAnimationToEndUsesAuthoredOrDefaultTimeout(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command model.Command
		want    int64
	}{
		{
			name:    "bare uses the declared default",
			command: model.Command{Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormScalar},
			want:    animationTimeoutMillis,
		},
		{
			name: "authored timeout wins",
			command: model.Command{
				Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormObject,
				Arguments: map[string]any{"timeout": int64(2500)},
			},
			want: 2500,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := navigationDriver(nil, true)
			if _, err := runNavigationCommand(t, driver, test.command); err != nil {
				t.Fatalf("execute(waitForAnimationToEnd) error = %v", err)
			}
			request, ok := navigationAction(t, driver).Request.(device.ScreenStaticRequest)
			if !ok {
				t.Fatalf("request = %#v, want device.ScreenStaticRequest", navigationAction(t, driver).Request)
			}
			if request.TimeoutMillis != test.want {
				t.Fatalf("timeout = %d, want %d", request.TimeoutMillis, test.want)
			}
		})
	}
}

func TestWaitForAnimationToEndDefaultMatchesSpecConstant(t *testing.T) {
	t.Parallel()

	// specs/04-wire-protocols.md pins ANIMATION_TIMEOUT_MS = 15000.
	if animationTimeoutMillis != 15000 {
		t.Fatalf("animation timeout = %d, want the declared 15000", animationTimeoutMillis)
	}
}

func TestWaitForAnimationToEndTreatsNonStaticScreenAsCompletion(t *testing.T) {
	t.Parallel()

	// The driver reports the screen never became static. This is a wait, not an
	// assertion, so it completes rather than failing the flow.
	driver := navigationDriver(nil, false)
	result, err := runNavigationCommand(t, driver, model.Command{
		Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormScalar,
	})
	if err != nil {
		t.Fatalf("execute error = %T %v, want completion on a non-static screen", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
}

func TestNavigationPropagatesDriverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("navigation driver refused")
	for _, command := range []model.Command{
		{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "https://example.invalid/fail"},
		{Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormScalar},
	} {
		t.Run(string(command.Kind), func(t *testing.T) {
			driver := navigationDriver(sentinel, true)
			_, err := runNavigationCommand(t, driver, command)
			if !errors.Is(err, sentinel) {
				t.Fatalf("execute(%s) error = %T %v, want the exact driver cause", command.Kind, err, err)
			}
		})
	}
}

func navigationDriver(failure error, static bool) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:              []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884}}},
		OpenLink:                []enginetest.Result[struct{}]{{Err: failure}},
		WaitUntilScreenIsStatic: []enginetest.Result[bool]{{Value: static, Err: failure}},
	})
	return driver
}

func navigationAction(t testing.TB, driver *enginetest.FakeDriver) enginetest.Action {
	t.Helper()
	var found []enginetest.Action
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodDeviceInfo {
			continue
		}
		found = append(found, action)
	}
	if len(found) != 1 {
		t.Fatalf("navigation driver calls = %#v, want exactly one", found)
	}
	return found[0]
}

func runNavigationCommand(t testing.TB, driver *enginetest.FakeDriver, command model.Command) (FlowResult, error) {
	t.Helper()
	registry, err := newHandlerRegistry(navigationHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry(navigation) error = %v", err)
	}
	return runSingleCommandFlow(t, driver, registry, command, "navigation")
}
