package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// A tab bar and a toolbar carry their own role as their label, so a screen
// took the word "Toolbar" for a name: session mmx56 keyed the contacts
// search screen as toolbar-search-32d1546f. Captured on iOS 26.2,
// 2026-08-31: shortcuts and photos both hold a tab bar (22) labelled "Tab
// Bar", calendar and contacts a toolbar (24) labelled "Toolbar", and the
// seven navigation bars (21) carry no label at all.
func TestComputeSignatureSkipsAContainersOwnRole(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			node(map[string]string{"elementType": "24", "accessibilityText": "Toolbar"},
				node(map[string]string{"elementType": "9", "accessibilityText": "Search"}),
			),
			node(map[string]string{"elementType": "22", "accessibilityText": "Tab Bar"},
				node(map[string]string{"elementType": "9", "accessibilityText": "Groups"}),
			),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Search", "Groups"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}
