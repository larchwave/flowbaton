// Package engine owns the deterministic host execution foundation.
package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/model"
)

// Program is the immutable-by-API set of flows validated by capability preflight.
type Program struct {
	roots   []string
	paths   []string
	flows   map[string]model.Flow
	aliases map[string]string
	graph   capability.Report
}

// Prepare runs capability preflight through a recording cache and retains the
// validated parsed flows. Executing a Program never reloads source files.
func Prepare(ctx context.Context, plan model.ExecutionPlan, loader capability.FlowLoader) (*Program, error) {
	if loader == nil {
		return nil, NewConfigurationError("engine Prepare requires a flow loader", nil)
	}
	recorder := newRecordingLoader(loader)
	report, err := capability.Check(ctx, plan, capability.WithLoader(recorder))
	if err != nil {
		return nil, err
	}

	program := &Program{
		roots:   append([]string(nil), report.Roots...),
		paths:   make([]string, 0, len(report.Nodes)),
		flows:   make(map[string]model.Flow, len(report.Nodes)),
		aliases: recorder.aliases(),
		graph:   cloneCapabilityReport(report),
	}
	for _, node := range report.Nodes {
		flow, exists := recorder.flow(node.Path)
		if !exists {
			return nil, fmt.Errorf("engine Prepare invariant: preflight node %q was not recorded", node.Path)
		}
		program.paths = append(program.paths, node.Path)
		program.flows[node.Path] = cloneFlow(flow)
	}
	return program, nil
}

// Roots returns canonical selected roots in execution-plan order, including
// repeated selections.
func (p *Program) Roots() []string {
	return append([]string(nil), p.roots...)
}

// FlowPaths returns unique canonical flow paths in preflight load order.
func (p *Program) FlowPaths() []string {
	return append([]string(nil), p.paths...)
}

// Flow returns one prepared flow without consulting the source loader.
func (p *Program) Flow(canonicalPath string) (model.Flow, bool) {
	flow, exists := p.flows[canonicalPath]
	return cloneFlow(flow), exists
}

// Graph returns the selected-root capability proof retained by the Program.
func (p *Program) Graph() capability.Report {
	return cloneCapabilityReport(p.graph)
}

// resolveFlowLink maps one prepared flow link to canonical identity
// using only aliases captured during capability preflight. It never consults
// the filesystem or source loader.
func (p *Program) resolveFlowLink(containingPath string, link model.FileLink) (string, error) {
	if p == nil {
		return "", NewConfigurationError("prepared Program must not be nil", nil)
	}
	if link.Kind != model.FileLinkFlow {
		return "", NewConfigurationError("prepared link is not a flow", nil)
	}
	if strings.TrimSpace(containingPath) == "" {
		return "", NewConfigurationError("containing flow path must not be blank", nil)
	}

	targets := make([]string, 0, 2)
	if strings.TrimSpace(link.ResolvedPath) != "" {
		targets = append(targets, link.ResolvedPath)
	}
	if strings.TrimSpace(link.Path) != "" {
		target := link.Path
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(containingPath), target)
		} else {
			target = filepath.Clean(target)
		}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return "", NewConfigurationError("flow link path must not be blank", nil)
	}

	resolved := ""
	for _, target := range targets {
		canonical, exists := p.canonicalAlias(target)
		if !exists {
			continue
		}
		if _, exists := p.flows[canonical]; !exists {
			return "", NewConfigurationError("flow link resolved outside the prepared Program", nil)
		}
		if resolved != "" && resolved != canonical {
			return "", NewConfigurationError("flow link resolves ambiguously", nil)
		}
		resolved = canonical
	}
	if resolved == "" {
		return "", NewConfigurationError("flow link was not proven by the prepared Program", nil)
	}
	return resolved, nil
}

func (p *Program) canonicalAlias(target string) (string, bool) {
	if canonical, exists := p.aliases[target]; exists {
		return canonical, true
	}
	cleaned := filepath.Clean(target)
	if canonical, exists := p.aliases[cleaned]; exists {
		return canonical, true
	}
	if _, exists := p.flows[target]; exists {
		return target, true
	}
	if _, exists := p.flows[cleaned]; exists {
		return cleaned, true
	}
	return "", false
}

type recordingLoader struct {
	base      capability.FlowLoader
	mu        sync.Mutex
	canonical map[string]canonicalResult
	flows     map[string]flowResult
}

type canonicalResult struct {
	path string
	err  error
}

type flowResult struct {
	flow model.Flow
	err  error
}

func newRecordingLoader(base capability.FlowLoader) *recordingLoader {
	return &recordingLoader{
		base:      base,
		canonical: make(map[string]canonicalResult),
		flows:     make(map[string]flowResult),
	}
}

func (l *recordingLoader) Canonical(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if result, exists := l.canonical[path]; exists {
		return result.path, result.err
	}
	canonical, err := l.base.Canonical(ctx, path)
	l.canonical[path] = canonicalResult{path: canonical, err: err}
	return canonical, err
}

func (l *recordingLoader) Load(ctx context.Context, canonicalPath string) (model.Flow, error) {
	if err := ctx.Err(); err != nil {
		return model.Flow{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if result, exists := l.flows[canonicalPath]; exists {
		return result.flow, result.err
	}
	flow, err := l.base.Load(ctx, canonicalPath)
	flow = cloneFlow(flow)
	l.flows[canonicalPath] = flowResult{flow: flow, err: err}
	return cloneFlow(flow), err
}

func (l *recordingLoader) flow(canonicalPath string) (model.Flow, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	result, exists := l.flows[canonicalPath]
	return cloneFlow(result.flow), exists && result.err == nil
}

func (l *recordingLoader) aliases() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	aliases := make(map[string]string, len(l.canonical))
	for raw, result := range l.canonical {
		if result.err == nil && strings.TrimSpace(result.path) != "" {
			aliases[raw] = result.path
		}
	}
	return aliases
}
