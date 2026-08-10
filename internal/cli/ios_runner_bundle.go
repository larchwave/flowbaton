package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/larchwave/flowbaton/internal/ios"
)

// Runs prefer the signed release asset installed by driver-setup. The
// derived-data lookup remains for explicit repository development builds.

const (
	// iosXCTestRunVariable names the built runner directly, for a build that
	// lives somewhere else — CI artifacts, a second checkout. The Android
	// sibling is FLOWBATON_ANDROID_APP_APK.
	iosXCTestRunVariable = "FLOWBATON_IOS_XCTESTRUN"
	// iosDerivedDataDirectory is the source-tree build location.
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
	acquired, err := loadCachedDriverAsset(context.Background(), "ios")
	if err == nil {
		return iosRunnerBundleAt(acquired.Directory)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load signed iOS Simulator driver: %w", err)
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

func iosRunnerBundleAt(directory string) (*ios.RunnerBundle, error) {
	built, err := filepath.Glob(filepath.Join(directory, "*.xctestrun"))
	if err != nil {
		return nil, err
	}
	switch len(built) {
	case 0:
		return nil, fmt.Errorf("signed iOS driver %s contains no .xctestrun", directory)
	case 1:
		return &ios.RunnerBundle{XCTestRun: built[0]}, nil
	default:
		sort.Strings(built)
		return nil, fmt.Errorf("signed iOS driver %s contains %d .xctestrun files (%s)",
			directory, len(built), filepath.Base(built[0])+", …")
	}
}
