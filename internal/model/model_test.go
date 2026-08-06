package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestContractV0DescriptorIsStableAndComplete(t *testing.T) {
	t.Parallel()

	contract := ContractV0()
	if contract.Version != ASTVersionV0 {
		t.Fatalf("contract version = %q, want %q", contract.Version, ASTVersionV0)
	}
	if got := len(contract.CommandKeywords); got != 53 {
		t.Fatalf("command keyword count = %d, want 53", got)
	}

	wantFirstLast := []CommandKeyword{CommandLaunchApp, CommandToggleAirplaneMode}
	gotFirstLast := []CommandKeyword{
		contract.CommandKeywords[0],
		contract.CommandKeywords[len(contract.CommandKeywords)-1],
	}
	if !reflect.DeepEqual(gotFirstLast, wantFirstLast) {
		t.Fatalf("command catalog bounds = %#v, want %#v", gotFirstLast, wantFirstLast)
	}

	wantSelectorFields := []string{
		"text", "id", "width", "height", "tolerance", "optional",
		"retryTapIfNoChange", "waitUntilVisible", "point", "start", "end",
		"below", "above", "leftOf", "rightOf", "containsChild",
		"containsDescendants", "childOf", "traits", "index", "enabled",
		"selected", "checked", "focused", "repeat", "delay",
		"waitToSettleTimeoutMs", "label", "css",
	}
	if !reflect.DeepEqual(contract.SelectorFields, wantSelectorFields) {
		t.Fatalf("selector fields = %#v, want %#v", contract.SelectorFields, wantSelectorFields)
	}

	wantConditionFields := []string{"platform", "visible", "notVisible", "true", "label", "optional"}
	if !reflect.DeepEqual(contract.ConditionFields, wantConditionFields) {
		t.Fatalf("condition fields = %#v, want %#v", contract.ConditionFields, wantConditionFields)
	}
}

func TestContractCatalogAccessorsReturnDefensiveCopies(t *testing.T) {
	t.Parallel()

	keywords := CommandKeywords()
	keywords[0] = CommandKeyword("mutated")
	if got := CommandKeywords()[0]; got != CommandLaunchApp {
		t.Fatalf("CommandKeywords shared backing storage: first = %q", got)
	}

	descriptor := ContractV0()
	descriptor.CommandKeywords[0] = CommandKeyword("mutated")
	descriptor.SelectorFields[0] = "mutated"
	descriptor.ConditionFields[0] = "mutated"
	fresh := ContractV0()
	if fresh.CommandKeywords[0] != CommandLaunchApp || fresh.SelectorFields[0] != "text" || fresh.ConditionFields[0] != "platform" {
		t.Fatalf("ContractV0 shared backing storage: %#v", fresh)
	}
}

func TestCommandSourceIsNonSemanticAndNotSerialized(t *testing.T) {
	t.Parallel()

	left := Command{
		Kind:      CommandTapOn,
		Form:      CommandFormObject,
		Arguments: map[string]any{"text": "Continue"},
		Source: SourceInfo{
			Path:  "first.yaml",
			Start: Position{Line: 4, Column: 3, Offset: 25},
		},
	}
	right := left
	right.Source = SourceInfo{
		Path:  "second.yaml",
		Start: Position{Line: 40, Column: 7, Offset: 250},
	}

	if !left.Equivalent(right) {
		t.Fatal("commands that differ only in source should be equivalent")
	}

	encoded, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	if strings.Contains(string(encoded), "first.yaml") || strings.Contains(string(encoded), "source") {
		t.Fatalf("serialized command leaks non-semantic source: %s", encoded)
	}
}

