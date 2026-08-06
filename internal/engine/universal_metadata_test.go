package engine

import (
	"context"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

// TestFiveCommandsAcceptLabelAndOptional pins universal metadata across five
// command compilers. A sibling `when:` remains invalid.
func TestFiveCommandsAcceptLabelAndOptional(t *testing.T) {
	t.Parallel()

	label := "labelled"
	optional := true
	for _, test := range []struct {
		name    string
		compile func(model.Command) (any, error)
		command model.Command
	}{
		{
			name:    "scrollUntilVisible",
			compile: compileScrollUntilVisible,
			command: model.Command{
				Kind: model.CommandScrollUntilVisible, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"element":   map[string]any{"text": "Target"},
					"direction": "DOWN",
				},
				Selector: &model.ElementSelector{TextRegex: stringPointer("Target")},
			},
		},
		{
			name:    "extendedWaitUntil",
			compile: compileExtendedWaitUntil,
			command: model.Command{
				Kind: model.CommandExtendedWaitUntil, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"visible": map[string]any{"text": "Target"},
					"timeout": int64(1200),
				},
				Condition: &model.Condition{Visible: &model.ElementSelector{TextRegex: stringPointer("Target")}},
			},
		},
		{
			name:    "swipe",
			compile: compileSwipe,
			command: model.Command{
				Kind: model.CommandSwipe, Form: model.CommandFormObject,
				Arguments: map[string]any{"direction": "UP"},
			},
		},
		{
			name: "repeat",
			compile: func(command model.Command) (any, error) {
				return compileRepeat(context.Background(), compileContext{}, command)
			},
			command: model.Command{
				Kind: model.CommandRepeat, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"times":    int64(1),
					"commands": []any{map[string]any{"action": "step"}},
				},
				Children: []model.Command{repeatActionCommand("step")},
			},
		},
		{
			name: "retry",
			compile: func(command model.Command) (any, error) {
				return compileRetry(context.Background(), compileContext{}, command)
			},
			command: model.Command{
				Kind: model.CommandRetry, Form: model.CommandFormObject,
				Arguments: map[string]any{
					"maxRetries": int64(1),
					"commands":   []any{map[string]any{"action": "step"}},
				},
				Children: []model.Command{retryActionCommand("step")},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			bare := cloneCommand(test.command)
			if _, err := test.compile(bare); err != nil {
				t.Fatalf("control: bare %s does not compile: %v", test.name, err)
			}

			labelled := cloneCommand(test.command)
			labelled.Label = &label
			if _, err := test.compile(labelled); err != nil {
				t.Fatalf("%s with a label = %v, want it accepted", test.name, err)
			}

			marked := cloneCommand(test.command)
			marked.Optional = &optional
			if _, err := test.compile(marked); err != nil {
				t.Fatalf("%s with optional = %v, want it accepted", test.name, err)
			}

			conditioned := cloneCommand(test.command)
			conditioned.Condition = &model.Condition{}
			if _, err := test.compile(conditioned); err == nil {
				t.Fatalf("%s with a sibling condition compiled; the parser contract reject that shape", test.name)
			}
		})
	}
}
