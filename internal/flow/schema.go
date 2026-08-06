package flow

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
	"go.yaml.in/yaml/v3"
)

type yamlKindMask uint16

const (
	yamlKindNull yamlKindMask = 1 << iota
	yamlKindString
	yamlKindBool
	yamlKindInt
	yamlKindFloat
	yamlKindMap
	yamlKindSequence
)

type fieldValidator func(path string, data []byte, node *yaml.Node) error

type commandFieldRule struct {
	kinds     yamlKindMask
	required  bool
	enum      []string
	validator fieldValidator
	// scalarText retains any scalar's textual value. It is explicit because some
	// mixed-kind fields, such as eraseText, preserve numeric meaning.
	scalarText bool
}

type commandSchema struct {
	bare          bool
	noArguments   bool
	rejectAliases bool
	valueKinds    yamlKindMask
	selector      bool
	fields        map[string]commandFieldRule
	requiredAny   []string
	// allowUnknownFields makes map-form validation ignore keys not in `fields`
	// while type-checking known keys. inputText uses this permissive shape; the
	// default keeps every other command strict.
	allowUnknownFields bool
	dynamicValidator   fieldValidator
	valueValidator     fieldValidator
	// scalarValueText is scalarText for the command's bare value.
	scalarValueText bool
}

func validateBareCommand(path string, data []byte, keyword model.CommandKeyword, node *yaml.Node) error {
	schema, ok := commandSchemaV0[keyword]
	if !ok {
		return diagnostic(path, "unclassified_command", fmt.Sprintf("command %s has no v0 syntax schema", keyword), node, data)
	}
	if !schema.bare {
		return diagnostic(path, "command_argument_required", fmt.Sprintf("command %s requires an argument", keyword), node, data)
	}
	return nil
}

func validateCommandValue(path string, data []byte, keyword model.CommandKeyword, node *yaml.Node) error {
	schema, ok := commandSchemaV0[keyword]
	if !ok {
		return diagnostic(path, "unclassified_command", fmt.Sprintf("command %s has no v0 syntax schema", keyword), node, data)
	}
	return validateValueAgainstSchema(path, data, keyword, node, schema)
}

func validateValueAgainstSchema(path string, data []byte, keyword model.CommandKeyword, node *yaml.Node, schema commandSchema) error {
	if schema.rejectAliases {
		if alias := firstAliasNode(node); alias != nil {
			return diagnostic(path, "command_argument_type", fmt.Sprintf("command %s does not accept YAML aliases", keyword), alias, data)
		}
	}
	if schema.selector {
		_, err := parseSelector(path, data, node)
		return err
	}
	if schema.valueValidator != nil {
		return schema.valueValidator(path, data, node)
	}
	if schema.noArguments && node != nil && node.Kind == yaml.MappingNode {
		// No-argument commands support universal `optional` and `label` fields,
		// as well as an empty map.
		pairs, err := mappingPairs(path, data, node, "duplicate_command_argument")
		if err != nil {
			return err
		}
		for _, pair := range pairs {
			handled, uErr := universalCommandField(path, data, keyword, pair.key.Value, pair.value)
			if uErr != nil {
				return uErr
			}
			if !handled {
				return diagnostic(path, "unknown_command_field", fmt.Sprintf("command %s does not accept field %s", keyword, pair.key.Value), pair.key, data)
			}
		}
		return nil
	}
	if schema.valueKinds&yamlKind(node) == 0 {
		return diagnostic(path, "command_argument_type", fmt.Sprintf("command %s has an invalid argument type", keyword), node, data)
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_command_argument")
	if err != nil {
		return err
	}
	if schema.dynamicValidator != nil {
		return schema.dynamicValidator(path, data, node)
	}
	seen := make(map[string]struct{}, len(pairs))
	for _, pair := range pairs {
		rule, exists := schema.fields[pair.key.Value]
		if !exists {
			handled, uErr := universalCommandField(path, data, keyword, pair.key.Value, pair.value)
			if uErr != nil {
				return uErr
			}
			if handled {
				seen[pair.key.Value] = struct{}{}
				continue
			}
			if schema.allowUnknownFields {
				continue
			}
			return diagnostic(path, "unknown_command_field", fmt.Sprintf("unknown %s field %s", keyword, pair.key.Value), pair.key, data)
		}
		seen[pair.key.Value] = struct{}{}
		if rule.validator != nil {
			if err := rule.validator(path, data, pair.value); err != nil {
				return err
			}
			continue
		}
		if rule.kinds&yamlKind(pair.value) == 0 {
			return diagnostic(path, "command_argument_type", fmt.Sprintf("%s.%s has an invalid type", keyword, pair.key.Value), pair.value, data)
		}
		if len(rule.enum) > 0 {
			if err := validateEnum(path, data, keyword, pair.value, rule.enum); err != nil {
				return err
			}
		}
	}
	for name, rule := range schema.fields {
		if rule.required {
			if _, exists := seen[name]; !exists {
				return diagnostic(path, "missing_command_field", fmt.Sprintf("%s requires field %s", keyword, name), node, data)
			}
		}
	}
	if len(schema.requiredAny) > 0 {
		found := false
		for _, name := range schema.requiredAny {
			if _, exists := seen[name]; exists {
				found = true
				break
			}
		}
		if !found {
			return diagnostic(path, "missing_command_field", fmt.Sprintf("%s requires one of %s", keyword, strings.Join(schema.requiredAny, ", ")), node, data)
		}
	}
	return nil
}

func firstAliasNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode {
		return node
	}
	for _, child := range node.Content {
		if alias := firstAliasNode(child); alias != nil {
			return alias
		}
	}
	return nil
}

