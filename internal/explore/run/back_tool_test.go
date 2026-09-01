package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func boxFor(t *testing.T, features map[string]bool) explore.ToolBox {
	t.Helper()
	state := makeState("app", screen("Home", button("Login", "login_button", "[0,0][100,50]")))
	session, err := newToolSession(toolDeps{
		driver:   &fakeDriver{features: features},
		observer: &fakeObserver{states: []*explore.ScreenState{state}},
		appID:    "app",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	return session.box()
}

func offers(box explore.ToolBox, name string) bool {
	return slices.ContainsFunc(box.Specs, func(spec explore.ToolSpec) bool { return spec.Name == name })
}

func describes(t *testing.T, box explore.ToolBox, name string) string {
	t.Helper()
	for _, spec := range box.Specs {
		if spec.Name == name {
			return spec.Description
		}
	}
	t.Fatalf("no %q tool", name)
	return ""
}

// A tool the driver cannot perform is not offered. iOS has no platform back
// gesture (drivercontract declares backPress false for both iOS surfaces and
// internal/ios answers device.ErrUnsupported), yet the toolbox advertised
// back on every platform. Session mmx61 on Calendar spent steps on it in two
// scenarios, one of which then ran out of budget, and the press_key text sent
// the model there: "use back to leave a screen".
func TestBoxOffersBackOnlyWhereTheDriverPressesIt(t *testing.T) {
	t.Parallel()

	without := boxFor(t, nil)
	if offers(without, "back") {
		t.Error("back offered to a driver that declares no backPress")
	}
	if _, found := without.Handlers["back"]; found {
		t.Error("back handler wired to a driver that declares no backPress")
	}
	if hint := describes(t, without, "press_key"); strings.Contains(hint, "back") {
		t.Errorf("press_key sends the model to a tool it does not have: %q", hint)
	}

	with := boxFor(t, map[string]bool{"backPress": true})
	if !offers(with, "back") {
		t.Error("back withheld from a driver that presses it")
	}
	if _, found := with.Handlers["back"]; !found {
		t.Error("back handler missing from a driver that presses it")
	}
	if hint := describes(t, with, "press_key"); !strings.Contains(hint, "back") {
		t.Errorf("press_key no longer names back where it works: %q", hint)
	}
}
