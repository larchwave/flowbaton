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

func TestFlattenScreenListsTextInputsWithoutLabels(t *testing.T) {
	// An empty field carries no text, label, or identifier, so the attribute
	// rules alone drop it -- and a typing scenario can never reach it.
	tree := device.TreeNode{
		Attributes: map[string]string{"class": "Root"},
		Children: []device.TreeNode{
			{Attributes: map[string]string{"class": "android.widget.EditText", "bounds": "[0,0][100,50]"}},
			{Attributes: map[string]string{"elementType": "49", "bounds": "[0,60][100,110]"}},
			{Attributes: map[string]string{"class": "android.widget.TextView"}},
		},
	}
	flat, err := FlattenScreen(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 2 {
		t.Fatalf("flattened %d elements, want the two text inputs: %+v", len(flat), flat)
	}
	if flat[0].Node.Attributes["class"] != "android.widget.EditText" {
		t.Fatalf("first element %+v", flat[0])
	}
	if flat[1].Node.Attributes["elementType"] != "49" {
		t.Fatalf("second element %+v", flat[1])
	}
}

func TestFlattenScreenSkipsStatusBarChrome(t *testing.T) {
	// The status bar is a sibling window of the app, so its clock, carrier,
	// and battery labels reach the element list and read as app content --
	// live sessions planned scenarios about Wi-Fi and signal in a reminders
	// app because of exactly these rows.
	tree := device.TreeNode{
		Attributes: map[string]string{"elementType": "1"},
		Children: []device.TreeNode{
			{
				Attributes: map[string]string{"elementType": "25"},
				Children: []device.TreeNode{
					{Attributes: map[string]string{"elementType": "48", "accessibilityText": "10:46"}},
					{Attributes: map[string]string{"elementType": "1", "accessibilityText": "No signal"}},
				},
			},
			{Attributes: map[string]string{"elementType": "9", "accessibilityText": "New Reminder"}},
		},
	}
	flat, err := FlattenScreen(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 1 {
		t.Fatalf("flattened %d elements, want only the app button: %+v", len(flat), flat)
	}
	if got := flat[0].Node.Attributes["accessibilityText"]; got != "New Reminder" {
		t.Fatalf("kept %q, want the app element", got)
	}
}

func TestIsTextInputSeparatesEditableFromStaticText(t *testing.T) {
	editable := []device.TreeNode{
		{Attributes: map[string]string{"class": "android.widget.EditText"}},
		{Attributes: map[string]string{"class": "androidx.appcompat.widget.AppCompatEditText"}},
		{Attributes: map[string]string{"class": "android.widget.AutoCompleteTextView"}},
		{Attributes: map[string]string{"elementType": "45"}},
		{Attributes: map[string]string{"elementType": "49"}},
		{Attributes: map[string]string{"elementType": "50"}},
		{Attributes: map[string]string{"elementType": "52"}},
	}
	for _, node := range editable {
		if !IsTextInput(node) {
			t.Fatalf("IsTextInput(%v) = false, want true", node.Attributes)
		}
	}
	static := []device.TreeNode{
		{Attributes: map[string]string{"class": "android.widget.TextView"}},
		{Attributes: map[string]string{"class": "android.widget.Button"}},
		{Attributes: map[string]string{"elementType": "44"}},
		{Attributes: map[string]string{"elementType": "47"}},
		{Attributes: map[string]string{"elementType": "48"}},
		{Attributes: map[string]string{}},
	}
	for _, node := range static {
		if IsTextInput(node) {
			t.Fatalf("IsTextInput(%v) = true, want false", node.Attributes)
		}
	}
}

func TestComputeSignatureIgnoresStatusBarChrome(t *testing.T) {
	// The signature names the screen for agents and keys the research cache.
	// Built over the status bar it names screens "No signal, SSID 3 of 3
	// Wi-Fi bars" and splits one screen into many as reception changes.
	withBar := func(carrier string) device.TreeNode {
		return device.TreeNode{
			Attributes: map[string]string{"elementType": "1"},
			Children: []device.TreeNode{
				{
					Attributes: map[string]string{"elementType": "25"},
					Children: []device.TreeNode{
						{Attributes: map[string]string{"elementType": "48", "text": carrier}},
					},
				},
				{Attributes: map[string]string{"elementType": "9", "text": "New Reminder"}},
			},
		}
	}
	first := ComputeSignature("app", withBar("No signal"))
	if len(first.Salient) == 0 || first.Salient[0] != "New Reminder" {
		t.Fatalf("salient %v, want the app label first", first.Salient)
	}
	for _, label := range first.Salient {
		if label == "No signal" {
			t.Fatalf("status bar text reached the salient labels: %v", first.Salient)
		}
	}
	second := ComputeSignature("app", withBar("SSID, 3 of 3 Wi-Fi bars"))
	if !first.Same(second) {
		t.Fatalf("reception change split the screen: %q vs %q", first.TreeDigest, second.TreeDigest)
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
