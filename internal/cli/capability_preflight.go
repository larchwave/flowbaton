package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/larchwave/flowbaton/internal/capability"
	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/model"
)

// NewParserChecker returns the production syntax checker. Parsing is followed
// by recursive capability analysis before a syntax check can succeed.
func NewParserChecker() ParserChecker {
	return ParserChecker{Preflight: capabilityPreflight{}}
}

type capabilityPreflight struct{}

func (capabilityPreflight) Check(ctx context.Context, source Source, root model.Flow) error {
	loader := &seededFlowLoader{
		source:   source,
		root:     root,
		delegate: capability.FileLoader{},
	}
	// Prepare runs the same capability traversal this used to call directly
	// and keeps the parsed flows, so Validate can compile them. Compilation
	// is where a command's values are checked, and it needs no device.
	program, err := engine.Prepare(
		ctx, model.ExecutionPlan{SelectedRoots: []string{source.Name}}, loader)
	if err != nil {
		return err
	}
	return engine.Validate(ctx, program)
}

// seededFlowLoader lets capability.Check own all graph traversal while using
// the root ParserChecker already parsed. Linked flows still use the normal
// read-only filesystem loader.
type seededFlowLoader struct {
	source        Source
	root          model.Flow
	rootCanonical string
	delegate      capability.FileLoader
}

func (loader *seededFlowLoader) Canonical(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	isSelectedRoot := loader.rootCanonical == ""
	if isSelectedRoot && path == "-" {
		loader.rootCanonical = "-"
		return loader.rootCanonical, nil
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = loader.source.ResolveLink(resolved)
	}
	if loader.source.ConfineTo != "" {
		base, err := filepath.EvalSymlinks(loader.source.ConfineTo)
		if err != nil {
			return "", fmt.Errorf("linked flow confinement: %w", err)
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			return "", fmt.Errorf("linked flow path: %w", err)
		}
		if !withinDirectory(base, filepath.Clean(absolute)) {
			return "", fmt.Errorf("linked flow %q resolves outside base directory %s", path, base)
		}
		if evaluated, evalErr := filepath.EvalSymlinks(absolute); evalErr == nil &&
			!withinDirectory(base, evaluated) {
			return "", fmt.Errorf("linked flow %q resolves outside base directory %s", path, base)
		}
	}
	canonical, err := loader.delegate.Canonical(ctx, resolved)
	if err == nil && loader.source.ConfineTo != "" {
		base, baseErr := filepath.EvalSymlinks(loader.source.ConfineTo)
		if baseErr != nil {
			return "", fmt.Errorf("linked flow confinement: %w", baseErr)
		}
		if !withinDirectory(base, canonical) {
			return "", fmt.Errorf("linked flow %q resolves outside base directory %s", path, base)
		}
	}
	if err == nil && isSelectedRoot {
		loader.rootCanonical = canonical
	}
	return canonical, err
}

func (loader *seededFlowLoader) Load(ctx context.Context, canonicalPath string) (model.Flow, error) {
	if err := ctx.Err(); err != nil {
		return model.Flow{}, err
	}
	if canonicalPath == loader.rootCanonical {
		return loader.root, nil
	}
	return loader.delegate.Load(ctx, canonicalPath)
}
