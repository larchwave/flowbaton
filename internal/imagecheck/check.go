// Package imagecheck provides deterministic, host-side encoded image
// inspection without external dependencies.
package imagecheck

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
)

// Result is an immutable pixel-inspection value. Its state is exposed only
// through value-returning accessors.
type Result struct {
	differentPixels uint64
	totalPixels     uint64
	differenceRatio float64
}

// DifferentPixels returns the exact number of unequal normalized RGBA pixels.
func (result Result) DifferentPixels() uint64 { return result.differentPixels }

// TotalPixels returns the exact number of evaluated pixels.
func (result Result) TotalPixels() uint64 { return result.totalPixels }

// DifferenceRatio returns DifferentPixels divided by TotalPixels.
func (result Result) DifferenceRatio() float64 { return result.differenceRatio }

// WithinThreshold reports whether the difference ratio is at or below the
// inclusive threshold. Thresholds must be finite values in [0,1].
func (result Result) WithinThreshold(threshold float64) (bool, error) {
	if math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 || threshold > 1 {
		return false, &ThresholdError{Threshold: threshold}
	}
	return result.differenceRatio <= threshold, nil
}

// Check decodes and checks expected and actual encoded images.
func Check(expected, actual []byte, crop *image.Rectangle) (Result, error) {
	expectedImage, _, err := image.Decode(bytes.NewReader(expected))
	if err != nil {
		return Result{}, &DecodeError{Input: InputExpected, Err: err}
	}
	actualImage, _, err := image.Decode(bytes.NewReader(actual))
	if err != nil {
		return Result{}, &DecodeError{Input: InputActual, Err: err}
	}
	expectedBounds := expectedImage.Bounds()
	actualBounds := actualImage.Bounds()
	expectedEffective := expectedBounds
	actualEffective := actualBounds
	if crop != nil {
		selected := *crop
		if selected.Empty() {
			return Result{}, &CropError{
				Crop:           selected,
				ExpectedBounds: expectedBounds,
				ActualBounds:   actualBounds,
				Reason:         "crop is empty",
			}
		}
		if !selected.In(expectedBounds) || !selected.In(actualBounds) {
			return Result{}, &CropError{
				Crop:           selected,
				ExpectedBounds: expectedBounds,
				ActualBounds:   actualBounds,
				Reason:         "crop must be fully contained in both images",
			}
		}
		expectedEffective = selected
		actualEffective = selected
	}
	if expectedEffective.Size() != actualEffective.Size() || expectedEffective.Empty() || actualEffective.Empty() {
		return Result{}, &DimensionMismatchError{Expected: expectedEffective.Size(), Actual: actualEffective.Size()}
	}
	total := uint64(expectedEffective.Dx()) * uint64(expectedEffective.Dy())

	var different uint64
	for offsetY := 0; offsetY < expectedEffective.Dy(); offsetY++ {
		for offsetX := 0; offsetX < expectedEffective.Dx(); offsetX++ {
			expectedRGBA := normalizedRGBA(expectedImage.At(expectedEffective.Min.X+offsetX, expectedEffective.Min.Y+offsetY))
			actualRGBA := normalizedRGBA(actualImage.At(actualEffective.Min.X+offsetX, actualEffective.Min.Y+offsetY))
			if expectedRGBA != actualRGBA {
				different++
			}
		}
	}
	return Result{
		differentPixels: different,
		totalPixels:     total,
		differenceRatio: float64(different) / float64(total),
	}, nil
}

type rgba struct {
	red, green, blue, alpha uint32
}

func normalizedRGBA(value interface {
	RGBA() (uint32, uint32, uint32, uint32)
}) rgba {
	red, green, blue, alpha := value.RGBA()
	return rgba{red: red, green: green, blue: blue, alpha: alpha}
}
