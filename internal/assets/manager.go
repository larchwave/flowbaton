package assets

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const cacheMarkerSchemaVersion = "flowbaton.asset-cache.v0"

var (
	ErrUnresolvedAsset       = errors.New("asset was not resolved by the compatibility contract")
	ErrArchiveSourceRequired = errors.New("archive source is required")
	ErrCacheRootRequired     = errors.New("asset cache root is required")
	ErrInvalidAssetCache     = errors.New("invalid asset cache")
)

type ArchiveSource interface {
	Open(context.Context, Asset) (io.ReadCloser, error)
}

type Manager struct {
	// CacheRoot is the drivers directory. Production callers use
	// ~/.flowbaton/drivers; tests and isolated tools inject a temporary root.
	CacheRoot string
	Source    ArchiveSource
	Verifier  IdentityVerifier
	Locker    Locker
}

type AcquiredAsset struct {
	Directory    string
	IdentityPath string
}

type cacheMarker struct {
	SchemaVersion    string `json:"schema_version"`
	ManifestVersion  string `json:"manifest_version"`
	HostVersion      string `json:"host_version"`
	AssetID          string `json:"asset_id"`
	AssetVersion     string `json:"asset_version"`
	AssetHash        string `json:"asset_hash"`
	DescriptorSHA256 string `json:"descriptor_sha256"`
}

func (m Manager) Acquire(ctx context.Context, resolved ResolvedAsset) (acquired AcquiredAsset, returnErr error) {
	if !resolved.resolved {
		return AcquiredAsset{}, ErrUnresolvedAsset
	}
	if m.Source == nil {
		return AcquiredAsset{}, ErrArchiveSourceRequired
	}
	if m.Verifier == nil {
		return AcquiredAsset{}, ErrVerifierRequired
	}
	if m.CacheRoot == "" {
		return AcquiredAsset{}, ErrCacheRootRequired
	}
	if err := ctx.Err(); err != nil {
		return AcquiredAsset{}, err
	}
	if err := validateAsset(resolved.Asset); err != nil {
		return AcquiredAsset{}, fmt.Errorf("%w: resolved descriptor: %v", ErrInvalidAssetManifest, err)
	}

	versionDirectory, lockDirectory, err := prepareCacheDirectories(m.CacheRoot, resolved.Asset.HostVersion)
	if err != nil {
		return AcquiredAsset{}, err
	}
	locker := m.Locker
	if locker == nil {
		locker = FileLocker{}
	}
	lockPath := filepath.Join(lockDirectory, resolved.Asset.AssetHash+".lock")
	unlocker, err := locker.Lock(ctx, lockPath)
	if err != nil {
		return AcquiredAsset{}, err
	}
	defer func() {
		if unlockErr := unlocker.Unlock(); returnErr == nil && unlockErr != nil {
			acquired = AcquiredAsset{}
			returnErr = unlockErr
		}
	}()

	if err := removeInterruptedTemps(versionDirectory, resolved.Asset.AssetHash); err != nil {
		return AcquiredAsset{}, err
	}
	finalDirectory := filepath.Join(versionDirectory, resolved.Asset.AssetHash)
	if _, err := os.Lstat(finalDirectory); err == nil {
		if verifyErr := verifyAssetDirectory(ctx, finalDirectory, resolved, m.Verifier); verifyErr == nil {
			return acquiredAssetAt(finalDirectory, resolved.Asset), nil
		}
		if removeErr := os.RemoveAll(finalDirectory); removeErr != nil {
			return AcquiredAsset{}, fmt.Errorf("remove invalid asset cache: %w", removeErr)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return AcquiredAsset{}, fmt.Errorf("inspect asset cache: %w", err)
	}

	archive, err := readAndVerifyArchive(ctx, m.Source, resolved.Asset)
	if err != nil {
		return AcquiredAsset{}, err
	}
	temporaryDirectory, err := os.MkdirTemp(versionDirectory, "."+resolved.Asset.AssetHash+".tmp-")
	if err != nil {
		return AcquiredAsset{}, fmt.Errorf("create sibling asset temp directory: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()

	if err := extractArchive(temporaryDirectory, resolved.Asset, archive); err != nil {
		return AcquiredAsset{}, err
	}
	if err := writeCacheMarker(temporaryDirectory, resolved); err != nil {
		return AcquiredAsset{}, err
	}
	if err := verifyAssetDirectory(ctx, temporaryDirectory, resolved, m.Verifier); err != nil {
		return AcquiredAsset{}, err
	}
	if err := ctx.Err(); err != nil {
		return AcquiredAsset{}, err
	}
	if err := os.Rename(temporaryDirectory, finalDirectory); err != nil {
		return AcquiredAsset{}, fmt.Errorf("atomically publish verified asset: %w", err)
	}
	published = true
	return acquiredAssetAt(finalDirectory, resolved.Asset), nil
}

func prepareCacheDirectories(cacheRoot, hostVersion string) (string, string, error) {
	if !safeToken(hostVersion) {
		return "", "", fmt.Errorf("%w: unsafe host version", ErrInvalidAssetManifest)
	}
	if _, err := ensureSecureDirectoryPath(cacheRoot, true); err != nil {
		return "", "", err
	}
	versionDirectory := filepath.Join(cacheRoot, hostVersion)
	if err := ensureDirectDirectory(versionDirectory); err != nil {
		return "", "", err
	}
	lockDirectory := filepath.Join(versionDirectory, ".locks")
	if err := ensureDirectDirectory(lockDirectory); err != nil {
		return "", "", err
	}
	return versionDirectory, lockDirectory, nil
}

func ensureDirectDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create cache directory %q: %w", path, err)
	}
	return requireRealDirectory(path)
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("lstat cache directory %q: %w", path, err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%w: %q is not an owned real directory", ErrInvalidAssetCache, path)
	}
	return nil
}

func removeInterruptedTemps(versionDirectory, assetHash string) error {
	entries, err := os.ReadDir(versionDirectory)
	if err != nil {
		return fmt.Errorf("read asset version cache: %w", err)
	}
	prefix := "." + assetHash + ".tmp-"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(versionDirectory, entry.Name())); err != nil {
			return fmt.Errorf("remove interrupted asset temp %q: %w", entry.Name(), err)
		}
	}
	return nil
}

