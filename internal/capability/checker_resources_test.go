package capability

import (
	"context"
	"path"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestCheckValidatesScriptAndMediaResourcesWithoutParsingThemAsFlows(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	root.Commands = []model.Command{
		resourceLink(model.CommandRunScript, model.FileLinkScript, root.Path, "scripts/setup.js", 4),
		{
			Kind: model.CommandAddMedia,
			Links: []model.FileLink{
				resourceFileLink(model.FileLinkMedia, root.Path, "media/a.png", 5),
				resourceFileLink(model.FileLinkMedia, root.Path, "media/b.mp4", 6),
			},
		},
	}
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
	loader.resources["/workspace/scripts/setup.js"] = true
	loader.resources["/workspace/media/a.png"] = true
	loader.resources["/workspace/media/b.mp4"] = true

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if got, want := len(report.Edges), 3; got != want {
		t.Fatalf("resource edge count = %d, want %d", got, want)
	}
	if len(loader.loads) != 1 || loader.loads[root.Path] != 1 {
		t.Fatalf("script/media resources were parsed as flows: %#v", loader.loads)
	}
}

func TestCheckRejectsInvalidScriptMediaAndRetryLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		command   model.CommandKeyword
		kind      model.FileLinkKind
		link      string
		configure func(*fakeFlowLoader, string)
		code      string
	}{
		{name: "missing script", command: model.CommandRunScript, kind: model.FileLinkScript, link: "scripts/missing.js", code: "missing_link"},
		{name: "missing media", command: model.CommandAddMedia, kind: model.FileLinkMedia, link: "media/missing.png", code: "missing_link"},
		{name: "missing retry flow", command: model.CommandRetry, kind: model.FileLinkFlow, link: "missing.yaml", code: "missing_link"},
		{
			name: "script directory", command: model.CommandRunScript, kind: model.FileLinkScript, link: "scripts",
			configure: func(loader *fakeFlowLoader, resolved string) { loader.directories[resolved] = true }, code: "directory_link",
		},
		{
			name: "media directory", command: model.CommandAddMedia, kind: model.FileLinkMedia, link: "media",
			configure: func(loader *fakeFlowLoader, resolved string) { loader.directories[resolved] = true }, code: "directory_link",
		},
		{
			name: "retry directory", command: model.CommandRetry, kind: model.FileLinkFlow, link: "flows",
			configure: func(loader *fakeFlowLoader, resolved string) { loader.directories[resolved] = true }, code: "directory_link",
		},
		{name: "self script", command: model.CommandRunScript, kind: model.FileLinkScript, link: "root.yaml", code: "self_link"},
		{name: "self media", command: model.CommandAddMedia, kind: model.FileLinkMedia, link: "root.yaml", code: "self_link"},
		{name: "self retry", command: model.CommandRetry, kind: model.FileLinkFlow, link: "root.yaml", code: "active_cycle"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			root.Commands = []model.Command{resourceLink(test.command, test.kind, root.Path, test.link, 7)}
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			resolved := path.Join(path.Dir(root.Path), test.link)
			if test.configure != nil {
				test.configure(loader, resolved)
			}
			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
			violation := requireViolation(t, err, test.code)
			if violation.Source.Start.Line != 7 || len(violation.Chain) != 1 {
				t.Fatalf("link violation source = %#v", violation)
			}
		})
	}
}

func TestCheckFindsFlowLinksInsideHooksRepeatAndRetry(t *testing.T) {
	t.Parallel()

	root := validFlow("/workspace/root.yaml")
	root.Config.OnFlowStart = []model.Command{flowLink(root.Path, "hook.yaml", 3)}
	root.Commands = []model.Command{
		{Kind: model.CommandRepeat, Children: []model.Command{flowLink(root.Path, "repeat.yaml", 7)}},
		{Kind: model.CommandRetry, Children: []model.Command{flowLink(root.Path, "retry.yaml", 10)}},
	}
	flows := map[string]model.Flow{
		root.Path:                root,
		"/workspace/hook.yaml":   validFlow("/workspace/hook.yaml"),
		"/workspace/repeat.yaml": validFlow("/workspace/repeat.yaml"),
		"/workspace/retry.yaml":  validFlow("/workspace/retry.yaml"),
	}
	loader := newFakeFlowLoader(flows)

	report, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}}, WithLoader(loader))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(report.Nodes) != 4 || len(report.Edges) != 3 {
		t.Fatalf("nested graph = nodes %d edges %d", len(report.Nodes), len(report.Edges))
	}
	for path := range flows {
		if loader.loads[path] != 1 {
			t.Fatalf("nested flow %s load count = %d", path, loader.loads[path])
		}
	}
}

