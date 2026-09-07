package version

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// unixReleaseShellHost is the portable-CI matrix for scripts that assume a
// POSIX shell, physical pwd -P paths, and tools such as shasum. Windows
// runners can still have bash, so a LookPath("bash") guard is not enough;
// that host uses scripts/release/smoke-windows.ps1 instead.
func unixReleaseShellHost(goos string) bool {
	return goos != "windows"
}

func requireUnixReleaseShell(t *testing.T) {
	t.Helper()
	if !unixReleaseShellHost(runtime.GOOS) {
		t.Skip("POSIX release scripts run on Unix CI hosts; Windows coverage is scripts/release/smoke-windows.ps1")
	}
}

func TestUnixReleaseShellHost(t *testing.T) {
	if unixReleaseShellHost("windows") {
		t.Fatal("windows must not execute POSIX Darwin release-shell tests")
	}
	for _, goos := range []string{"linux", "darwin"} {
		if !unixReleaseShellHost(goos) {
			t.Fatalf("%s must keep POSIX Darwin release-shell coverage", goos)
		}
	}
}

func TestPOSIXReleaseShellTestsCallUnixHostGuard(t *testing.T) {
	for _, name := range []string{"posix_smoke_test.go", "notary_auth_test.go"} {
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "requireUnixReleaseShell(t)") {
			t.Errorf("%s runs a POSIX release script without requireUnixReleaseShell", name)
		}
	}
}
