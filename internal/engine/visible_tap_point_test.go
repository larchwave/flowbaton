package engine

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// A list row scrolled past the bottom edge stays matchable -- visibleHierarchy
// keeps anything 10% visible -- so tapOn resolves it and then aims at its
// geometric center, which is off the device. Measured on a live capture of
// Apple's Reminders: a 402x874 screen, a row at [16,847][386,909], center
// y=878. The row here is the same shape against this suite's 400x884 grid.
func TestTapOnAimsInsideTheScreenForARowPastTheBottomEdge(t *testing.T) {
	t.Parallel()
	row := device.Bounds{X: 16, Y: 857, Width: 370, Height: 62}
	tree := tapTree(row)
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}, {Value: tree}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\nwaitToSettleTimeoutMs: 0", nil)
	if _, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-visible-center", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	// The visible strip is y 857..884, so the tap belongs at its middle.
	want := []device.TapRequest{{Point: device.Point{X: 201, Y: 870.5}, AppID: "com.example.tap-batch2"}}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap points = %#v, want %#v", got, want)
	}
}

// The same path must not move a tap that already lands on screen.
func TestTapOnKeepsTheGeometricCenterForAFullyVisibleElement(t *testing.T) {
	t.Parallel()
	tree := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}, {Value: tree}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\nwaitToSettleTimeoutMs: 0", nil)
	if _, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-visible-center-noop", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []device.TapRequest{{Point: device.Point{X: 20, Y: 30}, AppID: "com.example.tap-batch2"}}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap points = %#v, want %#v", got, want)
	}
}

// An authored point is measured against the element, never the screen, so
// `point: 50%,50%` on a row hanging off the bottom edge resolves to a
// coordinate the device does not have. Aiming elsewhere would change what the
// author's percentage means, so the flow says so instead of tapping nothing.
func TestTapOnRefusesAnAuthoredPointThatLandsOffTheScreen(t *testing.T) {
	t.Parallel()
	tree := tapTree(device.Bounds{X: 16, Y: 857, Width: 370, Height: 62})
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}, {Value: tree}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\npoint: '50%,50%'\nwaitToSettleTimeoutMs: 0", nil)
	_, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-relative-offscreen", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	})
	if err == nil {
		t.Fatalf("Execute() succeeded; taps = %#v, want a refusal", tapRequests(driver.Actions()))
	}
	if got := tapRequests(driver.Actions()); len(got) != 0 {
		t.Errorf("tapped %#v before refusing", got)
	}
	if !strings.Contains(err.Error(), "off the screen") {
		t.Errorf("error = %v, want it to name the off-screen coordinate", err)
	}
}

// The same authored point on a fully visible element still resolves.
func TestTapOnKeepsAnAuthoredPointOnAVisibleElement(t *testing.T) {
	t.Parallel()
	tree := tapTree(device.Bounds{X: 10, Y: 20, Width: 20, Height: 20})
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 400, HeightGrid: 884}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}, {Value: tree}},
	})
	flowModel := parsedTapFlow(t, "text: Continue\npoint: '50%,50%'\nwaitToSettleTimeoutMs: 0", nil)
	if _, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-relative-visible", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []device.TapRequest{{Point: device.Point{X: 20, Y: 30}, AppID: "com.example.tap-batch2"}}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap points = %#v, want %#v", got, want)
	}
}

// tapTargetingDriver is the fake driver with device.TapTargeter on top, as
// the iOS driver answers it: a switch aims at the switch nested inside it.
type tapTargetingDriver struct {
	*enginetest.FakeDriver
}

func (tapTargetingDriver) TapsInside(element, descendant device.TreeNode) bool {
	return element.Attributes["elementType"] == "40" && descendant.Attributes["elementType"] == "40"
}

// Issue #40: a SwiftUI Toggle's accessibility element spans its whole Form
// row, and only the switch at the row's trailing edge turns it. Measured on
// iOS 26.2: the row [16,168][386,220] holds an unnamed switch
// [309,180][372,208]. A centre tap on the row lands on the label and changes
// nothing, so the tap aims at the switch the driver names instead.
func TestTapOnAimsAtTheControlTheDriverNamesInsideTheElement(t *testing.T) {
	t.Parallel()
	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{{
			Attributes: map[string]string{
				"id": "addPlant.knowsAcquiredAt", "elementType": "40", "bounds": "[16,168][386,220]",
			},
			Children: []device.TreeNode{{Attributes: map[string]string{
				"elementType": "40", "bounds": "[309,180][372,208]",
			}}},
		}},
	}
	driver := tapTargetingDriver{FakeDriver: enginetest.NewFakeDriver()}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo:        []enginetest.Result[device.DeviceInfo]{{Value: device.DeviceInfo{WidthGrid: 402, HeightGrid: 874}}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{Value: tree}, {Value: tree}, {Value: tree}},
	})
	flowModel := parsedTapFlow(t, "id: addPlant.knowsAcquiredAt\nwaitToSettleTimeoutMs: 0", nil)
	if _, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "tap-inner-control", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	want := []device.TapRequest{{Point: device.Point{X: 340.5, Y: 194}, AppID: "com.example.tap-batch2"}}
	if got := tapRequests(driver.Actions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap points = %#v, want %#v", got, want)
	}
}
