// Package iosdevice owns the physical-iOS half of the driver surface:
// every import of go-ios lives here, so the usbmuxd/tunnel dependency has
// exactly one boundary package. The simulator half stays in internal/ios.
package iosdevice

import (
	"context"
	"fmt"
	"net"

	goios "github.com/danielpaulus/go-ios/ios"
)

// Device is one physical iOS device currently visible over usbmuxd.
type Device struct {
	UDID string
}

// enumerator is the closable slice of *goios.UsbMuxConnection this package
// uses: a blocking enumeration plus the Close that unblocks it.
type enumerator interface {
	ListDevices() (goios.DeviceList, error)
	Close() error
}

// dialUsbmuxd is the seam over the usbmuxd socket so tests and callers
// without hardware never open it. The dial itself honors the context:
// go-ios's own constructor dials without one, so this dials the same
// platform address with net.Dialer and hands the connection to go-ios.
var dialUsbmuxd = func(ctx context.Context) (enumerator, error) {
	network, address := goios.GetSocketTypeAndAddress(goios.GetUsbmuxdSocket())
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return goios.NewUsbMuxConnection(goios.NewDeviceConnectionWithConn(conn)), nil
}

// ListDevices returns the physical devices usbmuxd can currently reach. An
// unreachable usbmuxd is an error, not an empty list: callers that tolerate
// the tool's absence make that call themselves, mirroring android.ListDevices.
//
// The dial honors the context directly. go-ios exposes no cancellable
// enumeration for the read that follows, so cancellation closes the
// connection instead: the close unblocks the in-flight read, and the call
// waits for that goroutine, leaving no socket or goroutine behind.
func ListDevices(ctx context.Context) ([]Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := dialUsbmuxd(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect to usbmuxd: %w", err)
	}
	type answer struct {
		list goios.DeviceList
		err  error
	}
	answers := make(chan answer, 1)
	go func() {
		list, listErr := conn.ListDevices()
		answers <- answer{list: list, err: listErr}
	}()
	select {
	case <-ctx.Done():
		_ = conn.Close()
		<-answers
		return nil, ctx.Err()
	case got := <-answers:
		_ = conn.Close()
		if got.err != nil {
			return nil, fmt.Errorf("list physical iOS devices over usbmuxd: %w", got.err)
		}
		devices := make([]Device, 0, len(got.list.DeviceList))
		for _, entry := range got.list.DeviceList {
			devices = append(devices, Device{UDID: entry.Properties.SerialNumber})
		}
		return devices, nil
	}
}
