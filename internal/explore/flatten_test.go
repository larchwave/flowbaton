package explore

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func boolPtr(v bool) *bool { return &v }

func sampleTree() device.TreeNode {
	return device.TreeNode{
		Attributes: map[string]string{"class": "Root"},
		Children: []device.TreeNode{
			{
				Attributes: map[string]string{"class": "Title", "text": "Inbox 42"},
			},
			{
				Attributes: map[string]string{"class": "List"},
				Children: []device.TreeNode{
					{
						Attributes: map[string]string{"class": "Button", "text": "Compose", "bounds": "[0,0][100,50]"},
						Clickable:  boolPtr(true),
					},
					{
						Attributes: map[string]string{"class": "Spacer"},
					},
				},
			},
		},
	}
}

func TestFlattenScreenAssignsStableIndexes(t *testing.T) {
	flat, err := FlattenScreen(sampleTree())
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 2 {
		t.Fatalf("flattened %d elements: %+v", len(flat), flat)
	}
	if flat[0].Node.Attributes["text"] != "Inbox 42" || flat[0].EIDX != 0 {
		t.Fatalf("first element %+v", flat[0])
	}
	if flat[1].Node.Attributes["text"] != "Compose" || flat[1].Path != "1/0" {
		t.Fatalf("second element %+v", flat[1])
	}
	bounds, ok := ElementBounds(flat[1].Node)
	if !ok || bounds.Width != 100 || bounds.Height != 50 {
		t.Fatalf("bounds %+v ok=%v", bounds, ok)
	}
	if _, ok := ElementBounds(flat[0].Node); ok {
		t.Fatal("bounds parsed from a node without bounds")
	}
}

func TestComputeSignatureIgnoresVolatileDigits(t *testing.T) {
	first := ComputeSignature("app", sampleTree())
	changed := sampleTree()
	changed.Children[0].Attributes["text"] = "Inbox 7"
	second := ComputeSignature("app", changed)
	if !first.Same(second) {
		t.Fatalf("digit change split the signature: %q vs %q", first.TreeDigest, second.TreeDigest)
	}
	structural := sampleTree()
	structural.Children[1].Children[0].Attributes["text"] = "Delete"
	third := ComputeSignature("app", structural)
	if first.Same(third) {
		t.Fatal("text change did not change the signature")
	}
	if len(first.Salient) == 0 || first.Salient[0] != "Inbox 42" {
		t.Fatalf("salient %v", first.Salient)
	}
}
