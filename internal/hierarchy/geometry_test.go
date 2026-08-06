package hierarchy

import (
	"errors"
	"math"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func TestBoundsGeometryParsesFormatsAndMeasures(t *testing.T) {
	t.Parallel()

	bounds, err := ParseBounds("[-10,20][30,70]")
	if err != nil {
		t.Fatalf("ParseBounds: %v", err)
	}
	want := device.Bounds{X: -10, Y: 20, Width: 40, Height: 50}
	if bounds != want {
		t.Fatalf("ParseBounds = %#v, want %#v", bounds, want)
	}
	if got := FormatBounds(bounds); got != "[-10,20][30,70]" {
		t.Fatalf("FormatBounds = %q", got)
	}
	if got := Area(bounds); got != 2_000 {
		t.Fatalf("Area = %d, want 2000", got)
	}
	if got := Center(bounds); got != (device.Point{X: 10, Y: 45}) {
		t.Fatalf("Center = %#v, want (10,45)", got)
	}
}

func TestVisiblePercentageClipsEverySideAndHandlesDegenerateBounds(t *testing.T) {
	t.Parallel()

	viewport := device.Bounds{Width: 100, Height: 100}
	tests := []struct {
		name   string
		bounds device.Bounds
		want   float64
	}{
		{name: "fully inside", bounds: device.Bounds{X: 10, Y: 20, Width: 30, Height: 40}, want: 1},
		{name: "left clipped", bounds: device.Bounds{X: -50, Width: 100, Height: 100}, want: 0.5},
		{name: "right clipped", bounds: device.Bounds{X: 50, Width: 100, Height: 100}, want: 0.5},
		{name: "top clipped", bounds: device.Bounds{Y: -75, Width: 100, Height: 100}, want: 0.25},
		{name: "bottom clipped", bounds: device.Bounds{Y: 75, Width: 100, Height: 100}, want: 0.25},
		{name: "overflowing every side", bounds: device.Bounds{X: -10, Y: -10, Width: 120, Height: 120}, want: 1},
		{name: "zero width", bounds: device.Bounds{Width: 0, Height: 10}, want: 0},
		{name: "zero height", bounds: device.Bounds{Width: 10, Height: 0}, want: 0},
		{name: "outside", bounds: device.Bounds{X: 101, Width: 10, Height: 10}, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := VisiblePercentage(test.bounds, viewport); math.Abs(got-test.want) > 1e-9 {
				t.Fatalf("VisiblePercentage = %v, want %v", got, test.want)
			}
		})
	}
}

func TestVisibilityKeepsExactlyTenPercentAndDropsLess(t *testing.T) {
	t.Parallel()

	viewport := device.Bounds{Width: 100, Height: 100}
	exactlyTenPercent := device.Bounds{X: 90, Width: 100, Height: 100}
	lessThanTenPercent := device.Bounds{X: 91, Width: 100, Height: 100}
	if !IsVisible(exactlyTenPercent, viewport) {
		t.Fatal("exactly 10% visible bounds were dropped")
	}
	if IsVisible(lessThanTenPercent, viewport) {
		t.Fatal("less than 10% visible bounds were retained")
	}
}

func TestParseBoundsReturnsTypedErrors(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "[0,0,10,10]", "[10,0][5,10]", "[0,10][10,5]"} {
		_, err := ParseBounds(value)
		if err == nil {
			t.Fatalf("ParseBounds(%q) succeeded", value)
		}
		var boundsErr *BoundsError
		if !errors.As(err, &boundsErr) {
			t.Fatalf("ParseBounds(%q) error %T, want *BoundsError", value, err)
		}
	}
}
