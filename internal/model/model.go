// Package model defines FlowBaton's versioned, device-independent flow contract.
package model

import (
	"fmt"
	"reflect"
)

// ASTVersionV0 identifies the first frozen FlowBaton syntax contract.
const ASTVersionV0 = "flowbaton.ast/v0"

// Position is a one-based source position. Offset is a zero-based byte offset.
type Position struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"`
}

// SourceInfo identifies a source span. It is deliberately non-semantic and is
// excluded from serialized contract snapshots.
type SourceInfo struct {
	Path  string   `json:"-"`
	Start Position `json:"-"`
	End   Position `json:"-"`
}

// Diagnostic is a stable, location-aware parser or contract error.
type Diagnostic struct {
	Code       string     `json:"code"`
	Message    string     `json:"message"`
	Suggestion string     `json:"suggestion,omitempty"`
	Source     SourceInfo `json:"-"`
}

// Error renders a deterministic operator-facing diagnostic.
func (d Diagnostic) Error() string {
	location := d.Source.Path
	if location == "" {
		location = "<input>"
	}
	if d.Source.Start.Line > 0 {
		location = fmt.Sprintf("%s:%d:%d", location, d.Source.Start.Line, d.Source.Start.Column)
	}
	result := fmt.Sprintf("%s: %s: %s", location, d.Code, d.Message)
	if d.Suggestion != "" {
		result += fmt.Sprintf(". Did you mean %s?", d.Suggestion)
	}
	return result
}

// Flow is a parsed two-document flow. Commands contains author-written
// commands; ExecutionCommands adds the synthetic configuration prologue.
type Flow struct {
	SchemaVersion string     `json:"schemaVersion"`
	Path          string     `json:"-"`
	Config        Config     `json:"config"`
	Commands      []Command  `json:"commands"`
	Source        SourceInfo `json:"-"`
}

// ExecutionPlan is the boundary after workspace discovery, filtering,
// and ordering. Capability analysis only sees explicitly selected roots.
type ExecutionPlan struct {
	SelectedRoots []string `json:"selectedRoots"`
}

// Config is the typed first document of a flow file.
type Config struct {
	Name           string                `json:"name,omitempty"`
	AppID          string                `json:"appId,omitempty"`
	URL            string                `json:"url,omitempty"`
	Tags           []string              `json:"tags,omitempty"`
	Env            map[string]string     `json:"env,omitempty"`
	OnFlowStart    []Command             `json:"onFlowStart,omitempty"`
	OnFlowComplete []Command             `json:"onFlowComplete,omitempty"`
	Properties     map[string]string     `json:"properties,omitempty"`
	Ext            map[string]any        `json:"ext,omitempty"`
	Source         SourceInfo            `json:"-"`
	FieldSources   map[string]SourceInfo `json:"-"`
}

// WorkspaceConfig models the independently parsed workspace configuration.
type WorkspaceConfig struct {
	Flows          []string                 `json:"flows"`
	IncludeTags    []string                 `json:"includeTags,omitempty"`
	ExcludeTags    []string                 `json:"excludeTags,omitempty"`
	ExecutionOrder *WorkspaceExecutionOrder `json:"executionOrder,omitempty"`
	TargetBranch   string                   `json:"targetBranch,omitempty"`
	Notifications  map[string]any           `json:"notifications,omitempty"`
	DisableRetries *bool                    `json:"disableRetries,omitempty"`
	Platform       *WorkspacePlatformConfig `json:"platform,omitempty"`
	TestOutputDir  string                   `json:"testOutputDir,omitempty"`
	Unknown        map[string]any           `json:"unknown,omitempty"`
	Source         SourceInfo               `json:"-"`
}

// WorkspaceExecutionOrder is the workspace ordering contract.
type WorkspaceExecutionOrder struct {
	ContinueOnFailure bool     `json:"continueOnFailure,omitempty"`
	FlowsOrder        []string `json:"flowsOrder,omitempty"`
}

// WorkspacePlatformConfig holds platform-specific workspace behavior.
type WorkspacePlatformConfig struct {
	Android *WorkspaceAndroidConfig `json:"android,omitempty"`
	IOS     *WorkspaceIOSConfig     `json:"ios,omitempty"`
}

// WorkspaceAndroidConfig holds Android workspace behavior.
type WorkspaceAndroidConfig struct {
	DisableAnimations bool `json:"disableAnimations,omitempty"`
}

