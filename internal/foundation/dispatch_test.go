package foundation_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/cli"
)

// TopLevelSubcommands exists so completion offers the commands flowbaton
// actually dispatches, and its own comment promises "a test pins the set so
// dispatch and completion do not silently drift apart". No test did: the one
// that claimed to looped over TopLevelSubcommands and searched for each entry
// in a script built out of TopLevelSubcommands, so it checked the list
// against itself and would have stayed green for a command added to main.go
// and nowhere else.
//
// The dispatch itself is the independent source, so this reads it.
func TestCompletionListsExactlyWhatMainDispatches(t *testing.T) {
	source := readFile(t, "cmd/flowbaton/main.go")
	pattern := regexp.MustCompile(`args\[0\] == "([^"]+)"`)
	matches := pattern.FindAllStringSubmatch(source, -1)
	// A dispatch that stopped matching this shape would leave nothing to
	// check, and an empty set trivially equals an empty set.
	if len(matches) == 0 {
		t.Fatal("no `args[0] == \"...\"` dispatch found in cmd/flowbaton/main.go; this test proves nothing until the pattern matches again")
	}
	var dispatched []string
	for _, match := range matches {
		// --version is a flag, not a subcommand; completion offers it from its
		// own literal.
		if strings.HasPrefix(match[1], "-") {
			continue
		}
		dispatched = append(dispatched, match[1])
	}
	declared := append([]string(nil), cli.TopLevelSubcommands...)
	sort.Strings(dispatched)
	sort.Strings(declared)
	if strings.Join(dispatched, ",") != strings.Join(declared, ",") {
		t.Errorf(
			"cmd/flowbaton/main.go dispatches %v\ncli.TopLevelSubcommands holds %v\nthe two must name the same commands",
			dispatched, declared)
	}
}
