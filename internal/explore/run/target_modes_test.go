package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// mmx76 lost two steps to a model that sent an index and the label of that
// same row together. The refusal is right -- two fields can disagree -- but
// it named the rule and not the mistake, so the model had to guess which of
// the three it had sent one too many of.
func TestRefusingATargetSaysWhichFieldsItGot(t *testing.T) {
	t.Parallel()

	index := 53
	_, err := resolvePoint(&explore.ScreenState{}, targetArgs{EIDX: &index, Text: "Inbox"})
	if err == nil {
		t.Fatal("resolvePoint() = nil error, want a refusal for two targets")
	}
	if !strings.Contains(err.Error(), "got eidx and text") {
		t.Fatalf("error = %q, want it to name the two fields it was given", err)
	}
}

// A target with no field at all is the other half of the same mistake, and
// has nothing to list.
func TestRefusingAnEmptyTargetSaysSoPlainly(t *testing.T) {
	t.Parallel()

	_, err := resolvePoint(&explore.ScreenState{Viewport: device.Bounds{Width: 1, Height: 1}}, targetArgs{})
	if err == nil {
		t.Fatal("resolvePoint() = nil error, want a refusal for no target")
	}
	if !strings.Contains(err.Error(), "none") {
		t.Fatalf("error = %q, want it to say no target was given", err)
	}
}
