package capability

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/model"
)

// ErrFlowDirectory identifies a path that resolves to a directory rather than
// a file resource.
var ErrFlowDirectory = errors.New("flow link is a directory")

// ErrFlowNonRegular identifies a path that resolves to a FIFO, socket, device,
// or another non-regular resource that must never be opened during preflight.
var ErrFlowNonRegular = errors.New("flow link is not a regular file")

// FileLoader is the default read-only filesystem/parser adapter.
type FileLoader struct{}

// Canonical returns an absolute, symlink-resolved file identity.
func (FileLoader) Canonical(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", ErrFlowDirectory
	}
	if !info.Mode().IsRegular() {
		return "", ErrFlowNonRegular
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

// Load parses one already canonicalized flow file.
func (FileLoader) Load(ctx context.Context, canonicalPath string) (model.Flow, error) {
	if err := ctx.Err(); err != nil {
		return model.Flow{}, err
	}
	file, err := os.Open(canonicalPath)
	if err != nil {
		return model.Flow{}, err
	}
	defer file.Close()
	return flow.Parse(canonicalPath, file)
}
