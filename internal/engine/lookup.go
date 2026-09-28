package engine

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/matching"
	"github.com/larchwave/flowbaton/internal/model"
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
	return hierarchy.FilterVisibleWithin(normalized, viewport, lookup.clips()), nil
}

// ForgetDeviceInfo drops the cached device grid so the next read measures the
// screen as it is now. DeviceInfo is cached because a lookup polls and the iOS
// runner answers the route by taking a screenshot, but the grid is the one
// field that changes under a running session, and visibleHierarchy prunes with
// it: after a rotation an element plainly on a landscape screen falls outside
// the remembered portrait width and reads as missing.
//
// The engine calls this when IT rotates the device. A rotation the app performs
// on its own is a known gap: nothing tells the session, and the cached grid
// stays wrong until the flow rotates or the session ends.
func (lookup *ElementLookup) ForgetDeviceInfo() {
	lookup.deviceInfoMu.Lock()
	defer lookup.deviceInfoMu.Unlock()
	lookup.hasDeviceInfo = false
	lookup.deviceInfo = device.DeviceInfo{}
}

// visibleCenter aims a gesture at the part of an element that is on screen.
// visibleHierarchy keeps anything 10% visible, so a row scrolled past an edge
// stays selectable while its geometric center sits off the device. Scroll
// distance is the deliberate exception: scrollUntilVisibleCenterRequest needs
// the true center to know how far to travel.
func (lookup *ElementLookup) visibleCenter(ctx context.Context, bounds device.Bounds) (device.Point, error) {
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return device.Point{}, err
	}
	return hierarchy.VisibleCenter(
		bounds, device.Bounds{Width: info.WidthGrid, Height: info.HeightGrid}), nil
}

// clips is the driver's word on which nodes clip what is drawn inside them
// (device.ViewportClipper), or nil when the screen is the only viewport.
func (lookup *ElementLookup) clips() func(device.TreeNode) bool {
	if clipper, ok := lookup.driver.(device.ViewportClipper); ok {
		return clipper.ClipsDescendants
	}
	return nil
}

// elementViewport is the part of the screen an element can be seen in: the
// screen narrowed by the windows around it.
func (lookup *ElementLookup) elementViewport(element *hierarchy.Element, screen device.Bounds) device.Bounds {
	return hierarchy.ClippedViewport(element, screen, lookup.clips())
}

// tapCenter is visibleCenter for a touch: it aims at the control the driver
// names inside the element when there is exactly one (device.TapTargeter),
// and at the element itself otherwise. A target the driver says would not
// receive the touch is refused: a button under a pinned footer was reported
// tapped while the footer's Share button took the touch (issue #42).
func (lookup *ElementLookup) tapCenter(ctx context.Context, stability ElementStabilityResult, appID string) (device.Point, error) {
	if err := lookup.refuseCoveredTarget(ctx, stability, appID); err != nil {
		return device.Point{}, err
	}
	bounds := stability.Bounds
	if targeter, ok := lookup.driver.(device.TapTargeter); ok && stability.Element != nil {
		if inner, found := tapTargetInside(targeter, stability.Element); found {
			bounds = inner
		}
	}
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return device.Point{}, err
	}
	screen := device.Bounds{Width: info.WidthGrid, Height: info.HeightGrid}
	return hierarchy.VisibleCenter(bounds, lookup.elementViewport(stability.Element, screen)), nil
}

// refuseCoveredTarget asks a driver that can hit-test whether the element
// would receive a touch. Undecided answers leave geometry in charge, as in
// scrollUntilVisible (issue #14).
func (lookup *ElementLookup) refuseCoveredTarget(ctx context.Context, stability ElementStabilityResult, appID string) error {
	tester, ok := lookup.driver.(device.HitTester)
	if !ok || stability.Element == nil {
		return nil
	}
	result, err := tester.Hittable(ctx, device.HittableRequest{
		AppID: appID, Node: stability.Element.Node, Bounds: stability.Bounds,
	})
	if err != nil {
		if cancellation := ctx.Err(); cancellation != nil {
			return cancellation
		}
		return err
	}
	if result.Decided && !result.Hittable {
		return NewOperationError(fmt.Sprintf(
			"target at %s is covered and would not receive the touch; "+
				"scroll it clear of the overlay first", hierarchy.FormatBounds(stability.Bounds)), nil)
	}
	return nil
}

func tapTargetInside(targeter device.TapTargeter, element *hierarchy.Element) (device.Bounds, bool) {
	var matches []device.Bounds
	var walk func(*hierarchy.Element)
	walk = func(node *hierarchy.Element) {
		for _, child := range node.Children {
			if child.HasBounds && child.Bounds.Width > 0 && child.Bounds.Height > 0 &&
				targeter.TapsInside(element.Node, child.Node) {
				matches = append(matches, child.Bounds)
			}
			walk(child)
		}
	}
	walk(element)
	if len(matches) != 1 {
		return device.Bounds{}, false
	}
	return matches[0], true
}

// onScreen refuses a resolved point the device does not have. An authored
// point is measured against the ELEMENT (resolveAxis), never the screen, so
// `point: 50%,50%` on a row scrolled past an edge resolves to a coordinate off
// the display -- and the runner offsets a touch straight to it, since
// XCUITestAutomation.coordinate does not clamp. Retargeting is not an option
// here the way it is for a center: the author's percentage means a spot on the
// element, so the flow says so instead of tapping nothing.
func (lookup *ElementLookup) onScreen(ctx context.Context, point device.Point) (device.Point, error) {
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return device.Point{}, err
	}
	if info.WidthGrid <= 0 || info.HeightGrid <= 0 {
		return point, nil
	}
	if point.X < 0 || point.X >= float64(info.WidthGrid) ||
		point.Y < 0 || point.Y >= float64(info.HeightGrid) {
		return device.Point{}, NewOperationError(fmt.Sprintf(
			"resolved point %g,%g is off the screen (%dx%d): the element is only partly visible",
			point.X, point.Y, info.WidthGrid, info.HeightGrid), nil)
	}
	return point, nil
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
