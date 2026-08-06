package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// The agent's Gradle build needs the Android SDK, and Gradle only learns where
// it is from ANDROID_HOME or a local.properties this repo does not ship. On a
// machine with the SDK installed in the normal place but the variable not
// exported — the common case — the build failed and the reason scrolled past
// the tail of Gradle's output we keep. So we resolve it here: the exported
// value if there is one, the conventional location otherwise, and a message
// naming the variable when there is really nothing.

// conventionalAndroidSDK is where the SDK manager puts it, per OS.
func conventionalAndroidSDK() string {
	switch runtime.GOOS {
	case "darwin":
		return "Library/Android/sdk"
	case "windows":
		return "AppData/Local/Android/Sdk"
	default:
		return "Android/Sdk"
	}
}

func androidSDKPath() (string, error) {
	for _, variable := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if exported := os.Getenv(variable); exported != "" {
			return exported, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	conventional := filepath.Join(home, filepath.FromSlash(conventionalAndroidSDK()))
	if info, err := os.Stat(conventional); err == nil && info.IsDir() {
		return conventional, nil
	}
	return "", fmt.Errorf(
		"no Android SDK: nothing at %s and neither ANDROID_HOME nor ANDROID_SDK_ROOT is set",
		conventional)
}
