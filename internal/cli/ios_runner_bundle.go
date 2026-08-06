package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nohavewho/flowbaton/internal/ios"
)

// driver-setup builds the iOS runner into a fixed derived-data directory under
// the user's home. Runs discover the prebuilt .xctestrun there and start one
// managed process per simulator without requiring an Xcode project.

const (
	// iosXCTestRunVariable names the built runner directly, for a build that
	// lives somewhere else — CI artifacts, a second checkout. The Android
	// sibling is FLOWBATON_ANDROID_APP_APK.
	iosXCTestRunVariable = "FLOWBATON_IOS_XCTESTRUN"
	// iosDerivedDataDirectory is where driver-setup builds. Under the home
	// rather than in the repo: it is a build cache, it is large, and it must
	// survive a clean checkout of the tree it was built from.
	iosDerivedDataDirectory = ".flowbaton/ios-driver"
)

// iosDerivedDataPath is the -derivedDataPath both halves use.
func iosDerivedDataPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, filepath.FromSlash(iosDerivedDataDirectory)), nil
}

// iosRunnerBundle finds the built runner, if there is one. A nil bundle is not
// a failure: it means the operator starts the runner, the mode this driver has
// always had.
func iosRunnerBundle() (*ios.RunnerBundle, error) {
	if override := os.Getenv(iosXCTestRunVariable); override != "" {
		return &ios.RunnerBundle{XCTestRun: override}, nil
	}
	derived, err := iosDerivedDataPath()
	if err != nil {
		return nil, err
	}
	built, err := filepath.Glob(filepath.Join(derived, "Build", "Products", "*.xctestrun"))
	if err != nil {
		return nil, err
	}
	switch len(built) {
	case 0:
		return nil, nil
	case 1:
		return &ios.RunnerBundle{XCTestRun: built[0]}, nil
	default:
		sort.Strings(built)
		return nil, fmt.Errorf(
			"%s holds %d built runners (%s); set %s to the one to drive",
			derived, len(built), filepath.Base(built[0])+", …", iosXCTestRunVariable)
	}
}
