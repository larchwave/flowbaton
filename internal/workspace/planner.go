// Package workspace turns operator input — files, directories, CLI tag
// filters — into the ordered set of flows a run will execute.
//
// specs/03-cli-tooling.md section 1 "Flow discovery" is the contract. Nothing
// here touches a device or the engine: discovery is a pure function of the
// filesystem and the authored configuration, which is why it is testable on
// its own and buildable before any driver exists.
package workspace

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/larchwave/flowbaton/internal/flow"
	"github.com/larchwave/flowbaton/internal/model"
)

// configFileNames are the workspace configuration names auto-discovery looks
// for inside a directory root, in this order.
var configFileNames = []string{"config.yaml", "config.yml"}

// flowExtensions are the only file extensions a directory walk considers.
var flowExtensions = []string{".yaml", ".yml"}

// defaultFlowGlob is the inclusion glob applied when the workspace config
// declares none.
const defaultFlowGlob = "*"

// Flow is one selected flow: where it lives and the identity the workspace
// ordering and tag filters address it by.
type Flow struct {
	Path string
	Name string
	Tags []string
}

// Plan is the result of discovery: every selected flow in execution order,
// with the leading run-in-order prefix called out separately.
type Plan struct {
	Config model.WorkspaceConfig
	// Flows is every selected flow, already ordered.
	Flows []Flow
	// Sequence is the prefix of Flows that executionOrder.flowsOrder named, so
	// a caller can tell "must run in this order" from "may run in any order".
	Sequence          []Flow
	ContinueOnFailure bool
}

// Options carries the operator's overrides, which merge with the workspace
// configuration rather than replacing it.
type Options struct {
	// ConfigPath overrides auto-discovery when set.
	ConfigPath  string
	IncludeTags []string
	ExcludeTags []string
}

// SelectedPaths returns the ordered flow paths.
func (plan Plan) SelectedPaths() []string {
	paths := make([]string, len(plan.Flows))
	for index, selected := range plan.Flows {
		paths[index] = selected.Path
	}
	return paths
}

// ExecutionPlan projects the plan onto the engine's prepared boundary.
func (plan Plan) ExecutionPlan() model.ExecutionPlan {
	return model.ExecutionPlan{SelectedRoots: plan.SelectedPaths()}
}

// Discover resolves operator input into an ordered plan.
func Discover(roots []string, options Options) (Plan, error) {
	if len(roots) == 0 {
		return Plan{}, fmt.Errorf("workspace: at least one flow file or directory is required")
	}
	config, configPath, err := loadConfig(roots, options)
	if err != nil {
		return Plan{}, err
	}
	candidates, err := collectCandidates(roots, config, configPath)
	if err != nil {
		return Plan{}, err
	}
	selected := filterByTags(candidates, config, options)
	if len(selected) == 0 {
		// Naming the filters is the difference between an operator checking a
		// mistyped tag and an operator checking their filesystem. The tags in
		// play are the CLI's merged with the workspace config's, so both are
		// reported: the one that emptied the selection may be the one the
		// operator did not type.
		return Plan{}, fmt.Errorf(
			"workspace: include / exclude tags did not match any flows (%s)",
			describeTagFilters(config, options))
	}
	ordered, sequence, err := applyOrder(selected, config)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Config: config, Flows: ordered, Sequence: sequence}
	if config.ExecutionOrder != nil {
		plan.ContinueOnFailure = config.ExecutionOrder.ContinueOnFailure
	}
	return plan, nil
}

// loadConfig resolves the workspace configuration: an explicit --config wins,
// otherwise the first directory root is searched for a known config name. A
// run with no configuration at all is legal and yields the zero value.
func loadConfig(roots []string, options Options) (model.WorkspaceConfig, string, error) {
	if options.ConfigPath != "" {
		config, err := readConfig(options.ConfigPath)
		if err != nil {
			return model.WorkspaceConfig{}, "", err
		}
		return config, options.ConfigPath, nil
	}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return model.WorkspaceConfig{}, "", fmt.Errorf("workspace: %w", err)
		}
		if !info.IsDir() {
			continue
		}
		for _, name := range configFileNames {
			candidate := filepath.Join(root, name)
			if _, statErr := os.Stat(candidate); statErr != nil {
				continue
			}
			config, readErr := readConfig(candidate)
			if readErr != nil {
				return model.WorkspaceConfig{}, "", readErr
			}
			return config, candidate, nil
		}
	}
	return model.WorkspaceConfig{}, "", nil
}

