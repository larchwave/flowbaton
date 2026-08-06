package flow

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

const cyclicAliasFlow = `appId: com.example.app
foo: &foo
  self: *foo
---
- launchApp
`

func TestParseValidTwoDocumentScalarAndObjectFlow(t *testing.T) {
	t.Parallel()

	input := `appId: com.example.app
name: smoke
env:
  USER: Ada
---
- launchApp
- tapOn:
    text: Continue
    optional: true
`
	parsed, err := ParseBytes("/workspace/smoke.yaml", []byte(input))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	if parsed.SchemaVersion != model.ASTVersionV0 {
		t.Fatalf("schema version = %q, want %q", parsed.SchemaVersion, model.ASTVersionV0)
	}
	if parsed.Config.AppID != "com.example.app" || parsed.Config.Name != "smoke" {
		t.Fatalf("config = %#v", parsed.Config)
	}
	if got := parsed.Config.Env["USER"]; got != "Ada" {
		t.Fatalf("env USER = %q, want Ada", got)
	}
	if got, want := len(parsed.Commands), 2; got != want {
		t.Fatalf("command count = %d, want %d", got, want)
	}
	if parsed.Commands[0].Kind != model.CommandLaunchApp || parsed.Commands[0].Form != model.CommandFormScalar {
		t.Fatalf("scalar command = %#v", parsed.Commands[0])
	}
	tap := parsed.Commands[1]
	if tap.Kind != model.CommandTapOn || tap.Form != model.CommandFormObject {
		t.Fatalf("object command = %#v", tap)
	}
	if tap.Selector == nil || tap.Selector.TextRegex == nil || *tap.Selector.TextRegex != "Continue" {
		t.Fatalf("tap selector = %#v", tap.Selector)
	}
	if tap.Optional == nil || !*tap.Optional {
		t.Fatalf("tap optional = %#v", tap.Optional)
	}
	if got, want := len(parsed.ExecutionCommands()), 4; got != want {
		t.Fatalf("execution command count = %d, want %d", got, want)
	}
}

func TestParseRequiresExactlyTwoDocumentsAndExpectedShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		code  string
	}{
		{
			name: "missing separator",
			input: `appId: com.example.app
commands:
  - launchApp
`,
			code: "invalid_document_count",
		},
		{
			name: "extra document",
			input: `appId: com.example.app
---
- launchApp
---
- scroll
`,
			code: "invalid_document_count",
		},
		{
			name: "non object config",
			input: `- appId
---
- launchApp
`,
			code: "config_object_required",
		},
		{
			name: "non array commands",
			input: `appId: com.example.app
---
launchApp: {}
`,
			code: "commands_array_required",
		},
		{
			name: "empty command entry",
			input: `appId: com.example.app
---
-
`,
			code: "empty_command",
		},
		{
			name: "duplicate union fields",
			input: `appId: com.example.app
---
- launchApp: {}
  back: {}
`,
			code: "multiple_command_fields",
		},
		{
			name: "unsupported command node type",
			input: `appId: com.example.app
---
- 42
`,
			code: "command_type_required",
		},
		{
			name:  "malformed yaml",
			input: "appId: [\n---\n- launchApp\n",
			code:  "malformed_yaml",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBytes("/workspace/invalid.yaml", []byte(test.input))
			diagnostic := requireDiagnostic(t, err)
			if diagnostic.Code != test.code {
				t.Fatalf("diagnostic code = %q, want %q (%v)", diagnostic.Code, test.code, err)
			}
			if diagnostic.Source.Start.Line < 1 || diagnostic.Source.Start.Column < 1 {
				t.Fatalf("diagnostic lacks source position: %#v", diagnostic.Source)
			}
		})
	}
}

func TestParseMissingTargetReturnsStableCode(t *testing.T) {
	t.Parallel()

	_, err := ParseBytes("/workspace/missing.yaml", []byte("name: missing\n---\n- launchApp\n"))
	diagnostic := requireDiagnostic(t, err)
	if diagnostic.Code != "missing_app_target" {
		t.Fatalf("diagnostic = %#v, want missing_app_target", diagnostic)
	}
}

