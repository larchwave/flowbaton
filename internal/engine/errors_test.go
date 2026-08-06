package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestTerminalControlPrecedenceDominatesMixedRetryableChains(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
		warnable  bool
		skipped   bool
		outcome   Outcome
		surfaced  bool
	}{
		{
			name:    "device outside operation",
			err:     NewDeviceConnectionError("device", NewOperationError("retry", errors.New("transport"))),
			outcome: Failed, surfaced: true,
		},
		{
			name:    "operation outside device",
			err:     NewOperationError("retry", NewDeviceConnectionError("device", errors.New("transport"))),
			outcome: Failed, surfaced: true,
		},
		{
			name:    "configuration outside assertion",
			err:     NewConfigurationError("configuration", NewAssertionError("assertion", errors.New("invalid"))),
			outcome: Failed, surfaced: true,
		},
		{
			name:    "assertion outside configuration",
			err:     NewAssertionError("assertion", NewConfigurationError("configuration", errors.New("invalid"))),
			outcome: Failed, surfaced: true,
		},
		{
			name:    "cancellation inside operation",
			err:     NewOperationError("retry", context.Canceled),
			outcome: Cancelled, surfaced: true,
		},
		{
			name: "cancellation outside operation",
			err: &terminalControlTestError{
				control: context.Canceled,
				cause:   NewOperationError("retry", errors.New("product")),
			},
			outcome: Cancelled, surfaced: true,
		},
		{
			name:    "deadline inside assertion",
			err:     NewAssertionError("assertion", context.DeadlineExceeded),
			outcome: Cancelled, surfaced: true,
		},
		{
			name: "deadline outside assertion",
			err: &terminalControlTestError{
				control: context.DeadlineExceeded,
				cause:   NewAssertionError("assertion", errors.New("product")),
			},
			outcome: Cancelled, surfaced: true,
		},
		{
			name:    "skipped outside operation",
			err:     NewCommandSkippedError("skipped", NewOperationError("retry", errors.New("condition"))),
			skipped: true, outcome: Skipped,
		},
		{
			name:    "operation outside skipped",
			err:     NewOperationError("retry", NewCommandSkippedError("skipped", errors.New("condition"))),
			skipped: true, outcome: Skipped,
		},
		{
			name:      "direct operation",
			err:       NewOperationError("retry", errors.New("product")),
			retryable: true, warnable: true, outcome: Warned,
		},
		{
			name:      "direct assertion",
			err:       NewAssertionError("assertion", errors.New("product")),
			retryable: true, warnable: true, outcome: Warned,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.err.Error()
			if got := IsRetryable(test.err); got != test.retryable {
				t.Fatalf("IsRetryable() = %t, want %t", got, test.retryable)
			}
			if got := CanWarnWhenOptional(test.err); got != test.warnable {
				t.Fatalf("CanWarnWhenOptional() = %t, want %t", got, test.warnable)
			}
			if got := IsCommandSkipped(test.err); got != test.skipped {
				t.Fatalf("IsCommandSkipped() = %t, want %t", got, test.skipped)
			}
			outcome := ClassifyOutcome(test.err, true)
			if outcome != test.outcome {
				t.Fatalf("ClassifyOutcome() = %q, want %q", outcome, test.outcome)
			}
			surfaced := surfacedExecutionError(outcome, test.err)
			if test.surfaced {
				if surfaced != test.err {
					t.Fatalf("surfacedExecutionError() = %T %v, want original chain", surfaced, surfaced)
				}
			} else if surfaced != nil {
				t.Fatalf("surfacedExecutionError() = %v, want nil", surfaced)
			}
			if after := test.err.Error(); after != before {
				t.Fatalf("classification mutated diagnostics: before %q after %q", before, after)
			}
		})
	}
}

