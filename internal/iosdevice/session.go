package iosdevice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	goios "github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/forward"
	"github.com/danielpaulus/go-ios/ios/tunnel"
)

// runnerDevicePort is where the runner's loopback HTTP server listens ON THE
// DEVICE (the frozen wire's default port). The shard-assigned port is the
// HOST end of the forward; the two are equal only on a simulator.
const runnerDevicePort = 22087

// tunnelMajorFloor is the first iOS major version that requires a
// remotexpc tunnel before instruments/testmanagerd services answer.
const tunnelMajorFloor = 17

// session prepares one physical device for driving and undoes it: resolve
// over usbmuxd, verify pairing, start the in-process userspace tunnel on
// iOS 17+, and forward the host port to the runner's device port.
type session struct {
	udid     string
	hostPort int

	entry           goios.DeviceEntry
	iosVersionMajor int64

	tunnel  io.Closer
	forward io.Closer

	// Seams over go-ios; nil means talk to the real system.
	resolve     func(udid string) (goios.DeviceEntry, error)
	pairState   func(udid string) error
	version     func(entry goios.DeviceEntry) (int64, error)
	startTunnel func(ctx context.Context, udid string) (io.Closer, tunnelInfo, error)
	enrich      func(entry goios.DeviceEntry, udid string, info tunnelInfo) (goios.DeviceEntry, error)
	forwardPort func(entry goios.DeviceEntry, hostPort, devicePort int) (io.Closer, error)
}

// tunnelInfo is what the driver needs from a running tunnel to reach the
// iOS 17+ service surface: the remote-service-discovery endpoint plus the
// local userspace listener.
type tunnelInfo struct {
	Address          string
	RsdPort          int
	UserspaceTUN     bool
	UserspaceTUNPort int
}

func newSession(udid string, hostPort int) *session {
	return &session{udid: udid, hostPort: hostPort}
}

// start prepares the device, probing each precondition in an order that
// turns every failure into one actionable operator message. It runs before
// any device mutation.
func (session *session) start(ctx context.Context) (goios.DeviceEntry, error) {
	if err := ctx.Err(); err != nil {
		return goios.DeviceEntry{}, err
	}
	resolve := session.resolve
	if resolve == nil {
		resolve = resolveDevice
	}
	entry, err := resolve(session.udid)
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf(
			"physical iOS device %s is not reachable over usbmuxd: %w"+
				" (is the device connected and unlocked, and is usbmuxd running?)",
			session.udid, err)
	}
	pairState := session.pairState
	if pairState == nil {
		pairState = readPairState
	}
	if err := pairState(session.udid); err != nil {
		return goios.DeviceEntry{}, fmt.Errorf(
			"physical iOS device %s is not paired with this host: %w"+
				" (unlock the device, reconnect it, and tap Trust)",
			session.udid, err)
	}
	version := session.version
	if version == nil {
		version = deviceVersionMajor
	}
	major, err := version(entry)
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf(
			"read iOS version of device %s: %w", session.udid, err)
	}
	session.iosVersionMajor = major
	if major >= tunnelMajorFloor {
		start := session.startTunnel
		if start == nil {
			start = startUserspaceTunnel
		}
		tunnelHandle, info, err := start(ctx, session.udid)
		if err != nil {
			return goios.DeviceEntry{}, fmt.Errorf(
				"start the iOS %d tunnel for device %s: %w"+
					" (no sudo is needed for the built-in userspace tunnel;"+
					" as a fallback run `ios tunnel start` from go-ios and retry)",
				major, session.udid, err)
		}
		session.tunnel = tunnelHandle
		enrich := session.enrich
		if enrich == nil {
			enrich = enrichWithRsd
		}
		entry, err = enrich(entry, session.udid, info)
		if err != nil {
			session.closeTunnel()
			return goios.DeviceEntry{}, fmt.Errorf(
				"connect to the RSD service surface of %s over the tunnel: %w",
				session.udid, err)
		}
	}
	forwardSeam := session.forwardPort
	if forwardSeam == nil {
		forwardSeam = forwardToDevice
	}
	listener, err := forwardSeam(entry, session.hostPort, runnerDevicePort)
	if err != nil {
		session.closeTunnel()
		return goios.DeviceEntry{}, fmt.Errorf(
			"forward host port %d to device port %d on %s: %w",
			session.hostPort, runnerDevicePort, session.udid, err)
	}
	session.forward = listener
	session.entry = entry
	return entry, nil
}

