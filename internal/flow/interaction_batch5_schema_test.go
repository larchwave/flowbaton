package flow

import (
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

func TestInteractionBatch5SchemaRetainsClipboardGrammar(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBytes("/workspace/batch5.yaml", []byte(`appId: com.example.batch5
---
- setClipboard: ""
- setClipboard: "${VALUE}"
- copyTextFrom: "Title ${SUFFIX}"
- copyTextFrom:
    id: "${TARGET_ID}"
    containsChild:
      text: "${CHILD}"
    optional: true
    label: "copy ${TARGET}"
- pasteText
`))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	want := []model.CommandKeyword{
		model.CommandSetClipboard,
		model.CommandSetClipboard,
		model.CommandCopyTextFrom,
		model.CommandCopyTextFrom,
		model.CommandPasteText,
	}
	if len(parsed.Commands) != len(want) {
		t.Fatalf("commands = %d, want %d", len(parsed.Commands), len(want))
	}
	for index, keyword := range want {
		if parsed.Commands[index].Kind != keyword {
			t.Fatalf("command %d kind = %s, want %s", index, parsed.Commands[index].Kind, keyword)
		}
	}
	if parsed.Commands[0].Form != model.CommandFormObject || parsed.Commands[0].Arguments != "" ||
		parsed.Commands[1].Arguments != "${VALUE}" {
		t.Fatalf("setClipboard commands = %#v / %#v", parsed.Commands[0], parsed.Commands[1])
	}
	scalarCopy := parsed.Commands[2]
	if scalarCopy.Form != model.CommandFormObject || scalarCopy.Arguments != "Title ${SUFFIX}" ||
		scalarCopy.Selector == nil || scalarCopy.Selector.TextRegex == nil || *scalarCopy.Selector.TextRegex != "Title ${SUFFIX}" ||
		scalarCopy.Optional != nil || scalarCopy.Label != nil {
		t.Fatalf("scalar copyTextFrom = %#v", scalarCopy)
	}
	objectCopy := parsed.Commands[3]
	wantArguments := map[string]any{
		"id": "${TARGET_ID}", "containsChild": map[string]any{"text": "${CHILD}"},
		"optional": true, "label": "copy ${TARGET}",
	}
	if !reflect.DeepEqual(objectCopy.Arguments, wantArguments) || objectCopy.Selector == nil ||
		objectCopy.Selector.IDRegex == nil || *objectCopy.Selector.IDRegex != "${TARGET_ID}" ||
		objectCopy.Selector.ContainsChild == nil || objectCopy.Selector.ContainsChild.TextRegex == nil ||
		*objectCopy.Selector.ContainsChild.TextRegex != "${CHILD}" || objectCopy.Optional == nil || !*objectCopy.Optional ||
		objectCopy.Label == nil || *objectCopy.Label != "copy ${TARGET}" {
		t.Fatalf("object copyTextFrom = %#v", objectCopy)
	}
	if parsed.Commands[4].Form != model.CommandFormScalar || parsed.Commands[4].Arguments != nil {
		t.Fatalf("pasteText command = %#v", parsed.Commands[4])
	}
}

func TestInteractionBatch5SchemaRejectsSetAndPasteWrongForms(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"- setClipboard",
		"- setClipboard: null",
		"- setClipboard: true",
		"- setClipboard: 1",
		"- setClipboard: 1.5",
		"- setClipboard: [a, b]",
		"- setClipboard: {text: value}",
		"- pasteText: value",
		"- pasteText: null",
		"- pasteText: true",
		"- pasteText: 1",
		"- pasteText: []",
		// label and optional are universal command fields on
		// pasteText; only non-scalar/unknown shapes stay invalid.
		"- pasteText: {mystery: value}",
	}
	for _, command := range invalid {
		_, err := ParseBytes(
			"/workspace/batch5-invalid.yaml",
			[]byte("appId: com.example.batch5\n---\n"+command+"\n"),
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

func TestInteractionBatch5SchemaPreservesFrozenSelectorFieldsForPrivateCompilerRejection(t *testing.T) {
	t.Parallel()

	parsed, err := ParseBytes("/workspace/batch5-frozen-selector.yaml", []byte(`appId: com.example.batch5
---
- copyTextFrom:
    id: target
    point: 10,20
    retryTapIfNoChange: true
    waitUntilVisible: true
    repeat: 2
    delay: 1
    waitToSettleTimeoutMs: 5
    css: "#target"
`))
	if err != nil {
		t.Fatalf("frozen parser must retain v0 selector fields before private compiler rejection: %v", err)
	}
	if len(parsed.Commands) != 1 || parsed.Commands[0].Selector == nil ||
		parsed.Commands[0].Selector.Point == nil || parsed.Commands[0].Selector.RetryTapIfNoChange == nil ||
		parsed.Commands[0].Selector.WaitUntilVisible == nil || parsed.Commands[0].Selector.Repeat == nil ||
		parsed.Commands[0].Selector.Delay == nil || parsed.Commands[0].Selector.WaitToSettleTimeoutMS == nil ||
		parsed.Commands[0].Selector.CSS == nil {
		t.Fatalf("frozen selector snapshot = %#v", parsed.Commands)
	}
}
