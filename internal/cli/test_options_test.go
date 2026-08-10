package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The test option surface uses these exit codes:
//
//	flowbaton test                              -> 2   (no required positional)
//	flowbaton test --not-a-flag valid.yaml      -> 2
//	flowbaton test --format BOGUS valid.yaml    -> 2   (bad value for a known flag)
//	flowbaton test -e MALFORMED valid.yaml      -> 2   ("should be in KEY=VALUE format")
//	flowbaton test /nonexistent.yaml            -> 1
//	flowbaton test malformed.yaml               -> 1
//	flowbaton test --shard-split 2 --shard-all 2 valid.yaml
//	                                          -> 1   ("mutually exclusive")
//
// The line worth naming: a malformed COMMAND LINE is 2, and everything that
// fails after the command line has been understood is 1 — including the
// mutually-exclusive shard pair, which fails after parsing.

func TestParseTestOptionsAcceptsThePinnedSurface(t *testing.T) {
	t.Parallel()

	options, err := ParseTestOptions([]string{
		"--config", "cfg.yaml",
		"-e", "KEY=value", "--env", "OTHER=2",
		"--include-tags", "smoke,fast",
		"--exclude-tags", "slow",
		"--format", "JUNIT",
		"--test-suite-name", "suite",
		"--output", "report.xml",
		"--platform", "ios",
		"--device", "UDID-1,UDID-2",
		"flows/", "one.yaml",
	})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if !reflect.DeepEqual(options.Roots, []string{"flows/", "one.yaml"}) {
		t.Fatalf("Roots = %v", options.Roots)
	}
	if options.ConfigPath != "cfg.yaml" {
		t.Fatalf("ConfigPath = %q", options.ConfigPath)
	}
	wantEnv := map[string]string{"KEY": "value", "OTHER": "2"}
	if !reflect.DeepEqual(options.Env, wantEnv) {
		t.Fatalf("Env = %v, want %v", options.Env, wantEnv)
	}
	if !reflect.DeepEqual(options.IncludeTags, []string{"smoke", "fast"}) {
		t.Fatalf("IncludeTags = %v, want the comma list split", options.IncludeTags)
	}
	if !reflect.DeepEqual(options.ExcludeTags, []string{"slow"}) {
		t.Fatalf("ExcludeTags = %v", options.ExcludeTags)
	}
	if options.Format != "JUNIT" {
		t.Fatalf("Format = %q", options.Format)
	}
	if !reflect.DeepEqual(options.Devices, []string{"UDID-1", "UDID-2"}) {
		t.Fatalf("Devices = %v, want the comma list split", options.Devices)
	}
	if options.Platform != "ios" {
		t.Fatalf("Platform = %q", options.Platform)
	}
}

func TestFormatDefaultsToNoopAndRejectsAnythingElse(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:20 names the set and the default. The contract
	// answers an unknown value with exit 2 and lists the valid options, so the
	// message here has to carry them too.
	options, err := ParseTestOptions([]string{"one.yaml"})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if options.Format != "NOOP" {
		t.Fatalf("default Format = %q, want NOOP", options.Format)
	}

	for _, format := range []string{"JUNIT", "HTML", "HTML-DETAILED", "NOOP", "junit"} {
		if _, err := ParseTestOptions([]string{"--format", format, "one.yaml"}); err != nil {
			t.Fatalf("--format %s rejected: %v", format, err)
		}
	}

	_, err = ParseTestOptions([]string{"--format", "BOGUS", "one.yaml"})
	if err == nil {
		t.Fatal("--format BOGUS accepted")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want %d for a bad flag value", code, ExitInvalid)
	}
	for _, valid := range []string{"JUNIT", "HTML", "HTML-DETAILED", "NOOP"} {
		if !strings.Contains(err.Error(), valid) {
			t.Fatalf("error %q does not name the valid option %q", err, valid)
		}
	}
}