// universalCommandField handles `optional` and `label` for every command so
// individual schemas need not list them (spec 01-core-engine.md:32). `optional`
// is boolean; `label` is coerced from any scalar. Other keys return false.
func universalCommandField(path string, data []byte, keyword model.CommandKeyword, key string, value *yaml.Node) (bool, error) {
	switch key {
	case "optional":
		if _, err := coercedBoolValue(path, data, value, "command_argument_type", fmt.Sprintf("%s.optional", keyword)); err != nil {
			return true, err
		}
		return true, nil
	case "label":
		if yamlKind(value)&(yamlKindString|yamlKindInt|yamlKindFloat|yamlKindBool) == 0 {
			return true, diagnostic(path, "command_argument_type", fmt.Sprintf("%s.label must be a scalar", keyword), value, data)
		}
		return true, nil
	}
	return false, nil
}

func validateEnum(path string, data []byte, keyword model.CommandKeyword, node *yaml.Node, allowed []string) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return diagnostic(path, "command_argument_type", fmt.Sprintf("command %s requires a string enum", keyword), node, data)
	}
	for _, candidate := range allowed {
		if node.Value == candidate {
			return nil
		}
	}
	return diagnostic(path, "invalid_command_enum", fmt.Sprintf("invalid %s value %s; expected one of %s", keyword, node.Value, strings.Join(allowed, ", ")), node, data)
}

// validateEnumFold applies case-insensitive validation to direction and
// orientation enums. The engine sends their canonical case to the driver.
// setAirplaneMode remains case-sensitive.
func validateEnumFold(path string, data []byte, keyword model.CommandKeyword, node *yaml.Node, allowed []string) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return diagnostic(path, "command_argument_type", fmt.Sprintf("command %s requires a string enum", keyword), node, data)
	}
	for _, candidate := range allowed {
		if strings.EqualFold(node.Value, candidate) {
			return nil
		}
	}
	return diagnostic(path, "invalid_command_enum", fmt.Sprintf("invalid %s value %s; expected one of %s", keyword, node.Value, strings.Join(allowed, ", ")), node, data)
}

