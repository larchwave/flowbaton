package cli

import (
	"fmt"
	"net"
	"testing"
)

// A diagnostic must never disturb a session that is already driving the
// device. On Android the managed open uninstalls and reinstalls the agent
// packages, which would pull the agent out from under a running session.
func TestDiagnosticOptionsLeaveARunningSessionAlone(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot bind a probe listener: %v", err)
	}
	defer listener.Close()
	servingPort := listener.Addr().(*net.TCPAddr).Port

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot reserve a free port: %v", err)
	}
	idlePort := free.Addr().(*net.TCPAddr).Port
	free.Close()

	for _, test := range []struct {
		name      string
		platform  string
		port      int
		reinstall bool
	}{
		{name: "ios with nobody serving takes the managed driver", platform: "ios", port: idlePort, reinstall: true},
		{name: "ios beside a live runner stays passive", platform: "ios", port: servingPort, reinstall: false},
		{name: "android never reinstalls under a diagnostic", platform: "android", port: idlePort, reinstall: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := diagnosticDriverOptions(test.platform, test.port)
			if got.ReinstallDriver != test.reinstall {
				t.Fatalf("ReinstallDriver = %v, want %v", got.ReinstallDriver, test.reinstall)
			}
			if got.Platform != test.platform {
				t.Fatalf("Platform = %q", got.Platform)
			}
		})
	}
	_ = fmt.Sprint()
}
