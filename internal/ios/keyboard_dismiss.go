package ios

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// XCUIElement.ElementType raw values the dismissal logic reads. No spec names
// them; the runner serialises the enum as its integer.
const (
	buttonElementType          = 9
	keyboardElementType        = 19
	keyElementType             = 20
	navigationBarElementType   = 21
	toolbarElementType         = 24
	textFieldElementType       = 49
	secureTextFieldElementType = 50
	textViewElementType        = 52
)

const (
	keyboardDismissTimeout = 1500 * time.Millisecond
	keyboardDismissPoll    = 100 * time.Millisecond
	// keyboardToolbarGap is how far above the keyboard frame an input
	// accessory toolbar may sit. Observed on iOS 26.2 (AI Origami, issue #25):
	// the predictive bar occupies 44pt between the toolbar and the keyboard.
	keyboardToolbarGap = 64.0
	// keyboardSwipeLead is how far above the keyboard the dismissing drag
	// starts: inside the content, where a scroll view's interactive dismissal
	// listens, not on the keys.
	keyboardSwipeLead         = 24.0
	keyboardSwipeDurationSecs = 0.3
)

// HideKeyboard has no route of its own, and Return is not a dismissal: on an
// on-submit field it submits the form, in a multiline field it inserts a
// newline (issue #25). The driver dismisses the way a person would — the iPad
// hide key on the keyboard, the application's own keyboard toolbar button,
// or a drag down over the keyboard — and verifies through /keyboard that the
// keyboard actually went down. A drag cannot type or submit, but it can move
// content, so the text inputs are checked to be unchanged afterwards. Without
// any of these the command fails and says what the flow can do instead.
func (driver *Driver) HideKeyboard(ctx context.Context) error {
	appIDs := driver.defaultAppIDs(nil)
	visible, err := driver.client.KeyboardVisible(ctx, appIDs)
	if err != nil {
		return err
	}
	if !visible {
		return nil
	}
	hierarchy, err := driver.client.ViewHierarchy(ctx, appIDs, false)
	if err != nil {
		return fmt.Errorf("hideKeyboard: reading the hierarchy: %w", err)
	}
	keyboard, ok := findElement(hierarchy.AXElement, func(element AXElement) bool {
		return element.ElementType == keyboardElementType && element.Frame.Height > 0
	})
	if !ok {
		return fmt.Errorf("hideKeyboard: the runner reports a keyboard but the hierarchy has none")
	}
	if control, ok := findKeyboardDismissControl(hierarchy.AXElement, keyboard); ok {
		return driver.dismissKeyboardByTap(ctx, appIDs, control)
	}
	return driver.dismissKeyboardBySwipe(ctx, appIDs, hierarchy.AXElement, keyboard)
}

// dismissKeyboardByTap taps the control's centre in the application the
// hierarchy was read from: a screen-space tap misses inside an iPhone
// compatibility window on iPad (issues #15 and #17).
func (driver *Driver) dismissKeyboardByTap(ctx context.Context, appIDs []string, control AXElement) error {
	tap := TouchRequest{
		X: control.Frame.X + control.Frame.Width/2,
		Y: control.Frame.Y + control.Frame.Height/2,
	}
	if len(appIDs) > 0 {
		tap.AppID = appIDs[0]
	}
	if err := driver.client.Touch(ctx, tap); err != nil {
		return fmt.Errorf("hideKeyboard: tapping %q: %w", control.Label, err)
	}
	return driver.waitForKeyboardDismissal(ctx, appIDs, fmt.Sprintf("tapping %q", control.Label))
}