func yamlKind(node *yaml.Node) yamlKindMask {
	if node == nil {
		return yamlKindNull
	}
	if node.Kind == yaml.AliasNode {
		return yamlKind(node.Alias)
	}
	switch node.Kind {
	case yaml.MappingNode:
		return yamlKindMap
	case yaml.SequenceNode:
		return yamlKindSequence
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return yamlKindNull
		case "!!bool":
			return yamlKindBool
		case "!!int":
			return yamlKindInt
		case "!!float":
			return yamlKindFloat
		default:
			return yamlKindString
		}
	default:
		return 0
	}
}

func mapRule(fields map[string]commandFieldRule, requiredAny ...string) commandSchema {
	return commandSchema{valueKinds: yamlKindMap, fields: fields, requiredAny: requiredAny}
}

func optionalMapRule(fields map[string]commandFieldRule) commandSchema {
	return commandSchema{bare: true, valueKinds: yamlKindMap, fields: fields}
}

func noArgumentRule() commandSchema {
	return commandSchema{bare: true, noArguments: true}
}

func stringRule(bare bool) commandSchema {
	return commandSchema{bare: bare, valueKinds: yamlKindString}
}

// selectorRule accepts scalar text and object selectors. parseSelector applies
// the same scalar rule when it builds the typed selector.
func selectorRule() commandSchema {
	return commandSchema{valueKinds: scalarKinds | yamlKindMap, selector: true, scalarValueText: true}
}

// scalarKinds are the YAML kinds that carry their own text, so any of them can
// stand where a string is expected.
const scalarKinds = yamlKindString | yamlKindBool | yamlKindInt | yamlKindFloat

func enumRule(values ...string) commandSchema {
	return commandSchema{
		valueKinds: yamlKindString,
		valueValidator: func(path string, data []byte, node *yaml.Node) error {
			return validateEnum(path, data, "enum", node, values)
		},
	}
}

func stringField(required bool) commandFieldRule {
	return commandFieldRule{kinds: yamlKindString, required: required}
}

// scalarField accepts scalar YAML and stores its authored text.
func scalarField(required bool) commandFieldRule {
	return commandFieldRule{kinds: scalarKinds, required: required, scalarText: true}
}

// scalarRule accepts a bare scalar and stores its authored text.
func scalarRule(bare bool) commandSchema {
	return commandSchema{bare: bare, valueKinds: scalarKinds, scalarValueText: true}
}

// boolField accepts a native YAML boolean or a quoted case-insensitive "true"
// or "false" string. Other strings and interpolation are rejected.
func boolField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := coercedBoolValue(path, data, node, "command_argument_type", "field")
		return err
	}}
}

// intField accepts a native YAML integer or a quoted integer string.
// Non-integer strings and interpolation are rejected.
func intField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := coercedIntValue(path, data, node, "command_argument_type", "field")
		return err
	}}
}

func boundedIntField(required bool, field string, minimum, maximum int) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		value, err := coercedIntValue(path, data, node, "command_argument_type", field)
		if err != nil {
			return err
		}
		if value < minimum || value > maximum {
			return diagnostic(
				path,
				"invalid_command_value",
				fmt.Sprintf("%s must be an integer in [%d,%d]", field, minimum, maximum),
				node,
				data,
			)
		}
		return nil
	}}
}

func forbiddenField(field string) commandFieldRule {
	return commandFieldRule{validator: func(path string, data []byte, node *yaml.Node) error {
		return diagnostic(path, "invalid_command_value", fmt.Sprintf("%s is not accepted by this command", field), node, data)
	}}
}

func randomInputRule(fields map[string]commandFieldRule) commandSchema {
	rule := optionalMapRule(fields)
	rule.rejectAliases = true
	return rule
}

// numberField accepts a native YAML number or a quoted numeric string.
// Non-numeric strings and interpolation are rejected.
func numberField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := coercedNumberValue(path, data, node, "command_argument_type", "field")
		return err
	}}
}

func mapField(required bool) commandFieldRule {
	return commandFieldRule{kinds: yamlKindMap, required: required}
}

func selectorField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := parseSelector(path, data, node)
		return err
	}}
}

func conditionField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := parseCondition(path, data, node)
		return err
	}}
}

func commandsField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := parseCommandSequence(path, data, node)
		return err
	}}
}