func TestMissingPositionalIsAUsageError(t *testing.T) {
	t.Parallel()

	// A missing required flow is a usage error.
	_, err := ParseTestOptions(nil)
	if err == nil {
		t.Fatal("ParseTestOptions(nil) accepted an empty command line")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want %d", code, ExitInvalid)
	}
}

func TestUnknownFlagIsAUsageError(t *testing.T) {
	t.Parallel()

	_, err := ParseTestOptions([]string{"--not-a-flag", "one.yaml"})
	if err == nil {
		t.Fatal("an unknown flag was accepted")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want %d", code, ExitInvalid)
	}
}

func TestEnvRequiresKeyEqualsValue(t *testing.T) {
	t.Parallel()

	// Environment values require KEY=VALUE syntax.
	_, err := ParseTestOptions([]string{"-e", "MALFORMED", "one.yaml"})
	if err == nil {
		t.Fatal("-e MALFORMED accepted")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Fatalf("error = %q, want it to name the required shape", err)
	}

	// A value may itself contain '=' — only the first one separates.
	options, err := ParseTestOptions([]string{"-e", "URL=https://x/?a=b", "one.yaml"})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if got := options.Env["URL"]; got != "https://x/?a=b" {
		t.Fatalf("Env[URL] = %q, want only the first = to separate", got)
	}
}

func TestShardFlagsAreMutuallyExclusiveAndFailAfterParsing(t *testing.T) {
	t.Parallel()

	// The command line parses, then validation rejects the invalid combination
	// with exit 1 rather than a usage-error exit.
	_, err := ParseTestOptions([]string{"--shard-split", "2", "--shard-all", "2", "one.yaml"})
	if err == nil {
		t.Fatal("both shard flags were accepted together")
	}
	if code := ExitCodeFor(err); code != ExitFailure {
		t.Fatalf("exit code = %d, want %d — the command line parsed, the combination did not", code, ExitFailure)
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("error = %q, want it to say mutually exclusive", err)
	}
}

func TestShardCountMustBePositive(t *testing.T) {
	t.Parallel()

	// Non-positive shard counts are rejected before shard arithmetic.
	for _, argument := range []string{"--shard-split", "--shard-all"} {
		_, err := ParseTestOptions([]string{argument, "0", "one.yaml"})
		if err == nil {
			t.Fatalf("%s 0 accepted", argument)
		}
		if !strings.Contains(err.Error(), "positive") {
			t.Fatalf("%s 0 error = %q, want it to require a positive count", argument, err)
		}
	}
}

