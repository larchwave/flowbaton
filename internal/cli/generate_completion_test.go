package cli

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// generate-completion emits a shell completion script (spec 03). It is pure:
// no device and no I/O beyond stdout.

func runGenerateCompletion(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := GenerateCompletionRunner{}.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestGenerateCompletionDefaultsToBash(t *testing.T) {
	t.Parallel()

	stdout, _, code := runGenerateCompletion(t)
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	// A bash completion registers a function with `complete`. Without that the
	// script is inert.
	if !strings.Contains(stdout, "complete ") || !strings.Contains(stdout, "flowbaton") {
		t.Fatalf("output is not a bash completion for flowbaton\n%s", stdout)
	}
}

func TestGenerateCompletionListsEverySubcommand(t *testing.T) {
	t.Parallel()

	// Every subcommand must reach compgen's word list, which is the only place
	// bash reads them from. Searching the whole script instead would pass on a
	// list moved into a comment or an unused variable. This test says nothing
	// about drift from the dispatch, because the script is built out of the
	// same slice it would be checked against
	// (TestCompletionListsExactlyWhatMainDispatches in internal/foundation
	// reads cmd/flowbaton/main.go for that).
	stdout, _, code := runGenerateCompletion(t, "bash")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	words := compgenWords(t, stdout)
	for _, command := range TopLevelSubcommands {
		if !slices.Contains(words, command) {
			t.Fatalf("compgen word list %v is missing subcommand %q\n%s", words, command, stdout)
		}
	}
	if !slices.Contains(words, "--version") {
		t.Fatalf("compgen word list %v drops --version\n%s", words, stdout)
	}
}

// compgenWords returns the words bash would actually offer.
func compgenWords(t *testing.T, script string) []string {
	t.Helper()
	match := regexp.MustCompile(`compgen -W "([^"]*)"`).FindStringSubmatch(script)
	if match == nil {
		t.Fatalf("no compgen word list in the completion script\n%s", script)
	}
	return strings.Fields(match[1])
}

func TestGenerateCompletionSupportsZsh(t *testing.T) {
	t.Parallel()

	stdout, _, code := runGenerateCompletion(t, "zsh")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	// zsh completions announce themselves with a #compdef tag; a bash script has
	// none, so this distinguishes the two shells rather than emitting one twice.
	if !strings.Contains(stdout, "#compdef flowbaton") {
		t.Fatalf("output is not a zsh completion\n%s", stdout)
	}
}

func TestGenerateCompletionRefusesAnUnknownShell(t *testing.T) {
	t.Parallel()

	_, stderr, code := runGenerateCompletion(t, "powershell")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "powershell") {
		t.Fatalf("the refusal did not name the unsupported shell: %q", stderr)
	}
}
