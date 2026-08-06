package flow

import (
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch2BSchemaRetainsExactStaticActionGrammar(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBytes("/workspace/batch2b.yaml", []byte(`appId: com.example.batch2b
---
- action: back
- action: hideKeyboard
- action: scroll
- action: pasteText
- action: clearKeychain
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	want := []string{"back", "hideKeyboard", "scroll", "pasteText", "clearKeychain"}
	if len(parsed.Commands) != len(want) {
		t.Fatalf("commands = %#v, want %d", parsed.Commands, len(want))
	}
	for index, alias := range want {
		command := parsed.Commands[index]
		if command.Kind != model.CommandAction || command.Form != model.CommandFormObject || command.Arguments != alias ||
			len(command.Children) != 0 || command.Condition != nil || len(command.Links) != 0 ||
			command.Label != nil || command.Optional != nil || command.Selector != nil {
			t.Fatalf("Action %q snapshot = %#v", alias, command)
		}
	}
	gotAliases := make([]any, len(parsed.Commands))
	for index := range parsed.Commands {
		gotAliases[index] = parsed.Commands[index].Arguments
	}
	wantAliases := make([]any, len(want))
	for index := range want {
		wantAliases[index] = want[index]
	}
	if !reflect.DeepEqual(gotAliases, wantAliases) {
		t.Fatalf("Action aliases = %#v, want %#v", gotAliases, wantAliases)
	}

	direct, err := ParseBytes("/workspace/batch2b-clear-direct.yaml", []byte("appId: com.example.batch2b\n---\n- clearKeychain\n"))
	if err != nil || len(direct.Commands) != 1 || direct.Commands[0].Kind != model.CommandClearKeychain ||
		direct.Commands[0].Form != model.CommandFormScalar {
		t.Fatalf("direct clearKeychain parser snapshot = %#v error %v", direct.Commands, err)
	}
}

func TestInteractionBatch2BSchemaRejectsDynamicTypeAndShape(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"- action",
		"- action: null",
		"- action: ''",
		"- action: ' '",
		"- action: unknown",
		"- action: '${ACTION}'",
		"- action: 'prefix-${ACTION}'",
		"- action: true",
		"- action: 1",
		"- action: 1.5",
		"- action: []",
		"- action: [back]",
		"- action: {}",
		"- action: {alias: back}",
		"- {action: back, scroll: null}",
	}
	for _, command := range invalid {
		_, err := ParseBytes(
			"/workspace/batch2b-invalid.yaml",
			[]byte("appId: com.example.batch2b\n---\n"+command+"\n"),
		)
		if err == nil {
			t.Fatalf("ParseBytes(%q) error = nil", command)
		}
		var diagnostic model.Diagnostic
		if !errors.As(err, &diagnostic) {
			t.Fatalf("ParseBytes(%q) error = %T %v, want Diagnostic", command, err, err)
		}
	}
}
