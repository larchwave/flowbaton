package android

import (
	"testing"

	"github.com/nohavewho/flowbaton/internal/device"
)

// specs/02-device-drivers.md:44 — "WebView hierarchy optionally merged via
// Chrome DevTools (localabstract CDP socket)". The merge is what makes the
// option worth having: the accessibility dump reports a WebView as one opaque
// node, so without it a flow can see that a WebView exists and nothing inside.
//
// The two trees use different coordinate spaces. The native dump is in device
// pixels on the screen; the page walker is in CSS pixels inside the page. A
// merge that pasted the page's numbers in unchanged would place every web
// element near the top-left of the screen, and every tap would land on
// whatever is there instead.

func nativeNode(class, bounds string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"class": class, "bounds": bounds},
		Children:   children,
	}
}

func webNode(tag, bounds, text string, children ...device.TreeNode) device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"tagName": tag, "bounds": bounds, "text": text},
		Children:   children,
	}
}

func TestMergeWebViewPlacesPageNodesInScreenCoordinates(t *testing.T) {
	t.Parallel()

	native := nativeNode("android.widget.FrameLayout", "[0,0][1080,1920]",
		nativeNode("android.widget.TextView", "[0,0][1080,100]"),
		nativeNode("android.webkit.WebView", "[40,200][1040,1200]"),
	)
	// A 500x500 CSS page inside a 1000x1000 device-pixel WebView: scale 2.
	page := webNode("body", "[0,0][500,500]", "",
		webNode("button", "[10,20][110,60]", "Sign in"),
	)

	merged, err := mergeWebViewHierarchy(native, page)
	if err != nil {
		t.Fatalf("mergeWebViewHierarchy() error = %v", err)
	}

	webView := merged.Children[1]
	if len(webView.Children) != 1 {
		t.Fatalf("the WebView got %d children, want the page root", len(webView.Children))
	}
	body := webView.Children[0]
	if got := body.Attributes["bounds"]; got != "[40,200][1040,1200]" {
		t.Errorf("page root bounds = %q, want the WebView's own rectangle", got)
	}
	if len(body.Children) != 1 {
		t.Fatalf("page root got %d children", len(body.Children))
	}
	// 40 + 10*2 = 60, 200 + 20*2 = 240, width 100*2 = 200, height 40*2 = 80.
	if got := body.Children[0].Attributes["bounds"]; got != "[60,240][260,320]" {
		t.Errorf("button bounds = %q, want [60,240][260,320]", got)
	}
	if got := body.Children[0].Attributes["text"]; got != "Sign in" {
		t.Errorf("button text = %q; the merge dropped attributes", got)
	}
}

// The native tree is the driver's own value and other code reads it; a merge
// that wrote into it would change what an unrelated caller sees.
func TestMergeWebViewDoesNotMutateTheNativeTree(t *testing.T) {
	t.Parallel()

	webView := nativeNode("android.webkit.WebView", "[0,0][100,100]")
	native := nativeNode("android.widget.FrameLayout", "[0,0][100,100]", webView)
	page := webNode("body", "[0,0][100,100]", "")

	if _, err := mergeWebViewHierarchy(native, page); err != nil {
		t.Fatalf("mergeWebViewHierarchy() error = %v", err)
	}
	if len(native.Children[0].Children) != 0 {
		t.Fatalf("the native WebView gained %d children", len(native.Children[0].Children))
	}
}

// A tree with nowhere to put the page is not an error: a flow can enable the
// devtools hierarchy once in its config and then visit native screens.
func TestMergeWebViewWithoutAWebViewReturnsTheNativeTree(t *testing.T) {
	t.Parallel()

	native := nativeNode("android.widget.FrameLayout", "[0,0][100,100]",
		nativeNode("android.widget.TextView", "[0,0][100,20]"))
	page := webNode("body", "[0,0][100,100]", "")

	merged, err := mergeWebViewHierarchy(native, page)
	if err != nil {
		t.Fatalf("mergeWebViewHierarchy() error = %v", err)
	}
	if len(merged.Children) != 1 || len(merged.Children[0].Children) != 0 {
		t.Fatalf("merged = %#v, want the native tree untouched", merged)
	}
}

// A page with no area gives no scale to compute. Merging it anyway would divide
// by zero or place every node at the WebView's origin, which is a pile of
// overlapping elements that the first-match lookup then picks from at random.
func TestMergeWebViewRefusesAPageWithNoArea(t *testing.T) {
	t.Parallel()

	native := nativeNode("android.widget.FrameLayout", "[0,0][100,100]",
		nativeNode("android.webkit.WebView", "[0,0][100,100]"))

	if _, err := mergeWebViewHierarchy(native, webNode("body", "[0,0][0,0]", "")); err == nil {
		t.Fatal("a zero-area page was merged")
	}
}

// Only the first WebView is filled: one forwarded CDP socket is one page, so
// claiming the same content for a second WebView would be an invention.
func TestMergeWebViewFillsOnlyTheFirstWebView(t *testing.T) {
	t.Parallel()

	native := nativeNode("android.widget.FrameLayout", "[0,0][200,100]",
		nativeNode("android.webkit.WebView", "[0,0][100,100]"),
		nativeNode("android.webkit.WebView", "[100,0][200,100]"),
	)
	page := webNode("body", "[0,0][100,100]", "")

	merged, err := mergeWebViewHierarchy(native, page)
	if err != nil {
		t.Fatalf("mergeWebViewHierarchy() error = %v", err)
	}
	if len(merged.Children[0].Children) != 1 {
		t.Error("the first WebView did not receive the page")
	}
	if len(merged.Children[1].Children) != 0 {
		t.Error("the second WebView received the same page")
	}
}
