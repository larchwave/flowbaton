package run

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

func textField(id string, secure, focused bool) device.TreeNode {
	attrs := map[string]string{
		"class":       "android.widget.EditText",
		"resource-id": id,
		"bounds":      "[0,10][200,40]",
		"password":    "false",
	}
	if secure {
		attrs["password"] = "true"
	}
	yes := focused
	return device.TreeNode{Attributes: attrs, Focused: &yes}
}

func inputSession(t *testing.T, root device.TreeNode) (*toolSession, *fakeDriver) {
	t.Helper()
	state := makeState("app", root)
	driver := &fakeDriver{}
	session, err := newToolSession(toolDeps{
		driver:   driver,
		observer: &fakeObserver{states: []*explore.ScreenState{state}},
		appID:    "app",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	session.record = true
	return session, driver
}

func TestInputTextRecordingMasksSecureScreens(t *testing.T) {
	cases := []struct {
		name     string
		root     device.TreeNode
		wantText string
	}{
		{
			"focused secure field masks",
			screen("Login", textField("user", false, false), textField("pw", true, true)),
			"***",
		},
		{
			"secure present, nothing focused, masks",
			screen("Login", textField("user", false, false), textField("pw", true, false)),
			"***",
		},
		{
			"focused plain field beside secure stays raw",
			screen("Login", textField("user", false, true), textField("pw", true, false)),
			"hunter2",
		},
		{
			"no secure field stays raw",
			screen("Search", textField("query", false, true)),
			"hunter2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, driver := inputSession(t, tc.root)
			handler := session.box().Handlers["input_text"]
			if _, err := handler(context.Background(), json.RawMessage(`{"text":"hunter2"}`)); err != nil {
				t.Fatal(err)
			}
			if len(session.recording) != 1 {
				t.Fatalf("recording lines: %v", session.recording)
			}
			wantLine := `input_text {"text":"` + tc.wantText + `"}`
			if tc.wantText == explore.MaskedText {
				wantLine = `input_text {"masked":true}`
			}
			if session.recording[0] != wantLine {
				t.Fatalf("recorded %q, want %q", session.recording[0], wantLine)
			}
			if got := session.steps[0].Action.Text; got != tc.wantText {
				t.Fatalf("step action text %q, want %q", got, tc.wantText)
			}
			// The device must always receive the real text; only records mask.
			if !slices.Contains(driver.calls, "InputText:hunter2") {
				t.Fatalf("driver calls %v missed the raw text", driver.calls)
			}
			if tc.wantText == "***" && strings.Contains(strings.Join(session.recording, "\n"), "hunter2") {
				t.Fatal("raw text leaked into the recording")
			}
		})
	}
}

func TestInputTextRedactsDriverErrorOnSecureScreens(t *testing.T) {
	session, driver := inputSession(t, screen("Login", textField("pw", true, true)))
	driver.inputErr = errors.New(`no focused field accepts "hunter2"`)
	handler := session.box().Handlers["input_text"]
	if _, err := handler(context.Background(), json.RawMessage(`{"text":"hunter2"}`)); err != nil {
		t.Fatal(err)
	}
	step := session.steps[0]
	if step.Status != explore.StepFailed {
		t.Fatalf("step status %s, want failed", step.Status)
	}
	if strings.Contains(step.ErrText, "hunter2") {
		t.Fatalf("raw text leaked into ErrText: %q", step.ErrText)
	}
	if !strings.Contains(step.ErrText, explore.MaskedText) {
		t.Fatalf("ErrText %q carries no mask", step.ErrText)
	}
}

func TestReplayRefusesLiteralMaskInput(t *testing.T) {
	// Recordings written before the {"masked":true} marker used the literal
	// mask as text. Refusing both shapes costs one worker fallback when a
	// user really typed "***"; replaying a masked secret costs a login.
	session, driver := inputSession(t, screen("Search", textField("query", false, true)))
	if session.replay(context.Background(), session.box(), `input_text {"text":"***"}`) {
		t.Fatal("replay accepted a literal-mask recording")
	}
	if slices.Contains(driver.calls, "InputText:***") {
		t.Fatalf("literal mask reached the device: %v", driver.calls)
	}
}

func TestReplayRefusesMaskedInput(t *testing.T) {
	session, driver := inputSession(t, screen("Login", textField("pw", true, true)))
	body := "tap {\"text\":\"Login\"}\ninput_text {\"masked\":true}"
	if session.replay(context.Background(), session.box(), body) {
		t.Fatal("replay accepted a masked recording")
	}
	if slices.Contains(driver.calls, "InputText:***") {
		t.Fatalf("masked text reached the device: %v", driver.calls)
	}
}

// The target schema offers eidx to every target tool, and a live tester
// (2026-08-28) called check_visible with it seven times in one turn; each
// call failed with "needs text or id". An index from the newest table is a
// fine thing to check: it is visible exactly when the table still lists it.
func TestCheckVisibleAcceptsAnElementIndex(t *testing.T) {
	t.Parallel()
	session, _ := inputSession(t, screen("Login", textField("user", false, false)))
	reply, err := session.handleCheckVisible(context.Background(), json.RawMessage(`{"eidx":0}`))
	if err != nil || !strings.HasPrefix(reply, "visible: ") {
		t.Fatalf("reply %q, err %v", reply, err)
	}
	if reply, err = session.handleCheckVisible(context.Background(), json.RawMessage(`{"eidx":99}`)); err != nil || !strings.HasPrefix(reply, "not visible: ") {
		t.Fatalf("reply %q, err %v", reply, err)
	}
	if len(session.checks) != 2 || !session.checks[0].Met || session.checks[1].Met {
		t.Fatalf("checks = %+v", session.checks)
	}
}

// A check followed by anything that could change the screen -- a mutating
// call or a plain observe, neither of which need change the screen key --
// is not evidence about the final screen: the judge gets only the checks
// measured since the last such event.
func TestChecksOnFinalScreenDropAnythingBeforeTheLastScreenEvent(t *testing.T) {
	t.Parallel()
	session, _ := inputSession(t, screen("Login", textField("user", false, false)))
	check := func(args string) {
		t.Helper()
		if _, err := session.handleCheckVisible(context.Background(), json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}
	check(`{"eidx":0}`)
	// observe records no step, yet replaces the screen.
	if _, err := session.handleObserve(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	check(`{"eidx":99}`)
	final := session.checksOnFinalScreen()
	if len(session.checks) != 2 || len(final) != 1 || final[0].Met {
		t.Fatalf("after observe: all = %+v, final = %+v", session.checks, final)
	}
	// A mutating call whose re-observation fails moves the run on too.
	if _, err := session.afterMutation(context.Background(), "tap", nil, explore.Action{Kind: explore.ActionTap}, errors.New("device: tap failed")); err != nil {
		t.Fatal(err)
	}
	if got := session.checksOnFinalScreen(); len(got) != 0 {
		t.Fatalf("a check from before the last screen event reached the judge: %+v", got)
	}
}

// A failed call is followed by a fresh observation like any other: a
// driver error does not prove the device did nothing. When that observation
// cannot be taken, the table is known to be stale, and a check against it
// is answered with a warning and kept from the judge until observe succeeds.
func TestAFailedStepStillRefreshesTheScreenOrMarksItStale(t *testing.T) {
	t.Parallel()
	login := screen("Login", textField("user", false, false))
	home := screen("Home", textField("search", false, false))
	session, _ := inputSession(t, login)
	session.deps.observer = &fakeObserver{
		states: []*explore.ScreenState{makeState("app", home), makeState("app", login)},
		errs:   []error{nil, errors.New("hierarchy unavailable")},
	}

	reply, err := session.afterMutation(context.Background(), "tap", nil, explore.Action{Kind: explore.ActionTap}, errors.New("device: tap failed"))
	if err != nil {
		t.Fatal(err)
	}
	if !session.current.Signature.Same(makeState("app", home).Signature) {
		t.Fatalf("failed step left the old screen in place: %+v", session.current.Signature)
	}
	// The model must see the screen it is now on, not keep the old indexes.
	if !strings.Contains(reply, "the screen changed anyway") || !strings.Contains(reply, elementTableHeading) {
		t.Fatalf("failure reply hides the fresh screen: %q", reply)
	}

	reply, err = session.afterMutation(context.Background(), "tap", nil, explore.Action{Kind: explore.ActionTap}, errors.New("device: tap failed"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "call observe") || strings.Contains(reply, elementTableHeading) {
		t.Fatalf("failure reply after a lost observation = %q", reply)
	}
	reply, err = session.handleCheckVisible(context.Background(), json.RawMessage(`{"eidx":0}`))
	if err != nil || !strings.Contains(reply, "call observe") {
		t.Fatalf("stale check reply = %q, err %v", reply, err)
	}
	if got := session.checksOnFinalScreen(); len(got) != 0 {
		t.Fatalf("a check against a stale table reached the judge: %+v", got)
	}
}
