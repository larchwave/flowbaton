package aiengine

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"

	"github.com/larchwave/flowbaton/internal/explore"
)

// OpenAI's API rejects langchaingo's BinaryContent ("binary" part, 400), and
// Anthropic's adapter reads images only from BinaryContent — the image part
// shape must follow the provider. Proven live against api.openai.com on
// 2026-08-11; these pin both dialects.

func wantDataURI(t *testing.T, part llms.ContentPart) {
	t.Helper()
	image, ok := part.(llms.ImageURLContent)
	if !ok {
		t.Fatalf("image part = %#v, want ImageURLContent", part)
	}
	prefix := "data:image/png;base64,"
	if !strings.HasPrefix(image.URL, prefix) {
		t.Fatalf("image URL = %q, want %q prefix", image.URL, prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(image.URL, prefix))
	if err != nil || string(decoded) != string(pngFixture) {
		t.Fatalf("image URL payload = %q (%v), want the PNG bytes", decoded, err)
	}
}

func TestChatEncodesImagesForTheProviderDialect(t *testing.T) {
	t.Parallel()

	imageSentBy := func(provider Provider) llms.ContentPart {
		fake := &scriptedChatModel{response: textResponse("ok")}
		client := NewChatClient(fake, provider, "", 0)
		request := explore.ChatRequest{Messages: []explore.Message{
			{Role: explore.RoleUser, Text: "look", ImagePNG: pngFixture},
		}}
		if _, err := client.Chat(context.Background(), request); err != nil {
			t.Fatalf("Chat: %v", err)
		}
		return fake.lastMessages[0].Parts[1]
	}

	wantDataURI(t, imageSentBy(ProviderOpenAI))
	if _, ok := imageSentBy(ProviderAnthropic).(llms.BinaryContent); !ok {
		t.Fatalf("anthropic image part is not BinaryContent")
	}
}

// The dialect only holds if construction carries the provider through: a
// New() or ChatModelsFromEnv that dropped it would pass the direct-literal
// tests above while the live OpenAI 400 came back.
func TestConstructionCarriesTheProviderToEveryClient(t *testing.T) {
	t.Parallel()

	engine, err := New(Config{Provider: ProviderOpenAI, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if engine.provider != ProviderOpenAI {
		t.Fatalf("engine provider = %q, want %q", engine.provider, ProviderOpenAI)
	}

	set, err := ChatModelsFromEnv(func(key string) string {
		return map[string]string{
			"FLOWBATON_AI_PROVIDER":     "openai",
			"OPENAI_API_KEY":            "test-key",
			"FLOWBATON_AI_WORKER_MODEL": "worker-mini",
			"FLOWBATON_AI_VISION_MODEL": "vision-max",
		}[key]
	})
	if err != nil {
		t.Fatalf("ChatModelsFromEnv: %v", err)
	}
	for name, llm := range map[string]explore.LLM{
		"manager": set.Manager, "worker": set.Worker, "vision": set.Vision,
	} {
		client, ok := llm.(*ChatClient)
		if !ok {
			t.Fatalf("%s tier is %T, want *ChatClient", name, llm)
		}
		if client.provider != ProviderOpenAI {
			t.Fatalf("%s tier provider = %q, want %q", name, client.provider, ProviderOpenAI)
		}
	}
}

func TestEngineEncodesImagesForTheProviderDialect(t *testing.T) {
	t.Parallel()

	fake := &fakeModel{reply: `{"text":"ok","reasoning":"fine"}`}
	e := &Engine{model: fake, provider: ProviderOpenAI, timeout: DefaultProviderTimeout}
	if _, err := e.ExtractText(context.Background(), pngFixture, "x"); err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	wantDataURI(t, fake.lastMessages[0].Parts[1])
}
