package capability

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

func TestCheckAnalyzesOnlySelectedRoots(t *testing.T) {
	t.Parallel()

	loader := newFakeFlowLoader(map[string]model.Flow{
		"/workspace/mobile.yaml": validFlow("/workspace/mobile.yaml"),
		"/workspace/web.yaml": {
			SchemaVersion: model.ASTVersionV0,
			Path:          "/workspace/web.yaml",
			Config: model.Config{
				URL:          "https://example.invalid",
				FieldSources: map[string]model.SourceInfo{"url": testSource("/workspace/web.yaml", 2, 1)},
			},
		},
	})

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{"/workspace/mobile.yaml"}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := len(report.Nodes), 1; got != want {
		t.Fatalf("node count = %d, want %d", got, want)
	}
	if loader.loads["/workspace/web.yaml"] != 0 {
		t.Fatalf("excluded Web flow was loaded: %#v", loader.loads)
	}
}

func TestCheckRejectsFeatureOutsideDeclaredPlatforms(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/mobile.yaml")
	css := "#submit"
	root.Commands = []model.Command{{
		Kind:   model.CommandTapOn,
		Source: testSource(root.Path, 4, 3),
		Selector: &model.ElementSelector{
			CSS:          &css,
			FieldSources: map[string]model.SourceInfo{"css": testSource(root.Path, 5, 5)},
		},
	}}
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader), WithPlatform("android"))
	violation := requireViolation(t, err, "unsupported_platform")
	if violation.FeatureKind != FeatureSelector || violation.FeatureName != "css" || violation.Source.Start.Line != 5 {
		t.Fatalf("platform violation = %#v", violation)
	}
	if !strings.Contains(violation.Message, "android") || !strings.Contains(violation.Message, "web") {
		t.Fatalf("platform violation message = %q, want selected and supported platforms", violation.Message)
	}
}

func TestCheckUsesPlatformsForPlannedFeature(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/keychain.yaml")
	root.Commands = []model.Command{{
		Kind:   model.CommandClearKeychain,
		Source: testSource(root.Path, 4, 3),
	}}
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
	plan := model.ExecutionPlan{SelectedRoots: []string{root.Path}}

	_, err := Check(context.Background(), plan, WithLoader(loader), WithPlatform(ExecutionPlatformAndroid))
	violation := requireViolation(t, err, "unsupported_platform")
	if violation.FeatureName != string(model.CommandClearKeychain) {
		t.Fatalf("platform violation = %#v", violation)
	}

	if _, err := Check(context.Background(), plan,
		WithLoader(newFakeFlowLoader(map[string]model.Flow{root.Path: root})),
		WithPlatform(ExecutionPlatformIOSSimulator)); err != nil {
		t.Fatalf("Check(ios-simulator): %v", err)
	}
}

func TestCheckAcceptsPlatformLimitedFeatureOnDeclaredPlatform(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/web.yaml")
	root.Config.AppID = ""
	root.Config.URL = "https://example.invalid"
	root.Config.FieldSources = map[string]model.SourceInfo{"url": testSource(root.Path, 2, 1)}
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

	if _, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader), WithPlatform("web")); err != nil {
		t.Fatalf("Check(web): %v", err)
	}
}

func TestCheckRejectsUnknownSelectedPlatformBeforeLoading(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader), WithPlatform("blackberry"))
	if err == nil || !strings.Contains(err.Error(), "unknown selected platform") {
		t.Fatalf("Check() error = %v, want unknown selected platform", err)
	}
	if len(loader.loads) != 0 {
		t.Fatalf("invalid platform loaded flows: %#v", loader.loads)
	}
}

func TestCheckLoadsCanonicalDiamondOnceAndRetainsEveryCallSite(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	root.Commands = []model.Command{
		flowLink("/workspace/root.yaml", "a.yaml", 4),
		flowLink("/workspace/root.yaml", "a.yaml", 5),
		flowLink("/workspace/root.yaml", "b.yaml", 6),
	}
	a := validFlow("/workspace/a.yaml")
	a.Commands = []model.Command{flowLink("/workspace/a.yaml", "c.yaml", 4)}
	b := validFlow("/workspace/b.yaml")
	b.Commands = []model.Command{flowLink("/workspace/b.yaml", "c.yaml", 4)}
	c := validFlow("/workspace/c.yaml")
	loader := newFakeFlowLoader(map[string]model.Flow{
		root.Path: root,
		a.Path:    a,
		b.Path:    b,
		c.Path:    c,
	})

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := len(report.Nodes), 4; got != want {
		t.Fatalf("node count = %d, want %d", got, want)
	}
	if got, want := len(report.Edges), 5; got != want {
		t.Fatalf("edge count = %d, want %d", got, want)
	}
	for path, count := range loader.loads {
		if count != 1 {
			t.Fatalf("flow %s load count = %d, want 1", path, count)
		}
	}
	repeatedLines := make([]int, 0, 2)
	for _, edge := range report.Edges {
		if edge.From == root.Path && edge.To == a.Path {
			repeatedLines = append(repeatedLines, edge.Source.Start.Line)
		}
	}
	if !reflect.DeepEqual(repeatedLines, []int{4, 5}) {
		t.Fatalf("repeated call-site source was not retained: %#v", report.Edges)
	}
}

