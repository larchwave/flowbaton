package assets

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ensureSecureDirectoryPath inspects every existing path component with
// Lstat. It never accepts a symlink component. When create is true, missing
// components are created one at a time only after their parent passed the
// no-link check; when false, a missing component reports exists=false.
func ensureSecureDirectoryPath(path string, create bool) (exists bool, returnErr error) {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return false, fmt.Errorf("resolve cache path: %w", err)
	}
	chain := directoryChain(absolute)
	for index, component := range chain {
		info, err := os.Lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return false, nil
			}
			if index == 0 {
				return false, fmt.Errorf("create filesystem root %q: %w", component, err)
			}
			if err := os.Mkdir(component, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return false, fmt.Errorf("create cache path component %q: %w", component, err)
			}
			info, err = os.Lstat(component)
		}
		if err != nil {
			return false, fmt.Errorf("inspect cache path component %q: %w", component, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return false, fmt.Errorf("%w: cache path component %q is not a real directory", ErrInvalidAssetCache, component)
		}
	}
	return true, nil
}

func directoryChain(path string) []string {
	reversed := make([]string, 0, 8)
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		reversed = append(reversed, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	chain := make([]string, len(reversed))
	for index := range reversed {
		chain[len(reversed)-1-index] = reversed[index]
	}
	return chain
}
