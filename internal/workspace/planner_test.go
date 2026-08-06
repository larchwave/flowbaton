package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// specs/03-cli-tooling.md section 1 defines flow discovery: a single file runs
// directly; a
// directory is walked with config auto-discovery, top-level inclusion globs
// defaulting to ["*"], tag filtering merged between CLI and workspace config,
// and sequential ordering from executionOrder.flowsOrder.
//
// Nothing here touches a device, which is why it can be built before any
// driver exists.

func TestSingleFileRunsDirectly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	flow := writeFlow(t, root, "solo.yaml", "com.example.solo", nil, "Solo")
	plan, err := Discover([]string{flow}, Options{})
	if err != nil {
		t.Fatalf("Discover(file) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{flow}) {
		t.Fatalf("selected = %v, want the single authored file", got)
	}
}

func TestSingleFileIsNotFilteredByTags(t *testing.T) {
	t.Parallel()

	// "Single file → run directly": naming a file explicitly is the operator
	// saying they want it, so a tag filter must not silently drop it.
	root := t.TempDir()
	flow := writeFlow(t, root, "solo.yaml", "com.example.solo", []string{"slow"}, "Solo")
	plan, err := Discover([]string{flow}, Options{ExcludeTags: []string{"slow"}})
	if err != nil {
		t.Fatalf("Discover(file) error = %v", err)
	}
	if got := len(plan.SelectedPaths()); got != 1 {
		t.Fatalf("selected %d flows, want the explicitly named one", got)
	}
}

func TestDirectoryWalksTopLevelFlowsOnly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := writeFlow(t, root, "a.yaml", "com.example.a", nil, "A")
	second := writeFlow(t, root, "b.yml", "com.example.b", nil, "B")
	writeFlow(t, filepath.Join(root, "nested"), "c.yaml", "com.example.c", nil, "C")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("not a flow"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	// The default glob is ["*"] and top-level only, so the nested flow and the
	// non-YAML file are both out.
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{first, second}) {
		t.Fatalf("selected = %v, want the two top-level flows", got)
	}
}

func TestInclusionGlobsComeFromTheWorkspaceConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "smoke-login.yaml", "com.example.a", nil, "Login")
	writeFlow(t, root, "smoke-cart.yaml", "com.example.b", nil, "Cart")
	other := writeFlow(t, root, "regression.yaml", "com.example.c", nil, "Regression")
	writeConfig(t, root, "flows:\n  - regression*\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("selected = %v, want only the globbed flow", got)
	}
}

func TestConfigFileIsNeverItselfAFlow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	flow := writeFlow(t, root, "a.yaml", "com.example.a", nil, "A")
	writeConfig(t, root, "flows:\n  - \"*\"\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{flow}) {
		t.Fatalf("selected = %v, want the flow without config.yaml", got)
	}
}

func TestTagFiltersMergeCLIAndWorkspaceConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	smoke := writeFlow(t, root, "smoke.yaml", "com.example.a", []string{"smoke"}, "Smoke")
	writeFlow(t, root, "slow.yaml", "com.example.b", []string{"smoke", "slow"}, "Slow")
	writeFlow(t, root, "other.yaml", "com.example.c", []string{"nightly"}, "Other")
	writeConfig(t, root, "includeTags: smoke\n")

	// The workspace includes smoke; the CLI excludes slow. Both apply.
	plan, err := Discover([]string{root}, Options{ExcludeTags: []string{"slow"}})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{smoke}) {
		t.Fatalf("selected = %v, want only the untagged-slow smoke flow", got)
	}
}

func TestFlowsOrderSequencesByFlowName(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := writeFlow(t, root, "z-first.yaml", "com.example.a", nil, "First")
	second := writeFlow(t, root, "a-second.yaml", "com.example.b", nil, "Second")
	rest := writeFlow(t, root, "m-rest.yaml", "com.example.c", nil, "Rest")
	writeConfig(t, root, "executionOrder:\n  continueOnFailure: true\n  flowsOrder:\n    - First\n    - Second\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	// Named flows lead, in the authored order; everything else follows in a
	// deterministic path order rather than in filesystem order.
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{first, second, rest}) {
		t.Fatalf("selected = %v, want [%s %s %s]", got, first, second, rest)
	}
	if got := len(plan.Sequence); got != 2 {
		t.Fatalf("sequence length = %d, want the two named flows", got)
	}
	if !plan.ContinueOnFailure {
		t.Fatal("continueOnFailure = false, want the authored true")
	}
}

func TestFlowsOrderRejectsAnUnknownName(t *testing.T) {
	t.Parallel()

	// A misspelled name in flowsOrder silently reordering nothing is worse
	// than a refusal: the operator asked for an order they did not get.
	root := t.TempDir()
	writeFlow(t, root, "a.yaml", "com.example.a", nil, "A")
	writeConfig(t, root, "executionOrder:\n  flowsOrder:\n    - Missing\n")

	if _, err := Discover([]string{root}, Options{}); err == nil {
		t.Fatal("Discover() succeeded; want a refusal naming the unknown flow")
	}
}

func TestExplicitConfigPathOverridesDiscovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "a.yaml", "com.example.a", nil, "A")
	other := writeFlow(t, root, "b.yaml", "com.example.b", nil, "B")
	writeConfig(t, root, "flows:\n  - a*\n")
	explicit := filepath.Join(t.TempDir(), "explicit.yaml")
	if err := os.WriteFile(explicit, []byte("flows:\n  - b*\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := Discover([]string{root}, Options{ConfigPath: explicit})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{other}) {
		t.Fatalf("selected = %v, want the flow the explicit config globs", got)
	}
}

func TestDiscoverRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "a.yaml", "com.example.a", []string{"nightly"}, "A")
	for _, test := range []struct {
		name    string
		roots   []string
		options Options
	}{
		{name: "no roots", roots: nil},
		{name: "missing path", roots: []string{filepath.Join(root, "absent.yaml")}},
		{name: "everything filtered out", roots: []string{root}, options: Options{IncludeTags: []string{"smoke"}}},
		{name: "missing explicit config", roots: []string{root}, options: Options{ConfigPath: "/nope/config.yaml"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Discover(test.roots, test.options); err == nil {
				t.Fatal("Discover() succeeded; want a refusal")
			}
		})
	}
}

func TestPlanProducesAnExecutionPlanInSelectionOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := writeFlow(t, root, "z.yaml", "com.example.a", nil, "First")
	second := writeFlow(t, root, "a.yaml", "com.example.b", nil, "Second")
	writeConfig(t, root, "executionOrder:\n  flowsOrder:\n    - First\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	want := []string{first, second}
	if got := plan.ExecutionPlan().SelectedRoots; !reflect.DeepEqual(got, want) {
		t.Fatalf("execution plan roots = %v, want %v", got, want)
	}
}

func writeFlow(t testing.TB, dir, name, appID string, tags []string, flowName string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	document := "appId: " + appID + "\nname: " + flowName + "\n"
	if len(tags) != 0 {
		document += "tags:\n"
		for _, tag := range tags {
			document += "  - " + tag + "\n"
		}
	}
	document += "---\n- back\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConfig(t testing.TB, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A glob that names a directory resolves against the path relative to the
// workspace root:
//
//	flows: ["smoke/*.yaml"]   → runs smoke/one
//	flows: ["**/*.yaml"]      → runs only the nested flows
//	flows: ["smoke/one.yaml"] → runs smoke/one
//
// Matching is relative to the workspace root, so directory components remain
// available to glob patterns. The default stays top-level: `*` does not cross a
// separator, while an explicit nested pattern selects files below the root.

func TestAGlobMayNameASubdirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "top.yaml", "com.example.top", nil, "Top")
	nested := writeFlow(t, filepath.Join(root, "smoke"), "one.yaml", "com.example.one", nil, "One")
	writeConfig(t, root, "flows:\n  - \"smoke/*.yaml\"\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{nested}) {
		t.Fatalf("selected = %v, want only %s", got, nested)
	}
}

func TestAGlobMayNameASubdirectoryFileExactly(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "top.yaml", "com.example.top", nil, "Top")
	nested := writeFlow(t, filepath.Join(root, "smoke"), "one.yaml", "com.example.one", nil, "One")
	writeConfig(t, root, "flows:\n  - \"smoke/one.yaml\"\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{nested}) {
		t.Fatalf("selected = %v, want only %s", got, nested)
	}
}

// `**/*.yaml` selects nested flows and not top-level ones because the pattern
// requires a directory separator.
func TestADoubleStarGlobDemandsADirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFlow(t, root, "top.yaml", "com.example.top", nil, "Top")
	first := writeFlow(t, filepath.Join(root, "smoke"), "one.yaml", "com.example.one", nil, "One")
	second := writeFlow(t, filepath.Join(root, "regress"), "two.yaml", "com.example.two", nil, "Two")
	writeConfig(t, root, "flows:\n  - \"**/*.yaml\"\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{second, first}) {
		t.Fatalf("selected = %v, want the two nested flows", got)
	}
}

// A config living in a subdirectory is still not a flow.
func TestANestedConfigIsNeverAFlow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := writeFlow(t, filepath.Join(root, "smoke"), "one.yaml", "com.example.one", nil, "One")
	writeConfig(t, filepath.Join(root, "smoke"), "flows:\n  - \"*\"\n")
	writeConfig(t, root, "flows:\n  - \"smoke/*\"\n")

	plan, err := Discover([]string{root}, Options{})
	if err != nil {
		t.Fatalf("Discover(dir) error = %v", err)
	}
	if got := plan.SelectedPaths(); !reflect.DeepEqual(got, []string{nested}) {
		t.Fatalf("selected = %v, want only %s", got, nested)
	}
}