func TestParseUnknownCommandSuggestsNearestCanonicalKeyword(t *testing.T) {
	t.Parallel()

	_, err := ParseBytes("/workspace/typo.yaml", []byte("appId: com.example.app\n---\n- tapn: Continue\n"))
	diagnostic := requireDiagnostic(t, err)
	if diagnostic.Code != "unknown_command" || diagnostic.Suggestion != "tapOn" {
		t.Fatalf("diagnostic = %#v, want unknown_command with tapOn suggestion", diagnostic)
	}
	if diagnostic.Source.Start.Line != 3 || diagnostic.Source.Start.Column != 3 {
		t.Fatalf("diagnostic source = %#v, want line 3 column 3", diagnostic.Source)
	}
}

func TestParsePreservesObjectArgumentsAndBuildsTypedTraversalFields(t *testing.T) {
	t.Parallel()

	input := `appId: com.example.app
androidWebViewHierarchy: accessibility
custom:
  enabled: true
---
- tapOn:
    text: Continue
    id: com.example:id/continue
    below: Header
    containsDescendants:
      - text: Child
    traits: TEXT SQUARE
    index: -1
    enabled: true
    waitToSettleTimeoutMs: 30000
    label: primary tap
- runFlow:
    file: child.yaml
    env:
      OWNER: independently-authored
    when:
      platform: android
      visible:
        text: Ready
- repeat:
    times: "2"
    while:
      true: ${SHOULD_REPEAT}
    commands:
      - back
      - tapOn: Again
- retry:
    maxRetries: "2"
    commands:
      - scroll
- runScript: scripts/setup.js
- addMedia:
    - media/a.png
    - media/b.mp4
`
	path := filepath.Join(t.TempDir(), "root.yaml")
	parsed, err := ParseBytes(path, []byte(input))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}

	if got := parsed.Config.Ext["androidWebViewHierarchy"]; got != "accessibility" {
		t.Fatalf("androidWebViewHierarchy ext = %#v", got)
	}
	custom, ok := parsed.Config.Ext["custom"].(map[string]any)
	if !ok || custom["enabled"] != true {
		t.Fatalf("custom ext not preserved: %#v", parsed.Config.Ext["custom"])
	}

	tap := parsed.Commands[0]
	if tap.Selector == nil || tap.Selector.Below == nil || tap.Selector.Below.TextRegex == nil {
		t.Fatalf("typed relative selector missing: %#v", tap.Selector)
	}
	if got := *tap.Selector.Below.TextRegex; got != "Header" {
		t.Fatalf("below text = %q", got)
	}
	if got := tap.Selector.Traits; !reflect.DeepEqual(got, []model.ElementTrait{model.ElementTraitText, model.ElementTraitSquare}) {
		t.Fatalf("traits = %#v", got)
	}
	arguments, ok := tap.Arguments.(map[string]any)
	if !ok || arguments["waitToSettleTimeoutMs"] != int64(30000) {
		t.Fatalf("tap arguments not retained: %#v", tap.Arguments)
	}

	runFlow := parsed.Commands[1]
	if runFlow.Condition == nil || runFlow.Condition.Platform == nil || *runFlow.Condition.Platform != model.PlatformAndroid {
		t.Fatalf("runFlow condition = %#v", runFlow.Condition)
	}
	if got, want := runFlow.Links[0].ResolvedPath, filepath.Join(filepath.Dir(path), "child.yaml"); got != want {
		t.Fatalf("resolved flow link = %q, want %q", got, want)
	}
	runArguments := runFlow.Arguments.(map[string]any)
	if env, ok := runArguments["env"].(map[string]any); !ok || env["OWNER"] != "independently-authored" {
		t.Fatalf("documented/extension object shape was discarded: %#v", runFlow.Arguments)
	}

	repeat := parsed.Commands[2]
	if len(repeat.Children) != 2 || repeat.Condition == nil || repeat.Condition.ScriptCondition == nil {
		t.Fatalf("repeat traversal fields = %#v", repeat)
	}
	retry := parsed.Commands[3]
	if len(retry.Children) != 1 || retry.Children[0].Kind != model.CommandScroll {
		t.Fatalf("retry children = %#v", retry.Children)
	}
	if got := parsed.Commands[4].Links[0].ResolvedPath; got != filepath.Join(filepath.Dir(path), "scripts/setup.js") {
		t.Fatalf("script link = %q", got)
	}
	if got := len(parsed.Commands[5].Links); got != 2 {
		t.Fatalf("media link count = %d, want 2", got)
	}
}