func stringMapField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: func(path string, data []byte, node *yaml.Node) error {
		_, err := stringMap(path, data, node, "command_argument_type", "map")
		return err
	}}
}

func enumField(required bool, values ...string) commandFieldRule {
	return commandFieldRule{kinds: yamlKindString, required: required, enum: values}
}

func swipeDirectionField() commandFieldRule {
	return commandFieldRule{kinds: yamlKindString, validator: func(path string, data []byte, node *yaml.Node) error {
		if js.HasInterpolationExpression(node.Value) {
			return nil
		}
		return validateEnumFold(path, data, model.CommandSwipe, node, []string{"UP", "DOWN", "LEFT", "RIGHT"})
	}}
}

// swipeDurationField accepts a native integer or quoted integer. Interpolation
// and non-integer strings are invalid because duration is resolved at parse
// time; command timeouts remain runtime-resolved strings.
func swipeDurationField() commandFieldRule {
	return commandFieldRule{validator: func(path string, data []byte, node *yaml.Node) error {
		switch yamlKind(node) {
		case yamlKindInt:
			return nil
		case yamlKindString:
			if _, err := strconv.Atoi(node.Value); err == nil {
				return nil
			}
		}
		return diagnostic(path, "command_argument_type", "swipe.duration must be an integer", node, data)
	}}
}

func scrollUntilVisibleDirectionField() commandFieldRule {
	return commandFieldRule{kinds: yamlKindString, validator: func(path string, data []byte, node *yaml.Node) error {
		if js.HasInterpolationExpression(node.Value) {
			return nil
		}
		return validateEnumFold(path, data, model.CommandScrollUntilVisible, node, []string{"UP", "DOWN", "LEFT", "RIGHT"})
	}}
}

func validatePermissionMap(path string, data []byte, node *yaml.Node) error {
	pairs, err := mappingPairs(path, data, node, "duplicate_permission_field")
	if err != nil {
		return diagnostic(path, "command_argument_type", "permissions must be an object", node, data)
	}
	for _, pair := range pairs {
		if yamlKind(pair.value) != yamlKindString {
			return diagnostic(path, "command_argument_type", fmt.Sprintf("permission %s must be a string", pair.key.Value), pair.value, data)
		}
		if err := validateEnumFold(path, data, model.CommandSetPermissions, pair.value, []string{"allow", "deny", "unset"}); err != nil {
			return err
		}
	}
	return nil
}

func permissionMapField(required bool) commandFieldRule {
	return commandFieldRule{required: required, validator: validatePermissionMap}
}

func validateStringList(path string, data []byte, node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return diagnostic(path, "command_argument_type", "expected a string array", node, data)
	}
	for _, child := range node.Content {
		if yamlKind(child) != yamlKindString {
			return diagnostic(path, "command_argument_type", "array entries must be strings", child, data)
		}
	}
	return nil
}

// validateTravelPoints requires an array of strings. Coordinate contents are
// resolved at runtime, so empty arrays and unparseable point strings remain
// valid authored values as specified in specs/05-command-semantics-addendum.md.
func validateTravelPoints(path string, data []byte, node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return diagnostic(path, "command_argument_type", "travel points must be an array", node, data)
	}
	for _, point := range node.Content {
		if yamlKind(point) != yamlKindString {
			return diagnostic(path, "command_argument_type", "travel point must be a \"latitude, longitude\" string", point, data)
		}
	}
	return nil
}

func validateAirplaneMode(path string, data []byte, node *yaml.Node) error {
	return validateEnum(path, data, model.CommandSetAirplaneMode, node, []string{"enabled", "disabled"})
}

func validateAction(path string, data []byte, node *yaml.Node) error {
	return validateEnumFold(path, data, model.CommandAction, node, []string{"back", "hideKeyboard", "scroll", "clearKeychain", "pasteText"})
}

func validateOrientation(path string, data []byte, node *yaml.Node) error {
	return validateEnumFold(path, data, model.CommandSetOrientation, node, []string{"PORTRAIT", "LANDSCAPE_LEFT", "LANDSCAPE_RIGHT", "UPSIDE_DOWN"})
}

