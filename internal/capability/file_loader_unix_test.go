//go:build !windows

package capability

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFileLoaderRejectsNonRegularPathBeforeOpen(t *testing.T) {
	t.Parallel()

	pipe := filepath.Join(t.TempDir(), "flow.pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	_, err := (FileLoader{}).Canonical(context.Background(), pipe)
	if !errors.Is(err, ErrFlowNonRegular) {
		t.Fatalf("Canonical FIFO error = %v, want ErrFlowNonRegular", err)
	}
}
