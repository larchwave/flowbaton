package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The `test` subcommand's option surface, from specs/03-cli-tooling.md:20 and
// the pinned flag list in internal/capability/registry_catalog.go.
//
// The exit-code contract is defined in test_options_test.go: a command line
// that could not be understood exits 2, and anything
// that fails once it has been understood exits 1. The mutually-exclusive shard
// pair sits on the far side of that line — the contract parses it happily and
// then refuses.

// ExitFailure is the exit code for a run that was understood and did not pass.
const ExitFailure = 1

// reportFormats are the four values --format accepts, in diagnostic order.
var reportFormats = []string{"JUNIT", "HTML", "HTML-DETAILED", "NOOP"}

// TestOptions is one parsed `test` command line.
type TestOptions struct {
	Roots []string

	ConfigPath  string
	Env         map[string]string
	IncludeTags []string
	ExcludeTags []string

	Format        string
	TestSuiteName string
	Output        string

	DebugOutput        string
	TestOutputDir      string
	FlattenDebugOutput bool
	Continuous         bool
	Headless           bool
	Analyze            bool
	ReinstallDriver    bool
	ScreenSize         string
	APIURL             string
	APIKey             string
	Platform           string
	Devices            []string
	ShardSplit         int
	ShardAll           int

	// RecordTo is where `record` wants the video. It is NOT parsed from a
	// command line: the `test` command has no such flag. RecordRunner sets it.
	RecordTo string

	// SequencedRoots and ContinueOnFailure come from the workspace's
	// executionOrder, not from argv. They decide whether a failed flow ends
	// the suite; see engine.Dependencies for the rule.
	SequencedRoots    int
	ContinueOnFailure bool

	// attachedDevices lists what is plugged in, for a sharded run that named
	// no devices. A field so a test can shard without an emulator; nil reaches
	// the real adb and simctl listings. Not parsed from argv, and unexported
	// so it cannot become part of the documented surface.
	attachedDevices func(ctx context.Context, platform string) ([]string, error)
}

// UsageError marks a command line that could not be understood. It is the only
// thing that exits 2; everything else exits 1.
type UsageError struct {
	Message string
}

func (err *UsageError) Error() string { return err.Message }

func usageErrorf(format string, arguments ...any) error {
	return &UsageError{Message: fmt.Sprintf(format, arguments...)}
}

// ExitCodeFor classifies an error into the documented exit codes. Anything not
// explicitly a usage error is a failure: a misclassification that returned OK
// would report a green suite that never ran.
func ExitCodeFor(err error) int {
	if err == nil {
		return ExitOK
	}
	var usage *UsageError
	if errors.As(err, &usage) {
		return ExitInvalid
	}
	return ExitFailure
}