func readAndVerifyArchive(ctx context.Context, source ArchiveSource, asset Asset) ([]byte, error) {
	reader, err := source.Open(ctx, asset)
	if err != nil {
		return nil, fmt.Errorf("open asset archive: %w", err)
	}
	contents, readErr := io.ReadAll(io.LimitReader(reader, asset.Archive.Size+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read asset archive: %w", readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close asset archive: %w", closeErr)
	}
	if got := int64(len(contents)); got != asset.Archive.Size {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrCompressedSizeMismatch, got, asset.Archive.Size)
	}
	if got := sha256Hex(contents); got != asset.Archive.SHA256 {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrCompressedHashMismatch, got, asset.Archive.SHA256)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return contents, nil
}

func writeCacheMarker(root string, resolved ResolvedAsset) error {
	marker := expectedCacheMarker(resolved)
	contents, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode asset cache marker: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(filepath.Join(root, ".flowbaton-asset.json"), contents, 0o600); err != nil {
		return fmt.Errorf("write asset cache marker: %w", err)
	}
	return nil
}

func expectedCacheMarker(resolved ResolvedAsset) cacheMarker {
	return cacheMarker{
		SchemaVersion:    cacheMarkerSchemaVersion,
		ManifestVersion:  resolved.ManifestVersion,
		HostVersion:      resolved.Asset.HostVersion,
		AssetID:          resolved.Asset.ID,
		AssetVersion:     resolved.Asset.AssetVersion,
		AssetHash:        resolved.Asset.AssetHash,
		DescriptorSHA256: descriptorSHA256(resolved.Asset),
	}
}

func descriptorSHA256(asset Asset) string {
	contents, err := json.Marshal(asset)
	if err != nil {
		panic(fmt.Sprintf("asset descriptor is not JSON serializable: %v", err))
	}
	return fmt.Sprintf("%x", sha256.Sum256(contents))
}

func acquiredAssetAt(directory string, asset Asset) AcquiredAsset {
	return AcquiredAsset{
		Directory:    directory,
		IdentityPath: filepath.Join(directory, filepath.FromSlash(asset.Identity.Path)),
	}
}

func parseManifestMode(mode string) (fs.FileMode, error) {
	parsed, err := strconv.ParseUint(mode, 8, 32)
	if err != nil || parsed == 0 || parsed > 0o777 {
		return 0, fmt.Errorf("invalid manifest mode %q", mode)
	}
	return fs.FileMode(parsed), nil
}
