package imagecheck_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/larchwave/flowbaton/internal/imagecheck"
)

func TestCheckIdenticalImages(t *testing.T) {
	expected := encodedPNG(t, 2, 2, map[image.Point]color.NRGBA{
		{X: 0, Y: 0}: {R: 10, G: 20, B: 30, A: 255},
		{X: 1, Y: 1}: {R: 40, G: 50, B: 60, A: 200},
	})
	actual := bytes.Clone(expected)

	result, err := imagecheck.Check(expected, actual, nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.DifferentPixels() != 0 {
		t.Fatalf("DifferentPixels() = %d, want 0", result.DifferentPixels())
	}
	if result.TotalPixels() != 4 {
		t.Fatalf("TotalPixels() = %d, want 4", result.TotalPixels())
	}
	if result.DifferenceRatio() != 0 {
		t.Fatalf("DifferenceRatio() = %v, want 0", result.DifferenceRatio())
	}
}

func TestCheckReportsExactOnePixelRatio(t *testing.T) {
	expected := encodedPNG(t, 2, 2, nil)
	actual := encodedPNG(t, 2, 2, map[image.Point]color.NRGBA{
		{X: 1, Y: 0}: {R: 255, A: 255},
	})

	result, err := imagecheck.Check(expected, actual, nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.DifferentPixels() != 1 || result.TotalPixels() != 4 {
		t.Fatalf("pixel counts = %d/%d, want 1/4", result.DifferentPixels(), result.TotalPixels())
	}
	if result.DifferenceRatio() != 0.25 {
		t.Fatalf("DifferenceRatio() = %v, want 0.25", result.DifferenceRatio())
	}
}

func TestCheckDetectsNormalizedRGBAChannelDifferences(t *testing.T) {
	expectedColor := color.NRGBA{R: 20, G: 40, B: 60, A: 255}
	for _, test := range []struct {
		name   string
		actual color.NRGBA
	}{
		{name: "red", actual: color.NRGBA{R: 21, G: 40, B: 60, A: 255}},
		{name: "green", actual: color.NRGBA{R: 20, G: 41, B: 60, A: 255}},
		{name: "blue", actual: color.NRGBA{R: 20, G: 40, B: 61, A: 255}},
		{name: "alpha", actual: color.NRGBA{R: 20, G: 40, B: 60, A: 254}},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected := encodedPNG(t, 1, 1, map[image.Point]color.NRGBA{{}: expectedColor})
			actual := encodedPNG(t, 1, 1, map[image.Point]color.NRGBA{{}: test.actual})

			result, err := imagecheck.Check(expected, actual, nil)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if result.DifferentPixels() != 1 || result.DifferenceRatio() != 1 {
				t.Fatalf("result = %d/%d ratio %v, want 1/1 ratio 1", result.DifferentPixels(), result.TotalPixels(), result.DifferenceRatio())
			}
		})
	}
}

func encodedPNG(t testing.TB, width, height int, pixels map[image.Point]color.NRGBA) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for point, pixel := range pixels {
		value.SetNRGBA(point.X, point.Y, pixel)
	}
	var output bytes.Buffer
	if err := png.Encode(&output, value); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return output.Bytes()
}
