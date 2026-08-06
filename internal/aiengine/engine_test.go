package aiengine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"

	"github.com/larchwave/flowbaton/internal/engine"
)

// fakeModel is a scripted llms.Model: it records the messages/options it was
// called with and returns a canned reply (or error), so the prompt-build and
// JSON-parse logic is exercised with no network.
type fakeModel struct {
	reply    string
	err      error
	response *llms.ContentResponse // when set, overrides reply

	lastMessages []llms.MessageContent
	callCount    int
}

func (f *fakeModel) GenerateContent(_ context.Context, messages []llms.MessageContent, _ ...llms.CallOption) (*llms.ContentResponse, error) {
	f.callCount++
	f.lastMessages = messages
	if f.err != nil {
		return nil, f.err
	}
	if f.response != nil {
		return f.response, nil
	}
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{{Content: f.reply}}}, nil
}

func (f *fakeModel) Call(context.Context, string, ...llms.CallOption) (string, error) {
	return "", errors.New("unused")
}

var pngFixture = []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01}

func TestPerformAssertion(t *testing.T) {
	t.Parallel()
	fake := &fakeModel{reply: `{"pass": true, "reasoning": "the login button is centered"}`}
	e := NewFromModel(fake, "gpt-4o")

	got, err := e.PerformAssertion(context.Background(), pngFixture, "login button is visible")
	if err != nil {
		t.Fatalf("PerformAssertion: %v", err)
	}
	if !got.Pass || got.Reasoning != "the login button is centered" {
		t.Fatalf("result = %#v", got)
	}
	// The screenshot must ride along as a PNG binary part next to the prompt.
	assertScreenshotAndPromptSent(t, fake, "login button is visible")

	// A false verdict maps straight through.
	fake.reply = `{"pass": false, "reasoning": "no such button"}`
	got, err = e.PerformAssertion(context.Background(), pngFixture, "x")
	if err != nil || got.Pass {
		t.Fatalf("false verdict = %#v, %v", got, err)
	}
}

func TestFindDefects(t *testing.T) {
	t.Parallel()
	e := NewFromModel(&fakeModel{reply: `{"defects": ["overlapping text", "cut-off image"], "reasoning": "two issues"}`}, "")
	got, err := e.FindDefects(context.Background(), pngFixture)
	if err != nil {
		t.Fatalf("FindDefects: %v", err)
	}
	if got.Pass {
		t.Fatal("defects present but Pass=true")
	}
	if len(got.Defects) != 2 || got.Defects[0] != "overlapping text" {
		t.Fatalf("defects = %#v", got.Defects)
	}

	// Empty defect list is the success shape (Pass=true).
	clean := NewFromModel(&fakeModel{reply: `{"defects": [], "reasoning": "looks fine"}`}, "")
	got, err = clean.FindDefects(context.Background(), pngFixture)
	if err != nil || !got.Pass || len(got.Defects) != 0 {
		t.Fatalf("clean = %#v, %v", got, err)
	}
}

func TestExtractText(t *testing.T) {
	t.Parallel()
	// Reply wrapped in a markdown fence + prose — the brace-span extractor must
	// still find the object.
	fenced := "Here you go:\n```json\n{\"text\": \"$42.00\", \"reasoning\": \"top-right balance\"}\n```\n"
	e := NewFromModel(&fakeModel{reply: fenced}, "")
	got, err := e.ExtractText(context.Background(), pngFixture, "the balance")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if got.Text != "$42.00" || got.Reasoning != "top-right balance" {
		t.Fatalf("result = %#v", got)
	}
}