func validatePressKey(path string, data []byte, node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" && js.HasInterpolationExpression(node.Value) {
		return nil
	}
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return diagnostic(path, "command_argument_type", fmt.Sprintf("command %s requires a string enum", model.CommandPressKey), node, data)
	}
	// Key names are case-insensitive, and underscore and space spellings are
	// interchangeable (`volume_up` == `VOLUME UP`). The canonical key set and
	// normalization are defined in internal/model for parser and engine use.
	if _, ok := model.PressKeyCanonical(node.Value); ok {
		return nil
	}
	return diagnostic(path, "invalid_command_enum", fmt.Sprintf("invalid %s value %s; expected one of %s", model.CommandPressKey, node.Value, strings.Join(model.PressKeyCodes, ", ")), node, data)
}

func validateSwipe(path string, data []byte, node *yaml.Node) error {
	schema := commandSchema{
		valueKinds: yamlKindMap,
		fields: map[string]commandFieldRule{
			"direction":             swipeDirectionField(),
			"start":                 stringField(false),
			"end":                   stringField(false),
			"from":                  selectorField(false),
			"duration":              swipeDurationField(),
			"waitToSettleTimeoutMs": intField(false),
		},
		requiredAny: []string{"direction", "start", "from"},
	}
	if err := validateValueAgainstSchema(path, data, model.CommandSwipe, node, schema); err != nil {
		return err
	}
	pairs, err := mappingPairs(path, data, node, "duplicate_command_argument")
	if err != nil {
		return err
	}
	present := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		present[pair.key.Value] = true
	}
	hasDirection := present["direction"]
	hasStart := present["start"]
	hasEnd := present["end"]
	hasFrom := present["from"]
	if (hasFrom || hasDirection) && (hasStart || hasEnd) {
		return diagnostic(path, "conflicting_command_fields", "swipe variants are mutually exclusive", node, data)
	}
	if hasFrom && !hasDirection {
		return diagnostic(path, "missing_command_field", "swipe element variant requires from and direction", node, data)
	}
	if (hasStart || hasEnd) && (!hasStart || !hasEnd) {
		return diagnostic(path, "missing_command_field", "swipe coordinate variant requires both start and end", node, data)
	}
	if hasStart && hasEnd {
		return validateSwipeEndpoints(path, data, pairs)
	}
	return nil
}

// validateSwipeEndpoints enforces the half of the 4-variant union that
// specs/01-core-engine.md:28 puts behind one pair of keys: `start`/`end` carry
// BOTH the absolute-coordinate and the relative-coordinate variant, and which
// one an author gets is decided by whether the text contains a percent sign.
// See internal/flow/swipe_coordinates_test.go for the accepted variants.
func validateSwipeEndpoints(path string, data []byte, pairs []mappingPair) error {
	var startNode, endNode *yaml.Node
	for _, pair := range pairs {
		switch pair.key.Value {
		case "start":
			startNode = pair.value
		case "end":
			endNode = pair.value
		}
	}
	if startNode == nil || endNode == nil {
		return nil
	}
	startRelative, err := validateSwipeEndpoint(path, data, startNode, "start")
	if err != nil {
		return err
	}
	endRelative, err := validateSwipeEndpoint(path, data, endNode, "end")
	if err != nil {
		return err
	}
	if startRelative != endRelative {
		return diagnostic(path, "conflicting_command_fields",
			"swipe start and end must both be relative percentages or both be absolute coordinates",
			endNode, data)
	}
	return nil
}

// validateSwipeEndpoint identifies and validates relative percentage endpoints.
// Absolute endpoints retain scalar text for runtime resolution.
func validateSwipeEndpoint(path string, data []byte, node *yaml.Node, field string) (bool, error) {
	text := node.Value
	if !strings.Contains(text, "%") {
		return false, nil
	}
	components := strings.Split(text, ",")
	if len(components) != 2 {
		return true, diagnostic(path, "invalid_command_value",
			"swipe "+field+" must be two comma-separated percentages", node, data)
	}
	for _, component := range components {
		if !strictSwipePercentage(component) {
			return true, diagnostic(path, "invalid_command_value",
				"swipe "+field+" percentages must be whole numbers in [0,100]", node, data)
		}
	}
	return true, nil
}

