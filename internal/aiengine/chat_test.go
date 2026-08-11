package aiengine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tmc/langchaingo/llms"

	"github.com/larchwave/flowbaton/internal/explore"
)

// scriptedChatModel is a scripted llms.Model: it records the messages and
// applied call options of the last invocation and returns a canned response
// (or error), so mapping logic runs with no network.
type scriptedChatModel struct {
	response       *llms.ContentResponse
	err            error
	waitForContext bool

	lastMessages []llms.MessageContent
	lastOptions  llms.CallOptions
	calls        int
}

func (s *scriptedChatModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	s.calls++
	s.lastMessages = messages
	s.lastOptions = llms.CallOptions{}
	for _, option := range options {
		option(&s.lastOptions)
	}
	if s.waitForContext {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func (s *scriptedChatModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("unused")
}

func textResponse(text string) *llms.ContentResponse {
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: text}}}
}

func TestChatMapsRolesAndParts(t *testing.T) {
	t.Parallel()
	fake := &scriptedChatModel{response: textResponse("ok")}
	client := NewChatClient(fake, "", "", 0)

	request := explore.ChatRequest{Messages: []explore.Message{
		{Role: explore.RoleSystem, Text: "be brief"},
		{Role: explore.RoleUser, Text: "what is on screen?", ImagePNG: pngFixture},
		{Role: explore.RoleAssistant, Text: "tapping", ToolCalls: []explore.ToolCall{
			{ID: "call-1", Name: "tap", Arguments: json.RawMessage(`{"index":3}`)},
		}},
		{Role: explore.RoleTool, Text: "tapped", ToolCallID: "call-1"},
	}}
	if _, err := client.Chat(context.Background(), request); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	sent := fake.lastMessages
	if len(sent) != 4 {
		t.Fatalf("sent %d messages, want 4", len(sent))
	}

	wantRoles := []llms.ChatMessageType{
		llms.ChatMessageTypeSystem,
		llms.ChatMessageTypeHuman,
		llms.ChatMessageTypeAI,
		llms.ChatMessageTypeTool,
	}
	for i, want := range wantRoles {
		if sent[i].Role != want {
			t.Fatalf("message %d role = %q, want %q", i, sent[i].Role, want)
		}
	}

	if text, ok := sent[0].Parts[0].(llms.TextContent); !ok || text.Text != "be brief" {
		t.Fatalf("system part = %#v", sent[0].Parts[0])
	}
	if len(sent[1].Parts) != 2 {
		t.Fatalf("user parts = %#v", sent[1].Parts)
	}
	if binary, ok := sent[1].Parts[1].(llms.BinaryContent); !ok || binary.MIMEType != "image/png" || len(binary.Data) != len(pngFixture) {
		t.Fatalf("image part = %#v", sent[1].Parts[1])
	}
	if len(sent[2].Parts) != 2 {
		t.Fatalf("assistant parts = %#v", sent[2].Parts)
	}
	call, ok := sent[2].Parts[1].(llms.ToolCall)
	if !ok || call.ID != "call-1" || call.Type != "function" {
		t.Fatalf("tool call part = %#v", sent[2].Parts[1])
	}
	if call.FunctionCall == nil || call.FunctionCall.Name != "tap" || call.FunctionCall.Arguments != `{"index":3}` {
		t.Fatalf("function call = %#v", call.FunctionCall)
	}
	reply, ok := sent[3].Parts[0].(llms.ToolCallResponse)
	if !ok || reply.ToolCallID != "call-1" || reply.Content != "tapped" {
		t.Fatalf("tool reply part = %#v", sent[3].Parts[0])
	}
}

func TestChatMessageValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		messages []explore.Message
		wantErr  string
	}{
		{"no messages", nil, "at least one message"},
		{"unknown role", []explore.Message{{Role: "narrator", Text: "x"}}, "unknown chat role"},
		{"tool reply without id", []explore.Message{{Role: explore.RoleTool, Text: "done"}}, "tool call ID"},
		{"empty message", []explore.Message{{Role: explore.RoleUser}}, "no content"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fake := &scriptedChatModel{response: textResponse("ok")}
			client := NewChatClient(fake, "", "", 0)
			_, err := client.Chat(context.Background(), explore.ChatRequest{Messages: tc.messages})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
			if fake.calls != 0 {
				t.Fatalf("model was called %d times before validation failed", fake.calls)
			}
		})
	}
}

