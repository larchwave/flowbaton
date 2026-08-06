package engine

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestVisibleHierarchyCancellationDuringDeviceInfoPreventsContentDescriptor(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	fake := lookupCancellationDriverScript()
	driver := &cancelDuringDeviceInfoDriver{FakeDriver: fake, cancel: cancel}
	lookup := NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))

	_, err := lookup.Find(ctx, model.ElementSelector{}, LookupOptions{Timeout: durationPointer(0)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Find() error = %v, want context.Canceled", err)
	}
	if got := methodCallCount(fake, enginetest.MethodDeviceInfo); got != 1 {
		t.Fatalf("DeviceInfo calls = %d, want 1", got)
	}
	if got := methodCallCount(fake, enginetest.MethodContentDescriptor); got != 0 {
		t.Fatalf("ContentDescriptor calls after cancellation = %d, want 0", got)
	}
}

func TestCachedDeviceInfoCancellationWhileWaitingForMutexPreventsDriverCall(t *testing.T) {
	baseCtx, cancel := context.WithCancel(context.Background())
	ctx := &observedErrContext{Context: baseCtx, secondCheck: make(chan struct{})}
	fake := lookupCancellationDriverScript()
	driver := &contextIgnoringDeviceInfoDriver{FakeDriver: fake}
	lookup := NewElementLookup(driver, enginetest.NewFakeClock(time.Unix(0, 0)))
	lookup.deviceInfoMu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			lookup.deviceInfoMu.Unlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		_, err := lookup.Find(ctx, model.ElementSelector{}, LookupOptions{Timeout: durationPointer(0)})
		done <- err
	}()
	select {
	case <-ctx.secondCheck:
	case <-time.After(2 * time.Second):
		t.Fatal("Find() did not reach the pre-mutex context check")
	}
	for range 100 {
		runtime.Gosched()
	}
	cancel()
	lookup.deviceInfoMu.Unlock()
	unlocked = true

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Find() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Find() did not finish")
	}
	if got := methodCallCount(fake, enginetest.MethodDeviceInfo); got != 0 {
		t.Fatalf("DeviceInfo calls after mutex-wait cancellation = %d, want 0", got)
	}
}

type cancelDuringDeviceInfoDriver struct {
	*enginetest.FakeDriver
	cancel context.CancelFunc
}

func (driver *cancelDuringDeviceInfoDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	info, err := driver.FakeDriver.DeviceInfo(context.Background())
	driver.cancel()
	return info, err
}

func (driver *cancelDuringDeviceInfoDriver) ContentDescriptor(_ context.Context, request device.ContentDescriptorRequest) (device.TreeNode, error) {
	return driver.FakeDriver.ContentDescriptor(context.Background(), request)
}

type contextIgnoringDeviceInfoDriver struct {
	*enginetest.FakeDriver
}

func (driver *contextIgnoringDeviceInfoDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	return driver.FakeDriver.DeviceInfo(context.Background())
}

type observedErrContext struct {
	context.Context
	checks      atomic.Int32
	secondCheck chan struct{}
}

func (ctx *observedErrContext) Err() error {
	err := ctx.Context.Err()
	if ctx.checks.Add(1) == 2 {
		close(ctx.secondCheck)
	}
	return err
}

func lookupCancellationDriverScript() *enginetest.FakeDriver {
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{WidthGrid: 100, HeightGrid: 100},
		}},
		ContentDescriptor: []enginetest.Result[device.TreeNode]{{
			Value: device.TreeNode{Attributes: map[string]string{"bounds": "[0,0][100,100]"}},
		}},
	})
	return driver
}

func methodCallCount(driver *enginetest.FakeDriver, method enginetest.Method) int {
	count := 0
	for _, action := range driver.Actions() {
		if action.Method == method {
			count++
		}
	}
	return count
}

func durationPointer(value time.Duration) *time.Duration { return &value }
