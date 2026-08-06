package engine

import (
	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/model"
)

func cloneCapabilityReport(report capability.Report) capability.Report {
	cloned := report
	cloned.Roots = append([]string(nil), report.Roots...)
	cloned.Nodes = append([]capability.GraphNode(nil), report.Nodes...)
	cloned.Edges = append([]capability.GraphEdge(nil), report.Edges...)
	return cloned
}

func cloneFlow(flow model.Flow) model.Flow {
	cloned := flow
	cloned.Config = cloneConfig(flow.Config)
	cloned.Commands = cloneCommands(flow.Commands)
	return cloned
}

func cloneConfig(config model.Config) model.Config {
	cloned := config
	cloned.Tags = append([]string(nil), config.Tags...)
	cloned.Env = cloneStringMap(config.Env)
	cloned.OnFlowStart = cloneCommands(config.OnFlowStart)
	cloned.OnFlowComplete = cloneCommands(config.OnFlowComplete)
	cloned.Properties = cloneStringMap(config.Properties)
	if config.Ext != nil {
		cloned.Ext = make(map[string]any, len(config.Ext))
		for key, value := range config.Ext {
			cloned.Ext[key] = cloneDynamic(value)
		}
	}
	cloned.FieldSources = cloneSourceMap(config.FieldSources)
	return cloned
}

func cloneCommands(commands []model.Command) []model.Command {
	if commands == nil {
		return nil
	}
	cloned := make([]model.Command, len(commands))
	for index := range commands {
		cloned[index] = cloneCommand(commands[index])
	}
	return cloned
}

func cloneCommand(command model.Command) model.Command {
	cloned := command
	cloned.Arguments = cloneDynamic(command.Arguments)
	cloned.Selector = cloneSelector(command.Selector)
	cloned.Condition = cloneCondition(command.Condition)
	cloned.Children = cloneCommands(command.Children)
	cloned.Links = append([]model.FileLink(nil), command.Links...)
	cloned.Label = clonePointer(command.Label)
	cloned.Optional = clonePointer(command.Optional)
	return cloned
}

func cloneSelector(selector *model.ElementSelector) *model.ElementSelector {
	if selector == nil {
		return nil
	}
	cloned := *selector
	cloned.TextRegex = clonePointer(selector.TextRegex)
	cloned.IDRegex = clonePointer(selector.IDRegex)
	if selector.Size != nil {
		size := *selector.Size
		size.Width = clonePointer(selector.Size.Width)
		size.Height = clonePointer(selector.Size.Height)
		size.Tolerance = clonePointer(selector.Size.Tolerance)
		cloned.Size = &size
	}
	cloned.Optional = clonePointer(selector.Optional)
	cloned.RetryTapIfNoChange = clonePointer(selector.RetryTapIfNoChange)
	cloned.WaitUntilVisible = clonePointer(selector.WaitUntilVisible)
	cloned.Point = clonePointer(selector.Point)
	cloned.Start = clonePointer(selector.Start)
	cloned.End = clonePointer(selector.End)
	cloned.Below = cloneSelector(selector.Below)
	cloned.Above = cloneSelector(selector.Above)
	cloned.LeftOf = cloneSelector(selector.LeftOf)
	cloned.RightOf = cloneSelector(selector.RightOf)
	cloned.ContainsChild = cloneSelector(selector.ContainsChild)
	cloned.ChildOf = cloneSelector(selector.ChildOf)
	if selector.ContainsDescendants != nil {
		cloned.ContainsDescendants = make([]model.ElementSelector, len(selector.ContainsDescendants))
		for index := range selector.ContainsDescendants {
			cloned.ContainsDescendants[index] = *cloneSelector(&selector.ContainsDescendants[index])
		}
	}
	cloned.Traits = append([]model.ElementTrait(nil), selector.Traits...)
	cloned.Index = clonePointer(selector.Index)
	cloned.Enabled = clonePointer(selector.Enabled)
	cloned.Selected = clonePointer(selector.Selected)
	cloned.Checked = clonePointer(selector.Checked)
	cloned.Focused = clonePointer(selector.Focused)
	cloned.Repeat = clonePointer(selector.Repeat)
	cloned.Delay = clonePointer(selector.Delay)
	cloned.WaitToSettleTimeoutMS = clonePointer(selector.WaitToSettleTimeoutMS)
	cloned.Label = clonePointer(selector.Label)
	cloned.CSS = clonePointer(selector.CSS)
	cloned.FieldSources = cloneSourceMap(selector.FieldSources)
	return &cloned
}

func cloneCondition(condition *model.Condition) *model.Condition {
	if condition == nil {
		return nil
	}
	cloned := *condition
	cloned.Platform = clonePointer(condition.Platform)
	cloned.Visible = cloneSelector(condition.Visible)
	cloned.NotVisible = cloneSelector(condition.NotVisible)
	cloned.ScriptCondition = clonePointer(condition.ScriptCondition)
	cloned.Label = clonePointer(condition.Label)
	cloned.Optional = clonePointer(condition.Optional)
	cloned.FieldSources = cloneSourceMap(condition.FieldSources)
	return &cloned
}

func cloneDynamic(value any) any {
	switch typed := value.(type) {
	case nil, string, bool, int64, float64:
		return typed
	case []any:
		cloned := make([]any, len(typed))
		for index := range typed {
			cloned[index] = cloneDynamic(typed[index])
		}
		return cloned
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, nested := range typed {
			cloned[key] = cloneDynamic(nested)
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	case map[string]string:
		return cloneStringMap(typed)
	case model.Config:
		return cloneConfig(typed)
	case *model.Config:
		if typed == nil {
			return (*model.Config)(nil)
		}
		cloned := cloneConfig(*typed)
		return &cloned
	default:
		return typed
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneSourceMap(values map[string]model.SourceInfo) map[string]model.SourceInfo {
	if values == nil {
		return nil
	}
	cloned := make(map[string]model.SourceInfo, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
