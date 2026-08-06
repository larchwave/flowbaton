package engine

import (
	"context"
	"time"
)

// Clock is the minimal time source required by deterministic engine logic.
// enginetest.FakeClock and RealClock both satisfy it structurally.
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}

// Timer is the independently waitable timer contract implemented by
// enginetest.FakeTimer.
type Timer interface {
	Deadline() time.Time
	Done() <-chan struct{}
	Wait(context.Context) error
	Stop() bool
}

// RealClock is the production wall-clock implementation.
type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

func (RealClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const (
	ScreenshotDiffThreshold = 0.005

	AnimationTimeout                  = 15 * time.Second
	ElementStabilityTimeout           = 3 * time.Second
	ElementStabilityPollInterval      = 100 * time.Millisecond
	HierarchySettlePollInterval       = 200 * time.Millisecond
	IOSScreenSettleTimeout            = 3 * time.Second
	AndroidWindowUpdateTimeout        = 750 * time.Millisecond
	AndroidIMECommitSettleDelay       = 250 * time.Millisecond
	DriverServerLaunchTimeout         = 15 * time.Second
	LookupTimeout                     = 17 * time.Second
	OptionalLookupTimeout             = 7 * time.Second
	NotVisiblePollInterval            = 500 * time.Millisecond
	WaitUntilVisiblePollInterval      = time.Second
	RepeatDelay                       = 100 * time.Millisecond
	DefaultSwipeDuration              = 400 * time.Millisecond
	MaximumSettleTimeout              = 30 * time.Second
	AndroidReachabilityPollInterval   = 100 * time.Millisecond
	IOSStatusReachabilityPollInterval = 500 * time.Millisecond

	HierarchySettleAttempts  = 10
	WaitUntilVisibleAttempts = 10
	TapAttempts              = 1
	TapAttemptsWithRetry     = 2
	RetryCommandMaxRetries   = 3
)
