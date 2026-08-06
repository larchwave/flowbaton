// Package aiengine implements engine.AIPredictionEngine on top of langchaingo's
// provider-agnostic llms.Model. It lives outside internal/engine on purpose: the
// engine core stays stdlib-first (project CLAUDE.md hard rule 6), so the
// langchaingo dependency is confined to this peripheral package and wired in at
// the CLI composition layer.
package aiengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tmc/langchaingo/llms"

	"github.com/larchwave/flowbaton/internal/engine"
)

// Engine is a screenshot-driven AIPredictionEngine backed by one llms.Model.
// The model is injectable so tests drive the prompt/parse logic with a fake and
// no network. A per-call model name override is optional; blank uses whatever
// model the provider was constructed with.
type Engine struct {
	model     llms.Model
	modelName string
}

// NewFromModel wraps an already-constructed langchaingo model. modelName is an
// optional per-call override (e.g. "gpt-4o"); pass "" to use the model's own
// default. Used directly by tests; production goes through New/FromEnv.
func NewFromModel(model llms.Model, modelName string) *Engine {
	return &Engine{model: model, modelName: modelName}
}

// Compile-time proof this satisfies the engine boundary.
var _ engine.AIPredictionEngine = (*Engine)(nil)

// PerformAssertion asks whether assertion holds for the screenshot.
func (e *Engine) PerformAssertion(ctx context.Context, screenshotPNG []byte, assertion string) (engine.AIResult, error) {
	prompt := "You are inspecting a single screenshot from a mobile UI test. Decide whether this " +
		"assertion about the screen is true: \"" + assertion + "\". " +
		`Reply ONLY with a JSON object: {"pass": <true|false>, "reasoning": "<short explanation>"}.`
	var out struct {
		Pass      bool   `json:"pass"`
		Reasoning string `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	return engine.AIResult{Pass: out.Pass, Reasoning: out.Reasoning}, nil
}

// FindDefects lists user-visible defects on the screenshot. Pass is true when
// none are found, matching assertNoDefectsWithAI's "no defects" success.
func (e *Engine) FindDefects(ctx context.Context, screenshotPNG []byte) (engine.AIResult, error) {
	prompt := "Inspect this mobile app screenshot for user-visible defects: broken layout, error " +
		"dialogs, placeholder or lorem-ipsum text, overlapping or cut-off content, and missing images. " +
		`Reply ONLY with a JSON object: {"defects": ["<defect>", ...], "reasoning": "<short summary>"}. ` +
		"Use an empty array when there are none."
	var out struct {
		Defects   []string `json:"defects"`
		Reasoning string   `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	return engine.AIResult{Pass: len(out.Defects) == 0, Reasoning: out.Reasoning, Defects: out.Defects}, nil
}

// ExtractText pulls the text answering query out of the screenshot.
func (e *Engine) ExtractText(ctx context.Context, screenshotPNG []byte, query string) (engine.AIResult, error) {
	prompt := "Extract the following information from this mobile app screenshot: \"" + query + "\". " +
		`Reply ONLY with a JSON object: {"text": "<the extracted text, or empty string if not present>", ` +
		`"reasoning": "<short note>"}.`
	var out struct {
		Text      string `json:"text"`
		Reasoning string `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	return engine.AIResult{Text: out.Text, Reasoning: out.Reasoning}, nil
}

// generateJSON runs one multimodal completion and decodes its JSON object into
// dst. Temperature is pinned to 0 for the most deterministic answer a model can
// give; the reply is instructed to be a bare JSON object but real models still
// wrap it in prose or fences, so the first {...} span is what gets decoded.
func (e *Engine) generateJSON(ctx context.Context, prompt string, screenshotPNG []byte, dst any) error {
	if len(screenshotPNG) == 0 {
		return errors.New("aiengine: a screenshot is required")
	}
	message := llms.MessageContent{
		Role: llms.ChatMessageTypeHuman,
		Parts: []llms.ContentPart{
			llms.TextPart(prompt),
			llms.BinaryPart("image/png", screenshotPNG),
		},
	}
	options := []llms.CallOption{llms.WithTemperature(0)}
	if e.modelName != "" {
		options = append(options, llms.WithModel(e.modelName))
	}
	response, err := e.model.GenerateContent(ctx, []llms.MessageContent{message}, options...)
	if err != nil {
		return fmt.Errorf("aiengine: model call failed: %w", err)
	}
	if response == nil || len(response.Choices) == 0 {
		return errors.New("aiengine: model returned no choices")
	}
	object, ok := firstJSONObject(response.Choices[0].Content)
	if !ok {
		return fmt.Errorf("aiengine: model reply had no JSON object: %q", truncate(response.Choices[0].Content, 200))
	}
	if err := json.Unmarshal([]byte(object), dst); err != nil {
		return fmt.Errorf("aiengine: decoding model reply: %w", err)
	}
	return nil
}

// firstJSONObject returns the first '{' … last '}' span of s. This peels away
// markdown fences and any prose a model adds around the object it was asked for.
// ponytail: brace-span heuristic, not a streaming JSON scanner — fine for a
// temperature-0 reply instructed to be a single object; upgrade to a real
// tokenizer only if models start emitting multiple/nested stray braces.
func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end <= start {
		return "", false
	}
	return s[start : end+1], true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