func TestHardTerminalErrorsDominateSkippedAcrossWrappersAndJoins(t *testing.T) {
	t.Parallel()

	type hardErrorCase struct {
		name    string
		leaf    func() error
		wrap    func(error) error
		outcome Outcome
	}
	hardErrors := []hardErrorCase{
		{
			name: "cancellation",
			leaf: func() error { return context.Canceled },
			wrap: func(cause error) error {
				return &terminalControlTestError{control: context.Canceled, cause: cause}
			},
			outcome: Cancelled,
		},
		{
			name: "deadline",
			leaf: func() error { return context.DeadlineExceeded },
			wrap: func(cause error) error {
				return &terminalControlTestError{control: context.DeadlineExceeded, cause: cause}
			},
			outcome: Cancelled,
		},
		{
			name:    "device connection",
			leaf:    func() error { return NewDeviceConnectionError("device", errors.New("transport")) },
			wrap:    func(cause error) error { return NewDeviceConnectionError("device", cause) },
			outcome: Failed,
		},
		{
			name:    "configuration",
			leaf:    func() error { return NewConfigurationError("configuration", errors.New("invalid")) },
			wrap:    func(cause error) error { return NewConfigurationError("configuration", cause) },
			outcome: Failed,
		},
	}

	for _, hard := range hardErrors {
		hard := hard
		t.Run(hard.name, func(t *testing.T) {
			skipped := func() error { return NewCommandSkippedError("skipped", nil) }
			tests := []struct {
				name string
				err  error
			}{
				{name: "skipped wraps hard", err: NewCommandSkippedError("skipped", hard.leaf())},
				{name: "hard wraps skipped", err: hard.wrap(skipped())},
				{name: "join skipped first", err: errors.Join(skipped(), hard.leaf())},
				{name: "join hard first", err: errors.Join(hard.leaf(), skipped())},
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					assertErrorDecision(t, test.err, errorDecisionExpectation{
						outcome: hard.outcome, surfaced: true,
					})
					if normalized := normalizeTerminalError("test operation", test.err); normalized != test.err {
						t.Fatalf("normalizeTerminalError() = %T %v, want original error", normalized, normalized)
					}
				})
			}
		})
	}
}

func TestAggregateClassificationControlsAndRawBranchFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want errorDecisionExpectation
	}{
		{
			name: "pure skipped",
			err:  NewCommandSkippedError("skipped", nil),
			want: errorDecisionExpectation{skipped: true, outcome: Skipped},
		},
		{
			name: "pure retryable product error",
			err:  NewOperationError("product", nil),
			want: errorDecisionExpectation{retryable: true, warnable: true, outcome: Warned},
		},
		{
			name: "joined retryable product errors",
			err: errors.Join(
				NewOperationError("product", errors.New("lookup")),
				NewAssertionError("assertion", errors.New("not visible")),
			),
			want: errorDecisionExpectation{retryable: true, warnable: true, outcome: Warned},
		},
		{
			name: "causal operation raw",
			err:  NewOperationError("product", errors.New("raw driver failure")),
			want: errorDecisionExpectation{retryable: true, warnable: true, outcome: Warned},
		},
		{
			name: "independent raw aggregate branch fails closed",
			err: errors.Join(
				NewOperationError("product", errors.New("lookup")),
				errors.New("independent raw failure"),
			),
			want: errorDecisionExpectation{outcome: Failed, surfaced: true},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertErrorDecision(t, test.err, test.want)
			if normalized := normalizeTerminalError("test operation", test.err); normalized != test.err {
				t.Fatalf("normalizeTerminalError() = %T %v, want original error", normalized, normalized)
			}
		})
	}
}

func TestTypedNilErrorGraphsFailClosed(t *testing.T) {
	t.Parallel()

	var configuration *ConfigurationError
	var operation *OperationError
	var assertion *AssertionError
	var connection *DeviceConnectionError
	var skipped *CommandSkippedError
	var causal *graphCausalError
	var aggregate *graphAggregateError

	tests := []struct {
		name string
		err  error
	}{
		{name: "configuration", err: configuration},
		{name: "operation", err: operation},
		{name: "assertion", err: assertion},
		{name: "device connection", err: connection},
		{name: "command skipped", err: skipped},
		{name: "custom causal", err: causal},
		{name: "custom aggregate", err: aggregate},
		{name: "nested under operation", err: NewOperationError("outer", connection)},
		{name: "nested under skipped", err: NewCommandSkippedError("outer", operation)},
		{name: "inside join", err: errors.Join(NewOperationError("valid", nil), assertion)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertMalformedErrorGraph(t, test.err)
		})
	}
}

