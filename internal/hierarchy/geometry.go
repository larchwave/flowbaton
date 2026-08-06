// Package hierarchy provides platform-neutral geometry and normalized tree
// helpers over the frozen device DTOs.
package hierarchy

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/larchwave/flowbaton/internal/device"
)

var boundsPattern = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)

// MinimumVisiblePercentage is the exact hierarchy retention boundary.
const MinimumVisiblePercentage = 0.10

// BoundsError reports a malformed or inverted Android-style bounds value.
type BoundsError struct {
	Value  string
	Reason string
}

func (e *BoundsError) Error() string {
	return fmt.Sprintf("invalid bounds %q: %s", e.Value, e.Reason)
}

// ParseBounds parses [left,top][right,bottom] into the normalized bounds DTO.
func ParseBounds(value string) (device.Bounds, error) {
	matches := boundsPattern.FindStringSubmatch(value)
	if matches == nil {
		return device.Bounds{}, &BoundsError{Value: value, Reason: "expected [left,top][right,bottom]"}
	}
	coordinates := make([]int, 4)
	for index := range coordinates {
		parsed, err := strconv.Atoi(matches[index+1])
		if err != nil {
			return device.Bounds{}, &BoundsError{Value: value, Reason: "coordinate is outside the host integer range"}
		}
		coordinates[index] = parsed
	}
	if coordinates[2] < coordinates[0] || coordinates[3] < coordinates[1] {
		return device.Bounds{}, &BoundsError{Value: value, Reason: "right and bottom must not precede left and top"}
	}
	return device.Bounds{
		X:      coordinates[0],
		Y:      coordinates[1],
		Width:  coordinates[2] - coordinates[0],
		Height: coordinates[3] - coordinates[1],
	}, nil
}

// FormatBounds renders normalized bounds using Android hierarchy syntax.
func FormatBounds(bounds device.Bounds) string {
	return fmt.Sprintf("[%d,%d][%d,%d]", bounds.X, bounds.Y, bounds.X+bounds.Width, bounds.Y+bounds.Height)
}

// Area returns the non-negative rectangular area.
func Area(bounds device.Bounds) int64 {
	if bounds.Width <= 0 || bounds.Height <= 0 {
		return 0
	}
	return int64(bounds.Width) * int64(bounds.Height)
}

// Center returns the exact geometric center, including half-pixel positions.
func Center(bounds device.Bounds) device.Point {
	return device.Point{
		X: float64(bounds.X) + float64(bounds.Width)/2,
		Y: float64(bounds.Y) + float64(bounds.Height)/2,
	}
}

// VisiblePercentage returns the portion of an element visible in the viewport.
// An element covering every viewport edge is fully visible even when the
// element itself extends beyond those edges.
func VisiblePercentage(bounds, viewport device.Bounds) float64 {
	area := Area(bounds)
	if area == 0 || Area(viewport) == 0 {
		return 0
	}
	left, top := int64(bounds.X), int64(bounds.Y)
	right := left + int64(bounds.Width)
	bottom := top + int64(bounds.Height)
	viewportLeft, viewportTop := int64(viewport.X), int64(viewport.Y)
	viewportRight := viewportLeft + int64(viewport.Width)
	viewportBottom := viewportTop + int64(viewport.Height)
	if left <= viewportLeft && top <= viewportTop && right >= viewportRight && bottom >= viewportBottom {
		return 1
	}
	intersectionWidth := min64(right, viewportRight) - max64(left, viewportLeft)
	intersectionHeight := min64(bottom, viewportBottom) - max64(top, viewportTop)
	if intersectionWidth <= 0 || intersectionHeight <= 0 {
		return 0
	}
	return float64(intersectionWidth*intersectionHeight) / float64(area)
}

// IsVisible applies the exact 10% hierarchy-retention threshold.
func IsVisible(bounds, viewport device.Bounds) bool {
	return VisiblePercentage(bounds, viewport) >= MinimumVisiblePercentage
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