func TestCommandEquivalentDoesNotMutateNestedSourceSpans(t *testing.T) {
	t.Parallel()

	childSource := SourceInfo{Path: "child.yaml", Start: Position{Line: 8, Column: 5, Offset: 91}}
	linkSource := SourceInfo{Path: "root.yaml", Start: Position{Line: 6, Column: 9, Offset: 64}}
	left := Command{
		Kind: CommandRepeat,
		Children: []Command{{
			Kind:   CommandRunFlow,
			Source: childSource,
			Links: []FileLink{{
				Kind:   FileLinkFlow,
				Path:   "child.yaml",
				Source: linkSource,
			}},
		}},
	}
	right := left

	if !left.Equivalent(right) {
		t.Fatal("identical nested commands should be equivalent")
	}
	if left.Children[0].Source != childSource {
		t.Fatalf("Equivalent mutated child source: %#v", left.Children[0].Source)
	}
	if left.Children[0].Links[0].Source != linkSource {
		t.Fatalf("Equivalent mutated link source: %#v", left.Children[0].Links[0].Source)
	}
}

func TestFlowPathIsOriginOnlyAndNotSerialized(t *testing.T) {
	t.Parallel()

	flow := Flow{
		SchemaVersion: ASTVersionV0,
		Path:          "/private/workspace/root.yaml",
		Config:        Config{AppID: "com.example.app"},
	}
	encoded, err := json.Marshal(flow)
	if err != nil {
		t.Fatalf("marshal flow: %v", err)
	}
	if strings.Contains(string(encoded), "/private/workspace") || strings.Contains(string(encoded), `"path"`) {
		t.Fatalf("serialized AST leaks origin path: %s", encoded)
	}
}