func TestCheckRejectsActivePathCycleWithFullEdgeChain(t *testing.T) {
	t.Parallel()

	a := validFlow("/workspace/a.yaml")
	a.Commands = []model.Command{flowLink(a.Path, "b.yaml", 4)}
	b := validFlow("/workspace/b.yaml")
	b.Commands = []model.Command{flowLink(b.Path, "a.yaml", 8)}
	loader := newFakeFlowLoader(map[string]model.Flow{a.Path: a, b.Path: b})

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{a.Path}}, WithLoader(loader))
	violation := requireViolation(t, err, "active_cycle")
	if got, want := len(violation.Chain), 2; got != want {
		t.Fatalf("cycle chain length = %d, want %d (%#v)", got, want, violation.Chain)
	}
	if violation.Chain[0].From != a.Path || violation.Chain[0].To != b.Path ||
		violation.Chain[1].From != b.Path || violation.Chain[1].To != a.Path {
		t.Fatalf("cycle chain = %#v", violation.Chain)
	}
	if violation.Source.Start.Line != 8 || !strings.Contains(err.Error(), "/workspace/b.yaml:8") {
		t.Fatalf("cycle source/error = %#v / %v", violation.Source, err)
	}
}

func TestCheckRejectsSelfMissingAndDirectoryLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		link      string
		configure func(*fakeFlowLoader)
		code      string
	}{
		{name: "self", link: "root.yaml", code: "active_cycle"},
		{name: "missing", link: "missing.yaml", code: "missing_link"},
		{
			name:      "directory",
			link:      "flows",
			configure: func(loader *fakeFlowLoader) { loader.directories["/workspace/flows"] = true },
			code:      "directory_link",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			root.Commands = []model.Command{flowLink(root.Path, test.link, 7)}
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			if test.configure != nil {
				test.configure(loader)
			}
			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
			violation := requireViolation(t, err, test.code)
			if violation.Source.Start.Line != 7 || len(violation.Chain) != 1 {
				t.Fatalf("violation source = %#v", violation)
			}
		})
	}
}

func TestCheckTraversesHooksAndNestedCommandsRegardlessOfCondition(t *testing.T) {
	t.Parallel()

	locations := []struct {
		name  string
		build func(*model.Flow)
		line  int
	}{
		{
			name: "root command",
			line: 4,
			build: func(flow *model.Flow) {
				flow.Commands = []model.Command{deferredFeatureCommand(flow.Path, 4)}
			},
		},
		{
			name: "on flow start",
			line: 5,
			build: func(flow *model.Flow) {
				flow.Config.OnFlowStart = []model.Command{deferredFeatureCommand(flow.Path, 5)}
			},
		},
		{
			name: "on flow complete",
			line: 6,
			build: func(flow *model.Flow) {
				flow.Config.OnFlowComplete = []model.Command{deferredFeatureCommand(flow.Path, 6)}
			},
		},
		{
			name: "repeat child with false condition",
			line: 9,
			build: func(flow *model.Flow) {
				condition := "false"
				flow.Commands = []model.Command{{
					Kind:      model.CommandRepeat,
					Condition: &model.Condition{ScriptCondition: &condition},
					Children:  []model.Command{deferredFeatureCommand(flow.Path, 9)},
				}}
			},
		},
		{
			name: "retry child",
			line: 10,
			build: func(flow *model.Flow) {
				flow.Commands = []model.Command{{Kind: model.CommandRetry, Children: []model.Command{deferredFeatureCommand(flow.Path, 10)}}}
			},
		},
	}
	for _, location := range locations {
		location := location
		t.Run(location.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			location.build(&root)
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
				WithLoader(loader), WithRegistry(deferringCSSRegistry(t)))
			violation := requireViolation(t, err, "unsupported_capability")
			if violation.FeatureKind != FeatureSelector || violation.FeatureName != "css" || violation.Source.Start.Line != location.line {
				t.Fatalf("deferred-feature violation = %#v", violation)
			}
		})
	}
}

