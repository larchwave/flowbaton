package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// realIOSDiagnose collects simulator diagnostics into outputPath via
// `xcrun simctl diagnose`. The call stays local to the CLI runner (mirroring
// realIOSDriverBuild's xcodebuild wrap) so bugreport needs no device driver.
//
// -b suppresses the Finder reveal so the command stays headless; -X drops the
// per-diagnostic timeout so a slow collection completes instead of racing the
// default; --no-archive fills the --output directory with the collected files
// (without it simctl writes a sibling <output>.tar.gz, leaving --output empty).
//
// simctl diagnose prints a privacy notice and blocks on "Press ENTER to
// continue"; with no stdin it reads EOF and exits 0 having collected NOTHING
// (an empty output directory). Feeding a newline accepts that prompt — the
// consent is for local collection, not any upload — so it actually runs.
func realIOSDiagnose(ctx context.Context, udid, outputPath string) error {
	args := []string{
		"simctl", "diagnose",
		"-b", "-X", "--no-archive",
		"--udid=" + udid,
		"--output=" + outputPath,
	}
	cmd := exec.CommandContext(ctx, "xcrun", args...)
	cmd.Stdin = strings.NewReader("\n")
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(output))
		if len(trimmed) > 600 {
			trimmed = trimmed[len(trimmed)-600:]
		}
		return fmt.Errorf("simctl diagnose: %w: %s", err, trimmed)
	}
	return nil
}