// ParseTestOptions reads a `test` command line.
//
// Flags and positionals may interleave. Boolean flags never consume the
// following argument —
// doing so would silently drop a flow from the run, which is the worst way for
// a suite to pass.
func ParseTestOptions(args []string) (TestOptions, error) {
	options := TestOptions{
		Format:          "NOOP",
		Env:             map[string]string{},
		ReinstallDriver: true,
	}

	booleans := map[string]*bool{
		"--flatten-debug-output": &options.FlattenDebugOutput,
		"--continuous":           &options.Continuous,
		"-c":                     &options.Continuous,
		"--headless":             &options.Headless,
		"--analyze":              &options.Analyze,
	}
	values := map[string]*string{
		"--config":          &options.ConfigPath,
		"--format":          &options.Format,
		"--test-suite-name": &options.TestSuiteName,
		"--output":          &options.Output,
		"--debug-output":    &options.DebugOutput,
		"--test-output-dir": &options.TestOutputDir,
		"--screen-size":     &options.ScreenSize,
		"--api-url":         &options.APIURL,
		"--api-key":         &options.APIKey,
		"--platform":        &options.Platform,
		"-p":                &options.Platform,
	}

	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "" || !strings.HasPrefix(argument, "-") || argument == "-" {
			options.Roots = append(options.Roots, argument)
			continue
		}
		if target, ok := booleans[argument]; ok {
			*target = true
			continue
		}
		switch argument {
		case "--reinstall-driver":
			options.ReinstallDriver = true
			continue
		case "--no-reinstall-driver":
			options.ReinstallDriver = false
			continue
		}

		value, consumed, err := nextValue(args, index)
		if err != nil {
			return TestOptions{}, err
		}
		if argument == "--apple-team-id" {
			return TestOptions{}, usageErrorf(
				"option --apple-team-id belongs to flowbaton driver-setup, not flowbaton test")
		}

		if target, ok := values[argument]; ok {
			*target = value
			index += consumed
			continue
		}
		switch argument {
		case "-e", "--env":
			key, entry, err := splitEnvironmentEntry(value)
			if err != nil {
				return TestOptions{}, err
			}
			options.Env[key] = entry
		case "--include-tags":
			options.IncludeTags = splitList(value)
		case "--exclude-tags":
			options.ExcludeTags = splitList(value)
		case "--device", "--udid":
			options.Devices = splitList(value)
		case "--shard-split":
			options.ShardSplit, err = positiveCount(argument, value)
			if err != nil {
				return TestOptions{}, err
			}
		case "--shard-all":
			options.ShardAll, err = positiveCount(argument, value)
			if err != nil {
				return TestOptions{}, err
			}
		default:
			return TestOptions{}, usageErrorf("unknown option %q for flowbaton test", argument)
		}
		index += consumed
	}

	return options, validateTestOptions(options)
}

// nextValue returns the value following a flag. A flag that needs a value and
// has none must not fall back to a default: that would run a different suite
// than the operator asked for, silently.
func nextValue(args []string, index int) (string, int, error) {
	if index+1 >= len(args) {
		return "", 0, usageErrorf("option %q requires a value", args[index])
	}
	return args[index+1], 1, nil
}

func validateTestOptions(options TestOptions) error {
	if len(options.Roots) == 0 {
		return usageErrorf("flowbaton test requires at least one flow file or directory")
	}
	if !isReportFormat(options.Format) {
		return usageErrorf(
			"invalid value %q for option --format; valid options are: %s",
			options.Format, strings.Join(reportFormats, ", "))
	}
	// specs/03-cli-tooling.md:47 requires insights as HTML plus ai-(flow).json.
	// Refuse the flag before execution until a local writer implements that
	// output contract.
	if options.Analyze {
		return errors.New(
			"--analyze is not implemented: it would need the AI insights report " +
				"(specs/03-cli-tooling.md:47), and the contract's cloud route to it is " +
				"out of scope (specs/03-cli-tooling.md:50)")
	}
	// Expected: this pair parses and is then refused at
	// runtime, so it exits 1 rather than 2. The command line was well formed;
	// the combination was not.
	if options.ShardSplit > 0 && options.ShardAll > 0 {
		return errors.New("options --shard-split and --shard-all are mutually exclusive")
	}
	return nil
}

func isReportFormat(value string) bool {
	for _, format := range reportFormats {
		if strings.EqualFold(value, format) {
			return true
		}
	}
	return false
}

// splitEnvironmentEntry splits on the FIRST '=' only, so a value may contain
// its own — a URL with a query string is the common case.
func splitEnvironmentEntry(entry string) (string, string, error) {
	key, value, found := strings.Cut(entry, "=")
	if !found || strings.TrimSpace(key) == "" {
		return "", "", usageErrorf(
			"value for option --env should be in KEY=VALUE format but was %q", entry)
	}
	return key, value, nil
}

// positiveCount refuses a non-positive shard count.
//
// Reject zero before shard arithmetic to avoid division by zero.
func positiveCount(flag, value string) (int, error) {
	count, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, usageErrorf("option %s requires a number, got %q", flag, value)
	}
	if count <= 0 {
		return 0, usageErrorf("option %s requires a positive count, got %d", flag, count)
	}
	return count, nil
}

// splitList splits a comma list, dropping blanks. A stray comma should not
// become an empty tag that matches nothing and filters everything out.
func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}
