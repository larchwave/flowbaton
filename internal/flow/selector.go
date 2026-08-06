package flow

import (
	"fmt"
	"strings"

	"github.com/nohavewho/flowbaton/internal/model"
	"go.yaml.in/yaml/v3"
)

func parseSelector(path string, data []byte, node *yaml.Node) (*model.ElementSelector, error) {
	if node == nil || isNullNode(node) {
		return &model.ElementSelector{}, nil
	}
	if node.Kind == yaml.ScalarNode {
		// Any scalar, not just a string: YAML types a bare 42 as an int and the
		// contract accepts it as the text "42". See scalar_selector_test.go.
		text, err := scalarString(path, data, node, "selector_field_type", "selector")
		if err != nil {
			return nil, err
		}
		return &model.ElementSelector{
			TextRegex:    &text,
			Source:       sourceInfo(path, node, data),
			FieldSources: map[string]model.SourceInfo{"text": sourceInfo(path, node, data)},
		}, nil
	}
	if node.Kind != yaml.MappingNode {
		return nil, diagnostic(path, "selector_field_type", "selector must be a string or object", node, data)
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_selector_field")
	if err != nil {
		return nil, err
	}
	selector := &model.ElementSelector{
		Source:       sourceInfo(path, node, data),
		FieldSources: make(map[string]model.SourceInfo, len(pairs)),
	}
	size := &model.SizeSelector{}
	hasSize := false
	for _, pair := range pairs {
		selector.FieldSources[pair.key.Value] = sourceInfo(path, pair.key, data)
		switch pair.key.Value {
		case "text":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "text")
			selector.TextRegex, err = &value, parseErr
		case "id":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "id")
			selector.IDRegex, err = &value, parseErr
		case "width":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "width")
			size.Width, hasSize, err = &value, true, parseErr
		case "height":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "height")
			size.Height, hasSize, err = &value, true, parseErr
		case "tolerance":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "tolerance")
			size.Tolerance, hasSize, err = &value, true, parseErr
		case "optional":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "optional")
			selector.Optional, err = &value, parseErr
		case "retryTapIfNoChange":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "retryTapIfNoChange")
			selector.RetryTapIfNoChange, err = &value, parseErr
		case "waitUntilVisible":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "waitUntilVisible")
			selector.WaitUntilVisible, err = &value, parseErr
		case "point":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "point")
			selector.Point, err = &value, parseErr
		case "start":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "start")
			selector.Start, err = &value, parseErr
		case "end":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "end")
			selector.End, err = &value, parseErr
		case "below":
			selector.Below, err = parseSelector(path, data, pair.value)
		case "above":
			selector.Above, err = parseSelector(path, data, pair.value)
		case "leftOf":
			selector.LeftOf, err = parseSelector(path, data, pair.value)
		case "rightOf":
			selector.RightOf, err = parseSelector(path, data, pair.value)
		case "containsChild":
			selector.ContainsChild, err = parseSelector(path, data, pair.value)
		case "containsDescendants":
			selector.ContainsDescendants, err = parseSelectorList(path, data, pair.value)
		case "childOf":
			selector.ChildOf, err = parseSelector(path, data, pair.value)
		case "traits":
			selector.Traits, err = parseTraits(path, data, pair.value)
		case "index":
			// Index remains scalar text through parsing. The engine interpolates it,
			// and the matcher resolves the integer at match time.
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "index")
			selector.Index, err = &value, parseErr
		case "enabled":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "enabled")
			selector.Enabled, err = &value, parseErr
		case "selected":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "selected")
			selector.Selected, err = &value, parseErr
		case "checked":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "checked")
			selector.Checked, err = &value, parseErr
		case "focused":
			value, parseErr := coercedBoolValue(path, data, pair.value, "selector_field_type", "focused")
			selector.Focused, err = &value, parseErr
		case "repeat":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "repeat")
			selector.Repeat, err = &value, parseErr
		case "delay":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "delay")
			selector.Delay, err = &value, parseErr
		case "waitToSettleTimeoutMs":
			value, parseErr := coercedIntValue(path, data, pair.value, "selector_field_type", "waitToSettleTimeoutMs")
			selector.WaitToSettleTimeoutMS, err = &value, parseErr
		case "label":
			// Selector labels accept any scalar and retain its textual value.
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "label")
			selector.Label, err = &value, parseErr
		case "css":
			value, parseErr := scalarString(path, data, pair.value, "selector_field_type", "css")
			selector.CSS, err = &value, parseErr
		default:
			return nil, diagnostic(path, "unknown_selector_field", fmt.Sprintf("unknown selector field %s", pair.key.Value), pair.key, data)
		}
		if err != nil {
			return nil, err
		}
	}
	if hasSize {
		selector.Size = size
	}
	return selector, nil
}

