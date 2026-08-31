package engine

import (
	"context"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/model"
)

// rotationTree holds both labels at once, so a lookup finds one or the other
// purely by how wide the screen is said to be.
func rotationTree() device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][874,874]"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"text": "Portrait", "bounds": "[10,20][110,60]"}},
			{Attributes: map[string]string{"text": "Landscape", "bounds": "[600,100][800,160]"}},
		},
	}
}

func rotationDriver(results int) *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	trees := make([]enginetest.Result[device.TreeNode], results)
	for index := range trees {
		trees[index] = enginetest.Result[device.TreeNode]{Value: rotationTree()}
	}
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{
			{Value: device.DeviceInfo{WidthGrid: 402, HeightGrid: 874}},
			{Value: device.DeviceInfo{WidthGrid: 874, HeightGrid: 402}},
		},
		ContentDescriptor: trees,
	})
	return driver
}

// visibleHierarchy prunes with the device grid, and cachedDeviceInfo is filled
// once. Nothing dropped it, so after a rotation the rest of the flow kept
// measuring against the pre-rotation screen: an element plainly on a landscape
// screen fell outside the remembered portrait width and read as missing.
func TestALookupMeasuresTheScreenAgainAfterItIsForgotten(t *testing.T) {
	t.Parallel()
	lookup := NewElementLookup(rotationDriver(8), newAdvancingClock())

	portrait, landscape := "Portrait", "Landscape"
	found, err := lookup.Find(context.Background(),
		model.ElementSelector{TextRegex: &portrait}, LookupOptions{})
	if err != nil || found == nil {
		t.Fatalf("before rotation: Find(Portrait) = %v, %v; want the element", found, err)
	}

	lookup.ForgetDeviceInfo() // what setOrientation does

	found, err = lookup.Find(context.Background(),
		model.ElementSelector{TextRegex: &landscape}, LookupOptions{Optional: true})
	if err != nil {
		t.Fatalf("after rotation: Find(Landscape) error = %v", err)
	}
	if found == nil {
		t.Error("an element at x 600..800 on an 874-wide screen was pruned as off screen")
	}
}

// The wiring: the command itself must drop the remembered grid, or the
// mechanism above is one nobody calls.
func TestSetOrientationMakesTheNextLookupSeeTheRotatedScreen(t *testing.T) {
	t.Parallel()
	source := "appId: com.example.rotate\n---\n" +
		"- tapOn:\n    text: Portrait\n    waitToSettleTimeoutMs: 0\n" +
		"- setOrientation: LANDSCAPE_LEFT\n" +
		"- tapOn:\n    text: Landscape\n    waitToSettleTimeoutMs: 0\n"
	flowModel, err := flow.ParseBytes("/workspace/rotate.yaml", []byte(source))
	if err != nil {
		t.Fatalf("flow.ParseBytes() error = %v", err)
	}
	driver := rotationDriver(16)
	if _, err := Execute(context.Background(), singleCompileProgram(flowModel), Dependencies{
		ExecutionID: "rotate", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := tapRequests(driver.Actions()); len(got) != 2 {
		t.Fatalf("tap requests = %#v, want one before and one after the rotation", got)
	}
}
