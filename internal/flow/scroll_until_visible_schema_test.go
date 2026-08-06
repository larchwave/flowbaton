package flow

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestScrollUntilVisibleSchemaAcceptsBatch3FieldsAndRetainsTypedElement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want map[string]any
	}{
		{
			name: "defaults",
			body: "element: Ready",
			want: map[string]any{"element": "Ready"},
		},
		{
			name: "integer timeout lower bounds",
			body: "element: {text: '${TARGET}'}\n    direction: UP\n    timeout: 0\n    speed: 1\n    visibilityPercentage: 10\n    waitToSettleTimeoutMs: 0\n    centerElement: false",
			want: map[string]any{
				"element": map[string]any{"text": "${TARGET}"}, "direction": "UP",
				"timeout": int64(0), "speed": int64(1), "visibilityPercentage": int64(10),
				"waitToSettleTimeoutMs": int64(0), "centerElement": false,
			},
		},
		{
			name: "string timeout upper bounds",
			body: "element: {text: Ready}\n    direction: DOWN\n    timeout: '120000'\n    speed: 100\n    visibilityPercentage: 100\n    waitToSettleTimeoutMs: 30000\n    centerElement: true",
			want: map[string]any{
				"element": map[string]any{"text": "Ready"}, "direction": "DOWN",
				"timeout": "120000", "speed": int64(100), "visibilityPercentage": int64(100),
				"waitToSettleTimeoutMs": int64(30000), "centerElement": true,
			},
		},
		{
			name: "dynamic strings",
			body: "element: {text: '${TARGET}'}\n    direction: '${DIRECTION}'\n    timeout: '${TIMEOUT}'",
			want: map[string]any{
				"element":   map[string]any{"text": "${TARGET}"},
				"direction": "${DIRECTION}", "timeout": "${TIMEOUT}",
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			parsed, err := ParseBytes(
				"/workspace/scroll-search.yaml",
				[]byte("appId: com.example.app\n---\n- scrollUntilVisible:\n    "+test.body+"\n"),
			)
			if err != nil {
				t.Fatalf("ParseBytes() error = %v", err)
			}
			command := parsed.Commands[0]
			if command.Kind != model.CommandScrollUntilVisible || !reflect.DeepEqual(command.Arguments, test.want) {
				t.Fatalf("scrollUntilVisible command = %#v, want arguments %#v", command, test.want)
			}
			if command.Selector == nil || command.Selector.TextRegex == nil {
				t.Fatalf("typed element selector = %#v", command.Selector)
			}
		})
	}
}

func TestScrollUntilVisibleSchemaRejectsWrongTypesDirectionsAndMissingElement(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"{}",
		"{direction: DOWN}",
		"{element: Ready, direction: diagonal}",
		"{element: Ready, direction: sideways}",
		"{element: Ready, direction: sideways}",
		"{element: Ready, direction: ' DOWN'}",
		"{element: Ready, direction: true}",
		"{element: Ready, timeout: 1.5}",
		// speed is string-typed and accepts arbitrary text for runtime
		// resolution. See internal/flow/permissive_fields_test.go.
		"{element: Ready, visibilityPercentage: 'full'}",
		"{element: Ready, waitToSettleTimeoutMs: 'zero'}",
		"{element: Ready, centerElement: 'yes'}",
		"{element: Ready, mystery: true}",
	}
	for _, body := range invalid {
		_, err := ParseBytes(
			"/workspace/scroll-search.yaml",
			[]byte("appId: com.example.app\n---\n- scrollUntilVisible: "+body+"\n"),
		)
		if err == nil {
			t.Fatalf("ParseBytes(%q) error = nil", body)
		}
		var diagnostic model.Diagnostic
		if !errors.As(err, &diagnostic) {
			t.Fatalf("ParseBytes(%q) error = %T %v, want Diagnostic", body, err, err)
		}
	}
}
