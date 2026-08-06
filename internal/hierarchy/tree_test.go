package hierarchy

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

func TestNewBuildsNormalizedPreorderTree(t *testing.T) {
	t.Parallel()

	root, err := New(testNode("root", "[0,0][100,100]",
		testNode("first", "[0,0][10,10]"),
		testNode("container", "", testNode("nested", "[20,20][30,30]")),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	walk := Walk(root)
	if got, want := elementNames(walk), []string{"root", "first", "container", "nested"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Walk names = %v, want %v", got, want)
	}
	for index, element := range walk {
		if element.Order != index {
			t.Fatalf("%s order = %d, want %d", element.Node.Attributes["name"], element.Order, index)
		}
	}
	if walk[2].HasBounds {
		t.Fatal("missing bounds were reported as present")
	}
	if walk[3].Parent != walk[2] {
		t.Fatal("nested element parent was not retained")
	}
}

func TestNewRejectsMalformedNodeBounds(t *testing.T) {
	t.Parallel()

	_, err := New(testNode("root", "not-bounds"))
	var boundsErr *BoundsError
	if !errors.As(err, &boundsErr) {
		t.Fatalf("New error = %T %v, want *BoundsError", err, err)
	}
}

func TestFilterVisibleRetainsThresholdMissingBoundsAndVisibleChildren(t *testing.T) {
	t.Parallel()

	root, err := New(testNode("root", "[0,0][100,100]",
		testNode("retained-parent", "[99,0][119,10]",
			testNode("visible-child", "[10,10][20,20]"),
		),
		testNode("dropped", "[99,20][119,30]"),
		testNode("exactly-ten", "[98,40][118,50]"),
		testNode("missing-bounds", ""),
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	filtered := FilterVisible(root, device.Bounds{Width: 100, Height: 100})
	if filtered == nil {
		t.Fatal("FilterVisible dropped the root")
	}
	if got, want := elementNames(Walk(filtered)), []string{
		"root", "retained-parent", "visible-child", "exactly-ten", "missing-bounds",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered names = %v, want %v", got, want)
	}
	if filtered.Children[0].Parent != filtered {
		t.Fatal("filtered tree parent links were not rebuilt")
	}
	if len(root.Children) != 4 {
		t.Fatalf("FilterVisible mutated source tree children: %d", len(root.Children))
	}
}

func testNode(name, bounds string, children ...device.TreeNode) device.TreeNode {
	attributes := map[string]string{"name": name}
	if bounds != "" {
		attributes["bounds"] = bounds
	}
	return device.TreeNode{Attributes: attributes, Children: children}
}

func elementNames(elements []*Element) []string {
	names := make([]string, len(elements))
	for index, element := range elements {
		names[index] = element.Node.Attributes["name"]
	}
	return names
}
