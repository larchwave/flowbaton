package research

import (
	"context"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// A row scrolled past the bottom edge keeps its place in the element table,
// so research hands the model a point locator for it -- and the exporter
// writes that point into a flow. The geometric center of such a row is off
// the device, and the engine refuses an absolute point outside the screen,
// so the session would export a flow that cannot replay.
// Geometry from a live capture of Apple's Reminders on a 402x874 simulator.
func TestResearchPointLocatorStaysOnScreenForARowPastTheBottomEdge(t *testing.T) {
	root := testNode(map[string]string{},
		clickableNode(map[string]string{
			"class":       "android.widget.TextView",
			"text":        "Row",
			"resource-id": "com.example:id/row",
			"bounds":      "[16,847][386,909]",
		}),
	)
	state := researchState(t, root, nil)
	state.Viewport = device.Bounds{Width: 402, Height: 874}

	researcher := &Researcher{
		Models: explore.ModelSet{Worker: &scriptedLLM{replies: []string{
			`{"sections":[{"name":"List","notes":"rows","elements":[{"eidx":0,"role":"cell","label":"Row"}]}]}`,
		}}},
		Cache: &fakeCache{},
		Clock: func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) },
	}
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	locators := uiMap.Sections[0].Elements[0].Locators
	if len(locators) < 2 || locators[1].Kind != explore.LocatorPoint {
		t.Fatalf("locators %+v, want a point locator second", locators)
	}
	// The visible strip is y 847..874, so the point belongs at its middle.
	if locators[1].Value != "201,860" {
		t.Errorf("point locator = %q, want %q", locators[1].Value, "201,860")
	}
}

// Without a viewport, and for an element fully on screen, the point is the
// plain center it has always been.
func TestResearchPointLocatorIsUnchangedForAVisibleElement(t *testing.T) {
	state := researchState(t, researchTree(), nil)
	state.Viewport = device.Bounds{Width: 402, Height: 874}
	researcher := &Researcher{
		Models: explore.ModelSet{Worker: &scriptedLLM{replies: []string{validSectionsJSON}}},
		Cache:  &fakeCache{},
		Clock:  func() time.Time { return time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC) },
	}
	uiMap, err := researcher.Research(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if got := uiMap.Sections[0].Elements[0].Locators[1].Value; got != "60,40" {
		t.Errorf("point locator = %q, want the plain center %q", got, "60,40")
	}
}
