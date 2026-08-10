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
	"io"
	"strings"
	"time"

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
	timeout   time.Duration
}

const (
	DefaultProviderTimeout = 60 * time.Second
	MaxProviderTimeout     = 5 * time.Minute
)

// NewFromModel wraps an already-constructed langchaingo model. modelName is an
// optional per-call override (e.g. "gpt-4o"); pass "" to use the model's own
// default. Used directly by tests; production goes through New/FromEnv.
func NewFromModel(model llms.Model, modelName string) *Engine {
	return &Engine{model: model, modelName: modelName, timeout: DefaultProviderTimeout}
}

// Compile-time proof this satisfies the engine boundary.
var _ engine.AIPredictionEngine = (*Engine)(nil)

// PerformAssertion asks whether assertion holds for the screenshot.
func (e *Engine) PerformAssertion(ctx context.Context, screenshotPNG []byte, assertion string) (engine.AIResult, error) {
	prompt := "You are inspecting a single screenshot from a mobile UI test. Decide whether this " +
		"assertion about the screen is true: \"" + assertion + "\". " +
		`Reply ONLY with a JSON object: {"pass": <true|false>, "reasoning": "<short explanation>"}.`
	var out struct {
		Pass      *bool   `json:"pass"`
		Reasoning *string `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	if out.Pass == nil || out.Reasoning == nil {
		return engine.AIResult{}, errors.New("aiengine: model reply requires non-null pass and reasoning fields")
	}
	return engine.AIResult{Pass: *out.Pass, Reasoning: *out.Reasoning}, nil
}

// FindDefects lists user-visible defects on the screenshot. Pass is true when
// none are found, matching assertNoDefectsWithAI's "no defects" success.
func (e *Engine) FindDefects(ctx context.Context, screenshotPNG []byte) (engine.AIResult, error) {
	prompt := "Inspect this mobile app screenshot for user-visible defects: broken layout, error " +
		"dialogs, placeholder or lorem-ipsum text, overlapping or cut-off content, and missing images. " +
		`Reply ONLY with a JSON object: {"defects": ["<defect>", ...], "reasoning": "<short summary>"}. ` +
		"Use an empty array when there are none."
	var out struct {
		Defects   *[]string `json:"defects"`
		Reasoning *string   `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	if out.Defects == nil || out.Reasoning == nil {
		return engine.AIResult{}, errors.New("aiengine: model reply requires non-null defects and reasoning fields")
	}
	return engine.AIResult{Pass: len(*out.Defects) == 0, Reasoning: *out.Reasoning, Defects: *out.Defects}, nil
}

// ExtractText pulls the text answering query out of the screenshot.
func (e *Engine) ExtractText(ctx context.Context, screenshotPNG []byte, query string) (engine.AIResult, error) {
	prompt := "Extract the following information from this mobile app screenshot: \"" + query + "\". " +
		`Reply ONLY with a JSON object: {"text": "<the extracted text, or empty string if not present>", ` +
		`"reasoning": "<short note>"}.`
	var out struct {
		Text      *string `json:"text"`
		Reasoning *string `json:"reasoning"`
	}
	if err := e.generateJSON(ctx, prompt, screenshotPNG, &out); err != nil {
		return engine.AIResult{}, err
	}
	if out.Text == nil || out.Reasoning == nil {
		return engine.AIResult{}, errors.New("aiengine: model reply requires non-null text and reasoning fields")
	}
	return engine.AIResult{Text: *out.Text, Reasoning: *out.Reasoning}, nil
}

// generateJSON runs one multimodal completion and decodes its JSON object into
// dst. Temperature is pinned to 0 for the most deterministic answer a model can
// give; the reply is instructed to be a bare JSON object but real models still
// return malformed output. Decoding is deliberately strict because a missing
// field must never become a zero-value success verdict.
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
	timeout := e.timeout
	if timeout <= 0 || timeout > MaxProviderTimeout {
		timeout = DefaultProviderTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	response, err := e.model.GenerateContent(callCtx, []llms.MessageContent{message}, options...)
	if err != nil {
		return fmt.Errorf("aiengine: model call failed: %w", err)
	}
	if response == nil || len(response.Choices) == 0 {
		return errors.New("aiengine: model returned no choices")
	}
	reply := response.Choices[0].Content
	if err := validateUniqueJSONKeys(reply); err != nil {
		return fmt.Errorf("aiengine: validating model reply: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(reply))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("aiengine: decoding model reply: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("aiengine: model reply contains trailing JSON content")
		}
		return fmt.Errorf("aiengine: model reply contains trailing content: %w", err)
	}
	return nil
}

func validateUniqueJSONKeys(input string) error {
	decoder := json.NewDecoder(strings.NewReader(input))
	decoder.UseNumber()
	if err := validateUniqueJSONValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("read trailing JSON content: %w", err)
		}
		return fmt.Errorf("model reply contains trailing JSON token %v", token)
	}
	return nil
}

func validateUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key has type %T", keyToken)
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			keys[key] = struct{}{}
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object closed with %v", closing)
		}
	case '[':
		for decoder.More() {
			if err := validateUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array closed with %v", closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}
