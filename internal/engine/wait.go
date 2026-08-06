package engine

import (
	"context"
	"time"

	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/model"
)

// WaitUntilNotVisible checks immediately, then polls at the exact 500ms
// condition cadence through the deadline. A still-visible element at the
// deadline is a retryable OperationError.
func (lookup *ElementLookup) WaitUntilNotVisible(ctx context.Context, selector model.ElementSelector, timeout time.Duration) error {
	absent, err := lookup.conditionNotVisible(ctx, selector, timeout)
	if err != nil {
		return err
	}
	if !absent {
		return NewOperationError("element remained visible until deadline", nil)
	}
	return nil
}

// conditionNotVisible is the shared non-asserting condition primitive. It
// checks immediately, polls on the condition cadence, and reports exhaustion
// as false rather than converting control flow into a FlowBaton failure.
func (lookup *ElementLookup) conditionNotVisible(ctx context.Context, selector model.ElementSelector, timeout time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return lookup.conditionNotVisibleUntil(ctx, selector, lookup.clock.Now().Add(nonNegativeDuration(timeout)))
}

func (lookup *ElementLookup) conditionNotVisibleUntil(
	ctx context.Context,
	selector model.ElementSelector,
	deadline time.Time,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	for {
		element, err := lookup.findOnce(ctx, selector)
		if err != nil {
			return false, err
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if element == nil {
			return true, nil
		}

		now := lookup.clock.Now()
		if !now.Before(deadline) {
			return false, nil
		}
		if err := lookup.clock.Wait(ctx, minDuration(NotVisiblePollInterval, deadline.Sub(now))); err != nil {
			return false, err
		}
	}
}

// WaitUntilVisible performs exactly ten possible one-second waits followed by
// one hierarchy lookup per attempt. Optional exhaustion is a nil element and
// nil error; required exhaustion is a retryable OperationError.
func (lookup *ElementLookup) WaitUntilVisible(ctx context.Context, selector model.ElementSelector, optional bool) (*hierarchy.Element, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for range WaitUntilVisibleAttempts {
		if err := lookup.clock.Wait(ctx, WaitUntilVisiblePollInterval); err != nil {
			return nil, err
		}
		element, err := lookup.findOnce(ctx, selector)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if element != nil {
			return element, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if optional {
		return nil, nil
	}
	return nil, NewOperationError("element did not become visible after 10 attempts", nil)
}
