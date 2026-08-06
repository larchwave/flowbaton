package flow

import (
	"errors"
	"reflect"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

func TestSwipeSchemaBatch1BAcceptsRatifiedFormsAndRetainsTypedFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want map[string]any
	}{
		{name: "direction dynamic", body: "direction: '${DIRECTION}'", want: map[string]any{"direction": "${DIRECTION}"}},
		{name: "coordinate settle", body: "start: '${START}'\n    end: '${END}'\n    duration: 60000\n    waitToSettleTimeoutMs: 30000", want: map[string]any{"start": "${START}", "end": "${END}", "duration": int64(60000), "waitToSettleTimeoutMs": int64(30000)}},
		{name: "from", body: "from:\n      text: '${TARGET}'\n    direction: RIGHT\n    duration: 1\n    waitToSettleTimeoutMs: 0", want: map[string]any{"from": map[string]any{"text": "${TARGET}"}, "direction": "RIGHT", "duration": int64(1), "waitToSettleTimeoutMs": int64(0)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseBytes("/workspace/swipe.yaml", []byte("appId: com.example.app\n---\n- swipe:\n    "+test.body+"\n"))
			if err != nil {
				t.Fatalf("ParseBytes() error = %v", err)
			}
			command := parsed.Commands[0]
			if command.Kind != model.CommandSwipe || !reflect.DeepEqual(command.Arguments, test.want) {
				t.Fatalf("swipe command = %#v, want arguments %#v", command, test.want)
			}
			if test.name == "from" && (command.Selector == nil || command.Selector.TextRegex == nil || *command.Selector.TextRegex != "${TARGET}") {
				t.Fatalf("typed from selector = %#v", command.Selector)
			}
		})
	}
}

func TestSwipeSchemaBatch1BRejectsWrongTypesUnknownAndUnionConflicts(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"direction: true",
		"direction: UP\n    duration: 1.5",
		"direction: UP\n    duration: 9223372036854775808",
		"direction: UP\n    waitToSettleTimeoutMs: 'zero'",
		"direction: UP\n    waitToSettleTimeoutMs: 9223372036854775808",
		"direction: UP\n    unknown: true",
		"start: '0,0'",
		"end: '1,1'",
		"from:\n      text: Continue",
		"direction: UP\n    start: '0,0'\n    end: '1,1'",
	} {
		_, err := ParseBytes("/workspace/swipe.yaml", []byte("appId: com.example.app\n---\n- swipe:\n    "+body+"\n"))
		if err == nil {
			t.Fatalf("ParseBytes(%q) error = nil", body)
		}
		var diagnostic model.Diagnostic
		if !errors.As(err, &diagnostic) {
			t.Fatalf("ParseBytes(%q) error = %T %v, want Diagnostic", body, err, err)
		}
	}
}
