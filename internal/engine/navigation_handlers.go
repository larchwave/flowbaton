package engine

import (
	"context"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

// Navigation handlers cover authored links, browser launch, and animation waits.

// animationTimeoutMillis is ANIMATION_TIMEOUT_MS from specs/04-wire-protocols.md.
const animationTimeoutMillis int64 = 15000

// animationPollInterval is the host cadence for re-asking whether the screen
// is static, matching lookupPollInterval: the two mobile drivers answer with
// one sample and leave the waiting to their caller.
const animationPollInterval = 100 * time.Millisecond

// browserForced is the single non-empty inhabitant of device.Browser.
//
// specs/05-command-semantics-addendum.md §1.2 establishes that the authored
// surface is a boolean "force Google Chrome" flag, not a browser name. The
// frozen v0 driver contract types the field as a string, so the boolean is
// carried as exactly two states: "" (do not force) and this marker (force).
// The driver boundary retains its string type for the v0 wire contract.
const browserForced device.Browser = "chrome"

type openLinkCompiled struct {
	link       string
	autoVerify bool
	browser    device.Browser
}

type openLinkEvaluated struct {
	request device.OpenLinkRequest
}

type animationWaitCompiled struct {
	timeoutMillis             int64
	timeoutSource             string
	timeoutRequiresEvaluation bool
}

type animationWaitEvaluated struct {
	timeoutMillis int64
}

func navigationHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		{
			keyword: model.CommandOpenLink, effectClass: EffectDeviceMutation,
			postAction: postActionNoSettle,
			compile:    pureCompiler(compileOpenLink), evaluate: evaluateOpenLink, execute: executeOpenLink,
		},
		openBrowserHandlerSpec(),
		{
			keyword: model.CommandWaitForAnimationToEnd, effectClass: EffectObserved,
			compile: pureCompiler(compileAnimationWait), evaluate: evaluateAnimationWait, execute: executeAnimationWait,
		},
	}
}

// openBrowserHandlerSpec is openLink with the browser flag forced on, so it
// shares openLink's evaluator and executor and differs only in its compiler.
func openBrowserHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandOpenBrowser, effectClass: EffectDeviceMutation,
		postAction: postActionNoSettle,
		compile:    pureCompiler(compileOpenBrowser), evaluate: evaluateOpenLink, execute: executeOpenLink,
	}
}

func rejectCommandEnvelope(command model.Command) error {
	if len(command.Links) != 0 {
		return commandDecodeError(command.Kind, "must not carry metadata, a selector, or children")
	}
	return rejectCommandEnvelopeExceptLinks(command)
}

// rejectCommandEnvelopeExceptLinks is for the handlers whose keyword does
// carry prepared file links — the parser attaches them, so rejecting them
// wholesale would make the command unauthorable in real YAML.
func rejectCommandEnvelopeExceptLinks(command model.Command) error {
	if len(command.Children) != 0 || command.Condition != nil ||
		command.Label != nil || command.Optional != nil || command.Selector != nil {
		return commandDecodeError(command.Kind, "must not carry metadata, a selector, or children")
	}
	return nil
}

func compileOpenLink(command model.Command) (any, error) {
	if command.Kind != model.CommandOpenLink {
		return nil, NewConfigurationError("openLink keyword is invalid", nil)
	}
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	decoded, err := decodeStringOrObject(command)
	if err != nil {
		return nil, err
	}
	payload := openLinkCompiled{}
	if decoded.stringValue != nil {
		payload.link = *decoded.stringValue
	} else {
		object := *decoded.objectValue
		if err := object.rejectUnknown("link", "autoVerify", "browser"); err != nil {
			return nil, err
		}
		if payload.link, err = object.requireString("link"); err != nil {
			return nil, err
		}
		if payload.autoVerify, _, err = object.optionalBool("autoVerify"); err != nil {
			return nil, err
		}
		// browser is authored as a boolean and carried as the two-state string
		// the frozen v0 driver contract declares; see spec 05 §1.2 and §1.4.
		forceBrowser, _, err := object.optionalBool("browser")
		if err != nil {
			return nil, err
		}
		if forceBrowser {
			payload.browser = browserForced
		}
	}
	if strings.TrimSpace(payload.link) == "" {
		return nil, commandDecodeError(command.Kind, "requires a non-blank link")
	}
	return payload, nil
}

// compileOpenBrowser builds a link request with the browser flag forced on.
// openBrowser requires the scalar URL form.
func compileOpenBrowser(command model.Command) (any, error) {
	if command.Kind != model.CommandOpenBrowser {
		return nil, NewConfigurationError("openBrowser keyword is invalid", nil)
	}
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	link, err := decodeString(command)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(link) == "" {
		return nil, commandDecodeError(command.Kind, "requires a non-blank link")
	}
	return openLinkCompiled{link: link, browser: browserForced}, nil
}

