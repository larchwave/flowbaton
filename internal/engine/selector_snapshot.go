package engine

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/nohavewho/flowbaton/internal/model"
)

func selectorCommandSnapshotMatches(command model.Command) bool {
	if command.Selector == nil || !selectorArgumentsMatch(command.Arguments, command.Selector) {
		return false
	}
	return boolPointersEqual(command.Optional, command.Selector.Optional) &&
		stringPointersEqual(command.Label, command.Selector.Label)
}

func selectorArgumentsMatch(arguments any, selector *model.ElementSelector) bool {
	raw, ok := canonicalRawSelector(arguments)
	if !ok || selector == nil {
		return false
	}
	typed, ok := canonicalTypedSelector(selector)
	return ok && reflect.DeepEqual(raw, typed)
}

func canonicalRawSelector(arguments any) (map[string]any, bool) {
	if text, scalar := arguments.(string); scalar {
		return map[string]any{"text": text}, true
	}
	fields, ok := arguments.(map[string]any)
	if !ok {
		return nil, false
	}
	canonical := make(map[string]any, len(fields))
	for name, value := range fields {
		switch name {
		case "below", "above", "leftOf", "rightOf", "containsChild", "childOf":
			nested, valid := canonicalRawSelector(value)
			if !valid {
				return nil, false
			}
			canonical[name] = nested
		case "containsDescendants":
			values, valid := value.([]any)
			if !valid {
				return nil, false
			}
			nested := make([]any, len(values))
			for index := range values {
				selector, selectorValid := canonicalRawSelector(values[index])
				if !selectorValid {
					return nil, false
				}
				nested[index] = selector
			}
			canonical[name] = nested
		case "traits":
			traits, valid := canonicalRawTraits(value)
			if !valid {
				return nil, false
			}
			canonical[name] = traits
		case "width", "height", "tolerance", "repeat", "delay", "waitToSettleTimeoutMs":
			integer, valid := canonicalSelectorInteger(value)
			if !valid {
				return nil, false
			}
			canonical[name] = integer
		case "index":
			// Index remains scalar text until runtime. Normalize integers to decimal
			// text so raw and typed selector snapshots use the same representation.
			text, valid := canonicalSelectorIndex(value)
			if !valid {
				return nil, false
			}
			canonical[name] = text
		default:
			canonical[name] = value
		}
	}
	return canonical, true
}

func canonicalRawTraits(value any) ([]any, bool) {
	if text, scalar := value.(string); scalar {
		fields := strings.Fields(text)
		traits := make([]any, len(fields))
		for index := range fields {
			traits[index] = fields[index]
		}
		return traits, true
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	traits := make([]any, len(values))
	for index, value := range values {
		trait, valid := value.(string)
		if !valid {
			return nil, false
		}
		traits[index] = trait
	}
	return traits, true
}

// canonicalSelectorIndex normalizes a raw index scalar to its string form so it
// round-trips against the typed selector's string-typed Index. A string passes
// through; an integer scalar is rendered decimal; other kinds (float, overflow)
// are rejected.
func canonicalSelectorIndex(value any) (string, bool) {
	if text, ok := value.(string); ok {
		return text, true
	}
	integer, ok := canonicalSelectorInteger(value)
	if !ok {
		return "", false
	}
	return strconv.FormatInt(integer, 10), true
}

func canonicalSelectorInteger(value any) (int64, bool) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return 0, false
	}
	switch reflected.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflected.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		unsigned := reflected.Uint()
		if unsigned > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(unsigned), true
	default:
		return 0, false
	}
}

func canonicalTypedSelector(selector *model.ElementSelector) (map[string]any, bool) {
	if selector == nil {
		return nil, false
	}
	if selector.Size != nil && selector.Size.Width == nil && selector.Size.Height == nil && selector.Size.Tolerance == nil {
		return nil, false
	}
	canonical := make(map[string]any)
	addString := func(name string, value *string) {
		if value != nil {
			canonical[name] = *value
		}
	}
	addBool := func(name string, value *bool) {
		if value != nil {
			canonical[name] = *value
		}
	}
	addInt := func(name string, value *int) {
		if value != nil {
			canonical[name] = int64(*value)
		}
	}

	addString("text", selector.TextRegex)
	addString("id", selector.IDRegex)
	if selector.Size != nil {
		addInt("width", selector.Size.Width)
		addInt("height", selector.Size.Height)
		addInt("tolerance", selector.Size.Tolerance)
	}
	addBool("optional", selector.Optional)
	addBool("retryTapIfNoChange", selector.RetryTapIfNoChange)
	addBool("waitUntilVisible", selector.WaitUntilVisible)
	addString("point", selector.Point)
	addString("start", selector.Start)
	addString("end", selector.End)
	for name, nested := range map[string]*model.ElementSelector{
		"below":         selector.Below,
		"above":         selector.Above,
		"leftOf":        selector.LeftOf,
		"rightOf":       selector.RightOf,
		"containsChild": selector.ContainsChild,
		"childOf":       selector.ChildOf,
	} {
		if nested != nil {
			nestedCanonical, ok := canonicalTypedSelector(nested)
			if !ok {
				return nil, false
			}
			canonical[name] = nestedCanonical
		}
	}
	if selector.ContainsDescendants != nil {
		descendants := make([]any, len(selector.ContainsDescendants))
		for index := range selector.ContainsDescendants {
			descendant, ok := canonicalTypedSelector(&selector.ContainsDescendants[index])
			if !ok {
				return nil, false
			}
			descendants[index] = descendant
		}
		canonical["containsDescendants"] = descendants
	}
	if selector.Traits != nil {
		traits := make([]any, len(selector.Traits))
		for index := range selector.Traits {
			traits[index] = string(selector.Traits[index])
		}
		canonical["traits"] = traits
	}
	addString("index", selector.Index)
	addBool("enabled", selector.Enabled)
	addBool("selected", selector.Selected)
	addBool("checked", selector.Checked)
	addBool("focused", selector.Focused)
	addInt("repeat", selector.Repeat)
	addInt("delay", selector.Delay)
	addInt("waitToSettleTimeoutMs", selector.WaitToSettleTimeoutMS)
	addString("label", selector.Label)
	addString("css", selector.CSS)
	return canonical, true
}

func boolPointersEqual(left, right *bool) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func stringPointersEqual(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}
