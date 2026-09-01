package explore

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

// A tap that flips a switch and changes nothing else reported "done, but the
// screen did not change", which tells the tester its action was dead and
// tells the judge the outcome was never demonstrated.
//
// Two rules meet here and neither is wrong on its own. The signature reads a
// node's text first, and iOS answers with a switch's VALUE there -- "0" or
// "1". normalizeText collapses every digit run to "#" so a clock or a badge
// does not split one screen into many. Together they fold "0" and "1" onto
// the same token, and Android never had the state in the digest at all: it
// carries the flag on the node and leaves the text alone.
func TestSignatureSeesASwitchFlip(t *testing.T) {
	t.Parallel()

	screen := func(value string, checked bool) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{
					"elementType":       "40",
					"accessibilityText": "Airplane Mode",
					"text":              value,
					"bounds":            "[0,0][402,60]",
				},
				Checked: boolPtr(checked),
			}},
		}
	}
	off := ComputeSignature("app", screen("0", false))
	on := ComputeSignature("app", screen("1", true))
	if off.Same(on) {
		t.Errorf("a flipped switch keeps the digest %s, so the flip reads as no-change", off.TreeDigest)
	}
}

// An Android checkbox carries its state on the flag and never in the text,
// so the two screens below differ only by the flag.
func TestSignatureSeesAnAndroidCheckboxFlip(t *testing.T) {
	t.Parallel()

	screen := func(checked bool) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{
					"class":  "android.widget.CheckBox",
					"text":   "Remember me",
					"bounds": "[0,0][402,60]",
				},
				Checked: boolPtr(checked),
			}},
		}
	}
	if ComputeSignature("app", screen(false)).Same(ComputeSignature("app", screen(true))) {
		t.Error("a flipped checkbox keeps the digest, so the flip reads as no-change")
	}
}

// A count still folds: it is what normalizeText is for, and a badge ticking
// from 2 to 3 must not make the screen a new one.
func TestSignatureStillFoldsACount(t *testing.T) {
	t.Parallel()

	screen := func(count string) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"class": "android.widget.FrameLayout", "bounds": "[0,0][402,874]"},
			Children: []device.TreeNode{{
				Attributes: map[string]string{
					"elementType": "48", "text": count + " unread", "bounds": "[0,0][402,60]",
				},
			}},
		}
	}
	if !ComputeSignature("app", screen("2")).Same(ComputeSignature("app", screen("3"))) {
		t.Error("a changed count split the screen into two signatures")
	}
}
