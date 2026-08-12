package engine

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestCompileProgramCompilesDiamondChildFirstOnceWithoutEffects(t *testing.T) {
	t.Parallel()

	program := diamondCompileProgram()
	driver := enginetest.NewFakeDriver()
	var compileOrder []string
	executeCalls := 0
	registry := mustCompileProgramRegistry(t,
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandAction, effectClass: EffectHostMutation,
			compile: func(_ context.Context, compileCtx compileContext, command model.Command) (any, error) {
				name := command.Arguments.(string)
				compileOrder = append(compileOrder, compileCtx.FlowPath()+":"+name)
				return name, nil
			},
			execute: func(ctx context.Context, _ *executionState, _ evaluatedDispatch) (commandEffect, error) {
				executeCalls++
				err := driver.LaunchApp(ctx, device.LaunchAppRequest{AppID: "must-not-launch"})
				return commandEffect{effectClass: EffectHostMutation}, err
			},
		},
		handlerSpec{
			evaluate: identityEvaluator,
			keyword:  model.CommandRunFlow, effectClass: EffectComposite,
			compile: func(_ context.Context, compileCtx compileContext, command model.Command) (any, error) {
				compileOrder = append(compileOrder, compileCtx.FlowPath()+":run")
				if len(command.Links) != 1 {
					return nil, errors.New("runFlow requires one link")
				}
				return compileCtx.RequireFlow(command.Links[0])
			},
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				executeCalls++
				return commandEffect{effectClass: EffectComposite}, nil
			},
		},
	)

	compiled, err := compileProgram(context.Background(), program, registry)
	if err != nil {
		t.Fatalf("compileProgram() error: %v", err)
	}
	if got, want := compileOrder, []string{
		"/workspace/shared.yaml:shared",
		"/workspace/left.yaml:left", "/workspace/left.yaml:run",
		"/workspace/right.yaml:right", "/workspace/right.yaml:run",
		"/workspace/root.yaml:start", "/workspace/root.yaml:run", "/workspace/root.yaml:run",
		"/workspace/root.yaml:body", "/workspace/root.yaml:complete",
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("compile order = %#v, want %#v", got, want)
	}
	if executeCalls != 0 || len(driver.Actions()) != 0 {
		t.Fatalf("compileProgram allowed effects: execute=%d driver=%v", executeCalls, driver.Actions())
	}

	if got, want := compiled.Roots(), []string{"/workspace/root.yaml", "/workspace/root.yaml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Roots() = %#v, want duplicate selections %#v", got, want)
	}
	roots := compiled.Roots()
	roots[0] = "caller mutation"
	if compiled.Roots()[0] != "/workspace/root.yaml" {
		t.Fatal("compiled roots exposed mutable backing storage")
	}

	root, ok := compiled.Flow("/workspace/root.yaml")
	if !ok {
		t.Fatal("compiled root missing")
	}
	if len(root.onStart) != 1 || len(root.body) != 3 || len(root.onComplete) != 1 {
		t.Fatalf("compiled root sequences = start %d body %d complete %d, want 1/3/1", len(root.onStart), len(root.body), len(root.onComplete))
	}
	if root.config.Env["ROOT"] != "kept-as-config" {
		t.Fatalf("compiled config = %#v, want preserved environment without synthetic commands", root.config)
	}
	left, _ := compiled.Flow("/workspace/left.yaml")
	right, _ := compiled.Flow("/workspace/right.yaml")
	shared, _ := compiled.Flow("/workspace/shared.yaml")
	if left.body[1].value != shared || right.body[1].value != shared {
		t.Fatalf("diamond links did not share compiled child: left=%p right=%p shared=%p", left.body[1].value, right.body[1].value, shared)
	}
	if got := len(compiled.flows); got != 4 {
		t.Fatalf("compiled flow count = %d, want exactly four unique flows", got)
	}
}