func TestParseRejectsInvalidTypedSelectorAndConditionValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "selector bool", body: "- tapOn:\n    optional: yes\n", code: "selector_field_type"},
		{name: "selector unknown", body: "- tapOn:\n    madeUp: value\n", code: "unknown_selector_field"},
		{name: "trait enum", body: "- tapOn:\n    traits: TEXT ROUND\n", code: "invalid_selector_trait"},
		{name: "platform enum", body: "- runFlow:\n    file: child.yaml\n    when:\n      platform: desktop\n", code: "invalid_condition_platform"},
		{name: "condition unknown", body: "- runFlow:\n    file: child.yaml\n    when:\n      someday: true\n", code: "unknown_condition_field"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBytes("/workspace/typed-invalid.yaml", []byte("appId: com.example.app\n---\n"+test.body))
			diagnostic := requireDiagnostic(t, err)
			if diagnostic.Code != test.code {
				t.Fatalf("diagnostic code = %q, want %q (%v)", diagnostic.Code, test.code, diagnostic)
			}
		})
	}
}

func TestParseConfigAliasesHooksPropertiesAndSourceLocations(t *testing.T) {
	t.Parallel()

	input := `# independent fixture
_appId: com.example.alias
url: https://example.invalid
tags: [smoke, critical]
properties:
  owner: qa
onFlowStart:
  - launchApp
onFlowComplete:
  - stopApp
---

# first command
- back
`
	parsed, err := ParseBytes("/workspace/config.yaml", []byte(input))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if parsed.Config.AppID != "com.example.alias" || parsed.Config.URL != "https://example.invalid" {
		t.Fatalf("target aliases = %#v", parsed.Config)
	}
	if !reflect.DeepEqual(parsed.Config.Tags, []string{"smoke", "critical"}) {
		t.Fatalf("tags = %#v", parsed.Config.Tags)
	}
	if parsed.Config.Properties["owner"] != "qa" {
		t.Fatalf("properties = %#v", parsed.Config.Properties)
	}
	if len(parsed.Config.OnFlowStart) != 1 || parsed.Config.OnFlowStart[0].Kind != model.CommandLaunchApp {
		t.Fatalf("onFlowStart = %#v", parsed.Config.OnFlowStart)
	}
	if len(parsed.Config.OnFlowComplete) != 1 || parsed.Config.OnFlowComplete[0].Kind != model.CommandStopApp {
		t.Fatalf("onFlowComplete = %#v", parsed.Config.OnFlowComplete)
	}
	if got := parsed.Commands[0].Source.Start; got.Line != 14 || got.Column != 3 || got.Offset <= 0 {
		t.Fatalf("command source = %#v, want line 14 column 3 with byte offset", got)
	}
}

func TestParseJSEngineRequiresStringAndPreservesDocumentedToken(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBytes("/workspace/graaljs.yaml", []byte("appId: com.example.app\njsEngine: graaljs\n---\n- launchApp\n"))
	if err != nil {
		t.Fatalf("parse documented jsEngine token: %v", err)
	}
	if got := parsed.Config.Ext["jsEngine"]; got != "graaljs" {
		t.Fatalf("jsEngine = %#v, want graaljs", got)
	}

	// jsEngine is string-typed and coerces any scalar. Collections remain
	// invalid.
	parsed, err = ParseBytes("/workspace/coerced.yaml", []byte("appId: com.example.app\njsEngine: true\n---\n- launchApp\n"))
	if err != nil {
		t.Fatalf("parse coerced jsEngine: %v", err)
	}
	if got := parsed.Config.Ext["jsEngine"]; got != "true" {
		t.Fatalf("jsEngine = %#v, want the coerced text", got)
	}
	_, err = ParseBytes("/workspace/collection.yaml", []byte("appId: com.example.app\njsEngine: [a]\n---\n- launchApp\n"))
	diagnostic := requireDiagnostic(t, err)
	if diagnostic.Code != "config_field_type" || diagnostic.Source.Start.Line != 2 {
		t.Fatalf("collection jsEngine diagnostic = %#v", diagnostic)
	}
}

