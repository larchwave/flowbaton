package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// TopLevelSubcommands is the canonical list of subcommands flowbaton dispatches.
// generate-completion reads it so a new command shows up in completion the
// moment it is added here; a test pins the set so dispatch and completion do
// not silently drift apart.
var TopLevelSubcommands = []string{
	"check-syntax",
	"test",
	"record",
	"list-devices",
	"start-device",
	"hierarchy",
	"query",
	"bugreport",
	"driver-setup",
	"mcp",
	"generate-completion",
}

// GenerateCompletionRunner emits a shell completion script. Pure: no device, no
// I/O beyond stdout.
type GenerateCompletionRunner struct{}

func (GenerateCompletionRunner) Run(_ context.Context, args []string, stdout, stderr io.Writer) int {
	shell := "bash"
	if len(args) > 0 {
		shell = args[0]
	}
	if len(args) > 1 {
		fmt.Fprintf(stderr, "generate-completion: unexpected argument %q\n", args[1])
		return ExitInvalid
	}

	switch shell {
	case "bash":
		fmt.Fprint(stdout, bashCompletion())
	case "zsh":
		fmt.Fprint(stdout, zshCompletion())
	default:
		fmt.Fprintf(stderr, "generate-completion: unsupported shell %q (want bash or zsh)\n", shell)
		return ExitInvalid
	}
	return ExitOK
}

func bashCompletion() string {
	// A fresh slice, not append onto the package var, so completing never
	// mutates the canonical list.
	words := strings.Join(append([]string{"--version"}, TopLevelSubcommands...), " ")
	return `_flowbaton() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  if [ "${COMP_CWORD}" -eq 1 ]; then
    COMPREPLY=( $(compgen -W "` + words + `" -- "${cur}") )
  fi
}
complete -F _flowbaton flowbaton
`
}

func zshCompletion() string {
	commands := strings.Join(TopLevelSubcommands, " ")
	return `#compdef flowbaton
_flowbaton() {
  local -a commands
  commands=(` + commands + `)
  if (( CURRENT == 2 )); then
    _describe 'command' commands
  fi
}
_flowbaton "$@"
`
}