func TestChatDeclaresToolsAndOptions(t *testing.T) {
	t.Parallel()
	fake := &scriptedChatModel{response: textResponse("ok")}
	client := NewChatClient(fake, "", "worker-mini", 0)

	schema := json.RawMessage(`{"type":"object","properties":{"index":{"type":"integer"}}}`)
	request := explore.ChatRequest{
		Messages: []explore.Message{{Role: explore.RoleUser, Text: "act"}},
		Tools: []explore.ToolSpec{
			{Name: "tap", Description: "tap an element", Schema: schema},
			{Name: "stop", Description: "end the run"},
		},
		ForceTool: true,
		MaxTokens: 512,
	}
	if _, err := client.Chat(context.Background(), request); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	opts := fake.lastOptions
	if len(opts.Tools) != 2 {
		t.Fatalf("tools = %#v", opts.Tools)
	}
	first := opts.Tools[0]
	if first.Type != "function" || first.Function == nil || first.Function.Name != "tap" || first.Function.Description != "tap an element" {
		t.Fatalf("first tool = %#v", first)
	}
	if raw, ok := first.Function.Parameters.(json.RawMessage); !ok || string(raw) != string(schema) {
		t.Fatalf("first tool parameters = %#v", first.Function.Parameters)
	}
	if opts.Tools[1].Function.Parameters != nil {
		t.Fatalf("schemaless tool parameters = %#v", opts.Tools[1].Function.Parameters)
	}
	if opts.ToolChoice != "required" {
		t.Fatalf("tool choice = %#v, want %q", opts.ToolChoice, "required")
	}
	if opts.MaxTokens != 512 {
		t.Fatalf("max tokens = %d, want 512", opts.MaxTokens)
	}
	if opts.Model != "worker-mini" {
		t.Fatalf("model override = %q, want %q", opts.Model, "worker-mini")
	}
	if opts.Temperature != 0 {
		t.Fatalf("temperature = %v, want 0", opts.Temperature)
	}
}

func TestChatWithoutForceToolLeavesChoiceUnset(t *testing.T) {
	t.Parallel()
	fake := &scriptedChatModel{response: textResponse("ok")}
	client := NewChatClient(fake, "", "", 0)
	request := explore.ChatRequest{
		Messages: []explore.Message{{Role: explore.RoleUser, Text: "look"}},
		Tools:    []explore.ToolSpec{{Name: "tap", Description: "tap"}},
	}
	if _, err := client.Chat(context.Background(), request); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if fake.lastOptions.ToolChoice != nil {
		t.Fatalf("tool choice = %#v, want nil", fake.lastOptions.ToolChoice)
	}
	if fake.lastOptions.Model != "" {
		t.Fatalf("model override = %q, want blank", fake.lastOptions.Model)
	}
}

func TestChatForceToolWithoutToolsFails(t *testing.T) {
	t.Parallel()
	fake := &scriptedChatModel{response: textResponse("ok")}
	client := NewChatClient(fake, "", "", 0)
	request := explore.ChatRequest{
		Messages:  []explore.Message{{Role: explore.RoleUser, Text: "act"}},
		ForceTool: true,
	}
	if _, err := client.Chat(context.Background(), request); err == nil {
		t.Fatal("want error for ForceTool without tools")
	}
	if fake.calls != 0 {
		t.Fatalf("model was called %d times", fake.calls)
	}
}

