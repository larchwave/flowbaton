package explore

import (
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// An icon whose accessibility label is its SF Symbol name says nothing to a
// reader. Session mmx57 mapped a calendar screen as
// august-calendar-day-timeline-leading-26d1eb3d, from a button labelled
// "calendar.day.timeline.leading" whose id is the honest
// "toggle-day-list-view". Captured on iOS 26.2, 2026-08-31, three more:
// "doc.viewfinder.fill" and "deskclock.fill" in shortcuts,
// "photo.fill.on.rectangle.fill" in photos.
func TestComputeSignatureSkipsASymbolName(t *testing.T) {
	t.Parallel()

	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			node(map[string]string{"elementType": "9", "accessibilityText": "August", "id": "BackButton"}),
			node(map[string]string{"elementType": "9", "accessibilityText": "calendar.day.timeline.leading", "id": "toggle-day-list-view"}),
			node(map[string]string{"elementType": "9", "accessibilityText": "Search", "id": "searchbar-button"}),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"August", "Search"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// A label a person wrote keeps its dots: a sentence, a version, a name with
// capitals are all still names.
func TestComputeSignatureKeepsOrdinaryDottedText(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"Version 2.1.0", "Info.plist", "No results. Try again"} {
		tree := device.TreeNode{
			Attributes: map[string]string{"bounds": "[0,0][402,874]"},
			Children: []device.TreeNode{
				node(map[string]string{"elementType": "48", "accessibilityText": label}),
			},
		}
		if got := ComputeSignature("app", tree).Salient; !reflect.DeepEqual(got, []string{label}) {
			t.Fatalf("salient = %q, want %q", got, []string{label})
		}
	}
}
