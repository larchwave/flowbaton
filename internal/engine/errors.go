package engine

import (
	"context"
	"fmt"
	"reflect"
)

const maxTerminalErrorTraversalDepth = 128

type terminalErrorClass uint8

const (
	terminalErrorNone terminalErrorClass = iota
	terminalErrorSkipped
	terminalErrorCancelled
	terminalErrorDeviceConnection
	terminalErrorConfiguration
	terminalErrorRetryable
	terminalErrorFailure
)

type terminalErrorClassification struct {
	class             terminalErrorClass
	containsAggregate bool
	invalid           bool
}

type causalError interface {
	Unwrap() error
}

type aggregateError interface {
	Unwrap() []error
}

type nodeErrorMatcher interface {
	Is(error) bool
}

type nodeErrorCaster interface {
	As(any) bool
}

type terminalErrorIdentity struct {
	errorType reflect.Type
	pointer   uintptr
}

type terminalErrorTraversal struct {
	active map[terminalErrorIdentity]struct{}
}

// classifyTerminalError distinguishes causal wrappers from independent
// aggregate branches before applying their shared terminal decision.
func classifyTerminalError(err error) terminalErrorClass {
	return inspectTerminalError(err).class
}

func inspectTerminalError(err error) terminalErrorClassification {
	traversal := terminalErrorTraversal{active: make(map[terminalErrorIdentity]struct{})}
	return traversal.inspect(err, 0)
}

func (traversal *terminalErrorTraversal) inspect(err error, depth int) terminalErrorClassification {
	if err == nil {
		return terminalErrorClassification{class: terminalErrorNone}
	}
	if depth >= maxTerminalErrorTraversalDepth || isNilError(err) {
		return invalidTerminalErrorClassification(false)
	}

	if identity, track := pointerErrorIdentity(err); track {
		if _, exists := traversal.active[identity]; exists {
			return invalidTerminalErrorClassification(false)
		}
		traversal.active[identity] = struct{}{}
		defer delete(traversal.active, identity)
	}

	direct, invalid := classifyDirectTerminalError(err)
	if invalid {
		return invalidTerminalErrorClassification(false)
	}
	if aggregate, ok := err.(aggregateError); ok {
		classification := terminalErrorClassification{
			class:             direct,
			containsAggregate: true,
		}
		branches, unwrapInvalid := safelyUnwrapAggregate(aggregate)
		if unwrapInvalid {
			return invalidTerminalErrorClassification(true)
		}
		for _, branch := range branches {
			branchClassification := traversal.inspect(branch, depth+1)
			if branchClassification.invalid {
				return invalidTerminalErrorClassification(true)
			}
			classification.containsAggregate = classification.containsAggregate || branchClassification.containsAggregate
			classification.class = mergeAggregateTerminalClasses(classification.class, branchClassification.class)
		}
		if classification.class == terminalErrorNone {
			classification.class = terminalErrorFailure
		}
		return classification
	}

	if causal, ok := err.(causalError); ok {
		cause, unwrapInvalid := safelyUnwrapCausal(causal)
		if unwrapInvalid {
			return invalidTerminalErrorClassification(false)
		}
		if cause != nil {
			return mergeCausalTerminalClass(direct, traversal.inspect(cause, depth+1))
		}
	}
	if direct == terminalErrorNone {
		direct = terminalErrorFailure
	}
	return terminalErrorClassification{class: direct}
}

func invalidTerminalErrorClassification(containsAggregate bool) terminalErrorClassification {
	return terminalErrorClassification{
		class:             terminalErrorFailure,
		containsAggregate: containsAggregate,
		invalid:           true,
	}
}