func TestChatExtractsReply(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		response  *llms.ContentResponse
		wantText  string
		wantCalls []explore.ToolCall
		wantUsage explore.Usage
	}{
		{
			name:     "text only",
			response: textResponse("all good"),
			wantText: "all good",
		},
		{
			name: "tool calls with openai usage keys",
			response: &llms.ContentResponse{Choices: []*llms.ContentChoice{{
				ToolCalls: []llms.ToolCall{
					{ID: "c1", Type: "function", FunctionCall: &llms.FunctionCall{Name: "tap", Arguments: `{"index":1}`}},
					{ID: "c2", Type: "function", FunctionCall: &llms.FunctionCall{Name: "stop", Arguments: ""}},
				},
				GenerationInfo: map[string]any{"PromptTokens": 120, "CompletionTokens": 15},
			}}},
			wantCalls: []explore.ToolCall{
				{ID: "c1", Name: "tap", Arguments: json.RawMessage(`{"index":1}`)},
				{ID: "c2", Name: "stop"},
			},
			wantUsage: explore.Usage{InputTokens: 120, OutputTokens: 15},
		},
		{
			name: "split choices with repeated usage totals",
			response: &llms.ContentResponse{Choices: []*llms.ContentChoice{
				{
					Content:        "I will tap it",
					GenerationInfo: map[string]any{"InputTokens": 200, "OutputTokens": 40},
				},
				{
					ToolCalls: []llms.ToolCall{
						{ID: "c3", FunctionCall: &llms.FunctionCall{Name: "tap", Arguments: `{}`}},
					},
					GenerationInfo: map[string]any{"InputTokens": 200, "OutputTokens": 40},
				},
			}},
			wantText: "I will tap it",
			wantCalls: []explore.ToolCall{
				{ID: "c3", Name: "tap", Arguments: json.RawMessage(`{}`)},
			},
			wantUsage: explore.Usage{InputTokens: 200, OutputTokens: 40},
		},
		{
			name: "usage absent means zero",
			response: &llms.ContentResponse{Choices: []*llms.ContentChoice{{
				Content:        "done",
				GenerationInfo: map[string]any{"SomethingElse": "x"},
			}}},
			wantText: "done",
		},
		{
			name: "float usage values",
			response: &llms.ContentResponse{Choices: []*llms.ContentChoice{{
				Content:        "done",
				GenerationInfo: map[string]any{"PromptTokens": float64(7), "CompletionTokens": float64(3)},
			}}},
			wantText:  "done",
			wantUsage: explore.Usage{InputTokens: 7, OutputTokens: 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := NewChatClient(&scriptedChatModel{response: tc.response}, "", "", 0)
			got, err := client.Chat(context.Background(), explore.ChatRequest{
				Messages: []explore.Message{{Role: explore.RoleUser, Text: "go"}},
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if got.Message.Role != explore.RoleAssistant {
				t.Fatalf("reply role = %q", got.Message.Role)
			}
			if got.Message.Text != tc.wantText {
				t.Fatalf("reply text = %q, want %q", got.Message.Text, tc.wantText)
			}
			if len(got.Message.ToolCalls) != len(tc.wantCalls) {
				t.Fatalf("tool calls = %#v, want %#v", got.Message.ToolCalls, tc.wantCalls)
			}
			for i, want := range tc.wantCalls {
				call := got.Message.ToolCalls[i]
				if call.ID != want.ID || call.Name != want.Name || string(call.Arguments) != string(want.Arguments) {
					t.Fatalf("tool call %d = %#v, want %#v", i, call, want)
				}
			}
			if got.Usage != tc.wantUsage {
				t.Fatalf("usage = %#v, want %#v", got.Usage, tc.wantUsage)
			}
		})
	}
}

func TestChatRejectsUnusableReplies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		response *llms.ContentResponse
		err      error
	}{
		{"model error", nil, errors.New("boom")},
		{"nil response", nil, nil},
		{"no choices", &llms.ContentResponse{}, nil},
		{"empty reply", textResponse(""), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			client := NewChatClient(&scriptedChatModel{response: tc.response, err: tc.err}, "", "", 0)
			_, err := client.Chat(context.Background(), explore.ChatRequest{
				Messages: []explore.Message{{Role: explore.RoleUser, Text: "go"}},
			})
			if err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestChatAppliesTimeout(t *testing.T) {
	t.Parallel()
	client := NewChatClient(&scriptedChatModel{waitForContext: true}, "", "", 20*time.Millisecond)
	start := time.Now()
	_, err := client.Chat(context.Background(), explore.ChatRequest{
		Messages: []explore.Message{{Role: explore.RoleUser, Text: "go"}},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("timeout took %s", elapsed)
	}
}

func TestChatWithoutModelFails(t *testing.T) {
	t.Parallel()
	client := NewChatClient(nil, "", "", 0)
	if _, err := client.Chat(context.Background(), explore.ChatRequest{
		Messages: []explore.Message{{Role: explore.RoleUser, Text: "go"}},
	}); err == nil {
		t.Fatal("want error for missing model")
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestChatModelsFromEnv(t *testing.T) {
	t.Parallel()

	t.Run("no key fails closed", func(t *testing.T) {
		t.Parallel()
		set, err := ChatModelsFromEnv(mapGetenv(nil))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if set != (explore.ModelSet{}) {
			t.Fatalf("set = %#v, want zero", set)
		}
	})

	t.Run("single model serves every tier", func(t *testing.T) {
		t.Parallel()
		set, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"OPENAI_API_KEY": "sk-test",
		}))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if set.Manager == nil || set.Worker == nil || set.Vision == nil {
			t.Fatalf("set = %#v", set)
		}
		if set.Worker != set.Manager || set.Vision != set.Manager {
			t.Fatal("tiers without overrides must share the manager client")
		}
		if name := set.Manager.(*ChatClient).modelName; name != "" {
			t.Fatalf("manager per-call override = %q, want blank", name)
		}
	})

	t.Run("tier overrides make distinct clients", func(t *testing.T) {
		t.Parallel()
		set, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"OPENAI_API_KEY":            "sk-test",
			"FLOWBATON_AI_MODEL":        "boss-model",
			"FLOWBATON_AI_WORKER_MODEL": "worker-mini",
			"FLOWBATON_AI_VISION_MODEL": "vision-mid",
		}))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if set.Worker == set.Manager || set.Vision == set.Manager {
			t.Fatal("overridden tiers must not share the manager client")
		}
		if name := set.Worker.(*ChatClient).modelName; name != "worker-mini" {
			t.Fatalf("worker override = %q", name)
		}
		if name := set.Vision.(*ChatClient).modelName; name != "vision-mid" {
			t.Fatalf("vision override = %q", name)
		}
		worker := set.Worker.(*ChatClient)
		manager := set.Manager.(*ChatClient)
		if worker.model != manager.model {
			t.Fatal("tiers must share one provider client")
		}
	})

	t.Run("anthropic provider selection", func(t *testing.T) {
		t.Parallel()
		set, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"FLOWBATON_AI_PROVIDER": "anthropic",
			"ANTHROPIC_API_KEY":     "sk-ant-test",
		}))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if set.Manager == nil {
			t.Fatal("manager missing")
		}
	})

	t.Run("wrong provider key stays closed", func(t *testing.T) {
		t.Parallel()
		// An anthropic selection ignores an OpenAI key: still unconfigured.
		set, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"FLOWBATON_AI_PROVIDER": "anthropic",
			"OPENAI_API_KEY":        "sk-test",
		}))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if set != (explore.ModelSet{}) {
			t.Fatalf("set = %#v, want zero", set)
		}
	})

	t.Run("unknown provider errors", func(t *testing.T) {
		t.Parallel()
		if _, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"FLOWBATON_AI_PROVIDER": "quantum",
		})); err == nil {
			t.Fatal("want error for unknown provider")
		}
	})

	t.Run("invalid timeout errors", func(t *testing.T) {
		t.Parallel()
		if _, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"OPENAI_API_KEY":       "sk-test",
			"FLOWBATON_AI_TIMEOUT": "10h",
		})); err == nil {
			t.Fatal("want error for out-of-range timeout")
		}
	})

	t.Run("timeout carries into the clients", func(t *testing.T) {
		t.Parallel()
		set, err := ChatModelsFromEnv(mapGetenv(map[string]string{
			"OPENAI_API_KEY":       "sk-test",
			"FLOWBATON_AI_TIMEOUT": "90s",
		}))
		if err != nil {
			t.Fatalf("ChatModelsFromEnv: %v", err)
		}
		if timeout := set.Manager.(*ChatClient).timeout; timeout != 90*time.Second {
			t.Fatalf("timeout = %s, want 90s", timeout)
		}
	})
}
