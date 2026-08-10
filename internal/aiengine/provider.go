package aiengine

import (
	"fmt"
	"strings"
	"time"

	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/openai"

	"github.com/larchwave/flowbaton/internal/engine"
)

// Provider names the LLM backend an Engine talks to.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"
	ProviderAnthropic Provider = "anthropic"

	// Default vision-capable models. The provider libraries default to
	// text-only models (e.g. gpt-3.5), which cannot read a screenshot, so an
	// unset model must resolve to a multimodal one here.
	defaultOpenAIModel    = "gpt-4o"
	defaultAnthropicModel = "claude-3-5-sonnet-latest"
)

// Config selects and authenticates a provider. APIKey is optional: when blank,
// the provider library reads its own standard env var (OPENAI_API_KEY /
// ANTHROPIC_API_KEY). Model is optional and falls back to a vision-capable
// default for the chosen provider.
type Config struct {
	Provider Provider
	APIKey   string
	Model    string
	// Timeout bounds each provider call. Zero uses DefaultProviderTimeout.
	// Values above MaxProviderTimeout are rejected.
	Timeout time.Duration
	// BaseURL points the provider at an endpoint other than the vendor's own.
	// Blank keeps the vendor default. This is what makes an OpenAI- or
	// Anthropic-COMPATIBLE endpoint reachable — a self-hosted gateway, a proxy,
	// or another vendor speaking the same protocol, such as MiniMax at
	// https://api.minimax.io/anthropic. The protocol is the same; only the host
	// differs, so nothing else here has to know.
	//
	// Written either way round: with or without the trailing version segment.
	// See normalizeBaseURL for why both have to work.
	BaseURL string
}

// normalizeBaseURL appends the version segment the provider clients expect.
//
// langchaingo takes a base URL that already carries it — its defaults are
// https://api.openai.com/v1 and https://api.anthropic.com/v1 — and appends
// "/chat/completions" or "/messages" to that. Vendors document the other half:
// MiniMax publishes https://api.minimax.io/anthropic and Anthropic's own
// ANTHROPIC_BASE_URL is the host root, because their SDKs add /v1 themselves.
//
// Add /v1 before langchaingo appends /messages so a documented MiniMax
// Anthropic endpoint resolves to /anthropic/v1/messages. This prevents an
// optional AI command from masking a path error as a warning.
//
// An authored URL whose last segment already looks like a version is left
// alone, so a gateway pinned at /v2 keeps working.
func normalizeBaseURL(authored string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(authored), "/")
	if trimmed == "" {
		return ""
	}
	if last := trimmed[strings.LastIndexByte(trimmed, '/')+1:]; isVersionSegment(last) {
		return trimmed
	}
	return trimmed + "/v1"
}

