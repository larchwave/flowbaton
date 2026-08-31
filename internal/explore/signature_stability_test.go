package explore

import (
	"fmt"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func offscreenIcon(name string) device.TreeNode {
	return device.TreeNode{Attributes: map[string]string{
		"elementType": "44", "id": name, "accessibilityText": name, "bounds": "[0,0][0,0]",
	}}
}

// Two live captures of one untouched screen came back with these two
// zero-bounds siblings in opposite order (measured on iOS 26.2, 267 of 269
// nodes identical).
func TestSignatureSurvivesOffscreenSiblingSwap(t *testing.T) {
	root := func(first, second string) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"elementType": "0"},
			Children:   []device.TreeNode{offscreenIcon(first), offscreenIcon(second)},
		}
	}
	a := ComputeSignature("app", root("Passwords", "Shortcuts"))
	b := ComputeSignature("app", root("Shortcuts", "Passwords"))
	if a.TreeDigest != b.TreeDigest {
		t.Fatalf("the same screen produced two digests: %s vs %s", a.TreeDigest, b.TreeDigest)
	}
}

// The fix above must not buy that stability by making the digest blind: a
// swap of two nodes the screen actually shows is a real change.
func TestSignatureStillSeesAVisibleSiblingSwap(t *testing.T) {
	visible := func(name string, y int) device.TreeNode {
		return device.TreeNode{Attributes: map[string]string{
			"elementType": "44", "id": name, "accessibilityText": name,
			"bounds": fmt.Sprintf("[0,%d][100,%d]", y, y+40),
		}}
	}
	root := func(first, second device.TreeNode) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"elementType": "0"},
			Children:   []device.TreeNode{first, second},
		}
	}
	a := ComputeSignature("app", root(visible("Passwords", 0), visible("Shortcuts", 40)))
	b := ComputeSignature("app", root(visible("Shortcuts", 0), visible("Passwords", 40)))
	if a.TreeDigest == b.TreeDigest {
		t.Fatal("two visibly different screens share one digest")
	}
}
