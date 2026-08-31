package run

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

// scriptedDriverError answers the platform-neutral retryability question the
// way a runner error does.
type scriptedDriverError struct {
	message   string
	retryable bool
}

func (e *scriptedDriverError) Error() string   { return e.message }
func (e *scriptedDriverError) Retryable() bool { return e.retryable }

// The tester has no tool that brings an app back to the foreground, so a
// runner precondition is as final as a refused dial. Seen live 2026-08-31
// (session mmx47): the app left the foreground at step 6 and the model spent
// the remaining ten of fifteen steps on waits, taps and swipes that all failed
// with the same message.
func TestAnUnretryableObservationEndsTheLoop(t *testing.T) {
	t.Parallel()
	login := screen("Login", textField("user", false, false))
	precondition := &scriptedDriverError{
		message: "ios runner: precondition: none of com.example is in the foreground (HTTP 400)",
	}

	session, _ := inputSession(t, login)
	session.deps.observer = &fakeObserver{
		states: []*explore.ScreenState{makeState("app", login)},
		errs:   []error{fmt.Errorf("research: hierarchy: %w", precondition)},
	}
	_, err := session.afterMutation(context.Background(), "tap", nil, explore.Action{Kind: explore.ActionTap}, nil)
	if !errors.Is(err, explore.ErrScreenUnobservable) {
		t.Fatalf("afterMutation error = %v, want ErrScreenUnobservable", err)
	}

	session, _ = inputSession(t, login)
	session.deps.observer = &fakeObserver{
		states: []*explore.ScreenState{makeState("app", login)},
		errs:   []error{fmt.Errorf("research: hierarchy: %w", precondition)},
	}
	if _, err := session.handleObserve(context.Background(), nil); !errors.Is(err, explore.ErrScreenUnobservable) {
		t.Fatalf("observe error = %v, want ErrScreenUnobservable", err)
	}
}

// A failure the driver calls retryable still just marks the table stale: the
// next observation may well succeed, and ending the run would throw away a
// scenario over a transient.
func TestARetryableObservationFailureOnlyMarksTheTableStale(t *testing.T) {
	t.Parallel()
	login := screen("Login", textField("user", false, false))
	transient := &scriptedDriverError{message: "ios runner: internal: busy (HTTP 500)", retryable: true}

	session, _ := inputSession(t, login)
	session.deps.observer = &fakeObserver{
		states: []*explore.ScreenState{makeState("app", login)},
		errs:   []error{fmt.Errorf("research: hierarchy: %w", transient)},
	}
	if _, err := session.afterMutation(
		context.Background(), "tap", nil, explore.Action{Kind: explore.ActionTap}, nil,
	); err != nil {
		t.Fatalf("afterMutation error = %v, want the step to be recorded as failed", err)
	}
	if !session.stale {
		t.Errorf("session.stale = false, want the table marked stale")
	}
}
