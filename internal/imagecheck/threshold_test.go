package imagecheck_test

import (
	"errors"
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/larchwave/flowbaton/internal/imagecheck"
)

func TestResultWithinThresholdUsesInclusiveBoundary(t *testing.T) {
	expected := encodedPNG(t, 20, 10, nil)
	actual := encodedPNG(t, 20, 10, map[image.Point]color.NRGBA{
		{X: 19, Y: 9}: {R: 255, A: 255},
	})
	result, err := imagecheck.Check(expected, actual, nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.DifferenceRatio() != 0.005 {
		t.Fatalf("DifferenceRatio() = %v, want 0.005", result.DifferenceRatio())
	}

	passes, err := result.WithinThreshold(0.005)
	if err != nil {
		t.Fatalf("WithinThreshold() error = %v", err)
	}
	if !passes {
		t.Fatal("WithinThreshold(0.005) = false at inclusive boundary")
	}
	passes, err = result.WithinThreshold(math.Nextafter(0.005, 0))
	if err != nil {
		t.Fatalf("WithinThreshold() error = %v", err)
	}
	if passes {
		t.Fatal("WithinThreshold(value below ratio) = true, want false")
	}
}

func TestResultWithinThresholdRejectsInvalidThreshold(t *testing.T) {
	result, err := imagecheck.Check(encodedPNG(t, 1, 1, nil), encodedPNG(t, 1, 1, nil), nil)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, threshold := range []float64{math.NaN(), math.Inf(-1), -0.1, 1.1, math.Inf(1)} {
		_, err := result.WithinThreshold(threshold)
		var thresholdErr *imagecheck.ThresholdError
		if !errors.As(err, &thresholdErr) {
			t.Fatalf("WithinThreshold(%v) error = %T %v, want *imagecheck.ThresholdError", threshold, err, err)
		}
	}
}
