package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// driver-setup builds a platform driver (spec 03). iOS is the default: a
// simulator build needs no signing, and --apple-team-id is passed through as
// DEVELOPMENT_TEAM for device builds. Android builds the agent with Gradle and
// installs its two APKs where runs discover them (android_agent_apks.go).

const (
	iosProjectGenerator        = "xcodegen"
	iosProjectGeneratorVersion = "2.44.1"
	iosRunnerSpec              = "drivers/ios/project.yml"
	iosRunnerProject           = "drivers/ios/FlowBatonIOSRunner.xcodeproj"
	iosRunnerScheme            = "FlowBatonIOSRunnerUITests"

	androidAgentGradleDirectory = "drivers/android"
	androidAgentBuiltApp        = "agent/build/outputs/apk/debug/agent-debug.apk"
	androidAgentBuiltTest       = "agent/build/outputs/apk/androidTest/debug/agent-debug-androidTest.apk"
)

// DriverSetupRunner builds the driver. The build is a field so a test can
// record the invocation without running xcodebuild.
type DriverSetupRunner struct {
	Build func(ctx context.Context, platform, teamID string) error
}

func (runner DriverSetupRunner) build() func(context.Context, string, string) error {
	if runner.Build != nil {
		return runner.Build
	}
	return realDriverBuild
}

func realDriverBuild(ctx context.Context, platform, teamID string) error {
	if platform == "android" {
		return realAndroidDriverBuild(ctx)
	}
	return realIOSDriverBuild(ctx, platform, teamID)
}

// realAndroidDriverBuild runs the agent's Gradle build and installs the pair it
// produced. --no-daemon because this is a one-shot from a CLI, not a session.
func realAndroidDriverBuild(ctx context.Context) error {
	// Resolved before Gradle runs, because Gradle's own "SDK location not
	// found" line is the first thing the tail we keep scrolls away.
	sdk, err := androidSDKPath()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "./gradlew", "--no-daemon",
		":agent:assembleDebug", ":agent:assembleDebugAndroidTest")
	command.Dir = androidAgentGradleDirectory
	command.Env = append(os.Environ(), "ANDROID_HOME="+sdk)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("gradlew: %w: %s", err, tailOf(output))
	}
	return installAndroidAgent(
		filepath.Join(androidAgentGradleDirectory, filepath.FromSlash(androidAgentBuiltApp)),
		filepath.Join(androidAgentGradleDirectory, filepath.FromSlash(androidAgentBuiltTest)))
}

// installAndroidAgent copies the built pair to the fixed directory the run
// reads. Both are copied or neither is: a half-written directory is refused by
// androidAgentAPKs, and leaving one behind would strand the operator there.
func installAndroidAgent(builtApp, builtTest string) error {
	directory, err := androidAgentPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	// Read both BEFORE writing either: a missing second output would otherwise
	// leave the first one installed beside a stale partner.
	app, err := os.ReadFile(builtApp)
	if err != nil {
		return fmt.Errorf("the Gradle build left no %s: %w", filepath.Base(builtApp), err)
	}
	test, err := os.ReadFile(builtTest)
	if err != nil {
		return fmt.Errorf("the Gradle build left no %s: %w", filepath.Base(builtTest), err)
	}
	if err := os.WriteFile(filepath.Join(directory, androidAppAPKName), app, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, androidTestAPKName), test, 0o600)
}

// buildOutputTail preserves the diagnostic portion of failed build output.
const buildOutputTail = 2000

func tailOf(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) > buildOutputTail {
		trimmed = trimmed[len(trimmed)-buildOutputTail:]
	}
	return trimmed
}

// realIOSDriverBuild builds the runner for the simulator via build-for-testing,
// which produces the runner app and .xctestrun the host drives. A team id, when
// given, enables a signed device build.
func realIOSDriverBuild(ctx context.Context, _ string, teamID string) error {
	if err := generateIOSProject(ctx, exec.LookPath, runBuildCommand); err != nil {
		return err
	}
	return buildIOSProject(ctx, teamID, runBuildCommand)
}

