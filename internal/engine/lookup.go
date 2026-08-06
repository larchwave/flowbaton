package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
	"github.com/nohavewho/flowbaton/internal/matching"
	"github.com/nohavewho/flowbaton/internal/model"
)

// lookupPollInterval is the host retry cadence for an ordinary timed lookup.
// The externally specified wait commands use their dedicated public cadences.
const lookupPollInterval = 100 * time.Millisecond

// LookupOptions controls a selector lookup. A nil Timeout uses the adjusted
// required or optional default. Optional absence is represented explicitly as
// a nil element and nil error; required absence is a OperationError.
type LookupOptions struct {
	Optional bool
	Timeout  *time.Duration
}

// ElementLookup owns host-side hierarchy acquisition and selector lookup.
// Device information is cached after the first successful read because the
// viewport is stable for the lifetime of a driver session.
type ElementLookup struct {
	driver device.Driver
	clock  Clock

	interactionMu     sync.RWMutex
	latestInteraction time.Time
	hasInteraction    bool

	deviceInfoMu  sync.Mutex
	deviceInfo    device.DeviceInfo
	hasDeviceInfo bool

	// activeAppMu guards activeAppID, the app whose screen a lookup is about.
	activeAppMu sync.RWMutex
	activeAppID string
}

// SetActiveApp names the app whose hierarchy lookups should read, and returns
// what was set before so a caller can restore it.
//
// Drivers use the active app ID to scope hierarchy requests. Returning the
// previous value rather than exposing a stack keeps ownership
// with the flow scope that already pushes and pops the environment: one place
// that knows a flow was entered also knows it was left.
func (lookup *ElementLookup) SetActiveApp(appID string) string {
	lookup.activeAppMu.Lock()
	defer lookup.activeAppMu.Unlock()
	previous := lookup.activeAppID
	lookup.activeAppID = appID
	return previous
}

// activeAppIDs is the filter to put on a hierarchy request. Empty when no app is
// known, because naming an app that is not running makes a driver refuse,
// while naming none is a real question with a real answer.
func (lookup *ElementLookup) activeAppIDs() []string {
	lookup.activeAppMu.RLock()
	defer lookup.activeAppMu.RUnlock()
	if lookup.activeAppID == "" {
		return nil
	}
	return []string{lookup.activeAppID}
}

// NewElementLookup constructs deterministic host-side lookup primitives.
func NewElementLookup(driver device.Driver, clock Clock) *ElementLookup {
	return &ElementLookup{driver: driver, clock: clock}
}

// RecordInteraction records the latest accepted interaction instant.
// Timestamps before the watermark cannot move it backwards.
func (lookup *ElementLookup) RecordInteraction(at time.Time) {
	lookup.interactionMu.Lock()
	defer lookup.interactionMu.Unlock()
	if !lookup.hasInteraction || at.After(lookup.latestInteraction) {
		lookup.latestInteraction = at
		lookup.hasInteraction = true
	}
}

// AdjustedTimeout returns an explicit timeout unchanged (apart from flooring
// negatives at zero), or the required/optional default minus elapsed time
// since the latest interaction. The result is always non-negative.
func (lookup *ElementLookup) AdjustedTimeout(options LookupOptions) time.Duration {
	if options.Timeout != nil {
		return nonNegativeDuration(*options.Timeout)
	}
	base := LookupTimeout
	if options.Optional {
		base = OptionalLookupTimeout
	}

	lookup.interactionMu.RLock()
	latest := lookup.latestInteraction
	hasInteraction := lookup.hasInteraction
	lookup.interactionMu.RUnlock()
	if !hasInteraction {
		return base
	}
	elapsed := lookup.clock.Now().Sub(latest)
	if elapsed <= 0 {
		return base
	}
	return nonNegativeDuration(base - elapsed)
}

// adjustedDeadline anchors the adjusted lookup budget to one clock reading.
// Internal composite waits reuse this absolute deadline instead of converting
// it back into durations that can be re-anchored by downstream primitives.
func (lookup *ElementLookup) adjustedDeadline(options LookupOptions) time.Time {
	now := lookup.clock.Now()
	if options.Timeout != nil {
		return now.Add(nonNegativeDuration(*options.Timeout))
	}
	base := LookupTimeout
	if options.Optional {
		base = OptionalLookupTimeout
	}

	lookup.interactionMu.RLock()
	latest := lookup.latestInteraction
	hasInteraction := lookup.hasInteraction
	lookup.interactionMu.RUnlock()
	if !hasInteraction || !now.After(latest) {
		return now.Add(base)
	}
	deadline := latest.Add(base)
	if deadline.Before(now) {
		return now
	}
	return deadline
}

// Find polls normalized visible hierarchies until the selector matches or the
// adjusted deadline is reached. Matching delegates to the frozen exact
// matching package without altering selector semantics.
func (lookup *ElementLookup) Find(ctx context.Context, selector model.ElementSelector, options LookupOptions) (*hierarchy.Element, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return lookup.findUntil(ctx, selector, options, lookup.adjustedDeadline(options))
}

