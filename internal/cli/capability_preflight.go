package cli

import (
	"context"
	"path/filepath"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/model"
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
	_, err := capability.Check(
		ctx,
		model.ExecutionPlan{SelectedRoots: []string{source.Name}},
		capability.WithLoader(loader),
	)
	return err
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
	canonical, err := loader.delegate.Canonical(ctx, resolved)
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
