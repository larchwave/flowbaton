package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/explore"
)

func newNavigator(driver *fakeDriver, observer *fakeObserver, worker explore.LLM, store explore.ExperienceStore) *Navigator {
	return &Navigator{
		Driver:     driver,
		Observer:   observer,
		Worker:     worker,
		Config:     explore.Config{AppID: "com.example.app", Clock: func() time.Time { return time.Unix(50, 0) }},
		Experience: store,
		Sleep:      noSleep,
	}
}

func TestEnsureReadyRetriesWithKillAndRelaunchOnce(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{
		states: []*explore.ScreenState{home},
		errs:   []error{errors.New("blank capture"), nil},
	}
	navigator := newNavigator(driver, observer, &scriptedLLM{}, nil)
	state, err := navigator.EnsureReady(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || !state.Signature.Same(home.Signature) {
		t.Fatalf("state %+v", state)
	}
	joined := strings.Join(driver.calls, ",")
	if !strings.Contains(joined, "KillApp:com.example.app,LaunchApp:com.example.app,Settle,KillApp:com.example.app,LaunchApp:com.example.app") {
		t.Fatalf("driver calls %v", driver.calls)
	}
}

func TestEnsureReadyFailsWhenRelaunchDoesNotHelp(t *testing.T) {
	driver := &fakeDriver{}
	observer := &fakeObserver{errs: []error{errors.New("dead"), errors.New("still dead")}}
	navigator := newNavigator(driver, observer, &scriptedLLM{}, nil)
	if _, err := navigator.EnsureReady(context.Background()); err == nil {
		t.Fatal("unobservable app accepted")
	}
}

func TestReachReplaysStoredRecipeWithoutModelTurns(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	settings := makeState("com.example.app", screen("Settings", button("Toggle", "toggle_button", "[0,0][50,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home, settings}}
	store := &fakeStore{entries: map[string]map[string]string{
		home.Signature.Key(): {"reach settings": `tap {"eidx":0}`},
	}}
	worker := &scriptedLLM{}
	navigator := newNavigator(driver, observer, worker, store)
	state, err := navigator.Reach(context.Background(), "settings")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Signature.Same(settings.Signature) {
		t.Fatalf("landed on %q", state.Signature.Key())
	}
	if len(worker.requests) != 0 {
		t.Fatalf("model consulted %d times during replay", len(worker.requests))
	}
	if len(driver.tapped) != 1 {
		t.Fatalf("taps %+v", driver.tapped)
	}
}

func TestReachRunsToolLoopAndRecordsTheRecipe(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	settings := makeState("com.example.app", screen("Settings", button("Toggle", "toggle_button", "[0,0][50,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home, settings}}
	store := &fakeStore{entries: map[string]map[string]string{}}
	worker := &scriptedLLM{replies: []explore.Message{toolCall("1", "tap", `{"eidx":0}`)}}
	navigator := newNavigator(driver, observer, worker, store)
	state, err := navigator.Reach(context.Background(), "settings")
	if err != nil {
		t.Fatal(err)
	}
	if !state.Signature.Same(settings.Signature) {
		t.Fatalf("landed on %q", state.Signature.Key())
	}
	if len(store.recorded) != 1 || store.recorded[0].Title != "reach settings" {
		t.Fatalf("recorded %+v", store.recorded)
	}
	if !strings.Contains(store.recorded[0].Body, `tap {"eidx":0}`) {
		t.Fatalf("recipe body %q", store.recorded[0].Body)
	}
}

func TestReachReturnsTypedErrorWhenTheLoopRunsOut(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	replies := []explore.Message{}
	for turn := 0; turn < navigatorLoopBound; turn++ {
		replies = append(replies, textReply("thinking"))
	}
	worker := &scriptedLLM{replies: replies}
	navigator := newNavigator(driver, observer, worker, nil)
	state, err := navigator.Reach(context.Background(), "nowhere")
	if state != nil {
		t.Fatalf("state %+v on failure", state)
	}
	reachErr := &ReachError{}
	if !errors.As(err, &reachErr) || reachErr.Key != "nowhere" {
		t.Fatalf("err %v", err)
	}
}

func TestReachStopsOnCancellationMidLoop(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &scriptedLLM{
		replies: []explore.Message{toolCall("1", "tap", `{"eidx":0}`)},
		onCall:  func(int) { cancel() },
	}
	navigator := newNavigator(driver, observer, worker, nil)
	if _, err := navigator.Reach(ctx, "nowhere"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

// A session runs its scenarios back to back, so scenario N began wherever
// N-1 left the app before this change. That cost twice: the planner writes scenarios
// against the UI map of the START screen, and the exported flow records the
// actions without the screen they began on, so only the first scenario's
// flow could replay (measured on mmx36: 1 of 4 standalone).
//
// Killing first is what makes "prepares the app from any state" true. It
// resets navigation and not data: an app relaunched still holds whatever
// earlier scenarios created, which is what the later ones need.
func TestEnsureReadyStartsFromTheAppsOwnStartScreen(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	navigator := newNavigator(driver, observer, &scriptedLLM{}, nil)
	if _, err := navigator.EnsureReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(driver.calls, ",")
	if !strings.HasPrefix(joined, "KillApp:com.example.app,LaunchApp:com.example.app") {
		t.Fatalf("driver calls %v, want the app killed before it is launched", driver.calls)
	}
}

// An app that was not running has nothing to kill, and the platforms differ
// on whether that is an error. It never is one here: the point of the kill
// is the state after it, and the app is already in that state.
func TestEnsureReadyIgnoresAKillThatHadNothingToStop(t *testing.T) {
	home := makeState("com.example.app", screen("Home", button("Open", "open_button", "[0,0][100,50]")))
	driver := &fakeDriver{killErr: errors.New("no matching processes")}
	observer := &fakeObserver{states: []*explore.ScreenState{home}}
	navigator := newNavigator(driver, observer, &scriptedLLM{}, nil)
	state, err := navigator.EnsureReady(context.Background())
	if err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	if state == nil || !state.Signature.Same(home.Signature) {
		t.Fatalf("state %+v", state)
	}
}
