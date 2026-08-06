package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch2BPrivateRegistryStaticPolicyAndExactAliases(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	got := sortedHandlerKeywords(registry)
	want := []string{"action", "back", "copyTextFrom", "hideKeyboard", "pasteText", "scroll", "setClipboard"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("private registry = %#v, want %#v", got, want)
	}
	spec, ok := registry.lookup(model.CommandAction)
	if !ok || spec.keyword != model.CommandAction || spec.effectClass != EffectDeviceMutation ||
		spec.postAction != postActionNoSettle || spec.settleRequest != nil ||
		spec.exactErrorPolicy != exactErrorPublicationUnspecified || spec.requiredService != requiredServiceNone {
		t.Fatalf("action spec = %#v, want one static device/no-settle/nil-factory policy", spec)
	}

	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatal(err)
	}
	wantProduction := productionKeywordStrings()
	if got := sortedHandlerKeywords(production); !reflect.DeepEqual(got, wantProduction) {
		t.Fatalf("production registry = %#v, want %#v", got, wantProduction)
	}
	// The production registry includes action aliases.
	if _, exposed := production.lookup(model.CommandAction); !exposed {
		t.Fatalf("production registry must expose %s", model.CommandAction)
	}
	for _, alias := range batch2BAliases() {
		compiled, compileErr := compileActionAlias(batch2BActionCommand(alias))
		if compileErr != nil {
			t.Fatalf("compileActionAlias(%q) error = %v", alias, compileErr)
		}
		plan, ok := compiled.(actionAliasCompiled)
		if !ok || plan.target != model.CommandKeyword(alias) {
			t.Fatalf("compileActionAlias(%q) = %#v, want frozen target %q", alias, compiled, alias)
		}
	}
}

