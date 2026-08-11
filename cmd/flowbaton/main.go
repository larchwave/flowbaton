package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	flowcli "github.com/larchwave/flowbaton/internal/cli"
	"github.com/larchwave/flowbaton/internal/version"
)

const topLevelUsage = "usage: flowbaton --version | check-syntax FILE|- | test [options] FILE|DIR... | record [--local] [options] FLOW [OUTPUT] | explore --app ID -p ios|android|web [options] | list-devices [-p ios|android|web] | start-device -p ios|android --device ID | hierarchy -p ios|android [--device ID] [--app-id ID] [--csv|--compact] [--target devtools] | query -p ios|android [--device ID] [--app-id ID] EXPRESSION | bugreport -p ios|android [--device ID] [--output PATH] | driver-setup [-p ios|android] | mcp [--base-dir DIR] [--no-viewer] [--viewer-port PORT] | serve [options] | db apply-schema --database-url URL | auth keygen|cert-map [options] | generate-completion [bash|zsh]\n"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runWithContext(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithContext(context.Background(), args, stdin, stdout, stderr)
}

func runWithContext(
	ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer,
) int {
	return runWithCheckerContext(ctx, args, stdin, stdout, stderr, flowcli.NewParserChecker(), os.Getwd)
}

func runWithChecker(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	checker flowcli.Checker,
	getwd func() (string, error),
) int {
	return runWithCheckerContext(
		context.Background(), args, stdin, stdout, stderr, checker, getwd,
	)
}

func runWithCheckerContext(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	checker flowcli.Checker,
	getwd func() (string, error),
) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Fprintln(stdout, version.Line())
		return flowcli.ExitOK
	}
	if len(args) > 0 && args[0] == "check-syntax" {
		return (flowcli.CheckSyntaxRunner{Checker: checker, Getwd: getwd}).Run(
			ctx,
			args[1:],
			stdin,
			stdout,
			stderr,
		)
	}
	if len(args) > 0 && args[0] == "test" {
		// TestRunner falls back to NewDeviceSession when NewSession is unset.
		// `test` therefore drives an iOS Simulator or Android adb device, and a
		// platform without a driver is refused at the device boundary.
		return flowcli.TestRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "record" {
		return flowcli.RecordRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "explore" {
		// The zero ExploreRunner refuses with a typed error until the model
		// and crew constructors are wired; flags are still validated first,
		// so the command line surface is stable ahead of the assembly.
		return flowcli.ExploreRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "list-devices" {
		return flowcli.ListDevicesRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "start-device" {
		return flowcli.StartDeviceRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "hierarchy" {
		return flowcli.HierarchyRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "query" {
		return flowcli.QueryRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "bugreport" {
		return flowcli.BugreportRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "driver-setup" {
		return flowcli.DriverSetupRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "mcp" {
		return flowcli.MCPRunner{Checker: checker}.Run(ctx, args[1:], stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "serve" {
		return flowcli.DefaultServeRunner().Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "db" {
		return flowcli.DBRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "auth" {
		return flowcli.AuthRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "generate-completion" {
		return flowcli.GenerateCompletionRunner{}.Run(ctx, args[1:], stdout, stderr)
	}
	_, _ = io.WriteString(stderr, topLevelUsage)
	return flowcli.ExitInvalid
}
