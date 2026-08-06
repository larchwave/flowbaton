package engine_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

func TestWaitForHierarchySettleExplicitTimeoutRequiresTwoEqualNonNilHierarchies(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	ready := viewHierarchy("ready", false)
	driver := settleSequenceDriver(nil, ready, ready)
	lookup := engine.NewElementLookup(driver, clock)
	timeoutMillis := int64(600)
	done := make(chan struct {
		hierarchy *device.ViewHierarchy
		err       error
	}, 1)
	go func() {
		hierarchy, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: &timeoutMillis})
		done <- struct {
			hierarchy *device.ViewHierarchy
			err       error
		}{hierarchy: hierarchy, err: err}
	}()

	for range 3 {
		advancePendingTimer(t, clock, 200*time.Millisecond)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("WaitForHierarchySettle() error = %v", got.err)
		}
		if got.hierarchy == nil || got.hierarchy.Root.Attributes["state"] != "ready" {
			t.Fatalf("hierarchy = %#v, want ready", got.hierarchy)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForHierarchySettle() did not finish")
	}
	if got := countDriverMethod(driver, enginetest.MethodWaitForAppToSettle); got != 3 {
		t.Fatalf("WaitForAppToSettle calls = %d, want 3", got)
	}
}

func TestWaitForHierarchySettleDoesNotAcceptEqualLoadingHierarchies(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	loading := viewHierarchy("loading", true)
	ready := viewHierarchy("ready", false)
	driver := settleSequenceDriver(loading, loading, ready, ready)
	lookup := engine.NewElementLookup(driver, clock)
	timeoutMillis := int64(1000)
	done := make(chan error, 1)
	go func() {
		_, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: &timeoutMillis})
		done <- err
	}()

	for range 4 {
		advancePendingTimer(t, clock, 200*time.Millisecond)
	}
	if err := receiveError(t, done); err != nil {
		t.Fatalf("WaitForHierarchySettle() error = %v", err)
	}
	if got := countDriverMethod(driver, enginetest.MethodWaitForAppToSettle); got != 4 {
		t.Fatalf("WaitForAppToSettle calls = %d, want 4", got)
	}
}

func TestWaitForHierarchySettleUsesInitialHierarchyAsFirstSample(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	ready := viewHierarchy("ready", false)
	driver := settleSequenceDriver(ready)
	lookup := engine.NewElementLookup(driver, clock)
	timeoutMillis := int64(500)
	done := make(chan error, 1)
	go func() {
		_, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{
			InitialHierarchy: ready,
			TimeoutMillis:    &timeoutMillis,
		})
		done <- err
	}()

	advancePendingTimer(t, clock, 200*time.Millisecond)
	if err := receiveError(t, done); err != nil {
		t.Fatalf("WaitForHierarchySettle() error = %v", err)
	}
	if got := countDriverMethod(driver, enginetest.MethodWaitForAppToSettle); got != 1 {
		t.Fatalf("WaitForAppToSettle calls = %d, want 1", got)
	}
}

func TestWaitForHierarchySettleNilBreaksConsecutiveEquality(t *testing.T) {
	for _, test := range []struct {
		name          string
		timeoutMillis *int64
	}{
		{name: "explicit timeout", timeoutMillis: int64Ptr(1000)},
		{name: "default attempts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clock := enginetest.NewFakeClock(time.Unix(0, 0))
			ready := viewHierarchy("ready", false)
			driver := settleSequenceDriver(ready, nil, ready, ready)
			lookup := engine.NewElementLookup(driver, clock)
			done := make(chan error, 1)
			go func() {
				_, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: test.timeoutMillis})
				done <- err
			}()

			for range 3 {
				advancePendingTimer(t, clock, 200*time.Millisecond)
			}
			waitForPendingSettleTimerWithoutCompletion(t, clock, done)
			clock.Advance(200 * time.Millisecond)
			if err := receiveError(t, done); err != nil {
				t.Fatalf("WaitForHierarchySettle() error = %v", err)
			}
			if got := countDriverMethod(driver, enginetest.MethodWaitForAppToSettle); got != 4 {
				t.Fatalf("WaitForAppToSettle calls = %d, want 4", got)
			}
		})
	}
}