type executableFinder func(string) (string, error)
type buildCommandRunner func(context.Context, string, ...string) ([]byte, error)

func runBuildCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func generateIOSProject(
	ctx context.Context,
	find executableFinder,
	run buildCommandRunner,
) error {
	generator, err := find(iosProjectGenerator)
	if err != nil {
		return fmt.Errorf(
			"xcodegen %s is required to build the iOS driver; install that release and ensure it is on PATH: %w",
			iosProjectGeneratorVersion,
			err,
		)
	}
	versionOutput, err := run(ctx, generator, "--version")
	if err != nil {
		return fmt.Errorf("xcodegen --version: %w: %s", err, tailOf(versionOutput))
	}
	version := strings.TrimSpace(strings.TrimPrefix(
		strings.TrimSpace(string(versionOutput)), "Version:"))
	if version != iosProjectGeneratorVersion {
		return fmt.Errorf(
			"xcodegen %s is required for deterministic project generation; found %q; install the required release and ensure it is first on PATH",
			iosProjectGeneratorVersion,
			version,
		)
	}
	output, err := run(ctx, generator,
		"generate", "--spec", iosRunnerSpec, "--project", filepath.Dir(iosRunnerSpec))
	if err != nil {
		return fmt.Errorf("xcodegen: %w: %s", err, tailOf(output))
	}
	return nil
}

func buildIOSProject(ctx context.Context, teamID string, run buildCommandRunner) error {
	derived, err := iosDerivedDataPath()
	if err != nil {
		return err
	}
	// A FIXED derived-data path, not xcodebuild's default hashed one: the run
	// that starts the runner is a different process and has to find what this
	// built. See ios_runner_bundle.go.
	args := []string{
		"-project", iosRunnerProject,
		"-scheme", iosRunnerScheme,
		"-destination", "generic/platform=iOS Simulator",
		"-derivedDataPath", derived,
		"build-for-testing",
	}
	if teamID != "" {
		args = append(args, "DEVELOPMENT_TEAM="+teamID)
	}
	output, err := run(ctx, "xcodebuild", args...)
	if err != nil {
		return fmt.Errorf("xcodebuild: %w: %s", err, tailOf(output))
	}
	return nil
}

func (runner DriverSetupRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	platform, teamID, code := parseDriverSetupArgs(args, stderr)
	if code != ExitOK {
		return code
	}
	if err := runner.build()(ctx, platform, teamID); err != nil {
		fmt.Fprintf(stderr, "driver-setup: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "%s driver built\n", platform)
	return ExitOK
}

func parseDriverSetupArgs(args []string, stderr io.Writer) (platform, teamID string, code int) {
	platform = "ios"
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "driver-setup: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "-p" || arg == "--platform":
			value, ok := needsValue()
			if !ok {
				return "", "", ExitInvalid
			}
			platform = value
		case strings.HasPrefix(arg, "--platform="):
			platform = strings.TrimPrefix(arg, "--platform=")
		case arg == "--apple-team-id":
			value, ok := needsValue()
			if !ok {
				return "", "", ExitInvalid
			}
			teamID = value
		case strings.HasPrefix(arg, "--apple-team-id="):
			teamID = strings.TrimPrefix(arg, "--apple-team-id=")
		default:
			fmt.Fprintf(stderr, "driver-setup: unexpected argument %q\n", arg)
			return "", "", ExitInvalid
		}
	}

	switch platform {
	case "ios":
	case "android":
		// Signing is an iOS detail; accepting the flag here would promise a
		// signing step the Gradle build does not have.
		if teamID != "" {
			fmt.Fprintln(stderr, "driver-setup: --apple-team-id is an iOS signing detail and does not apply to android")
			return "", "", ExitInvalid
		}
	default:
		fmt.Fprintf(stderr, "driver-setup: unknown platform %q (supported: ios, android)\n", platform)
		return "", "", ExitInvalid
	}
	return platform, teamID, ExitOK
}
