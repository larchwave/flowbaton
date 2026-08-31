package ios

import (
	"errors"
	"fmt"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// The explore session decides whether to keep testing after a failed
// observation by asking the error itself, because internal/explore may not
// import a platform package. That only works while the runner's error still
// satisfies the neutral contract through the wrapping the observer adds.
func TestRunnerErrorAnswersTheNeutralRetryabilityContract(t *testing.T) {
	var target device.RetryableError
	wrapped := fmt.Errorf("research: hierarchy: %w", &Error{
		Code:    CodePrecondition,
		Message: "none of com.example is in the foreground",
		Status:  400,
	})
	if !errors.As(wrapped, &target) {
		t.Fatalf("errors.As(%v) found no device.RetryableError", wrapped)
	}
	if target.Retryable() {
		t.Errorf("a precondition failure reported itself retryable")
	}

	target = nil
	internal := fmt.Errorf("research: hierarchy: %w", &Error{Code: CodeInternal, Status: 500})
	if !errors.As(internal, &target) || !target.Retryable() {
		t.Errorf("an internal failure did not report itself retryable")
	}
}