func TestCheckRejectsReachableURLCSSDevtoolsAndUnknownConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		configure   func(*model.Flow)
		featureKind FeatureKind
		featureName string
		line        int
	}{
		{
			name: "web URL",
			configure: func(flow *model.Flow) {
				flow.Config.AppID = ""
				flow.Config.URL = "https://example.invalid"
				flow.Config.FieldSources = map[string]model.SourceInfo{"url": testSource(flow.Path, 2, 1)}
			},
			featureKind: FeatureConfigExtension,
			featureName: "url",
			line:        2,
		},
		{
			name: "CSS selector",
			configure: func(flow *model.Flow) {
				css := "#submit"
				flow.Commands = []model.Command{{
					Kind:   model.CommandTapOn,
					Source: testSource(flow.Path, 4, 3),
					Selector: &model.ElementSelector{
						CSS:          &css,
						FieldSources: map[string]model.SourceInfo{"css": testSource(flow.Path, 5, 5)},
					},
				}}
			},
			featureKind: FeatureSelector,
			featureName: "css",
			line:        5,
		},
		{
			name: "devtools hierarchy",
			configure: func(flow *model.Flow) {
				flow.Config.Ext = map[string]any{"androidWebViewHierarchy": "devtools"}
				flow.Config.FieldSources = map[string]model.SourceInfo{"androidWebViewHierarchy": testSource(flow.Path, 3, 1)}
			},
			featureKind: FeatureConfigExtension,
			featureName: "androidWebViewHierarchy=devtools",
			line:        3,
		},
		{
			name: "unknown config extension",
			configure: func(flow *model.Flow) {
				flow.Config.Ext = map[string]any{"futureMagic": true}
				flow.Config.FieldSources = map[string]model.SourceInfo{"futureMagic": testSource(flow.Path, 3, 1)}
			},
			featureKind: FeatureConfigExtension,
			featureName: "futureMagic",
			line:        3,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			test.configure(&root)
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			// The sentinel table covers every reachable config and selector
			// position.
			registry := deferringRegistry(t,
				featureRef{FeatureSelector, "css"},
				featureRef{FeatureConfigExtension, "url"},
				featureRef{FeatureConfigExtension, "androidWebViewHierarchy=devtools"})
			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
				WithLoader(loader), WithRegistry(registry))
			violation := requireViolation(t, err, "unsupported_capability")
			if violation.FeatureKind != test.featureKind || violation.FeatureName != test.featureName || violation.Source.Start.Line != test.line {
				t.Fatalf("capability violation = %#v", violation)
			}
		})
	}
}

func TestCheckClassifiesJSEngineValuesFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		value       any
		wantFeature string
	}{
		{name: "documented GraalJS token", value: "graaljs"},
		{name: "removed Rhino token", value: "rhino", wantFeature: "jsEngine=rhino"},
		{name: "unknown token", value: "future", wantFeature: "jsEngine=future"},
		{name: "non-string token", value: true, wantFeature: "jsEngine=<invalid>"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			root.Config.Ext = map[string]any{"jsEngine": test.value}
			root.Config.FieldSources = map[string]model.SourceInfo{"jsEngine": testSource(root.Path, 2, 1)}
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
			if test.wantFeature == "" {
				if err != nil {
					t.Fatalf("documented jsEngine token rejected: %v", err)
				}
				return
			}
			violation := requireViolation(t, err, "unsupported_capability")
			if violation.FeatureKind != FeatureConfigExtension || violation.FeatureName != test.wantFeature || violation.Source.Start.Line != 2 {
				t.Fatalf("jsEngine violation = %#v", violation)
			}
		})
	}
}

func TestCheckRejectsUnsupportedFeatureInLinkedChildWithEdgeChain(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	root.Commands = []model.Command{flowLink(root.Path, "child.yaml", 4)}
	child := validFlow("/workspace/child.yaml")
	child.Config.OnFlowComplete = []model.Command{deferredFeatureCommand(child.Path, 11)}
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root, child.Path: child})

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader), WithRegistry(deferringCSSRegistry(t)))
	violation := requireViolation(t, err, "unsupported_capability")
	if len(violation.Chain) != 1 || violation.Chain[0].Source.Start.Line != 4 || violation.Source.Start.Line != 11 {
		t.Fatalf("child violation source = %#v", violation)
	}
}

