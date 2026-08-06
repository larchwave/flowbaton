package engine

import (
	"context"
	"sort"
	"strings"

	"github.com/nohavewho/flowbaton/internal/model"
)

// compileContext is the effect-free structural context supplied to handler
// compilers for one containing flow.
type compileContext struct {
	containingFlow string
	requireFlow    func(model.FileLink) (*compiledFlow, error)
}

func (c compileContext) FlowPath() string {
	return c.containingFlow
}

func (c compileContext) RequireFlow(link model.FileLink) (*compiledFlow, error) {
	if c.requireFlow == nil {
		return nil, NewConfigurationError("linked-flow compilation is unavailable", nil)
	}
	return c.requireFlow(link)
}

// compiledProgram is the immutable-by-convention command template for one
// prepared Program. Roots retain execution-plan order and duplicates.
type compiledProgram struct {
	roots []string
	flows map[string]*compiledFlow
}

func (p *compiledProgram) Roots() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.roots...)
}

func (p *compiledProgram) Flow(canonicalPath string) (*compiledFlow, bool) {
	if p == nil {
		return nil, false
	}
	flow, exists := p.flows[canonicalPath]
	return flow, exists
}

type compiledFlow struct {
	path       string
	config     model.Config
	onStart    []compiledDispatch
	body       []compiledDispatch
	onComplete []compiledDispatch
}

// compileProgram compiles the complete prepared flow graph before any runtime
// or device dependency is constructed. File-flow dependencies are ordered
// child before parent; script and media edges do not participate.
func compileProgram(ctx context.Context, program *Program, registry handlerRegistry) (*compiledProgram, error) {
	if ctx == nil {
		return nil, NewConfigurationError("program compilation context must not be nil", nil)
	}
	if program == nil {
		return nil, NewConfigurationError("prepared Program must not be nil", nil)
	}
	if registry.byKeyword == nil {
		return nil, NewConfigurationError("handler registry must be initialized", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	order, children, err := preparedFlowOrder(program)
	if err != nil {
		return nil, err
	}
	dispatcher := newDispatcher(registry)
	compiled := &compiledProgram{
		roots: append([]string(nil), program.roots...),
		flows: make(map[string]*compiledFlow, len(order)),
	}
	for _, path := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		flow, exists := program.Flow(path)
		if !exists {
			return nil, NewConfigurationError("capability graph flow is missing from the prepared Program", nil)
		}
		compileCtx := compileContext{
			containingFlow: path,
			requireFlow: func(link model.FileLink) (*compiledFlow, error) {
				canonical, resolveErr := program.resolveFlowLink(path, link)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if _, declared := children[path][canonical]; !declared {
					return nil, NewConfigurationError("flow link is absent from the prepared capability graph", nil)
				}
				child, ready := compiled.flows[canonical]
				if !ready {
					return nil, NewConfigurationError("linked flow was not compiled before its parent", nil)
				}
				return child, nil
			},
		}
		onStart, compileErr := dispatcher.compileSequence(ctx, compileCtx, flow.Config.OnFlowStart)
		if compileErr != nil {
			return nil, compileErr
		}
		body, compileErr := dispatcher.compileSequence(ctx, compileCtx, flow.Commands)
		if compileErr != nil {
			return nil, compileErr
		}
		onComplete, compileErr := dispatcher.compileSequence(ctx, compileCtx, flow.Config.OnFlowComplete)
		if compileErr != nil {
			return nil, compileErr
		}
		compiled.flows[path] = &compiledFlow{
			path: path, config: cloneConfig(flow.Config),
			onStart: onStart, body: body, onComplete: onComplete,
		}
	}
	for _, root := range compiled.roots {
		if _, exists := compiled.flows[root]; !exists {
			return nil, NewConfigurationError("prepared root is missing from the compiled Program", nil)
		}
	}
	return compiled, nil
}

func preparedFlowOrder(program *Program) ([]string, map[string]map[string]struct{}, error) {
	report := program.Graph()
	if len(report.Nodes) == 0 {
		return nil, nil, NewConfigurationError("prepared capability graph has no flows", nil)
	}
	if len(program.paths) != len(report.Nodes) || len(program.flows) != len(report.Nodes) {
		return nil, nil, NewConfigurationError("prepared Program and capability graph disagree", nil)
	}

	nodeIndex := make(map[string]int, len(report.Nodes))
	children := make(map[string]map[string]struct{}, len(report.Nodes))
	parents := make(map[string][]string, len(report.Nodes))
	for index, node := range report.Nodes {
		if strings.TrimSpace(node.Path) == "" {
			return nil, nil, NewConfigurationError("capability graph flow path must not be blank", nil)
		}
		if _, duplicate := nodeIndex[node.Path]; duplicate {
			return nil, nil, NewConfigurationError("capability graph flow paths must be unique", nil)
		}
		nodeIndex[node.Path] = index
		children[node.Path] = make(map[string]struct{})
		if index >= len(program.paths) || program.paths[index] != node.Path {
			return nil, nil, NewConfigurationError("prepared Program flow order disagrees with capability graph", nil)
		}
		if _, exists := program.flows[node.Path]; !exists {
			return nil, nil, NewConfigurationError("capability graph flow is missing from the prepared Program", nil)
		}
	}
	if len(program.roots) != len(report.Roots) {
		return nil, nil, NewConfigurationError("prepared Program roots disagree with capability graph", nil)
	}
	for index := range program.roots {
		if program.roots[index] != report.Roots[index] {
			return nil, nil, NewConfigurationError("prepared Program roots disagree with capability graph", nil)
		}
	}

	for _, edge := range report.Edges {
		if edge.Kind != model.FileLinkFlow {
			continue
		}
		if _, exists := nodeIndex[edge.From]; !exists {
			return nil, nil, NewConfigurationError("flow edge source is absent from the capability graph", nil)
		}
		if _, exists := nodeIndex[edge.To]; !exists {
			return nil, nil, NewConfigurationError("flow edge target is absent from the capability graph", nil)
		}
		if _, duplicate := children[edge.From][edge.To]; duplicate {
			continue
		}
		children[edge.From][edge.To] = struct{}{}
		parents[edge.To] = append(parents[edge.To], edge.From)
	}
	for child := range parents {
		sort.Slice(parents[child], func(left, right int) bool {
			return nodeIndex[parents[child][left]] < nodeIndex[parents[child][right]]
		})
	}

	remainingChildren := make(map[string]int, len(children))
	ready := make([]string, 0, len(report.Nodes))
	for _, node := range report.Nodes {
		remainingChildren[node.Path] = len(children[node.Path])
		if len(children[node.Path]) == 0 {
			ready = append(ready, node.Path)
		}
	}
	order := make([]string, 0, len(report.Nodes))
	for len(ready) > 0 {
		path := ready[0]
		ready = ready[1:]
		order = append(order, path)
		for _, parent := range parents[path] {
			remainingChildren[parent]--
			if remainingChildren[parent] == 0 {
				ready = append(ready, parent)
			}
		}
	}
	if len(order) != len(report.Nodes) {
		return nil, nil, NewConfigurationError("prepared flow graph contains a cycle", nil)
	}
	return order, children, nil
}
