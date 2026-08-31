package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// The dimming view behind a sheet fills the whole viewport and carries the
// label that tells a screen reader what tapping it does. It is not the name
// of the screen, and because it sits above the sheet in document order it
// took the first label slot: the contacts search screen on iOS 26.2, with a
// sheet open, keyed as activate-to-dismiss-search.
func TestComputeSignatureSkipsAFullViewportLabel(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][0,0]", "elementType": "0"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{"bounds": "[0,0][402,874]", "elementType": "2", "accessibilityText": "Contacts"},
			Children: []device.TreeNode{
				{Attributes: map[string]string{"bounds": "[0,0][402,874]", "elementType": "1", "accessibilityText": "Activate to dismiss"}},
				{Attributes: map[string]string{"bounds": "[17,484][325,522]", "elementType": "45", "accessibilityText": "Search"}},
				{Attributes: map[string]string{"bounds": "[30,600][200,640]", "elementType": "48", "accessibilityText": "Groups"}},
			},
		}},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Search", "Groups"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// A platform that sends no application element keeps every label: the
// viewport is unknown there, so nothing is measured against it. Android
// sends a root as large as the screen on every dump, and a screen that is
// one full-sized labelled view must keep the only name it has.
func TestComputeSignatureKeepsLabelsUnderAFullSizedRoot(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][1080,2400]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"bounds": "[0,100][1080,200]", "class": "android.widget.TextView", "text": "Settings"}},
			{Attributes: map[string]string{"bounds": "[0,200][1080,300]", "class": "android.widget.TextView", "text": "Search settings"}},
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Settings", "Search settings"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}