func TestCompileProgramRejectsInvalidGraphAndResolutionInputs(t *testing.T) {
	t.Parallel()

	emptyRegistry := mustCompileProgramRegistry(t)
	valid := singleCompileProgram(model.Flow{SchemaVersion: model.ASTVersionV0, Path: "/workspace/root.yaml"})

	tests := []struct {
		name     string
		ctx      context.Context
		program  *Program
		registry handlerRegistry
	}{
		{name: "nil context", program: valid, registry: emptyRegistry},
		{name: "nil program", ctx: context.Background(), registry: emptyRegistry},
		{name: "nil registry", ctx: context.Background(), program: valid},
		{
			name: "cycle", ctx: context.Background(), registry: emptyRegistry,
			program: &Program{
				roots: []string{"/workspace/root.yaml"},
				paths: []string{"/workspace/root.yaml", "/workspace/child.yaml"},
				flows: map[string]model.Flow{
					"/workspace/root.yaml":  {SchemaVersion: model.ASTVersionV0, Path: "/workspace/root.yaml"},
					"/workspace/child.yaml": {SchemaVersion: model.ASTVersionV0, Path: "/workspace/child.yaml"},
				},
				graph: capability.Report{
					Roots: []string{"/workspace/root.yaml"},
					Nodes: []capability.GraphNode{{Path: "/workspace/root.yaml"}, {Path: "/workspace/child.yaml"}},
					Edges: []capability.GraphEdge{
						{From: "/workspace/root.yaml", To: "/workspace/child.yaml", Kind: model.FileLinkFlow},
						{From: "/workspace/child.yaml", To: "/workspace/root.yaml", Kind: model.FileLinkFlow},
					},
				},
			},
		},
		{
			name: "missing prepared flow", ctx: context.Background(), registry: emptyRegistry,
			program: &Program{
				roots: []string{"/workspace/root.yaml"}, paths: []string{"/workspace/root.yaml"},
				flows: map[string]model.Flow{},
				graph: capability.Report{Roots: []string{"/workspace/root.yaml"}, Nodes: []capability.GraphNode{{Path: "/workspace/root.yaml"}}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled, err := compileProgram(test.ctx, test.program, test.registry)
			if compiled != nil || err == nil {
				t.Fatalf("compileProgram() = %#v, %v; want nil configuration error", compiled, err)
			}
			var configurationError *ConfigurationError
			if !errors.As(err, &configurationError) {
				t.Fatalf("compileProgram() error = %T %v, want *ConfigurationError", err, err)
			}
		})
	}
}

func TestCompileProgramRejectsMissingAndAmbiguousFlowLinks(t *testing.T) {
	t.Parallel()

	runFlowRegistry := mustCompileProgramRegistry(t, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandRunFlow, effectClass: EffectComposite,
		compile: func(_ context.Context, compileCtx compileContext, command model.Command) (any, error) {
			return compileCtx.RequireFlow(command.Links[0])
		},
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectComposite}, nil
		},
	})
	missing := singleCompileProgram(model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: "/workspace/root.yaml",
		Commands: []model.Command{{
			Kind:  model.CommandRunFlow,
			Links: []model.FileLink{{Kind: model.FileLinkFlow, Path: "missing.yaml"}},
		}},
	})
	directory := t.TempDir()
	rootPath := filepath.Join(directory, "root.yaml")
	leftPath := filepath.Join(directory, "left.yaml")
	rightPath := filepath.Join(directory, "right.yaml")
	ambiguousLink := model.FileLink{
		Kind: model.FileLinkFlow, Path: "left.yaml", ResolvedPath: rightPath,
	}
	ambiguous := &Program{
		roots: []string{rootPath},
		paths: []string{rootPath, leftPath, rightPath},
		flows: map[string]model.Flow{
			rootPath: {
				SchemaVersion: model.ASTVersionV0, Path: rootPath,
				Commands: []model.Command{{Kind: model.CommandRunFlow, Links: []model.FileLink{ambiguousLink}}},
			},
			leftPath:  {SchemaVersion: model.ASTVersionV0, Path: leftPath},
			rightPath: {SchemaVersion: model.ASTVersionV0, Path: rightPath},
		},
		aliases: map[string]string{
			leftPath:  leftPath,
			rightPath: rightPath,
		},
		graph: capability.Report{
			Roots: []string{rootPath},
			Nodes: []capability.GraphNode{{Path: rootPath}, {Path: leftPath}, {Path: rightPath}},
			Edges: []capability.GraphEdge{
				{From: rootPath, To: leftPath, Kind: model.FileLinkFlow},
				{From: rootPath, To: rightPath, Kind: model.FileLinkFlow},
			},
		},
	}

	for _, test := range []struct {
		name    string
		program *Program
	}{
		{name: "missing", program: missing},
		{name: "ambiguous", program: ambiguous},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled, err := compileProgram(context.Background(), test.program, runFlowRegistry)
			if compiled != nil || err == nil {
				t.Fatalf("compileProgram() = %#v, %v; want link rejection", compiled, err)
			}
			var configurationError *ConfigurationError
			if !errors.As(err, &configurationError) {
				t.Fatalf("compileProgram() error = %T %v, want *ConfigurationError", err, err)
			}
		})
	}
}