// isVersionSegment reports whether a path segment is a "v" followed by digits.
func isVersionSegment(segment string) bool {
	if len(segment) < 2 || segment[0] != 'v' {
		return false
	}
	for _, r := range segment[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// New builds an Engine for cfg. It fails loudly on an unknown provider or a
// provider the library refuses to construct (e.g. no API key anywhere) — a
// misconfiguration should surface, not silently fall back to fail-closed.
func New(cfg Config) (*Engine, error) {
	timeout, err := providerTimeout(cfg.Timeout)
	if err != nil {
		return nil, err
	}
	model := cfg.Model
	switch cfg.Provider {
	case ProviderOpenAI:
		if model == "" {
			model = defaultOpenAIModel
		}
		options := []openai.Option{openai.WithModel(model)}
		if cfg.APIKey != "" {
			options = append(options, openai.WithToken(cfg.APIKey))
		}
		if base := normalizeBaseURL(cfg.BaseURL); base != "" {
			options = append(options, openai.WithBaseURL(base))
		}
		llm, err := openai.New(options...)
		if err != nil {
			return nil, fmt.Errorf("aiengine: openai: %w", err)
		}
		return &Engine{model: llm, timeout: timeout}, nil
	case ProviderAnthropic:
		if model == "" {
			model = defaultAnthropicModel
		}
		options := []anthropic.Option{anthropic.WithModel(model)}
		if cfg.APIKey != "" {
			options = append(options, anthropic.WithToken(cfg.APIKey))
		}
		if base := normalizeBaseURL(cfg.BaseURL); base != "" {
			options = append(options, anthropic.WithBaseURL(base))
		}
		llm, err := anthropic.New(options...)
		if err != nil {
			return nil, fmt.Errorf("aiengine: anthropic: %w", err)
		}
		return &Engine{model: llm, timeout: timeout}, nil
	default:
		return nil, fmt.Errorf("aiengine: unknown provider %q (want %q or %q)", cfg.Provider, ProviderOpenAI, ProviderAnthropic)
	}
}

func providerTimeout(configured time.Duration) (time.Duration, error) {
	if configured == 0 {
		return DefaultProviderTimeout, nil
	}
	if configured < 0 || configured > MaxProviderTimeout {
		return 0, fmt.Errorf("aiengine: provider timeout must be between 1ns and %s", MaxProviderTimeout)
	}
	return configured, nil
}

// FromEnv builds an Engine from environment configuration, or returns a nil
// engine (untyped) when no AI is configured — leaving AI commands to fail closed
// with ErrCloudAPIKeyNotAvailable, the same as product runtime FlowBaton without a
// provider.
//
// Config is read through getenv (injected for tests):
//   - FLOWBATON_AI_PROVIDER: "openai" (default) or "anthropic"
//   - the provider's own key var: OPENAI_API_KEY / ANTHROPIC_API_KEY
//   - FLOWBATON_AI_MODEL: optional model override
//   - FLOWBATON_AI_BASE_URL: optional endpoint override, for an
//     OpenAI/Anthropic-compatible host that is not the vendor's own
//   - FLOWBATON_AI_TIMEOUT: optional Go duration, default 60s, maximum 5m
//
// "Configured" means the selected provider's key var is non-blank. With no key,
// the return is (nil, nil): unconfigured is not an error, it is fail-closed.
func FromEnv(getenv func(string) string) (engine.AIPredictionEngine, error) {
	provider := Provider(strings.ToLower(strings.TrimSpace(getenv("FLOWBATON_AI_PROVIDER"))))
	if provider == "" {
		provider = ProviderOpenAI
	}

	var keyVar string
	switch provider {
	case ProviderOpenAI:
		keyVar = "OPENAI_API_KEY"
	case ProviderAnthropic:
		keyVar = "ANTHROPIC_API_KEY"
	default:
		return nil, fmt.Errorf("aiengine: unknown FLOWBATON_AI_PROVIDER %q", provider)
	}

	apiKey := strings.TrimSpace(getenv(keyVar))
	if apiKey == "" {
		// Nothing configured: fail-closed, not an error.
		return nil, nil
	}

	var timeout time.Duration
	if authored := strings.TrimSpace(getenv("FLOWBATON_AI_TIMEOUT")); authored != "" {
		parsed, err := time.ParseDuration(authored)
		if err != nil {
			return nil, fmt.Errorf("aiengine: invalid FLOWBATON_AI_TIMEOUT %q: %w", authored, err)
		}
		timeout = parsed
		if _, err := providerTimeout(timeout); err != nil {
			return nil, fmt.Errorf("aiengine: invalid FLOWBATON_AI_TIMEOUT %q: %w", authored, err)
		}
	}

	e, err := New(Config{
		Provider: provider,
		APIKey:   apiKey,
		Model:    strings.TrimSpace(getenv("FLOWBATON_AI_MODEL")),
		BaseURL:  strings.TrimSpace(getenv("FLOWBATON_AI_BASE_URL")),
		Timeout:  timeout,
	})
	if err != nil {
		return nil, err
	}
	return e, nil
}
