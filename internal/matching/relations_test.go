package matching

import (
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestFindAppliesBasicIntersectionBeforeDeepestReduction(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", map[string]string{"text": "Match", "resource-id": "root"},
		matchNode("deep", map[string]string{"text": "Match", "resource-id": "deep"}),
		matchNode("wrong-id", map[string]string{"text": "Match", "resource-id": "wrong"}),
	))
	matches, err := Find(root, model.ElementSelector{
		TextRegex: stringPointer("Match"),
		IDRegex:   stringPointer("root|deep"),
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got := matchNames(matches); !reflect.DeepEqual(got, []string{"deep"}) {
		t.Fatalf("Find names = %v, want [deep]", got)
	}
}

// Directional matches retain hierarchy traversal order. The fixture declares
// each far node before its near sibling and asserts that stable order.
func TestFindAppliesDirectionalRelationsInDocumentOrder(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("anchor", map[string]string{"text": "Anchor", "bounds": "[40,40][60,60]"}),
		matchNode("below-far", map[string]string{"text": "Target", "bounds": "[40,80][60,90]"}),
		matchNode("below-near", map[string]string{"text": "Target", "bounds": "[40,65][60,75]"}),
		matchNode("above-far", map[string]string{"text": "Target", "bounds": "[40,10][60,20]"}),
		matchNode("above-near", map[string]string{"text": "Target", "bounds": "[40,25][60,35]"}),
		matchNode("left-far", map[string]string{"text": "Target", "bounds": "[10,40][20,60]"}),
		matchNode("left-near", map[string]string{"text": "Target", "bounds": "[25,40][35,60]"}),
		matchNode("right-far", map[string]string{"text": "Target", "bounds": "[80,40][90,60]"}),
		matchNode("right-near", map[string]string{"text": "Target", "bounds": "[65,40][75,60]"}),
	))
	anchor := &model.ElementSelector{TextRegex: stringPointer("Anchor")}
	tests := []struct {
		name     string
		selector model.ElementSelector
		want     []string
	}{
		{name: "below", selector: model.ElementSelector{TextRegex: stringPointer("Target"), Below: anchor}, want: []string{"below-far", "below-near"}},
		{name: "above", selector: model.ElementSelector{TextRegex: stringPointer("Target"), Above: anchor}, want: []string{"above-far", "above-near"}},
		{name: "left", selector: model.ElementSelector{TextRegex: stringPointer("Target"), LeftOf: anchor}, want: []string{"left-far", "left-near"}},
		{name: "right", selector: model.ElementSelector{TextRegex: stringPointer("Target"), RightOf: anchor}, want: []string{"right-far", "right-near"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			matches, err := Find(root, test.selector)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if got := matchNames(matches); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Find names = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFindMatchesDirectChildrenDescendantListsAndChildOf(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("container-one", map[string]string{"resource-id": "container-one"},
			matchNode("direct-icon", map[string]string{"text": "Icon"}),
			matchNode("wrapper-one", nil, matchNode("nested-label", map[string]string{"text": "Label"})),
		),
		matchNode("container-two", map[string]string{"resource-id": "container-two"},
			matchNode("wrapper-two", nil, matchNode("nested-icon", map[string]string{"text": "Icon"})),
		),
	))

	directMatches, err := Find(root, model.ElementSelector{
		IDRegex:       stringPointer("container-.*"),
		ContainsChild: &model.ElementSelector{TextRegex: stringPointer("Icon")},
	})
	if err != nil {
		t.Fatalf("Find containsChild: %v", err)
	}
	if got := matchNames(directMatches); !reflect.DeepEqual(got, []string{"container-one"}) {
		t.Fatalf("containsChild names = %v", got)
	}

	descendantMatches, err := Find(root, model.ElementSelector{
		IDRegex: stringPointer("container-.*"),
		ContainsDescendants: []model.ElementSelector{
			{TextRegex: stringPointer("Icon")},
			{TextRegex: stringPointer("Label")},
		},
	})
	if err != nil {
		t.Fatalf("Find containsDescendants: %v", err)
	}
	if got := matchNames(descendantMatches); !reflect.DeepEqual(got, []string{"container-one"}) {
		t.Fatalf("containsDescendants names = %v", got)
	}

	childMatches, err := Find(root, model.ElementSelector{
		TextRegex: stringPointer("Icon"),
		ChildOf:   &model.ElementSelector{IDRegex: stringPointer("container-one")},
	})
	if err != nil {
		t.Fatalf("Find childOf: %v", err)
	}
	if got := matchNames(childMatches); !reflect.DeepEqual(got, []string{"direct-icon"}) {
		t.Fatalf("childOf names = %v", got)
	}
}

// TestFindResolvesContainerSelectorsWithNoOwnTraits pins two product rules:
//
//   - a node carrying no text at all reads as empty text, so `text: ".*"` and
//     `text: ""` resolve to it.
//   - the deepest-node reduction runs AFTER the structural filters, not before.
//     A selector whose only trait is `containsChild` matches every node
//     basically, and reducing to leaves first threw away every container the
//     filter was about to select.
func TestFindResolvesContainerSelectorsWithNoOwnTraits(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("row", map[string]string{"resource-id": "row"},
			matchNode("label", map[string]string{"text": "Battery", "resource-id": "label"}),
		),
		matchNode("bare", map[string]string{"resource-id": "bare"}),
	))

	t.Run("contains child selects the parent", func(t *testing.T) {
		matches, err := Find(root, model.ElementSelector{
			ContainsChild: &model.ElementSelector{TextRegex: stringPointer("Battery")},
		})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if got := matchNames(matches); !reflect.DeepEqual(got, []string{"row"}) {
			t.Fatalf("containsChild names = %v, want [row]", got)
		}
	})

	t.Run("contains descendants selects the ancestor", func(t *testing.T) {
		matches, err := Find(root, model.ElementSelector{
			ContainsDescendants: []model.ElementSelector{{TextRegex: stringPointer("Battery")}},
		})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		got := matchNames(matches)
		if len(got) == 0 {
			t.Fatal("containsDescendants matched nothing, want the ancestors of the label")
		}
		for _, want := range []string{"row"} {
			found := false
			for _, name := range got {
				if name == want {
					found = true
				}
			}
			if !found {
				t.Fatalf("containsDescendants names = %v, want %q among them", got, want)
			}
		}
	})

	t.Run("text-less nodes read as empty text", func(t *testing.T) {
		// Nodes without a text attribute read as empty text. This keeps selectors
		// consistent across hierarchy producers and still lets accessibility-only
		// nodes participate in empty-text queries.
		for _, pattern := range []string{".*", ""} {
			matches, err := Find(root, model.ElementSelector{TextRegex: stringPointer(pattern)})
			if err != nil {
				t.Fatalf("Find(%q): %v", pattern, err)
			}
			found := false
			for _, name := range matchNames(matches) {
				if name == "bare" {
					found = true
				}
			}
			if !found {
				t.Fatalf("Find(text: %q) = %v, want the text-less node among them",
					pattern, matchNames(matches))
			}
		}

		// Absence reads as empty PER ATTRIBUTE: a node with a description but
		// no text still matches `text: ""`, which is the avatar's shape.
		described := mustHierarchy(t, matchNode("root", nil,
			matchNode("avatar", map[string]string{"accessibilityText": "Profile picture", "resource-id": "avatar"}),
		))
		avatar, err := Find(described, model.ElementSelector{TextRegex: stringPointer("")})
		if err != nil {
			t.Fatalf("Find: %v", err)
		}
		if got := matchNames(avatar); len(got) == 0 {
			t.Fatalf(`Find(text: "") on a description-only node = %v, want it matched`, got)
		}
	})
}

