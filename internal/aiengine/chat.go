package aiengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/tmc/langchaingo/llms"

	"github.com/larchwave/flowbaton/internal/explore"
)

// ChatClient implements explore.LLM on one llms.Model, keeping langchaingo
// quarantined in this package. A blank modelName uses the model the provider
// client was constructed with; a non-blank one overrides it per call, which
// is how the explore tiers share one provider client.
type ChatClient struct {
	model     llms.Model
	provider  Provider
	modelName string
	timeout   time.Duration
}

// NewChatClient wraps an already-constructed langchaingo model. The provider
// names the API dialect the model speaks, which decides how an image turn is
// encoded (see imagePart); blank keeps the Anthropic-shaped BinaryContent.
// Pass "" as modelName to use the client's own model; timeout zero (or out of
// range) falls back to DefaultProviderTimeout at call time.
func NewChatClient(model llms.Model, provider Provider, modelName string, timeout time.Duration) *ChatClient {
	return &ChatClient{model: model, provider: provider, modelName: modelName, timeout: timeout}
}

// Compile-time proof this satisfies the explore chat seam.
var _ explore.LLM = (*ChatClient)(nil)

// Chat runs one model invocation. Temperature is pinned to 0 and each call is
// bounded by the configured timeout.
func (c *ChatClient) Chat(ctx context.Context, request explore.ChatRequest) (explore.ChatResponse, error) {
	if c == nil || c.model == nil {
		return explore.ChatResponse{}, errors.New("aiengine: chat client has no model")
	}
	if len(request.Messages) == 0 {
		return explore.ChatResponse{}, errors.New("aiengine: chat requires at least one message")
	}
	if request.ForceTool && len(request.Tools) == 0 {
		return explore.ChatResponse{}, errors.New("aiengine: forcing a tool call requires at least one declared tool")
	}
	messages, err := chatMessages(c.provider, request.Messages)
	if err != nil {
		return explore.ChatResponse{}, err
	}
	options := []llms.CallOption{llms.WithTemperature(0)}
	if c.modelName != "" {
		options = append(options, llms.WithModel(c.modelName))
	}
	if len(request.Tools) > 0 {
		options = append(options, llms.WithTools(chatTools(request.Tools)))
	}
	if request.ForceTool {
		// "required" is the strongest choice value the installed provider
		// clients pass through; a backend without a tool-choice knob
		// ignores the option, so forcing degrades to a request.
		options = append(options, llms.WithToolChoice("required"))
	}
	if request.MaxTokens > 0 {
		options = append(options, llms.WithMaxTokens(request.MaxTokens))
	}
	timeout := c.timeout
	if timeout <= 0 || timeout > MaxProviderTimeout {
		timeout = DefaultProviderTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := c.model.GenerateContent(callCtx, messages, options...)
	if err != nil {
		return explore.ChatResponse{}, fmt.Errorf("aiengine: chat model call failed: %w", err)
	}
	return chatResponse(response)
}

// chatMessages converts explore turns to langchaingo message contents. The
// input is only read, never mutated.
func chatMessages(provider Provider, messages []explore.Message) ([]llms.MessageContent, error) {
	converted := make([]llms.MessageContent, 0, len(messages))
	for i, message := range messages {
		role, err := chatRole(message.Role)
		if err != nil {
			return nil, fmt.Errorf("aiengine: message %d: %w", i, err)
		}
		var parts []llms.ContentPart
		if message.Role == explore.RoleTool {
			if message.ToolCallID == "" {
				return nil, fmt.Errorf("aiengine: message %d: a tool reply requires a tool call ID", i)
			}
			parts = append(parts, llms.ToolCallResponse{
				ToolCallID: message.ToolCallID,
				Content:    message.Text,
			})
		} else {
			if message.Text != "" {
				parts = append(parts, llms.TextPart(message.Text))
			}
			if len(message.ImagePNG) > 0 {
				parts = append(parts, imagePart(provider, message.ImagePNG))
			}
			for _, call := range message.ToolCalls {
				parts = append(parts, llms.ToolCall{
					ID:   call.ID,
					Type: "function",
					FunctionCall: &llms.FunctionCall{
						Name:      call.Name,
						Arguments: string(call.Arguments),
					},
				})
			}
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("aiengine: message %d has no content", i)
		}
		converted = append(converted, llms.MessageContent{Role: role, Parts: parts})
	}
	return converted, nil
}

func chatRole(role explore.Role) (llms.ChatMessageType, error) {
	switch role {
	case explore.RoleSystem:
		return llms.ChatMessageTypeSystem, nil
	case explore.RoleUser:
		return llms.ChatMessageTypeHuman, nil
	case explore.RoleAssistant:
		return llms.ChatMessageTypeAI, nil
	case explore.RoleTool:
		return llms.ChatMessageTypeTool, nil
	default:
		return "", fmt.Errorf("unknown chat role %q", role)
	}
}

// chatTools converts tool declarations to function declarations.
func chatTools(specs []explore.ToolSpec) []llms.Tool {
	tools := make([]llms.Tool, 0, len(specs))
	for _, spec := range specs {
		definition := &llms.FunctionDefinition{
			Name:        spec.Name,
			Description: spec.Description,
		}
		if len(spec.Schema) > 0 {
			definition.Parameters = spec.Schema
		}
		tools = append(tools, llms.Tool{Type: "function", Function: definition})
	}
	return tools
}

// chatResponse folds every choice into one assistant message. Some backends
// return one choice per content block (a text block and a tool call arrive as
// separate choices), so text concatenates and tool calls accumulate across
// all of them.
func chatResponse(response *llms.ContentResponse) (explore.ChatResponse, error) {
	if response == nil || len(response.Choices) == 0 {
		return explore.ChatResponse{}, errors.New("aiengine: chat model returned no choices")
	}
	message := explore.Message{Role: explore.RoleAssistant}
	var usage explore.Usage
	for _, choice := range response.Choices {
		if choice == nil {
			continue
		}
		message.Text += choice.Content
		for _, call := range choice.ToolCalls {
			if call.FunctionCall == nil {
				continue
			}
			converted := explore.ToolCall{ID: call.ID, Name: call.FunctionCall.Name}
			if arguments := call.FunctionCall.Arguments; arguments != "" {
				converted.Arguments = json.RawMessage(arguments)
			}
			message.ToolCalls = append(message.ToolCalls, converted)
		}
		// Backends that split content across choices stamp the same totals
		// on each one, so take the first non-zero set instead of summing.
		if usage == (explore.Usage{}) {
			usage = choiceUsage(choice.GenerationInfo)
		}
	}
	if message.Text == "" && len(message.ToolCalls) == 0 {
		return explore.ChatResponse{}, errors.New("aiengine: chat model returned an empty reply")
	}
	return explore.ChatResponse{Message: message, Usage: usage}, nil
}

// choiceUsage reads token totals best effort: key names differ per provider
// and absence means zero, never an error.
func choiceUsage(info map[string]any) explore.Usage {
	return explore.Usage{
		InputTokens:  tokenCount(info, "PromptTokens", "InputTokens"),
		OutputTokens: tokenCount(info, "CompletionTokens", "OutputTokens"),
	}
}

func tokenCount(info map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := info[key].(type) {
		case int:
			return value
		case int64:
			return int(value)
		case float64:
			return int(value)
		}
	}
	return 0
}