// dismissKeyboardBySwipe drags from just above the keyboard down over it, the
// gesture a scroll view's interactive dismissal answers. The drag is safe
// for the text but not for the screen — a sheet can follow the finger — so
// the text inputs before and after must match.
func (driver *Driver) dismissKeyboardBySwipe(ctx context.Context, appIDs []string, root, keyboard AXElement) error {
	before := textInputs(root)
	x := keyboard.Frame.X + keyboard.Frame.Width/2
	swipe := SwipeV2Request{
		StartX: x, StartY: keyboard.Frame.Y - keyboardSwipeLead,
		EndX: x, EndY: keyboard.Frame.Y + keyboard.Frame.Height,
		Duration: keyboardSwipeDurationSecs,
		AppIDs:   appIDs,
	}
	if err := driver.client.SwipeV2(ctx, swipe); err != nil {
		return fmt.Errorf("hideKeyboard: swiping the keyboard down: %w", err)
	}
	if err := driver.waitForKeyboardDismissal(ctx, appIDs, "a swipe"); err != nil {
		return fmt.Errorf("%w (no iPad hide key, no keyboard toolbar button; "+
			"tap the application's own control instead)", err)
	}
	after, err := driver.client.ViewHierarchy(ctx, appIDs, false)
	if err != nil {
		return fmt.Errorf("hideKeyboard: re-reading the hierarchy: %w", err)
	}
	if got := textInputs(after.AXElement); got != before {
		return fmt.Errorf("hideKeyboard: the keyboard went down but the swipe changed the screen: "+
			"text inputs were %q, now %q", before, got)
	}
	return nil
}

func (driver *Driver) waitForKeyboardDismissal(ctx context.Context, appIDs []string, action string) error {
	wait := driver.keyboardDismissWait
	if wait <= 0 {
		wait = keyboardDismissTimeout
	}
	deadline := time.Now().Add(wait)
	for {
		visible, err := driver.client.KeyboardVisible(ctx, appIDs)
		if err != nil {
			return err
		}
		if !visible {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("hideKeyboard: keyboard is still visible %s after %s", wait, action)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(keyboardDismissPoll):
		}
	}
}

// findKeyboardDismissControl prefers the keyboard's own hide key (iPad), then
// a button in the input accessory toolbar directly above the keyboard. A
// navigation bar "Done" closes a sheet and a bottom bar is under the keyboard,
// so geometry — not the label — decides what counts.
func findKeyboardDismissControl(root, keyboard AXElement) (AXElement, bool) {
	if key, ok := findElement(keyboard, isHideKeyboardKey); ok {
		return key, true
	}
	toolbar, ok := findElement(root, func(element AXElement) bool {
		if element.ElementType != toolbarElementType || element.Frame.Height <= 0 {
			return false
		}
		gap := keyboard.Frame.Y - (element.Frame.Y + element.Frame.Height)
		return gap >= 0 && gap <= keyboardToolbarGap
	})
	if !ok {
		return AXElement{}, false
	}
	var buttons []AXElement
	walkElements(toolbar, func(element AXElement) {
		if element.ElementType == buttonElementType && element.Enabled {
			buttons = append(buttons, element)
		}
	})
	for _, button := range buttons {
		if strings.EqualFold(strings.TrimSpace(button.Label), "done") {
			return button, true
		}
	}
	if len(buttons) == 1 {
		return buttons[0], true
	}
	return AXElement{}, false
}

func isHideKeyboardKey(element AXElement) bool {
	if element.ElementType != buttonElementType && element.ElementType != keyElementType {
		return false
	}
	label := strings.ToLower(element.Label + " " + element.Identifier)
	return strings.Contains(label, "hide keyboard") || strings.Contains(label, "dismiss")
}

// textInputs is the screen's text fields as one string of identity and
// value, in tree order: the same before and after a dismissal means the
// gesture changed nothing a flow could read. Focus is left out; the runner
// reports it false while the keyboard is up.
func textInputs(root AXElement) string {
	var inputs []string
	walkElements(root, func(element AXElement) {
		switch element.ElementType {
		case textFieldElementType, secureTextFieldElementType, textViewElementType:
		default:
			return
		}
		value := ""
		if element.Value != nil {
			value = *element.Value
		}
		inputs = append(inputs, element.Identifier+"="+value)
	})
	return strings.Join(inputs, "; ")
}

func findElement(root AXElement, matches func(AXElement) bool) (AXElement, bool) {
	if matches(root) {
		return root, true
	}
	for _, child := range root.Children {
		if found, ok := findElement(child, matches); ok {
			return found, true
		}
	}
	return AXElement{}, false
}

func walkElements(root AXElement, visit func(AXElement)) {
	visit(root)
	for _, child := range root.Children {
		walkElements(child, visit)
	}
}