func TestParseRejectsCyclicYAMLAliasDeterministically(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^TestParseCyclicYAMLAliasHelper$")
	command.Env = append(os.Environ(), "FLOWBATON_CYCLIC_ALIAS_HELPER=1")
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		t.Fatalf("parser subprocess crashed instead of returning a diagnostic: %v", err)
	}
	want := "/workspace/cyclic.yaml:3:9: cyclic_yaml_alias: YAML alias cycle is not supported"
	if got := stdout.String(); got != want {
		t.Fatalf("cyclic alias diagnostic = %q, want %q", got, want)
	}
}

func TestParseCyclicYAMLAliasHelper(t *testing.T) {
	if os.Getenv("FLOWBATON_CYCLIC_ALIAS_HELPER") != "1" {
		return
	}
	_, err := ParseBytes("/workspace/cyclic.yaml", []byte(cyclicAliasFlow))
	if err == nil {
		_, _ = fmt.Fprint(os.Stdout, "<nil>")
		os.Exit(0)
	}
	_, _ = fmt.Fprint(os.Stdout, err)
	os.Exit(0)
}

func TestParsePreservesSharedAndDeepAcyclicAliases(t *testing.T) {
	t.Parallel()

	const depth = 256
	var input strings.Builder
	input.WriteString("appId: com.example.app\n")
	input.WriteString("alias0: &alias0\n  value: leaf\n")
	for index := 1; index <= depth; index++ {
		_, _ = fmt.Fprintf(&input, "alias%d: &alias%d\n  previous: *alias%d\n", index, index, index-1)
	}
	_, _ = fmt.Fprintf(&input, "shared: *alias%d\ncopy: *alias%d\n---\n- launchApp\n", depth, depth)

	parsed, err := ParseBytes("/workspace/acyclic.yaml", []byte(input.String()))
	if err != nil {
		t.Fatalf("parse deep acyclic aliases: %v", err)
	}
	shared, sharedOK := parsed.Config.Ext["shared"].(map[string]any)
	copyValue, copyOK := parsed.Config.Ext["copy"].(map[string]any)
	if !sharedOK || !copyOK || !reflect.DeepEqual(shared, copyValue) {
		t.Fatalf("shared aliases were not normalized independently: shared=%#v copy=%#v", shared, copyValue)
	}
}

func TestParseSwipeEnforcesFourVariantUnion(t *testing.T) {
	t.Parallel()

	valid := []string{
		"- swipe: {direction: DOWN, duration: 400}\n",
		"- swipe: {start: '10,20', end: '90,80'}\n",
		"- swipe: {start: '10%,20%', end: '90%,80%', duration: 400}\n",
		"- swipe: {from: {text: Card}, direction: LEFT, duration: 400}\n",
	}
	for _, command := range valid {
		if _, err := ParseBytes("/workspace/swipe-valid.yaml", []byte("appId: com.example.app\n---\n"+command)); err != nil {
			t.Errorf("valid swipe %q rejected: %v", command, err)
		}
	}

	invalid := []struct {
		name string
		body string
		code string
	}{
		{name: "start without end", body: "- swipe: {start: '10,20'}\n", code: "missing_command_field"},
		{name: "end without start", body: "- swipe: {end: '90,80'}\n", code: "missing_command_field"},
		{name: "from without direction", body: "- swipe: {from: {text: Card}}\n", code: "missing_command_field"},
		{name: "direction mixed with coordinates", body: "- swipe: {direction: DOWN, start: '10,20', end: '90,80'}\n", code: "conflicting_command_fields"},
		{name: "element mixed with coordinates", body: "- swipe: {from: {text: Card}, direction: LEFT, start: '10,20', end: '90,80'}\n", code: "conflicting_command_fields"},
	}
	for _, test := range invalid {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseBytes("/workspace/swipe-invalid.yaml", []byte("appId: com.example.app\n---\n"+test.body))
			diagnostic := requireDiagnostic(t, err)
			if diagnostic.Code != test.code || diagnostic.Source.Start.Line != 3 {
				t.Fatalf("swipe diagnostic = %#v, want code %q on line 3", diagnostic, test.code)
			}
		})
	}
}