func TestCyclicAndPanickingErrorGraphsFailClosed(t *testing.T) {
	t.Parallel()

	causalCycle := &graphCausalError{}
	causalCycle.cause = causalCycle
	aggregateCycle := &graphAggregateError{}
	aggregateCycle.branches = []error{aggregateCycle}

	tests := []struct {
		name string
		err  error
	}{
		{name: "self causal cycle", err: causalCycle},
		{name: "self aggregate cycle", err: aggregateCycle},
		{name: "panicking Is", err: &panickingIsError{}},
		{name: "panicking As", err: &panickingAsError{}},
		{name: "panicking causal unwrap", err: &graphCausalError{panicUnwrap: true}},
		{name: "panicking aggregate unwrap", err: &graphAggregateError{panicUnwrap: true}},
		{
			name: "invalid nested under operation",
			err:  NewOperationError("outer", &panickingAsError{}),
		},
		{
			name: "invalid nested under skipped",
			err:  NewCommandSkippedError("outer", &panickingIsError{}),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertMalformedErrorGraph(t, test.err)
		})
	}
}

func TestPathologicalOpaqueErrorDepthFailsClosed(t *testing.T) {
	t.Parallel()

	var err error = errors.New("leaf")
	for range 512 {
		err = opaqueCausalError{err}
	}
	assertMalformedErrorGraph(t, err)
}

func TestIdentitySafeStructWithOpaquePayloadTraversesSafely(t *testing.T) {
	t.Parallel()

	err := interfacePayloadCausalError{
		payload: []string{"non-identitySafe"},
		cause:   NewOperationError("product", errors.New("lookup")),
	}
	assertErrorDecision(t, err, errorDecisionExpectation{
		retryable: true, warnable: true, outcome: Warned,
	})
	if normalized := normalizeTerminalError("test operation", err); normalized == nil {
		t.Fatal("normalizeTerminalError() = nil, want valid causal wrapper")
	} else if _, ok := normalized.(interfacePayloadCausalError); !ok {
		t.Fatalf("normalizeTerminalError() type = %T, want interfacePayloadCausalError", normalized)
	}
}

func TestNodeLocalAsClassificationPreservesTaxonomy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *asOnlyError
		want errorDecisionExpectation
	}{
		{
			name: "device connection",
			err:  &asOnlyError{target: asDeviceConnection},
			want: errorDecisionExpectation{outcome: Failed, surfaced: true},
		},
		{
			name: "configuration",
			err:  &asOnlyError{target: asConfiguration},
			want: errorDecisionExpectation{outcome: Failed, surfaced: true},
		},
		{
			name: "command skipped",
			err:  &asOnlyError{target: asCommandSkipped},
			want: errorDecisionExpectation{skipped: true, outcome: Skipped},
		},
		{
			name: "operation",
			err:  &asOnlyError{target: asFlowBaton},
			want: errorDecisionExpectation{retryable: true, warnable: true, outcome: Warned},
		},
		{
			name: "assertion",
			err:  &asOnlyError{target: asAssertion},
			want: errorDecisionExpectation{retryable: true, warnable: true, outcome: Warned},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertErrorDecision(t, test.err, test.want)
			if normalized := normalizeTerminalError("test operation", test.err); normalized != test.err {
				t.Fatalf("normalizeTerminalError() type = %T, want original As-only error", normalized)
			}
		})
	}
}

func TestAsOnlyDeviceDominatesCausalWrappersAndAggregateBranches(t *testing.T) {
	t.Parallel()

	direct := func() error { return &asOnlyError{target: asDeviceConnection} }
	tests := []struct {
		name string
		err  error
	}{
		{name: "operation wrapper", err: NewOperationError("retry", direct())},
		{name: "skipped wrapper", err: NewCommandSkippedError("skipped", direct())},
		{name: "transparent wrapper", err: fmt.Errorf("driver: %w", direct())},
		{name: "join skipped first", err: errors.Join(NewCommandSkippedError("skipped", nil), direct())},
		{name: "join device first", err: errors.Join(direct(), NewCommandSkippedError("skipped", nil))},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertErrorDecision(t, test.err, errorDecisionExpectation{outcome: Failed, surfaced: true})
			if normalized := normalizeTerminalError("test operation", test.err); normalized != test.err {
				t.Fatalf("normalizeTerminalError() type = %T, want original graph", normalized)
			}
		})
	}
}

type errorDecisionExpectation struct {
	retryable bool
	warnable  bool
	skipped   bool
	outcome   Outcome
	surfaced  bool
}

