package run

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Three independent booleans let a weak model contradict itself, and it did:
// mmx75 and mmx77 each wrote the routing reason into evidence -- "driver
// checks only confirm visibility, not dot indicators", "There is no 'Day
// view' button in the footer" -- and set met=false with both flags off, so a
// fact gap and a feature the app never had were both filed as defects. One
// word cannot disagree with itself.
func TestOneVerdictWordDecidesTheOutcome(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		word string
		met  bool
		miss explore.MissReason
	}{
		{word: "met", met: true},
		{word: "not_met", miss: ""},
		{word: "undecidable", miss: explore.MissUnjudged},
		{word: "inapplicable", miss: explore.MissUnpromised},
	} {
		check := readVerdict(workerVerdict{Verdict: testCase.word}, "x")
		if check.Met != testCase.met {
			t.Fatalf("%q: met = %v, want %v", testCase.word, check.Met, testCase.met)
		}
		if check.Missed != testCase.miss {
			t.Fatalf("%q: missed = %q, want %q", testCase.word, check.Missed, testCase.miss)
		}
	}
}

// A reply that answers in the old shape still counts. The word is what the
// prompt asks for; the booleans are what a model that ignores the prompt
// sends, and dropping those would turn every one of them into a defect.
func TestTheOldBooleanShapeStillDecides(t *testing.T) {
	t.Parallel()

	if check := readVerdict(workerVerdict{Met: true}, "x"); !check.Met {
		t.Fatal("a bare met=true no longer counts as met")
	}
	if check := readVerdict(workerVerdict{Undecidable: true}, "x"); check.Missed != explore.MissUnjudged {
		t.Fatalf("missed = %q, want the fact gap", check.Missed)
	}
	if check := readVerdict(workerVerdict{Inapplicable: true}, "x"); check.Missed != explore.MissUnpromised {
		t.Fatalf("missed = %q, want unpromised", check.Missed)
	}
}

// A word the model invented is not a verdict. Falling back to the booleans
// keeps the safe direction -- an unmet outcome stays a defect -- rather than
// inventing a route from a typo.
func TestAnUnknownVerdictWordFallsBackToTheFlags(t *testing.T) {
	t.Parallel()

	check := readVerdict(workerVerdict{Verdict: "probably", Undecidable: true}, "x")
	if check.Met {
		t.Fatal("an unknown word was read as met")
	}
	if check.Missed != explore.MissUnjudged {
		t.Fatalf("missed = %q, want the flags to decide", check.Missed)
	}
}
