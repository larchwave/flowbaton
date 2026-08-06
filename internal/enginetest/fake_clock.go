package enginetest

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrFakeTimerStopped is returned when waiting on an explicitly stopped timer.
var ErrFakeTimerStopped = errors.New("fake timer stopped")

// FakeClock is a manually advanced, concurrency-safe clock for deterministic
// engine tests.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	nextID uint64
	timers map[uint64]*FakeTimer
}

// FakeTimer is one deadline registered with a FakeClock.
type FakeTimer struct {
	clock    *FakeClock
	id       uint64
	deadline time.Time
	done     chan struct{}
	state    fakeTimerState
}

type fakeTimerState uint8

const (
	timerPending fakeTimerState = iota
	timerFired
	timerStopped
)

// NewFakeClock starts a clock at the supplied instant.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, timers: make(map[uint64]*FakeTimer)}
}

// Now returns the current fake instant.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// NewTimer registers a timer relative to the current fake instant.
func (c *FakeClock) NewTimer(delay time.Duration) *FakeTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timers == nil {
		c.timers = make(map[uint64]*FakeTimer)
	}
	c.nextID++
	timer := &FakeTimer{
		clock:    c,
		id:       c.nextID,
		deadline: c.now.Add(delay),
		done:     make(chan struct{}),
	}
	if delay <= 0 {
		timer.state = timerFired
		close(timer.done)
		return timer
	}
	c.timers[timer.id] = timer
	return timer
}

// Advance moves time forward and fires every timer at or before the new time.
func (c *FakeClock) Advance(delta time.Duration) time.Time {
	if delta < 0 {
		panic("enginetest: FakeClock cannot advance backwards")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(delta)
	for id, timer := range c.timers {
		if timer.deadline.After(c.now) {
			continue
		}
		delete(c.timers, id)
		timer.state = timerFired
		close(timer.done)
	}
	return c.now
}

// Wait blocks without wall-clock sleeping until delay elapses in fake time or
// the context is cancelled. A cancelled wait unregisters its timer.
func (c *FakeClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	timer := c.NewTimer(delay)
	err := timer.Wait(ctx)
	if err != nil {
		timer.stop()
	}
	return err
}

// PendingTimers returns the number of registered deadlines that have not
// fired.
func (c *FakeClock) PendingTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}

// Deadline returns the immutable fake deadline.
func (t *FakeTimer) Deadline() time.Time {
	return t.deadline
}

// Done closes when the fake clock reaches the timer deadline.
func (t *FakeTimer) Done() <-chan struct{} {
	return t.done
}

// Wait blocks without wall-clock sleeping until the timer fires or the context
// is cancelled.
func (t *FakeTimer) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-t.done:
		if t.state == timerStopped {
			return ErrFakeTimerStopped
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop prevents a pending timer from firing and unblocks its waiters.
func (t *FakeTimer) Stop() bool {
	return t.stop()
}

func (t *FakeTimer) stop() bool {
	c := t.clock
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.state != timerPending {
		return false
	}
	delete(c.timers, t.id)
	t.state = timerStopped
	close(t.done)
	return true
}
