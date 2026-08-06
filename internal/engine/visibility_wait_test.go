package engine_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestWaitUntilNotVisiblePollsEvery500MillisecondsUntilAbsent(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(visibleNode("Loading"), visibleNode("Loading"), emptyNode())
	lookup := engine.NewElementLookup(driver, clock)
	text := "Loading"
	done := make(chan error, 1)
	go func() {
		done <- lookup.WaitUntilNotVisible(context.Background(), model.ElementSelector{TextRegex: &text}, 5*time.Second)
	}()

	advancePendingTimer(t, clock, 500*time.Millisecond)
	advancePendingTimer(t, clock, 500*time.Millisecond)
	if err := receiveError(t, done); err != nil {
		t.Fatalf("WaitUntilNotVisible() error = %v", err)
	}
	if got := clock.Now(); !got.Equal(time.Unix(1, 0)) {
		t.Fatalf("clock.Now() = %v, want 1s", got)
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 3 {
		t.Fatalf("ContentDescriptor calls = %d, want 3", got)
	}
}

func TestWaitUntilNotVisibleChecksAtDeadlineThenReturnsOperationError(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(visibleNode("Loading"), visibleNode("Loading"), visibleNode("Loading"))
	lookup := engine.NewElementLookup(driver, clock)
	text := "Loading"
	done := make(chan error, 1)
	go func() {
		done <- lookup.WaitUntilNotVisible(context.Background(), model.ElementSelector{TextRegex: &text}, time.Second)
	}()

	advancePendingTimer(t, clock, 500*time.Millisecond)
	advancePendingTimer(t, clock, 500*time.Millisecond)
	err := receiveError(t, done)
	var operationErr *engine.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("WaitUntilNotVisible() error = %T %v, want *engine.OperationError", err, err)
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 3 {
		t.Fatalf("ContentDescriptor calls = %d, want 3", got)
	}
}

func TestWaitUntilNotVisibleCancellationPreventsLaterCalls(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(visibleNode("Loading"), emptyNode())
	lookup := engine.NewElementLookup(driver, clock)
	text := "Loading"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- lookup.WaitUntilNotVisible(ctx, model.ElementSelector{TextRegex: &text}, 5*time.Second)
	}()

	waitForPendingTimer(t, clock)
	cancel()
	if err := receiveError(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitUntilNotVisible() error = %v, want context.Canceled", err)
	}
	clock.Advance(5 * time.Second)
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 1 {
		t.Fatalf("ContentDescriptor calls after cancellation = %d, want 1", got)
	}
}

func TestWaitUntilVisibleWaitsOneSecondPerAttemptAndStopsOnMatch(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(emptyNode(), emptyNode(), visibleNode("Ready"))
	lookup := engine.NewElementLookup(driver, clock)
	text := "Ready"
	done := make(chan struct {
		elementBounds device.Bounds
		err           error
	}, 1)
	go func() {
		element, err := lookup.WaitUntilVisible(context.Background(), model.ElementSelector{TextRegex: &text}, false)
		result := struct {
			elementBounds device.Bounds
			err           error
		}{err: err}
		if element != nil {
			result.elementBounds = element.Bounds
		}
		done <- result
	}()

	for range 3 {
		advancePendingTimer(t, clock, time.Second)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("WaitUntilVisible() error = %v", result.err)
		}
		want := device.Bounds{X: 10, Y: 10, Width: 20, Height: 20}
		if result.elementBounds != want {
			t.Fatalf("element bounds = %+v, want %+v", result.elementBounds, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitUntilVisible() did not finish")
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 3 {
		t.Fatalf("ContentDescriptor calls = %d, want 3", got)
	}
}

func TestWaitUntilVisibleMakesExactlyTenOneSecondAttempts(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	nodes := make([]device.TreeNode, engine.WaitUntilVisibleAttempts)
	for index := range nodes {
		nodes[index] = emptyNode()
	}
	driver := hierarchySequenceDriver(nodes...)
	lookup := engine.NewElementLookup(driver, clock)
	text := "Ready"
	done := make(chan error, 1)
	go func() {
		_, err := lookup.WaitUntilVisible(context.Background(), model.ElementSelector{TextRegex: &text}, false)
		done <- err
	}()

	for range engine.WaitUntilVisibleAttempts {
		advancePendingTimer(t, clock, time.Second)
	}
	err := receiveError(t, done)
	var operationErr *engine.OperationError
	if !errors.As(err, &operationErr) {
		t.Fatalf("WaitUntilVisible() error = %T %v, want *engine.OperationError", err, err)
	}
	if got := clock.Now(); !got.Equal(time.Unix(int64(engine.WaitUntilVisibleAttempts), 0)) {
		t.Fatalf("clock.Now() = %v, want 10s", got)
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != engine.WaitUntilVisibleAttempts {
		t.Fatalf("ContentDescriptor calls = %d, want %d", got, engine.WaitUntilVisibleAttempts)
	}
}

func hierarchySequenceDriver(nodes ...device.TreeNode) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	descriptors := make([]enginetest.Result[device.TreeNode], len(nodes))
	for index := range nodes {
		descriptors[index] = enginetest.Result[device.TreeNode]{Value: nodes[index]}
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100}}},
		ContentDescriptor: descriptors,
	})
	return driver
}

func visibleNode(text string) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][100,100]"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{"text": text, "bounds": "[10,10][30,30]"},
		}},
	}
}

func emptyNode() device.TreeNode {
	return device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][100,100]"}}
}

func countDriverMethod(driver *enginetest.FakeDriver, method enginetest.Method) int {
	count := 0
	for _, action := range driver.Actions() {
		if action.Method == method {
			count++
		}
	}
	return count
}

func advancePendingTimer(t *testing.T, clock *enginetest.FakeClock, duration time.Duration) {
	t.Helper()
	waitForPendingTimer(t, clock)
	clock.Advance(duration)
}

func waitForPendingTimer(t *testing.T, clock *enginetest.FakeClock) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for clock.PendingTimers() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fake timer")
		}
		runtime.Gosched()
	}
}

func receiveError(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for operation")
		return nil
	}
}
