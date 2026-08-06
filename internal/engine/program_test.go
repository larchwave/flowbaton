package engine

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestPrepareRejectsNilLoaderBeforePreflight(t *testing.T) {
	t.Parallel()

	program, err := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{"root.yaml"}}, nil)
	if program != nil {
		t.Fatalf("Program = %#v, want nil", program)
	}
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("error = %T %v, want *ConfigurationError", err, err)
	}
}

func TestPrepareStopsAtPreflightFailureWithoutProducingOrMutatingProgramData(t *testing.T) {
	t.Parallel()

	canonical := "/workspace/unsupported.yaml"
	text := "Submit"
	loader := &boundaryLoader{canonical: canonical, flow: model.Flow{
		SchemaVersion: model.ASTVersionV0,
		// An unregistered config extension fails this preflight boundary.
		Config: model.Config{
			Env: map[string]string{"ROLE": "original"},
			Ext: map[string]any{"futureMagic": true},
		},
		Commands: []model.Command{{Kind: model.CommandTapOn, Selector: &model.ElementSelector{TextRegex: &text}}},
	}}
	plan := model.ExecutionPlan{SelectedRoots: []string{canonical}}
	program, err := Prepare(context.Background(), plan, loader)
	if program != nil {
		t.Fatalf("Program = %#v, want nil after preflight failure", program)
	}
	var violation capability.Violation
	if !errors.As(err, &violation) {
		t.Fatalf("error = %T %v, want capability.Violation", err, err)
	}
	if loader.canonicalCalls != 1 || loader.loadCalls != 1 {
		t.Fatalf("loader calls = canonical %d load %d, want 1/1", loader.canonicalCalls, loader.loadCalls)
	}
	if plan.SelectedRoots[0] != canonical || loader.flow.Config.Env["ROLE"] != "original" || loader.flow.Commands[0].Kind != model.CommandTapOn {
		t.Fatal("Prepare mutated plan or loader-owned flow on preflight failure")
	}
}

func TestPrepareCancelledContextDoesNotReachLoader(t *testing.T) {
	t.Parallel()

	loader := &boundaryLoader{canonical: "/workspace/root.yaml"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	program, err := Prepare(ctx, model.ExecutionPlan{SelectedRoots: []string{"root.yaml"}}, loader)
	if program != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Prepare = %#v, %v; want nil, context.Canceled", program, err)
	}
	if loader.canonicalCalls != 0 || loader.loadCalls != 0 {
		t.Fatalf("cancelled loader calls = canonical %d load %d, want zero", loader.canonicalCalls, loader.loadCalls)
	}
}

func TestPrepareCachesExactlyTheDiamondFlowsSelectedByPreflight(t *testing.T) {
	t.Parallel()

	root := fixturePath(t, "prepare", "root.yaml")
	loader := newCountingLoader(capability.FileLoader{})
	program, err := Prepare(context.Background(), model.ExecutionPlan{
		SelectedRoots: []string{root, root},
	}, loader)
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}

	canonicalRoot, err := loader.Canonical(context.Background(), root)
	if err != nil {
		t.Fatalf("canonical root: %v", err)
	}
	if got, want := program.Roots(), []string{canonicalRoot, canonicalRoot}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Roots = %#v, want %#v", got, want)
	}

	wantPaths := []string{
		canonicalRoot,
		fixtureCanonical(t, loader, "prepare", "left.yaml"),
		fixtureCanonical(t, loader, "prepare", "shared.yaml"),
		fixtureCanonical(t, loader, "prepare", "right.yaml"),
	}
	if got := program.FlowPaths(); !reflect.DeepEqual(got, wantPaths) {
		t.Fatalf("FlowPaths = %#v, want %#v", got, wantPaths)
	}
	if got := len(program.Graph().Edges); got != 6 {
		t.Fatalf("graph edges = %d, want 6 call sites", got)
	}
	for _, path := range wantPaths {
		if got := loader.LoadCount(path); got != 1 {
			t.Fatalf("Load count for %s = %d, want 1", path, got)
		}
		flow, exists := program.Flow(path)
		if !exists || flow.SchemaVersion != model.ASTVersionV0 {
			t.Fatalf("Flow(%s) = %#v, %v", path, flow, exists)
		}
		if got := loader.LoadCount(path); got != 1 {
			t.Fatalf("Flow(%s) reloaded source: count = %d", path, got)
		}
	}
}

