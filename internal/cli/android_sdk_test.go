package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Android setup honors exported SDK paths, recognizes the conventional local
// installation, and names the required variable when no SDK is available.

func TestTheSDKIsFoundWhereItNormallyLives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	conventional := filepath.Join(home, filepath.FromSlash(conventionalAndroidSDK()))
	if err := os.MkdirAll(filepath.Join(conventional, "platform-tools"), 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := androidSDKPath()
	if err != nil {
		t.Fatalf("androidSDKPath() error = %v", err)
	}
	if found != conventional {
		t.Fatalf("androidSDKPath() = %q, want %q", found, conventional)
	}
}

func TestAnExportedSDKPathWins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	exported := t.TempDir()
	t.Setenv("ANDROID_HOME", exported)

	found, err := androidSDKPath()
	if err != nil {
		t.Fatalf("androidSDKPath() error = %v", err)
	}
	if found != exported {
		t.Fatalf("androidSDKPath() = %q, want the exported %q", found, exported)
	}
}

func TestANDROIDSDKROOTIsHonoredToo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANDROID_HOME", "")
	exported := t.TempDir()
	t.Setenv("ANDROID_SDK_ROOT", exported)

	found, err := androidSDKPath()
	if err != nil {
		t.Fatalf("androidSDKPath() error = %v", err)
	}
	if found != exported {
		t.Fatalf("androidSDKPath() = %q, want the exported %q", found, exported)
	}
}

// No SDK anywhere is the case that has to explain itself, because Gradle's
// explanation is the one we scroll away.
func TestAMissingSDKNamesTheVariableToSet(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")

	_, err := androidSDKPath()
	if err == nil {
		t.Fatal("a missing SDK was accepted; the failure lands in Gradle output instead")
	}
	if !strings.Contains(err.Error(), "ANDROID_HOME") {
		t.Fatalf("the error did not name the variable to set: %v", err)
	}
}

func TestTheConventionalSDKPathMatchesThePlatform(t *testing.T) {
	t.Parallel()

	// Not a tautology: this is the one value that has to be right per OS, and
	// getting it wrong turns the fallback into silence.
	want := map[string]string{
		"darwin":  "Library/Android/sdk",
		"linux":   "Android/Sdk",
		"windows": "AppData/Local/Android/Sdk",
	}[runtime.GOOS]
	if want == "" {
		t.Skipf("no conventional SDK path pinned for %s", runtime.GOOS)
	}
	if got := conventionalAndroidSDK(); got != want {
		t.Fatalf("conventionalAndroidSDK() = %q, want %q", got, want)
	}
}
