package report

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Every mmx34 and mmx35 report carried "run stopped: stopped: the step
// budget ran out": the tester's verdict already opens with the word, and
// the report prefixed it again. An operator reads this line to learn why a
// scenario produced nothing, and it stutters at them.
func TestAStoppedRunIsNotAnnouncedTwice(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"stopped: the step budget ran out before the model finished": "run stopped: the step budget ran out before the model finished",
		"pilot stop: the app is not the one under test":              "run stopped: pilot stop: the app is not the one under test",
		"": "run stopped before a verdict",
	}
	for verdict, want := range cases {
		got := stoppedReason(explore.TestResult{Verdict: verdict})
		if got != want {
			t.Errorf("stoppedReason(%q) = %q, want %q", verdict, got, want)
		}
	}
}