func TestWaitForHierarchySettleNilPreservesLatestHierarchyAtTimeout(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	ready := viewHierarchy("ready", false)
	driver := settleSequenceDriver(ready, nil)
	lookup := engine.NewElementLookup(driver, clock)
	timeoutMillis := int64(400)
	done := make(chan struct {
		hierarchy *device.ViewHierarchy
		err       error
	}, 1)
	go func() {
		hierarchy, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: &timeoutMillis})
		done <- struct {
			hierarchy *device.ViewHierarchy
			err       error
		}{hierarchy: hierarchy, err: err}
	}()

	for range 2 {
		advancePendingTimer(t, clock, 200*time.Millisecond)
	}
	select {
	case got := <-done:
		var operationErr *engine.OperationError
		if !errors.As(got.err, &operationErr) {
			t.Fatalf("error = %T %v, want *engine.OperationError", got.err, got.err)
		}
		if got.hierarchy == nil || got.hierarchy.Root.Attributes["state"] != "ready" {
			t.Fatalf("latest hierarchy = %#v, want ready", got.hierarchy)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForHierarchySettle() did not finish")
	}
}

func TestWaitForHierarchySettleWithoutTimeoutMakesExactlyTen200MillisecondPolls(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	samples := make([]*device.ViewHierarchy, engine.HierarchySettleAttempts)
	driver := settleSequenceDriver(samples...)
	lookup := engine.NewElementLookup(driver, clock)
	done := make(chan struct {
		hierarchy *device.ViewHierarchy
		err       error
	}, 1)
	go func() {
		hierarchy, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{})
		done <- struct {
			hierarchy *device.ViewHierarchy
			err       error
		}{hierarchy: hierarchy, err: err}
	}()

	for range engine.HierarchySettleAttempts {
		advancePendingTimer(t, clock, 200*time.Millisecond)
	}
	select {
	case got := <-done:
		if got.hierarchy != nil {
			t.Fatalf("hierarchy = %#v, want nil", got.hierarchy)
		}
		var operationErr *engine.OperationError
		if !errors.As(got.err, &operationErr) {
			t.Fatalf("error = %T %v, want *engine.OperationError", got.err, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForHierarchySettle() did not finish")
	}
	if got := clock.Now(); !got.Equal(time.Unix(2, 0)) {
		t.Fatalf("clock.Now() = %v, want 2s", got)
	}
	if got := countDriverMethod(driver, enginetest.MethodWaitForAppToSettle); got != engine.HierarchySettleAttempts {
		t.Fatalf("WaitForAppToSettle calls = %d, want %d", got, engine.HierarchySettleAttempts)
	}
}

func TestWaitForHierarchySettleCancellationBeforeCallMakesNoDriverCalls(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	lookup := engine.NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := lookup.WaitForHierarchySettle(ctx, device.SettleRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if actions := driver.Actions(); len(actions) != 0 {
		t.Fatalf("driver actions = %v, want none", actions)
	}
}

func TestWaitForHierarchySettleCancellationAfterWaitMakesNoDriverCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := &cancelAfterWaitClock{
		FakeClock: enginetest.NewFakeClock(time.Unix(0, 0)),
		cancel:    cancel,
	}
	fake := settleSequenceDriver(viewHierarchy("ready", false))
	driver := &contextIgnoringSettleDriver{FakeDriver: fake}
	lookup := engine.NewElementLookup(driver, clock)

	_, err := lookup.WaitForHierarchySettle(ctx, device.SettleRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if actions := fake.Actions(); len(actions) != 0 {
		t.Fatalf("driver actions after cancellation = %v, want none", actions)
	}
}

func TestWaitForHierarchySettleCancellationAfterContextIgnoringDriverErrorTakesPrecedence(t *testing.T) {
	t.Parallel()

	driverErrors := []struct {
		name string
		err  error
	}{
		{name: "ordinary", err: errors.New("ordinary settle failure")},
		{name: "joined", err: errors.Join(errors.New("settle failure"), engine.NewDeviceConnectionError("settle disconnected", nil))},
	}
	paths := []struct {
		name          string
		timeoutMillis int64
	}{
		{name: "zero", timeoutMillis: 0},
		{name: "polling", timeoutMillis: 400},
	}
	for _, path := range paths {
		for _, driverError := range driverErrors {
			t.Run(path.name+"/cancelled/"+driverError.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				fake := enginetest.NewFakeDriver()
				driver := &contextIgnoringErrorSettleDriver{
					FakeDriver: fake, cancel: cancel, err: driverError.err,
				}
				lookup := engine.NewElementLookup(driver, &immediateSettleClock{now: time.Unix(0, 0)})
				timeoutMillis := path.timeoutMillis
				_, err := lookup.WaitForHierarchySettle(ctx, device.SettleRequest{TimeoutMillis: &timeoutMillis})
				if err != context.Canceled {
					t.Fatalf("error = %T %v, want exact context.Canceled", err, err)
				}
				if got := countDriverMethod(fake, enginetest.MethodWaitForAppToSettle); got != 1 {
					t.Fatalf("WaitForAppToSettle calls = %d, want 1 and no later poll", got)
				}
			})

			t.Run(path.name+"/live/"+driverError.name, func(t *testing.T) {
				fake := enginetest.NewFakeDriver()
				driver := &contextIgnoringErrorSettleDriver{FakeDriver: fake, err: driverError.err}
				lookup := engine.NewElementLookup(driver, &immediateSettleClock{now: time.Unix(0, 0)})
				timeoutMillis := path.timeoutMillis
				_, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: &timeoutMillis})
				if err != driverError.err {
					t.Fatalf("error = %T %v, want exact driver error %p", err, err, driverError.err)
				}
				if got := countDriverMethod(fake, enginetest.MethodWaitForAppToSettle); got != 1 {
					t.Fatalf("WaitForAppToSettle calls = %d, want 1", got)
				}
			})
		}
	}
}

