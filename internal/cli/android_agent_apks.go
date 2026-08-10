package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/larchwave/flowbaton/internal/android"
)

// The Android driver installs the agent and starts instrumentation when the
// APK pair is available (spec 02 §2.2). The operator may export the pair or use
// the signed release asset installed by driver-setup.
//
// Same answer as the iOS runner (ios_runner_bundle.go): a fixed directory under
// the home that `driver-setup` writes and the run reads. Under the home rather
// than in the tree because it must survive a clean checkout.

const (
	androidAppAPKVariable  = "FLOWBATON_ANDROID_APP_APK"
	androidTestAPKVariable = "FLOWBATON_ANDROID_TEST_APK"

	androidAgentDirectory = ".flowbaton/android-agent"
	androidAppAPKName     = "agent.apk"
	androidTestAPKName    = "agent-androidTest.apk"
)

// androidAgentPath is the directory both halves use.
func androidAgentPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, filepath.FromSlash(androidAgentDirectory)), nil
}

// androidAgentAPKs finds the agent to install, if there is one. A nil pair is
// not a failure: it means the operator started the agent.
func androidAgentAPKs() (*android.AgentAPKs, error) {
	app := os.Getenv(androidAppAPKVariable)
	test := os.Getenv(androidTestAPKVariable)
	switch {
	case app != "" && test != "":
		return &android.AgentAPKs{App: app, Test: test}, nil
	case app != "" || test != "":
		return nil, fmt.Errorf("%s and %s must be set together",
			androidAppAPKVariable, androidTestAPKVariable)
	}

	acquired, err := loadCachedDriverAsset(context.Background(), "android")
	if err == nil {
		return installedAgentAPKs(acquired.Directory)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load signed Android driver: %w", err)
	}

	directory, err := androidAgentPath()
	if err != nil {
		return nil, err
	}
	installed, err := installedAgentAPKs(directory)
	if err != nil {
		return nil, err
	}
	return installed, nil
}

// installedAgentAPKs reads the pair driver-setup left behind. Half a pair is a
// broken install, not an operator-started agent, and is reported explicitly.
func installedAgentAPKs(directory string) (*android.AgentAPKs, error) {
	app := filepath.Join(directory, androidAppAPKName)
	test := filepath.Join(directory, androidTestAPKName)
	hasApp, err := fileExists(app)
	if err != nil {
		return nil, err
	}
	hasTest, err := fileExists(test)
	if err != nil {
		return nil, err
	}
	switch {
	case hasApp && hasTest:
		return &android.AgentAPKs{App: app, Test: test}, nil
	case !hasApp && !hasTest:
		return nil, nil
	case hasApp:
		return nil, fmt.Errorf("%s holds %s but not %s; rerun flowbaton driver-setup -p android",
			directory, androidAppAPKName, androidTestAPKName)
	default:
		return nil, fmt.Errorf("%s holds %s but not %s; rerun flowbaton driver-setup -p android",
			directory, androidTestAPKName, androidAppAPKName)
	}
}

func fileExists(path string) (bool, error) {
	switch _, err := os.Stat(path); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}
