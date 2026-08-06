package engine

import "testing"

// TestEnumDirectionOrientationNormalizeCase covers case-insensitive direction
// and orientation values and their canonical uppercase driver form.
// setAirplaneMode is intentionally case-sensitive.
func TestEnumDirectionOrientationNormalizeCase(t *testing.T) {
	t.Parallel()

	t.Run("swipe direction", func(t *testing.T) {
		for in, want := range map[string]string{"up": "UP", "Left": "LEFT", "RIGHT": "RIGHT", "dOwN": "DOWN"} {
			got, err := validatedSwipeDirection(in)
			if err != nil || string(got) != want {
				t.Fatalf("validatedSwipeDirection(%q) = %q, %v; want %q", in, got, err, want)
			}
		}
		if _, err := validatedSwipeDirection("sideways"); err == nil {
			t.Fatal("sideways must reject")
		}
	})

	t.Run("scrollUntilVisible direction", func(t *testing.T) {
		for in, want := range map[string]string{"down": "DOWN", "Up": "UP", "left": "LEFT", "RIGHT": "RIGHT"} {
			got, err := validatedScrollUntilVisibleDirection(in)
			if err != nil || string(got) != want {
				t.Fatalf("validatedScrollUntilVisibleDirection(%q) = %q, %v; want %q", in, got, err, want)
			}
		}
		// scrollUntilVisible accepts UP/DOWN/LEFT/RIGHT (contract with swipe); a
		// non-direction still rejects.
		if _, err := validatedScrollUntilVisibleDirection("sideways"); err == nil {
			t.Fatal("sideways must reject for scrollUntilVisible")
		}
	})

	t.Run("orientation", func(t *testing.T) {
		for in, want := range map[string]string{"portrait": "PORTRAIT", "landscape_left": "LANDSCAPE_LEFT", "Upside_Down": "UPSIDE_DOWN"} {
			got, err := canonicalOrientation(in)
			if err != nil || string(got) != want {
				t.Fatalf("canonicalOrientation(%q) = %q, %v; want %q", in, got, err, want)
			}
		}
		if _, err := canonicalOrientation("tilted"); err == nil {
			t.Fatal("tilted must reject")
		}
	})
}
