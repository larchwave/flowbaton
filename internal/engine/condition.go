package engine

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/matching"
	"github.com/nohavewho/flowbaton/internal/model"
)

type conditionServices struct {
	captureEvaluation func() (evaluationContext, error)
	captureLookup     func() (*ElementLookup, error)

	evaluation         evaluationContext
	evaluationErr      error
	evaluationCaptured bool
	lookup             *ElementLookup
	lookupErr          error
	lookupCaptured     bool
}

func (services *conditionServices) getEvaluation() (evaluationContext, error) {
	if services.evaluationCaptured {
		return services.evaluation, services.evaluationErr
	}
	services.evaluationCaptured = true
	if services.captureEvaluation == nil {
		services.evaluationErr = NewConfigurationError("condition JavaScript evaluation is unavailable", nil)
		return evaluationContext{}, services.evaluationErr
	}
	services.evaluation, services.evaluationErr = services.captureEvaluation()
	return services.evaluation, services.evaluationErr
}

func (services *conditionServices) getLookup() (*ElementLookup, error) {
	if services.lookupCaptured {
		return services.lookup, services.lookupErr
	}
	services.lookupCaptured = true
	if services.captureLookup == nil {
		services.lookupErr = NewConfigurationError("condition element lookup is unavailable", nil)
		return nil, services.lookupErr
	}
	services.lookup, services.lookupErr = services.captureLookup()
	if services.lookupErr == nil && services.lookup == nil {
		services.lookupErr = NewConfigurationError("condition element lookup is unavailable", nil)
	}
	return services.lookup, services.lookupErr
}

func (state *executionState) evaluateCondition(
	ctx context.Context,
	source *model.Condition,
) (*model.Condition, bool, error) {
	if state == nil {
		return nil, false, NewConfigurationError("condition evaluation state must not be nil", nil)
	}
	services := &conditionServices{
		captureEvaluation: func() (evaluationContext, error) {
			return captureEvaluationContext(state)
		},
		captureLookup: state.elementLookup,
	}
	return evaluateConditionWithServices(ctx, services, source)
}

// evaluateCondition evaluates an owned condition snapshot in FlowBaton's fixed
// short-circuit order. Callers may retain the returned snapshot or call the
// function again after JavaScript environment changes; source is never
// mutated and no predicate result is cached here.
func evaluateCondition(
	ctx context.Context,
	evaluation evaluationContext,
	lookup *ElementLookup,
	source *model.Condition,
) (*model.Condition, bool, error) {
	services := &conditionServices{
		captureEvaluation: func() (evaluationContext, error) { return evaluation, nil },
		captureLookup:     func() (*ElementLookup, error) { return lookup, nil },
	}
	return evaluateConditionWithServices(ctx, services, source)
}

func evaluateConditionWithServices(
	ctx context.Context,
	services *conditionServices,
	source *model.Condition,
) (*model.Condition, bool, error) {
	if ctx == nil {
		return nil, false, NewConfigurationError("condition evaluation context must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	evaluated := cloneCondition(source)
	if evaluated == nil {
		return nil, true, nil
	}

	if evaluated.Platform != nil {
		lookup, err := services.getLookup()
		if err != nil {
			return evaluated, false, err
		}
		info, err := lookup.cachedDeviceInfo(ctx)
		if err != nil {
			return evaluated, false, err
		}
		if model.Platform(info.Platform) != *evaluated.Platform {
			return evaluated, false, nil
		}
	}

	if evaluated.ScriptCondition != nil {
		evaluation, err := services.getEvaluation()
		if err != nil {
			return evaluated, false, err
		}
		rawScript := *evaluated.ScriptCondition
		script := rawScript
		exactExpression, exact := exactInterpolatedConditionExpression(rawScript)
		if exact {
			script = exactExpression
		} else {
			script, err = evaluation.Interpolate(ctx, rawScript, nil)
			if err != nil {
				return evaluated, false, err
			}
			evaluated.ScriptCondition = &script
		}
		request := js.EvalRequest{Script: script, SourceName: evaluated.Source.Path}
		if evaluated.Source.Path != "" {
			request.ScriptDir = filepath.Dir(evaluated.Source.Path)
		}
		result, err := evaluation.Evaluate(ctx, request)
		if err != nil {
			return evaluated, false, err
		}
		if exact {
			evaluated.ScriptCondition = &result.Text
		}
		if !conditionScriptTruthy(result.Value) {
			return evaluated, false, nil
		}
	}

	optional := evaluated.Optional != nil && *evaluated.Optional
	if evaluated.Visible != nil {
		evaluation, err := services.getEvaluation()
		if err != nil {
			return evaluated, false, err
		}
		if err := evaluateConditionSelector(ctx, evaluation, evaluated.Visible, "visible"); err != nil {
			return evaluated, false, err
		}
		lookup, err := services.getLookup()
		if err != nil {
			return evaluated, false, err
		}
		options := LookupOptions{Optional: optional}
		deadline := lookup.adjustedDeadline(options)
		element, err := lookup.findUntil(ctx, *evaluated.Visible, LookupOptions{Optional: true}, deadline)
		if err != nil {
			return evaluated, false, err
		}
		if element == nil {
			return evaluated, false, nil
		}
	}

	if evaluated.NotVisible != nil {
		evaluation, err := services.getEvaluation()
		if err != nil {
			return evaluated, false, err
		}
		if err := evaluateConditionSelector(ctx, evaluation, evaluated.NotVisible, "notVisible"); err != nil {
			return evaluated, false, err
		}
		lookup, err := services.getLookup()
		if err != nil {
			return evaluated, false, err
		}
		deadline := lookup.adjustedDeadline(LookupOptions{Optional: optional})
		absent, err := lookup.conditionNotVisibleUntil(ctx, *evaluated.NotVisible, deadline)
		if err != nil {
			return evaluated, false, err
		}
		if !absent {
			return evaluated, false, nil
		}
	}
	return evaluated, true, nil
}

func evaluateConditionSelector(
	ctx context.Context,
	evaluation evaluationContext,
	selector *model.ElementSelector,
	field string,
) error {
	if err := interpolateSelector(ctx, evaluation, selector); err != nil {
		return err
	}
	conditionKeyword := model.CommandKeyword("condition")
	if err := validateImplementedSelectorTargets(conditionKeyword, selector, field); err != nil {
		return err
	}
	if err := rejectUnsupportedSelectorFeatures(conditionKeyword, selector, false, true); err != nil {
		return err
	}
	if _, err := matching.Find(nil, *selector); err != nil {
		return NewConfigurationError(fmt.Sprintf("condition %s selector is invalid after interpolation", field), err)
	}
	return nil
}

func conditionScriptTruthy(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed != ""
	case *big.Int:
		return typed != nil && typed.Sign() != 0
	case big.Int:
		return typed.Sign() != 0
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int() != 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return reflected.Uint() != 0
	case reflect.Float32, reflect.Float64:
		value := reflected.Float()
		return value != 0 && !math.IsNaN(value)
	default:
		return true
	}
}

func exactInterpolatedConditionExpression(script string) (string, bool) {
	trimmed := strings.TrimSpace(script)
	if !strings.HasPrefix(trimmed, "${") {
		return "", false
	}
	closing := strings.Index(trimmed[2:], "}")
	if closing < 0 || closing+2 != len(trimmed)-1 {
		return "", false
	}
	return trimmed[2 : len(trimmed)-1], true
}