func TestFileLinkResolvedPathIsOriginOnly(t *testing.T) {
	t.Parallel()

	link := FileLink{
		Kind:         FileLinkFlow,
		Path:         "child.yaml",
		ResolvedPath: "/private/workspace/child.yaml",
		Source:       SourceInfo{Path: "/private/workspace/root.yaml"},
	}
	encoded, err := json.Marshal(link)
	if err != nil {
		t.Fatalf("marshal file link: %v", err)
	}
	if strings.Contains(string(encoded), "/private/workspace") || strings.Contains(string(encoded), "resolvedPath") {
		t.Fatalf("serialized file link leaks resolved origin: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"path":"child.yaml"`) {
		t.Fatalf("serialized file link lost author path: %s", encoded)
	}
}

func TestTypedFieldSourcesAreOriginOnly(t *testing.T) {
	t.Parallel()

	source := SourceInfo{Path: "/private/root.yaml", Start: Position{Line: 7, Column: 9, Offset: 61}}
	selector := ElementSelector{
		CSS:          stringPointer("#submit"),
		Source:       source,
		FieldSources: map[string]SourceInfo{"css": source},
	}
	condition := Condition{
		Visible:      &selector,
		Source:       source,
		FieldSources: map[string]SourceInfo{"visible": source},
	}
	config := Config{
		AppID:        "com.example.app",
		Source:       source,
		FieldSources: map[string]SourceInfo{"url": source},
	}
	encoded, err := json.Marshal(struct {
		Selector  ElementSelector `json:"selector"`
		Condition Condition       `json:"condition"`
		Config    Config          `json:"config"`
	}{selector, condition, config})
	if err != nil {
		t.Fatalf("marshal typed models: %v", err)
	}
	if strings.Contains(string(encoded), "/private/") || strings.Contains(string(encoded), "fieldSources") {
		t.Fatalf("serialized typed models leak source origin: %s", encoded)
	}
}

func TestExecutionPlanV0SerializesOnlySelectedRoots(t *testing.T) {
	t.Parallel()

	plan := ExecutionPlan{SelectedRoots: []string{"flows/mobile.yaml", "flows/smoke.yaml"}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal execution plan: %v", err)
	}
	want := `{"selectedRoots":["flows/mobile.yaml","flows/smoke.yaml"]}`
	if string(encoded) != want {
		t.Fatalf("execution plan JSON = %s, want %s", encoded, want)
	}
}

func TestCommandEquivalentIgnoresAndPreservesTypedSources(t *testing.T) {
	t.Parallel()

	source := SourceInfo{Path: "root.yaml", Start: Position{Line: 9, Column: 7, Offset: 90}}
	left := Command{
		Kind: CommandRunFlow,
		Selector: &ElementSelector{
			TextRegex:    stringPointer("Ready"),
			Source:       source,
			FieldSources: map[string]SourceInfo{"text": source},
		},
		Condition: &Condition{
			Label:        stringPointer("when ready"),
			Source:       source,
			FieldSources: map[string]SourceInfo{"label": source},
		},
	}
	right := left
	right.Selector = cloneSelectorForTest(left.Selector)
	right.Condition = cloneConditionForTest(left.Condition)
	right.Selector.Source.Path = "other.yaml"
	right.Selector.FieldSources["text"] = SourceInfo{Path: "other.yaml"}
	right.Condition.Source.Path = "other.yaml"
	right.Condition.FieldSources["label"] = SourceInfo{Path: "other.yaml"}

	if !left.Equivalent(right) {
		t.Fatal("typed source locations should not affect command equivalence")
	}
	if left.Selector.Source != source || left.Condition.Source != source {
		t.Fatal("Equivalent mutated typed source locations")
	}
}

func cloneSelectorForTest(selector *ElementSelector) *ElementSelector {
	cloned := *selector
	cloned.FieldSources = map[string]SourceInfo{}
	for key, source := range selector.FieldSources {
		cloned.FieldSources[key] = source
	}
	return &cloned
}

func cloneConditionForTest(condition *Condition) *Condition {
	cloned := *condition
	cloned.FieldSources = map[string]SourceInfo{}
	for key, source := range condition.FieldSources {
		cloned.FieldSources[key] = source
	}
	return &cloned
}

func stringPointer(value string) *string {
	return &value
}

func TestExecutionCommandsPrependConfigurationAndEnvironment(t *testing.T) {
	t.Parallel()

	flow := Flow{
		SchemaVersion: ASTVersionV0,
		Config: Config{
			AppID: "com.example.app",
			Env:   map[string]string{"TOKEN": "local"},
		},
		Commands: []Command{{Kind: CommandLaunchApp, Form: CommandFormScalar}},
	}

	commands := flow.ExecutionCommands()
	if got, want := len(commands), 3; got != want {
		t.Fatalf("execution command count = %d, want %d", got, want)
	}
	if commands[0].Kind != CommandApplyConfiguration {
		t.Fatalf("first command = %q, want %q", commands[0].Kind, CommandApplyConfiguration)
	}
	if commands[1].Kind != CommandDefineVariables {
		t.Fatalf("second command = %q, want %q", commands[1].Kind, CommandDefineVariables)
	}
	if commands[2].Kind != CommandLaunchApp {
		t.Fatalf("third command = %q, want %q", commands[2].Kind, CommandLaunchApp)
	}

	env := commands[1].Arguments.(map[string]string)
	env["TOKEN"] = "mutated"
	if flow.Config.Env["TOKEN"] != "local" {
		t.Fatalf("ExecutionCommands leaked mutable environment map: %#v", flow.Config.Env)
	}
}

func TestExecutionCommandsOmitEmptyEnvironment(t *testing.T) {
	t.Parallel()

	flow := Flow{
		Config:   Config{AppID: "com.example.app"},
		Commands: []Command{{Kind: CommandLaunchApp, Form: CommandFormScalar}},
	}
	commands := flow.ExecutionCommands()
	if got, want := len(commands), 2; got != want {
		t.Fatalf("execution command count = %d, want %d", got, want)
	}
	if commands[0].Kind != CommandApplyConfiguration || commands[1].Kind != CommandLaunchApp {
		t.Fatalf("execution commands = %#v", commands)
	}
}

func TestDiagnosticRendersLocationCodeAndSuggestion(t *testing.T) {
	t.Parallel()

	diagnostic := Diagnostic{
		Code:       "unknown_command",
		Message:    "unknown command tapn",
		Suggestion: "tapOn",
		Source: SourceInfo{
			Path:  "/workspace/flow.yaml",
			Start: Position{Line: 5, Column: 7},
		},
	}

	got := diagnostic.Error()
	for _, fragment := range []string{
		"/workspace/flow.yaml:5:7",
		"unknown_command",
		"unknown command tapn",
		"Did you mean tapOn?",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("diagnostic %q does not contain %q", got, fragment)
		}
	}
}

