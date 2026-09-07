package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

func courseElement(bounds device.Bounds) *hierarchy.Element {
	return &hierarchy.Element{Bounds: bounds, HasBounds: true}
}

func TestScrollUntilVisibleCourseSearchesThenConverges(t *testing.T) {
	t.Parallel()
	viewport := device.Bounds{Width: 100, Height: 100}
	course := newScrollUntilVisibleCourse("DOWN", 0.4, viewport)

	steps := []struct {
		name    string
		element *hierarchy.Element
		want    device.ScrollVerticalRequest
	}{
		{name: "never seen keeps the authored step", want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: 0.4}},
		{name: "still unseen keeps searching", want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: 0.4}},
		{name: "half over the bottom edge steps by the overhang", element: courseElement(device.Bounds{Y: 80, Width: 100, Height: 40}), want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: 0.2}},
		{name: "gone after being seen reverses with half the step", want: device.ScrollVerticalRequest{Direction: "UP", Amount: 0.1}},
		{name: "gone again reverses and halves again", want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: scrollUntilVisibleMinimumStep}},
		{name: "over the top edge steps up whatever was authored", element: courseElement(device.Bounds{Y: -30, Width: 100, Height: 40}), want: device.ScrollVerticalRequest{Direction: "UP", Amount: 0.3}},
		{name: "a large overhang is capped at the authored speed", element: courseElement(device.Bounds{Y: 50, Width: 100, Height: 200}), want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: 0.4}},
		{name: "inside the viewport but refused takes the covered step", element: courseElement(device.Bounds{Y: 40, Width: 100, Height: 20}), want: device.ScrollVerticalRequest{Direction: "DOWN", Amount: scrollUntilVisibleCoveredStep}},
	}
	for _, step := range steps {
		if got := course.next(step.element); got != step.want {
			t.Fatalf("%s: request = %#v, want %#v", step.name, got, step.want)
		}
	}
	if course.passes != 2 {
		t.Fatalf("passes = %d, want 2", course.passes)
	}
}

func TestScrollUntilVisibleCourseHorizontalAxis(t *testing.T) {
	t.Parallel()
	viewport := device.Bounds{Width: 200, Height: 100}
	course := newScrollUntilVisibleCourse("RIGHT", 0.5, viewport)
	if got, want := course.next(nil), (device.ScrollVerticalRequest{Direction: "RIGHT", Amount: 0.5}); got != want {
		t.Fatalf("search = %#v, want %#v", got, want)
	}
	if got, want := course.next(courseElement(device.Bounds{X: 180, Width: 60, Height: 20})), (device.ScrollVerticalRequest{Direction: "RIGHT", Amount: 0.2}); got != want {
		t.Fatalf("overhang right = %#v, want %#v", got, want)
	}
	if got, want := course.next(courseElement(device.Bounds{X: -50, Width: 60, Height: 20})), (device.ScrollVerticalRequest{Direction: "LEFT", Amount: 0.25}); got != want {
		t.Fatalf("overhang left = %#v, want %#v", got, want)
	}
	if got, want := course.next(nil), (device.ScrollVerticalRequest{Direction: "RIGHT", Amount: 0.125}); got != want {
		t.Fatalf("passed = %#v, want %#v", got, want)
	}
}

// TestScrollUntilVisiblePassedTargetIsRecovered runs the whole command: the
// target is missed, seen hanging over the bottom, passed, then found after the
// reversing step. Before issue #16 the third scroll repeated DOWN 0.4 and the
// command ran to its timeout.
func TestScrollUntilVisiblePassedTargetIsRecovered(t *testing.T) {
	t.Parallel()
	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 40}),
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		batch3Tree("Ready", device.Bounds{Y: 30, Width: 100, Height: 40}),
	}, []error{nil, nil, nil}, nil)
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(900, 0), true))
	if err != nil {
		t.Fatalf("recovered target error = %v", err)
	}
	want := []device.ScrollVerticalRequest{
		{Direction: "DOWN", Amount: 0.4},
		{Direction: "DOWN", Amount: 0.2},
		{Direction: "UP", Amount: 0.1},
	}
	if got := batch3ScrollRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("scroll requests = %#v, want %#v", got, want)
	}
}

func TestScrollUntilVisibleTimeoutNamesThePasses(t *testing.T) {
	t.Parallel()
	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 40}),
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
	}, []error{nil, nil, nil}, nil)
	clock := newBatch3Clock(time.Unix(910, 0), true)
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"timeout": int64(150)}), nil, driver, clock)
	var assertion *AssertionError
	if !errors.As(err, &assertion) || !strings.Contains(err.Error(), "passed") {
		t.Fatalf("timeout after a pass: error = %T %v", err, err)
	}
	_ = enginetest.MethodScrollVertical
}