func TestParseRejectsNegativeRandomLengthsAndEraseCounts(t *testing.T) {
	t.Parallel()

	variableLengthCommands := []string{
		"inputRandomText",
		"inputRandomNumber",
	}
	for _, keyword := range variableLengthCommands {
		keyword := keyword
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			negative := fmt.Sprintf("appId: com.example.app\n---\n- %s: {length: -1}\n", keyword)
			_, err := ParseBytes("/workspace/negative-length.yaml", []byte(negative))
			diagnostic := requireDiagnostic(t, err)
			if diagnostic.Code != "invalid_command_value" || diagnostic.Source.Start.Line != 3 {
				t.Fatalf("negative %s diagnostic = %#v", keyword, diagnostic)
			}

			zero := fmt.Sprintf("appId: com.example.app\n---\n- %s: {length: 0}\n", keyword)
			if _, err := ParseBytes("/workspace/zero-length.yaml", []byte(zero)); err != nil {
				t.Fatalf("zero %s length rejected: %v", keyword, err)
			}
		})
	}

	namedCommands := []string{
		"inputRandomEmail",
		"inputRandomPersonName",
		"inputRandomCityName",
		"inputRandomCountryName",
		"inputRandomColorName",
	}
	for _, keyword := range namedCommands {
		keyword := keyword
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			for _, length := range []int{-1, 0} {
				input := fmt.Sprintf("appId: com.example.app\n---\n- %s: {length: %d}\n", keyword, length)
				_, err := ParseBytes("/workspace/invalid-named-length.yaml", []byte(input))
				diagnostic := requireDiagnostic(t, err)
				if diagnostic.Code != "invalid_command_value" || diagnostic.Source.Start.Line != 3 {
					t.Fatalf("%s length %d diagnostic = %#v", keyword, length, diagnostic)
				}
			}
		})
	}

	t.Run("eraseText", func(t *testing.T) {
		t.Parallel()
		_, err := ParseBytes("/workspace/negative-erase.yaml", []byte("appId: com.example.app\n---\n- eraseText: -2\n"))
		diagnostic := requireDiagnostic(t, err)
		if diagnostic.Code != "invalid_command_value" || diagnostic.Source.Start.Line != 3 {
			t.Fatalf("negative eraseText diagnostic = %#v", diagnostic)
		}
		if _, err := ParseBytes("/workspace/zero-erase.yaml", []byte("appId: com.example.app\n---\n- eraseText: 0\n")); err != nil {
			t.Fatalf("zero eraseText count rejected: %v", err)
		}
	})
}

