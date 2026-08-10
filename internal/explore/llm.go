package explore

import (
	"context"
	"encoding/json"
)

// Role identifies the author of a chat message.
type Role string

// Chat roles.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in an agent conversation. Text and ImagePNG may both
// be set (multimodal user turns). Assistant turns may carry tool calls;
// tool turns answer exactly one call by ID.
type Message struct {
	Role       Role
	Text       string
	ImagePNG   []byte
	ToolCalls  []ToolCall
	ToolCallID string
}

// ToolCall is a model-requested invocation of a registered tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolSpec declares a tool the model may call. Schema is a JSON Schema
// object for the arguments.
type ToolSpec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// ChatRequest is one model invocation.
type ChatRequest struct {
	Messages []Message
	Tools    []ToolSpec
	// ForceTool requires the model to call some tool this turn.
	ForceTool bool
	// MaxTokens caps the reply when positive.
	MaxTokens int
}

// Usage reports token spend for one invocation.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// ChatResponse is the model reply plus usage accounting.
type ChatResponse struct {
	Message Message
	Usage   Usage
}

// LLM is the narrow chat seam this package needs. The langchaingo-backed
// implementation lives in internal/aiengine so the provider dependency
// stays quarantined there.
type LLM interface {
	Chat(ctx context.Context, request ChatRequest) (ChatResponse, error)
}

// ModelSet groups the role tiers. Cheap fast workers consume screen dumps;
// a smarter manager reads only short summaries; a vision-capable model
// reads screenshots. Any field may hold the same underlying model.
type ModelSet struct {
	Worker  LLM
	Manager LLM
	Vision  LLM
}
