package engine

import (
	"context"
	"errors"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/matching"
)

// ElementStabilityResult preserves the latest refreshable element and bounds
// even when the stability deadline expires. Stable is true only when equal
// consecutive bounds are observed before that deadline.
type ElementStabilityResult struct {
	Element *hierarchy.Element
	Bounds  device.Bounds
	Stable  bool
}

// WaitForElementStability refreshes the element by attributes (excluding
// bounds) every 100ms. Transient absence or ambiguity keeps the last-known
// element; driver and hierarchy failures still propagate.
func (lookup *ElementLookup) WaitForElementStability(ctx context.Context, previous *hierarchy.Element) (ElementStabilityResult, error) {
	if err := ctx.Err(); err != nil {
		return ElementStabilityResult{}, err
	}
	if previous == nil || !previous.HasBounds {
		return ElementStabilityResult{}, NewConfigurationError("element stability requires an element with bounds", nil)
	}

	deadline := lookup.clock.Now().Add(ElementStabilityTimeout)
	last := previous
	for lookup.clock.Now().Before(deadline) {
		now := lookup.clock.Now()
		if err := lookup.clock.Wait(ctx, minDuration(ElementStabilityPollInterval, deadline.Sub(now))); err != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				return ElementStabilityResult{}, cancellation
			}
			return ElementStabilityResult{}, err
		}
		if cancellation := ctx.Err(); cancellation != nil {
			return ElementStabilityResult{}, cancellation
		}

		root, err := lookup.visibleHierarchy(ctx)
		if err != nil {
			return ElementStabilityResult{}, err
		}
		refreshed, err := matching.Refresh(root, last)
		if err != nil {
			var refreshErr *matching.RefreshError
			if !errors.As(err, &refreshErr) {
				return ElementStabilityResult{}, err
			}
		} else if refreshed.HasBounds {
			isBeforeDeadline := lookup.clock.Now().Before(deadline)
			if isBeforeDeadline && refreshed.Bounds == last.Bounds {
				return ElementStabilityResult{Element: refreshed, Bounds: refreshed.Bounds, Stable: true}, nil
			}
			last = refreshed
		}
	}
	return ElementStabilityResult{Element: last, Bounds: last.Bounds, Stable: false}, nil
}