// stop tears the session down in reverse order of start.
func (session *session) stop() error {
	var errs []error
	if session.forward != nil {
		if err := session.forward.Close(); err != nil {
			errs = append(errs, fmt.Errorf("stop port forward for %s: %w", session.udid, err))
		}
		session.forward = nil
	}
	if err := session.closeTunnel(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (session *session) closeTunnel() error {
	if session.tunnel == nil {
		return nil
	}
	err := session.tunnel.Close()
	session.tunnel = nil
	if err != nil {
		return fmt.Errorf("stop tunnel for %s: %w", session.udid, err)
	}
	return nil
}

func resolveDevice(udid string) (goios.DeviceEntry, error) {
	return goios.GetDevice(udid)
}

func readPairState(udid string) error {
	_, err := goios.ReadPairRecord(udid)
	return err
}

func deviceVersionMajor(entry goios.DeviceEntry) (int64, error) {
	version, err := goios.GetProductVersion(entry)
	if err != nil {
		return 0, err
	}
	return version.Major(), nil
}

// startUserspaceTunnel runs the go-ios tunnel in-process with the userspace
// network stack: no root, no separate daemon. Pair records live under the
// host's cache directory so re-opens skip re-pairing.
func startUserspaceTunnel(ctx context.Context, udid string) (io.Closer, tunnelInfo, error) {
	records, err := tunnelPairRecordsPath()
	if err != nil {
		return nil, tunnelInfo{}, err
	}
	manager, err := tunnel.NewPairRecordManager(records)
	if err != nil {
		return nil, tunnelInfo{}, fmt.Errorf("prepare tunnel pair records in %s: %w", records, err)
	}
	tunnelManager := tunnel.NewTunnelManagerForDevice(manager, true, udid, goios.HttpApiPort())
	if err := tunnelManager.UpdateTunnels(ctx); err != nil {
		_ = tunnelManager.Close()
		return nil, tunnelInfo{}, err
	}
	established, err := tunnelManager.FindTunnel(udid)
	if err != nil {
		_ = tunnelManager.Close()
		return nil, tunnelInfo{}, err
	}
	return tunnelManager, tunnelInfo{
		Address:          established.Address,
		RsdPort:          established.RsdPort,
		UserspaceTUN:     established.UserspaceTUN,
		UserspaceTUNPort: established.UserspaceTUNPort,
	}, nil
}

// enrichWithRsd performs the remote-service-discovery handshake over the
// tunnel and returns the device entry testmanagerd and instruments need on
// iOS 17+. It mirrors go-ios's own device preparation, with errors returned
// instead of exiting.
func enrichWithRsd(entry goios.DeviceEntry, udid string, info tunnelInfo) (goios.DeviceEntry, error) {
	entry.UserspaceTUN = info.UserspaceTUN
	entry.UserspaceTUNHost = "127.0.0.1"
	entry.UserspaceTUNPort = info.UserspaceTUNPort
	rsdService, err := goios.NewWithAddrPortDevice(info.Address, info.RsdPort, entry)
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf("connect to RSD at [%s]:%d: %w", info.Address, info.RsdPort, err)
	}
	defer rsdService.Close()
	rsdProvider, err := rsdService.Handshake()
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf("RSD handshake: %w", err)
	}
	enriched, err := goios.GetDeviceWithAddress(udid, info.Address, rsdProvider)
	if err != nil {
		return goios.DeviceEntry{}, fmt.Errorf("resolve tunneled device entry: %w", err)
	}
	enriched.UserspaceTUN = entry.UserspaceTUN
	enriched.UserspaceTUNHost = entry.UserspaceTUNHost
	enriched.UserspaceTUNPort = entry.UserspaceTUNPort
	return enriched, nil
}

func tunnelPairRecordsPath() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve a cache directory for tunnel pair records: %w", err)
	}
	records := filepath.Join(cache, "flowbaton", "ios-tunnel")
	if err := os.MkdirAll(records, 0o700); err != nil {
		return "", fmt.Errorf("create tunnel pair-record directory: %w", err)
	}
	return records, nil
}

func forwardToDevice(entry goios.DeviceEntry, hostPort, devicePort int) (io.Closer, error) {
	listener, err := forward.Forward(entry, uint16(hostPort), uint16(devicePort))
	if err != nil {
		return nil, err
	}
	return listener, nil
}
