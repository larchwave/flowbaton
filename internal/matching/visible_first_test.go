package matching_test

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
)

// FilterVisible keeps a node that is not itself visible when a child of it
// is, so a visible descendant does not lose its ancestors. Those ancestors
// stay matchable, and one carrying the same label as a real control ranked
// ahead of it on document order alone: "Save" on a list header scrolled 9%
// into view centers at (200,-225), above the top of the screen, while the
// button the flow meant sits at (200,630).
func TestAMatchOnScreenOutranksOneThatIsNot(t *testing.T) {
	viewport := device.Bounds{Width: 400, Height: 800}
	node := func(text, bounds string, kids ...device.TreeNode) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.TextView", "text": text, "bounds": bounds},
			Children:   kids,
		}
	}
	root, err := hierarchy.New(node("root", "[0,0][400,800]",
		node("Save", "[0,-500][400,50]", node("icon", "[0,0][400,50]")),
		node("Save", "[100,600][300,660]"),
	))
	if err != nil {
		t.Fatal(err)
	}
	text := "Save"
	found, err := matching.Find(hierarchy.FilterVisible(root, viewport),
		model.ElementSelector{TextRegex: &text})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("want both matches kept, got %d", len(found))
	}
	if got := hierarchy.Center(found[0].Bounds); got.Y < 0 {
		t.Fatalf("the first match centers off the top of the screen at %+v; the visible one is at %+v",
			got, hierarchy.Center(found[1].Bounds))
	}
}

// Nothing is dropped: scrollUntilVisible finds its target in this same tree
// and applies its own visibility threshold (internal/engine, scroll handler).
func TestAnOffScreenMatchIsStillReturned(t *testing.T) {
	viewport := device.Bounds{Width: 400, Height: 800}
	root, err := hierarchy.New(device.TreeNode{
		Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][400,800]"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{"class": "android.widget.TextView", "text": "Later", "bounds": "[0,-500][400,50]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{"class": "android.widget.ImageView", "bounds": "[0,0][400,50]"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := "Later"
	found, err := matching.Find(hierarchy.FilterVisible(root, viewport),
		model.ElementSelector{TextRegex: &text})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Fatalf("an off-screen match must still be found, got %d", len(found))
	}
}