func readConfig(path string) (model.WorkspaceConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.WorkspaceConfig{}, fmt.Errorf("workspace: reading config: %w", err)
	}
	config, _, err := flow.ParseWorkspaceBytes(path, data)
	if err != nil {
		return model.WorkspaceConfig{}, fmt.Errorf("workspace: parsing config %s: %w", path, err)
	}
	return config, nil
}

// collectCandidates expands every root. A named file is taken as authored; a
// directory is walked in full, with the inclusion globs deciding the depth —
// the default ["*"] selects the top level only, because `*` does not cross a
// separator.
func collectCandidates(roots []string, config model.WorkspaceConfig, configPath string) ([]candidate, error) {
	var candidates []candidate
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("workspace: %w", err)
		}
		if !info.IsDir() {
			selected, loadErr := loadFlow(root)
			if loadErr != nil {
				return nil, loadErr
			}
			// An explicitly named file is the operator saying they want it, so
			// it bypasses the tag filters a directory walk applies.
			candidates = append(candidates, candidate{flow: selected, explicit: true})
			continue
		}
		walked, walkErr := walkDirectory(root, config, configPath)
		if walkErr != nil {
			return nil, walkErr
		}
		candidates = append(candidates, walked...)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("workspace: no flow files matched the configured inclusion globs")
	}
	return candidates, nil
}

type candidate struct {
	flow     Flow
	explicit bool
}

func walkDirectory(root string, config model.WorkspaceConfig, configPath string) ([]candidate, error) {
	globs := config.Flows
	if len(globs) == 0 {
		globs = []string{defaultFlowGlob}
	}
	var candidates []candidate
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !hasFlowExtension(entry.Name()) {
			return nil
		}
		// A workspace configuration is not one of the flows it selects, at any
		// depth: a subdirectory may carry its own.
		if path == configPath || isConfigName(entry.Name()) {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		matches, matchErr := matchesAnyGlob(filepath.ToSlash(relative), globs)
		if matchErr != nil {
			return matchErr
		}
		if !matches {
			return nil
		}
		selected, loadErr := loadFlow(path)
		if loadErr != nil {
			return loadErr
		}
		candidates = append(candidates, candidate{flow: selected})
		return nil
	})
	if err != nil {
		if _, isDiagnostic := err.(interface{ Unwrap() error }); !isDiagnostic {
			return nil, fmt.Errorf("workspace: %w", err)
		}
		return nil, err
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].flow.Path < candidates[right].flow.Path
	})
	return candidates, nil
}

