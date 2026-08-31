package explore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrStopRequested is returned by a tool handler to end the loop cleanly.
var ErrStopRequested = errors.New("explore: stop requested")

// ErrDeviceUnreachable is returned (wrapped) by a tool handler when the
// driver's transport is gone -- the runner process died, the socket
// refuses. It ends the loop as an error: every further action would fail
// the same way, and a model told "tool failed" spends its whole budget on
// waits and retries against a dead endpoint (seen live, 2026-08-28).
var ErrDeviceUnreachable = errors.New("explore: device unreachable")

// ErrScreenUnobservable is returned (wrapped) by a tool handler when the
// driver answered the observation with a failure it says will repeat -- the
// app under test left the foreground, say. It ends the loop for the same
// reason ErrDeviceUnreachable does, but the device is fine and the cause must
// not be reported as an unreachable one. The tester has no tool that restores
// an app to the foreground: seen live 2026-08-31, a run spent ten of fifteen
// steps on waits, taps and swipes that all failed with the same message.
var ErrScreenUnobservable = errors.New("explore: screen cannot be observed")

// ToolHandler executes one tool call and returns the text shown to the
// model. Returning ErrStopRequested (possibly wrapped) ends the loop with
// Stopped set; any other error is reported to the model as a tool failure
// and the loop continues.
type ToolHandler func(ctx context.Context, args json.RawMessage) (string, error)

// ToolBox pairs tool declarations with their handlers.
type ToolBox struct {
	Specs    []ToolSpec
	Handlers map[string]ToolHandler
}

// LoopResult is the outcome of a bounded tool loop.
type LoopResult struct {
	// Messages is the full conversation including tool turns.
	Messages []Message
	Usage    Usage
	// Stopped reports that a handler requested the stop.
	Stopped bool
	// Exhausted reports that the iteration bound was hit before the
	// model finished.
	Exhausted bool
}

// RunToolLoop drives one bounded agent conversation: invoke the model,
// execute requested tools, feed results back, and repeat until the model
// answers without tool calls, a handler requests a stop, the bound is hit,
// or the context ends. The input slice is never mutated.
func RunToolLoop(ctx context.Context, llm LLM, messages []Message, box ToolBox, maxIterations int) (LoopResult, error) {
	if llm == nil {
		return LoopResult{}, errors.New("explore: nil llm")
	}
	if maxIterations <= 0 {
		return LoopResult{}, errors.New("explore: non-positive iteration bound")
	}
	conversation := append([]Message(nil), messages...)
	result := LoopResult{}
	for iteration := 0; iteration < maxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			result.Messages = conversation
			return result, err
		}
		response, err := llm.Chat(ctx, ChatRequest{Messages: conversation, Tools: box.Specs})
		if err != nil {
			result.Messages = conversation
			return result, err
		}
		result.Usage.InputTokens += response.Usage.InputTokens
		result.Usage.OutputTokens += response.Usage.OutputTokens
		conversation = append(conversation, response.Message)
		if len(response.Message.ToolCalls) == 0 {
			result.Messages = conversation
			return result, nil
		}
		for _, call := range response.Message.ToolCalls {
			text, handled := runToolCall(ctx, box, call)
			conversation = append(conversation, Message{
				Role:       RoleTool,
				Text:       text,
				ToolCallID: call.ID,
			})
			if handled != nil {
				if errors.Is(handled, ErrStopRequested) {
					result.Messages = conversation
					result.Stopped = true
					return result, nil
				}
				result.Messages = conversation
				return result, handled
			}
		}
	}
	result.Messages = conversation
	result.Exhausted = true
	return result, nil
}

// runToolCall executes one call. Handler failures other than stop and
// context errors become model-visible text, not loop errors.
func runToolCall(ctx context.Context, box ToolBox, call ToolCall) (string, error) {
	handler, ok := box.Handlers[call.Name]
	if !ok {
		return fmt.Sprintf("unknown tool %q", call.Name), nil
	}
	text, err := handler(ctx, call.Arguments)
	if err == nil {
		return text, nil
	}
	if errors.Is(err, ErrStopRequested) || errors.Is(err, ErrDeviceUnreachable) ||
		errors.Is(err, ErrScreenUnobservable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return text, err
	}
	return fmt.Sprintf("tool %s failed: %v", call.Name, err), nil
}
