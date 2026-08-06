package engine

import (
	"context"
	"reflect"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

// WaitForHierarchySettle confirms settling only after two equal, non-loading
// hierarchy samples. Nil samples are inconclusive and never count as settled.
// An explicit timeout governs deadline polling; an omitted timeout performs
// exactly ten possible 200ms polls.
func (lookup *ElementLookup) WaitForHierarchySettle(ctx context.Context, request device.SettleRequest) (*device.ViewHierarchy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	initial := cloneSettleHierarchy(request.InitialHierarchy)
	request.InitialHierarchy = initial
	state := hierarchySettleState{
		previous: initial,
		latest:   initial,
	}

	if request.TimeoutMillis == nil {
		for range HierarchySettleAttempts {
			settled, err := lookup.pollHierarchySettle(ctx, request, HierarchySettlePollInterval, &state)
			if err != nil {
				return state.latest, err
			}
			if settled {
				return state.latest, nil
			}
		}
		return state.latest, NewOperationError("hierarchy did not settle after 10 attempts", nil)
	}

	timeout := explicitSettleTimeout(*request.TimeoutMillis)
	if timeout == 0 {
		current, err := lookup.driver.WaitForAppToSettle(ctx, cloneSettleRequest(request))
		if err := ctx.Err(); err != nil {
			return state.latest, err
		}
		if err != nil {
			return state.latest, err
		}
		if state.observe(current) {
			return state.latest, nil
		}
		return state.latest, NewOperationError("hierarchy did not settle before timeout", nil)
	}

	deadline := lookup.clock.Now().Add(timeout)
	for lookup.clock.Now().Before(deadline) {
		now := lookup.clock.Now()
		settled, err := lookup.pollHierarchySettle(ctx, request, minDuration(HierarchySettlePollInterval, deadline.Sub(now)), &state)
		if err != nil {
			return state.latest, err
		}
		if settled {
			return state.latest, nil
		}
	}
	return state.latest, NewOperationError("hierarchy did not settle before timeout", nil)
}

type hierarchySettleState struct {
	previous *device.ViewHierarchy
	latest   *device.ViewHierarchy
}

func (lookup *ElementLookup) pollHierarchySettle(
	ctx context.Context,
	request device.SettleRequest,
	delay time.Duration,
	state *hierarchySettleState,
) (bool, error) {
	if err := lookup.clock.Wait(ctx, delay); err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return false, cancellation
		}
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	current, err := lookup.driver.WaitForAppToSettle(ctx, cloneSettleRequest(request))
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err != nil {
		return false, err
	}
	return state.observe(current), nil
}

func (state *hierarchySettleState) observe(current *device.ViewHierarchy) bool {
	owned := cloneSettleHierarchy(current)
	settled := hierarchySamplesSettled(state.previous, owned)
	state.previous = owned
	if owned != nil {
		state.latest = owned
	}
	return settled
}

func hierarchySamplesSettled(previous, current *device.ViewHierarchy) bool {
	return previous != nil && current != nil &&
		current.Root.Attributes["is-loading"] != "true" &&
		reflect.DeepEqual(previous, current)
}

func explicitSettleTimeout(milliseconds int64) time.Duration {
	if milliseconds <= 0 {
		return 0
	}
	if milliseconds >= MaximumSettleTimeout.Milliseconds() {
		return MaximumSettleTimeout
	}
	return time.Duration(milliseconds) * time.Millisecond
}

func cloneSettleRequest(request device.SettleRequest) device.SettleRequest {
	request.TimeoutMillis = clonePointer(request.TimeoutMillis)
	request.InitialHierarchy = cloneSettleHierarchy(request.InitialHierarchy)
	return request
}

func cloneSettleHierarchy(hierarchy *device.ViewHierarchy) *device.ViewHierarchy {
	if hierarchy == nil {
		return nil
	}
	return &device.ViewHierarchy{Root: cloneSettleTreeNode(hierarchy.Root)}
}

func cloneSettleTreeNode(node device.TreeNode) device.TreeNode {
	cloned := node
	if node.Attributes != nil {
		cloned.Attributes = make(map[string]string, len(node.Attributes))
		for key, value := range node.Attributes {
			cloned.Attributes[key] = value
		}
	}
	cloned.Clickable = clonePointer(node.Clickable)
	cloned.Enabled = clonePointer(node.Enabled)
	cloned.Focused = clonePointer(node.Focused)
	cloned.Checked = clonePointer(node.Checked)
	cloned.Selected = clonePointer(node.Selected)
	if node.Children != nil {
		cloned.Children = make([]device.TreeNode, len(node.Children))
		for index := range node.Children {
			cloned.Children[index] = cloneSettleTreeNode(node.Children[index])
		}
	}
	return cloned
}