func assertErrorDecision(t *testing.T, err error, want errorDecisionExpectation) {
	t.Helper()

	if got := IsRetryable(err); got != want.retryable {
		t.Fatalf("IsRetryable() = %t, want %t", got, want.retryable)
	}
	if got := CanWarnWhenOptional(err); got != want.warnable {
		t.Fatalf("CanWarnWhenOptional() = %t, want %t", got, want.warnable)
	}
	if got := IsCommandSkipped(err); got != want.skipped {
		t.Fatalf("IsCommandSkipped() = %t, want %t", got, want.skipped)
	}
	outcome := ClassifyOutcome(err, true)
	if outcome != want.outcome {
		t.Fatalf("ClassifyOutcome() = %q, want %q", outcome, want.outcome)
	}
	surfaced := surfacedExecutionError(outcome, err)
	if want.surfaced {
		if surfaced != err {
			t.Fatalf("surfacedExecutionError() = %T %v, want original error", surfaced, surfaced)
		}
	} else if surfaced != nil {
		t.Fatalf("surfacedExecutionError() = %v, want nil", surfaced)
	}
}

func assertMalformedErrorGraph(t *testing.T, err error) {
	t.Helper()

	if IsRetryable(err) {
		t.Fatal("IsRetryable() = true, want false")
	}
	if CanWarnWhenOptional(err) {
		t.Fatal("CanWarnWhenOptional() = true, want false")
	}
	if IsCommandSkipped(err) {
		t.Fatal("IsCommandSkipped() = true, want false")
	}
	if outcome := ClassifyOutcome(err, true); outcome != Failed {
		t.Fatalf("ClassifyOutcome() = %q, want %q", outcome, Failed)
	}
	normalized := normalizeTerminalError("test operation", err)
	assertSafeConfigurationError(t, normalized)
	if again := normalizeTerminalError("test operation", normalized); again != normalized {
		t.Fatalf("normalizing safe diagnostic returned %T, want identical ConfigurationError", again)
	}
}

func assertSafeConfigurationError(t *testing.T, err error) *ConfigurationError {
	t.Helper()

	configuration, ok := err.(*ConfigurationError)
	if !ok || configuration == nil {
		t.Fatalf("safe diagnostic type = %T, want non-nil *ConfigurationError", err)
	}
	if configuration.Error() == "" {
		t.Fatal("safe diagnostic Error() is empty")
	}
	if cause := configuration.Unwrap(); cause != nil {
		t.Fatalf("safe diagnostic retained unsafe cause type %T", cause)
	}
	if IsRetryable(configuration) || CanWarnWhenOptional(configuration) || IsCommandSkipped(configuration) {
		t.Fatal("safe ConfigurationError was classified as retryable, warnable, or skipped")
	}
	if outcome := ClassifyOutcome(configuration, true); outcome != Failed {
		t.Fatalf("safe ConfigurationError outcome = %q, want %q", outcome, Failed)
	}
	return configuration
}

type terminalControlTestError struct {
	control error
	cause   error
}

func (err *terminalControlTestError) Error() string {
	return fmt.Sprintf("%v: %v", err.control, err.cause)
}

func (err *terminalControlTestError) Is(target error) bool {
	return target == err.control
}

func (err *terminalControlTestError) Unwrap() error {
	return err.cause
}

type graphCausalError struct {
	cause       error
	panicUnwrap bool
}

func (err *graphCausalError) Error() string { return "custom causal graph error" }

func (err *graphCausalError) Unwrap() error {
	if err.panicUnwrap {
		panic("causal unwrap panic")
	}
	return err.cause
}

type graphAggregateError struct {
	branches    []error
	panicUnwrap bool
}

func (err *graphAggregateError) Error() string { return "custom aggregate graph error" }

func (err *graphAggregateError) Unwrap() []error {
	if err.panicUnwrap {
		panic("aggregate unwrap panic")
	}
	return err.branches
}

type opaqueCausalError []error

func (err opaqueCausalError) Error() string { return "non-identitySafe causal graph error" }

func (err opaqueCausalError) Unwrap() error {
	if len(err) == 0 {
		return nil
	}
	return err[0]
}

type interfacePayloadCausalError struct {
	payload any
	cause   error
}

func (err interfacePayloadCausalError) Error() string { return "interface-payload causal error" }

func (err interfacePayloadCausalError) Unwrap() error { return err.cause }

type panickingIsError struct{}

