package imagecheck_test

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/larchwave/flowbaton/internal/imagecheck"
)

func TestCheckCropIgnoresPixelsOutsideAndCountsPixelsInside(t *testing.T) {
	expected := encodedPNG(t, 3, 2, nil)
	crop := image.Rect(1, 0, 3, 2)

	outsideOnly := encodedPNG(t, 3, 2, map[image.Point]color.NRGBA{
		{X: 0, Y: 0}: {R: 255, A: 255},
	})
	ignored, err := imagecheck.Check(expected, outsideOnly, &crop)
	if err != nil {
		t.Fatalf("Check(outside crop) error = %v", err)
	}
	if ignored.DifferentPixels() != 0 || ignored.TotalPixels() != 4 {
		t.Fatalf("outside-crop result = %d/%d, want 0/4", ignored.DifferentPixels(), ignored.TotalPixels())
	}

	insideAndOutside := encodedPNG(t, 3, 2, map[image.Point]color.NRGBA{
		{X: 0, Y: 0}: {R: 255, A: 255},
		{X: 2, Y: 1}: {G: 255, A: 255},
	})
	isolated, err := imagecheck.Check(expected, insideAndOutside, &crop)
	if err != nil {
		t.Fatalf("Check(inside crop) error = %v", err)
	}
	if isolated.DifferentPixels() != 1 || isolated.TotalPixels() != 4 || isolated.DifferenceRatio() != 0.25 {
		t.Fatalf("inside-crop result = %d/%d ratio %v, want 1/4 ratio 0.25", isolated.DifferentPixels(), isolated.TotalPixels(), isolated.DifferenceRatio())
	}
	if crop != image.Rect(1, 0, 3, 2) {
		t.Fatalf("Check() mutated crop to %v", crop)
	}
}

func TestCheckCropCanSelectEqualEffectiveDimensions(t *testing.T) {
	expected := encodedPNG(t, 2, 2, nil)
	actual := encodedPNG(t, 3, 3, nil)
	crop := image.Rect(0, 0, 2, 2)

	result, err := imagecheck.Check(expected, actual, &crop)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.TotalPixels() != 4 || result.DifferentPixels() != 0 {
		t.Fatalf("result = %d/%d, want 0/4", result.DifferentPixels(), result.TotalPixels())
	}
}

func TestCheckRejectsInvalidEncodedImagesWithTypedError(t *testing.T) {
	valid := encodedPNG(t, 1, 1, nil)
	for _, test := range []struct {
		name     string
		expected []byte
		actual   []byte
		want     imagecheck.Input
	}{
		{name: "expected", expected: []byte("not an image"), actual: valid, want: imagecheck.InputExpected},
		{name: "actual", expected: valid, actual: []byte("not an image"), want: imagecheck.InputActual},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := imagecheck.Check(test.expected, test.actual, nil)
			var decodeErr *imagecheck.DecodeError
			if !errors.As(err, &decodeErr) {
				t.Fatalf("Check() error = %T %v, want *imagecheck.DecodeError", err, err)
			}
			if decodeErr.Input != test.want {
				t.Fatalf("DecodeError.Input = %q, want %q", decodeErr.Input, test.want)
			}
			if !errors.Is(err, decodeErr.Err) {
				t.Fatalf("DecodeError does not unwrap its cause")
			}
		})
	}
}

func TestCheckRejectsEffectiveDimensionMismatchWithTypedError(t *testing.T) {
	expected := encodedPNG(t, 2, 2, nil)
	actual := encodedPNG(t, 3, 2, nil)

	_, err := imagecheck.Check(expected, actual, nil)
	var dimensionErr *imagecheck.DimensionMismatchError
	if !errors.As(err, &dimensionErr) {
		t.Fatalf("Check() error = %T %v, want *imagecheck.DimensionMismatchError", err, err)
	}
	if dimensionErr.Expected != image.Pt(2, 2) || dimensionErr.Actual != image.Pt(3, 2) {
		t.Fatalf("dimension error = expected %v actual %v", dimensionErr.Expected, dimensionErr.Actual)
	}
}

func TestCheckRejectsEmptyOrOutOfImageCropWithTypedError(t *testing.T) {
	encoded := encodedPNG(t, 3, 3, nil)
	for _, test := range []struct {
		name string
		crop image.Rectangle
	}{
		{name: "empty", crop: image.Rect(1, 1, 1, 2)},
		{name: "negative coordinate", crop: image.Rect(-1, 0, 1, 1)},
		{name: "outside expected and actual", crop: image.Rect(0, 0, 4, 3)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := imagecheck.Check(encoded, bytes.Clone(encoded), &test.crop)
			var cropErr *imagecheck.CropError
			if !errors.As(err, &cropErr) {
				t.Fatalf("Check() error = %T %v, want *imagecheck.CropError", err, err)
			}
			if cropErr.Crop != test.crop {
				t.Fatalf("CropError.Crop = %v, want %v", cropErr.Crop, test.crop)
			}
		})
	}
}