func TestParseWorkspaceNormalizesScalarListsAndPreservesUnknownWarnings(t *testing.T) {
	t.Parallel()

	input := `flows: "*.yaml"
includeTags: smoke
excludeTags: [slow, web]
executionOrder:
  continueOnFailure: true
  flowsOrder: [login.yaml, checkout.yaml]
platform:
  android:
    disableAnimations: true
  ios:
    disableAnimations: true
    snapshotKeyHonorModalViews: true
testOutputDir: artifacts
futureOption:
  enabled: true
`
	workspace, warnings, err := ParseWorkspaceBytes("/workspace/config.yaml", []byte(input))
	if err != nil {
		t.Fatalf("ParseWorkspaceBytes: %v", err)
	}
	if !reflect.DeepEqual(workspace.Flows, []string{"*.yaml"}) {
		t.Fatalf("flows = %#v", workspace.Flows)
	}
	if !reflect.DeepEqual(workspace.IncludeTags, []string{"smoke"}) || !reflect.DeepEqual(workspace.ExcludeTags, []string{"slow", "web"}) {
		t.Fatalf("tags = include %#v exclude %#v", workspace.IncludeTags, workspace.ExcludeTags)
	}
	if workspace.ExecutionOrder == nil || !workspace.ExecutionOrder.ContinueOnFailure || len(workspace.ExecutionOrder.FlowsOrder) != 2 {
		t.Fatalf("executionOrder = %#v", workspace.ExecutionOrder)
	}
	if workspace.Platform == nil || workspace.Platform.Android == nil || workspace.Platform.IOS == nil {
		t.Fatalf("platform config = %#v", workspace.Platform)
	}
	if got := workspace.Unknown["futureOption"].(map[string]any)["enabled"]; got != true {
		t.Fatalf("unknown config not preserved: %#v", workspace.Unknown)
	}
	if len(warnings) != 1 || warnings[0].Code != "unknown_workspace_key" {
		t.Fatalf("warnings = %#v", warnings)
	}
}

func TestParseCapturesTypedFieldSources(t *testing.T) {
	t.Parallel()

	input := `appId: com.example.app
url: https://example.invalid
---
- tapOn:
    css: '#submit'
- runFlow:
    file: child.yaml
    when:
      visible: Ready
`
	parsed, err := ParseBytes("/workspace/sources.yaml", []byte(input))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	urlSource := parsed.Config.FieldSources["url"]
	if urlSource.Start.Line != 2 || urlSource.Start.Column != 1 {
		t.Fatalf("url field source = %#v, want line 2 column 1", urlSource)
	}
	cssSource := parsed.Commands[0].Selector.FieldSources["css"]
	if cssSource.Start.Line != 5 || cssSource.Start.Column != 5 {
		t.Fatalf("css field source = %#v, want line 5 column 5", cssSource)
	}
	visibleSource := parsed.Commands[1].Condition.FieldSources["visible"]
	if visibleSource.Start.Line != 9 || visibleSource.Start.Column != 7 {
		t.Fatalf("visible field source = %#v, want line 9 column 7", visibleSource)
	}
}

func TestParsedRunFlowEquivalenceIgnoresResolvedFilesystemOrigin(t *testing.T) {
	t.Parallel()

	input := []byte("appId: com.example.app\n---\n- runFlow: child.yaml\n")
	left, err := ParseBytes("/workspace/one/root.yaml", input)
	if err != nil {
		t.Fatalf("parse left flow: %v", err)
	}
	right, err := ParseBytes("/workspace/two/root.yaml", input)
	if err != nil {
		t.Fatalf("parse right flow: %v", err)
	}
	leftLink := left.Commands[0].Links[0]
	rightLink := right.Commands[0].Links[0]
	if leftLink.ResolvedPath == rightLink.ResolvedPath {
		t.Fatalf("test requires distinct resolved paths: left=%q right=%q", leftLink.ResolvedPath, rightLink.ResolvedPath)
	}
	if !left.Commands[0].Equivalent(right.Commands[0]) {
		t.Fatal("the same authored runFlow command should check equal across parse directories")
	}
	if left.Commands[0].Links[0].ResolvedPath != leftLink.ResolvedPath || right.Commands[0].Links[0].ResolvedPath != rightLink.ResolvedPath {
		t.Fatal("Equivalent mutated resolved-path origin")
	}
}

