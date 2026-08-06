package imagecheck_test

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"sync"
	"testing"

	"github.com/nohavewho/flowbaton/internal/imagecheck"
)

func TestCheckDoesNotMutateEncodedInputs(t *testing.T) {
	expected := encodedPNG(t, 2, 2, map[image.Point]color.NRGBA{
		{X: 0, Y: 0}: {R: 10, G: 20, B: 30, A: 255},
	})
	actual := encodedPNG(t, 2, 2, map[image.Point]color.NRGBA{
		{X: 1, Y: 1}: {R: 30, G: 20, B: 10, A: 255},
	})
	expectedBefore := bytes.Clone(expected)
	actualBefore := bytes.Clone(actual)
	crop := image.Rect(0, 0, 2, 2)

	if _, err := imagecheck.Check(expected, actual, &crop); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !bytes.Equal(expected, expectedBefore) {
		t.Fatal("Check() mutated expected bytes")
	}
	if !bytes.Equal(actual, actualBefore) {
		t.Fatal("Check() mutated actual bytes")
	}
}

func TestCheckIsDeterministicAndConcurrentSafe(t *testing.T) {
	expected := encodedPNG(t, 4, 4, nil)
	actual := encodedPNG(t, 4, 4, map[image.Point]color.NRGBA{
		{X: 1, Y: 1}: {R: 255, A: 255},
		{X: 2, Y: 2}: {G: 255, A: 255},
	})
	crop := image.Rect(0, 0, 4, 4)
	const goroutines = 32
	const checksPerGoroutine = 20
	errorsByGoroutine := make(chan error, goroutines)
	var waitGroup sync.WaitGroup
	waitGroup.Add(goroutines)

	for range goroutines {
		go func() {
			defer waitGroup.Done()
			for range checksPerGoroutine {
				result, err := imagecheck.Check(expected, actual, &crop)
				if err != nil {
					errorsByGoroutine <- err
					return
				}
				if result.DifferentPixels() != 2 || result.TotalPixels() != 16 || result.DifferenceRatio() != 0.125 {
					errorsByGoroutine <- fmt.Errorf(
						"result = %d/%d ratio %v, want 2/16 ratio 0.125",
						result.DifferentPixels(),
						result.TotalPixels(),
						result.DifferenceRatio(),
					)
					return
				}
			}
		}()
	}
	waitGroup.Wait()
	close(errorsByGoroutine)
	for err := range errorsByGoroutine {
		t.Errorf("concurrent Check(): %v", err)
	}
}

func FuzzCheckNoPanic(f *testing.F) {
	valid := encodedPNG(f, 2, 2, nil)
	f.Add(valid, valid, false, int16(0), int16(0), int16(0), int16(0))
	f.Add([]byte("invalid expected"), valid, true, int16(0), int16(0), int16(1), int16(1))
	f.Add(valid, []byte("invalid actual"), true, int16(-1), int16(-1), int16(3), int16(3))

	f.Fuzz(func(t *testing.T, expected, actual []byte, useCrop bool, minX, minY, maxX, maxY int16) {
		var crop *image.Rectangle
		if useCrop {
			value := image.Rectangle{
				Min: image.Pt(int(minX), int(minY)),
				Max: image.Pt(int(maxX), int(maxY)),
			}
			crop = &value
		}
		result, err := imagecheck.Check(expected, actual, crop)
		if err != nil {
			return
		}
		if result.TotalPixels() == 0 {
			t.Fatal("successful Check() returned zero TotalPixels")
		}
		if result.DifferentPixels() > result.TotalPixels() {
			t.Fatalf("DifferentPixels() = %d exceeds TotalPixels() = %d", result.DifferentPixels(), result.TotalPixels())
		}
		if ratio := result.DifferenceRatio(); ratio < 0 || ratio > 1 {
			t.Fatalf("DifferenceRatio() = %v, want [0,1]", ratio)
		}
	})
}
