package explore

import (
	"strings"
	"testing"
)

// A wrapped Go error puts the root cause last, and cutting the tail throws
// away the only part that answers anything. mmx82 ended a run on
//
//	explore/run: tester turn 14: explore: screen cannot be observed: observe
//	after scroll: research: hierarchy: ios runner: precondition: no named
//	application is in the foreground: com.apple.mobilecal is running in the
//	background
//
// and the note stopped at "no named application is in", one clause short of
// the crash-or-background answer the runner had just been rebuilt to give.
func TestACauseKeepsItsRootWhenItIsTooLong(t *testing.T) {
	t.Parallel()

	cause := "explore/run: tester turn 14: explore: screen cannot be observed: " +
		"observe after scroll: research: hierarchy: ios runner: precondition: " +
		"no named application is in the foreground: com.apple.mobilecal is " +
		"running in the background"
	short := truncateCause(cause)

	if !strings.Contains(short, "running in the background") {
		t.Fatalf("cause = %q, want the root the chain ends on", short)
	}
	if !strings.HasPrefix(short, "explore/run: tester turn 14") {
		t.Fatalf("cause = %q, want the head that says what was being done", short)
	}
	if len([]rune(short)) > len([]rune(cause)) {
		t.Fatalf("cause grew from %d to %d runes", len([]rune(cause)), len([]rune(short)))
	}
}

// A cause that fits is left exactly as it is, with no ellipsis invented for it.
func TestAShortCauseIsUntouched(t *testing.T) {
	t.Parallel()

	cause := "explore: device unreachable"
	if short := truncateCause(cause); short != cause {
		t.Fatalf("cause = %q, want it unchanged", short)
	}
}
