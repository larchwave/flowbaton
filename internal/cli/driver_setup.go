package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// driver-setup installs the signed, version-matched platform driver published
// with the current FlowBaton release. Production setup never depends on a
// source checkout.

// DriverSetupRunner builds the driver. The build is a field so a test can
// record the invocation without running xcodebuild.
type DriverSetupRunner struct {
	Build func(ctx context.Context, platform string) error
}

func (runner DriverSetupRunner) build() func(context.Context, string) error {
	if runner.Build != nil {
		return runner.Build
	}
	return realDriverBuild
}

func realDriverBuild(ctx context.Context, platform string) error {
	_, err := acquireDriverAsset(ctx, platform)
	return err
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

func (runner DriverSetupRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	platform, code := parseDriverSetupArgs(args, stderr)
	if code != ExitOK {
		return code
	}
	if err := runner.build()(ctx, platform); err != nil {
		fmt.Fprintf(stderr, "driver-setup: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "%s driver installed\n", platform)
	return ExitOK
}

func parseDriverSetupArgs(args []string, stderr io.Writer) (platform string, code int) {
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
				return "", ExitInvalid
			}
			platform = value
		case strings.HasPrefix(arg, "--platform="):
			platform = strings.TrimPrefix(arg, "--platform=")
		default:
			fmt.Fprintf(stderr, "driver-setup: unexpected argument %q\n", arg)
			return "", ExitInvalid
		}
	}

	switch platform {
	case "ios", "android":
	default:
		fmt.Fprintf(stderr, "driver-setup: unknown platform %q (supported: ios, android)\n", platform)
		return "", ExitInvalid
	}
	return platform, ExitOK
}
