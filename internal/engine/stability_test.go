package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
)

func TestWaitForElementStabilityRefreshesByAttributesUntilBoundsRepeat(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	driver := hierarchySequenceDriver(stabilityNode(20), stabilityNode(20))
	lookup := engine.NewElementLookup(driver, clock)
	previous := mustElement(t, stabilityNode(10).Children[0])
	done := make(chan struct {
		result engine.ElementStabilityResult
		err    error
	}, 1)
	go func() {
		result, err := lookup.WaitForElementStability(context.Background(), previous)
		done <- struct {
			result engine.ElementStabilityResult
			err    error
		}{result: result, err: err}
	}()

	advancePendingTimer(t, clock, 100*time.Millisecond)
	advancePendingTimer(t, clock, 100*time.Millisecond)
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("WaitForElementStability() error = %v", got.err)
		}
		if !got.result.Stable {
			t.Fatal("WaitForElementStability() Stable = false, want true")
		}
		want := device.Bounds{X: 20, Y: 10, Width: 20, Height: 20}
		if got.result.Bounds != want {
			t.Fatalf("result bounds = %+v, want %+v", got.result.Bounds, want)
		}
		if got.result.Element == nil || got.result.Element.Node.Attributes["id"] != "target" {
			t.Fatalf("result element = %#v, want refreshed target", got.result.Element)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForElementStability() did not finish")
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 2 {
		t.Fatalf("ContentDescriptor calls = %d, want 2", got)
	}
}

func TestWaitForElementStabilityReturnsLatestBoundsAtThreeSecondDeadline(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	nodes := make([]device.TreeNode, 30)
	for index := range nodes {
		nodes[index] = stabilityNode(index + 1)
	}
	driver := hierarchySequenceDriver(nodes...)
	lookup := engine.NewElementLookup(driver, clock)
	previous := mustElement(t, stabilityNode(0).Children[0])
	done := make(chan struct {
		result engine.ElementStabilityResult
		err    error
	}, 1)
	go func() {
		result, err := lookup.WaitForElementStability(context.Background(), previous)
		done <- struct {
			result engine.ElementStabilityResult
			err    error
		}{result: result, err: err}
	}()

	for range 30 {
		advancePendingTimer(t, clock, 100*time.Millisecond)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("WaitForElementStability() error = %v", got.err)
		}
		if got.result.Stable {
			t.Fatal("WaitForElementStability() Stable = true, want false")
		}
		want := device.Bounds{X: 30, Y: 10, Width: 20, Height: 20}
		if got.result.Bounds != want {
			t.Fatalf("result bounds = %+v, want latest %+v", got.result.Bounds, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForElementStability() did not finish")
	}
	if got := countDriverMethod(driver, enginetest.MethodContentDescriptor); got != 30 {
		t.Fatalf("ContentDescriptor calls = %d, want 30", got)
	}
}

func TestWaitForElementStabilityReturnsLastKnownBoundsWhenRefreshDisappears(t *testing.T) {
	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	nodes := make([]device.TreeNode, 30)
	for index := range nodes {
		nodes[index] = emptyNode()
	}
	driver := hierarchySequenceDriver(nodes...)
	lookup := engine.NewElementLookup(driver, clock)
	previous := mustElement(t, stabilityNode(7).Children[0])
	done := make(chan engine.ElementStabilityResult, 1)
	errDone := make(chan error, 1)
	go func() {
		result, err := lookup.WaitForElementStability(context.Background(), previous)
		done <- result
		errDone <- err
	}()

	for range 30 {
		advancePendingTimer(t, clock, 100*time.Millisecond)
	}
	result := <-done
	if err := <-errDone; err != nil {
		t.Fatalf("WaitForElementStability() error = %v", err)
	}
	want := device.Bounds{X: 7, Y: 10, Width: 20, Height: 20}
	if result.Bounds != want {
		t.Fatalf("result bounds = %+v, want last-known %+v", result.Bounds, want)
	}
}

func TestWaitForElementStabilityRejectsElementWithoutBounds(t *testing.T) {
	lookup := engine.NewElementLookup(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0)))
	result, err := lookup.WaitForElementStability(context.Background(), &hierarchy.Element{})
	if result.Element != nil {
		t.Fatalf("result = %#v, want zero", result)
	}
	var configurationErr *engine.ConfigurationError
	if !errors.As(err, &configurationErr) {
		t.Fatalf("error = %T %v, want *engine.ConfigurationError", err, err)
	}
}

func stabilityNode(x int) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][100,100]"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{
				"id":     "target",
				"bounds": hierarchy.FormatBounds(device.Bounds{X: x, Y: 10, Width: 20, Height: 20}),
			},
		}},
	}
}

func mustElement(t *testing.T, node device.TreeNode) *hierarchy.Element {
	t.Helper()
	element, err := hierarchy.New(node)
	if err != nil {
		t.Fatalf("hierarchy.New() error = %v", err)
	}
	return element
}