// WorkspaceIOSConfig holds iOS workspace behavior.
type WorkspaceIOSConfig struct {
	DisableAnimations          bool `json:"disableAnimations,omitempty"`
	SnapshotKeyHonorModalViews bool `json:"snapshotKeyHonorModalViews,omitempty"`
}

// CommandForm records whether a command used scalar or object YAML syntax.
type CommandForm string

const (
	CommandFormScalar CommandForm = "scalar"
	CommandFormObject CommandForm = "object"
)

// Command is the common v0 discriminated command node. Arguments retains the
// complete normalized YAML value while typed fields expose traversal-critical
// selector, condition, composite, and file-link data.
type Command struct {
	Kind      CommandKeyword   `json:"kind"`
	Form      CommandForm      `json:"form"`
	Arguments any              `json:"arguments,omitempty"`
	Selector  *ElementSelector `json:"selector,omitempty"`
	Condition *Condition       `json:"condition,omitempty"`
	Children  []Command        `json:"children,omitempty"`
	Links     []FileLink       `json:"links,omitempty"`
	Label     *string          `json:"label,omitempty"`
	Optional  *bool            `json:"optional,omitempty"`
	Source    SourceInfo       `json:"-"`
}

// Equivalent checks semantic command data while ignoring source locations.
func (c Command) Equivalent(other Command) bool {
	return reflect.DeepEqual(withoutCommandSources(c), withoutCommandSources(other))
}

func withoutCommandSources(command Command) Command {
	command.Source = SourceInfo{}
	command.Selector = withoutSelectorSources(command.Selector)
	command.Condition = withoutConditionSources(command.Condition)
	switch arguments := command.Arguments.(type) {
	case Config:
		command.Arguments = withoutConfigSources(arguments)
	case *Config:
		if arguments != nil {
			config := withoutConfigSources(*arguments)
			command.Arguments = &config
		}
	}
	if command.Children != nil {
		children := make([]Command, len(command.Children))
		for i := range command.Children {
			children[i] = withoutCommandSources(command.Children[i])
		}
		command.Children = children
	}
	if command.Links != nil {
		links := append([]FileLink(nil), command.Links...)
		for i := range links {
			links[i].ResolvedPath = ""
			links[i].Source = SourceInfo{}
		}
		command.Links = links
	}
	return command
}

func withoutConfigSources(config Config) Config {
	config.Source = SourceInfo{}
	config.FieldSources = nil
	if config.OnFlowStart != nil {
		commands := make([]Command, len(config.OnFlowStart))
		for index := range config.OnFlowStart {
			commands[index] = withoutCommandSources(config.OnFlowStart[index])
		}
		config.OnFlowStart = commands
	}
	if config.OnFlowComplete != nil {
		commands := make([]Command, len(config.OnFlowComplete))
		for index := range config.OnFlowComplete {
			commands[index] = withoutCommandSources(config.OnFlowComplete[index])
		}
		config.OnFlowComplete = commands
	}
	return config
}

func withoutSelectorSources(selector *ElementSelector) *ElementSelector {
	if selector == nil {
		return nil
	}
	cloned := *selector
	cloned.Source = SourceInfo{}
	cloned.FieldSources = nil
	cloned.Below = withoutSelectorSources(selector.Below)
	cloned.Above = withoutSelectorSources(selector.Above)
	cloned.LeftOf = withoutSelectorSources(selector.LeftOf)
	cloned.RightOf = withoutSelectorSources(selector.RightOf)
	cloned.ContainsChild = withoutSelectorSources(selector.ContainsChild)
	cloned.ChildOf = withoutSelectorSources(selector.ChildOf)
	if selector.ContainsDescendants != nil {
		cloned.ContainsDescendants = make([]ElementSelector, len(selector.ContainsDescendants))
		for index := range selector.ContainsDescendants {
			child := withoutSelectorSources(&selector.ContainsDescendants[index])
			cloned.ContainsDescendants[index] = *child
		}
	}
	return &cloned
}

func withoutConditionSources(condition *Condition) *Condition {
	if condition == nil {
		return nil
	}
	cloned := *condition
	cloned.Source = SourceInfo{}
	cloned.FieldSources = nil
	cloned.Visible = withoutSelectorSources(condition.Visible)
	cloned.NotVisible = withoutSelectorSources(condition.NotVisible)
	return &cloned
}