func TestCheckRejectsCSSThroughConditionsAndRecursiveSelectors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		selector func(string) model.Command
		line     int
	}{
		{
			name: "condition visible",
			line: 8,
			selector: func(path string) model.Command {
				return model.Command{Kind: model.CommandRunFlow, Condition: &model.Condition{
					Visible: cssSelector(path, 8),
				}}
			},
		},
		{
			name: "condition not visible",
			line: 9,
			selector: func(path string) model.Command {
				return model.Command{Kind: model.CommandRunScript, Condition: &model.Condition{
					NotVisible: cssSelector(path, 9),
				}}
			},
		},
		{
			name: "contains descendants",
			line: 12,
			selector: func(path string) model.Command {
				return model.Command{Kind: model.CommandTapOn, Selector: &model.ElementSelector{
					ContainsDescendants: []model.ElementSelector{*cssSelector(path, 12)},
				}}
			},
		},
		{
			name: "recursive below",
			line: 13,
			selector: func(path string) model.Command {
				return model.Command{Kind: model.CommandTapOn, Selector: &model.ElementSelector{Below: &model.ElementSelector{ChildOf: cssSelector(path, 13)}}}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := validFlow("/workspace/root.yaml")
			root.Commands = []model.Command{test.selector(root.Path)}
			loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})
			_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
				WithLoader(loader), WithRegistry(deferringCSSRegistry(t)))
			violation := requireViolation(t, err, "unsupported_capability")
			if violation.FeatureName != "css" || violation.Source.Start.Line != test.line {
				t.Fatalf("CSS violation = %#v", violation)
			}
		})
	}
}

// webPlatformCondition is a command gated on `platform: web`.
func webPlatformCondition(path string) model.Flow {
	flow := validFlow(path)
	platform := model.PlatformWeb
	flow.Commands = []model.Command{{
		Kind: model.CommandRepeat,
		Condition: &model.Condition{
			Platform:     &platform,
			FieldSources: map[string]model.SourceInfo{"platform": testSource(path, 8, 7)},
		},
		Children: []model.Command{{Kind: model.CommandBack}},
	}}
	return flow
}

// A platform named in a condition is a capability claim like any other, and it
// is checked against the registry rather than waved through because it sits
// inside a condition the flow may never take.
func TestCheckChecksThePlatformNamedInACondition(t *testing.T) {
	t.Parallel()

	root := webPlatformCondition("/workspace/root.yaml")
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

	_, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader), WithRegistry(deferringRegistry(t, featureRef{FeatureDevicePlatform, "web"})))
	violation := requireViolation(t, err, "unsupported_capability")
	if violation.FeatureKind != FeatureDevicePlatform || violation.FeatureName != "web" || violation.Source.Start.Line != 8 {
		t.Fatalf("Web condition violation = %#v", violation)
	}
}

// The counterpart against the live registry: the Web/CDP driver exists, so a
// flow that branches on `platform: web` must survive preflight. This is the
// half that catches a stale deferred status — the rejection above cannot,
// because it supplies its own.
func TestCheckAcceptsTheWebPlatformCondition(t *testing.T) {
	t.Parallel()

	root := webPlatformCondition("/workspace/root.yaml")
	loader := newFakeFlowLoader(map[string]model.Flow{root.Path: root})

	if _, err := Check(context.Background(), model.ExecutionPlan{SelectedRoots: []string{root.Path}},
		WithLoader(loader)); err != nil {
		t.Fatalf("the web platform condition was rejected: %v", err)
	}
}

func resourceLink(command model.CommandKeyword, kind model.FileLinkKind, from, relative string, line int) model.Command {
	return model.Command{Kind: command, Links: []model.FileLink{resourceFileLink(kind, from, relative, line)}}
}

func resourceFileLink(kind model.FileLinkKind, from, relative string, line int) model.FileLink {
	source := testSource(from, line, 3)
	return model.FileLink{
		Kind: kind, Path: relative, ResolvedPath: path.Join(path.Dir(from), relative), Source: source,
	}
}

func cssSelector(path string, line int) *model.ElementSelector {
	css := "#deferred"
	return &model.ElementSelector{
		CSS:          &css,
		FieldSources: map[string]model.SourceInfo{"css": testSource(path, line, 5)},
	}
}
