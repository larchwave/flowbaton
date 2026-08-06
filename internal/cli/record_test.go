package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// `record` follows the command shape declared by the v0 registry:
//
//	Usage: flowbaton record [-h] [--local] ... <flowFile> [<outputFile>]
//	  <flowFile>       The Flow file to record.
//	  [<outputFile>]   Output file for the rendered video. Only valid for
//	                   local rendering (--local).
//
// Cloud rendering is `excluded` in the registry, so local is not a mode here,
// it is the only mode — `--local` is accepted and means nothing.

func TestRecordRunsTheFlowAndLeavesAVideo(t *testing.T) {
	dir := t.TempDir()
	flow := filepath.Join(dir, "demo.yaml")
	writeFile(t, flow, "appId: com.example.a\n---\n- assertVisible: OK\n")

	working := t.TempDir()
	t.Chdir(working)

	runner := recordRunnerOn(permissiveDriver(), dir)
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{"--local", flow}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, stdout.String(), stderr.String())
	}
	// Named after the flow when the operator named no output, the way a
	// screenshot is named after what the author wrote.
	if _, err := os.Stat(filepath.Join(working, "demo.mp4")); err != nil {
		listing, _ := filepath.Glob(filepath.Join(working, "*"))
		t.Fatalf("demo.mp4 is not in the working directory: %v (has %v)", err, listing)
	}
}

func TestRecordHonorsTheOutputFileTheOperatorNamed(t *testing.T) {
	dir := t.TempDir()
	flow := filepath.Join(dir, "demo.yaml")
	writeFile(t, flow, "appId: com.example.a\n---\n- assertVisible: OK\n")

	working := t.TempDir()
	t.Chdir(working)

	runner := recordRunnerOn(permissiveDriver(), dir)
	var stdout, stderr bytes.Buffer
	if code := runner.Run(
		context.Background(), []string{flow, "demo-for-the-bug-report.mp4"}, &stdout, &stderr,
	); code != ExitOK {
		t.Fatalf("exit = %d\nstderr: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(working, "demo-for-the-bug-report.mp4")); err != nil {
		t.Fatalf("the named output file is missing: %v", err)
	}
}

// A recording of a run that failed is the recording most worth having: it is
// what goes into the bug report. The exit code still tells the truth.
func TestRecordKeepsTheVideoOfAFailingFlow(t *testing.T) {
	dir := t.TempDir()
	flow := filepath.Join(dir, "demo.yaml")
	writeFile(t, flow, "appId: com.example.a\n---\n- assertVisible: NotOnScreen\n")

	working := t.TempDir()
	t.Chdir(working)

	runner := recordRunnerOn(emptyScreenDriver(), dir)
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{flow}, &stdout, &stderr); code == ExitOK {
		t.Fatal("the flow passed; it was supposed to fail")
	}
	if _, err := os.Stat(filepath.Join(working, "demo.mp4")); err != nil {
		t.Fatalf("the recording of the failing run was lost: %v", err)
	}
}

func TestRecordNeedsExactlyOneFlow(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{}, {"--local"}, {"a.yaml", "out.mp4", "extra"}} {
		var stdout, stderr bytes.Buffer
		code := RecordRunner{}.Run(context.Background(), args, &stdout, &stderr)
		if code != ExitInvalid {
			t.Fatalf("record %v exit = %d, want %d", args, code, ExitInvalid)
		}
		if !strings.Contains(stderr.String(), "record") {
			t.Fatalf("record %v stderr = %q, want it to name the command", args, stderr.String())
		}
	}
}

// recordingDriver is a FakeDriver that can also finish a recording, which the
// FakeDriver alone cannot: frozen Driver v0 declares only the start half. It
// writes a stub file so the test can assert on where the video landed rather
// than on a call it was told happened.
type recordingDriver struct {
	*enginetest.FakeDriver
	sink string
}

func (driver *recordingDriver) StartScreenRecording(
	_ context.Context, request device.ScreenRecordingRequest,
) (device.CaptureID, error) {
	driver.sink = request.OutputPath
	return device.CaptureID(request.OutputPath), nil
}

func (driver *recordingDriver) StopScreenRecording(
	_ context.Context, id device.CaptureID,
) ([]device.Artifact, error) {
	if err := os.WriteFile(string(id), []byte("not really an mp4"), 0o644); err != nil {
		return nil, err
	}
	return []device.Artifact{{Kind: "recording", Path: string(id)}}, nil
}

func recordRunnerOn(fake *enginetest.FakeDriver, baseDirectory string) RecordRunner {
	driver := &recordingDriver{FakeDriver: fake}
	moment := time.Unix(1_700_000_000, 0).UTC()
	return RecordRunner{NewSession: func(shard Shard, _ TestOptions) (TestSession, error) {
		return DeviceSession{
			Driver:          driver,
			OutputDirectory: shard.OutputDirectory,
			BaseDirectory:   baseDirectory,
			Clock:           &advancingClock{now: moment},
			ExecutionID:     "test-execution",
		}, nil
	}}
}
