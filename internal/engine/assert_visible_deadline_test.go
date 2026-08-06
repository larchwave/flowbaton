package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestAssertVisibleReusesOneInteractionAdjustedAbsoluteDeadline(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		optional bool
		base     time.Duration
		outcome  Outcome
	}{
		{name: "required", base: LookupTimeout, outcome: Failed},
		{name: "optional", optional: true, base: OptionalLookupTimeout, outcome: Warned},
	} {
		t.Run(test.name, func(t *testing.T) {
			start := time.Unix(700, 0)
			deadline := start.Add(100 * time.Millisecond)
			clock := newSteppingNowClock(start, 10*time.Millisecond)
			driver := conditionDriver(device.Platform("ios"), conditionRoot(), conditionRoot())
			lookup := NewElementLookup(driver, clock)
			lookup.RecordInteraction(start.Add(-(test.base - 100*time.Millisecond)))
			source := selectorCommand(model.CommandAssertVisible, "Missing", test.optional)
			snapshot := cloneCommand(source)
			dispatcher, compiled := compileBatch1Command(t, assertVisibleTestHandlerSpec(), source)
			evaluated, err := dispatcher.evaluate(context.Background(), identityConditionEvaluation(), compiled)
			if err != nil {
				t.Fatalf("evaluate(assertVisible) error = %v", err)
			}
			_, err = dispatcher.execute(context.Background(), &executionState{
				lookupFn: func() (*ElementLookup, error) { return lookup, nil },
			}, compiled, evaluated)
			var assertion *AssertionError
			if !errors.As(err, &assertion) || ClassifyOutcome(err, test.optional) != test.outcome {
				t.Fatalf("execute(assertVisible) error = %T %v outcome %q, want AssertionError/%q", err, err, ClassifyOutcome(err, test.optional), test.outcome)
			}
			if got, want := clock.WaitDeadlines(), []time.Time{deadline}; !reflect.DeepEqual(got, want) {
				t.Fatalf("assertVisible wait deadlines = %#v, want one absolute deadline %#v", got, want)
			}
			if !reflect.DeepEqual(source, snapshot) {
				t.Fatalf("assertVisible source mutated: got %#v want %#v", source, snapshot)
			}
		})
	}
}

func TestAssertVisiblePreservesCancellationIdentityAfterDescriptor(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	lookup := NewElementLookup(
		newCancelAfterDescriptorDriver(cancel, conditionRoot()),
		newConditionClock(time.Unix(710, 0)),
	)
	command := selectorCommand(model.CommandAssertVisible, "Missing", false)
	dispatcher, compiled := compileBatch1Command(t, assertVisibleTestHandlerSpec(), command)
	evaluated, err := dispatcher.evaluate(ctx, identityConditionEvaluation(), compiled)
	if err != nil {
		t.Fatalf("evaluate(assertVisible) error = %v", err)
	}
	_, err = dispatcher.execute(ctx, &executionState{
		lookupFn: func() (*ElementLookup, error) { return lookup, nil },
	}, compiled, evaluated)
	if err != context.Canceled {
		t.Fatalf("execute(assertVisible) error = %T %v, want exact context.Canceled", err, err)
	}
}

func assertVisibleTestHandlerSpec() handlerSpec {
	return handlerSpec{
		keyword: model.CommandAssertVisible, effectClass: EffectObserved,
		compile: pureCompiler(compileAssertVisible), evaluate: evaluateAssertVisible, execute: executeAssertVisible,
	}
}