func parseSelectorList(path string, data []byte, node *yaml.Node) ([]model.ElementSelector, error) {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil, diagnostic(path, "selector_field_type", "containsDescendants must be an array of selectors", node, data)
	}
	selectors := make([]model.ElementSelector, 0, len(node.Content))
	for _, child := range node.Content {
		selector, err := parseSelector(path, data, child)
		if err != nil {
			return nil, err
		}
		selectors = append(selectors, *selector)
	}
	return selectors, nil
}

func parseTraits(path string, data []byte, node *yaml.Node) ([]model.ElementTrait, error) {
	values, err := stringList(path, data, node, true, "selector_field_type", "traits")
	if err != nil {
		return nil, err
	}
	if node.Kind == yaml.ScalarNode {
		values = strings.Fields(values[0])
	}
	traits := make([]model.ElementTrait, 0, len(values))
	for _, value := range values {
		trait := model.ElementTrait(value)
		switch trait {
		case model.ElementTraitText, model.ElementTraitSquare, model.ElementTraitLongText:
			traits = append(traits, trait)
		default:
			return nil, diagnostic(path, "invalid_selector_trait", fmt.Sprintf("unknown selector trait %s", value), node, data)
		}
	}
	return traits, nil
}

func parseCondition(path string, data []byte, node *yaml.Node) (*model.Condition, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, diagnostic(path, "condition_field_type", "condition must be an object", node, data)
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_condition_field")
	if err != nil {
		return nil, err
	}
	condition := &model.Condition{
		Source:       sourceInfo(path, node, data),
		FieldSources: make(map[string]model.SourceInfo, len(pairs)),
	}
	for _, pair := range pairs {
		condition.FieldSources[pair.key.Value] = sourceInfo(path, pair.key, data)
		switch pair.key.Value {
		case "platform":
			value, parseErr := stringValue(path, data, pair.value, "condition_field_type", "platform")
			if parseErr != nil {
				return nil, parseErr
			}
			// Platform names are case-insensitive. Store the lowercase canonical
			// form the engine checks. See contract_link_test.go
			// TestPlatformConditionCaseInsensitive.
			platform := model.Platform(strings.ToLower(value))
			switch platform {
			case model.PlatformAndroid, model.PlatformIOS, model.PlatformWeb:
				condition.Platform = &platform
			default:
				return nil, diagnostic(path, "invalid_condition_platform", fmt.Sprintf("unknown condition platform %s", value), pair.value, data)
			}
		case "visible":
			condition.Visible, err = parseSelector(path, data, pair.value)
		case "notVisible":
			condition.NotVisible, err = parseSelector(path, data, pair.value)
		case "true":
			value, parseErr := stringValue(path, data, pair.value, "condition_field_type", "true")
			condition.ScriptCondition, err = &value, parseErr
		case "label":
			value, parseErr := stringValue(path, data, pair.value, "condition_field_type", "label")
			condition.Label, err = &value, parseErr
		case "optional":
			value, parseErr := coercedBoolValue(path, data, pair.value, "condition_field_type", "optional")
			condition.Optional, err = &value, parseErr
		default:
			return nil, diagnostic(path, "unknown_condition_field", fmt.Sprintf("unknown condition field %s", pair.key.Value), pair.key, data)
		}
		if err != nil {
			return nil, err
		}
	}
	return condition, nil
}
