package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nohavewho/flowbaton/internal/android"
)

// bugreport collects a device diagnostic bundle (spec 03). Android is an adb
// bugreport zip; iOS is a `simctl diagnose` archive — a different artifact, so
// each platform has its own collector behind an injectable field.

const defaultBugreportOutput = "bugreport.zip"

// BugreportRunner holds the collection and serial resolution behind fields so a
// test can record the call without a device. The defaults reach adb / simctl.
type BugreportRunner struct {
	Collect       func(ctx context.Context, serial, outputPath string) error
	CollectIOS    func(ctx context.Context, udid, outputPath string) error
	ResolveSerial func() (string, error)
}

func (runner BugreportRunner) collect() func(context.Context, string, string) error {
	if runner.Collect != nil {
		return runner.Collect
	}
	return func(ctx context.Context, serial, outputPath string) error {
		return android.NewAdb(serial, nil).Bugreport(ctx, outputPath)
	}
}

func (runner BugreportRunner) collectIOS() func(context.Context, string, string) error {
	if runner.CollectIOS != nil {
		return runner.CollectIOS
	}
	return realIOSDiagnose
}

func (runner BugreportRunner) resolveSerial() func() (string, error) {
	if runner.ResolveSerial != nil {
		return runner.ResolveSerial
	}
	return resolveAndroidSerial
}

func (runner BugreportRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	platform, udid, output, code := parseBugreportArgs(args, stderr)
	if code != ExitOK {
		return code
	}

	if platform == "ios" {
		return runner.runIOS(ctx, udid, output, stdout, stderr)
	}

	serial := udid
	if serial == "" {
		resolved, err := runner.resolveSerial()()
		if err != nil {
			fmt.Fprintf(stderr, "bugreport: %v\n", err)
			return ExitFailure
		}
		serial = resolved
	}

	if err := runner.collect()(ctx, serial, output); err != nil {
		fmt.Fprintf(stderr, "bugreport: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "wrote %s\n", output)
	return ExitOK
}

// runIOS collects a simulator diagnostic. Unlike Android, iOS cannot resolve a
// device from an inventory (`simctl list` is not authoritative for "the one
// attached"), so the udid must be named.
func (runner BugreportRunner) runIOS(ctx context.Context, udid, output string, stdout, stderr io.Writer) int {
	if udid == "" {
		fmt.Fprintln(stderr, "bugreport: a simulator udid is required for ios: pass --device <udid>")
		return ExitInvalid
	}
	if err := runner.collectIOS()(ctx, udid, output); err != nil {
		fmt.Fprintf(stderr, "bugreport: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "wrote %s\n", output)
	return ExitOK
}

func parseBugreportArgs(args []string, stderr io.Writer) (platform, udid, output string, code int) {
	output = defaultBugreportOutput
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "bugreport: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "-p" || arg == "--platform":
			value, ok := needsValue()
			if !ok {
				return "", "", "", ExitInvalid
			}
			platform = value
		case strings.HasPrefix(arg, "--platform="):
			platform = strings.TrimPrefix(arg, "--platform=")
		case arg == "--device" || arg == "--udid":
			value, ok := needsValue()
			if !ok {
				return "", "", "", ExitInvalid
			}
			udid = value
		case strings.HasPrefix(arg, "--device="):
			udid = strings.TrimPrefix(arg, "--device=")
		case strings.HasPrefix(arg, "--udid="):
			udid = strings.TrimPrefix(arg, "--udid=")
		case arg == "--output" || arg == "-o":
			value, ok := needsValue()
			if !ok {
				return "", "", "", ExitInvalid
			}
			output = value
		case strings.HasPrefix(arg, "--output="):
			output = strings.TrimPrefix(arg, "--output=")
		default:
			fmt.Fprintf(stderr, "bugreport: unexpected argument %q\n", arg)
			return "", "", "", ExitInvalid
		}
	}

	switch platform {
	case "android", "ios":
	case "":
		fmt.Fprintln(stderr, "bugreport: a platform is required: pass -p android or -p ios")
		return "", "", "", ExitInvalid
	default:
		fmt.Fprintf(stderr, "bugreport: unknown platform %q (want android or ios)\n", platform)
		return "", "", "", ExitInvalid
	}
	return platform, udid, output, ExitOK
}
