package engine

import (
	"math"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

const (
	// scrollUntilVisibleMinimumStep keeps a correcting scroll large enough to
	// move anything: a drag of a few points is absorbed as a touch.
	scrollUntilVisibleMinimumStep = 0.05
	// scrollUntilVisibleCoveredStep is the step for a target that is already
	// inside the viewport and still not accepted, so its geometry gives no
	// distance to aim for.
	scrollUntilVisibleCoveredStep = 0.25
)

// scrollUntilVisibleCourse chooses each scroll of a scrollUntilVisible loop.
//
// The authored direction and speed are the search: they apply while the
// target has never been seen. Once the target is on screen, the step comes
// from its geometry — the side it hangs over and by how much — capped at the
// authored speed. A target that was seen and is gone again has been passed,
// so the next step reverses and halves (issue #16: a fixed step in a fixed
// direction skipped the interval where a tall card was fully visible and
// then carried on to the timeout).
type scrollUntilVisibleCourse struct {
	authored      device.Direction
	speed         float64
	viewport      device.Bounds
	seen          bool
	scrolled      bool
	lastDirection device.Direction
	lastAmount    float64
	passes        int
}

func newScrollUntilVisibleCourse(direction device.Direction, speed float64, viewport device.Bounds) *scrollUntilVisibleCourse {
	return &scrollUntilVisibleCourse{authored: direction, speed: speed, viewport: viewport}
}

// next is the scroll to perform after observing element (nil when the target
// is not in the visible hierarchy).
func (course *scrollUntilVisibleCourse) next(element *hierarchy.Element) device.ScrollVerticalRequest {
	switch {
	case element != nil && element.HasBounds:
		course.seen = true
		direction, amount := course.towardViewport(element.Bounds)
		return course.record(direction, amount)
	case course.seen && course.scrolled:
		course.passes++
		return course.record(oppositeDirection(course.lastDirection), math.Max(course.lastAmount/2, scrollUntilVisibleMinimumStep))
	default:
		return course.record(course.authored, course.speed)
	}
}

// towardViewport is the direction that brings the overhanging side of bounds
// back inside the viewport, and the fraction of the viewport that side hangs
// over, along the authored axis.
func (course *scrollUntilVisibleCourse) towardViewport(bounds device.Bounds) (device.Direction, float64) {
	var before, after float64
	var forward, backward device.Direction
	var axis float64
	if course.horizontal() {
		before = float64(course.viewport.X - bounds.X)
		after = float64((bounds.X + bounds.Width) - (course.viewport.X + course.viewport.Width))
		forward, backward, axis = "RIGHT", "LEFT", float64(course.viewport.Width)
	} else {
		before = float64(course.viewport.Y - bounds.Y)
		after = float64((bounds.Y + bounds.Height) - (course.viewport.Y + course.viewport.Height))
		forward, backward, axis = "DOWN", "UP", float64(course.viewport.Height)
	}
	before, after = math.Max(before, 0), math.Max(after, 0)
	if before == 0 && after == 0 {
		return course.authored, math.Min(course.speed, scrollUntilVisibleCoveredStep)
	}
	direction, overhang := forward, after
	if before > after {
		direction, overhang = backward, before
	}
	if axis <= 0 {
		return direction, course.speed
	}
	return direction, math.Min(course.speed, math.Max(overhang/axis, scrollUntilVisibleMinimumStep))
}

func (course *scrollUntilVisibleCourse) horizontal() bool {
	return course.authored == "LEFT" || course.authored == "RIGHT"
}

func (course *scrollUntilVisibleCourse) record(direction device.Direction, amount float64) device.ScrollVerticalRequest {
	course.scrolled = true
	course.lastDirection = direction
	course.lastAmount = amount
	return device.ScrollVerticalRequest{Direction: direction, Amount: amount}
}

func oppositeDirection(direction device.Direction) device.Direction {
	switch direction {
	case "UP":
		return "DOWN"
	case "DOWN":
		return "UP"
	case "LEFT":
		return "RIGHT"
	default:
		return "LEFT"
	}
}