func TestInteractionBatch2BStrictCompileGrammarOwnershipAndForgedPayloads(t *testing.T) {
	t.Parallel()

	// Mixed-case aliases are valid and therefore excluded from this invalid set.
	invalidValues := []any{
		"", " ", "unknown", "clearKeychain",
		"${ACTION}", "prefix-${ACTION}", true, int64(1), map[string]any{"alias": "back"}, []any{"back"}, nil,
	}
	for _, value := range invalidValues {
		command := model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: value}
		if _, err := compileActionAlias(command); !isConfigurationError(err) {
			t.Fatalf("compileActionAlias(%#v) error = %T %v, want ConfigurationError", value, err, err)
		}
	}
	for _, command := range []model.Command{
		{Kind: model.CommandAction, Form: model.CommandFormScalar},
		{Kind: model.CommandBack, Form: model.CommandFormObject, Arguments: "back"},
		batch2BWithEnvelope(batch2BActionCommand("back"), "children"),
		batch2BWithEnvelope(batch2BActionCommand("back"), "condition"),
		batch2BWithEnvelope(batch2BActionCommand("back"), "links"),
		batch2BWithEnvelope(batch2BActionCommand("back"), "label"),
		batch2BWithEnvelope(batch2BActionCommand("back"), "optional"),
		batch2BWithEnvelope(batch2BActionCommand("back"), "selector"),
	} {
		if _, err := compileActionAlias(command); !isConfigurationError(err) {
			t.Fatalf("compileActionAlias(%#v) error = %T %v, want ConfigurationError", command, err, err)
		}
	}

	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	source := batch2BActionCommand("pasteText")
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Arguments = "back"
	source.Children = []model.Command{batch2BActionCommand("scroll")}
	if plan := compiled.value.(actionAliasCompiled); plan.target != model.CommandPasteText {
		t.Fatalf("compiled target changed after source mutation: %#v", plan)
	}
	if compiled.command.Arguments != "pasteText" || len(compiled.command.Children) != 0 {
		t.Fatalf("compiled command aliased source = %#v", compiled.command)
	}

	evaluation := batch2BEvaluation("com.example.batch2b", nil)
	for name, call := range map[string]func() error{
		"wrong compiled type": func() error {
			_, err := evaluateActionAlias(context.Background(), evaluation, batch2BActionCommand("back"), struct{}{})
			return err
		},
		"zero compiled target": func() error {
			_, err := evaluateActionAlias(context.Background(), evaluation, batch2BActionCommand("back"), actionAliasCompiled{})
			return err
		},
		"unsupported compiled target": func() error {
			_, err := evaluateActionAlias(context.Background(), evaluation, batch2BActionCommand("back"), actionAliasCompiled{target: model.CommandPressKey})
			return err
		},
		"compiled command mismatch": func() error {
			_, err := evaluateActionAlias(context.Background(), evaluation, batch2BActionCommand("scroll"), actionAliasCompiled{target: model.CommandBack})
			return err
		},
		"wrong evaluated type": func() error {
			_, err := executeActionAlias(context.Background(), &executionState{}, evaluatedDispatch{command: batch2BActionCommand("back"), value: struct{}{}})
			return err
		},
		"zero evaluated target": func() error {
			_, err := executeActionAlias(context.Background(), &executionState{}, evaluatedDispatch{command: batch2BActionCommand("back"), value: actionAliasEvaluated{}})
			return err
		},
		"forged direct payload": func() error {
			_, err := executeActionAlias(context.Background(), &executionState{}, evaluatedDispatch{
				command: batch2BActionCommand("back"),
				value:   actionAliasEvaluated{target: model.CommandBack, value: batch2ADirectEvaluated{keyword: model.CommandScroll, appID: "app"}},
			})
			return err
		},
		"forged paste payload": func() error {
			_, err := executeActionAlias(context.Background(), &executionState{}, evaluatedDispatch{
				command: batch2BActionCommand("pasteText"),
				value:   actionAliasEvaluated{target: model.CommandPasteText, value: batch2ADirectEvaluated{keyword: model.CommandPasteText, appID: "app"}},
			})
			return err
		},
		"typed nil evaluated payload": func() error {
			var payload *batch2ADirectEvaluated
			_, err := executeActionAlias(context.Background(), &executionState{}, evaluatedDispatch{
				command: batch2BActionCommand("back"),
				value:   actionAliasEvaluated{target: model.CommandBack, value: payload},
			})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := call(); !isConfigurationError(err) {
				t.Fatalf("error = %T %v, want ConfigurationError", err, err)
			}
		})
	}

	for _, test := range []struct {
		name        string
		command     model.Command
		evaluated   actionAliasEvaluated
		wantReads   int
		wantMessage string
	}{
		{
			name: "forged blank direct app ID", command: batch2BActionCommand("back"),
			evaluated: actionAliasEvaluated{
				target: model.CommandBack,
				value:  batch2ADirectEvaluated{keyword: model.CommandBack, appID: ""},
			},
			wantMessage: "back evaluated plan requires a non-blank appId",
		},
		{
			name: "forged blank paste app ID", command: batch2BActionCommand("pasteText"),
			evaluated: actionAliasEvaluated{
				target: model.CommandPasteText,
				value:  pasteTextEvaluated{appID: " "},
			},
			wantReads:   1,
			wantMessage: "pasteText evaluated plan requires a non-blank appId",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			driver := batch2ADriver("android")
			clock := newAdvancingClock()
			lookupCalls := 0
			copiedReads := 0
			state := &executionState{
				dependencies: Dependencies{Driver: driver, Clock: clock},
				lookupFn: func() (*ElementLookup, error) {
					lookupCalls++
					return NewElementLookup(driver, clock), nil
				},
				copiedTextFn: func() (string, error) {
					copiedReads++
					return "must-not-paste", nil
				},
			}
			effect, executeErr := executeActionAlias(context.Background(), state, evaluatedDispatch{
				command: test.command,
				value:   test.evaluated,
			})
			if !isConfigurationError(executeErr) || executeErr.Error() != test.wantMessage ||
				effect.effectClass != EffectDeviceMutation {
				t.Fatalf("forged blank app ID = effect %#v error %T %v, want ConfigurationError %q", effect, executeErr, executeErr, test.wantMessage)
			}
			if lookupCalls != 0 || copiedReads != test.wantReads || len(driver.Actions()) != 0 ||
				batch2BPhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
				t.Fatalf("forged blank app ID effects = lookup %d copied reads %d Driver %#v", lookupCalls, copiedReads, driver.Actions())
			}
		})
	}
}