func TestCompileProgramHonorsCancellationBeforeAndAfterCompiler(t *testing.T) {
	t.Parallel()

	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0, Path: "/workspace/root.yaml",
		Commands: []model.Command{{Kind: model.CommandAction}, {Kind: model.CommandAction}},
	}
	program := singleCompileProgram(flow)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	compileCalls := 0
	registry := mustCompileProgramRegistry(t, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) {
			compileCalls++
			return struct{}{}, nil
		}),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	})
	if compiled, err := compileProgram(ctx, program, registry); compiled != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled compileProgram() = %#v, %v; want nil, context.Canceled", compiled, err)
	}
	if compileCalls != 0 {
		t.Fatalf("pre-cancelled compiler calls = %d, want zero", compileCalls)
	}

	ctx, cancel = context.WithCancel(context.Background())
	compileCalls = 0
	registry = mustCompileProgramRegistry(t, handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandAction, effectClass: EffectObserved,
		compile: pureCompiler(func(model.Command) (any, error) {
			compileCalls++
			cancel()
			return struct{}{}, nil
		}),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectObserved}, nil
		},
	})
	if compiled, err := compileProgram(ctx, program, registry); compiled != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("compiler-cancelled compileProgram() = %#v, %v; want nil, context.Canceled", compiled, err)
	}
	if compileCalls != 1 {
		t.Fatalf("compiler-cancelled compiler calls = %d, want one", compileCalls)
	}
}

func diamondCompileProgram() *Program {
	const (
		root   = "/workspace/root.yaml"
		left   = "/workspace/left.yaml"
		right  = "/workspace/right.yaml"
		shared = "/workspace/shared.yaml"
	)
	flowRef := func(path string) model.Command {
		return model.Command{Kind: model.CommandRunFlow, Links: []model.FileLink{{
			Kind: model.FileLinkFlow, Path: path, ResolvedPath: "/workspace/" + path,
		}}}
	}
	action := func(name string) model.Command {
		return model.Command{Kind: model.CommandAction, Arguments: name}
	}
	return &Program{
		roots: []string{root, root},
		paths: []string{root, left, shared, right},
		flows: map[string]model.Flow{
			root: {
				SchemaVersion: model.ASTVersionV0, Path: root,
				Config: model.Config{
					Env:            map[string]string{"ROOT": "kept-as-config"},
					OnFlowStart:    []model.Command{action("start")},
					OnFlowComplete: []model.Command{action("complete")},
				},
				Commands: []model.Command{flowRef("left.yaml"), flowRef("right.yaml"), action("body")},
			},
			left: {
				SchemaVersion: model.ASTVersionV0, Path: left,
				Commands: []model.Command{action("left"), flowRef("shared.yaml")},
			},
			right: {
				SchemaVersion: model.ASTVersionV0, Path: right,
				Commands: []model.Command{action("right"), flowRef("shared.yaml")},
			},
			shared: {SchemaVersion: model.ASTVersionV0, Path: shared, Commands: []model.Command{action("shared")}},
		},
		aliases: map[string]string{root: root, left: left, right: right, shared: shared},
		graph: capability.Report{
			Roots: []string{root, root},
			Nodes: []capability.GraphNode{{Path: root}, {Path: left}, {Path: shared}, {Path: right}},
			Edges: []capability.GraphEdge{
				{From: root, To: left, Kind: model.FileLinkFlow},
				{From: left, To: shared, Kind: model.FileLinkFlow},
				{From: root, To: right, Kind: model.FileLinkFlow},
				{From: right, To: shared, Kind: model.FileLinkFlow},
				{From: root, To: "/workspace/helper.js", Kind: model.FileLinkScript},
			},
		},
	}
}

func singleCompileProgram(flow model.Flow) *Program {
	return &Program{
		roots: []string{flow.Path}, paths: []string{flow.Path},
		flows:   map[string]model.Flow{flow.Path: cloneFlow(flow)},
		aliases: map[string]string{flow.Path: flow.Path},
		graph:   capability.Report{Roots: []string{flow.Path}, Nodes: []capability.GraphNode{{Path: flow.Path}}},
	}
}

func mustCompileProgramRegistry(t *testing.T, specs ...handlerSpec) handlerRegistry {
	t.Helper()
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error: %v", err)
	}
	return registry
}