func (err *panickingIsError) Error() string { return "panicking Is error" }

func (err *panickingIsError) Is(error) bool { panic("Is panic") }

type panickingAsError struct{}

func (err *panickingAsError) Error() string { return "panicking As error" }

func (err *panickingAsError) As(any) bool { panic("As panic") }

type asOnlyTarget uint8

const (
	asDeviceConnection asOnlyTarget = iota
	asConfiguration
	asCommandSkipped
	asFlowBaton
	asAssertion
)

type asOnlyError struct {
	target asOnlyTarget
}

func (err *asOnlyError) Error() string { return "As-only taxonomy error" }

func (err *asOnlyError) As(target any) bool {
	switch err.target {
	case asDeviceConnection:
		matched, ok := target.(**DeviceConnectionError)
		if ok {
			*matched = NewDeviceConnectionError("matched device", nil)
		}
		return ok
	case asConfiguration:
		matched, ok := target.(**ConfigurationError)
		if ok {
			*matched = NewConfigurationError("matched configuration", nil)
		}
		return ok
	case asCommandSkipped:
		matched, ok := target.(**CommandSkippedError)
		if ok {
			*matched = NewCommandSkippedError("matched skipped", nil)
		}
		return ok
	case asFlowBaton:
		matched, ok := target.(**OperationError)
		if ok {
			*matched = NewOperationError("matched operation", nil)
		}
		return ok
	case asAssertion:
		matched, ok := target.(**AssertionError)
		if ok {
			*matched = NewAssertionError("matched assertion", nil)
		}
		return ok
	default:
		return false
	}
}

func TestErrorTaxonomyClassifiesRetryAndOptionalFailures(t *testing.T) {
	t.Parallel()

	cause := errors.New("root cause")
	tests := []struct {
		name       string
		err        error
		retryable  bool
		optional   bool
		skipped    bool
		assertType func(*testing.T, error)
	}{
		{
			name: "configuration", err: NewConfigurationError("invalid retry count", cause),
			assertType: func(t *testing.T, err error) {
				var target *ConfigurationError
				if !errors.As(err, &target) {
					t.Fatalf("error type = %T, want *ConfigurationError", err)
				}
			},
		},
		{
			name: "operation", err: NewOperationError("element missing", cause), retryable: true, optional: true,
			assertType: func(t *testing.T, err error) {
				var target *OperationError
				if !errors.As(err, &target) {
					t.Fatalf("error type = %T, want *OperationError", err)
				}
			},
		},
		{
			name: "wrapped assertion", err: fmt.Errorf("dispatch: %w", NewAssertionError("not visible", cause)), retryable: true, optional: true,
			assertType: func(t *testing.T, err error) {
				var target *AssertionError
				if !errors.As(err, &target) {
					t.Fatalf("error type = %T, want *AssertionError", err)
				}
			},
		},
		{
			name: "device connection", err: NewDeviceConnectionError("transport closed", cause),
			assertType: func(t *testing.T, err error) {
				var target *DeviceConnectionError
				if !errors.As(err, &target) {
					t.Fatalf("error type = %T, want *DeviceConnectionError", err)
				}
			},
		},
		{
			name: "command skipped", err: NewCommandSkippedError("condition false", cause), skipped: true,
			assertType: func(t *testing.T, err error) {
				var target *CommandSkippedError
				if !errors.As(err, &target) {
					t.Fatalf("error type = %T, want *CommandSkippedError", err)
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRetryable(test.err); got != test.retryable {
				t.Fatalf("IsRetryable = %v, want %v", got, test.retryable)
			}
			if got := CanWarnWhenOptional(test.err); got != test.optional {
				t.Fatalf("CanWarnWhenOptional = %v, want %v", got, test.optional)
			}
			if got := IsCommandSkipped(test.err); got != test.skipped {
				t.Fatalf("IsCommandSkipped = %v, want %v", got, test.skipped)
			}
			if !errors.Is(test.err, cause) {
				t.Fatalf("error %v does not unwrap to cause", test.err)
			}
			test.assertType(t, test.err)
		})
	}
}

func TestTypedErrorWithoutCauseUsesOnlyItsMessage(t *testing.T) {
	t.Parallel()

	err := NewOperationError("element missing", nil)
	if got, want := err.Error(), "element missing"; got != want {
		t.Fatalf("Error = %q, want %q", got, want)
	}
}
