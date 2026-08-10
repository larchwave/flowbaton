package capability

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/larchwave/flowbaton/internal/model"
)

// FlowLoader separates canonical identity from parsing so the checker can
// cache each canonical flow while retaining every call-site edge.
type FlowLoader interface {
	Canonical(context.Context, string) (string, error)
	Load(context.Context, string) (model.Flow, error)
}

// Option configures Check without introducing device, asset, JS, or session
// dependencies.
type Option func(*checkConfig)

type checkConfig struct {
	registry Registry
	loader   FlowLoader
	platform ExecutionPlatform
}

// WithLoader supplies a pure flow/resource loader, primarily for deterministic
// tests and alternate read-only filesystems.
func WithLoader(loader FlowLoader) Option {
	return func(config *checkConfig) {
		config.loader = loader
	}
}

// WithRegistry supplies the frozen registry used by capability analysis.
func WithRegistry(registry Registry) Option {
	return func(config *checkConfig) {
		config.registry = registry
	}
}

// WithPlatform limits preflight to the selected execution platform. An empty
// platform preserves platform-neutral syntax checking; executable callers
// should pass one of android, ios-simulator, or web before opening a driver.
func WithPlatform(platform ExecutionPlatform) Option {
	return func(config *checkConfig) {
		config.platform = ExecutionPlatform(strings.TrimSpace(string(platform)))
	}
}

// Report is the deterministic selected-root graph proven by preflight.
type Report struct {
	Roots []string    `json:"roots"`
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphNode is one canonical flow loaded and analyzed exactly once.
type GraphNode struct {
	Path string `json:"path"`
}

// GraphEdge retains every flow, script, and media call site. Source carries
// local diagnostics and is excluded from contract snapshots.
type GraphEdge struct {
	From   string             `json:"from"`
	To     string             `json:"to"`
	Kind   model.FileLinkKind `json:"kind"`
	Source model.SourceInfo   `json:"-"`
}

// Violation is a fail-closed, location-aware preflight error.
type Violation struct {
	Code        string           `json:"code"`
	Message     string           `json:"message"`
	FeatureKind FeatureKind      `json:"featureKind,omitempty"`
	FeatureName string           `json:"featureName,omitempty"`
	Source      model.SourceInfo `json:"-"`
	Chain       []GraphEdge      `json:"chain,omitempty"`
	Cause       error            `json:"-"`
}

// Error renders a deterministic local diagnostic including its edge chain.
func (v Violation) Error() string {
	location := v.Source.Path
	if location == "" {
		location = "<preflight>"
	}
	if v.Source.Start.Line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, v.Source.Start.Line, v.Source.Start.Column)
	}
	message := fmt.Sprintf("%s: %s: %s", location, v.Code, v.Message)
	if v.FeatureName != "" {
		message += fmt.Sprintf(" [%s/%s]", v.FeatureKind, v.FeatureName)
	}
	if len(v.Chain) > 0 {
		parts := make([]string, 0, len(v.Chain))
		for _, edge := range v.Chain {
			part := edge.From + " -> " + edge.To
			if edge.Source.Start.Line > 0 {
				part += fmt.Sprintf("@%s:%d:%d", edge.Source.Path, edge.Source.Start.Line, edge.Source.Start.Column)
			}
			parts = append(parts, part)
		}
		message += "; chain: " + strings.Join(parts, " | ")
	}
	if v.Cause != nil {
		message += ": " + v.Cause.Error()
	}
	return message
}

// Unwrap exposes the underlying filesystem or parse error.
func (v Violation) Unwrap() error {
	return v.Cause
}

type visitColor uint8

const (
	visitWhite visitColor = iota
	visitGray
	visitBlack
)

type traversalFrame struct {
	path     string
	incoming *GraphEdge
	entered  bool
	links    []model.FileLink
	nextLink int
}