func TestGenerateJSONErrors(t *testing.T) {
	t.Parallel()

	t.Run("empty screenshot rejected before the model call", func(t *testing.T) {
		fake := &fakeModel{reply: `{"pass": true}`}
		if _, err := NewFromModel(fake, "").PerformAssertion(context.Background(), nil, "x"); err == nil {
			t.Fatal("want error for empty screenshot")
		}
		if fake.callCount != 0 {
			t.Fatal("model was called despite an empty screenshot")
		}
	})

	t.Run("model error propagates", func(t *testing.T) {
		e := NewFromModel(&fakeModel{err: errors.New("429 rate limited")}, "")
		if _, err := e.FindDefects(context.Background(), pngFixture); err == nil || !strings.Contains(err.Error(), "429") {
			t.Fatalf("want wrapped model error, got %v", err)
		}
	})

	t.Run("no choices is an error", func(t *testing.T) {
		e := NewFromModel(&fakeModel{response: &llms.ContentResponse{}}, "")
		if _, err := e.ExtractText(context.Background(), pngFixture, "x"); err == nil {
			t.Fatal("want error for zero choices")
		}
	})

	t.Run("reply with no JSON object is an error", func(t *testing.T) {
		e := NewFromModel(&fakeModel{reply: "I cannot help with that."}, "")
		if _, err := e.PerformAssertion(context.Background(), pngFixture, "x"); err == nil {
			t.Fatal("want error when reply carries no JSON")
		}
	})

	t.Run("malformed JSON is an error", func(t *testing.T) {
		e := NewFromModel(&fakeModel{reply: `{"pass": tru`}, "")
		if _, err := e.PerformAssertion(context.Background(), pngFixture, "x"); err == nil {
			t.Fatal("want decode error for malformed JSON")
		}
	})
}

func assertScreenshotAndPromptSent(t *testing.T, fake *fakeModel, wantPromptSubstr string) {
	t.Helper()
	if len(fake.lastMessages) != 1 {
		t.Fatalf("want 1 message, got %d", len(fake.lastMessages))
	}
	parts := fake.lastMessages[0].Parts
	if len(parts) != 2 {
		t.Fatalf("want text+image parts, got %d", len(parts))
	}
	text, ok := parts[0].(llms.TextContent)
	if !ok || !strings.Contains(text.Text, wantPromptSubstr) {
		t.Fatalf("prompt part = %#v, want substring %q", parts[0], wantPromptSubstr)
	}
	bin, ok := parts[1].(llms.BinaryContent)
	if !ok || bin.MIMEType != "image/png" || len(bin.Data) == 0 {
		t.Fatalf("image part = %#v, want non-empty image/png", parts[1])
	}
}

func TestFromEnv(t *testing.T) {
	t.Parallel()

	t.Run("unconfigured returns a nil engine, no error", func(t *testing.T) {
		got, err := FromEnv(func(string) string { return "" })
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got != nil {
			t.Fatalf("want nil interface, got %#v", got)
		}
	})

	t.Run("openai key present constructs an engine", func(t *testing.T) {
		env := map[string]string{"OPENAI_API_KEY": "sk-test"}
		got, err := FromEnv(func(k string) string { return env[k] })
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got == nil {
			t.Fatal("want a constructed engine")
		}
		var _ engine.AIPredictionEngine = got
	})

	t.Run("explicit anthropic provider uses its own key var", func(t *testing.T) {
		env := map[string]string{"FLOWBATON_AI_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-ant"}
		got, err := FromEnv(func(k string) string { return env[k] })
		if err != nil || got == nil {
			t.Fatalf("anthropic FromEnv = %#v, %v", got, err)
		}
		// An OpenAI key alone must NOT enable the anthropic-selected engine.
		env2 := map[string]string{"FLOWBATON_AI_PROVIDER": "anthropic", "OPENAI_API_KEY": "sk-test"}
		got2, err := FromEnv(func(k string) string { return env2[k] })
		if err != nil || got2 != nil {
			t.Fatalf("wrong-key = %#v, %v; want nil engine", got2, err)
		}
	})

	t.Run("unknown provider is an error", func(t *testing.T) {
		env := map[string]string{"FLOWBATON_AI_PROVIDER": "gemini"}
		if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
			t.Fatal("want error for unknown provider")
		}
	})
}
