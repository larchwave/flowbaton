package main

import (
	"context"
	"fmt"
	"io"
	"os"

	flowcli "github.com/nohavewho/flowbaton/internal/cli"
	"github.com/nohavewho/flowbaton/internal/version"
)

const topLevelUsage = "usage: flowbaton --version | check-syntax FILE|- | test FILE|DIR... | record [--local] FLOW [OUTPUT] | list-devices [-p ios|android] | start-device -p ios --device UDID | hierarchy -p ios|android [--device UDID] [--csv] | query -p ios|android [--device UDID] EXPRESSION | bugreport -p android [--device SERIAL] [--output PATH] | driver-setup [--apple-team-id ID] | mcp [--no-viewer]\n"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runWithChecker(args, stdin, stdout, stderr, flowcli.NewParserChecker(), os.Getwd)
}

func runWithChecker(
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
			context.Background(),
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
		return flowcli.TestRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "record" {
		return flowcli.RecordRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "list-devices" {
		return flowcli.ListDevicesRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "start-device" {
		return flowcli.StartDeviceRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "hierarchy" {
		return flowcli.HierarchyRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "query" {
		return flowcli.QueryRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "bugreport" {
		return flowcli.BugreportRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "driver-setup" {
		return flowcli.DriverSetupRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	if len(args) > 0 && args[0] == "mcp" {
		return flowcli.MCPRunner{Checker: checker}.Run(context.Background(), args[1:], stdin, stdout, stderr)
	}
	if len(args) > 0 && args[0] == "generate-completion" {
		return flowcli.GenerateCompletionRunner{}.Run(context.Background(), args[1:], stdout, stderr)
	}
	_, _ = io.WriteString(stderr, topLevelUsage)
	return flowcli.ExitInvalid
}