// Check validates the registry and selected execution roots, constructs the
// canonical graph iteratively, and rejects every reachable unsupported
// capability or invalid resource before any mutating subsystem can be opened.
func Check(ctx context.Context, plan model.ExecutionPlan, options ...Option) (Report, error) {
	config := checkConfig{registry: DefaultRegistry(), loader: FileLoader{}}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	if len(plan.SelectedRoots) == 0 {
		return Report{}, fmt.Errorf("execution plan requires at least one selected root")
	}
	if err := config.registry.Validate(); err != nil {
		return Report{}, fmt.Errorf("support registry: %w", err)
	}
	if config.platform != "" && !validExecutionPlatforms[config.platform] {
		return Report{}, fmt.Errorf("unknown selected platform %q", config.platform)
	}
	if config.loader == nil {
		return Report{}, fmt.Errorf("capability checker requires a flow loader")
	}

	report := Report{
		Roots: make([]string, 0, len(plan.SelectedRoots)),
		Nodes: make([]GraphNode, 0),
		Edges: make([]GraphEdge, 0),
	}
	colors := make(map[string]visitColor)
	loaded := make(map[string]model.Flow)

	for _, selectedRoot := range plan.SelectedRoots {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		canonical, err := config.loader.Canonical(ctx, selectedRoot)
		if err != nil {
			return report, pathViolation(selectedRoot, nil, err, nil)
		}
		report.Roots = append(report.Roots, canonical)
		if colors[canonical] == visitBlack {
			continue
		}

		stack := []traversalFrame{{path: canonical}}
		for len(stack) > 0 {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			index := len(stack) - 1
			frame := &stack[index]
			if !frame.entered {
				flow, exists := loaded[frame.path]
				if !exists {
					flow, err = config.loader.Load(ctx, frame.path)
					if err != nil {
						return report, loadViolation(frame.path, err, chainForStack(stack))
					}
					if flow.SchemaVersion != model.ASTVersionV0 {
						return report, astVersionViolation(frame.path, flow, chainForStack(stack))
					}
					loaded[frame.path] = flow
					report.Nodes = append(report.Nodes, GraphNode{Path: frame.path})
				}
				colors[frame.path] = visitGray
				frame.entered = true
				frame.links = collectLinks(flow)
				if violation := inspectFlow(flow, config.registry, config.platform); violation != nil {
					violation.Chain = chainForStack(stack)
					return report, *violation
				}
			}

			if frame.nextLink >= len(frame.links) {
				colors[frame.path] = visitBlack
				stack = stack[:index]
				continue
			}

			link := frame.links[frame.nextLink]
			frame.nextLink++
			target := linkTarget(frame.path, link)
			canonicalTarget, canonicalErr := config.loader.Canonical(ctx, target)
			edgeTarget := canonicalTarget
			if edgeTarget == "" {
				edgeTarget = filepath.Clean(target)
			}
			edge := GraphEdge{From: frame.path, To: edgeTarget, Kind: link.Kind, Source: link.Source}
			report.Edges = append(report.Edges, edge)
			if canonicalErr != nil {
				chain := append(chainForStack(stack), edge)
				return report, pathViolation(target, &link.Source, canonicalErr, chain)
			}

			if link.Kind != model.FileLinkFlow {
				if canonicalTarget == frame.path {
					return report, Violation{
						Code: "self_link", Message: fmt.Sprintf("%s resource links its containing flow", link.Kind),
						Source: link.Source, Chain: append(chainForStack(stack), edge),
					}
				}
				if link.Kind != model.FileLinkScript && link.Kind != model.FileLinkMedia {
					return report, Violation{
						Code: "unknown_link_kind", Message: fmt.Sprintf("unknown link kind %s", link.Kind),
						Source: link.Source, Chain: append(chainForStack(stack), edge),
					}
				}
				continue
			}

			switch colors[canonicalTarget] {
			case visitGray:
				return report, Violation{
					Code: "active_cycle", Message: "flow link creates an active-path cycle",
					Source: link.Source, Chain: append(chainForStack(stack), edge),
				}
			case visitBlack:
				continue
			default:
				edgeCopy := edge
				stack = append(stack, traversalFrame{path: canonicalTarget, incoming: &edgeCopy})
			}
		}
	}
	return report, nil
}

