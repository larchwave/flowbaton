package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func labelled(elementType, bounds, label string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{
			"elementType":       elementType,
			"bounds":            bounds,
			"accessibilityText": label,
		},
		Children: children,
	}
}

// The application element carries the app's own name and covers the whole
// viewport, so it names every screen of an app identically -- and
// ScreenSignature.AppID already says which app this is. Taking it as a
// salient label spends one of the two slots on a constant.
//
// Captured on iOS 26.2, Contacts, 2026-08-31: two different screens both
// began with elementType 2 at [0,0][402,874] labelled "Contacts", and the
// session's own cache showed six distinct screens all keyed
// contacts-back-<digest>.
func TestComputeSignatureSkipsTheApplicationLabel(t *testing.T) {
	t.Parallel()

	lists := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			labelled("2", "[0,0][402,874]", "Contacts",
				labelled("9", "[20,66][74,102]", "Edit"),
				labelled("48", "[182,73][220,94]", "Lists"),
			),
		},
	}
	all := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			labelled("2", "[0,0][402,874]", "Contacts",
				labelled("9", "[16,62][60,106]", "Lists"),
				labelled("48", "[164,73][237,94]", "Contacts"),
			),
		},
	}

	listsSalient := ComputeSignature("com.apple.MobileAddressBook", lists).Salient
	allSalient := ComputeSignature("com.apple.MobileAddressBook", all).Salient
	if want := []string{"Edit", "Lists"}; !reflect.DeepEqual(listsSalient, want) {
		t.Fatalf("lists salient = %q, want %q", listsSalient, want)
	}
	if want := []string{"Lists", "Contacts"}; !reflect.DeepEqual(allSalient, want) {
		t.Fatalf("all-contacts salient = %q, want %q", allSalient, want)
	}
}

// Skipping the label must not skip the node: it is real structure, and
// dropping it from the digest would change the identity of every screen
// already recorded.
func TestComputeSignatureKeepsTheApplicationNodeInTheDigest(t *testing.T) {
	t.Parallel()

	withApp := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			labelled("2", "[0,0][402,874]", "Contacts",
				labelled("48", "[182,73][220,94]", "Lists"),
			),
		},
	}
	withoutApp := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			labelled("48", "[182,73][220,94]", "Lists"),
		},
	}
	if a, b := ComputeSignature("app", withApp), ComputeSignature("app", withoutApp); a.TreeDigest == b.TreeDigest {
		t.Fatalf("digest %q is the same with and without the application node", a.TreeDigest)
	}
}

// An app whose own element carries no label loses nothing: the skip is about
// the application element, not about the app's name appearing on a screen.
// A screen that really shows the word stays free to be named by it.
func TestComputeSignatureKeepsAppNamedContentThatIsNotTheApplicationNode(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			labelled("2", "[0,0][402,874]", "Contacts",
				labelled("48", "[164,73][237,94]", "Contacts"),
				labelled("48", "[18,126][28,144]", "A"),
			),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Contacts", "A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}
