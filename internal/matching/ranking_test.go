package matching

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestFindExplicitIndexSortsYXSupportsNegativeAndPlacesMissingBoundsLast(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("missing", map[string]string{"text": "Item"}),
		matchNode("bottom", map[string]string{"text": "Item", "bounds": "[10,20][20,30]"}),
		matchNode("top-right", map[string]string{"text": "Item", "bounds": "[20,10][30,20]"}),
		matchNode("top-left", map[string]string{"text": "Item", "bounds": "[5,10][15,20]"}),
		matchNode("top-left-tie", map[string]string{"text": "Item", "bounds": "[5,10][15,20]"}),
	))
	tests := []struct {
		index int
		want  []string
	}{
		{index: 0, want: []string{"top-left"}},
		{index: 1, want: []string{"top-left-tie"}},
		{index: -1, want: []string{"missing"}},
		{index: -2, want: []string{"bottom"}},
		{index: 5, want: []string{}},
		{index: -6, want: []string{}},
	}
	for _, test := range tests {
		matches, err := Find(root, model.ElementSelector{TextRegex: stringPointer("Item"), Index: stringPointer(strconv.Itoa(test.index))})
		if err != nil {
			t.Fatalf("Find(index=%d): %v", test.index, err)
		}
		if got := matchNames(matches); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Find(index=%d) names = %v, want %v", test.index, got, test.want)
		}
	}
}

func TestFindDefaultsToStableClickableFirstOrdering(t *testing.T) {
	t.Parallel()

	nonClickableOne := matchNode("non-clickable-one", map[string]string{"text": "Item"})
	clickableOne := matchNode("clickable-one", map[string]string{"text": "Item"})
	clickableOne.Clickable = boolPointer(true)
	nonClickableTwo := matchNode("non-clickable-two", map[string]string{"text": "Item"})
	nonClickableTwo.Clickable = boolPointer(false)
	clickableTwo := matchNode("clickable-two", map[string]string{"text": "Item"})
	clickableTwo.Clickable = boolPointer(true)
	root := mustHierarchy(t, device.TreeNode{Attributes: map[string]string{"name": "root"}, Children: []device.TreeNode{
		nonClickableOne, clickableOne, nonClickableTwo, clickableTwo,
	}})

	matches, err := Find(root, model.ElementSelector{TextRegex: stringPointer("Item")})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got, want := matchNames(matches), []string{
		"clickable-one", "clickable-two", "non-clickable-one", "non-clickable-two",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Find names = %v, want %v", got, want)
	}
}
