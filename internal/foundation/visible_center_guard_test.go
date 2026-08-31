package foundation_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every point FlowBaton aims a gesture at, or writes into an exported flow,
// must be one the device has. Matching keeps an element at 10% visible, so a
// list row scrolled past an edge stays selectable while hierarchy.Center puts
// its point off the screen -- measured live on Apple's Reminders, where five
// of one screen's 47 element-table rows carried such a point.
// hierarchy.VisibleCenter is the answer everywhere except one place.
//
// scrollUntilVisibleCenterRequest is that place, and it is not an oversight:
// it computes how far to scroll to bring an element's centre to the middle of
// the viewport, so it needs the TRUE centre, off-screen included. The visible
// centre would shorten every scroll. A sweep that fixed three look-alike call
// sites broke a fourth the same evening by treating them as one kind, which is
// why this is pinned rather than remembered.
func TestGesturePointsComeFromTheVisibleCentre(t *testing.T) {
	allowed := map[string]bool{
		"internal/engine/interaction_scroll_handler.go": true,
	}
	root := repoRoot(t)
	var offenders []string
	for _, tree := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, tree),
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
				if strings.Contains(string(body), "hierarchy.Center(") {
					offenders = append(offenders, relative)
				}
				return nil
			})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("these aim at a raw geometric centre instead of hierarchy.VisibleCenter: %v", offenders)
	}
}
