package flow

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch4ASchemaAcceptsExactTextAndEraseForms(t *testing.T) {
	t.Parallel()

	flow, err := ParseBytes("/workspace/batch4a.yaml", []byte(`appId: com.example.batch4a
---
- inputText: ""
- inputText:
    text: "hello ${NAME}\n世界"
    label: "typing ${NAME}"
- inputText:
    text: "without label"
- inputText:
    text: "empty label"
    label: ""
- eraseText
- eraseText: 0
- eraseText: 1
- eraseText: "50"
- eraseText: 100
- eraseText: "${COUNT}"
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if len(flow.Commands) != 10 {
		t.Fatalf("commands = %d, want 10", len(flow.Commands))
	}

	scalar := flow.Commands[0]
	if scalar.Kind != model.CommandInputText || scalar.Form != model.CommandFormObject || scalar.Arguments != "" || scalar.Label != nil {
		t.Fatalf("scalar inputText = %#v", scalar)
	}
	object := flow.Commands[1]
	wantObject := map[string]any{"text": "hello ${NAME}\n世界", "label": "typing ${NAME}"}
	if object.Kind != model.CommandInputText || !reflect.DeepEqual(object.Arguments, wantObject) ||
		object.Label == nil || *object.Label != "typing ${NAME}" {
		t.Fatalf("object inputText = %#v, want arguments %#v and typed label", object, wantObject)
	}
	withoutLabel := flow.Commands[2]
	if withoutLabel.Kind != model.CommandInputText || !reflect.DeepEqual(withoutLabel.Arguments, map[string]any{"text": "without label"}) || withoutLabel.Label != nil {
		t.Fatalf("object inputText without label = %#v", withoutLabel)
	}
	emptyLabel := flow.Commands[3]
	if emptyLabel.Label == nil || *emptyLabel.Label != "" || !reflect.DeepEqual(emptyLabel.Arguments, map[string]any{"text": "empty label", "label": ""}) {
		t.Fatalf("object inputText empty label = %#v", emptyLabel)
	}
	if flow.Commands[4].Kind != model.CommandEraseText || flow.Commands[4].Form != model.CommandFormScalar || flow.Commands[4].Arguments != nil {
		t.Fatalf("bare eraseText = %#v", flow.Commands[4])
	}
	for index, want := range []any{int64(0), int64(1), "50", int64(100), "${COUNT}"} {
		command := flow.Commands[index+5]
		if command.Kind != model.CommandEraseText || command.Form != model.CommandFormObject || !reflect.DeepEqual(command.Arguments, want) {
			t.Fatalf("eraseText command %d = %#v, want %#v", index, command, want)
		}
	}
}

func TestInteractionBatch4ASchemaRejectsWrongObjectFieldsAndIntegerBounds(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"- inputText",
		// Map-form text and label must be strings when present. Unknown fields
		// remain permitted by the inputText object contract; see
		// runtime_contract_test.go.
		"- inputText: {text: 7}",            // text must be a string, not an int
		"- inputText: {text: ok, label: 7}", // label must be a string, not an int
		// Bare scalar inputText is covered by scalar_selector_test.go.
		"- inputText: null",
		"- inputText: [a, b]",
		"- eraseText: -1",
		"- eraseText: 101",
		"- eraseText: '101'",
		"- eraseText: '4294967296'",
		"- eraseText: 9223372036854775808",
		"- eraseText: ''",
		"- eraseText: ' 1'",
		"- eraseText: '+1'",
		"- eraseText: 1.5",
		"- eraseText: '1.0'",
		"- eraseText: '1e2'",
		"- eraseText: true",
		"- eraseText: null",
		"- eraseText: {count: 1}",
		"- eraseText: [1]",
	}
	for _, command := range invalid {
		_, err := ParseBytes(
			"/workspace/batch4a-invalid.yaml",
			[]byte("appId: com.example.batch4a\n---\n"+command+"\n"),
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
