package assets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// CommandIdentityVerifier performs the platform-native identity checks after
// archive hashes and paths have been verified. Android uses apkanalyzer's
// manifest parser; iOS uses codesign's designated bundle identifier.
type CommandIdentityVerifier struct {
	Run CommandRunner
}

func (verifier CommandIdentityVerifier) Verify(ctx context.Context, candidate VerificationCandidate) error {
	run := verifier.Run
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		}
	}
	switch candidate.Kind {
	case VerificationPackageIdentity:
		command := "apkanalyzer"
		if verifier.Run == nil {
			var err error
			command, err = androidAnalyzerPath()
			if err != nil {
				return err
			}
		}
		output, err := run(ctx, command, "manifest", "application-id", candidate.Path)
		if err != nil {
			return fmt.Errorf("apkanalyzer package identity: %w: %s", err, strings.TrimSpace(string(output)))
		}
		if got := strings.TrimSpace(string(output)); got != candidate.Identity {
			return fmt.Errorf("Android package identity = %q, want %q", got, candidate.Identity)
		}
		return nil
	case VerificationBundleSignatureIdentity:
		output, err := run(ctx, "codesign", "-dv", "--verbose=4", candidate.Path)
		if err != nil {
			return fmt.Errorf("codesign bundle identity: %w: %s", err, strings.TrimSpace(string(output)))
		}
		identifier := codesignIdentifier(string(output))
		if identifier != candidate.Identity {
			return fmt.Errorf("iOS bundle identity = %q, want %q", identifier, candidate.Identity)
		}
		return nil
	default:
		return fmt.Errorf("unsupported identity verification kind %q", candidate.Kind)
	}
}

func androidAnalyzerPath() (string, error) {
	if command, err := exec.LookPath("apkanalyzer"); err == nil {
		return command, nil
	}
	names := []string{"apkanalyzer"}
	if runtime.GOOS == "windows" {
		names = append([]string{"apkanalyzer.bat"}, names...)
	}
	var candidates []string
	for _, variable := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		root := strings.TrimSpace(os.Getenv(variable))
		if root == "" {
			continue
		}
		for _, name := range names {
			candidates = append(candidates, filepath.Join(root, "cmdline-tools", "latest", "bin", name))
			matches, _ := filepath.Glob(filepath.Join(root, "cmdline-tools", "*", "bin", name))
			sort.Sort(sort.Reverse(sort.StringSlice(matches)))
			candidates = append(candidates, matches...)
			candidates = append(candidates, filepath.Join(root, "tools", "bin", name))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return "", errors.New("apkanalyzer is required to verify the Android driver package identity; install Android SDK command-line tools or put apkanalyzer on PATH")
}

func codesignIdentifier(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "Identifier="); found {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