func TestWaitForHierarchySettleOwnsReusedDriverSamplesAndInitialHierarchy(t *testing.T) {
	t.Parallel()

	t.Run("reused driver pointer does not falsely settle or alias return", func(t *testing.T) {
		fake := enginetest.NewFakeDriver()
		driver := newReusedHierarchySettleDriver(fake, "one", "two", "two")
		lookup := engine.NewElementLookup(driver, &immediateSettleClock{now: time.Unix(0, 0)})
		timeoutMillis := int64(600)
		got, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{TimeoutMillis: &timeoutMillis})
		if err != nil {
			t.Fatalf("WaitForHierarchySettle() error = %v", err)
		}
		if calls := countDriverMethod(fake, enginetest.MethodWaitForAppToSettle); calls != 3 {
			t.Fatalf("WaitForAppToSettle calls = %d, want 3 after one->two->two", calls)
		}
		assertOwnedHierarchyState(t, got, "two")

		driver.mutate("after-return")
		assertOwnedHierarchyState(t, got, "two")
	})

	t.Run("initial hierarchy is cloned before observation", func(t *testing.T) {
		initial := ownedSettleHierarchy("initial")
		fake := enginetest.NewFakeDriver()
		lookup := engine.NewElementLookup(fake, &immediateSettleClock{now: time.Unix(0, 0)})
		timeoutMillis := int64(0)
		got, err := lookup.WaitForHierarchySettle(context.Background(), device.SettleRequest{
			InitialHierarchy: initial,
			TimeoutMillis:    &timeoutMillis,
		})
		var operationErr *engine.OperationError
		if !errors.As(err, &operationErr) {
			t.Fatalf("error = %T %v, want *engine.OperationError", err, err)
		}
		assertOwnedHierarchyState(t, got, "initial")

		mutateOwnedSettleHierarchy(initial, "caller-after-return")
		assertOwnedHierarchyState(t, got, "initial")
	})
}

func settleSequenceDriver(samples ...*device.ViewHierarchy) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	results := make([]enginetest.Result[*device.ViewHierarchy], len(samples))
	for index := range samples {
		results[index] = enginetest.Result[*device.ViewHierarchy]{Value: samples[index]}
	}
	driver.Enqueue(enginetest.DriverScript{WaitForAppToSettle: results})
	return driver
}

