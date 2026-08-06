package engine

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// productionKeywords defines the sorted production handler set used by
// every registry-shape test.
func productionKeywords() []model.CommandKeyword {
	keywords := []model.CommandKeyword{
		// core flow and assertion commands
		model.CommandAssertNotVisible,
		model.CommandAssertTrue,
		model.CommandAssertVisible,
		model.CommandExtendedWaitUntil,
		model.CommandLaunchApp,
		model.CommandRepeat,
		model.CommandRetry,
		model.CommandRunFlow,
		model.CommandTapOn,
		// gesture
		model.CommandDoubleTapOn,
		model.CommandLongPressOn,
		model.CommandSwipe,
		// direct control
		model.CommandBack,
		model.CommandHideKeyboard,
		model.CommandScroll,
		model.CommandPressKey,
		// scroll search
		model.CommandScrollUntilVisible,
		// text
		model.CommandInputText,
		model.CommandEraseText,
		// random input
		model.CommandInputRandomText,
		model.CommandInputRandomNumber,
		model.CommandInputRandomEmail,
		model.CommandInputRandomPersonName,
		model.CommandInputRandomCityName,
		model.CommandInputRandomCountryName,
		model.CommandInputRandomColorName,
		// clipboard
		model.CommandCopyTextFrom,
		model.CommandSetClipboard,
		model.CommandPasteText,
		// action alias
		model.CommandAction,
		// app lifecycle
		model.CommandStopApp,
		model.CommandKillApp,
		model.CommandClearState,
		model.CommandClearKeychain,
		// navigation
		model.CommandOpenLink,
		model.CommandWaitForAnimationToEnd,
		// browser navigation
		model.CommandOpenBrowser,
		// device state
		model.CommandSetLocation,
		model.CommandSetOrientation,
		model.CommandSetPermissions,
		model.CommandSetAirplaneMode,
		model.CommandToggleAirplaneMode,
		// media and artifacts
		model.CommandTakeScreenshot,
		model.CommandStartRecording,
		model.CommandStopRecording,
		model.CommandAddMedia,
		// screenshot assertion
		model.CommandAssertScreenshot,
		// scripting
		model.CommandRunScript,
		model.CommandEvalScript,
		// travel
		model.CommandTravel,
		// screenshot-based AI commands
		model.CommandAssertNoDefectsWithAI,
		model.CommandAssertWithAI,
		model.CommandExtractTextWithAI,
	}
	sort.Slice(keywords, func(left, right int) bool { return keywords[left] < keywords[right] })
	return keywords
}

// productionKeywordStrings renders the production set for string tests.
func productionKeywordStrings() []string {
	keywords := productionKeywords()
	names := make([]string, len(keywords))
	for index, keyword := range keywords {
		names[index] = string(keyword)
	}
	return names
}

func TestProductionHandlerRegistryMatchesAcceptedCommandSet(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error: %v", err)
	}
	got := make([]model.CommandKeyword, 0, len(registry.byKeyword))
	for keyword := range registry.byKeyword {
		got = append(got, keyword)
	}
	sort.Slice(got, func(left, right int) bool { return got[left] < got[right] })
	want := productionKeywords()
	if len(want) != 53 {
		t.Fatalf("production keyword count = %d, want exactly fifty-three", len(want))
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("production handler registry = %#v, want %#v", got, want)
	}
}

// TestProductionHandlerRegistryRejectsPartialInteractionExposure requires the
// complete interaction command family.
func TestProductionHandlerRegistryRejectsPartialInteractionExposure(t *testing.T) {
	t.Parallel()

	registry, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error: %v", err)
	}
	for _, keyword := range []model.CommandKeyword{
		model.CommandDoubleTapOn, model.CommandLongPressOn, model.CommandSwipe,
		model.CommandBack, model.CommandHideKeyboard, model.CommandScroll, model.CommandPressKey,
		model.CommandScrollUntilVisible, model.CommandInputText, model.CommandEraseText,
		model.CommandInputRandomText, model.CommandInputRandomNumber, model.CommandInputRandomEmail,
		model.CommandInputRandomPersonName, model.CommandInputRandomCityName,
		model.CommandInputRandomCountryName, model.CommandInputRandomColorName,
		model.CommandCopyTextFrom, model.CommandSetClipboard, model.CommandPasteText,
		model.CommandAction,
	} {
		if _, exposed := registry.lookup(keyword); !exposed {
			t.Fatalf("production registry is missing atomically exposed %s", keyword)
		}
	}
}