func collectLinks(flow model.Flow) []model.FileLink {
	commands := make([]model.Command, 0, len(flow.Config.OnFlowStart)+len(flow.Commands)+len(flow.Config.OnFlowComplete))
	commands = append(commands, flow.Config.OnFlowStart...)
	commands = append(commands, flow.Commands...)
	commands = append(commands, flow.Config.OnFlowComplete...)
	stack := make([]model.Command, 0, len(commands))
	for index := len(commands) - 1; index >= 0; index-- {
		stack = append(stack, commands[index])
	}
	links := make([]model.FileLink, 0)
	for len(stack) > 0 {
		index := len(stack) - 1
		command := stack[index]
		stack = stack[:index]
		links = append(links, command.Links...)
		for childIndex := len(command.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, command.Children[childIndex])
		}
	}
	return links
}

func inspectFlow(flow model.Flow, registry Registry, platform ExecutionPlatform) *Violation {
	if flow.Config.URL != "" {
		return unsupportedFeature(registry, FeatureConfigExtension, "url", sourceForField(flow.Config.FieldSources, "url", flow.Config.Source), platform)
	}
	extKeys := make([]string, 0, len(flow.Config.Ext))
	for key := range flow.Config.Ext {
		extKeys = append(extKeys, key)
	}
	sort.Strings(extKeys)
	for _, key := range extKeys {
		name := key
		if key == "androidWebViewHierarchy" || key == "jsEngine" {
			if value, ok := flow.Config.Ext[key].(string); ok {
				name += "=" + value
			} else {
				name += "=<invalid>"
			}
		}
		if violation := unsupportedFeature(registry, FeatureConfigExtension, name, sourceForField(flow.Config.FieldSources, key, flow.Config.Source), platform); violation != nil {
			return violation
		}
	}

	commands := make([]model.Command, 0, len(flow.Config.OnFlowStart)+len(flow.Commands)+len(flow.Config.OnFlowComplete))
	commands = append(commands, flow.Config.OnFlowStart...)
	commands = append(commands, flow.Commands...)
	commands = append(commands, flow.Config.OnFlowComplete...)
	stack := make([]model.Command, 0, len(commands))
	for index := len(commands) - 1; index >= 0; index-- {
		stack = append(stack, commands[index])
	}
	for len(stack) > 0 {
		index := len(stack) - 1
		command := stack[index]
		stack = stack[:index]
		if conditionExcludesPlatform(command.Condition, platform) {
			if violation := inspectCondition(command.Condition, registry, command.Source, ""); violation != nil {
				return violation
			}
			continue
		}
		if violation := inspectCommand(command, registry, platform); violation != nil {
			return violation
		}
		if violation := inspectCondition(command.Condition, registry, command.Source, platform); violation != nil {
			return violation
		}
		if violation := inspectSelector(command.Selector, registry, command.Source, platform); violation != nil {
			return violation
		}
		for childIndex := len(command.Children) - 1; childIndex >= 0; childIndex-- {
			stack = append(stack, command.Children[childIndex])
		}
	}
	return nil
}

func inspectCommand(command model.Command, registry Registry, platform ExecutionPlatform) *Violation {
	if violation := unsupportedFeature(registry, FeatureCommand, string(command.Kind), command.Source, platform); violation != nil {
		return violation
	}
	if platform == "" {
		return nil
	}
	platforms, constrained, reason := authoredCommandPlatforms(command)
	if !constrained || containsPlatform(platforms, platform) {
		return nil
	}
	message := reason
	if message == "" {
		message = fmt.Sprintf(
			"command value does not support selected platform %q; supported: %s",
			platform, strings.Join(platforms, ", "),
		)
	}
	return &Violation{
		Code: "unsupported_platform", Message: message,
		FeatureKind: FeatureCommand, FeatureName: string(command.Kind), Source: command.Source,
	}
}

