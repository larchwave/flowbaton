package explore

import (
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/larchwave/flowbaton/internal/device"
)

// The limit says which labels are short enough to read as a name rather than
// as content, so it counts what a person reads: characters. It counted bytes,
// which is the same number only for a screen written in ASCII -- a Russian
// title of 29 characters weighs 55 bytes and was thrown out as content, and a
// Chinese one hits the limit at 14. That empties the salient list on exactly
// the devices f49dc21 taught it to name.
func TestComputeSignatureMeasuresALabelInCharacters(t *testing.T) {
	t.Parallel()

	const title = "Настройки уведомлений и звуков"
	if runes, bytes := utf8.RuneCountInString(title), len(title); runes > salientLabelLimit || bytes <= salientLabelLimit {
		t.Fatalf("title is %d characters and %d bytes, which does not test the limit", runes, bytes)
	}
	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			node(map[string]string{"elementType": "48", "accessibilityText": title}),
			node(map[string]string{"elementType": "9", "accessibilityText": "Готово"}),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{title, "Готово"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}

// A label that really is long stays content, in any script.
func TestComputeSignatureStillDropsALongLabel(t *testing.T) {
	t.Parallel()

	long := "Разрешить приложению доступ к вашим контактам и календарю"
	tree := device.TreeNode{
		Attributes: map[string]string{"bounds": "[0,0][402,874]"},
		Children: []device.TreeNode{
			node(map[string]string{"elementType": "48", "accessibilityText": long}),
			node(map[string]string{"elementType": "9", "accessibilityText": "Готово"}),
		},
	}
	if got, want := ComputeSignature("app", tree).Salient, []string{"Готово"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("salient = %q, want %q", got, want)
	}
}
