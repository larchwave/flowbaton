//go:build !windows

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCheckSyntaxRejectsFIFOAsNonRegularInput(t *testing.T) {
	directory := t.TempDir()
	fifo := filepath.Join(directory, "flow.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("create FIFO: %v", err)
	}
	keeper, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("open FIFO keeper: %v", err)
	}
	defer keeper.Close()

	type result struct {
		exit   int
		stdout string
		stderr string
	}
	done := make(chan result, 1)
	go func() {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		exit := (CheckSyntaxRunner{
			Checker: NewParserChecker(),
			Getwd:   func() (string, error) { return directory, nil },
		}).Run(context.Background(), []string{fifo}, bytes.NewReader(nil), &stdout, &stderr)
		done <- result{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}()

	select {
	case got := <-done:
		if got.exit != ExitInvalid || got.stdout != "" {
			t.Fatalf("exit/stdout = %d/%q, want %d/empty", got.exit, got.stdout, ExitInvalid)
		}
		want := fifo + ": input_non_regular: flow path is not a regular file\n"
		if got.stderr != want {
			t.Fatalf("stderr = %q, want %q", got.stderr, want)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("check-syntax blocked while reading a FIFO instead of rejecting it before read")
	}
}