func viewHierarchy(state string, loading bool) *device.ViewHierarchy {
	attributes := map[string]string{"state": state}
	if loading {
		attributes["is-loading"] = "true"
	}
	return &device.ViewHierarchy{Root: device.TreeNode{Attributes: attributes}}
}

func waitForPendingSettleTimerWithoutCompletion(t *testing.T, clock *enginetest.FakeClock, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case err := <-done:
			t.Fatalf("settled before a fourth consecutive sample: %v", err)
		default:
		}
		if clock.PendingTimers() > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fourth settle poll")
		}
		runtime.Gosched()
	}
}

type cancelAfterWaitClock struct {
	*enginetest.FakeClock
	cancel context.CancelFunc
}

func (clock *cancelAfterWaitClock) Wait(context.Context, time.Duration) error {
	clock.cancel()
	return nil
}

type contextIgnoringSettleDriver struct {
	*enginetest.FakeDriver
}

func (driver *contextIgnoringSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	return driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
}

type contextIgnoringErrorSettleDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
	err    error
}

func (driver *contextIgnoringErrorSettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	value, _ := driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	if driver.cancel != nil {
		driver.cancel()
	}
	return value, driver.err
}

type immediateSettleClock struct {
	now time.Time
}

func (clock *immediateSettleClock) Now() time.Time { return clock.now }

func (clock *immediateSettleClock) Wait(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.now = clock.now.Add(delay)
	return nil
}

type reusedHierarchySettleDriver struct {
	*enginetest.FakeDriver
	shared *device.ViewHierarchy
	states []string
}

func newReusedHierarchySettleDriver(fake *enginetest.FakeDriver, states ...string) *reusedHierarchySettleDriver {
	return &reusedHierarchySettleDriver{
		FakeDriver: fake,
		shared:     ownedSettleHierarchy("unobserved"),
		states:     append([]string(nil), states...),
	}
}

func (driver *reusedHierarchySettleDriver) WaitForAppToSettle(_ context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	_, err := driver.FakeDriver.WaitForAppToSettle(context.Background(), request)
	if err != nil {
		return nil, err
	}
	if len(driver.states) == 0 {
		return driver.shared, nil
	}
	state := driver.states[0]
	driver.states = driver.states[1:]
	driver.mutate(state)
	return driver.shared, nil
}

func (driver *reusedHierarchySettleDriver) mutate(state string) {
	mutateOwnedSettleHierarchy(driver.shared, state)
}

func ownedSettleHierarchy(state string) *device.ViewHierarchy {
	enabled := false
	checked := false
	hierarchy := &device.ViewHierarchy{Root: device.TreeNode{
		Attributes: map[string]string{"state": ""},
		Enabled:    &enabled,
		Children: []device.TreeNode{{
			Attributes: map[string]string{"child-state": ""},
			Checked:    &checked,
		}},
	}}
	mutateOwnedSettleHierarchy(hierarchy, state)
	return hierarchy
}

func mutateOwnedSettleHierarchy(hierarchy *device.ViewHierarchy, state string) {
	hierarchy.Root.Attributes["state"] = state
	*hierarchy.Root.Enabled = state == "two"
	hierarchy.Root.Children[0].Attributes["child-state"] = state
	*hierarchy.Root.Children[0].Checked = state == "two"
}

func assertOwnedHierarchyState(t *testing.T, hierarchy *device.ViewHierarchy, state string) {
	t.Helper()
	if hierarchy == nil || hierarchy.Root.Attributes["state"] != state || len(hierarchy.Root.Children) != 1 ||
		hierarchy.Root.Children[0].Attributes["child-state"] != state || hierarchy.Root.Enabled == nil ||
		hierarchy.Root.Children[0].Checked == nil || *hierarchy.Root.Enabled != (state == "two") ||
		*hierarchy.Root.Children[0].Checked != (state == "two") {
		t.Fatalf("hierarchy = %#v, want deeply owned state %q", hierarchy, state)
	}
}

func int64Ptr(value int64) *int64 { return &value }
