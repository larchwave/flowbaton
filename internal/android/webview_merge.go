package android

// Merging the Chrome DevTools page hierarchy into the accessibility dump.
//
// specs/02-device-drivers.md:44 makes the WebView hierarchy "optionally merged
// via Chrome DevTools (localabstract CDP socket)". The accessibility dump
// reports a WebView as one opaque node, so without the merge a flow can see
// that a WebView exists and nothing inside it.
//
// The two trees do not share a coordinate space: the dump is device pixels on
// the screen, the page walker is CSS pixels inside the page. Everything here is
// about putting the second into the first.

import (
	"fmt"
	"maps"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/hierarchy"
)

// webViewClassSuffix is what an Android WebView reports as its class. The
// suffix rather than the exact name because subclasses are common, and a
// subclass is still the node the page lives in.
const webViewClassSuffix = "WebView"

// mergeWebViewHierarchy returns a new tree with the page attached under the
// first WebView, its coordinates scaled into screen space.
//
// Only the FIRST WebView is filled. One forwarded CDP socket is one page, so
// giving a second WebView the same content would be an invention, and a flow
// would then assert against an element that is not on the screen it named.
func mergeWebViewHierarchy(native, page device.TreeNode) (device.TreeNode, error) {
	target, found := findWebView(native)
	if !found {
		// Not an error: a flow enables the devtools hierarchy once in its
		// config and then visits native screens for the rest of the run.
		return native, nil
	}
	frame, err := hierarchy.ParseBounds(target.Attributes["bounds"])
	if err != nil {
		return device.TreeNode{}, fmt.Errorf("webview bounds: %w", err)
	}
	pageBounds, err := hierarchy.ParseBounds(page.Attributes["bounds"])
	if err != nil {
		return device.TreeNode{}, fmt.Errorf("page bounds: %w", err)
	}
	if pageBounds.Width <= 0 || pageBounds.Height <= 0 {
		return device.TreeNode{}, fmt.Errorf(
			"page reports no area (%s); nothing can be placed inside the WebView",
			page.Attributes["bounds"])
	}
	placed := placePage(page, frame, pageBounds)

	merged, _ := attachToFirstWebView(native, placed)
	return merged, nil
}

// findWebView returns the first WebView node in document order.
func findWebView(node device.TreeNode) (device.TreeNode, bool) {
	if isWebView(node) {
		return node, true
	}
	for _, child := range node.Children {
		if found, ok := findWebView(child); ok {
			return found, true
		}
	}
	return device.TreeNode{}, false
}

func isWebView(node device.TreeNode) bool {
	return strings.HasSuffix(node.Attributes["class"], webViewClassSuffix)
}

// attachToFirstWebView rebuilds the tree with the page under the first WebView.
// It rebuilds rather than assigns because the native tree is the driver's own
// value and other code reads it; writing into it would change what an unrelated
// caller sees. The bool reports whether the attachment already happened, so a
// later sibling does not receive it too.
func attachToFirstWebView(node device.TreeNode, page device.TreeNode) (device.TreeNode, bool) {
	if isWebView(node) {
		replaced := node
		replaced.Children = append(append([]device.TreeNode{}, node.Children...), page)
		return replaced, true
	}
	if len(node.Children) == 0 {
		return node, false
	}
	children := make([]device.TreeNode, len(node.Children))
	attached := false
	for index, child := range node.Children {
		if attached {
			children[index] = child
			continue
		}
		children[index], attached = attachToFirstWebView(child, page)
	}
	if !attached {
		return node, false
	}
	replaced := node
	replaced.Children = children
	return replaced, true
}

// placePage rewrites every bound in the page tree from CSS pixels inside the
// page to device pixels on the screen.
//
// The scale is per-axis because a WebView is not required to have the page's
// aspect ratio; a single averaged factor would smear one axis.
func placePage(node device.TreeNode, frame, pageBounds device.Bounds) device.TreeNode {
	scaleX := float64(frame.Width) / float64(pageBounds.Width)
	scaleY := float64(frame.Height) / float64(pageBounds.Height)
	return scaleNode(node, frame, pageBounds, scaleX, scaleY)
}

func scaleNode(
	node device.TreeNode, frame, pageBounds device.Bounds, scaleX, scaleY float64,
) device.TreeNode {
	placed := node
	placed.Attributes = make(map[string]string, len(node.Attributes))
	maps.Copy(placed.Attributes, node.Attributes)
	if bounds, err := hierarchy.ParseBounds(node.Attributes["bounds"]); err == nil {
		placed.Attributes["bounds"] = hierarchy.FormatBounds(device.Bounds{
			X:      frame.X + int(float64(bounds.X-pageBounds.X)*scaleX),
			Y:      frame.Y + int(float64(bounds.Y-pageBounds.Y)*scaleY),
			Width:  int(float64(bounds.Width) * scaleX),
			Height: int(float64(bounds.Height) * scaleY),
		})
	}
	// A node whose bounds do not parse keeps them verbatim: the synthetic
	// <option> nodes the page walker emits at the +100000 offset are meant to be
	// off-screen, and a driver that invented numbers for them would make them
	// tappable.
	if len(node.Children) == 0 {
		placed.Children = nil
		return placed
	}
	children := make([]device.TreeNode, len(node.Children))
	for index, child := range node.Children {
		children[index] = scaleNode(child, frame, pageBounds, scaleX, scaleY)
	}
	placed.Children = children
	return placed
}
