package foundation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Everything in the explore session that selects an element by name has to
// see the screen the engine will see when the exported flow is replayed:
// hierarchy.New alone reaches elements with no area, whose centre is (0,0),
// and elements past the screen edge. ScreenState.VisibleTree is the one
// place that prunes, and two fixes in one evening were needed because the
// second consumer sat twelve lines from the first.
//
// FlattenScreen is the other allowed caller: it builds the element table
// from the whole tree and drops what cannot be touched by its own rule.
func TestExploreNormalizesTheScreenThroughOnePlace(t *testing.T) {
	allowed := map[string]bool{
		"internal/explore/screen.go":  true,
		"internal/explore/flatten.go": true,
	}
	root := repoRoot(t)
	var offenders []string
	err := filepath.Walk(filepath.Join(root, "internal", "explore"),
		func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return err
			}
			relative, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			relative = filepath.ToSlash(relative)
			if allowed[relative] {
				return nil
			}
			body, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if strings.Contains(string(body), "hierarchy.New(") {
				offenders = append(offenders, relative)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("these normalize the screen tree themselves instead of through ScreenState.VisibleTree: %v", offenders)
	}
}
