package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func typed(elementType, label string, children ...device.TreeNode) device.TreeNode {
	attrs := map[string]string{"elementType": elementType, "bounds": "[0,62][402,116]"}
	if label != "" {
		attrs["accessibilityText"] = label
	}
	return device.TreeNode{Attributes: attrs, Children: children}
}

func appScreen(children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			typed("2", "TheApp", children...),
		},
	}
}

// Salient labels are taken in document order, which on iOS means the
// navigation bar's leftmost BUTTON rather than the screen's title. Captured
// on iOS 26.2, 2026-08-31, one navigation bar (elementType 21) per screen,
// its title the single StaticText (48) among buttons (9):
//
//	reminders      Back(9)  More(9)  Completed(48)      -> "Completed"
//	contacts list  Edit(9)  Lists(48)  Add List(9)      -> "Lists"
//	contacts all   Lists(9) Contacts(48)                -> "Contacts"
//	shortcuts      Library(9) Edit(9) add(9) All Shortcuts(48) -> "All Shortcuts"
func TestComputeSignaturePrefersTheNavigationTitle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		tree device.TreeNode
		want []string
	}{
		{
			name: "title after the buttons",
			tree: appScreen(typed("21", "",
				typed("9", "Back"), typed("9", "More"), typed("48", "Completed"))),
			want: []string{"Completed", "Back"},
		},
		{
			name: "title between the buttons",
			tree: appScreen(typed("21", "",
				typed("9", "Edit"), typed("48", "Lists"), typed("9", "Add List"))),
			want: []string{"Lists", "Edit"},
		},
		{
			name: "title last of two",
			tree: appScreen(typed("21", "",
				typed("9", "Lists"), typed("48", "Contacts"))),
			want: []string{"Contacts", "Lists"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ComputeSignature("app", test.tree).Salient; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("salient = %q, want %q", got, test.want)
			}
		})
	}
}

// Not every screen has a static title: Calendar's is a button, Settings'
// modal carries only Close, and a locked Passwords screen has no navigation
// bar at all. Those keep the labels they had.
func TestComputeSignatureFallsBackWhenTheBarHasNoTitle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		tree device.TreeNode
		want []string
	}{
		{
			name: "every bar child is a button",
			tree: appScreen(typed("21", "",
				typed("9", "August"), typed("9", "Single Day"), typed("9", "Search"))),
			want: []string{"August", "Single Day"},
		},
		{
			name: "no navigation bar at all",
			tree: appScreen(typed("48", "Passwords Is Locked"), typed("9", "Unlock")),
			want: []string{"Passwords Is Locked", "Unlock"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ComputeSignature("app", test.tree).Salient; !reflect.DeepEqual(got, test.want) {
				t.Fatalf("salient = %q, want %q", got, test.want)
			}
		})
	}
}

// A StaticText outside the bar is content, not a title, and must not be
// promoted past what the screen actually leads with.
func TestComputeSignatureDoesNotPromoteStaticTextOutsideTheBar(t *testing.T) {
	t.Parallel()

	tree := appScreen(
		typed("21", "", typed("9", "Back")),
		typed("48", "Some row of content"),
	)
	if got, want := ComputeSignature("app", tree).Salient, []string{"Back", "Some row of content"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}
