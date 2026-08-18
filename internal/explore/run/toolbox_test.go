package run

import (
	"context"
	"encoding/json"
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