func TestInteractionBatch2BExecuteRejectsAuthoredCommandTargetMismatchBeforeEffects(t *testing.T) {
	t.Parallel()

	driver := batch2ADriver("android")
	lookupCalls := 0
	copiedReads := 0
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: newAdvancingClock()},
		lookupFn: func() (*ElementLookup, error) {
			lookupCalls++
			return nil, nil
		},
		copiedTextFn: func() (string, error) {
			copiedReads++
			return "must-not-read", nil
		},
	}
	evaluated := evaluatedDispatch{
		command: batch2BActionCommand("scroll"),
		value: actionAliasEvaluated{
			target: model.CommandBack,
			value: batch2ADirectEvaluated{
				keyword: model.CommandBack,
				appID:   "com.example.batch2b",
			},
		},
	}

	effect, err := executeActionAlias(context.Background(), state, evaluated)
	if !isConfigurationError(err) || err.Error() != "action evaluated command does not match its target" {
		t.Fatalf("executeActionAlias() error = %T %v, want exact ConfigurationError", err, err)
	}
	if effect.effectClass != EffectDeviceMutation || effect.exactErrorRequest != nil ||
		effect.exactErrorPropagation != nil || effect.exactErrorDisposition != nil ||
		effect.numberOfRuns != 0 || effect.numberOfRunsSet || effect.evaluatedCommand != nil ||
		len(effect.logMessages) != 0 || effect.insight != "" || effect.aiReasoning != "" ||
		len(effect.finalizedArtifacts) != 0 || len(effect.artifactWrites) != 0 {
		t.Fatalf("executeActionAlias() effect = %#v, want device-mutation classification only", effect)
	}
	if lookupCalls != 0 || copiedReads != 0 || len(driver.Actions()) != 0 ||
		batch2BPhysicalCount(driver.Actions()) != 0 || len(settleRequests(driver.Actions())) != 0 {
		t.Fatalf("executeActionAlias() effects = lookup %d copied reads %d Driver %#v", lookupCalls, copiedReads, driver.Actions())
	}
}

func TestInteractionBatch2BDirectActionEquivalenceStress(t *testing.T) {
	for _, alias := range batch2BAliases() {
		t.Run(alias, func(t *testing.T) {
			directDriver := batch2ADriver("android")
			actionDriver := batch2ADriver("android")
			directClock := newAdvancingClock()
			actionClock := newAdvancingClock()
			directLookup := NewElementLookup(directDriver, directClock)
			actionLookup := NewElementLookup(actionDriver, actionClock)
			directEffect, directEvaluated, directErr := batch2BExecuteLeaf(
				context.Background(), batch2BDirectCommand(alias), batch2BEvaluation("com.example.batch2b."+alias, nil),
				directDriver, directClock, directLookup, "copied-"+alias, nil,
			)
			actionEffect, actionEvaluated, actionErr := batch2BExecuteLeaf(
				context.Background(), batch2BActionCommand(alias), batch2BEvaluation("com.example.batch2b."+alias, nil),
				actionDriver, actionClock, actionLookup, "copied-"+alias, nil,
			)
			if directErr != nil || actionErr != nil || directEffect.effectClass != EffectDeviceMutation ||
				actionEffect.effectClass != EffectDeviceMutation {
				t.Fatalf("effects direct %#v action %#v errors %v / %v", directEffect, actionEffect, directErr, actionErr)
			}
			if !reflect.DeepEqual(directDriver.Actions(), actionDriver.Actions()) {
				t.Fatalf("Driver traces differ:\ndirect %#v\naction %#v", directDriver.Actions(), actionDriver.Actions())
			}
			if got := actionEvaluated.command; got.Kind != model.CommandAction || got.Arguments != alias {
				t.Fatalf("Action evaluated metadata = %#v", got)
			}
			plan, ok := actionEvaluated.value.(actionAliasEvaluated)
			if !ok || plan.target != model.CommandKeyword(alias) {
				t.Fatalf("Action evaluated payload = %#v", actionEvaluated.value)
			}
			switch alias {
			case "back", "hideKeyboard", "scroll":
				direct := plan.value.(batch2ADirectEvaluated)
				if direct.keyword != model.CommandKeyword(alias) || direct.appID != "com.example.batch2b."+alias {
					t.Fatalf("Action direct payload = %#v", direct)
				}
			case "pasteText":
				paste := plan.value.(pasteTextEvaluated)
				if paste.appID != "com.example.batch2b.pasteText" {
					t.Fatalf("Action paste payload = %#v", paste)
				}
			}
			if directEvaluated.command.Kind != model.CommandKeyword(alias) {
				t.Fatalf("direct evaluated metadata = %#v", directEvaluated.command)
			}
			if got, want := actionLookup.AdjustedTimeout(LookupOptions{}), directLookup.AdjustedTimeout(LookupOptions{}); got != want {
				t.Fatalf("watermark/settle timeout = action %v direct %v", got, want)
			}
		})
	}
}

