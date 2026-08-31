package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

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

// ChatJSON asks the model once, and asks again when the reply does not
// decode -- carrying the rejected reply and the error, since the model is
// the only one who can see what it cut off. A transport failure is returned
// as it is: that is a provider to wait on, not a reply to correct. A reply
// that decodes into the wrong answer is the model's answer and is returned
// too; only unreadable ones are worth a second call.
func ChatJSON(ctx context.Context, llm LLM, request ChatRequest, target any) (ChatResponse, error) {
	if llm == nil {
		return ChatResponse{}, errors.New("explore: nil llm")
	}
	response, err := llm.Chat(ctx, request)
	if err != nil {
		return ChatResponse{}, err
	}
	decodeErr := DecodeReply(response.Message.Text, target)
	if decodeErr == nil {
		return response, nil
	}
	// The model is the only one who can see why its own reply did not
	// parse, so the second attempt carries the reply and the error rather
	// than repeating the question blind. Appending to a fresh slice keeps
	// the caller's request untouched for a caller that reuses it.
	retry := ChatRequest{Messages: append(append([]Message(nil), request.Messages...),
		response.Message,
		Message{
			Role: RoleUser,
			Text: fmt.Sprintf("That reply did not decode: %v. Answer again with only the JSON object in the required shape.", decodeErr),
		})}
	response, err = llm.Chat(ctx, retry)
	if err != nil {
		return ChatResponse{}, err
	}
	if decodeErr = DecodeReply(response.Message.Text, target); decodeErr != nil {
		return response, decodeErr
	}
	return response, nil
}

// ErrUnreadableReply marks a failure to decode what a model sent, as
// opposed to a failure to reach it at all. Callers that fold both into one
// message -- the outcome judge writes the reason into the report -- need to
// tell an operator which of the two happened.
var ErrUnreadableReply = errors.New("unreadable model reply")

// unreadableReply carries the mark without printing it: the decode error and
// the rejected reply are already the whole message, and this text is fed back
// to the model on a retry.
type unreadableReply struct{ err error }

func (u unreadableReply) Error() string        { return u.err.Error() }
func (u unreadableReply) Unwrap() error        { return u.err }
func (u unreadableReply) Is(target error) bool { return target == ErrUnreadableReply }

func DecodeReply(text string, target any) error {
	unfenced := UnfencedJSON(text)
	err := strictjson.Decode([]byte(unfenced), target)
	if err == nil {
		return nil
	}
	if around, offset, ok := replyFaultSite(unfenced, err); ok {
		return unreadableReply{fmt.Errorf(
			"%w; the reply reads %q around byte %d", err, around, offset)}
	}
	return unreadableReply{fmt.Errorf("%w; reply begins %q", err, replyExcerpt(text))}
}

func replyExcerpt(text string) string {
	// Truncate rather than a byte slice: the excerpt is quoted back to the
	// model on the retry, and a reply cut through a character would feed it
	// its own broken text as the example of what not to send.
	return Truncate(strings.Join(strings.Fields(text), " "), replyExcerptLimit)
}

// replyFaultSite quotes the reply around the byte the decoder stopped on. A
// long reply breaks where the model lost track, which its opening does not
// show: two planning replies died on "invalid character '}' after array
// element" with an excerpt that stopped inside the first scenario. The
// window serves the retry too, since the model needs the place it went
// wrong rather than its own first words.
//
// A fault inside the opening excerpt keeps the opening: there the head IS
// the site, and it reads as the whole short reply rather than a slice of it.
func replyFaultSite(unfenced string, err error) (string, int64, bool) {
	var syntax *json.SyntaxError
	if !errors.As(err, &syntax) {
		return "", 0, false
	}
	offset := syntax.Offset
	if offset <= replyExcerptLimit || offset > int64(len(unfenced)) {
		return "", 0, false
	}
	start := int(offset) - replyExcerptLimit/2
	for start > 0 && !utf8.RuneStart(unfenced[start]) {
		start--
	}
	end := int(offset) + replyExcerptLimit/2
	if end > len(unfenced) {
		end = len(unfenced)
	}
	for end < len(unfenced) && !utf8.RuneStart(unfenced[end]) {
		end++
	}
	return strings.Join(strings.Fields(unfenced[start:end]), " "), offset, true
}