func TestReinstallDriverIsNegatableAndDefaultsToTrue(t *testing.T) {
	t.Parallel()

	// specs/03-cli-tooling.md:20: "--reinstall-driver (negatable, default true)".
	options, err := ParseTestOptions([]string{"one.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.ReinstallDriver {
		t.Fatal("default ReinstallDriver = false, want true")
	}
	options, err = ParseTestOptions([]string{"--no-reinstall-driver", "one.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if options.ReinstallDriver {
		t.Fatal("--no-reinstall-driver did not turn it off")
	}
}

func TestTestCommandRejectsRemovedAppleTeamID(t *testing.T) {
	t.Parallel()

	_, err := ParseTestOptions([]string{"--apple-team-id", "ABCDE12345", "one.yaml"})
	if err == nil {
		t.Fatal("test accepted the driver-setup-only --apple-team-id flag")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want usage error %d", code, ExitInvalid)
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Fatalf("error = %q, want unknown-option guidance", err)
	}
}

func TestBooleanFlagsDoNotSwallowTheNextArgument(t *testing.T) {
	t.Parallel()

	// A boolean flag that consumed its successor would silently drop a flow
	// from the run, which is the worst possible way to pass a test suite.
	//
	// Pair --headless with an ordinary flow so this test isolates boolean flag
	// parsing from validation of unrelated options.
	options, err := ParseTestOptions(
		[]string{"--headless", "--flatten-debug-output", "one.yaml", "two.yaml"})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if !options.Headless || !options.FlattenDebugOutput {
		t.Fatalf("Headless = %v, FlattenDebugOutput = %v; both flags were given",
			options.Headless, options.FlattenDebugOutput)
	}
	if !reflect.DeepEqual(options.Roots, []string{"one.yaml", "two.yaml"}) {
		t.Fatalf("Roots = %v, want both files", options.Roots)
	}
}

func TestFlagsAndPositionalsMayInterleave(t *testing.T) {
	t.Parallel()

	options, err := ParseTestOptions([]string{"one.yaml", "--headless", "two.yaml"})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if !reflect.DeepEqual(options.Roots, []string{"one.yaml", "two.yaml"}) {
		t.Fatalf("Roots = %v", options.Roots)
	}
	if !options.Headless {
		t.Fatal("the interleaved flag was lost")
	}
}

func TestValueFlagAtTheEndOfTheLineIsAUsageError(t *testing.T) {
	t.Parallel()

	// A flag that needs a value and has none must not silently take a default,
	// which would run a different suite than the operator asked for.
	_, err := ParseTestOptions([]string{"one.yaml", "--config"})
	if err == nil {
		t.Fatal("a value flag with no value was accepted")
	}
	if code := ExitCodeFor(err); code != ExitInvalid {
		t.Fatalf("exit code = %d, want %d", code, ExitInvalid)
	}
}

func TestTagListsIgnoreBlanksAndSurroundingSpace(t *testing.T) {
	t.Parallel()

	options, err := ParseTestOptions([]string{"--include-tags", " smoke , ,fast ", "one.yaml"})
	if err != nil {
		t.Fatalf("ParseTestOptions() error = %v", err)
	}
	if !reflect.DeepEqual(options.IncludeTags, []string{"smoke", "fast"}) {
		t.Fatalf("IncludeTags = %#v, want blanks dropped and space trimmed", options.IncludeTags)
	}
}

func TestExitCodeForClassifiesAPlainError(t *testing.T) {
	t.Parallel()

	// Anything that is not explicitly a usage error is a run failure. The
	// default must be the failing side: a mis-classified error that exits 0
	// reports a green suite that never ran.
	if code := ExitCodeFor(errPlain("boom")); code != ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, ExitFailure)
	}
	if code := ExitCodeFor(nil); code != ExitOK {
		t.Fatalf("exit code for nil = %d, want %d", code, ExitOK)
	}
}

type errPlain string

func (err errPlain) Error() string { return string(err) }

func TestParserHandlesEveryPinnedTestFlag(t *testing.T) {
	t.Parallel()

	// internal/capability/registry_catalog.go pins the contract `test`
	// flag list, and that list is what preflight answers questions about. A
	// flag the registry declares supported but the parser rejects would have
	// preflight promising something the CLI cannot do.
	//
	// This reads the pinned list rather than restating it, so adding a flag
	// there without handling it here reds.
	source, err := os.ReadFile(
		filepath.Join("..", "capability", "registry_catalog.go"))
	if err != nil {
		t.Fatal(err)
	}
	pinned := regexp.MustCompile(`"test\.(-[^"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(pinned) == 0 {
		t.Fatal("found no pinned test flags; the extraction is broken, not the parser")
	}

	var unhandled []string
	for _, match := range pinned {
		flag := match[1]
		// Every flag is offered with a value. A boolean ignores it and treats
		// the value as a positional, which is exactly the shape a rejection
		// would show up in.
		if _, err := ParseTestOptions([]string{flag, "1", "one.yaml"}); err != nil {
			var usage *UsageError
			if errors.As(err, &usage) && strings.Contains(usage.Message, "unknown option") {
				unhandled = append(unhandled, flag)
			}
		}
	}
	sort.Strings(unhandled)
	if len(unhandled) != 0 {
		t.Fatalf("pinned test flags the parser rejects as unknown: %v", unhandled)
	}
}
