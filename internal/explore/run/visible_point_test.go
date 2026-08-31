package run

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// row returns a plain labelled row, the shape a list produces.
func row(text, bounds string) device.TreeNode {
	return device.TreeNode{Attributes: map[string]string{
		"class": "android.widget.TextView", "text": text, "bounds": bounds,
	}}
}

// A row scrolled past the bottom edge stays in the element table -- the
// matcher keeps anything 10% visible -- and the tester taps its geometric
// centre, which is off the device. Geometry from a live capture of Apple's
// Reminders, scaled to this suite's 400x800 grid: the row spans y 773..835
// and its plain centre is y=804.
func TestTapAimsInsideTheScreenForARowPastTheBottomEdge(t *testing.T) {
	t.Parallel()
	for _, target := range []string{`{"text":"Bottom"}`, `{"eidx":1}`} {
		session, driver := inputSession(t, screen("List",
			row("Middle", "[16,300][386,362]"),
			row("Bottom", "[16,773][386,835]"),
		))
		if _, err := session.handleTap(context.Background(), json.RawMessage(target)); err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		if len(driver.tapped) != 1 {
			t.Fatalf("%s: tapped %+v, want one tap", target, driver.tapped)
		}
		point := driver.tapped[0]
		if point.Y >= 800 {
			t.Errorf("%s: tapped y=%v, past the 800-point bottom edge", target, point.Y)
		}
		if point.Y < 773 {
			t.Errorf("%s: tapped y=%v, above the row's own top", target, point.Y)
		}
	}
}

// The exporter writes this locator into a flow, and the engine refuses an
// absolute point outside the screen, so an off-screen centre here exports a
// flow that cannot replay.
func TestTheRecordedPointLocatorStaysOnScreen(t *testing.T) {
	t.Parallel()
	root := screen("List",
		row("Row", "[16,300][386,362]"),
		row("Row", "[16,773][386,835]"),
	)
	state := makeState("app", root)
	var bottom explore.FlatElement
	for _, element := range state.Elements {
		if bounds, ok := explore.ElementBounds(element.Node); ok && bounds.Y == 773 {
			bottom = element
		}
	}
	if bottom.Node.Attributes == nil {
		t.Fatalf("the bottom row is not in the element table: %+v", state.Elements)
	}
	locator := elementLocator(state, bottom)
	if locator == nil || locator.Kind != explore.LocatorPoint {
		t.Fatalf("locator = %+v, want a point locator for a duplicated label", locator)
	}
	if locator.Value != "201,786" {
		t.Errorf("point locator = %q, want %q", locator.Value, "201,786")
	}
}

// Nothing moves for a row the screen shows in full.
func TestAFullyVisibleRowKeepsItsPoint(t *testing.T) {
	t.Parallel()
	session, driver := inputSession(t, screen("List", row("Middle", "[16,300][386,362]")))
	if _, err := session.handleTap(context.Background(), json.RawMessage(`{"text":"Middle"}`)); err != nil {
		t.Fatal(err)
	}
	if len(driver.tapped) != 1 || driver.tapped[0].X != 201 || driver.tapped[0].Y != 331 {
		t.Fatalf("tapped %+v, want the plain centre 201,331", driver.tapped)
	}
}