func compileAnimationWait(command model.Command) (any, error) {
	if command.Kind != model.CommandWaitForAnimationToEnd {
		return nil, NewConfigurationError("waitForAnimationToEnd keyword is invalid", nil)
	}
	if err := rejectCommandEnvelope(command); err != nil {
		return nil, err
	}
	object, _, err := decodeOptionalObject(command)
	if err != nil {
		return nil, err
	}
	if err := object.rejectUnknown("timeout"); err != nil {
		return nil, err
	}
	payload := animationWaitCompiled{timeoutMillis: animationTimeoutMillis}
	if timeout, exists := object.raw("timeout"); exists {
		switch value := timeout.(type) {
		case int64:
			if value <= 0 {
				return nil, commandDecodeError(command.Kind, "timeout must be positive")
			}
			payload.timeoutMillis = value
		case string:
			payload.timeoutSource = value
			payload.timeoutRequiresEvaluation = js.HasInterpolationExpression(value)
			if !payload.timeoutRequiresEvaluation {
				millis, err := parseTimeoutMillis(command.Kind, value)
				if err != nil {
					return nil, err
				}
				if millis <= 0 {
					return nil, commandDecodeError(command.Kind, "timeout must be positive")
				}
				payload.timeoutMillis = millis
			}
		default:
			return nil, commandDecodeError(command.Kind, "timeout must be an integer or string")
		}
	}
	return payload, nil
}

func evaluateOpenLink(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(openLinkCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("openLink received an invalid compiled payload", nil)
	}
	// openLink and openBrowser share this evaluated payload.
	evaluated := evaluatedDispatch{command: cloneCommand(command), value: openLinkEvaluated{}}
	link, err := evaluation.Interpolate(ctx, payload.link, nil)
	if err != nil {
		return evaluated, err
	}
	if strings.TrimSpace(link) == "" {
		return evaluated, NewConfigurationError("openLink link must not be blank after interpolation", nil)
	}
	// The v0 driver method includes an optional app id
	// (specs/02-device-drivers.md:9), while the authored command does not expose
	// one. The request therefore leaves the app id empty.
	evaluated.value = openLinkEvaluated{request: device.OpenLinkRequest{
		Link: link, AutoVerify: payload.autoVerify, Browser: payload.browser,
	}}
	return evaluated, nil
}

func evaluateAnimationWait(
	ctx context.Context,
	evaluation evaluationContext,
	command model.Command,
	compiled any,
) (evaluatedDispatch, error) {
	payload, ok := compiled.(animationWaitCompiled)
	if !ok {
		return evaluatedDispatch{}, NewConfigurationError("waitForAnimationToEnd received an invalid compiled payload", nil)
	}
	timeoutMillis := payload.timeoutMillis
	if payload.timeoutRequiresEvaluation {
		resolved, err := evaluation.Interpolate(ctx, payload.timeoutSource, nil)
		if err != nil {
			return evaluatedDispatch{command: cloneCommand(command)}, err
		}
		millis, err := parseTimeoutMillis(command.Kind, resolved)
		if err != nil {
			return evaluatedDispatch{command: cloneCommand(command)}, err
		}
		if millis <= 0 {
			return evaluatedDispatch{command: cloneCommand(command)}, commandDecodeError(command.Kind, "timeout must be positive")
		}
		timeoutMillis = millis
	}
	return evaluatedDispatch{
		command: cloneCommand(command),
		value:   animationWaitEvaluated{timeoutMillis: timeoutMillis},
	}, nil
}

func executeOpenLink(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectDeviceMutation}
	payload, ok := evaluated.value.(openLinkEvaluated)
	if !ok {
		return effect, NewConfigurationError("openLink received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	if err := state.dependencies.Driver.OpenLink(ctx, payload.request); err != nil {
		return effect, err
	}
	return effect, nil
}

func executeAnimationWait(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
	effect := commandEffect{effectClass: EffectObserved}
	payload, ok := evaluated.value.(animationWaitEvaluated)
	if !ok {
		return effect, NewConfigurationError("waitForAnimationToEnd received an invalid evaluated payload", nil)
	}
	if err := ctx.Err(); err != nil {
		return effect, err
	}
	// This is a wait, not an assertion: a screen that never becomes static
	// within the timeout completes the command instead of failing the flow.
	// Only a driver error propagates.
	//
	// The waiting happens here because the drivers do not do it.
	// internal/ios samples two frames a tenth of a second apart and
	// internal/android asks the agent once; both ignore the request's
	// timeout and say the caller owns the budget. Sending the request once
	// therefore returned in a fraction of a second whatever the flow asked
	// for. internal/web is the exception -- it blocks internally until the
	// page settles or the timeout expires -- and it composes: its first
	// answer already consumes the budget, so the loop ends after one call.
	deadline := state.dependencies.Clock.Now().Add(
		time.Duration(payload.timeoutMillis) * time.Millisecond)
	for {
		static, err := state.dependencies.Driver.WaitUntilScreenIsStatic(
			ctx, device.ScreenStaticRequest{TimeoutMillis: payload.timeoutMillis})
		if err != nil {
			return effect, err
		}
		if static {
			return effect, ctx.Err()
		}
		now := state.dependencies.Clock.Now()
		if !now.Before(deadline) {
			return effect, ctx.Err()
		}
		if err := state.dependencies.Clock.Wait(
			ctx, minDuration(animationPollInterval, deadline.Sub(now))); err != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				return effect, cancellation
			}
			return effect, err
		}
	}
}