func TestProgramDefensivelyCopiesLoaderDataAndAccessorResults(t *testing.T) {
	t.Parallel()

	canonical := "/workspace/root.yaml"
	loader := &mutableFlowLoader{canonical: canonical, flow: model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          canonical,
		Config: model.Config{
			Tags:       []string{"smoke"},
			Env:        map[string]string{"ROLE": "original"},
			Properties: map[string]string{"owner": "engine"},
			FieldSources: map[string]model.SourceInfo{
				"env": {Path: canonical, Start: model.Position{Line: 1}},
			},
		},
		Commands: []model.Command{{
			Kind: model.CommandLaunchApp,
			Arguments: map[string]any{
				"nested": []any{map[string]any{"value": "original"}},
			},
		}},
	}}

	program, err := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{canonical}}, loader)
	if err != nil {
		t.Fatalf("Prepare error: %v", err)
	}
	loader.flow.Config.Tags[0] = "loader-mutated"
	loader.flow.Config.Env["ROLE"] = "loader-mutated"
	loader.flow.Config.Properties["owner"] = "loader-mutated"
	loader.flow.Config.FieldSources["env"] = model.SourceInfo{Path: "loader-mutated"}
	loader.flow.Commands[0].Arguments.(map[string]any)["nested"].([]any)[0].(map[string]any)["value"] = "loader-mutated"

	first, exists := program.Flow(canonical)
	if !exists {
		t.Fatal("prepared flow missing")
	}
	assertOriginalFlowData(t, first)
	first.Config.Tags[0] = "accessor-mutated"
	first.Config.Env["ROLE"] = "accessor-mutated"
	first.Config.Properties["owner"] = "accessor-mutated"
	first.Commands[0].Arguments.(map[string]any)["nested"].([]any)[0].(map[string]any)["value"] = "accessor-mutated"

	second, exists := program.Flow(canonical)
	if !exists {
		t.Fatal("prepared flow missing on second access")
	}
	assertOriginalFlowData(t, second)

	roots := program.Roots()
	paths := program.FlowPaths()
	graph := program.Graph()
	roots[0] = "accessor-mutated"
	paths[0] = "accessor-mutated"
	graph.Roots[0] = "accessor-mutated"
	graph.Nodes[0].Path = "accessor-mutated"
	if program.Roots()[0] != canonical || program.FlowPaths()[0] != canonical {
		t.Fatal("Program root/path accessors exposed backing storage")
	}
	secondGraph := program.Graph()
	if secondGraph.Roots[0] != canonical || secondGraph.Nodes[0].Path != canonical {
		t.Fatal("Program Graph exposed backing storage")
	}
}

func TestPrepareRetainsDefensiveAliasesForOfflineFlowResolution(t *testing.T) {
	t.Parallel()

	const (
		rootAlias      = "/workspace/root-alias.yaml"
		childAlias     = "/workspace/child-alias.yaml"
		canonicalRoot  = "/canonical/root.yaml"
		canonicalChild = "/canonical/child.yaml"
	)
	link := model.FileLink{
		Kind: model.FileLinkFlow, Path: "child-alias.yaml", ResolvedPath: childAlias,
	}
	loader := &aliasingFlowLoader{
		canonical: map[string]string{
			rootAlias: rootAlias, childAlias: canonicalChild,
		},
		flows: map[string]model.Flow{
			rootAlias: {
				SchemaVersion: model.ASTVersionV0, Path: canonicalRoot,
				Commands: []model.Command{{Kind: model.CommandRunFlow, Links: []model.FileLink{link}}},
			},
			canonicalChild: {SchemaVersion: model.ASTVersionV0, Path: canonicalChild},
		},
	}
	loader.canonical[rootAlias] = canonicalRoot
	loader.flows[canonicalRoot] = loader.flows[rootAlias]

	program, err := Prepare(context.Background(), model.ExecutionPlan{SelectedRoots: []string{rootAlias}}, loader)
	if err != nil {
		t.Fatalf("Prepare() error: %v", err)
	}
	loader.canonical[childAlias] = "/mutated/child.yaml"
	delete(loader.flows, canonicalChild)

	resolved, err := program.resolveFlowLink(canonicalRoot, link)
	if err != nil {
		t.Fatalf("resolveFlowLink() error: %v", err)
	}
	if resolved != canonicalChild {
		t.Fatalf("resolveFlowLink() = %q, want retained canonical %q", resolved, canonicalChild)
	}
	if _, exists := program.Flow(canonicalChild); !exists {
		t.Fatal("offline resolution lost the prepared canonical child")
	}
}

