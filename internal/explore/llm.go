package explore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/strictjson"
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

// UnfencedJSON strips the markdown code fence real models wrap around a
// JSON-only reply even when told not to (proven live with gpt-4o on
// 2026-08-11). Bare replies pass through untouched; the decode after it
// stays strict, so tolerance stops at the wrapper.
func UnfencedJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	if end := strings.LastIndex(trimmed, "```"); end >= 0 {
		trimmed = trimmed[:end]
	}
	return strings.TrimSpace(trimmed)
}

// replyExcerptLimit bounds how much of a rejected reply a decode error quotes.
const replyExcerptLimit = 240

// DecodeReply unfences a model reply and decodes it strictly into target. A
// decode failure quotes the start of the reply, because the syntax error
// alone ("invalid character after object key") says nothing about what the
// model actually sent. Every model-reply decode goes through here.
func DecodeReply(text string, target any) error {
	if err := strictjson.Decode([]byte(UnfencedJSON(text)), target); err != nil {
		return fmt.Errorf("%w; reply begins %q", err, replyExcerpt(text))
	}
	return nil
}

func replyExcerpt(text string) string {
	excerpt := strings.Join(strings.Fields(text), " ")
	if len(excerpt) > replyExcerptLimit {
		excerpt = excerpt[:replyExcerptLimit] + "…"
	}
	return excerpt
}
