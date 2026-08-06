package workspace

import (
	"path/filepath"
	"reflect"
	"testing"
)

// Directory discovery sorts paths so a suite has the same default order on
// every filesystem. Authors who need another order can set
// `executionOrder.flowsOrder`.
func TestFlowsRunInAStableOrderRatherThanTheFilesystems(t *testing.T) {
	t.Parallel()

	// Named so that byte order, case-insensitive order and creation order all
	// disagree: "Beta" sorts before "alpha" by byte and after it by collation,
	// and the files are written last-first.
	root := t.TempDir()
	for _, name := range []string{"zebra.yaml", "middle.yaml", "alpha.yaml", "Beta.yaml", "01-first.yaml"} {
		writeFlow(t, root, name, "com.example.order", nil, name)
	}

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}

	want := []string{
		filepath.Join(root, "01-first.yaml"),
		filepath.Join(root, "Beta.yaml"),
		filepath.Join(root, "alpha.yaml"),
		filepath.Join(root, "middle.yaml"),
		filepath.Join(root, "zebra.yaml"),
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Twice over the same directory has to give the same answer. Sorting is what
// makes that true no matter what the walk hands over, and a single run cannot
// tell a sorted planner from one that got lucky.
func TestTheSameDirectoryPlansTheSameOrderTwice(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, name := range []string{"kilo.yaml", "delta.yaml", "echo.yaml", "juliet.yaml", "bravo.yaml", "foxtrot.yaml"} {
		writeFlow(t, root, name, "com.example.order", nil, name)
	}

	first, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	second, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) second error = %v", err)
	}
	if !reflect.DeepEqual(first.SelectedPaths(), second.SelectedPaths()) {
		t.Fatalf("two plans of one directory disagree:\n%v\n%v",
			first.SelectedPaths(), second.SelectedPaths())
	}
	if got := first.SelectedPaths(); !sortedAscending(got) {
		t.Fatalf("order = %v, want it sorted", got)
	}
}

func sortedAscending(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			return false
		}
	}
	return true
}
