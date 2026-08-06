package aiengine

import (
	"strings"
	"testing"
)

// The provider could only ever reach the vendor's own host, which rules out
// every OpenAI- and Anthropic-compatible endpoint that is not one: a self-hosted
// gateway, a proxy, or another vendor speaking the same protocol. MiniMax is the
// case that forced it — its Anthropic-compatible API lives at
// https://api.minimax.io/anthropic and is reached with the same SDK and the same
// request shape, differing only in the host.
//
// One variable, FLOWBATON_AI_BASE_URL, alongside the provider and model that
// were already there. Unset means the vendor's default, so nothing changes for
// anyone not asking for it.

func envFrom(pairs map[string]string) func(string) string {
	return func(name string) string { return pairs[name] }
}

func TestFromEnvCarriesTheBaseURL(t *testing.T) {
	t.Parallel()

	engine, err := FromEnv(envFrom(map[string]string{
		"FLOWBATON_AI_PROVIDER": "anthropic",
		"ANTHROPIC_API_KEY":     "test-key",
		"FLOWBATON_AI_MODEL":    "MiniMax-M3",
		"FLOWBATON_AI_BASE_URL": "https://api.minimax.io/anthropic",
	}))
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if engine == nil {
		t.Fatal("FromEnv() returned no engine with a key configured")
	}
}

// A base URL with no key is still fail-closed: pointing at a host does not
// configure an AI, and a nil engine is what makes the AI commands refuse the
// way product runtime FlowBaton does without a provider.
func TestABaseURLAloneIsStillUnconfigured(t *testing.T) {
	t.Parallel()

	engine, err := FromEnv(envFrom(map[string]string{
		"FLOWBATON_AI_PROVIDER": "anthropic",
		"FLOWBATON_AI_BASE_URL": "https://api.minimax.io/anthropic",
	}))
	if err != nil {
		t.Fatalf("FromEnv() error = %v", err)
	}
	if engine != nil {
		t.Fatal("a base URL without a key configured an engine")
	}
}

// The same variable has to reach the OpenAI half, or half the compatible
// endpoints in the world stay unreachable for no stated reason.
func TestTheBaseURLReachesBothProviders(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"openai", "anthropic"} {
		keyVar := "OPENAI_API_KEY"
		if provider == "anthropic" {
			keyVar = "ANTHROPIC_API_KEY"
		}
		engine, err := New(Config{
			Provider: Provider(provider),
			APIKey:   "test-key",
			BaseURL:  "https://example.invalid/v1",
		})
		if err != nil {
			t.Fatalf("New(%s with a base URL) error = %v (key var %s)", provider, err, keyVar)
		}
		if engine == nil {
			t.Fatalf("New(%s) returned no engine", provider)
		}
	}
}

// An unknown provider still names both it and the alternatives, because a
// typo in FLOWBATON_AI_PROVIDER is the likeliest way to reach this.
func TestAnUnknownProviderSaysWhatIsAvailable(t *testing.T) {
	t.Parallel()

	_, err := New(Config{Provider: Provider("minimax"), APIKey: "test-key"})
	if err == nil {
		t.Fatal("New(unknown provider) succeeded")
	}
	for _, fragment := range []string{"minimax", "openai", "anthropic"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to name %s", err.Error(), fragment)
		}
	}
}