func isNilError(err error) bool {
	value := reflect.ValueOf(err)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func pointerErrorIdentity(err error) (terminalErrorIdentity, bool) {
	value := reflect.ValueOf(err)
	if value.Kind() != reflect.Pointer {
		return terminalErrorIdentity{}, false
	}
	return terminalErrorIdentity{errorType: value.Type(), pointer: value.Pointer()}, true
}

func classifyDirectTerminalError(err error) (terminalErrorClass, bool) {
	matched, invalid := directlyMatches(err, context.Canceled)
	if invalid {
		return terminalErrorFailure, true
	}
	if !matched {
		matched, invalid = directlyMatches(err, context.DeadlineExceeded)
		if invalid {
			return terminalErrorFailure, true
		}
	}
	if matched {
		return terminalErrorCancelled, false
	}
	switch err.(type) {
	case *DeviceConnectionError:
		return terminalErrorDeviceConnection, false
	case *ConfigurationError:
		return terminalErrorConfiguration, false
	case *CommandSkippedError:
		return terminalErrorSkipped, false
	case *OperationError, *AssertionError:
		return terminalErrorRetryable, false
	}

	caster, ok := err.(nodeErrorCaster)
	if !ok {
		return terminalErrorNone, false
	}
	var connection *DeviceConnectionError
	if matched, invalid = safelyMatchesAs(caster, &connection); matched || invalid {
		return terminalErrorDeviceConnection, invalid
	}
	var configuration *ConfigurationError
	if matched, invalid = safelyMatchesAs(caster, &configuration); matched || invalid {
		return terminalErrorConfiguration, invalid
	}
	var skipped *CommandSkippedError
	if matched, invalid = safelyMatchesAs(caster, &skipped); matched || invalid {
		return terminalErrorSkipped, invalid
	}
	var operation *OperationError
	if matched, invalid = safelyMatchesAs(caster, &operation); matched || invalid {
		return terminalErrorRetryable, invalid
	}
	var assertion *AssertionError
	if matched, invalid = safelyMatchesAs(caster, &assertion); matched || invalid {
		return terminalErrorRetryable, invalid
	}
	return terminalErrorNone, false
}

func directlyMatches(err, target error) (matched bool, invalid bool) {
	defer func() {
		if recover() != nil {
			matched = false
			invalid = true
		}
	}()
	if isEqualitySafe(reflect.ValueOf(err)) && err == target {
		return true, false
	}
	matcher, ok := err.(nodeErrorMatcher)
	if !ok {
		return false, false
	}
	return matcher.Is(target), false
}

func isEqualitySafe(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Array:
		for index := 0; index < value.Len(); index++ {
			if !isEqualitySafe(value.Index(index)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			if !isEqualitySafe(value.Field(index)) {
				return false
			}
		}
		return true
	case reflect.Interface:
		if value.IsNil() {
			return true
		}
		return isEqualitySafe(value.Elem())
	case reflect.Bool,
		reflect.Chan,
		reflect.Complex64,
		reflect.Complex128,
		reflect.Float32,
		reflect.Float64,
		reflect.Int,
		reflect.Int8,
		reflect.Int16,
		reflect.Int32,
		reflect.Int64,
		reflect.Pointer,
		reflect.String,
		reflect.Uint,
		reflect.Uint8,
		reflect.Uint16,
		reflect.Uint32,
		reflect.Uint64,
		reflect.Uintptr,
		reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func safelyMatchesAs(caster nodeErrorCaster, target any) (matched bool, invalid bool) {
	defer func() {
		if recover() != nil {
			matched = false
			invalid = true
		}
	}()
	return caster.As(target), false
}

func safelyUnwrapCausal(causal causalError) (cause error, invalid bool) {
	defer func() {
		if recover() != nil {
			cause = nil
			invalid = true
		}
	}()
	return causal.Unwrap(), false
}

func safelyUnwrapAggregate(aggregate aggregateError) (branches []error, invalid bool) {
	defer func() {
		if recover() != nil {
			branches = nil
			invalid = true
		}
	}()
	return aggregate.Unwrap(), false
}

func mergeCausalTerminalClass(
	direct terminalErrorClass,
	cause terminalErrorClassification,
) terminalErrorClassification {
	if cause.invalid {
		return invalidTerminalErrorClassification(cause.containsAggregate)
	}
	classification := terminalErrorClassification{
		class:             direct,
		containsAggregate: cause.containsAggregate,
	}
	switch direct {
	case terminalErrorNone:
		classification.class = cause.class
	case terminalErrorRetryable:
		switch cause.class {
		case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
			classification.class = cause.class
		case terminalErrorFailure:
			if cause.containsAggregate {
				classification.class = terminalErrorFailure
			}
		case terminalErrorSkipped:
			classification.class = terminalErrorSkipped
		}
	case terminalErrorSkipped:
		switch cause.class {
		case terminalErrorCancelled, terminalErrorDeviceConnection, terminalErrorConfiguration:
			classification.class = cause.class
		case terminalErrorFailure:
			if cause.containsAggregate {
				classification.class = terminalErrorFailure
			}
		}
	default:
		classification.class = mergeAggregateTerminalClasses(direct, cause.class)
	}
	return classification
}

// Aggregate branches merge independently of argument order. Cancellation
// dominates failed branches; failed branches dominate retryable product
// failures; retryable failures dominate skipped control flow. Device and
// configuration classes stay distinct for causal inspection but both produce
// a Failed outcome.
func mergeAggregateTerminalClasses(left, right terminalErrorClass) terminalErrorClass {
	if terminalClassPrecedence(right) > terminalClassPrecedence(left) {
		return right
	}
	return left
}

func terminalClassPrecedence(class terminalErrorClass) uint8 {
	switch class {
	case terminalErrorCancelled:
		return 6
	case terminalErrorDeviceConnection:
		return 5
	case terminalErrorConfiguration:
		return 4
	case terminalErrorFailure:
		return 3
	case terminalErrorRetryable:
		return 2
	case terminalErrorSkipped:
		return 1
	default:
		return 0
	}
}

func normalizeTerminalError(operation string, err error) error {
	classification := inspectTerminalError(err)
	if classification.invalid {
		return malformedTerminalError(operation)
	}
	switch classification.class {
	case terminalErrorNone:
		return nil
	case terminalErrorFailure:
		if classification.containsAggregate {
			return err
		}
		return NewOperationError(operation, err)
	default:
		return err
	}
}

// sanitizeMalformedError preserves every valid error object exactly. Invalid
// graphs are the identity-preservation exception because retaining them would
// allow later formatting or traversal to panic again.
func sanitizeMalformedError(operation string, err error) error {
	sanitized, _ := sanitizeMalformedErrorWithStatus(operation, err)
	return sanitized
}

func sanitizeMalformedErrorWithStatus(operation string, err error) (error, bool) {
	if err == nil || !inspectTerminalError(err).invalid {
		return err, false
	}
	return malformedTerminalError(operation), true
}

func malformedTerminalError(operation string) *ConfigurationError {
	return NewConfigurationError(fmt.Sprintf("%s: malformed error graph", operation), nil)
}

type errorDetail struct {
	message string
	cause   error
}

func (e errorDetail) Error() string {
	if e.cause == nil {
		return e.message
	}
	return fmt.Sprintf("%s: %v", e.message, e.cause)
}

func (e errorDetail) Unwrap() error {
	return e.cause
}

// ConfigurationError identifies invalid engine input or configuration.
type ConfigurationError struct{ errorDetail }

func NewConfigurationError(message string, cause error) *ConfigurationError {
	return &ConfigurationError{errorDetail{message: message, cause: cause}}
}

// OperationError identifies a product operation failure eligible for command
// retry and optional-warning handling.
type OperationError struct{ errorDetail }

func NewOperationError(message string, cause error) *OperationError {
	return &OperationError{errorDetail{message: message, cause: cause}}
}

// AssertionError identifies a failed product assertion. Assertions share the
// operation retry and optional-warning classification but remain separately inspectable.
type AssertionError struct{ errorDetail }

func NewAssertionError(message string, cause error) *AssertionError {
	return &AssertionError{errorDetail{message: message, cause: cause}}
}

// DeviceConnectionError identifies a transport or device-session loss that
// must propagate rather than be retried or downgraded to an optional warning.
type DeviceConnectionError struct{ errorDetail }

func NewDeviceConnectionError(message string, cause error) *DeviceConnectionError {
	return &DeviceConnectionError{errorDetail{message: message, cause: cause}}
}

// CommandSkippedError is control flow for a command that intentionally did
// not execute.
type CommandSkippedError struct{ errorDetail }

func NewCommandSkippedError(message string, cause error) *CommandSkippedError {
	return &CommandSkippedError{errorDetail{message: message, cause: cause}}
}

// IsRetryable reports whether retry command semantics may retry the failure.
func IsRetryable(err error) bool {
	return classifyTerminalError(err) == terminalErrorRetryable
}

// CanWarnWhenOptional reports whether an optional command may convert the
// failure into a warned outcome.
func CanWarnWhenOptional(err error) bool {
	return classifyTerminalError(err) == terminalErrorRetryable
}

// IsCommandSkipped reports whether the error represents intentional control
// flow rather than a failed command.
func IsCommandSkipped(err error) bool {
	return classifyTerminalError(err) == terminalErrorSkipped
}
