package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/assets"
	"github.com/larchwave/flowbaton/internal/version"
)

const (
	androidDriverAssetID = "android-agent"
	iosDriverAssetID     = "ios-simulator-runner"
	driverManifestEnv    = "FLOWBATON_DRIVER_MANIFEST"
	driverAssetBaseEnv   = "FLOWBATON_DRIVER_ASSET_BASE_URL"
)

func acquireDriverAsset(ctx context.Context, platform string) (assets.AcquiredAsset, error) {
	hostVersion := strings.TrimSpace(version.Version)
	if hostVersion == "" || hostVersion == "dev" {
		return assets.AcquiredAsset{}, errors.New(
			"signed driver assets require a tagged FlowBaton release; development builds must use an explicit source build")
	}
	source := assets.GitHubReleaseSource{BaseURL: os.Getenv(driverAssetBaseEnv)}
	manifestContents, err := loadReleaseDriverManifest(ctx, source, hostVersion)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	manifest, err := assets.ParseManifest(manifestContents)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	runtimeContract, request, err := driverAssetRequest(ctx, platform, hostVersion)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	resolved, err := assets.Resolve(manifest, runtimeContract, request)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	cacheRoot, err := driverAssetCacheRoot()
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	acquired, err := (assets.Manager{
		CacheRoot: cacheRoot,
		Source:    source,
		Verifier:  assets.CommandIdentityVerifier{},
	}).Acquire(ctx, resolved)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	if err := storeReleaseDriverManifest(cacheRoot, hostVersion, manifestContents); err != nil {
		return assets.AcquiredAsset{}, err
	}
	return acquired, nil
}

func loadReleaseDriverManifest(
	ctx context.Context, source assets.GitHubReleaseSource, hostVersion string,
) ([]byte, error) {
	if override := strings.TrimSpace(os.Getenv(driverManifestEnv)); override != "" {
		contents, err := os.ReadFile(override)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", driverManifestEnv, err)
		}
		return contents, nil
	}
	contents, err := source.DownloadManifest(ctx, hostVersion)
	if err != nil {
		return nil, fmt.Errorf("download signed driver manifest: %w", err)
	}
	return contents, nil
}

func driverAssetRequest(
	ctx context.Context, platform, hostVersion string,
) (assets.Runtime, assets.Request, error) {
	runtimeContract := assets.Runtime{
		HostVersion: hostVersion,
		HostOS:      runtime.GOOS,
		HostArch:    runtime.GOARCH,
	}
	request := assets.Request{AssetVersion: hostVersion}
	switch platform {
	case "android":
		runtimeContract.AndroidAPI = 26
		request.ID = androidDriverAssetID
		request.Platform = assets.PlatformAndroid
	case "ios":
		if runtime.GOOS != "darwin" {
			return assets.Runtime{}, assets.Request{}, errors.New("the iOS Simulator driver is available only on macOS")
		}
		xcode, iosRuntime, err := localAppleToolchain(ctx)
		if err != nil {
			return assets.Runtime{}, assets.Request{}, err
		}
		runtimeContract.XcodeVersion = xcode
		runtimeContract.IOSRuntimeVersion = iosRuntime
		request.ID = iosDriverAssetID
		request.Platform = assets.PlatformIOSSimulator
	default:
		return assets.Runtime{}, assets.Request{}, fmt.Errorf("unsupported driver asset platform %q", platform)
	}
	return runtimeContract, request, nil
}

