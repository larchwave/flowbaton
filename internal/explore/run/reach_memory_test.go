package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// mmx71 sent the same reach five times, once per scenario, and all five spent
// the whole eight-turn budget on "search-add-4d3ffed3" and gave up: forty
// worker turns for nothing. The first failure is information the session
// already has.
//
// Measured on real captures, the target is real and the arrival check works
// -- Calendar's years view keys as search-add-fff26e68 and NamesTheSameScreen
// accepts it. What fails is the navigation: the app relaunches on
// september-2026 or august-single-day and the worker cannot get back in
// eight turns.
//
// A reach is only refused for the same target FROM THE SAME SCREEN. Another
// origin is another route, and a recipe is still replayed either way: a
// stored recipe costs no model turns, which is the thing being rationed.
func TestReachDoesNotRepeatAFailureFromTheSameScreen(t *testing.T) {
	t.Parallel()

	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: repeatState(home, 4*navigatorLoopBound)}
	// A worker that acts forever and never arrives.
	replies := []explore.Message{}
	for turn := 0; turn < 4*navigatorLoopBound; turn++ {
		replies = append(replies, toolCall("t", "tap", `{"eidx":0}`))
	}
	worker := &scriptedLLM{replies: replies}
	navigator := newNavigator(driver, observer, worker, nil)

	if _, _, err := navigator.Reach(context.Background(), "settings"); err == nil {
		t.Fatal("the first reach succeeded; the fixture cannot arrive")
	}
	spent := len(worker.requests)
	if spent == 0 {
		t.Fatal("the first reach spent no model turns")
	}

	_, _, err := navigator.Reach(context.Background(), "settings")
	if err == nil {
		t.Fatal("the second reach succeeded")
	}
	var reach *ReachError
	if !errors.As(err, &reach) || !strings.Contains(reach.Reason, "earlier") {
		t.Fatalf("second reach error = %v, want it to say the route already failed", err)
	}
	if len(worker.requests) != spent {
		t.Errorf("the second reach spent %d more model turns", len(worker.requests)-spent)
	}
}

func repeatState(state *explore.ScreenState, times int) []*explore.ScreenState {
	out := make([]*explore.ScreenState, 0, times)
	for i := 0; i < times; i++ {
		out = append(out, state)
	}
	return out
}