func TestInteractionBatch2BBackHidePlatformMatrixAndFailureCutoff(t *testing.T) {
	t.Parallel()

	for _, alias := range []string{"back", "hideKeyboard"} {
		for _, platform := range []device.Platform{"android", "ios", "web"} {
			t.Run(alias+"/"+string(platform), func(t *testing.T) {
				direct := batch2ADriver(platform)
				action := batch2ADriver(platform)
				_, _, directErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BDirectCommand(alias), direct, "")
				_, _, actionErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BActionCommand(alias), action, "")
				if directErr != nil || actionErr != nil || !reflect.DeepEqual(direct.Actions(), action.Actions()) || batch2BPhysicalCount(action.Actions()) != 1 {
					t.Fatalf("platform %s direct %#v action %#v errors %v / %v", platform, direct.Actions(), action.Actions(), directErr, actionErr)
				}
			})
		}
		for _, platform := range []device.Platform{"", "linux", "ANDROID"} {
			direct := batch2ADriver(platform)
			action := batch2ADriver(platform)
			_, _, directErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BDirectCommand(alias), direct, "")
			_, _, actionErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BActionCommand(alias), action, "")
			batch2BAssertEquivalentErrors(t, directErr, actionErr, nil)
			if !isConfigurationError(actionErr) || batch2BPhysicalCount(direct.Actions()) != 0 ||
				batch2BPhysicalCount(action.Actions()) != 0 || len(settleRequests(action.Actions())) != 0 {
				t.Fatalf("invalid platform %q direct %#v action %#v errors %v / %v", platform, direct.Actions(), action.Actions(), directErr, actionErr)
			}
		}
	}
}