// matchesAnyGlob applies the inclusion globs to a flow's path RELATIVE to the
// root, slash-separated. The glob is matched with and without the file
// extension, so `regression*` selects `regression.yaml` and a bare flow name
// selects its file.
//
// Relative-path rather than base-name matching is what lets a glob name a
// folder — `smoke/*.yaml` — which is how a real workspace is laid out and what
// the contract does. It also keeps the default: `*` does not cross a
// separator, so `["*"]` still selects the top level and nothing below it.
func matchesAnyGlob(name string, globs []string) (bool, error) {
	stem := name[:len(name)-len(filepath.Ext(name))]
	for _, glob := range globs {
		for _, subject := range []string{name, stem} {
			matched, err := filepath.Match(glob, subject)
			if err != nil {
				return false, fmt.Errorf("workspace: invalid inclusion glob %q: %w", glob, err)
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func hasFlowExtension(name string) bool {
	return slices.Contains(flowExtensions, filepath.Ext(name))
}

func isConfigName(name string) bool {
	return slices.Contains(configFileNames, name)
}

// loadFlow parses a flow far enough to learn its identity. Parsing the whole
// document rather than only its first block keeps discovery honest: a flow
// that cannot parse is not a flow that can be selected.
func loadFlow(path string) (Flow, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Flow{}, fmt.Errorf("workspace: %w", err)
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return Flow{}, fmt.Errorf("workspace: %w", err)
	}
	parsed, err := flow.ParseBytes(absolute, data)
	if err != nil {
		return Flow{}, fmt.Errorf("workspace: parsing %s: %w", absolute, err)
	}
	name := parsed.Config.Name
	if name == "" {
		base := filepath.Base(absolute)
		name = base[:len(base)-len(filepath.Ext(base))]
	}
	return Flow{Path: absolute, Name: name, Tags: append([]string(nil), parsed.Config.Tags...)}, nil
}

// filterByTags merges the workspace filters with the operator's. Both apply:
// the CLI narrows a workspace selection rather than replacing it.
// describeTagFilters renders the merged filters for the empty-selection error.
// It reports the merge, not just what the operator typed, because a workspace
// config's own tags can empty a selection just as easily.
func describeTagFilters(config model.WorkspaceConfig, options Options) string {
	include := append(append([]string(nil), config.IncludeTags...), options.IncludeTags...)
	exclude := append(append([]string(nil), config.ExcludeTags...), options.ExcludeTags...)
	var parts []string
	if len(include) != 0 {
		parts = append(parts, "include tags: "+strings.Join(include, ", "))
	}
	if len(exclude) != 0 {
		parts = append(parts, "exclude tags: "+strings.Join(exclude, ", "))
	}
	if len(parts) == 0 {
		return "no tag filters were configured"
	}
	return strings.Join(parts, "; ")
}

func filterByTags(candidates []candidate, config model.WorkspaceConfig, options Options) []Flow {
	include := append(append([]string(nil), config.IncludeTags...), options.IncludeTags...)
	exclude := append(append([]string(nil), config.ExcludeTags...), options.ExcludeTags...)
	var selected []Flow
	for _, item := range candidates {
		if item.explicit {
			selected = append(selected, item.flow)
			continue
		}
		if len(include) != 0 && !hasAnyTag(item.flow.Tags, include) {
			continue
		}
		if hasAnyTag(item.flow.Tags, exclude) {
			continue
		}
		selected = append(selected, item.flow)
	}
	return selected
}

func hasAnyTag(tags, wanted []string) bool {
	for _, tag := range tags {
		if slices.Contains(wanted, tag) {
			return true
		}
	}
	return false
}

// applyOrder puts the flows named by executionOrder.flowsOrder first, in the
// authored order, and leaves the rest in the deterministic order discovery
// produced. An unknown name is refused rather than ignored: an operator who
// asked for an order and did not get it should hear about it.
func applyOrder(selected []Flow, config model.WorkspaceConfig) ([]Flow, []Flow, error) {
	if config.ExecutionOrder == nil || len(config.ExecutionOrder.FlowsOrder) == 0 {
		return selected, nil, nil
	}
	byName := make(map[string]Flow, len(selected))
	for _, item := range selected {
		byName[item.Name] = item
	}
	sequence := make([]Flow, 0, len(config.ExecutionOrder.FlowsOrder))
	taken := make(map[string]bool, len(config.ExecutionOrder.FlowsOrder))
	for _, name := range config.ExecutionOrder.FlowsOrder {
		item, exists := byName[name]
		if !exists {
			return nil, nil, fmt.Errorf(
				"workspace: executionOrder.flowsOrder names %q, which is not a selected flow", name)
		}
		if taken[name] {
			return nil, nil, fmt.Errorf("workspace: executionOrder.flowsOrder names %q twice", name)
		}
		taken[name] = true
		sequence = append(sequence, item)
	}
	ordered := append([]Flow(nil), sequence...)
	for _, item := range selected {
		if !taken[item.Name] {
			ordered = append(ordered, item)
		}
	}
	return ordered, sequence, nil
}