func TestParseSourceSpansUseByteOffsetsAndLexicalScalarEnds(t *testing.T) {
	t.Parallel()

	t.Run("unicode scalar end column", func(t *testing.T) {
		input := "appId: com.example.app\n---\n- inputText: café\n"
		parsed, err := ParseBytes("/workspace/unicode.yaml", []byte(input))
		if err != nil {
			t.Fatalf("ParseBytes: %v", err)
		}
		source := parsed.Commands[0].Source
		wantStart := model.Position{Line: 3, Column: 3, Offset: strings.Index(input, "inputText")}
		wantEnd := model.Position{Line: 3, Column: 18, Offset: strings.Index(input, "café") + len("café")}
		if source.Start != wantStart || source.End != wantEnd {
			t.Fatalf("unicode command source = %#v, want start=%#v end=%#v", source, wantStart, wantEnd)
		}
	})

	t.Run("unicode before a field preserves byte offset", func(t *testing.T) {
		input := "appId: com.example.app\n---\n- tapOn: {text: 'é', id: submit}\n"
		parsed, err := ParseBytes("/workspace/unicode-field.yaml", []byte(input))
		if err != nil {
			t.Fatalf("ParseBytes: %v", err)
		}
		source := parsed.Commands[0].Selector.FieldSources["id"]
		wantStart := model.Position{Line: 3, Column: 22, Offset: strings.Index(input, "id: submit")}
		wantEnd := model.Position{Line: 3, Column: 24, Offset: strings.Index(input, "id: submit") + len("id")}
		if source.Start != wantStart || source.End != wantEnd {
			t.Fatalf("post-unicode field source = %#v, want start=%#v end=%#v", source, wantStart, wantEnd)
		}
	})

	tests := []struct {
		name    string
		command string
		wantEnd model.Position
	}{
		{
			name:    "double quoted escape",
			command: `- inputText: "line\nquoted"`,
			wantEnd: model.Position{Line: 3, Column: 28},
		},
		{
			name:    "single quoted escape",
			command: `- inputText: 'Ada''s phone'`,
			wantEnd: model.Position{Line: 3, Column: 28},
		},
		{
			name:    "multiline quoted scalar",
			command: "- inputText: \"first\n    second\"",
			wantEnd: model.Position{Line: 4, Column: 12},
		},
		{
			name:    "literal block scalar",
			command: "- inputText: |-\n    first\n    second",
			wantEnd: model.Position{Line: 5, Column: 11},
		},
		{
			name:    "folded block scalar",
			command: "- inputText: >-\n    first\n    second",
			wantEnd: model.Position{Line: 5, Column: 11},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			input := "appId: com.example.app\n---\n" + test.command
			parsed, err := ParseBytes("/workspace/lexical-span.yaml", []byte(input))
			if err != nil {
				t.Fatalf("ParseBytes: %v", err)
			}
			source := parsed.Commands[0].Source
			wantEnd := test.wantEnd
			wantEnd.Offset = len(input)
			if source.End != wantEnd {
				t.Fatalf("command source end = %#v, want %#v", source.End, wantEnd)
			}
		})
	}
}

func TestParseBuildsTraversalSelectorsForWrappedCommands(t *testing.T) {
	t.Parallel()

	input := `appId: com.example.app
---
- scrollUntilVisible:
    element: {css: '#scroll-target'}
- assertScreenshot:
    path: expected
    cropOn: {css: '#crop'}
- swipe:
    from: {css: '#swipe-source'}
    direction: DOWN
- extendedWaitUntil:
    visible: {css: '#eventual'}
    timeout: 20000
`
	parsed, err := ParseBytes("/workspace/wrapped-selectors.yaml", []byte(input))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	for index, command := range parsed.Commands[:3] {
		if command.Selector == nil || command.Selector.CSS == nil {
			t.Fatalf("command[%d] wrapped selector missing: %#v", index, command)
		}
	}
	extended := parsed.Commands[3]
	if extended.Condition == nil || extended.Condition.Visible == nil || extended.Condition.Visible.CSS == nil {
		t.Fatalf("extended wait condition missing: %#v", extended)
	}
	if source := extended.Condition.Visible.FieldSources["css"]; source.Start.Line != 12 {
		t.Fatalf("wrapped CSS source = %#v, want line 12", source)
	}
}

func requireDiagnostic(t *testing.T, err error) model.Diagnostic {
	t.Helper()
	if err == nil {
		t.Fatal("expected a diagnostic, got nil")
	}
	var diagnostic model.Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("error type = %T, want model.Diagnostic (%v)", err, err)
	}
	return diagnostic
}
