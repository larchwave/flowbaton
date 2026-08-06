package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestCommandMetadataDistinguishesAbsentAndExplicitNumberOfRuns(t *testing.T) {
	t.Parallel()

	absent := CommandMetadata{}
	if absent.HasNumberOfRuns() {
		t.Fatalf("zero-value metadata HasNumberOfRuns() = true, want false")
	}

	explicitZero := NewCommandMetadata(0, nil, nil, "", "")
	if !explicitZero.HasNumberOfRuns() || explicitZero.NumberOfRuns() != 0 {
		t.Fatalf(
			"explicit zero metadata = present %t value %d, want present zero",
			explicitZero.HasNumberOfRuns(),
			explicitZero.NumberOfRuns(),
		)
	}

	cloned := cloneMetadata(explicitZero)
	if !cloned.HasNumberOfRuns() || cloned.NumberOfRuns() != 0 {
		t.Fatalf(
			"cloned explicit zero metadata = present %t value %d, want present zero",
			cloned.HasNumberOfRuns(),
			cloned.NumberOfRuns(),
		)
	}
	if cloneMetadata(absent).HasNumberOfRuns() {
		t.Fatal("cloned zero-value metadata changed absent numberOfRuns to present")
	}
}

func TestMetadataForEffectDefaultsOrdinaryCommandsAndPreservesExplicitZero(t *testing.T) {
	t.Parallel()

	command := model.Command{Kind: model.CommandRepeat}
	ordinary := metadataForEffect(command, commandEffect{})
	if !ordinary.HasNumberOfRuns() || ordinary.NumberOfRuns() != 1 {
		t.Fatalf(
			"ordinary metadata = present %t value %d, want present one",
			ordinary.HasNumberOfRuns(),
			ordinary.NumberOfRuns(),
		)
	}

	explicitZero := metadataForEffect(command, commandEffect{
		numberOfRuns:    0,
		numberOfRunsSet: true,
	})
	if !explicitZero.HasNumberOfRuns() || explicitZero.NumberOfRuns() != 0 {
		t.Fatalf(
			"effect metadata = present %t value %d, want present zero",
			explicitZero.HasNumberOfRuns(),
			explicitZero.NumberOfRuns(),
		)
	}
}

func TestExecutorRejectsOrContainsNegativeExplicitNumberOfRuns(t *testing.T) {
	t.Parallel()

	productFailure := NewDeviceConnectionError("device disconnected", errors.New("transport"))
	tests := []struct {
		name       string
		productErr error
	}{
		{name: "successful handler fails closed"},
		{name: "product error remains primary", productErr: productFailure},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			core, err := newExecutorCore(
				coreDependencies(enginetest.NewFakeDriver(), enginetest.NewFakeClock(time.Unix(0, 0))),
				handlerSpec{
					keyword: model.CommandAction, effectClass: EffectObserved,
					compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
					evaluate: identityEvaluator,
					execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
						return commandEffect{
							effectClass:     EffectObserved,
							numberOfRuns:    -1,
							numberOfRunsSet: true,
						}, test.productErr
					},
				},
			)
			if err != nil {
				t.Fatalf("newExecutorCore() error: %v", err)
			}

			result, executeErr := core.execute(context.Background(), model.Command{Kind: model.CommandAction}, 0)
			if test.productErr != nil {
				if executeErr != test.productErr || result.ProductError() != test.productErr {
					t.Fatalf("product error = returned %v recorded %v, want original %v", executeErr, result.ProductError(), test.productErr)
				}
			} else {
				var configuration *ConfigurationError
				if !errors.As(executeErr, &configuration) || result.ProductError() != executeErr {
					t.Fatalf("negative run count = outcome %q returned %T %v recorded %v, want configuration failure", result.Outcome(), executeErr, executeErr, result.ProductError())
				}
			}
			if result.Outcome() != Failed {
				t.Fatalf("negative run count outcome = %q, want Failed", result.Outcome())
			}
			if metadata := result.Metadata(); metadata.HasNumberOfRuns() || metadata.NumberOfRuns() < 0 {
				t.Fatalf("negative run count escaped into metadata: present %t value %d", metadata.HasNumberOfRuns(), metadata.NumberOfRuns())
			}
		})
	}
}

func TestCommandSpanRejectsNegativeNumberOfRunsWithoutConsumingSpan(t *testing.T) {
	t.Parallel()

	timeline, err := NewTimeline(enginetest.NewFakeClock(time.Unix(0, 0)))
	if err != nil {
		t.Fatalf("NewTimeline() error: %v", err)
	}
	span, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRepeat}, 0)
	if err != nil {
		t.Fatalf("BeginCommand() error: %v", err)
	}
	if _, _, finishErr := span.Finish(Completed, nil, NewCommandMetadata(-1, nil, nil, "", "")); finishErr == nil {
		t.Fatal("Finish() accepted negative numberOfRuns")
	} else {
		var configuration *ConfigurationError
		if !errors.As(finishErr, &configuration) {
			t.Fatalf("Finish() error = %T %v, want ConfigurationError", finishErr, finishErr)
		}
	}
	result, _, err := span.Finish(Skipped, NewCommandSkippedError("zero runs", nil), NewCommandMetadata(0, nil, nil, "", ""))
	if err != nil {
		t.Fatalf("Finish() after rejected metadata error: %v", err)
	}
	if !result.Metadata().HasNumberOfRuns() || result.Metadata().NumberOfRuns() != 0 {
		t.Fatalf("reused span metadata = %#v, want explicit zero", result.Metadata())
	}
}

func TestCommandSpanNegativeNumberOfRunsDoesNotReplaceProductFailure(t *testing.T) {
	t.Parallel()

	timeline, err := NewTimeline(enginetest.NewFakeClock(time.Unix(10, 0)))
	if err != nil {
		t.Fatalf("NewTimeline() error: %v", err)
	}
	span, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRepeat}, 0)
	if err != nil {
		t.Fatalf("BeginCommand() error: %v", err)
	}
	productFailure := NewDeviceConnectionError("device disconnected", errors.New("transport"))
	if _, _, finishErr := span.Finish(Failed, productFailure, NewCommandMetadata(-1, nil, nil, "", "")); finishErr != productFailure {
		t.Fatalf("Finish() error = %T %v, want original product failure %v", finishErr, finishErr, productFailure)
	}
	result, _, err := span.Finish(Failed, productFailure, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish() after rejected metadata error: %v", err)
	}
	if result.ProductError() != productFailure {
		t.Fatalf("reused span product error = %v, want %v", result.ProductError(), productFailure)
	}
}
