package run

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// refusingDriver answers every gesture the way a dead runner's socket does
// and keeps answering observations, which is the shape mmx69 hit.
type refusingDriver struct {
	fakeDriver
}

func (d *refusingDriver) Tap(context.Context, device.TapRequest) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
}

// ErrDeviceUnreachable exists because "a model told 'tool failed' spends its
// whole budget on waits and retries against a dead endpoint (seen live,
// 2026-08-28)". It happened again in mmx69: eighteen calls to /touch and
// /swipeV2 answered "connection refused" and the run kept going to the end
// of its budget, because the check ran on the OBSERVATION error and the
// runner's hierarchy endpoint was still answering. The refusal is on the
// gesture, which is where it has to be read.
func TestARefusedGestureEndsTheRun(t *testing.T) {
	t.Parallel()

	state := makeState("app", screen("Home", button("Go", "go_button", "[0,0][100,50]")))
	session, err := newToolSession(toolDeps{
		driver:   &refusingDriver{},
		observer: &fakeObserver{states: []*explore.ScreenState{state, state, state}},
		appID:    "app",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	_, err = session.handleTap(context.Background(), json.RawMessage(`{"eidx":0}`))
	if !errors.Is(err, explore.ErrDeviceUnreachable) {
		t.Fatalf("err = %v, want it to wrap ErrDeviceUnreachable", err)
	}
}

// An ordinary gesture failure is still a failed step the model can work
// around: the element moved, the tap missed, the app refused it.
func TestAnOrdinaryGestureFailureIsStillJustAFailedStep(t *testing.T) {
	t.Parallel()

	state := makeState("app", screen("Home", button("Go", "go_button", "[0,0][100,50]")))
	session, err := newToolSession(toolDeps{
		driver:   &failingTapDriver{},
		observer: &fakeObserver{states: []*explore.ScreenState{state, state, state}},
		appID:    "app",
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.handleTap(context.Background(), json.RawMessage(`{"eidx":0}`)); err != nil {
		t.Fatalf("an ordinary failure ended the run: %v", err)
	}
	if len(session.steps) != 1 || session.steps[0].Status != explore.StepFailed {
		t.Fatalf("steps %+v", session.steps)
	}
}

type failingTapDriver struct {
	fakeDriver
}

func (d *failingTapDriver) Tap(context.Context, device.TapRequest) error {
	return errors.New("the element moved")
}
