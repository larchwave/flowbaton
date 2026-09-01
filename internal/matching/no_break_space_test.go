package matching

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/model"
)

// A selector carries the text a reader sees, and a reader cannot see which
// space a label holds. iOS labels its account row "Apple Account" and
// every clock "4:10 PM"; the space typed into a flow is the plain one and
// matched neither. Measured over thirteen captures of six apps, iOS 26.2:
// twelve no-break spaces and seventy-four narrow no-break spaces inside
// labels, and no other exotic whitespace but a newline in body prose, which
// this matcher already folded.
//
// Session mmx63 filed "[High] not observed: A screen titled 'Apple Account' is
// displayed" with the evidence that only the no-break form was there.
func TestTextMatchesAcrossTheSpacesAScreenShows(t *testing.T) {
	t.Parallel()

	root, err := hierarchy.New(device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][400,800]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"accessibilityText": "Apple Account", "bounds": "[0,0][400,60]"}},
			{Attributes: map[string]string{"accessibilityText": "4:10 PM", "bounds": "[0,60][400,90]"}},
			{Attributes: map[string]string{"text": "Two\nlines", "bounds": "[0,90][400,120]"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, one := range []struct {
		pattern string
		want    int
	}{
		{"Apple Account", 1},
		{"4:10 PM", 1},
		// The fold does not invent a match.
		{"Apple Zccount", 0},
		// The newline fold this replaces must keep working.
		{"Two lines", 1},
		// A pattern written with the screen's own character still matches.
		{"Apple Account", 1},
	} {
		text := one.pattern
		found, err := Find(root, model.ElementSelector{TextRegex: &text})
		if err != nil {
			t.Fatalf("%q: %v", one.pattern, err)
		}
		if len(found) != one.want {
			t.Errorf("%q matched %d elements, want %d", one.pattern, len(found), one.want)
		}
	}
}
