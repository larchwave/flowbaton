package ios

import (
	"context"
	"fmt"

	"github.com/larchwave/flowbaton/internal/device"
)

// systemBackButtonID is the identifier UIKit gives the navigation bar's back
// button, and SwiftUI's NavigationStack inherits it: measured on iOS 26.2 the
// button reads `BackButton` with the previous screen's title as its label
// (issue #41). The label is localised and app-chosen; the identifier is not.
const systemBackButtonID = "BackButton"

// switchElementType and toggleElementType are the XCUIElementTypes whose
// accessibility element can wrap the control that actually turns.
const (
	switchElementType = "40"
	toggleElementType = "41"
)

// BackPress pops one level of the native navigation stack the way a person
// does: it taps the navigation bar's system back button. iOS has no platform
// back gesture a driver can send, and the edge swipe is a coordinate guess, so
// the button is the one semantic way back. A leading Close or Cancel is a
// different action, and a button that merely shares the identifier outside a
// navigation bar is the app's own; with no back button, or more than one, the
// command fails and touches nothing.
func (driver *Driver) BackPress(ctx context.Context) error {
	appIDs := driver.defaultAppIDs(nil)
	hierarchy, err := driver.client.ViewHierarchy(ctx, appIDs, true)
	if err != nil {
		return fmt.Errorf("back: reading the hierarchy: %w", err)
	}
	var buttons []AXElement
	walkElements(hierarchy.AXElement, func(bar AXElement) {
		if bar.ElementType != navigationBarElementType {
			return
		}
		walkElements(bar, func(element AXElement) {
			if element.ElementType == buttonElementType && element.Identifier == systemBackButtonID &&
				element.Enabled && element.Frame.Width > 0 && element.Frame.Height > 0 {
				buttons = append(buttons, element)
			}
		})
	})
	switch len(buttons) {
	case 0:
		return fmt.Errorf("back: no navigation back button on screen; nothing to go back to")
	case 1:
	default:
		return fmt.Errorf("back: %d navigation back buttons on screen; tap the one you mean by its label", len(buttons))
	}
	button := buttons[0]
	tap := TouchRequest{X: button.Frame.X + button.Frame.Width/2, Y: button.Frame.Y + button.Frame.Height/2}
	if len(appIDs) > 0 {
		tap.AppID = appIDs[0]
	}
	if err := driver.client.Touch(ctx, tap); err != nil {
		return fmt.Errorf("back: tapping the back button %q: %w", button.Label, err)
	}
	return nil
}

// TapsInside implements device.TapTargeter. A SwiftUI Toggle is a switch that
// spans its Form row with the real switch nested at the trailing edge; a tap
// at the row's centre lands on the label and turns nothing (issue #40).
func (driver *Driver) TapsInside(element, descendant device.TreeNode) bool {
	kind := element.Attributes["elementType"]
	return (kind == switchElementType || kind == toggleElementType) &&
		descendant.Attributes["elementType"] == kind
}