// strictSwipePercentage accepts a whole number in [0,100] with an optional
// percent suffix. The suffix is per-component under the contract: "50%,100" and
// "100,50%" are both relative and both accepted.
func strictSwipePercentage(component string) bool {
	trimmed := strings.TrimSuffix(strings.TrimSpace(component), "%")
	value, err := strconv.Atoi(trimmed)
	if err != nil || trimmed != strconv.Itoa(value) {
		return false
	}
	return value >= 0 && value <= 100
}

func validateEraseText(path string, data []byte, node *yaml.Node) error {
	if yamlKind(node) == yamlKindString {
		if js.HasInterpolationExpression(node.Value) {
			return nil
		}
		if !strictEraseTextCount(node.Value) {
			return diagnostic(path, "invalid_command_value", "eraseText count must be a strict base-10 integer in [0,100]", node, data)
		}
		return nil
	}
	value, err := intValue(path, data, node, "command_argument_type", "eraseText count")
	if err != nil {
		return err
	}
	if value < 0 || value > 100 {
		return diagnostic(path, "invalid_command_value", "eraseText count must be in [0,100]", node, data)
	}
	return nil
}

func strictEraseTextCount(raw string) bool {
	if raw == "" {
		return false
	}
	for _, character := range raw {
		if character < '0' || character > '9' {
			return false
		}
	}
	value, err := strconv.ParseUint(raw, 10, 32)
	return err == nil && value <= 100
}

var commandSchemaV0 map[model.CommandKeyword]commandSchema

func init() {
	commandSchemaV0 = buildCommandSchemaV0()
}

