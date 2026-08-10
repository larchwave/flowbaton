package iosdevice

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	goios "github.com/danielpaulus/go-ios/ios"
)

// fakeConn simulates a usbmuxd connection: ListDevices returns the scripted
// answer, and Close unblocks a hanging ListDevices the way a socket close
// unblocks a read.
type fakeConn struct {
	list    goios.DeviceList
	err     error
	hang    bool
	started chan struct{} // closed when ListDevices is entered, if non-nil
	closed  chan struct{}
	closes  atomic.Int32
}

func newFakeConn() *fakeConn {
	return &fakeConn{closed: make(chan struct{})}
}

func (conn *fakeConn) ListDevices() (goios.DeviceList, error) {
	if conn.started != nil {
		close(conn.started)
	}
	if conn.hang {
		<-conn.closed
		return goios.DeviceList{}, errors.New("read on closed usbmuxd socket")
	}
	return conn.list, conn.err
}

func (conn *fakeConn) Close() error {
	if conn.closes.Add(1) == 1 {
		close(conn.closed)
	}
	return nil
}

func withFakeConn(t *testing.T, conn *fakeConn) {
	t.Helper()
	restore := dialUsbmuxd
	t.Cleanup(func() { dialUsbmuxd = restore })
	dialUsbmuxd = func(context.Context) (enumerator, error) { return conn, nil }
}

func TestListDevicesMapsEveryUsbmuxdEntry(t *testing.T) {
	conn := newFakeConn()
	conn.list = goios.DeviceList{DeviceList: []goios.DeviceEntry{
		{Properties: goios.DeviceProperties{SerialNumber: "00008110-000A1B2C3D4E5F60"}},
		{Properties: goios.DeviceProperties{SerialNumber: "00008030-001122334455667"}},
	}}
	withFakeConn(t, conn)

	devices, err := ListDevices(context.Background())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d: %#v", len(devices), devices)
	}
	if devices[0].UDID != "00008110-000A1B2C3D4E5F60" || devices[1].UDID != "00008030-001122334455667" {
		t.Fatalf("udid mapping lost order or value: %#v", devices)
	}
	if conn.closes.Load() == 0 {
		t.Fatal("connection must be closed after a successful enumeration")
	}
}

func TestListDevicesWrapsTransportErrors(t *testing.T) {
	conn := newFakeConn()
	conn.err = errors.New("could not read usbmuxd reply")
	withFakeConn(t, conn)

	_, err := ListDevices(context.Background())
	if !errors.Is(err, conn.err) {
		t.Fatalf("expected wrapped transport error, got %v", err)
	}
	if !strings.Contains(err.Error(), "usbmuxd") {
		t.Fatalf("error should name usbmuxd so the operator knows what to start: %v", err)
	}
	if conn.closes.Load() == 0 {
		t.Fatal("connection must be closed after a failed enumeration")
	}
}

func TestListDevicesWrapsDialErrors(t *testing.T) {
	restore := dialUsbmuxd
	t.Cleanup(func() { dialUsbmuxd = restore })
	dial := errors.New("could not connect to usbmuxd socket")
	dialUsbmuxd = func(context.Context) (enumerator, error) { return nil, dial }

	_, err := ListDevices(context.Background())
	if !errors.Is(err, dial) {
		t.Fatalf("expected wrapped dial error, got %v", err)
	}
	if !strings.Contains(err.Error(), "usbmuxd") {
		t.Fatalf("error should name usbmuxd so the operator knows what to start: %v", err)
	}
}

func TestListDevicesCancellationClosesSocketAndGoroutine(t *testing.T) {
	conn := newFakeConn()
	conn.hang = true
	conn.started = make(chan struct{})
	withFakeConn(t, conn)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ListDevices(ctx)
		done <- err
	}()
	<-conn.started // cancel only once the usbmuxd call is provably in flight
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListDevices did not return after cancellation while usbmuxd hung")
	}
	// ListDevices returning proves the reader goroutine finished: the
	// cancellation path drains the answer channel before returning, and the
	// fake can only answer after Close unblocked it.
	if conn.closes.Load() == 0 {
		t.Fatal("cancellation must close the usbmuxd connection")
	}
}

func TestListDevicesCancellationUnblocksHungDial(t *testing.T) {
	restore := dialUsbmuxd
	t.Cleanup(func() { dialUsbmuxd = restore })
	started := make(chan struct{})
	// Behave like net.Dialer.DialContext against an unresponsive endpoint:
	// block until the caller's context ends. A dial seam that drops the
	// context (e.g. passes context.Background()) hangs here forever.
	dialUsbmuxd = func(ctx context.Context) (enumerator, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ListDevices(ctx)
		done <- err
	}()
	<-started // cancel only once the dial is provably in flight
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled from a cancelled dial, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListDevices did not return after cancellation while the dial hung")
	}
}

// The two tests below execute the PRODUCTION dialUsbmuxd body (no seam
// replacement) against a real unix socket, via go-ios's own
// USBMUXD_SOCKET_ADDRESS override.

// shortSocketPath returns a socket path short enough for the ~104-byte
// sun_path limit; t.TempDir embeds the test name and exceeds it on macOS.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "fb-ux-*")
	if err != nil {
		t.Fatalf("make short temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "u.sock")
}

func TestProductionDialConnectsToConfiguredSocket(t *testing.T) {
	socket := shortSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on test socket: %v", err)
	}
	defer listener.Close()
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "unix://"+socket)

	conn, err := dialUsbmuxd(context.Background())
	if err != nil {
		t.Fatalf("production dial against a live socket: %v", err)
	}
	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close dialed connection: %v", closeErr)
	}
}

func TestProductionDialHonorsCancelledContext(t *testing.T) {
	socket := shortSocketPath(t)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on test socket: %v", err)
	}
	defer listener.Close()
	t.Setenv("USBMUXD_SOCKET_ADDRESS", "unix://"+socket)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A dial body that drops the context (net.Dial, context.Background())
	// would connect to the live listener and return nil error — failing here.
	if _, err := dialUsbmuxd(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled from the production dial, got %v", err)
	}
}

func TestListDevicesStopsOnCancelledContext(t *testing.T) {
	dialed := false
	restore := dialUsbmuxd
	t.Cleanup(func() { dialUsbmuxd = restore })
	dialUsbmuxd = func(context.Context) (enumerator, error) {
		dialed = true
		return newFakeConn(), nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ListDevices(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if dialed {
		t.Fatal("cancelled context must not reach usbmuxd")
	}
}