func (lookup *ElementLookup) findUntil(
	ctx context.Context,
	selector model.ElementSelector,
	options LookupOptions,
	deadline time.Time,
) (*hierarchy.Element, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
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

		now := lookup.clock.Now()
		if !now.Before(deadline) {
			if options.Optional {
				return nil, nil
			}
			return nil, NewOperationError("element not found", nil)
		}
		if err := lookup.clock.Wait(ctx, minDuration(lookupPollInterval, deadline.Sub(now))); err != nil {
			if cancellation := ctx.Err(); cancellation != nil {
				return nil, cancellation
			}
			return nil, err
		}
		if cancellation := ctx.Err(); cancellation != nil {
			return nil, cancellation
		}
	}
}

// cssSelectorFeature is the capability a driver advertises when it can resolve
// a css query; cssPathAttribute is the hierarchy attribute that carries the
// path a resolved node maps back to.
const (
	cssSelectorFeature = "cssSelector"
	cssPathAttribute   = "css"
)

func (lookup *ElementLookup) findOnce(ctx context.Context, selector model.ElementSelector) (*hierarchy.Element, error) {
	root, err := lookup.visibleHierarchy(ctx)
	if err != nil {
		return nil, err
	}
	// A css selector is a query, not a value, so it cannot be matched against a
	// captured attribute: the driver resolves it in the page and the lookup
	// keeps only the nodes it named. Resolution happens before matching so the
	// rest of the selector still applies — css composes with text, index and
	// the relational fields instead of short-circuiting them.
	allowed, err := lookup.resolveCSS(ctx, selector)
	if err != nil {
		return nil, err
	}
	if selector.CSS != nil {
		if len(allowed) == 0 {
			return nil, nil
		}
		// The matcher has no CSS rule; the node filter enforces the query's result.
		selector.CSS = nil
	}
	matches, err := matching.Find(root, selector)
	if err != nil {
		return nil, err
	}
	if allowed != nil {
		matches = filterByCSSPath(matches, allowed)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	return matches[0], nil
}

// resolveCSS asks the driver which nodes a css expression selects and returns
// their paths. It returns nil when the selector carries no css at all, which is
// what tells the caller not to filter.
//
// A driver that cannot resolve css fails the lookup rather than ignoring the
// field: silently dropping it would match the first node with the same text on
// any platform, which is a wrong element rather than a missing one.
func (lookup *ElementLookup) resolveCSS(
	ctx context.Context, selector model.ElementSelector,
) (map[string]struct{}, error) {
	if selector.CSS == nil {
		return nil, nil
	}
	if !lookup.driver.Capabilities().Features[cssSelectorFeature] {
		return nil, NewConfigurationError(fmt.Sprintf(
			"selector css is not supported by the %s driver", lookup.driver.Name()), nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	nodes, err := lookup.driver.QueryOnDeviceElements(
		ctx, device.QueryRequest{Expression: *selector.CSS, AppIDs: lookup.activeAppIDs()})
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		if path := node.Attributes[cssPathAttribute]; path != "" {
			paths[path] = struct{}{}
		}
	}
	return paths, nil
}

// filterByCSSPath keeps the matches the driver's query named, in match order.
func filterByCSSPath(matches []*hierarchy.Element, allowed map[string]struct{}) []*hierarchy.Element {
	kept := make([]*hierarchy.Element, 0, len(matches))
	for _, match := range matches {
		if _, exists := allowed[match.Node.Attributes[cssPathAttribute]]; exists {
			kept = append(kept, match)
		}
	}
	return kept
}

func (lookup *ElementLookup) visibleHierarchy(ctx context.Context) (*hierarchy.Element, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptor, err := lookup.driver.ContentDescriptor(
		ctx, device.ContentDescriptorRequest{AppIDs: lookup.activeAppIDs()})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	normalized, err := hierarchy.New(descriptor)
	if err != nil {
		return nil, err
	}
	viewport := device.Bounds{Width: info.WidthGrid, Height: info.HeightGrid}
	return hierarchy.FilterVisible(normalized, viewport), nil
}

func (lookup *ElementLookup) cachedDeviceInfo(ctx context.Context) (device.DeviceInfo, error) {
	lookup.deviceInfoMu.Lock()
	defer lookup.deviceInfoMu.Unlock()
	if err := ctx.Err(); err != nil {
		return device.DeviceInfo{}, err
	}
	if lookup.hasDeviceInfo {
		return lookup.deviceInfo, nil
	}
	info, err := lookup.driver.DeviceInfo(ctx)
	if err := ctx.Err(); err != nil {
		return device.DeviceInfo{}, err
	}
	if err != nil {
		return device.DeviceInfo{}, err
	}
	lookup.deviceInfo = info
	lookup.hasDeviceInfo = true
	return info, nil
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
}
