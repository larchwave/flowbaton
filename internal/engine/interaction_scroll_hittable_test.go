package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// hitTestingDriver is the fake driver with device.HitTester on top: each
// Hittable call pops the next scripted result and records its request.
type hitTestingDriver struct {
	*enginetest.FakeDriver
	results  []device.HittableResult
	requests []device.HittableRequest
}

func (driver *hitTestingDriver) Hittable(_ context.Context, request device.HittableRequest) (device.HittableResult, error) {
	driver.requests = append(driver.requests, request)
	if len(driver.results) == 0 {
		return device.HittableResult{}, errors.New("unscripted Hittable call")
	}
	result := driver.results[0]
	driver.results = driver.results[1:]
	return result, nil
}

func hittableTree(text string, bounds device.Bounds) device.TreeNode {
	tree := batch3Tree(text, bounds)
	tree.Children[0].Attributes["id"] = "field"
	tree.Children[0].Attributes["elementType"] = "49"
	return tree
}

// TestScrollUntilVisibleKeepsScrollingWhileTheTargetIsCovered pins issue #14:
// a target fully inside the viewport that the driver says is not hittable is
// not accepted; the loop steps on and accepts it once it is hittable.
func TestScrollUntilVisibleKeepsScrollingWhileTheTargetIsCovered(t *testing.T) {
	t.Parallel()
	driver := &hitTestingDriver{
		FakeDriver: batch3Driver(batch3Info(100, 100), []device.TreeNode{
			hittableTree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
			hittableTree("Ready", device.Bounds{Y: 55, Width: 100, Height: 20}),
		}, []error{nil}, nil),
		results: []device.HittableResult{{Decided: true, Hittable: false}, {Decided: true, Hittable: true}},
	}
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"visibilityPercentage": int64(100)}), nil, driver, newBatch3Clock(time.Unix(900, 0), true))
	if err != nil {
		t.Fatalf("covered target error = %v", err)
	}
	want := []device.ScrollVerticalRequest{{Direction: "DOWN", Amount: 0.25}}
	if got := batch3ScrollRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("scroll requests = %#v, want %#v", got, want)
	}
	if len(driver.requests) != 2 || driver.requests[0].AppID != "com.example.batch3" || driver.requests[0].Node.Attributes["id"] != "field" || driver.requests[0].Bounds != (device.Bounds{Y: 80, Width: 100, Height: 20}) {
		t.Fatalf("hit-test requests = %#v, want two for the observed field", driver.requests)
	}
}

// TestScrollUntilVisibleUndecidedHitTestLeavesGeometryInCharge: a driver that
// cannot say (no single match) does not hold the command back.
func TestScrollUntilVisibleUndecidedHitTestLeavesGeometryInCharge(t *testing.T) {
	t.Parallel()
	driver := &hitTestingDriver{
		FakeDriver: batch3Driver(batch3Info(100, 100), []device.TreeNode{
			hittableTree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
		}, nil, nil),
		results: []device.HittableResult{{Decided: false}},
	}
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", nil), nil, driver, newBatch3Clock(time.Unix(900, 0), true))
	if err != nil {
		t.Fatalf("undecided target error = %v", err)
	}
	if got := batch3ScrollRequests(driver.Actions()); len(got) != 0 {
		t.Fatalf("scroll requests = %#v, want none", got)
	}
}

// TestScrollUntilVisibleTimeoutNamesTheCover: the timeout says the target was
// covered, not merely late.
func TestScrollUntilVisibleTimeoutNamesTheCover(t *testing.T) {
	t.Parallel()
	driver := &hitTestingDriver{
		FakeDriver: batch3Driver(batch3Info(100, 100), []device.TreeNode{
			hittableTree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
			hittableTree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
			hittableTree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
		}, []error{nil, nil, nil}, nil),
		results: []device.HittableResult{{Decided: true}, {Decided: true}, {Decided: true}},
	}
	_, _, err := executeBatch3ForTest(context.Background(), batch3Command("Ready", map[string]any{"timeout": int64(150)}), nil, driver, newBatch3Clock(time.Unix(910, 0), true))
	var assertion *AssertionError
	if !errors.As(err, &assertion) || !strings.Contains(err.Error(), "covers it") {
		t.Fatalf("timeout under a cover: error = %T %v", err, err)
	}
}
