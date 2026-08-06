package cli

import (
	"bytes"
	"context"
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

	// The whole point is to complete the real subcommands. If the script and the
	// dispatch drift, completion offers commands that do not exist or omits ones
	// that do — so every command flowbaton actually dispatches must appear.
	stdout, _, code := runGenerateCompletion(t, "bash")
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, command := range []string{"check-syntax", "test", "list-devices", "hierarchy", "query"} {
		if !strings.Contains(stdout, command) {
			t.Fatalf("completion is missing subcommand %q\n%s", command, stdout)
		}
	}
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
