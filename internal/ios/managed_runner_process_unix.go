//go:build !windows

package ios

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureManagedRunnerCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stopManagedRunnerCommand(cmd *exec.Cmd, done <-chan error) error {
	// Negative pid means the group. SIGINT lets xcodebuild tear the test
	// session down and leave the simulator usable.
	// The child can die between the caller's check and this signal, and a
	// process group that is already gone has done what the signal asked for.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil &&
		!errors.Is(err, syscall.ESRCH) {
		return err
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return nil
}
