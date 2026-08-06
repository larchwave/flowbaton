package cli

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Giving each shard its own driver port.
//
// specs/04-wire-protocols.md:90: "Per-shard ports selected by CLI (ephemeral).
// No dynamic port scanning — explicit port agreement host↔device."
// specs/03-cli-tooling.md:28 adds that the port is OS-assigned via an ephemeral
// socket unless the user set one.
//
// Two shards sharing a port is not a degraded run, it is a wrong one: both talk
// to the SAME runner, so two suites land on one device while the report still
// says they ran on two. That is why a duplicate is refused rather than logged.

// Default ports, both frozen by the wire contract:
// specs/02-device-drivers.md:34 (Android) and :68 (iOS).
const (
	defaultAndroidDriverPort = 7001
	defaultIOSDriverPort     = 22087
)

// defaultWebDevToolsPort is what a web run's "driver port" means: the Chrome
// DevTools port. Not a wire-contract number like the two above — 9222 is
// simply what every browser tool already means by it.
const defaultWebDevToolsPort = 9222

// driverPortVariable is the environment variable the iOS runner itself reads
// (specs/02-device-drivers.md:68). The host reads the same one, because a host
// and a runner that disagree about the port simply never meet.
const driverPortVariable = "PORT"

var errAllocatorExhausted = errors.New("no ephemeral port was available")

// assignDriverPorts gives shard 1 the agreed base port and every later shard one
// of its own.
//
// Shard 1 keeps the base because the contract pins it and because an unsharded
// run is the ordinary case: moving it would leave every runner already listening
// on 22087 unreachable.
func assignDriverPorts(
	shards []Shard, base int, operator []int, allocate func() (int, error),
) error {
	if len(operator) != 0 && len(operator) != len(shards) {
		return fmt.Errorf(
			"%s names %d ports but the run has %d shards",
			driverPortsVariable, len(operator), len(shards))
	}
	taken := map[int]int{}
	for index := range shards {
		port := base
		if len(operator) != 0 {
			port = operator[index]
		} else if index > 0 {
			allocated, err := allocate()
			if err != nil {
				// Falling back to the base port here would be the duplicate
				// case with no error — exactly what this refuses below.
				return fmt.Errorf("shard %d: allocating a driver port: %w", index+1, err)
			}
			port = allocated
		}
		if owner, clash := taken[port]; clash {
			return fmt.Errorf(
				"shards %d and %d were both given driver port %d", owner, index+1, port)
		}
		taken[port] = index + 1
		shards[index].DriverPort = port
	}
	return nil
}

// ephemeralPort asks the operating system for a free port and gives it back.
//
// Binding port 0 and reading what was assigned is what "OS-assigned via
// ephemeral socket" means, and it is deliberately NOT a scan: the spec rules
// scanning out, and a scan races with every other process on the machine anyway.
// This still has a window between the close and the runner's bind, which is
// unavoidable without handing the listener itself to the runner.
func ephemeralPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("%w: %w", errAllocatorExhausted, err)
	}
	defer func() { _ = listener.Close() }()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("%w: unexpected address %T", errAllocatorExhausted, listener.Addr())
	}
	return address.Port, nil
}

// basePort resolves the port shard 1 will use.
//
// An operator who exported PORT told the runner where to listen, so the host has
// to read the same variable. A value that is not a usable port falls back to the
// platform default rather than failing the run: the variable is also read by
// unrelated tools, and refusing every run on a machine where something else set
// PORT would be worse than using the documented default.
func basePort(options TestOptions, environ []string) int {
	fallback := defaultIOSDriverPort
	switch strings.ToLower(options.Platform) {
	case "android":
		fallback = defaultAndroidDriverPort
	case "web":
		fallback = defaultWebDevToolsPort
	}
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found || key != driverPortVariable {
			continue
		}
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return fallback
		}
		return port
	}
	return fallback
}

// diagnosticPort is what `hierarchy` and `query` connect on.
//
// They are not `test`: they do not start anything, they attach to whatever is
// already there. On iOS that is a runner the operator started, and a runner
// only listens where PORT told it to — so asking the OS for a free port and
// hoping meant those two subcommands could never reach a simulator at all.
//
// Android and web keep the free port, because there the host side of the
// connection is one end of a forward this process creates: any free number
// works, and taking a fixed one would collide with a run already using it.
func diagnosticPort(platform string, environ []string) (int, error) {
	if strings.EqualFold(platform, "ios") {
		return basePort(TestOptions{Platform: platform}, environ), nil
	}
	return ephemeralPort()
}

// driverPortsVariable lets an operator who started the runners say where they
// are, one port per shard, in shard order.
//
// Operator-started iOS runners need one explicit port per shard. Without the
// list, later shards receive OS-assigned ports that no prestarted runner knows.
//
// An environment variable rather than a flag, because the CLI surface has no
// driver-port-list option. It is NOT the runner's PORT, which
// the runner itself reads and which is a single value.
const driverPortsVariable = "FLOWBATON_DRIVER_PORTS"

// operatorPorts parses the list. A malformed entry is refused rather than
// skipped: a shard silently moved to a different port fails later, further from
// the mistake, and with a message about a connection instead of a command line.
func operatorPorts(values []string) []int {
	ports := make([]int, 0, len(values))
	for _, value := range values {
		port, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || port < 1 || port > 65535 {
			return nil
		}
		ports = append(ports, port)
	}
	return ports
}

// driverPortsFrom reads the list out of an environment.
func driverPortsFrom(environ []string) []int {
	for _, entry := range environ {
		key, value, found := strings.Cut(entry, "=")
		if !found || key != driverPortsVariable || strings.TrimSpace(value) == "" {
			continue
		}
		return operatorPorts(strings.Split(value, ","))
	}
	return nil
}
