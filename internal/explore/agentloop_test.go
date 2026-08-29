package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestRunToolLoopEndsWithDeviceUnreachable(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "1", Name: "wait", Arguments: json.RawMessage(`{}`)}}},
	}}
	box := ToolBox{Handlers: map[string]ToolHandler{
		"wait": func(context.Context, json.RawMessage) (string, error) {
			return "", fmt.Errorf("%w: dial refused", ErrDeviceUnreachable)
		},
	}}
	_, err := RunToolLoop(context.Background(), llm, nil, box, 3)
	if !errors.Is(err, ErrDeviceUnreachable) {
		t.Fatalf("loop error = %v, want ErrDeviceUnreachable", err)
	}
}
