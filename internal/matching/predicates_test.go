package matching

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestFindMatchesTextHintAccessibilityLiteralAndNewlines(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("text", map[string]string{"text": "Hello\nWorld"}),
		matchNode("hint", map[string]string{"hintText": "a+b"}),
		matchNode("accessibility", map[string]string{"accessibilityText": "READY\nNOW"}),
	))
	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{name: "text regex is case insensitive dot-all and full match", pattern: "hello.*world", want: []string{"text"}},
		{name: "literal equality remains available", pattern: "a+b", want: []string{"hint"}},
		{name: "newlines are also normalized to spaces", pattern: "ready now", want: []string{"accessibility"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			matches, err := Find(root, model.ElementSelector{TextRegex: stringPointer(test.pattern)})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}
			if got := matchNames(matches); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Find names = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFindMatchesSizeToleranceAndTraits(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("square-text", map[string]string{"bounds": "[0,0][100,102]", "text": ""}),
		matchNode("wide", map[string]string{"bounds": "[0,0][100,50]", "text": "wide"}),
		matchNode("long", map[string]string{"bounds": "[0,0][20,20]", "text": strings.Repeat("x", 201)}),
		matchNode("not-long", map[string]string{"bounds": "[0,0][20,20]", "text": strings.Repeat("x", 200)}),
	))
	width, height, tolerance := 100, 100, 2
	matches, err := Find(root, model.ElementSelector{
		Size:   &model.SizeSelector{Width: &width, Height: &height, Tolerance: &tolerance},
		Traits: []model.ElementTrait{model.ElementTraitText, model.ElementTraitSquare},
	})
	if err != nil {
		t.Fatalf("Find size/traits: %v", err)
	}
	if got := matchNames(matches); !reflect.DeepEqual(got, []string{"square-text"}) {
		t.Fatalf("size/traits names = %v", got)
	}

	matches, err = Find(root, model.ElementSelector{Traits: []model.ElementTrait{model.ElementTraitLongText}})
	if err != nil {
		t.Fatalf("Find LONG_TEXT: %v", err)
	}
	if got := matchNames(matches); !reflect.DeepEqual(got, []string{"long"}) {
		t.Fatalf("LONG_TEXT names = %v", got)
	}
}

func TestFindIntersectsTypedElementStates(t *testing.T) {
	t.Parallel()

	matchingNode := matchNode("matching", nil)
	matchingNode.Enabled = boolPointer(true)
	matchingNode.Selected = boolPointer(false)
	matchingNode.Checked = boolPointer(true)
	matchingNode.Focused = boolPointer(false)
	mismatch := matchNode("mismatch", nil)
	mismatch.Enabled = boolPointer(false)
	mismatch.Selected = boolPointer(false)
	mismatch.Checked = boolPointer(true)
	mismatch.Focused = boolPointer(false)
	root := mustHierarchy(t, matchNode("root", nil, matchingNode, mismatch))
	enabled, selected, checked, focused := true, false, true, false
	matches, err := Find(root, model.ElementSelector{
		Enabled: &enabled, Selected: &selected, Checked: &checked, Focused: &focused,
	})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got := matchNames(matches); !reflect.DeepEqual(got, []string{"matching"}) {
		t.Fatalf("state intersection names = %v", got)
	}
}

func TestFindMatchesFullAndSuffixResourceID(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", nil,
		matchNode("target", map[string]string{"resource-id": "com.example:id/continue"}),
	))
	for _, pattern := range []string{`com\.example:id/continue`, "continue"} {
		matches, err := Find(root, model.ElementSelector{IDRegex: stringPointer(pattern)})
		if err != nil {
			t.Fatalf("Find(%q): %v", pattern, err)
		}
		if got := matchNames(matches); !reflect.DeepEqual(got, []string{"target"}) {
			t.Fatalf("Find(%q) names = %v", pattern, got)
		}
	}
}

func TestFindReturnsTypedInvalidRegexErrorWithoutPanic(t *testing.T) {
	t.Parallel()

	root := mustHierarchy(t, matchNode("root", map[string]string{"text": "anything"}))
	_, err := Find(root, model.ElementSelector{TextRegex: stringPointer("[")})
	if err == nil {
		t.Fatal("Find accepted invalid regex")
	}
	var regexErr *RegexError
	if !errors.As(err, &regexErr) {
		t.Fatalf("Find error = %T %v, want *RegexError", err, err)
	}
	if regexErr.Field != "text" || regexErr.Pattern != "[" {
		t.Fatalf("RegexError = %#v", regexErr)
	}
}

func mustHierarchy(t *testing.T, root device.TreeNode) *hierarchy.Element {
	t.Helper()
	result, err := hierarchy.New(root)
	if err != nil {
		t.Fatalf("hierarchy.New: %v", err)
	}
	return result
}

func matchNode(name string, attributes map[string]string, children ...device.TreeNode) device.TreeNode {
	cloned := make(map[string]string, len(attributes)+1)
	cloned["name"] = name
	for key, value := range attributes {
		cloned[key] = value
	}
	return device.TreeNode{Attributes: cloned, Children: children}
}

func matchNames(elements []*hierarchy.Element) []string {
	names := make([]string, len(elements))
	for index, element := range elements {
		names[index] = element.Node.Attributes["name"]
	}
	return names
}

func stringPointer(value string) *string { return &value }

func boolPointer(value bool) *bool { return &value }
