package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
)

var (
	_ Clock = (*enginetest.FakeClock)(nil)
	_ Timer = (*enginetest.FakeTimer)(nil)
	_ Clock = RealClock{}
)

func TestSpec04TimingConstants(t *testing.T) {
	t.Parallel()

	durations := map[string]struct {
		got  time.Duration
		want time.Duration
	}{
		"animation":                    {AnimationTimeout, 15 * time.Second},
		"element stability":            {ElementStabilityTimeout, 3 * time.Second},
		"element stability poll":       {ElementStabilityPollInterval, 100 * time.Millisecond},
		"hierarchy settle poll":        {HierarchySettlePollInterval, 200 * time.Millisecond},
		"iOS screen settle":            {IOSScreenSettleTimeout, 3 * time.Second},
		"Android window update":        {AndroidWindowUpdateTimeout, 750 * time.Millisecond},
		"Android IME commit settle":    {AndroidIMECommitSettleDelay, 250 * time.Millisecond},
		"driver server launch":         {DriverServerLaunchTimeout, 15 * time.Second},
		"lookup":                       {LookupTimeout, 17 * time.Second},
		"optional lookup":              {OptionalLookupTimeout, 7 * time.Second},
		"not visible poll":             {NotVisiblePollInterval, 500 * time.Millisecond},
		"wait until visible poll":      {WaitUntilVisiblePollInterval, time.Second},
		"repeat delay":                 {RepeatDelay, 100 * time.Millisecond},
		"default swipe duration":       {DefaultSwipeDuration, 400 * time.Millisecond},
		"maximum settle timeout":       {MaximumSettleTimeout, 30 * time.Second},
		"Android reachability poll":    {AndroidReachabilityPollInterval, 100 * time.Millisecond},
		"iOS status reachability poll": {IOSStatusReachabilityPollInterval, 500 * time.Millisecond},
	}
	for name, test := range durations {
		if test.got != test.want {
			t.Errorf("%s = %v, want %v", name, test.got, test.want)
		}
	}
	if ScreenshotDiffThreshold != 0.005 {
		t.Errorf("ScreenshotDiffThreshold = %v, want 0.005", ScreenshotDiffThreshold)
	}
	counts := map[string]struct{ got, want int }{
		"hierarchy settle attempts": {HierarchySettleAttempts, 10},
		"wait visible attempts":     {WaitUntilVisibleAttempts, 10},
		"tap attempts":              {TapAttempts, 1},
		"tap retry attempts":        {TapAttemptsWithRetry, 2},
		"retry command maximum":     {RetryCommandMaxRetries, 3},
	}
	for name, test := range counts {
		if test.got != test.want {
			t.Errorf("%s = %d, want %d", name, test.got, test.want)
		}
	}
}

func TestRealClockWaitHonorsCancellationWithoutSleeping(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (RealClock{}).Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
	if err := (RealClock{}).Wait(context.Background(), 0); err != nil {
		t.Fatalf("zero Wait error: %v", err)
	}
	if (RealClock{}).Now().IsZero() {
		t.Fatal("RealClock.Now returned zero time")
	}
}