func TestDiagnosticUsesInputFallbackWithoutSuggestion(t *testing.T) {
	t.Parallel()

	diagnostic := Diagnostic{Code: "invalid_document", Message: "expected a mapping"}
	if got, want := diagnostic.Error(), "<input>: invalid_document: expected a mapping"; got != want {
		t.Fatalf("diagnostic = %q, want %q", got, want)
	}
}

func TestCommandEquivalentIgnoresAllNestedTypedOrigin(t *testing.T) {
	t.Parallel()

	left := originRichCommand("left.yaml")
	right := originRichCommand("right.yaml")
	if !left.Equivalent(right) {
		t.Fatal("nested selector, condition, config, and link sources should be non-semantic")
	}
	if left.Source.Path != "left.yaml" || left.Selector.Below.Source.Path != "left.yaml" {
		t.Fatal("Equivalent mutated the receiver's nested origin")
	}

	leftConfig := originRichConfig("left.yaml")
	rightConfig := originRichConfig("right.yaml")
	leftPointer := Command{Kind: CommandApplyConfiguration, Arguments: &leftConfig}
	rightPointer := Command{Kind: CommandApplyConfiguration, Arguments: &rightConfig}
	if !leftPointer.Equivalent(rightPointer) {
		t.Fatal("pointer config origin should be non-semantic")
	}
	if leftConfig.Source.Path != "left.yaml" {
		t.Fatal("Equivalent mutated pointer config origin")
	}
}

func originRichCommand(path string) Command {
	source := SourceInfo{Path: path, Start: Position{Line: 3, Column: 5, Offset: 17}}
	selector := originRichSelector(source)
	return Command{
		Kind:      CommandRepeat,
		Form:      CommandFormObject,
		Arguments: originRichConfig(path),
		Selector:  selector,
		Condition: &Condition{
			Visible:      originLeafSelector(source, "visible"),
			NotVisible:   originLeafSelector(source, "not-visible"),
			Source:       source,
			FieldSources: map[string]SourceInfo{"visible": source, "notVisible": source},
		},
		Children: []Command{{Kind: CommandTapOn, Source: source}},
		Links: []FileLink{{
			Kind: FileLinkFlow, Path: "child.yaml", ResolvedPath: "/workspace/child.yaml", Source: source,
		}},
		Source: source,
	}
}

func originRichConfig(path string) Config {
	source := SourceInfo{Path: path, Start: Position{Line: 1, Column: 1}}
	return Config{
		AppID:          "com.example.app",
		Source:         source,
		FieldSources:   map[string]SourceInfo{"appId": source},
		OnFlowStart:    []Command{{Kind: CommandLaunchApp, Source: source}},
		OnFlowComplete: []Command{{Kind: CommandStopApp, Source: source}},
	}
}

func originRichSelector(source SourceInfo) *ElementSelector {
	return &ElementSelector{
		TextRegex:           stringPointer("root"),
		Below:               originLeafSelector(source, "below"),
		Above:               originLeafSelector(source, "above"),
		LeftOf:              originLeafSelector(source, "left"),
		RightOf:             originLeafSelector(source, "right"),
		ContainsChild:       originLeafSelector(source, "child"),
		ChildOf:             originLeafSelector(source, "parent"),
		ContainsDescendants: []ElementSelector{*originLeafSelector(source, "descendant")},
		Source:              source,
		FieldSources:        map[string]SourceInfo{"text": source},
	}
}

func originLeafSelector(source SourceInfo, text string) *ElementSelector {
	return &ElementSelector{
		TextRegex:    stringPointer(text),
		Source:       source,
		FieldSources: map[string]SourceInfo{"text": source},
	}
}