func TestInteractionBatch2BCancellationWatermarkAndSettleEquivalence(t *testing.T) {
	t.Parallel()

	for _, alias := range batch2BAliases() {
		t.Run(alias+"/pre-cancel", func(t *testing.T) {
			directCtx, directCancel := context.WithCancel(context.Background())
			directCancel()
			actionCtx, actionCancel := context.WithCancel(context.Background())
			actionCancel()
			direct := batch2ADriver("android")
			action := batch2ADriver("android")
			_, _, directErr := batch2BExecuteLeafWithFreshLookup(directCtx, batch2BDirectCommand(alias), direct, "copied")
			_, _, actionErr := batch2BExecuteLeafWithFreshLookup(actionCtx, batch2BActionCommand(alias), action, "copied")
			batch2BAssertEquivalentErrors(t, directErr, actionErr, context.Canceled)
			if batch2BPhysicalCount(direct.Actions()) != 0 || batch2BPhysicalCount(action.Actions()) != 0 || len(settleRequests(action.Actions())) != 0 {
				t.Fatalf("pre-cancel traces direct %#v action %#v", direct.Actions(), action.Actions())
			}
		})

		t.Run(alias+"/post-call-cancel", func(t *testing.T) {
			directCtx, directCancel := context.WithCancel(context.Background())
			directBase := batch2ADriver("android")
			directDriver := &batch2BCancelAfterDriver{Driver: directBase, cancel: directCancel}
			directClock := &batch1ATraceClock{now: time.Unix(1700, 0).UTC()}
			directLookup := NewElementLookup(directDriver, directClock)
			_, _, directErr := batch2BExecuteLeaf(
				directCtx, batch2BDirectCommand(alias), batch2BEvaluation("com.example.batch2b", nil),
				directDriver, directClock, directLookup, "copied", nil,
			)

			actionCtx, actionCancel := context.WithCancel(context.Background())
			actionBase := batch2ADriver("android")
			actionDriver := &batch2BCancelAfterDriver{Driver: actionBase, cancel: actionCancel}
			actionClock := &batch1ATraceClock{now: time.Unix(1700, 0).UTC()}
			actionLookup := NewElementLookup(actionDriver, actionClock)
			_, _, actionErr := batch2BExecuteLeaf(
				actionCtx, batch2BActionCommand(alias), batch2BEvaluation("com.example.batch2b", nil),
				actionDriver, actionClock, actionLookup, "copied", nil,
			)
			batch2BAssertEquivalentErrors(t, directErr, actionErr, context.Canceled)
			if batch2BPhysicalCount(directBase.Actions()) != 1 || batch2BPhysicalCount(actionBase.Actions()) != 1 ||
				len(settleRequests(directBase.Actions())) != 0 || len(settleRequests(actionBase.Actions())) != 0 {
				t.Fatalf("post-call traces direct %#v action %#v", directBase.Actions(), actionBase.Actions())
			}
			directClock.now = directClock.now.Add(time.Second)
			actionClock.now = actionClock.now.Add(time.Second)
			if directLookup.AdjustedTimeout(LookupOptions{}) != LookupTimeout-time.Second ||
				actionLookup.AdjustedTimeout(LookupOptions{}) != LookupTimeout-time.Second {
				t.Fatalf("post-call watermark missing direct %v action %v", directLookup.AdjustedTimeout(LookupOptions{}), actionLookup.AdjustedTimeout(LookupOptions{}))
			}
		})

		for _, settle := range []struct {
			name string
			err  error
		}{
			{name: "ordinary ignored", err: NewOperationError("ordinary settle", nil)},
			{name: "configuration terminal", err: NewConfigurationError("configuration settle", nil)},
			{name: "connection terminal", err: NewDeviceConnectionError("connection settle", nil)},
		} {
			t.Run(alias+"/"+settle.name, func(t *testing.T) {
				direct := batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: settle.err}})
				action := batch2ADriverWithSettle("android", []enginetest.Result[*device.ViewHierarchy]{{Err: settle.err}})
				_, _, directErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BDirectCommand(alias), direct, "copied")
				_, _, actionErr := batch2BExecuteLeafWithFreshLookup(context.Background(), batch2BActionCommand(alias), action, "copied")
				batch2BAssertEquivalentErrors(t, directErr, actionErr, nil)
				if settle.name == "ordinary ignored" && actionErr != nil {
					t.Fatalf("ordinary settle error = %v", actionErr)
				}
				if settle.name != "ordinary ignored" && actionErr == nil {
					t.Fatal("terminal settle error = nil")
				}
				if !reflect.DeepEqual(direct.Actions(), action.Actions()) {
					t.Fatalf("settle traces direct %#v action %#v", direct.Actions(), action.Actions())
				}
			})
		}
	}
}

