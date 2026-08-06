package assets

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

const lockPollInterval = 10 * time.Millisecond

type Unlocker interface {
	Unlock() error
}

type Locker interface {
	Lock(context.Context, string) (Unlocker, error)
}

// FileLocker uses an operating-system advisory file lock. The lock is released
// by the kernel if the owning process terminates, so interrupted acquisitions
// cannot leave a permanent lock behind.
type FileLocker struct{}

func (FileLocker) Lock(ctx context.Context, path string) (Unlocker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := openLockFileNoLinks(path)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(lockPollInterval)
	defer ticker.Stop()
	for {
		locked, lockErr := tryLockFile(file)
		if lockErr != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock asset: %w", lockErr)
		}
		if locked {
			return &fileUnlocker{file: file}, nil
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func openLockFileNoLinks(path string) (*os.File, error) {
	for {
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			file, createErr := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
			if errors.Is(createErr, fs.ErrExist) {
				continue
			}
			if createErr != nil {
				return nil, fmt.Errorf("create asset lock: %w", createErr)
			}
			if err := verifyOpenedLockFile(path, file); err != nil {
				_ = file.Close()
				return nil, err
			}
			return file, nil
		}
		if err != nil {
			return nil, fmt.Errorf("inspect asset lock: %w", err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%w: asset lock %q is not a real regular file", ErrInvalidAssetCache, path)
		}
		file, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open asset lock: %w", err)
		}
		if err := verifyOpenedLockFile(path, file); err != nil {
			_ = file.Close()
			return nil, err
		}
		return file, nil
	}
}

func verifyOpenedLockFile(path string, file *os.File) error {
	openedInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened asset lock: %w", err)
	}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect asset lock: %w", err)
	}
	if pathInfo.Mode()&fs.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !openedInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) {
		return fmt.Errorf("%w: asset lock %q changed or is not a real regular file", ErrInvalidAssetCache, path)
	}
	return nil
}

type fileUnlocker struct {
	file *os.File
}

func (u *fileUnlocker) Unlock() error {
	if u.file == nil {
		return nil
	}
	file := u.file
	u.file = nil
	unlockErr := unlockFile(file)
	closeErr := file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock asset: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close asset lock: %w", closeErr)
	}
	return nil
}