func TestRecordingLoaderAliasSnapshotsAreDefensive(t *testing.T) {
	t.Parallel()

	const raw = "/workspace/alias.yaml"
	loader := newRecordingLoader(&aliasingFlowLoader{
		canonical: map[string]string{raw: "/canonical/flow.yaml"},
	})
	if _, err := loader.Canonical(context.Background(), raw); err != nil {
		t.Fatalf("Canonical() error: %v", err)
	}
	aliases := loader.aliases()
	aliases[raw] = "/caller/mutation.yaml"
	if got := loader.aliases()[raw]; got != "/canonical/flow.yaml" {
		t.Fatalf("aliases() retained caller mutation: %q", got)
	}
}

func assertOriginalFlowData(t *testing.T, flow model.Flow) {
	t.Helper()
	if flow.Config.Tags[0] != "smoke" || flow.Config.Env["ROLE"] != "original" || flow.Config.Properties["owner"] != "engine" {
		t.Fatalf("flow config mutated: %#v", flow.Config)
	}
	if got := flow.Commands[0].Arguments.(map[string]any)["nested"].([]any)[0].(map[string]any)["value"]; got != "original" {
		t.Fatalf("command nested value = %#v, want original", got)
	}
	if got := flow.Config.FieldSources["env"].Path; got != "/workspace/root.yaml" {
		t.Fatalf("field source path = %q, want original", got)
	}
}

type mutableFlowLoader struct {
	canonical string
	flow      model.Flow
}

type aliasingFlowLoader struct {
	canonical map[string]string
	flows     map[string]model.Flow
}

func (l *aliasingFlowLoader) Canonical(_ context.Context, path string) (string, error) {
	if canonical, exists := l.canonical[path]; exists {
		return canonical, nil
	}
	return path, nil
}

func (l *aliasingFlowLoader) Load(_ context.Context, path string) (model.Flow, error) {
	flow, exists := l.flows[path]
	if !exists {
		return model.Flow{}, errors.New("missing flow " + path)
	}
	return cloneFlow(flow), nil
}

type boundaryLoader struct {
	canonical      string
	flow           model.Flow
	canonicalCalls int
	loadCalls      int
}

func (l *boundaryLoader) Canonical(context.Context, string) (string, error) {
	l.canonicalCalls++
	return l.canonical, nil
}

func (l *boundaryLoader) Load(context.Context, string) (model.Flow, error) {
	l.loadCalls++
	return l.flow, nil
}

func (l *mutableFlowLoader) Canonical(context.Context, string) (string, error) {
	return l.canonical, nil
}

func (l *mutableFlowLoader) Load(context.Context, string) (model.Flow, error) {
	return l.flow, nil
}

type countingLoader struct {
	base  capability.FlowLoader
	mu    sync.Mutex
	loads map[string]int
}

func newCountingLoader(base capability.FlowLoader) *countingLoader {
	return &countingLoader{base: base, loads: make(map[string]int)}
}

func (l *countingLoader) Canonical(ctx context.Context, path string) (string, error) {
	return l.base.Canonical(ctx, path)
}

func (l *countingLoader) Load(ctx context.Context, path string) (model.Flow, error) {
	l.mu.Lock()
	l.loads[path]++
	l.mu.Unlock()
	return l.base.Load(ctx, path)
}

func (l *countingLoader) LoadCount(path string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loads[path]
}

func fixturePath(t *testing.T, parts ...string) string {
	t.Helper()
	pathParts := append([]string{"..", "..", "testdata", "engine"}, parts...)
	path, err := filepath.Abs(filepath.Join(pathParts...))
	if err != nil {
		t.Fatalf("fixture path: %v", err)
	}
	return path
}

func fixtureCanonical(t *testing.T, loader capability.FlowLoader, parts ...string) string {
	t.Helper()
	canonical, err := loader.Canonical(context.Background(), fixturePath(t, parts...))
	if err != nil {
		t.Fatalf("canonical fixture: %v", err)
	}
	return canonical
}