func authoredCommandPlatforms(command model.Command) ([]string, bool, string) {
	switch command.Kind {
	case model.CommandPressKey:
		authored, ok := command.Arguments.(string)
		if !ok {
			return nil, false, ""
		}
		if hasInterpolationExpression(authored) {
			return nil, true, "dynamic pressKey values cannot be proven safe for a selected platform before device startup"
		}
		canonical, ok := model.PressKeyCanonical(authored)
		if !ok {
			return nil, false, ""
		}
		return driverCommandValuePlatforms(command.Kind, canonical), true, ""
	case model.CommandAction:
		authored, ok := command.Arguments.(string)
		if !ok {
			return nil, false, ""
		}
		if strings.EqualFold(authored, string(model.CommandBack)) {
			return driverCommandPlatforms(model.CommandBack), true, ""
		}
	case model.CommandOpenLink:
		if object, ok := command.Arguments.(map[string]any); ok {
			if force, ok := object["browser"].(bool); ok && force {
				return driverCommandPlatforms(model.CommandOpenBrowser), true, ""
			}
		}
	}
	return nil, false, ""
}

// hasInterpolationExpression mirrors the runtime's single-pass dollar
// grammar without importing the JavaScript execution surface into preflight.
func hasInterpolationExpression(input string) bool {
	for index := 0; index < len(input); index++ {
		if index+2 < len(input) && input[index] == '\\' && input[index+1] == '$' && input[index+2] == '{' {
			index += 2
			continue
		}
		if index+1 >= len(input) || input[index] != '$' || input[index+1] != '{' {
			continue
		}
		region := index + 2
		for region < len(input) && input[region] != '$' {
			region++
		}
		if strings.LastIndexByte(input[index+2:region], '}') >= 0 {
			return true
		}
	}
	return false
}

func containsPlatform(platforms []string, selected ExecutionPlatform) bool {
	for _, platform := range platforms {
		if platform == string(selected) {
			return true
		}
	}
	return false
}

func conditionExcludesPlatform(condition *model.Condition, selected ExecutionPlatform) bool {
	if condition == nil || condition.Platform == nil || selected == "" {
		return false
	}
	switch *condition.Platform {
	case model.PlatformAndroid:
		return selected != ExecutionPlatformAndroid
	case model.PlatformIOS:
		return selected != ExecutionPlatformIOSSimulator
	case model.PlatformWeb:
		return selected != ExecutionPlatformWeb
	default:
		return false
	}
}

func inspectCondition(condition *model.Condition, registry Registry, fallback model.SourceInfo, platform ExecutionPlatform) *Violation {
	if condition == nil {
		return nil
	}
	if condition.Platform != nil && *condition.Platform == model.PlatformWeb {
		if violation := unsupportedFeature(registry, FeatureDevicePlatform, "web", sourceForField(condition.FieldSources, "platform", fallback), platform); violation != nil {
			return violation
		}
	}
	if violation := inspectSelector(condition.Visible, registry, sourceForField(condition.FieldSources, "visible", fallback), platform); violation != nil {
		return violation
	}
	return inspectSelector(condition.NotVisible, registry, sourceForField(condition.FieldSources, "notVisible", fallback), platform)
}

func inspectSelector(selector *model.ElementSelector, registry Registry, fallback model.SourceInfo, platform ExecutionPlatform) *Violation {
	if selector == nil {
		return nil
	}
	stack := []*model.ElementSelector{selector}
	for len(stack) > 0 {
		index := len(stack) - 1
		current := stack[index]
		stack = stack[:index]
		if current.CSS != nil {
			if violation := unsupportedFeature(registry, FeatureSelector, "css", sourceForField(current.FieldSources, "css", fallback), platform); violation != nil {
				return violation
			}
		}
		children := []*model.ElementSelector{
			current.Below, current.Above, current.LeftOf, current.RightOf,
			current.ContainsChild, current.ChildOf,
		}
		for childIndex := len(current.ContainsDescendants) - 1; childIndex >= 0; childIndex-- {
			child := &current.ContainsDescendants[childIndex]
			stack = append(stack, child)
		}
		for childIndex := len(children) - 1; childIndex >= 0; childIndex-- {
			if children[childIndex] != nil {
				stack = append(stack, children[childIndex])
			}
		}
	}
	return nil
}

