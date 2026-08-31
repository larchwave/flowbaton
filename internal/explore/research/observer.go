// Package research implements the Observer and Researcher exploration
// roles: capturing one settled screen of the app through the device driver
// and turning that capture into a validated UI map for planning and
// testing conversations.
package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/explore"
)

// DefaultSettleTimeout bounds the settle wait when the caller sets none.
const DefaultSettleTimeout = 5 * time.Second

// modalMarkers are class/type fragments that mark a modal surface.
var modalMarkers = []string{"alert", "dialog", "sheet", "modal", "popup"}

// Observer captures the current screen of one application. It settles the
// screen within a bound, reads the hierarchy scoped to the app, takes an
// uncompressed screenshot, and derives flattened elements plus the screen
// signature.
type Observer struct {
	Driver device.Driver
	AppID  string
	// SettleTimeout bounds the static-screen wait; zero means
	// DefaultSettleTimeout.
	SettleTimeout time.Duration
	// Logf receives observation notes, such as an unsettled screen
	// being captured anyway. Nil is silent.
	Logf func(format string, args ...any)
	// Clock supplies capture timestamps; nil means wall time.
	Clock func() time.Time
}

// Observe captures one full screen state. An unsettled screen is noted and
// captured anyway; a settle probe the platform cannot answer is treated
// the same way.
func (o *Observer) Observe(ctx context.Context) (*explore.ScreenState, error) {
	if o.Driver == nil {
		return nil, errors.New("research: observer needs a driver")
	}
	if strings.TrimSpace(o.AppID) == "" {
		return nil, errors.New("research: observer needs an app id")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := o.SettleTimeout
	if timeout <= 0 {
		timeout = DefaultSettleTimeout
	}
	static, err := o.Driver.WaitUntilScreenIsStatic(ctx, device.ScreenStaticRequest{
		TimeoutMillis: timeout.Milliseconds(),
	})
	switch {
	case errors.Is(err, device.ErrUnsupported):
		o.logf("settle probe unsupported, capturing anyway")
	case err != nil:
		return nil, fmt.Errorf("research: settle: %w", err)
	case !static:
		o.logf("screen still moving after %s, capturing anyway", timeout)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Without this the keyboard arrives as a row per key: 41 of 342 nodes on
	// a reminders screen with a focused field, every one of them looking
	// tappable in the element table. Session mmx34 spent four steps tapping
	// "the element with id Return". The tester reaches the keyboard through
	// press_key and hide_keyboard, and needs the app's own elements here.
	root, err := o.Driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{
		AppIDs:                  []string{o.AppID},
		ExcludeKeyboardElements: true,
	})
	if err != nil {
		return nil, fmt.Errorf("research: hierarchy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	screenshot, err := o.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{Compressed: false})
	if err != nil {
		return nil, fmt.Errorf("research: screenshot: %w", err)
	}
	elements, err := explore.FlattenScreen(root)
	if err != nil {
		return nil, fmt.Errorf("research: flatten: %w", err)
	}
	// Everything downstream that selects an element by name prunes the tree
	// to this box, the way the engine does before matching. Without it a
	// name reaches an element with no area or one past the screen edge, and
	// a tap lands on the corner or off the screen.
	info, err := o.Driver.DeviceInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("research: screen size: %w", err)
	}
	return &explore.ScreenState{
		Viewport:      device.Bounds{Width: info.WidthGrid, Height: info.HeightGrid},
		Signature:     explore.ComputeSignature(o.AppID, root),
		Hierarchy:     root,
		Elements:      elements,
		ScreenshotPNG: screenshot,
		CapturedAt:    o.now(),
		DialogActive:  dialogActive(root),
	}, nil
}

func (o *Observer) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

func (o *Observer) now() time.Time {
	if o.Clock != nil {
		return o.Clock()
	}
	return time.Now()
}

// dialogActive reports a modal surface dominating the screen: a top-level
// subtree after the first whose class or type names a modal container.
func dialogActive(root device.TreeNode) bool {
	for index, child := range root.Children {
		if index == 0 {
			continue
		}
		if subtreeHasModalClass(child) {
			return true
		}
	}
	return false
}

func subtreeHasModalClass(node device.TreeNode) bool {
	role := node.Attributes["class"]
	if role == "" {
		role = node.Attributes["type"]
	}
	folded := strings.ToLower(role)
	for _, marker := range modalMarkers {
		if strings.Contains(folded, marker) {
			return true
		}
	}
	for _, child := range node.Children {
		if subtreeHasModalClass(child) {
			return true
		}
	}
	return false
}