func TestFindUsesOriginsNotGaps(t *testing.T) {
	t.Parallel()

	// An element that overlaps the anchor still counts as above, below, left, or
	// right when its origin lies in that direction.
	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("anchor", map[string]string{"text": "Anchor", "bounds": "[40,40][60,60]"}),
		// Overlaps the anchor on both axes; every origin is one pixel off it.
		matchNode("after", map[string]string{"text": "Target", "bounds": "[41,41][200,200]"}),
		matchNode("before", map[string]string{"text": "Target", "bounds": "[39,39][200,200]"}),
	))
	anchor := &model.ElementSelector{TextRegex: stringPointer("Anchor")}
	for _, test := range []struct {
		name     string
		selector model.ElementSelector
		want     []string
	}{
		{name: "below", selector: model.ElementSelector{TextRegex: stringPointer("Target"), Below: anchor}, want: []string{"after"}},
		{name: "above", selector: model.ElementSelector{TextRegex: stringPointer("Target"), Above: anchor}, want: []string{"before"}},
		{name: "left", selector: model.ElementSelector{TextRegex: stringPointer("Target"), LeftOf: anchor}, want: []string{"before"}},
		{name: "right", selector: model.ElementSelector{TextRegex: stringPointer("Target"), RightOf: anchor}, want: []string{"after"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			matches, err := Find(root, test.selector)
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if got := matchNames(matches); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Find names = %v, want %v", got, test.want)
			}
		})
	}
}
