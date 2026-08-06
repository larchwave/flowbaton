package imagecheck

import (
	"fmt"
	"image"
)

// Input identifies one encoded image argument.
type Input string

const (
	InputExpected Input = "expected"
	InputActual   Input = "actual"
)

// DecodeError reports that one encoded input could not be decoded.
type DecodeError struct {
	Input Input
	Err   error
}

func (err *DecodeError) Error() string {
	return fmt.Sprintf("decode %s image: %v", err.Input, err.Err)
}

func (err *DecodeError) Unwrap() error { return err.Err }

// CropError reports an empty crop or one not fully contained in both images.
type CropError struct {
	Crop           image.Rectangle
	ExpectedBounds image.Rectangle
	ActualBounds   image.Rectangle
	Reason         string
}

func (err *CropError) Error() string {
	return fmt.Sprintf(
		"invalid crop %v for expected %v and actual %v: %s",
		err.Crop,
		err.ExpectedBounds,
		err.ActualBounds,
		err.Reason,
	)
}

// DimensionMismatchError reports unequal effective dimensions after applying
// the optional crop.
type DimensionMismatchError struct {
	Expected image.Point
	Actual   image.Point
}

func (err *DimensionMismatchError) Error() string {
	return fmt.Sprintf("effective image dimensions differ: expected %v, actual %v", err.Expected, err.Actual)
}

// ThresholdError reports a non-finite or out-of-range inspection threshold.
type ThresholdError struct {
	Threshold float64
}

func (err *ThresholdError) Error() string {
	return fmt.Sprintf("invalid difference threshold %v: expected a value in [0,1]", err.Threshold)
}
