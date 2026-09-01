package run

import (
	"context"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// A recipe is stored against one screen's digest and replayed only when the
// digest still matches, so an element index is meaningful today. It is not
// meaningful anywhere else: an exported flow needs a selector, and a recipe
// written as "tap {\"eidx\":3}" can never become "tapOn: General".
//
// The tap tool already takes text and id as well as eidx, and the locator
// the toolbox puts on the action is the same one the exporter builds -- id
// first, then the row's name, regex-quoted, with a point only when neither
// answers. Recording that instead costs nothing and is the shape both
// readers need. No recipe exists on disk, so there is nothing to migrate.
func TestARecipeLineNamesTheElementItTapped(t *testing.T) {
	t.Parallel()

	root := screen("Home",
		button("General", "general_row", "[0,0][402,60]"),
		device.TreeNode{Attributes: map[string]string{
			"class": "android.widget.Button", "text": "Sign out", "bounds": "[0,60][402,120]"}},
	)
	for _, test := range []struct {
		name string
		args string
		want string
	}{
		{"an id becomes the id", `{"eidx":0}`, `tap {"id":"general_row"}`},
		{"a row with no id becomes its name", `{"eidx":1}`, `tap {"text":"Sign out"}`},
		{"a text target stays what was asked", `{"text":"Sign out"}`, `tap {"text":"Sign out"}`},
	} {
		session, _ := inputSession(t, root)
		if _, err := session.handleTap(context.Background(), []byte(test.args)); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
		if len(session.recording) != 1 {
			t.Fatalf("%s: recording = %q", test.name, session.recording)
		}
		if session.recording[0] != test.want {
			t.Errorf("%s: recorded %q, want %q", test.name, session.recording[0], test.want)
		}
	}
}

// A screen-level action has no target and a row addressable only by
// coordinate has no selector, so both keep the arguments the model sent.
func TestARecipeLineKeepsArgumentsWithNoSelector(t *testing.T) {
	t.Parallel()

	session, _ := inputSession(t, screen("Home", button("Go", "go", "[0,0][402,60]")))
	if _, err := session.handleSwipe(context.Background(), []byte(`{"direction":"up"}`)); err != nil {
		t.Fatal(err)
	}
	if len(session.recording) != 1 || !strings.Contains(session.recording[0], `"direction":"up"`) {
		t.Fatalf("recording = %q", session.recording)
	}
}