func TestCheckUsesIterativeStackSafeTraversal(t *testing.T) {
	t.Parallel()

	const depth = 10000
	flows := make(map[string]model.Flow, depth)
	for index := 0; index < depth; index++ {
		path := fmt.Sprintf("/workspace/deep/%05d.yaml", index)
		flow := validFlow(path)
		if index+1 < depth {
			flow.Commands = []model.Command{flowLink(path, fmt.Sprintf("%05d.yaml", index+1), 3)}
		}
		flows[path] = flow
	}
	loader := newFakeFlowLoader(flows)

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{"/workspace/deep/00000.yaml"}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check deep graph: %v", err)
	}
	if len(report.Nodes) != depth || len(report.Edges) != depth-1 {
		t.Fatalf("deep graph sizes = nodes %d edges %d", len(report.Nodes), len(report.Edges))
	}
}

func TestCheckFailsBeforeLoadingForInvalidRegistryPlanOrCancellation(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	tests := []struct {
		name    string
		context context.Context
		plan    model.ExecutionPlan
		options []Option
		want    string
	}{
		{
			name:    "empty plan",
			context: context.Background(),
			plan:    model.ExecutionPlan{},
			want:    "selected root",
		},
		{
			name:    "invalid registry",
			context: context.Background(),
			plan:    model.ExecutionPlan{SelectedRoots: []string{root.Path}},
			options: []Option{WithRegistry(NewRegistry(RegistryVersionV0, DefaultRegistry().Entries()[1:]))},
			want:    "missing registry entry",
		},
		{
			name:    "cancelled",
			context: cancelledContext(),
			plan:    model.ExecutionPlan{SelectedRoots: []string{root.Path}},
			want:    context.Canceled.Error(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			options := append([]Option{WithLoader(loader)}, test.options...)
			_, err := Check(test.context, test.plan, options...)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Check error = %v, want substring %q", err, test.want)
			}
			if len(loader.loads) != 0 {
				t.Fatalf("loader called before precondition failure: %#v", loader.loads)
			}
		})
	}
}

func TestCheckRejectsNonRegularPathWithStableViolation(t *testing.T) {
	t.Parallel()

	loader := &canonicalErrorLoader{err: ErrFlowNonRegular}
	_, err := Check(
		context.Background(),
		model.ExecutionPlan{SelectedRoots: []string{"/workspace/pipe"}},
		WithLoader(loader),
	)
	violation := requireViolation(t, err, "non_regular_link")
	if violation.Source.Path != "/workspace/pipe" || loader.loadCalls != 0 {
		t.Fatalf("non-regular violation/load calls = %#v / %d", violation, loader.loadCalls)
	}
}

func TestCheckRejectsMismatchedLoadedASTVersionBeforeAnalysis(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	root.SchemaVersion = "flowbaton.ast/v99"
	root.Source = testSource(root.Path, 1, 1)
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
	violation := requireViolation(t, err, "unsupported_ast_version")
	if violation.Source.Path != root.Path || violation.Source.Start.Line != 1 {
		t.Fatalf("AST version violation = %#v", violation)
	}
}

func validFlow(path string) model.Flow {
	return model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          path,
		Config:        model.Config{AppID: "com.example.app", Source: testSource(path, 1, 1)},
	}
}

func flowLink(from, relative string, line int) model.Command {
	resolved := path.Join(path.Dir(from), relative)
	source := testSource(from, line, 3)
	return model.Command{
		Kind:   model.CommandRunFlow,
		Source: source,
		Links: []model.FileLink{{
			Kind:         model.FileLinkFlow,
			Path:         relative,
			ResolvedPath: resolved,
			Source:       source,
		}},
	}
}

// deferredFeatureCommand builds a command carrying the css selector rejected
// by the traversal and source-retention tests. deferringRegistry supplies css
// as an explicit deferred sentinel even though the product registry marks it
// implemented.
func deferredFeatureCommand(path string, line int) model.Command {
	css := "#target"
	return model.Command{
		Kind:   model.CommandTapOn,
		Source: testSource(path, line, 3),
		Selector: &model.ElementSelector{
			CSS:          &css,
			FieldSources: map[string]model.SourceInfo{"css": testSource(path, line, 3)},
		},
	}
}

// featureRef names one registry entry.
type featureRef struct {
	kind FeatureKind
	name string
}