func TestInteractionBatch2BSourceAppIDRequestAndResultMetadataOwnership(t *testing.T) {
	t.Parallel()

	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(context.Background(), compileContext{}, batch2BActionCommand("pasteText"))
	if err != nil {
		t.Fatal(err)
	}
	appIDBytes := []byte("com.example.batch2b.owned")
	aliasedAppID := unsafe.String(unsafe.SliceData(appIDBytes), len(appIDBytes))
	evaluated, err := dispatcher.evaluate(context.Background(), evaluationContext{
		activeConfig: model.Config{AppID: "${APP}"}, hasActiveConfig: true,
		interpolateFn: func(context.Context, string, map[string]any) (string, error) {
			return aliasedAppID, nil
		},
	}, compiled)
	if err != nil {
		t.Fatal(err)
	}
	appIDBytes[0] = 'X'
	plan := evaluated.value.(actionAliasEvaluated)
	if got := plan.value.(pasteTextEvaluated).appID; got != "com.example.batch2b.owned" {
		t.Fatalf("owned app ID = %q", got)
	}
	if evaluated.command.Kind != model.CommandAction || evaluated.command.Arguments != "pasteText" {
		t.Fatalf("evaluated Action command = %#v", evaluated.command)
	}

	base := batch2ADriver("android")
	mutating := &batch4AMutatingRequestDriver{Driver: base}
	clock := newAdvancingClock()
	lookup := NewElementLookup(mutating, clock)
	effect, executeErr := dispatcher.execute(context.Background(), &executionState{
		dependencies: Dependencies{Driver: mutating, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		copiedTextFn: func() (string, error) { return strings.Clone("owned text 世界"), nil },
	}, compiled, evaluated)
	if executeErr != nil || effect.effectClass != EffectDeviceMutation {
		t.Fatalf("execute = effect %#v error %v", effect, executeErr)
	}
	requests := batch5InputRequests(base.Actions())
	want := []device.InputTextRequest{{Text: "owned text 世界", AppIDs: []string{"com.example.batch2b.owned"}}}
	if !reflect.DeepEqual(requests, want) {
		t.Fatalf("InputText requests = %#v, want %#v", requests, want)
	}
	requests[0].AppIDs[0] = "mutated"
	requests[0].Text = "mutated"
	if fresh := batch5InputRequests(base.Actions()); !reflect.DeepEqual(fresh, want) {
		t.Fatalf("returned request mutation escaped = %#v", fresh)
	}
	if got := evaluated.value.(actionAliasEvaluated).value.(pasteTextEvaluated).appID; got != "com.example.batch2b.owned" {
		t.Fatalf("Driver mutation escaped into evaluated plan = %q", got)
	}
}

func batch2BHandlerSpecs() []handlerSpec {
	return []handlerSpec{
		backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(),
		clipboardHandlerSpecs()[0], clipboardHandlerSpecs()[1], clipboardHandlerSpecs()[2],
		actionHandlerSpec(),
	}
}

func batch2BAliases() []string {
	return []string{"back", "hideKeyboard", "scroll", "pasteText"}
}

func batch2BActionCommand(alias string) model.Command {
	return model.Command{Kind: model.CommandAction, Form: model.CommandFormObject, Arguments: alias}
}

func batch2BDirectCommand(alias string) model.Command {
	keyword := model.CommandKeyword(alias)
	if keyword == model.CommandPasteText {
		return batch5PasteCommand()
	}
	return batch2ABareCommand(keyword)
}

func batch2BWithEnvelope(command model.Command, field string) model.Command {
	switch field {
	case "children":
		command.Children = []model.Command{batch2BActionCommand("scroll")}
	case "condition":
		command.Condition = &model.Condition{}
	case "links":
		command.Links = []model.FileLink{{Kind: model.FileLinkFlow, Path: "foreign.yaml"}}
	case "label":
		command.Label = stringPointer("label")
	case "optional":
		command.Optional = boolPointer(true)
	case "selector":
		command.Selector = &model.ElementSelector{TextRegex: stringPointer("target")}
	}
	return command
}

func batch2BEvaluation(appID string, interpolateErr error) evaluationContext {
	return evaluationContext{
		activeConfig: model.Config{AppID: appID}, hasActiveConfig: true,
		interpolateFn: func(_ context.Context, input string, _ map[string]any) (string, error) {
			if interpolateErr != nil {
				return "", interpolateErr
			}
			return strings.Clone(input), nil
		},
	}
}

func batch2BExecuteLeafWithFreshLookup(
	ctx context.Context,
	command model.Command,
	driver *enginetest.FakeDriver,
	copied string,
) (commandEffect, evaluatedDispatch, error) {
	clock := newAdvancingClock()
	return batch2BExecuteLeaf(
		ctx, command, batch2BEvaluation("com.example.batch2b", nil), driver, clock,
		NewElementLookup(driver, clock), copied, nil,
	)
}

func batch2BExecuteLeaf(
	ctx context.Context,
	command model.Command,
	evaluation evaluationContext,
	driver device.Driver,
	clock Clock,
	lookup *ElementLookup,
	copied string,
	copiedErr error,
) (commandEffect, evaluatedDispatch, error) {
	registry, err := newHandlerRegistry(batch2BHandlerSpecs()...)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compile(ctx, compileContext{}, command)
	if err != nil {
		return commandEffect{}, evaluatedDispatch{}, err
	}
	evaluated, err := dispatcher.evaluate(ctx, evaluation, compiled)
	if err != nil {
		return commandEffect{}, evaluated, normalizeTerminalError(fmt.Sprintf("command %s failed", command.Kind), err)
	}
	state := &executionState{
		dependencies: Dependencies{Driver: driver, Clock: clock},
		lookupFn:     func() (*ElementLookup, error) { return lookup, nil },
		copiedTextFn: func() (string, error) {
			if copiedErr != nil {
				return "", copiedErr
			}
			return strings.Clone(copied), nil
		},
	}
	effect, err := dispatcher.execute(ctx, state, compiled, evaluated)
	return effect, evaluated, normalizeTerminalError(fmt.Sprintf("command %s failed", command.Kind), err)
}

func batch2BPhysicalCount(actions []enginetest.Action) int {
	return batch2APhysicalCount(actions) + countBatch2AMethod(actions, enginetest.MethodInputText) +
		countBatch2AMethod(actions, enginetest.MethodClearKeychain)
}

func batch2BAssertEquivalentErrors(t testing.TB, direct, action, sentinel error) {
	t.Helper()
	if (direct == nil) != (action == nil) {
		t.Fatalf("error presence differs: direct %T %v, action %T %v", direct, direct, action, action)
	}
	if direct == nil {
		return
	}
	if reflect.TypeOf(direct) != reflect.TypeOf(action) || direct.Error() != action.Error() ||
		classifyTerminalError(direct) != classifyTerminalError(action) {
		t.Fatalf("errors differ: direct %T %q class %v, action %T %q class %v", direct, direct.Error(), classifyTerminalError(direct), action, action.Error(), classifyTerminalError(action))
	}
	if sentinel != nil && (!errors.Is(direct, sentinel) || !errors.Is(action, sentinel)) {
		t.Fatalf("errors do not retain sentinel %v: direct %v action %v", sentinel, direct, action)
	}
}

type batch2BCancelAfterDriver struct {
	device.Driver
	cancel context.CancelFunc
}

func (driver *batch2BCancelAfterDriver) BackPress(ctx context.Context) error {
	err := driver.Driver.BackPress(ctx)
	driver.cancel()
	return err
}

func (driver *batch2BCancelAfterDriver) HideKeyboard(ctx context.Context) error {
	err := driver.Driver.HideKeyboard(ctx)
	driver.cancel()
	return err
}

func (driver *batch2BCancelAfterDriver) ScrollVertical(ctx context.Context, request device.ScrollVerticalRequest) error {
	err := driver.Driver.ScrollVertical(ctx, request)
	driver.cancel()
	return err
}

func (driver *batch2BCancelAfterDriver) InputText(ctx context.Context, request device.InputTextRequest) error {
	err := driver.Driver.InputText(ctx, request)
	driver.cancel()
	return err
}
