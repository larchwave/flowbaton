package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// driver-setup installs the Android agent APK pair under the user's home so a
// run can discover it without environment variables. Explicit APK variables
// remain available for custom build locations.

func agentHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)
	directory := filepath.Join(home, filepath.FromSlash(androidAgentDirectory))
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}

func writeAPK(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("apk"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTheInstalledAgentIsFoundWithoutAnyVariables(t *testing.T) {
	directory := agentHome(t)
	writeAPK(t, filepath.Join(directory, androidAppAPKName))
	writeAPK(t, filepath.Join(directory, androidTestAPKName))

	apks, err := androidAgentAPKs(context.Background())
	if err != nil {
		t.Fatalf("androidAgentAPKs() error = %v", err)
	}
	if apks == nil {
		t.Fatal("the installed agent was not found, so -p android needs adb by hand")
	}
	if filepath.Base(apks.App) != androidAppAPKName || filepath.Base(apks.Test) != androidTestAPKName {
		t.Fatalf("found the wrong pair: %#v", apks)
	}
}

// An empty installation directory permits an externally managed agent.
func TestNoInstalledAgentIsNotAFailure(t *testing.T) {
	agentHome(t)

	apks, err := androidAgentAPKs(context.Background())
	if err != nil {
		t.Fatalf("androidAgentAPKs() error = %v", err)
	}
	if apks != nil {
		t.Fatalf("found an agent in an empty directory: %#v", apks)
	}
}

// Half an install is a broken install, not an operator-started agent: saying
// nothing here is how a stale build silently drives the run.
func TestHalfAnInstalledAgentIsRefused(t *testing.T) {
	directory := agentHome(t)
	writeAPK(t, filepath.Join(directory, androidAppAPKName))

	_, err := androidAgentAPKs(context.Background())
	if err == nil {
		t.Fatal("a half-installed agent was accepted")
	}
	if !strings.Contains(err.Error(), androidTestAPKName) {
		t.Fatalf("the error did not name the missing half: %v", err)
	}
}

// The variables still win, for a build that lives somewhere else.
func TestTheVariablesOutrankTheInstalledAgent(t *testing.T) {
	directory := agentHome(t)
	writeAPK(t, filepath.Join(directory, androidAppAPKName))
	writeAPK(t, filepath.Join(directory, androidTestAPKName))
	t.Setenv("FLOWBATON_ANDROID_APP_APK", "/elsewhere/app.apk")
	t.Setenv("FLOWBATON_ANDROID_TEST_APK", "/elsewhere/test.apk")

	apks, err := androidAgentAPKs(context.Background())
	if err != nil {
		t.Fatalf("androidAgentAPKs() error = %v", err)
	}
	if apks == nil || apks.App != "/elsewhere/app.apk" {
		t.Fatalf("the variables lost to the installed agent: %#v", apks)
	}
}
