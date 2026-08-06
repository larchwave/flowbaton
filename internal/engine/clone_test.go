package engine

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

func TestCloneFlowCopiesEveryLinkBearingASTField(t *testing.T) {
	t.Parallel()

	relative := func(text string) *model.ElementSelector {
		return &model.ElementSelector{
			TextRegex:    pointer(text),
			FieldSources: map[string]model.SourceInfo{"text": {Path: text}},
		}
	}
	selector := &model.ElementSelector{
		TextRegex:             pointer("text"),
		IDRegex:               pointer("id"),
		Size:                  &model.SizeSelector{Width: pointer(10), Height: pointer(20), Tolerance: pointer(2)},
		Optional:              pointer(true),
		RetryTapIfNoChange:    pointer(true),
		WaitUntilVisible:      pointer(true),
		Point:                 pointer("10%,20%"),
		Start:                 pointer("0%,0%"),
		End:                   pointer("100%,100%"),
		Below:                 relative("below"),
		Above:                 relative("above"),
		LeftOf:                relative("left"),
		RightOf:               relative("right"),
		ContainsChild:         relative("child"),
		ContainsDescendants:   []model.ElementSelector{*relative("descendant")},
		ChildOf:               relative("parent"),
		Traits:                []model.ElementTrait{model.ElementTraitText, model.ElementTraitSquare},
		Index:                 pointer("-1"),
		Enabled:               pointer(true),
		Selected:              pointer(false),
		Checked:               pointer(true),
		Focused:               pointer(false),
		Repeat:                pointer(2),
		Delay:                 pointer(100),
		WaitToSettleTimeoutMS: pointer(3000),
		Label:                 pointer("selector label"),
		CSS:                   pointer("#id"),
		FieldSources:          map[string]model.SourceInfo{"text": {Path: "selector"}},
	}
	condition := &model.Condition{
		Platform:        pointer(model.PlatformAndroid),
		Visible:         relative("visible"),
		NotVisible:      relative("not-visible"),
		ScriptCondition: pointer("true"),
		Label:           pointer("condition label"),
		Optional:        pointer(true),
		FieldSources:    map[string]model.SourceInfo{"true": {Path: "condition"}},
	}
	dynamicConfig := model.Config{Env: map[string]string{"INNER": "original"}}
	dynamicConfigPointer := &model.Config{Properties: map[string]string{"inner": "original"}}
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Config: model.Config{
			Tags:           []string{"smoke"},
			Env:            map[string]string{"ROLE": "original"},
			OnFlowStart:    []model.Command{{Kind: model.CommandLaunchApp, Label: pointer("start")}},
			OnFlowComplete: []model.Command{{Kind: model.CommandStopApp, Optional: pointer(true)}},
			Properties:     map[string]string{"owner": "engine"},
			Ext:            map[string]any{"nested": map[string]any{"value": "original"}},
			FieldSources:   map[string]model.SourceInfo{"env": {Path: "config"}},
		},
		Commands: []model.Command{{
			Kind:      model.CommandTapOn,
			Selector:  selector,
			Condition: condition,
			Children:  []model.Command{{Kind: model.CommandBack, Label: pointer("child")}},
			Links: []model.FileLink{{
				Kind: model.FileLinkFlow, Path: "child.yaml", ResolvedPath: "/child.yaml",
			}},
			Label:    pointer("command label"),
			Optional: pointer(true),
			Arguments: map[string]any{
				"sequence":  []any{map[string]any{"value": "original"}},
				"strings":   []string{"original"},
				"stringMap": map[string]string{"value": "original"},
				"config":    dynamicConfig,
				"configPtr": dynamicConfigPointer,
				"nil":       nil,
				"bool":      true,
				"int":       int64(1),
				"float":     1.5,
			},
		}},
	}

	cloned := cloneFlow(flow)
	*flow.Commands[0].Selector.TextRegex = "mutated"
	*flow.Commands[0].Selector.Size.Width = 99
	*flow.Commands[0].Selector.Below.TextRegex = "mutated"
	flow.Commands[0].Selector.ContainsDescendants[0].FieldSources["text"] = model.SourceInfo{Path: "mutated"}
	flow.Commands[0].Selector.Traits[0] = model.ElementTraitLongText
	*flow.Commands[0].Condition.Platform = model.PlatformIOS
	*flow.Commands[0].Condition.Visible.TextRegex = "mutated"
	flow.Commands[0].Condition.FieldSources["true"] = model.SourceInfo{Path: "mutated"}
	*flow.Commands[0].Label = "mutated"
	*flow.Commands[0].Optional = false
	*flow.Commands[0].Children[0].Label = "mutated"
	flow.Commands[0].Links[0].Path = "mutated"
	flow.Config.Tags[0] = "mutated"
	flow.Config.Env["ROLE"] = "mutated"
	flow.Config.OnFlowStart[0].Label = pointer("mutated")
	flow.Config.OnFlowComplete[0].Optional = pointer(false)
	flow.Config.Properties["owner"] = "mutated"
	flow.Config.Ext["nested"].(map[string]any)["value"] = "mutated"
	flow.Config.FieldSources["env"] = model.SourceInfo{Path: "mutated"}
	arguments := flow.Commands[0].Arguments.(map[string]any)
	arguments["sequence"].([]any)[0].(map[string]any)["value"] = "mutated"
	arguments["strings"].([]string)[0] = "mutated"
	arguments["stringMap"].(map[string]string)["value"] = "mutated"
	dynamicConfig.Env["INNER"] = "mutated"
	dynamicConfigPointer.Properties["inner"] = "mutated"

	command := cloned.Commands[0]
	if *command.Selector.TextRegex != "text" || *command.Selector.Size.Width != 10 || *command.Selector.Below.TextRegex != "below" {
		t.Fatalf("selector clone mutated: %#v", command.Selector)
	}
	if command.Selector.ContainsDescendants[0].FieldSources["text"].Path != "descendant" || command.Selector.Traits[0] != model.ElementTraitText {
		t.Fatalf("selector slices/maps mutated: %#v", command.Selector)
	}
	if *command.Condition.Platform != model.PlatformAndroid || *command.Condition.Visible.TextRegex != "visible" || command.Condition.FieldSources["true"].Path != "condition" {
		t.Fatalf("condition clone mutated: %#v", command.Condition)
	}
	if *command.Label != "command label" || !*command.Optional || *command.Children[0].Label != "child" || command.Links[0].Path != "child.yaml" {
		t.Fatalf("command clone mutated: %#v", command)
	}
	if cloned.Config.Tags[0] != "smoke" || cloned.Config.Env["ROLE"] != "original" || *cloned.Config.OnFlowStart[0].Label != "start" || !*cloned.Config.OnFlowComplete[0].Optional {
		t.Fatalf("config clone mutated: %#v", cloned.Config)
	}
	if cloned.Config.Properties["owner"] != "engine" || cloned.Config.Ext["nested"].(map[string]any)["value"] != "original" || cloned.Config.FieldSources["env"].Path != "config" {
		t.Fatalf("config maps clone mutated: %#v", cloned.Config)
	}
	clonedArguments := command.Arguments.(map[string]any)
	if clonedArguments["sequence"].([]any)[0].(map[string]any)["value"] != "original" || clonedArguments["strings"].([]string)[0] != "original" || clonedArguments["stringMap"].(map[string]string)["value"] != "original" {
		t.Fatalf("dynamic clone mutated: %#v", clonedArguments)
	}
	if clonedArguments["config"].(model.Config).Env["INNER"] != "original" || clonedArguments["configPtr"].(*model.Config).Properties["inner"] != "original" {
		t.Fatalf("dynamic config clone mutated: %#v", clonedArguments)
	}
}

func pointer[T any](value T) *T { return &value }
