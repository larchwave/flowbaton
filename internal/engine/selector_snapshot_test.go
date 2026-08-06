package engine

import (
	"reflect"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestSelectorArgumentsMatchCanonicalizesCompletePreparedSnapshot(t *testing.T) {
	t.Parallel()

	text := "Target"
	id := "resource-id"
	point := "50%,50%"
	start := "10%,10%"
	end := "90%,90%"
	label := "primary"
	css := "#target"
	width := 100
	height := 40
	tolerance := 5
	index := "-1"
	repeat := 2
	delay := 100
	settle := 30_000
	trueValue := true
	falseValue := false
	nestedText := "Nested"
	selector := &model.ElementSelector{
		TextRegex:             &text,
		IDRegex:               &id,
		Size:                  &model.SizeSelector{Width: &width, Height: &height, Tolerance: &tolerance},
		Optional:              &falseValue,
		RetryTapIfNoChange:    &trueValue,
		WaitUntilVisible:      &trueValue,
		Point:                 &point,
		Start:                 &start,
		End:                   &end,
		Below:                 &model.ElementSelector{TextRegex: &nestedText},
		Above:                 &model.ElementSelector{TextRegex: &nestedText},
		LeftOf:                &model.ElementSelector{TextRegex: &nestedText},
		RightOf:               &model.ElementSelector{TextRegex: &nestedText},
		ContainsChild:         &model.ElementSelector{TextRegex: &nestedText},
		ContainsDescendants:   []model.ElementSelector{{TextRegex: &nestedText}},
		ChildOf:               &model.ElementSelector{TextRegex: &nestedText},
		Traits:                []model.ElementTrait{model.ElementTraitText, model.ElementTraitSquare},
		Index:                 &index,
		Enabled:               &trueValue,
		Selected:              &falseValue,
		Checked:               &trueValue,
		Focused:               &falseValue,
		Repeat:                &repeat,
		Delay:                 &delay,
		WaitToSettleTimeoutMS: &settle,
		Label:                 &label,
		CSS:                   &css,
	}
	raw := map[string]any{
		"text":                  text,
		"id":                    id,
		"width":                 int64(width),
		"height":                int32(height),
		"tolerance":             uint(tolerance),
		"optional":              falseValue,
		"retryTapIfNoChange":    trueValue,
		"waitUntilVisible":      trueValue,
		"point":                 point,
		"start":                 start,
		"end":                   end,
		"below":                 nestedText,
		"above":                 map[string]any{"text": nestedText},
		"leftOf":                nestedText,
		"rightOf":               nestedText,
		"containsChild":         nestedText,
		"containsDescendants":   []any{nestedText},
		"childOf":               nestedText,
		"traits":                "TEXT SQUARE",
		"index":                 index,
		"enabled":               trueValue,
		"selected":              falseValue,
		"checked":               trueValue,
		"focused":               falseValue,
		"repeat":                int64(repeat),
		"delay":                 int64(delay),
		"waitToSettleTimeoutMs": int64(settle),
		"label":                 label,
		"css":                   css,
	}
	snapshot := cloneDynamic(raw)
	if !selectorArgumentsMatch(raw, selector) {
		t.Fatal("complete raw selector does not match its equivalent typed snapshot")
	}
	if !reflect.DeepEqual(raw, snapshot) {
		t.Fatalf("selector canonicalization mutated raw arguments: got %#v want %#v", raw, snapshot)
	}

	raw["traits"] = []any{"TEXT", "SQUARE"}
	if !selectorArgumentsMatch(raw, selector) {
		t.Fatal("list-form traits do not match their equivalent typed snapshot")
	}
	raw["text"] = "Different"
	if selectorArgumentsMatch(raw, selector) {
		t.Fatal("different raw selector matched typed snapshot")
	}
}

func TestCanonicalRawSelectorRejectsMalformedShapes(t *testing.T) {
	t.Parallel()

	maximumUnsigned := ^uint64(0)
	for _, test := range []struct {
		name string
		raw  any
	}{
		{name: "nil", raw: nil},
		{name: "invalid nested selector", raw: map[string]any{"text": "Target", "below": true}},
		{name: "invalid descendants container", raw: map[string]any{"text": "Target", "containsDescendants": "Child"}},
		{name: "invalid descendant", raw: map[string]any{"text": "Target", "containsDescendants": []any{true}}},
		{name: "invalid scalar traits", raw: map[string]any{"text": "Target", "traits": true}},
		{name: "invalid list traits", raw: map[string]any{"text": "Target", "traits": []any{"TEXT", true}}},
		{name: "invalid integer kind", raw: map[string]any{"text": "Target", "index": 1.0}},
		{name: "overflowing unsigned integer", raw: map[string]any{"text": "Target", "index": maximumUnsigned}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if canonical, ok := canonicalRawSelector(test.raw); ok || canonical != nil {
				t.Fatalf("canonicalRawSelector(%#v) = %#v, %v; want nil, false", test.raw, canonical, ok)
			}
		})
	}
}

func TestSelectorCommandSnapshotMatchesTypedLabelAndOptional(t *testing.T) {
	t.Parallel()

	text := "Target"
	label := "label"
	optional := true
	command := model.Command{
		Arguments: map[string]any{"text": text, "label": label, "optional": optional},
		Selector: &model.ElementSelector{
			TextRegex: &text,
			Label:     &label,
			Optional:  &optional,
		},
		Label: &label, Optional: &optional,
	}
	if !selectorCommandSnapshotMatches(command) {
		t.Fatal("equivalent command selector metadata did not match")
	}
	different := false
	command.Optional = &different
	if selectorCommandSnapshotMatches(command) {
		t.Fatal("different command optional flag matched selector snapshot")
	}
	command.Optional = &optional
	otherLabel := "other"
	command.Label = &otherLabel
	if selectorCommandSnapshotMatches(command) {
		t.Fatal("different command label matched selector snapshot")
	}
}
