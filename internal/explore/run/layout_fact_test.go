package run

import (
	"fmt"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

func labelledAt(x, y, w, h int, label string) explore.FlatElement {
	return explore.FlatElement{Node: device.TreeNode{Attributes: map[string]string{
		"accessibilityText": label,
		"bounds":            fmt.Sprintf("[%d,%d][%d,%d]", x, y, x+w, y+h),
	}}}
}

// Three sessions asked the judge a question about layout -- a vertical split,
// an agenda beneath a grid, a list against a month grid -- and the element
// table answers none of them. mmx79's judge said so itself.
//
// The widest row does NOT separate them: measured on two live Calendar
// captures, the month grid and the day timeline both answer "up to 7 across",
// because the timeline carries a week strip along its top. How MANY rows hold
// more than one element does separate them, 11 of 17 against 6 of 27.
func TestTheLayoutFactSeparatesAGridFromAList(t *testing.T) {
	t.Parallel()

	// A grid, with one single-element row standing in for a header.
	grid := &explore.ScreenState{Elements: []explore.FlatElement{
		labelledAt(10, 100, 380, 40, "header"),
	}}
	for row := 0; row < 5; row++ {
		for column := 0; column < 7; column++ {
			grid.Elements = append(grid.Elements,
				labelledAt(10+column*50, 200+row*60, 40, 40, "day"))
		}
	}
	gridFact := layoutLine(grid)
	if !strings.Contains(gridFact, "6 rows") || !strings.Contains(gridFact, "5 of which") {
		t.Fatalf("grid layout = %q, want 6 rows, 5 of them wide", gridFact)
	}

	// A timeline: single-element rows under a week strip of seven, which is
	// the shape that defeats a widest-row test.
	list := &explore.ScreenState{}
	for column := 0; column < 7; column++ {
		list.Elements = append(list.Elements, labelledAt(10+column*50, 100, 40, 40, "day"))
	}
	for row := 0; row < 6; row++ {
		list.Elements = append(list.Elements, labelledAt(10, 200+row*60, 380, 40, "entry"))
	}
	listFact := layoutLine(list)
	if !strings.Contains(listFact, "7 rows") || !strings.Contains(listFact, "1 of which") {
		t.Fatalf("list layout = %q, want 7 rows, 1 of them wide", listFact)
	}
	// The statistic that fails on this pair must not be the one deciding.
	if !strings.Contains(gridFact, "widest 7") || !strings.Contains(listFact, "widest 7") {
		t.Fatalf("both screens should report the same widest row:\n%s\n%s", gridFact, listFact)
	}
}

// A screen whose elements carry no bounds has no layout to describe, and
// saying nothing is better than saying "0 rows".
func TestNoBoundsMeansNoLayoutFact(t *testing.T) {
	t.Parallel()

	state := &explore.ScreenState{Elements: []explore.FlatElement{
		{Node: device.TreeNode{Attributes: map[string]string{"accessibilityText": "x"}}},
	}}
	if fact := layoutLine(state); fact != "" {
		t.Fatalf("layout = %q, want nothing without bounds", fact)
	}
}