// ExecutionCommands returns a fresh executable sequence with configuration
// and environment initialization before author-written commands.
func (f Flow) ExecutionCommands() []Command {
	result := make([]Command, 0, len(f.Commands)+2)
	result = append(result, Command{
		Kind:      CommandApplyConfiguration,
		Form:      CommandFormObject,
		Arguments: f.Config,
		Source:    f.Config.Source,
	})
	if len(f.Config.Env) > 0 {
		env := make(map[string]string, len(f.Config.Env))
		for key, value := range f.Config.Env {
			env[key] = value
		}
		result = append(result, Command{
			Kind:      CommandDefineVariables,
			Form:      CommandFormObject,
			Arguments: env,
			Source:    f.Config.Source,
		})
	}
	result = append(result, f.Commands...)
	return result
}

// FileLink records a link without performing filesystem validation.
type FileLink struct {
	Kind         FileLinkKind `json:"kind"`
	Path         string       `json:"path"`
	ResolvedPath string       `json:"-"`
	Source       SourceInfo   `json:"-"`
}

// FileLinkKind identifies why a command links a file.
type FileLinkKind string

const (
	FileLinkFlow   FileLinkKind = "flow"
	FileLinkScript FileLinkKind = "script"
	FileLinkMedia  FileLinkKind = "media"
)

// ElementTrait is a selector trait token.
type ElementTrait string

const (
	ElementTraitText     ElementTrait = "TEXT"
	ElementTraitSquare   ElementTrait = "SQUARE"
	ElementTraitLongText ElementTrait = "LONG_TEXT"
)

// SizeSelector is the typed selector size constraint.
type SizeSelector struct {
	Width     *int `json:"width,omitempty"`
	Height    *int `json:"height,omitempty"`
	Tolerance *int `json:"tolerance,omitempty"`
}

// ElementSelector is the complete v0 selector surface.
type ElementSelector struct {
	TextRegex             *string               `json:"text,omitempty"`
	IDRegex               *string               `json:"id,omitempty"`
	Size                  *SizeSelector         `json:"size,omitempty"`
	Optional              *bool                 `json:"optional,omitempty"`
	RetryTapIfNoChange    *bool                 `json:"retryTapIfNoChange,omitempty"`
	WaitUntilVisible      *bool                 `json:"waitUntilVisible,omitempty"`
	Point                 *string               `json:"point,omitempty"`
	Start                 *string               `json:"start,omitempty"`
	End                   *string               `json:"end,omitempty"`
	Below                 *ElementSelector      `json:"below,omitempty"`
	Above                 *ElementSelector      `json:"above,omitempty"`
	LeftOf                *ElementSelector      `json:"leftOf,omitempty"`
	RightOf               *ElementSelector      `json:"rightOf,omitempty"`
	ContainsChild         *ElementSelector      `json:"containsChild,omitempty"`
	ContainsDescendants   []ElementSelector     `json:"containsDescendants,omitempty"`
	ChildOf               *ElementSelector      `json:"childOf,omitempty"`
	Traits                []ElementTrait        `json:"traits,omitempty"`
	Index                 *string               `json:"index,omitempty"`
	Enabled               *bool                 `json:"enabled,omitempty"`
	Selected              *bool                 `json:"selected,omitempty"`
	Checked               *bool                 `json:"checked,omitempty"`
	Focused               *bool                 `json:"focused,omitempty"`
	Repeat                *int                  `json:"repeat,omitempty"`
	Delay                 *int                  `json:"delay,omitempty"`
	WaitToSettleTimeoutMS *int                  `json:"waitToSettleTimeoutMs,omitempty"`
	Label                 *string               `json:"label,omitempty"`
	CSS                   *string               `json:"css,omitempty"`
	Source                SourceInfo            `json:"-"`
	FieldSources          map[string]SourceInfo `json:"-"`
}

// Platform is a flow condition platform discriminator.
type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformIOS     Platform = "ios"
	PlatformWeb     Platform = "web"
)

// Condition is the complete v0 conditional wrapper.
type Condition struct {
	Platform        *Platform             `json:"platform,omitempty"`
	Visible         *ElementSelector      `json:"visible,omitempty"`
	NotVisible      *ElementSelector      `json:"notVisible,omitempty"`
	ScriptCondition *string               `json:"true,omitempty"`
	Label           *string               `json:"label,omitempty"`
	Optional        *bool                 `json:"optional,omitempty"`
	Source          SourceInfo            `json:"-"`
	FieldSources    map[string]SourceInfo `json:"-"`
}
