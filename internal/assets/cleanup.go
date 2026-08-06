package assets

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// RetentionPolicy keeps the selected asset hashes for the active host version
// and the complete immediately previous host version. Other directories are
// removed only when their cache marker proves Flowbaton ownership.
type RetentionPolicy struct {
	ActiveHostVersion   string
	ActiveAssetHashes   []string
	PreviousHostVersion string
}

func (m Manager) Cleanup(ctx context.Context, policy RetentionPolicy) error {
	if m.CacheRoot == "" {
		return ErrCacheRootRequired
	}
	activeHashes, err := validateRetentionPolicy(policy)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	exists, err := ensureSecureDirectoryPath(m.CacheRoot, false)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	versions, err := os.ReadDir(m.CacheRoot)
	if err != nil {
		return fmt.Errorf("read asset cache root: %w", err)
	}
	for _, versionEntry := range versions {
		if err := ctx.Err(); err != nil {
			return err
		}
		version := versionEntry.Name()
		versionPath := filepath.Join(m.CacheRoot, version)
		info, err := os.Lstat(versionPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return fmt.Errorf("inspect cached host version %q: %w", version, err)
		}
		// Never traverse a link or an unrecognized entry under the owned root.
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || !safeToken(version) {
			continue
		}
		if version == policy.PreviousHostVersion {
			continue
		}
		lockDirectory := filepath.Join(versionPath, ".locks")
		if err := ensureDirectDirectory(lockDirectory); err != nil {
			// A link or other non-directory at .locks is an untrusted boundary.
			// Preserve the version rather than following or replacing it.
			continue
		}
		entries, err := os.ReadDir(versionPath)
		if err != nil {
			return fmt.Errorf("read cached host version %q: %w", version, err)
		}
		for _, entry := range entries {
			hash := entry.Name()
			if !canonicalHash(hash) {
				continue
			}
			if version == policy.ActiveHostVersion {
				if _, keep := activeHashes[hash]; keep {
					continue
				}
			}
			assetPath := filepath.Join(versionPath, hash)
			if !ownedCacheDirectory(assetPath, version, hash) {
				continue
			}
			if err := m.removeOwnedAssetUnderLock(ctx, lockDirectory, assetPath, version, hash); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRetentionPolicy(policy RetentionPolicy) (map[string]struct{}, error) {
	if !safeToken(policy.ActiveHostVersion) {
		return nil, fmt.Errorf("%w: unsafe active host version", ErrInvalidAssetCache)
	}
	if policy.PreviousHostVersion != "" && !safeToken(policy.PreviousHostVersion) {
		return nil, fmt.Errorf("%w: unsafe previous host version", ErrInvalidAssetCache)
	}
	if policy.PreviousHostVersion == policy.ActiveHostVersion {
		return nil, fmt.Errorf("%w: active and previous host versions must differ", ErrInvalidAssetCache)
	}
	active := make(map[string]struct{}, len(policy.ActiveAssetHashes))
	for _, hash := range policy.ActiveAssetHashes {
		if !canonicalHash(hash) {
			return nil, fmt.Errorf("%w: invalid active asset hash %q", ErrInvalidAssetCache, hash)
		}
		if _, duplicate := active[hash]; duplicate {
			return nil, fmt.Errorf("%w: duplicate active asset hash %q", ErrInvalidAssetCache, hash)
		}
		active[hash] = struct{}{}
	}
	return active, nil
}

func ownedCacheDirectory(assetPath, hostVersion, assetHash string) bool {
	info, err := os.Lstat(assetPath)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	marker, err := readCacheMarker(assetPath)
	if err != nil {
		return false
	}
	return marker.SchemaVersion == cacheMarkerSchemaVersion &&
		marker.HostVersion == hostVersion &&
		marker.AssetHash == assetHash &&
		safeToken(marker.AssetID) &&
		safeToken(marker.AssetVersion) &&
		canonicalHash(marker.DescriptorSHA256)
}

func (m Manager) removeOwnedAssetUnderLock(ctx context.Context, lockDirectory, assetPath, hostVersion, assetHash string) error {
	locker := m.Locker
	if locker == nil {
		locker = FileLocker{}
	}
	unlocker, err := locker.Lock(ctx, filepath.Join(lockDirectory, assetHash+".lock"))
	if err != nil {
		return err
	}
	if !ownedCacheDirectory(assetPath, hostVersion, assetHash) {
		return unlocker.Unlock()
	}
	removeErr := os.RemoveAll(assetPath)
	unlockErr := unlocker.Unlock()
	if removeErr != nil {
		return fmt.Errorf("remove owned stale asset %q: %w", assetPath, removeErr)
	}
	if unlockErr != nil {
		return unlockErr
	}
	return nil
}
