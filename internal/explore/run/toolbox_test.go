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

func TestReplayRefusesLegacyMaskedInput(t *testing.T) {
	// Recordings written before the {"masked":true} marker used the literal
	// mask as text. Refusing both shapes costs one worker fallback when a
	// user really typed "***"; replaying a masked secret costs a login.
	session, driver := inputSession(t, screen("Search", textField("query", false, true)))
	if session.replay(context.Background(), session.box(), `input_text {"text":"***"}`) {
		t.Fatal("replay accepted a legacy masked recording")
	}
	if slices.Contains(driver.calls, "InputText:***") {
		t.Fatalf("legacy mask reached the device: %v", driver.calls)
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
