package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func node(attrs map[string]string, children ...device.TreeNode) device.TreeNode {
	attrs["bounds"] = "[0,0][100,50]"
	return device.TreeNode{Attributes: attrs, Children: children}
}

// A container and the label inside it carry the same string, so both salient
// slots hold one word and the second says nothing. Captured live on both
// platforms, 2026-08-31: Android contacts has ViewGroup product_lockup
// "Contacts" wrapping TextView product_name "Contacts", and named the screen
// contacts-contacts-ba7ab7d2; iOS contacts has cell(75) "All Contacts"
// wrapping (52) "All Contacts".
func TestComputeSignatureDoesNotSpendBothSlotsOnOneLabel(t *testing.T) {
	t.Parallel()

	android := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][1080,2400]"},
		Children: []device.TreeNode{
			node(map[string]string{"class": "android.view.ViewGroup", "accessibilityText": "Contacts"},
				node(map[string]string{"class": "android.widget.TextView", "text": "Contacts"}),
			),
			node(map[string]string{"class": "android.widget.TextView", "text": "Skip"}),
		},
	}
	if got, want := ComputeSignature("app", android).Salient, []string{"Contacts", "Skip"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// A screen that really has one label keeps it and stops there rather than
// padding the list.
func TestComputeSignatureKeepsASingleLabelSingle(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][100,50]"},
		Children: []device.TreeNode{
			node(map[string]string{"class": "android.widget.TextView", "text": "Only"}),
			node(map[string]string{"class": "android.view.ViewGroup", "accessibilityText": "Only"}),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Only"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// Two labels that differ only in case read as one name: Key lowercases both,
// so keeping them spends the list on a word the reader sees twice. Captured
// live on iOS 26.2, 2026-08-31: the contacts search screen carries an image
// labelled "Search" and a keyboard key labelled "search".
func TestComputeSignatureTreatsCaseVariantsAsOneLabel(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			node(map[string]string{"elementType": "43", "id": "magnifyingglass", "accessibilityText": "Search"}),
			node(map[string]string{"elementType": "9", "id": "Search", "accessibilityText": "search"}),
			node(map[string]string{"elementType": "48", "accessibilityText": "Groups"}),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Search", "Groups"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// The navigation title moves in front of the labels rather than repeating
// when the two differ only in case, for the same reason.
func TestWithTitleFirstDropsACaseVariantOfTheTitle(t *testing.T) {
	t.Parallel()

	if got, want := withTitleFirst("Lists", []string{"lists", "Edit"}), []string{"Lists", "Edit"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ordered = %q, want %q", got, want)
	}
}
