package capability

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestDefaultFileLoaderCanonicalizesSymlinkAliasesAndLoadsOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows developer mode or elevation is required for symlink creation")
	}
	t.Parallel()

	directory := t.TempDir()
	child := filepath.Join(directory, "child.yaml")
	alias := filepath.Join(directory, "alias.yaml")
	root := filepath.Join(directory, "root.yaml")
	writeTestFlow(t, child, "- back\n")
	if err := os.Symlink(child, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	writeTestFlow(t, root, "- runFlow: child.yaml\n- runFlow: alias.yaml\n")
	loader := &countingFileLoader{delegate: FileLoader{}, loads: map[string]int{}}

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	canonicalChild, err := loader.delegate.Canonical(context.Background(), child)
	if err != nil {
		t.Fatalf("canonical child: %v", err)
	}
	if loader.loads[canonicalChild] != 1 {
		t.Fatalf("canonical child load count = %d, want 1 (%#v)", loader.loads[canonicalChild], loader.loads)
	}
	if len(report.Nodes) != 2 || len(report.Edges) != 2 {
		t.Fatalf("alias graph = nodes %d edges %d", len(report.Nodes), len(report.Edges))
	}
}

func TestDefaultFileLoaderDetectsSymlinkAliasCycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows developer mode or elevation is required for symlink creation")
	}
	t.Parallel()

	directory := t.TempDir()
	root := filepath.Join(directory, "root.yaml")
	alias := filepath.Join(directory, "alias-root.yaml")
	writeTestFlow(t, root, "- runFlow: alias-root.yaml\n")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root}})
	violation := requireViolation(t, err, "active_cycle")
	if len(violation.Chain) != 1 {
		t.Fatalf("alias cycle chain = %#v", violation.Chain)
	}
}

func TestDefaultFileLoaderRetainsEdgeChainForChildParseError(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	root := filepath.Join(directory, "root.yaml")
	child := filepath.Join(directory, "child.yaml")
	writeTestFlow(t, root, "- runFlow: child.yaml\n")
	if err := os.WriteFile(child, []byte("appId: [\n---\n- back\n"), 0o600); err != nil {
		t.Fatalf("write malformed child: %v", err)
	}

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root}})
	violation := requireViolation(t, err, "flow_parse_error")
	canonicalChild, canonicalErr := (FileLoader{}).Canonical(context.Background(), child)
	if canonicalErr != nil {
		t.Fatalf("canonical child: %v", canonicalErr)
	}
	if len(violation.Chain) != 1 || violation.Chain[0].From == "" || violation.Chain[0].Source.Start.Line != 3 ||
		violation.Source.Path != canonicalChild || violation.Source.Start.Line <= 0 {
		t.Fatalf("parse failure diagnostic = %#v", violation)
	}
}

type countingFileLoader struct {
	delegate FileLoader
	loads    map[string]int
}

func (loader *countingFileLoader) Canonical(ctx context.Context, path string) (string, error) {
	return loader.delegate.Canonical(ctx, path)
}

func (loader *countingFileLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	loader.loads[path]++
	return loader.delegate.Load(ctx, path)
}

func writeTestFlow(t *testing.T, path, commands string) {
	t.Helper()
	content := "appId: com.example.app\n---\n" + commands
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write test flow: %v", err)
	}
}
