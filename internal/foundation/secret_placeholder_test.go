package foundation_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// readSourceLine returns the one line of a repository file that holds needle.
func readSourceLine(t *testing.T, relative, needle string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("%s no longer holds %q; this guard reads that line", relative, needle)
	return ""
}

var (
	placeholderFormat = regexp.MustCompile(`\$\{FLOWBATON_[A-Za-z0-9_]*%d\}`)
	patternLiteral    = regexp.MustCompile("regexp.MustCompile\\(`(.+)`\\)")
)

// The exporter writes a placeholder for text a run typed into a secure
// field, and the engine refuses to run a flow whose placeholder is unset --
// keeping the placeholder, not the resolved value, in artifacts. Neither
// side names the other: the exporter writes a literal and the engine matches
// a shape. Rename either and the flow types "${FLOWBATON_EXPLORE_SECRET_1}"
// into a login field as if it were a password, with no guard anywhere
// saying so.
func TestTheExportedSecretPlaceholderIsTheOneTheEngineGuards(t *testing.T) {
	t.Parallel()

	written := placeholderFormat.FindString(readSourceLine(t,
		"internal/explore/export/exporter.go", "step.Action.Text = fmt.Sprintf("))
	if written == "" {
		t.Fatal("the exporter's placeholder format was not read; this guard reads that line")
	}
	sample := strings.Replace(written, "%d", "1", 1)

	literal := patternLiteral.FindStringSubmatch(readSourceLine(t,
		"internal/engine/interaction_text_handlers.go", "var secretVariablePattern ="))
	if literal == nil {
		t.Fatal("the engine's placeholder pattern was not read; this guard reads that line")
	}
	guard, err := regexp.Compile(literal[1])
	if err != nil {
		t.Fatalf("the engine's pattern does not compile: %v", err)
	}
	if !guard.MatchString(sample) {
		t.Errorf("the exporter writes %q and the engine's guard %q does not match it",
			sample, literal[1])
	}
}