func buildCommandSchemaV0() map[model.CommandKeyword]commandSchema {
	return map[model.CommandKeyword]commandSchema{
		model.CommandLaunchApp: {
			bare:       true,
			valueKinds: yamlKindMap,
			fields: map[string]commandFieldRule{
				"appId":         stringField(false),
				"clearState":    boolField(false),
				"clearKeychain": boolField(false),
				"stopApp":       boolField(false),
				"permissions":   permissionMapField(false),
				"arguments":     mapField(false),
			},
		},
		model.CommandStopApp:       stringRule(true),
		model.CommandKillApp:       stringRule(true),
		model.CommandClearState:    stringRule(true),
		model.CommandClearKeychain: noArgumentRule(),
		// setPermissions wraps the grant map under the required `permissions` key
		// documented by 04-wire-protocols.md:54 (`{permissions:{k:v}}`). See
		// contract_link_test.go TestSetPermissionsNestedPermissionsMap.
		model.CommandSetPermissions: mapRule(map[string]commandFieldRule{
			"permissions": permissionMapField(true),
		}),
		model.CommandTapOn:            selectorRule(),
		model.CommandDoubleTapOn:      selectorRule(),
		model.CommandLongPressOn:      selectorRule(),
		model.CommandAssertVisible:    selectorRule(),
		model.CommandAssertNotVisible: selectorRule(),
		model.CommandAssertTrue: {
			// Bare takes any scalar, condition does not — the contract draws
			// exactly that line. See scalar_selector_test.go.
			valueKinds:      scalarKinds | yamlKindMap,
			scalarValueText: true,
			fields: map[string]commandFieldRule{
				"condition": stringField(true),
				"label":     stringField(false),
				"optional":  boolField(false),
			},
		},
		model.CommandAssertNoDefectsWithAI: optionalMapRule(map[string]commandFieldRule{
			"label": stringField(false), "optional": boolField(false),
		}),
		model.CommandAssertScreenshot: {
			valueKinds: yamlKindString | yamlKindMap,
			fields: map[string]commandFieldRule{
				"path": stringField(true),
				// specs/01-core-engine.md declares thresholdPercentage;
				// threshold is not an authored field.
				"thresholdPercentage": numberField(false),
				"cropOn":              selectorField(false),
			},
		},
		model.CommandAssertWithAI: {
			valueKinds: yamlKindString | yamlKindMap,
			fields: map[string]commandFieldRule{
				"assertion": stringField(true),
				"label":     stringField(false),
				"optional":  boolField(false),
			},
		},
		// query is required and outputVariable is optional. See
		// contract_link_test.go TestExtractTextWithAIOutputVariableOptional.
		model.CommandExtractTextWithAI: mapRule(map[string]commandFieldRule{
			"query": stringField(true), "outputVariable": stringField(false), "label": stringField(false), "optional": boolField(false),
		}),
		model.CommandBack:         noArgumentRule(),
		model.CommandHideKeyboard: noArgumentRule(),
		model.CommandPasteText:    noArgumentRule(),
		model.CommandScroll:       noArgumentRule(),
		model.CommandScrollUntilVisible: mapRule(map[string]commandFieldRule{
			"element": selectorField(true), "direction": scrollUntilVisibleDirectionField(),
			// `speed` is the authored name; specs/01-core-engine.md:36 records
			// the speed→duration normalization that turns it into the command's
			// scrollDuration. The authored surface does not expose scrollDuration,
			// so a shape error remains at parse time. speed is scalar text and is
			// resolved at runtime, including interpolation. retry.maxRetries uses the
			// same scalar-text shape; travel.speed remains numeric.
			"speed":                {kinds: yamlKindString | yamlKindInt},
			"visibilityPercentage": intField(false), "timeout": {kinds: yamlKindInt | yamlKindString},
			"waitToSettleTimeoutMs": intField(false), "centerElement": boolField(false),
		}),
		// inputText supports a scalar shorthand or an object with optional string
		// text and label fields. Unknown object keys are ignored. Object labels are
		// string-only; universal command labels permit any scalar.
		model.CommandInputText: {
			// The bare value is scalar text; the named text field is string-only.
			valueKinds:         scalarKinds | yamlKindMap,
			scalarValueText:    true,
			allowUnknownFields: true,
			fields: map[string]commandFieldRule{
				"text": stringField(false), "label": stringField(false),
			},
		},
		model.CommandInputRandomText:   randomInputRule(map[string]commandFieldRule{"length": boundedIntField(false, "length", 0, 1024)}),
		model.CommandInputRandomNumber: randomInputRule(map[string]commandFieldRule{"length": boundedIntField(false, "length", 0, 1024)}),
		model.CommandInputRandomEmail: randomInputRule(map[string]commandFieldRule{
			"length": forbiddenField("length"),
		}),
		model.CommandInputRandomPersonName: randomInputRule(map[string]commandFieldRule{
			"length": forbiddenField("length"),
		}),
		model.CommandInputRandomCityName: randomInputRule(map[string]commandFieldRule{
			"length": forbiddenField("length"),
		}),
		model.CommandInputRandomCountryName: randomInputRule(map[string]commandFieldRule{
			"length": forbiddenField("length"),
		}),
		model.CommandInputRandomColorName: randomInputRule(map[string]commandFieldRule{
			"length": forbiddenField("length"),
		}),
		model.CommandSwipe: {valueKinds: yamlKindMap, valueValidator: validateSwipe},
		model.CommandOpenLink: {
			// A non-string bare value is invalid; object link accepts scalar text.
			valueKinds: yamlKindString | yamlKindMap,
			fields: map[string]commandFieldRule{
				// appId belongs to the driver method (specs/02-device-drivers.md:9),
				// not the authored command. Authors cannot set it here.
				"link": scalarField(true), "autoVerify": boolField(false),
				"browser": boolField(false),
			},
		},
		model.CommandOpenBrowser:    scalarRule(false),
		model.CommandPressKey:       {valueKinds: yamlKindString, valueValidator: validatePressKey},
		model.CommandEraseText:      {bare: true, valueKinds: yamlKindInt | yamlKindString, valueValidator: validateEraseText},
		model.CommandAction:         {valueKinds: yamlKindString, valueValidator: validateAction},
		model.CommandTakeScreenshot: stringRule(false),
		model.CommandExtendedWaitUntil: mapRule(map[string]commandFieldRule{
			"visible": selectorField(false), "notVisible": selectorField(false),
			"timeout": {kinds: yamlKindInt | yamlKindString},
		}, "visible", "notVisible"),
		model.CommandRunFlow: mapOrStringSchema(map[string]commandFieldRule{
			"file": stringField(false), "env": stringMapField(false), "when": conditionField(false),
			"commands": commandsField(false), "label": stringField(false), "optional": boolField(false),
		}, "file", "commands"),
		// Coordinates remain scalar text until runtime, which permits interpolation.
		// Both fields are required even though their values resolve later.
		model.CommandSetLocation: mapRule(map[string]commandFieldRule{
			"latitude":  {kinds: yamlKindString | yamlKindInt | yamlKindFloat, required: true},
			"longitude": {kinds: yamlKindString | yamlKindInt | yamlKindFloat, required: true},
		}),
		model.CommandSetOrientation: {valueKinds: yamlKindString, valueValidator: validateOrientation},
		model.CommandRepeat: mapRule(map[string]commandFieldRule{
			"times": {kinds: yamlKindString | yamlKindInt}, "while": conditionField(false), "commands": commandsField(true),
		}),
		model.CommandRetry: mapRule(map[string]commandFieldRule{
			"maxRetries": {kinds: yamlKindString | yamlKindInt}, "commands": commandsField(false), "file": stringField(false),
		}, "commands", "file"),
		model.CommandCopyTextFrom: selectorRule(),
		model.CommandSetClipboard: stringRule(false),
		model.CommandRunScript: mapOrStringSchema(map[string]commandFieldRule{
			"file": stringField(true), "env": stringMapField(false), "when": conditionField(false), "label": stringField(false),
		}),
		model.CommandEvalScript:            scalarRule(false),
		model.CommandWaitForAnimationToEnd: optionalMapRule(map[string]commandFieldRule{"timeout": {kinds: yamlKindInt | yamlKindString}}),
		model.CommandTravel: mapRule(map[string]commandFieldRule{
			"points": {required: true, validator: validateTravelPoints}, "speed": numberField(false),
		}),
		model.CommandStartRecording: stringRule(false),
		model.CommandStopRecording:  noArgumentRule(),
		// addMedia requires a non-empty list of paths; a bare scalar is invalid.
		model.CommandAddMedia:           {valueKinds: yamlKindSequence, valueValidator: validateStringList},
		model.CommandSetAirplaneMode:    {valueKinds: yamlKindString, valueValidator: validateAirplaneMode},
		model.CommandToggleAirplaneMode: noArgumentRule(),
	}
}

func mapOrStringSchema(fields map[string]commandFieldRule, requiredAny ...string) commandSchema {
	return commandSchema{valueKinds: yamlKindString | yamlKindMap, fields: fields, requiredAny: requiredAny}
}

// coerceScalarText carries accepted scalar text into typed engine decoders.
// Coercion is explicit per rule because commands such as eraseText retain
// numeric semantics.
func coerceScalarText(
	keyword model.CommandKeyword, node *yaml.Node, arguments any,
) any {
	schema, ok := commandSchemaV0[keyword]
	if !ok || node == nil {
		return arguments
	}
	if schema.scalarValueText && node.Kind == yaml.ScalarNode && node.Tag != "!!str" {
		return node.Value
	}
	fields, isMap := arguments.(map[string]any)
	if !isMap || node.Kind != yaml.MappingNode {
		return arguments
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		name, value := node.Content[index], node.Content[index+1]
		rule, known := schema.fields[name.Value]
		if !known || !rule.scalarText {
			continue
		}
		if value.Kind == yaml.ScalarNode && value.Tag != "!!str" {
			fields[name.Value] = value.Value
		}
	}
	return fields
}
