package memory

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Knowledge serves operator-authored hints from knowledge/*.md. A file whose
// first line is "match: <pattern>" applies only to screens the pattern
// matches; a file without the directive applies to every screen of the app.
// Bodies interpolate ${env.NAME} through the injected getenv, so credentials
// stay out of the files themselves.
type Knowledge struct {
	dir    string
	getenv func(string) string
}

var _ explore.KnowledgeStore = (*Knowledge)(nil)

// NewKnowledge returns a store rooted at stateDir/knowledge. A nil getenv
// resolves every ${env.NAME} to empty rather than reading ambient state.
func NewKnowledge(stateDir string, getenv func(string) string) *Knowledge {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	return &Knowledge{dir: filepath.Join(stateDir, "knowledge"), getenv: getenv}
}

// Match returns the interpolated hint bodies that apply to the screen, in
// filename order.
func (k *Knowledge) Match(ctx context.Context, screen explore.ScreenSignature) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	paths, err := filepath.Glob(filepath.Join(k.dir, "*.md"))
	if err != nil {
		return nil, fmt.Errorf("knowledge: list %s: %w", k.dir, err)
	}
	var bodies []string
	for _, filePath := range paths {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("knowledge: read %s: %w", filepath.Base(filePath), err)
		}
		pattern, body := splitMatchDirective(string(data))
		if pattern != "" && !screenMatchesPattern(pattern, screen) {
			continue
		}
		body = strings.TrimSpace(interpolateEnv(body, k.getenv))
		if body == "" {
			continue
		}
		bodies = append(bodies, body)
	}
	return bodies, nil
}

// splitMatchDirective peels an optional leading "match: <pattern>" line off
// a hint file, returning the pattern (empty when absent) and the body.
func splitMatchDirective(content string) (string, string) {
	first, rest, _ := strings.Cut(content, "\n")
	if pattern, ok := strings.CutPrefix(strings.TrimSpace(first), "match:"); ok {
		return strings.TrimSpace(pattern), rest
	}
	return "", content
}

// screenMatchesPattern accepts a screen when the pattern globs (path.Match)
// or is a plain substring of either the screen key or the joined salient
// labels. Matching is case-insensitive; a malformed glob falls back to the
// substring check instead of failing.
func screenMatchesPattern(pattern string, screen explore.ScreenSignature) bool {
	needle := strings.ToLower(pattern)
	targets := []string{screen.Key(), strings.Join(screen.Salient, " ")}
	for _, target := range targets {
		target = strings.ToLower(target)
		if target == "" {
			continue
		}
		if ok, err := path.Match(needle, target); err == nil && ok {
			return true
		}
		if strings.Contains(target, needle) {
			return true
		}
	}
	return false
}

var envPlaceholder = regexp.MustCompile(`\$\{env\.([A-Za-z_][A-Za-z0-9_]*)\}`)

func interpolateEnv(body string, getenv func(string) string) string {
	return envPlaceholder.ReplaceAllStringFunc(body, func(placeholder string) string {
		name := placeholder[len("${env.") : len(placeholder)-1]
		return getenv(name)
	})
}
