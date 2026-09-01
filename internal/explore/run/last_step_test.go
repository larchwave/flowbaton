package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/ios"
)

// losingObserver answers the way the iOS runner does once the app under test
// is no longer in front.
type losingObserver struct {
	states []*explore.ScreenState
	calls  int
}

func (o *losingObserver) Observe(context.Context) (*explore.ScreenState, error) {
	o.calls++
	if o.calls <= len(o.states) {
		return o.states[o.calls-1], nil
	}
	return nil, fmt.Errorf("research: hierarchy: %w", &ios.Error{
		Code:    ios.CodePrecondition,
		Message: "none of com.apple.mobilecal is in the foreground",
		Status:  400,
	})
}

// mmx71 lost the app to a tap twice and the step log names neither: the run
// ends on the observation that follows, and that path returned before the
// step was recorded. The report reads "tester turn 22" with sixteen steps
// listed, so the action that did it is the one thing missing.
//
// Whichever way the run ends, the action that ended it is the evidence
// somebody came for.
func TestTheStepThatLostTheScreenIsStillRecorded(t *testing.T) {
	t.Parallel()

	state := makeState("app", screen("Home", button("Add", "add-plus-button", "[0,0][100,50]")))
	session, err := newToolSession(toolDeps{
		driver:   &fakeDriver{},
		observer: &losingObserver{},
		appID:    "app",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.handleTap(context.Background(), json.RawMessage(`{"id":"add-plus-button"}`))
	if !errors.Is(err, explore.ErrScreenUnobservable) {
		t.Fatalf("err = %v, want ErrScreenUnobservable", err)
	}
	if len(session.steps) != 1 {
		t.Fatalf("steps = %+v, want the tap that lost the screen", session.steps)
	}
	if got := session.steps[0].Action.Target; got == nil || got.Value != "add-plus-button" {
		t.Errorf("recorded target %+v, want the element that was tapped", got)
	}
	if session.steps[0].Status != explore.StepFailed || session.steps[0].ErrText == "" {
		t.Errorf("step = %+v, want a failed step carrying the reason", session.steps[0])
	}
}