func TestHandlerRegistryRejectsInvalidRegistrations(t *testing.T) {
	t.Parallel()

	compile := pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil })
	execute := func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
		return commandEffect{effectClass: EffectObserved}, nil
	}
	valid := handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectObserved,
		compile: compile, execute: execute,
	}

	tests := []struct {
		name  string
		specs []handlerSpec
	}{
		{name: "blank keyword", specs: []handlerSpec{{effectClass: EffectObserved, compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "duplicate keyword", specs: []handlerSpec{valid, valid}},
		{name: "missing compiler", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectObserved, evaluate: identityEvaluator, execute: execute}}},
		{name: "missing evaluator", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectObserved, compile: compile, execute: execute}}},
		{name: "missing executor", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectObserved, compile: compile, evaluate: identityEvaluator}}},
		{name: "no effect", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectNone, compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "unknown effect class", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: effectClass(255), compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "settle without request", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation, postAction: postActionSettle, compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "no settle with request", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation, postAction: postActionNoSettle, settleRequest: func(evaluatedDispatch) (device.SettleRequest, error) { return device.SettleRequest{}, nil }, compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "non mutation policy", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectObserved, postAction: postActionNoSettle, compile: compile, evaluate: identityEvaluator, execute: execute}}},
		{name: "unknown post action", specs: []handlerSpec{{keyword: model.CommandLaunchApp, effectClass: EffectDeviceMutation, postAction: postActionPolicy(255), compile: compile, evaluate: identityEvaluator, execute: execute}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newHandlerRegistry(test.specs...); err == nil {
				t.Fatal("newHandlerRegistry() error = nil, want configuration error")
			} else {
				var configurationError *ConfigurationError
				if !errors.As(err, &configurationError) {
					t.Fatalf("newHandlerRegistry() error = %T, want *ConfigurationError", err)
				}
			}
		})
	}
}

func TestHandlerRegistryRejectsEffectNoneBeforeExecution(t *testing.T) {
	t.Parallel()

	executeCalls := 0
	_, err := newHandlerRegistry(handlerSpec{
		evaluate:    identityEvaluator,
		keyword:     model.CommandLaunchApp,
		effectClass: EffectNone,
		compile:     pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			executeCalls++
			return commandEffect{effectClass: EffectNone}, nil
		},
	})
	if err == nil {
		t.Fatal("newHandlerRegistry() error = nil, want EffectNone rejection")
	}
	if executeCalls != 0 {
		t.Fatalf("EffectNone handler executed %d time(s) during rejection", executeCalls)
	}
}

func TestHandlerRegistryRejectsDeviceMutationWithoutPostActionPolicy(t *testing.T) {
	t.Parallel()

	_, err := newHandlerRegistry(handlerSpec{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectDeviceMutation,
		compile: pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
		execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
			return commandEffect{effectClass: EffectDeviceMutation}, nil
		},
	})
	var configuration *ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("newHandlerRegistry() error = %T %v, want missing post-action *ConfigurationError", err, err)
	}
}

func TestHandlerRegistryCopiesSpecifications(t *testing.T) {
	t.Parallel()

	compile := pureCompiler(func(model.Command) (any, error) { return "compiled", nil })
	execute := func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
		return commandEffect{effectClass: EffectHostMutation}, nil
	}
	specs := []handlerSpec{{
		evaluate: identityEvaluator,
		keyword:  model.CommandLaunchApp, effectClass: EffectHostMutation,
		compile: compile, execute: execute,
	}}
	registry, err := newHandlerRegistry(specs...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error: %v", err)
	}

	specs[0].keyword = model.CommandStopApp
	specs[0].effectClass = EffectNone
	got, ok := registry.lookup(model.CommandLaunchApp)
	if !ok || got.keyword != model.CommandLaunchApp || got.effectClass != EffectHostMutation {
		t.Fatalf("lookup() = %#v, %t; registry retained caller-owned mutation", got, ok)
	}
	if _, ok := registry.lookup(model.CommandStopApp); ok {
		t.Fatal("registry changed after caller mutated source specifications")
	}
}