// deferringRegistry is the default registry with the named entries forced back
// to deferred.
//
// Tests whose subject is *where* the checker looks — into hooks, past a false
// condition, down recursive selectors, across a flow link — need some feature
// to be deferred, but they do not care which. Each test pins its own sentinel
// so traversal coverage remains independent of the default registry's current
// runtime statuses.
//
// It fails when a name matches nothing, because a silently-unmatched name
// yields a registry that defers nothing at all, and a traversal test that finds
// no violation would then read as a traversal bug.
func deferringRegistry(t *testing.T, refs ...featureRef) Registry {
	t.Helper()
	wanted := make(map[featureRef]struct{}, len(refs))
	for _, ref := range refs {
		wanted[ref] = struct{}{}
	}
	entries := DefaultRegistry().Entries()
	matched := 0
	for index := range entries {
		ref := featureRef{kind: entries[index].Kind, name: entries[index].Name}
		if _, deferred := wanted[ref]; !deferred {
			continue
		}
		entries[index].RuntimeStatus = RuntimeStatusDeferred
		entries[index].Reason = "deferred by this test"
		matched++
	}
	if matched != len(wanted) {
		t.Fatalf("deferringRegistry matched %d of %d features: %v", matched, len(wanted), refs)
	}
	registry := NewRegistry(RegistryVersionV0, entries)
	if err := registry.Validate(); err != nil {
		t.Fatalf("test registry is invalid: %v", err)
	}
	return registry
}

// deferringCSSRegistry is the common case: css alone.
func deferringCSSRegistry(t *testing.T) Registry {
	t.Helper()
	return deferringRegistry(t, featureRef{FeatureSelector, "css"})
}

func testSource(path string, line, column int) model.SourceInfo {
	return model.SourceInfo{
		Path:  path,
		Start: model.Position{Line: line, Column: column, Offset: line*10 + column},
		End:   model.Position{Line: line, Column: column + 1, Offset: line*10 + column + 1},
	}
}

func requireViolation(t *testing.T, err error, code string) Violation {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s violation, got nil", code)
	}
	var violation Violation
	if !errors.As(err, &violation) {
		t.Fatalf("error type = %T, want Violation (%v)", err, err)
	}
	if violation.Code != code {
		t.Fatalf("violation code = %q, want %q (%v)", violation.Code, code, err)
	}
	return violation
}

type fakeFlowLoader struct {
	flows       map[string]model.Flow
	resources   map[string]bool
	directories map[string]bool
	loads       map[string]int
}

func newFakeFlowLoader(flows map[string]model.Flow) *fakeFlowLoader {
	// The fake models a POSIX virtual filesystem so fixture identity is
	// independent of the host running the tests.
	normalizedFlows := make(map[string]model.Flow, len(flows))
	for fixturePath, flow := range flows {
		normalizedFlows[path.Clean(fixturePath)] = flow
	}
	return &fakeFlowLoader{flows: normalizedFlows, resources: map[string]bool{}, directories: map[string]bool{}, loads: map[string]int{}}
}

func (loader *fakeFlowLoader) Canonical(_ context.Context, fixturePath string) (string, error) {
	canonical := path.Clean(fixturePath)
	if loader.directories[canonical] {
		return "", ErrFlowDirectory
	}
	_, flowExists := loader.flows[canonical]
	if !flowExists && !loader.resources[canonical] {
		return "", fs.ErrNotExist
	}
	return canonical, nil
}

func (loader *fakeFlowLoader) Load(_ context.Context, canonicalPath string) (model.Flow, error) {
	flow, exists := loader.flows[canonicalPath]
	if !exists {
		return model.Flow{}, fs.ErrNotExist
	}
	loader.loads[canonicalPath]++
	return flow, nil
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

type canonicalErrorLoader struct {
	err       error
	loadCalls int
}

func (loader *canonicalErrorLoader) Canonical(_ context.Context, _ string) (string, error) {
	return "", loader.err
}

func (loader *canonicalErrorLoader) Load(_ context.Context, _ string) (model.Flow, error) {
	loader.loadCalls++
	return model.Flow{}, loader.err
}

func TestFakeLoaderContractIsDeterministic(t *testing.T) {
	t.Parallel()

	canonicalPath := path.Clean("/workspace/root.yaml")
	fixturePath := "/workspace/fixtures/../root.yaml"
	loader := newFakeFlowLoader(map[string]model.Flow{fixturePath: validFlow(canonicalPath)})
	canonical, err := loader.Canonical(context.Background(), "/workspace/./root.yaml")
	if err != nil || canonical != canonicalPath {
		t.Fatalf("canonical = %q, err = %v", canonical, err)
	}
	first, err := loader.Load(context.Background(), canonical)
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	second, err := loader.Load(context.Background(), canonical)
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("fake loader returned nondeterministic flows")
	}
}
