//go:build !windows

package ios

import (
	"os/exec"
	"strings"
	"testing"
	"time"
)

// A child that is already gone is not a stop failure, and stopping it is the
// one moment its exit reason is both latched and still worth printing. mmx72
// closed with "no such process" -- the errno from signalling a dead process
// group -- and never said the runner had died, which was the whole question.
func TestStoppingARunnerThatAlreadyDiedSaysWhyItDied(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "echo 'Test session exited unexpectedly' >&2; exit 3")
	output := &boundedBuffer{limit: 4096}
	cmd.Stdout, cmd.Stderr = output, output
	configureManagedRunnerCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the stand-in child: %v", err)
	}
	runner := watchRunnerChild(cmd, output)

	deadline := time.Now().Add(5 * time.Second)
	for runner.exitReason() == "" {
		if time.Now().After(deadline) {
			t.Fatal("the stand-in child never exited")
		}
		time.Sleep(10 * time.Millisecond)
	}

	err := runner.stopRunner()
	if err == nil {
		t.Fatal("stopRunner() = nil, want the reason the runner died")
	}
	if !strings.Contains(err.Error(), "Test session exited unexpectedly") {
		t.Fatalf("stopRunner() = %v, want it to carry what the child said", err)
	}
}