func localAppleToolchain(ctx context.Context) (string, string, error) {
	xcodeOutput, err := exec.CommandContext(ctx, "xcodebuild", "-version").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("xcodebuild -version: %w: %s", err, tailOf(xcodeOutput))
	}
	fields := strings.Fields(string(xcodeOutput))
	if len(fields) < 2 || fields[0] != "Xcode" {
		return "", "", fmt.Errorf("xcodebuild -version returned an unrecognized value %q", strings.TrimSpace(string(xcodeOutput)))
	}
	runtimeOutput, err := exec.CommandContext(ctx, "xcrun", "simctl", "list", "runtimes", "-j").CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("simctl list runtimes: %w: %s", err, tailOf(runtimeOutput))
	}
	var document struct {
		Runtimes []struct {
			Version     string `json:"version"`
			IsAvailable bool   `json:"isAvailable"`
			Name        string `json:"name"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(runtimeOutput, &document); err != nil {
		return "", "", fmt.Errorf("decode simctl runtimes: %w", err)
	}
	var versions []string
	for _, candidate := range document.Runtimes {
		if candidate.IsAvailable && strings.HasPrefix(candidate.Name, "iOS") && numericVersion(candidate.Version) != nil {
			versions = append(versions, candidate.Version)
		}
	}
	if len(versions) == 0 {
		return "", "", errors.New("no available iOS Simulator runtime was reported by simctl")
	}
	sort.Slice(versions, func(i, j int) bool { return compareNumericVersions(versions[i], versions[j]) > 0 })
	return fields[1], versions[0], nil
}

func numericVersion(value string) []int {
	parts := strings.Split(value, ".")
	result := make([]int, len(parts))
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return nil
		}
		result[index] = parsed
	}
	return result
}

func compareNumericVersions(left, right string) int {
	a := numericVersion(left)
	b := numericVersion(right)
	for index := 0; index < len(a) || index < len(b); index++ {
		var x, y int
		if index < len(a) {
			x = a[index]
		}
		if index < len(b) {
			y = b[index]
		}
		if x < y {
			return -1
		}
		if x > y {
			return 1
		}
	}
	return 0
}

func driverAssetCacheRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".flowbaton", "drivers"), nil
}

func storedDriverManifest(cacheRoot, hostVersion string) string {
	return filepath.Join(cacheRoot, "manifests", hostVersion+".json")
}

func storeReleaseDriverManifest(cacheRoot, hostVersion string, contents []byte) error {
	directory, err := secureDriverManifestDirectory(cacheRoot, true)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".manifest-*")
	if err != nil {
		return fmt.Errorf("create driver manifest temp file: %w", err)
	}
	path := temporary.Name()
	published := false
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(path)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	final := storedDriverManifest(cacheRoot, hostVersion)
	if err := os.Rename(path, final); err != nil {
		return fmt.Errorf("publish driver manifest cache: %w", err)
	}
	published = true
	return nil
}

func loadCachedDriverAsset(ctx context.Context, platform string) (assets.AcquiredAsset, error) {
	hostVersion := strings.TrimSpace(version.Version)
	if hostVersion == "" || hostVersion == "dev" {
		return assets.AcquiredAsset{}, fs.ErrNotExist
	}
	cacheRoot, err := driverAssetCacheRoot()
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	directory, err := secureDriverManifestDirectory(cacheRoot, false)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	manifestPath := filepath.Join(directory, hostVersion+".json")
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return assets.AcquiredAsset{}, fmt.Errorf("%w: driver manifest is not a regular file", assets.ErrInvalidAssetCache)
	}
	contents, err := os.ReadFile(manifestPath)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	manifest, err := assets.ParseManifest(contents)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	runtimeContract, request, err := driverAssetRequest(ctx, platform, hostVersion)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	resolved, err := assets.Resolve(manifest, runtimeContract, request)
	if err != nil {
		return assets.AcquiredAsset{}, err
	}
	return (assets.Manager{
		CacheRoot: cacheRoot,
		Source:    assets.GitHubReleaseSource{BaseURL: os.Getenv(driverAssetBaseEnv)},
		Verifier:  assets.CommandIdentityVerifier{},
	}).Acquire(ctx, resolved)
}

func secureDriverManifestDirectory(cacheRoot string, create bool) (string, error) {
	if err := (assets.Manager{CacheRoot: cacheRoot}).EnsureCacheRoot(); err != nil {
		return "", err
	}
	directory := filepath.Join(cacheRoot, "manifests")
	if create {
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create driver manifest cache: %w", err)
		}
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%w: driver manifest cache is not a real directory", assets.ErrInvalidAssetCache)
	}
	return directory, nil
}
