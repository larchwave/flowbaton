package enginetest

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestFakeTimerFiresOnlyWhenClockReachesDeadline(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)
	timer := clock.NewTimer(5 * time.Second)

	if got := clock.Now(); !got.Equal(start) {
		t.Fatalf("initial Now = %v, want %v", got, start)
	}
	if got := timer.Deadline(); !got.Equal(start.Add(5 * time.Second)) {
		t.Fatalf("timer deadline = %v, want %v", got, start.Add(5*time.Second))
	}
	if got := clock.PendingTimers(); got != 1 {
		t.Fatalf("pending timers = %d, want 1", got)
	}

	clock.Advance(4 * time.Second)
	select {
	case <-timer.Done():
		t.Fatal("timer fired before its deadline")
	default:
	}
	if got := clock.PendingTimers(); got != 1 {
		t.Fatalf("pending timers before deadline = %d, want 1", got)
	}

	if got := clock.Advance(time.Second); !got.Equal(start.Add(5 * time.Second)) {
		t.Fatalf("Now after advance = %v, want %v", got, start.Add(5*time.Second))
	}
	select {
	case <-timer.Done():
	default:
		t.Fatal("timer did not fire at its deadline")
	}
	if err := timer.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after firing error: %v", err)
	}
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("pending timers after deadline = %d, want 0", got)
	}
}

func TestFakeClockWaitCompletesOnAdvanceAndUnregistersOnCancellation(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Unix(100, 0))
	completed := make(chan error, 1)
	go func() {
		completed <- clock.Wait(context.Background(), 3*time.Second)
	}()
	waitForPendingTimers(t, clock, 1)
	clock.Advance(3 * time.Second)
	if err := receiveScheduled(t, completed); err != nil {
		t.Fatalf("Wait after advance error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)
	go func() {
		cancelled <- clock.Wait(ctx, 5*time.Second)
	}()
	waitForPendingTimers(t, clock, 1)
	cancel()
	if err := receiveScheduled(t, cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Wait error = %v, want context.Canceled", err)
	}
	waitForPendingTimers(t, clock, 0)
}

func TestFakeTimerStopUnblocksWaitWithStableError(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Unix(200, 0))
	timer := clock.NewTimer(time.Hour)
	if !timer.Stop() {
		t.Fatal("first Stop = false, want true")
	}
	if timer.Stop() {
		t.Fatal("second Stop = true, want false")
	}
	if err := timer.Wait(context.Background()); !errors.Is(err, ErrFakeTimerStopped) {
		t.Fatalf("Wait after Stop error = %v, want ErrFakeTimerStopped", err)
	}
	if got := clock.PendingTimers(); got != 0 {
		t.Fatalf("pending timers after Stop = %d, want 0", got)
	}
}

func TestFakeClockRejectsBackwardAdvance(t *testing.T) {
	t.Parallel()

	clock := NewFakeClock(time.Unix(300, 0))
	defer func() {
		if recover() == nil {
			t.Fatal("Advance with a negative duration did not panic")
		}
	}()
	clock.Advance(-time.Nanosecond)
}

func waitForPendingTimers(t *testing.T, clock *FakeClock, want int) {
	t.Helper()
	for range 100_000 {
		if clock.PendingTimers() == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("pending timers = %d, want %d", clock.PendingTimers(), want)
}

func receiveScheduled(t *testing.T, result <-chan error) error {
	t.Helper()
	for range 100_000 {
		select {
		case err := <-result:
			return err
		default:
			runtime.Gosched()
		}
	}
	t.Fatal("goroutine did not complete after fake-time or context signal")
	return nil
}
