package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// iosRunnerFlavor selects which platform build of the runner a target needs:
// a simulator drives the iphonesimulator build, hardware the iphoneos build.
type iosRunnerFlavor string

const (
	iosRunnerFlavorSimulator iosRunnerFlavor = "iphonesimulator"
	iosRunnerFlavorDevice    iosRunnerFlavor = "iphoneos"
)

// matches reports whether a built .xctestrun belongs to this flavor.
// xcodebuild names the file after the destination SDK
// (…_iphoneos26.0-arm64.xctestrun vs …_iphonesimulator26.0-arm64.xctestrun);
// a name that says neither (an operator's hand-rolled file) matches both.
func (flavor iosRunnerFlavor) matches(path string) bool {
	name := filepath.Base(path)
	if strings.Contains(name, string(iosRunnerFlavorSimulator)) {
		return flavor == iosRunnerFlavorSimulator
	}
	if strings.Contains(name, string(iosRunnerFlavorDevice)) {
		return flavor == iosRunnerFlavorDevice
	}
	return true
}

func (flavor iosRunnerFlavor) filter(paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if flavor.matches(path) {
			kept = append(kept, path)
		}
	}
	return kept
}

// iosRunnerBundle finds the built runner for one flavor, if there is one. A
// nil bundle is not a failure: it means the operator starts the runner, the
// mode this driver has always had.
func iosRunnerBundle(ctx context.Context, flavor iosRunnerFlavor) (*ios.RunnerBundle, error) {
	if override := os.Getenv(iosXCTestRunVariable); override != "" {
		return &ios.RunnerBundle{XCTestRun: override}, nil
	}
	acquired, err := loadCachedDriverAsset(ctx, "ios")
	if err == nil {
		return iosRunnerBundleAt(acquired.Directory, flavor)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("load signed iOS driver: %w", err)
	}
	derived, err := iosDerivedDataPath()
	if err != nil {
		return nil, err
	}
	built, err := filepath.Glob(filepath.Join(derived, "Build", "Products", "*.xctestrun"))
	if err != nil {
		return nil, err
	}
	built = flavor.filter(built)
	switch len(built) {
	case 0:
		return nil, nil
	case 1:
		return &ios.RunnerBundle{XCTestRun: built[0]}, nil
	default:
		sort.Strings(built)
		return nil, fmt.Errorf(
			"%s holds %d built %s runners (%s); set %s to the one to drive",
			derived, len(built), flavor, filepath.Base(built[0])+", …", iosXCTestRunVariable)
	}
}

func iosRunnerBundleAt(directory string, flavor iosRunnerFlavor) (*ios.RunnerBundle, error) {
	built, err := filepath.Glob(filepath.Join(directory, "*.xctestrun"))
	if err != nil {
		return nil, err
	}
	built = flavor.filter(built)
	switch len(built) {
	case 0:
		return nil, fmt.Errorf("signed iOS driver %s contains no %s .xctestrun", directory, flavor)
	case 1:
		return &ios.RunnerBundle{XCTestRun: built[0]}, nil
	default:
		sort.Strings(built)
		return nil, fmt.Errorf("signed iOS driver %s contains %d %s .xctestrun files (%s)",
			directory, len(built), flavor, filepath.Base(built[0])+", …")
	}
}
