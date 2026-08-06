//go:build windows

package ios

import "os/exec"

func configureManagedRunnerCommand(_ *exec.Cmd) {}

func stopManagedRunnerCommand(cmd *exec.Cmd, _ <-chan error) error {
	return cmd.Process.Kill()
}