func unsupportedFeature(registry Registry, kind FeatureKind, name string, source model.SourceInfo, platform ExecutionPlatform) *Violation {
	entry, found := registry.Lookup(kind, name)
	if !found {
		return &Violation{
			Code: "unsupported_capability", Message: "feature is not classified by the v0 support registry",
			FeatureKind: kind, FeatureName: name, Source: source,
		}
	}
	if entry.ParseStatus == ParseStatusOmitted || entry.RuntimeStatus == RuntimeStatusDeferred || entry.RuntimeStatus == RuntimeStatusExcluded {
		return &Violation{
			Code: "unsupported_capability", Message: entry.Reason,
			FeatureKind: kind, FeatureName: name, Source: source,
		}
	}
	if platform != "" && !entrySupportsPlatform(entry, platform) {
		return &Violation{
			Code: "unsupported_platform",
			Message: fmt.Sprintf("feature does not support selected platform %q; supported: %s",
				platform, strings.Join(entry.Platforms, ", ")),
			FeatureKind: kind, FeatureName: name, Source: source,
		}
	}
	return nil
}

func entrySupportsPlatform(entry Entry, platform ExecutionPlatform) bool {
	for _, supported := range entry.Platforms {
		if supported == string(platform) || supported == "all-hosts" {
			return true
		}
	}
	return false
}

func pathViolation(path string, source *model.SourceInfo, err error, chain []GraphEdge) Violation {
	location := model.SourceInfo{Path: path, Start: model.Position{Line: 1, Column: 1}}
	if source != nil {
		location = *source
	}
	code := "link_error"
	message := "cannot resolve linked path"
	switch {
	case errors.Is(err, ErrFlowDirectory):
		code = "directory_link"
		message = "linked path is a directory"
	case errors.Is(err, ErrFlowNonRegular):
		code = "non_regular_link"
		message = "linked path is not a regular file"
	case errors.Is(err, fs.ErrNotExist):
		code = "missing_link"
		message = "linked path does not exist"
	}
	return Violation{Code: code, Message: message, Source: location, Chain: chain, Cause: err}
}

func astVersionViolation(path string, flow model.Flow, chain []GraphEdge) Violation {
	location := flow.Source
	if location.Path == "" {
		location = flow.Config.Source
	}
	if location.Path == "" {
		location = model.SourceInfo{Path: path, Start: model.Position{Line: 1, Column: 1}}
	}
	return Violation{
		Code:    "unsupported_ast_version",
		Message: fmt.Sprintf("loaded flow schema version %q is not supported; expected %q", flow.SchemaVersion, model.ASTVersionV0),
		Source:  location,
		Chain:   chain,
	}
}

func loadViolation(path string, err error, chain []GraphEdge) Violation {
	location := model.SourceInfo{Path: path, Start: model.Position{Line: 1, Column: 1}}
	var diagnostic model.Diagnostic
	if errors.As(err, &diagnostic) {
		location = diagnostic.Source
		return Violation{Code: "flow_parse_error", Message: "linked flow failed syntax parsing", Source: location, Chain: chain, Cause: err}
	}
	violation := pathViolation(path, &location, err, chain)
	if violation.Code == "link_error" {
		violation.Code = "flow_load_error"
		violation.Message = "cannot load linked flow"
	}
	return violation
}

func linkTarget(containingFlow string, link model.FileLink) string {
	if link.ResolvedPath != "" {
		return link.ResolvedPath
	}
	if filepath.IsAbs(link.Path) {
		return filepath.Clean(link.Path)
	}
	return filepath.Join(filepath.Dir(containingFlow), link.Path)
}

func chainForStack(stack []traversalFrame) []GraphEdge {
	chain := make([]GraphEdge, 0, len(stack)-1)
	for index := 1; index < len(stack); index++ {
		if stack[index].incoming != nil {
			chain = append(chain, *stack[index].incoming)
		}
	}
	return chain
}

func sourceForField(sources map[string]model.SourceInfo, field string, fallback model.SourceInfo) model.SourceInfo {
	if source, found := sources[field]; found {
		return source
	}
	return fallback
}
