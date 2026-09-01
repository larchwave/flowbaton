package foundation_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// importRule forbids one family of imports inside one family of packages.
// Both sides are path prefixes relative to the module root, except Import,
// which also matches a third-party path fragment.
type importRule struct {
	// Why names the boundary in the words CLAUDE.md uses for it.
	Why string
	// Inside is the package prefix the rule applies to; empty means every
	// package outside Only.
	Inside string
	// Import is the forbidden import prefix or fragment.
	Import string
	// Only exempts packages under this prefix.
	Only string
}

// architectureBoundaries are the import rules CLAUDE.md states. They were
// stated and unenforced: measured on 2026-09-01 all four held, and nothing
// would have said so on the day one stopped.
var architectureBoundaries = []importRule{
	{Why: "preflight performs side-effect-free checks", Inside: "internal/capability", Import: "internal/device"},
	{Why: "preflight performs side-effect-free checks", Inside: "internal/capability", Import: "internal/asset"},
	{Why: "preflight performs side-effect-free checks", Inside: "internal/capability", Import: "internal/cli"},
	{Why: "preflight performs side-effect-free checks", Inside: "internal/capability", Import: "internal/session"},
	{Why: "exploration is wired only in internal/cli", Inside: "internal/engine", Import: "internal/explore"},
	{Why: "exploration is wired only in internal/cli", Inside: "internal/device", Import: "internal/explore"},
	{Why: "exploration is wired only in internal/cli", Inside: "internal/capability", Import: "internal/explore"},
	{Why: "the AI provider wall", Import: "langchaingo", Only: "internal/aiengine"},
	{Why: "physical-device transport owns go-ios", Import: "go-ios", Only: "internal/iosdevice"},
}

// violates reports whether one import inside one package breaks the rule.
func (rule importRule) violates(pkg, imported string) bool {
	if !strings.Contains(imported, rule.Import) {
		return false
	}
	if rule.Only != "" {
		return !strings.HasPrefix(pkg, rule.Only)
	}
	return strings.HasPrefix(pkg, rule.Inside)
}

// The rule logic itself, so the repository scan below is not the only thing
// that can turn this file red.
func TestImportRuleCatchesAViolation(t *testing.T) {
	t.Parallel()

	wall := importRule{Import: "langchaingo", Only: "internal/aiengine"}
	if !wall.violates("internal/engine", "github.com/tmc/langchaingo/llms") {
		t.Error("a wall breach outside the exempt package was allowed")
	}
	if wall.violates("internal/aiengine", "github.com/tmc/langchaingo/llms") {
		t.Error("the exempt package was refused its own dependency")
	}
	inside := importRule{Inside: "internal/capability", Import: "internal/device"}
	if !inside.violates("internal/capability/preflight", "github.com/larchwave/flowbaton/internal/device") {
		t.Error("a forbidden import inside the named package was allowed")
	}
	if inside.violates("internal/engine", "github.com/larchwave/flowbaton/internal/device") {
		t.Error("a package the rule does not name was refused")
	}
}

// CLAUDE.md states these boundaries in prose. A rule nothing enforces is a
// rule the next change may break without anything saying so -- which is how
// a preflight that promises no side effects acquires a driver, and how the
// provider wall stops being one wall.
func TestArchitectureImportBoundariesHold(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	fset := token.NewFileSet()
	broken := []string{}
	scanned := 0
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		scanned++
		relative, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		pkg := filepath.ToSlash(relative)
		for _, spec := range file.Imports {
			imported, quoteErr := strconv.Unquote(spec.Path.Value)
			if quoteErr != nil {
				return quoteErr
			}
			for _, rule := range architectureBoundaries {
				if rule.violates(pkg, imported) {
					broken = append(broken, pkg+" imports "+imported+" ("+rule.Why+")")
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned == 0 {
		t.Fatal("no source files scanned; the guard is reading the wrong tree")
	}
	sort.Strings(broken)
	for _, line := range broken {
		t.Error(line)
	}
}
