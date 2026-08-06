package flow

import "testing"

// These tests define parser behavior for case normalization, universal
// metadata, scalar coercion, and command value sets.
// pressKey names are case-insensitive, with `_` and spaces interchangeable.
// Every command supports the universal `optional` and `label` fields described
// in specs/01-core-engine.md:32.
func parses(t *testing.T, body string) error {
	t.Helper()
	yaml := "appId: com.example\n---\n" + body + "\n"
	_, err := ParseBytes("/workspace/contract.yaml", []byte(yaml))
	return err
}

func TestPressKeyIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	// The v0 key set includes standard device keys and Android TV remote keys.
	// Matching is case-insensitive, with spaces and underscores interchangeable.
	for _, key := range []string{
		"Enter", "ENTER", "enter", "EnTeR", "Home", "Back", "Lock", "Power",
		"VOLUME UP", "volume_up", "Volume Down", "backspace", "TAB",
		"Remote Dpad Up", "Remote Dpad Down", "Remote Dpad Left", "Remote Dpad Right", "Remote Dpad Center",
		"Remote Media Play Pause", "Remote Media Stop", "Remote Media Next", "Remote Media Previous",
		"Remote Media Rewind", "Remote Media Fast Forward",
		"Remote System Navigation Up", "Remote System Navigation Down",
		"Remote Button A", "Remote Button B", "Remote Menu",
		"TV Input", "TV Input HDMI 1", "TV Input HDMI 2", "TV Input HDMI 3",
		"remote_dpad_up", "TV_INPUT_HDMI_1",
	} {
		if err := parses(t, "- pressKey: '"+key+"'"); err != nil {
			t.Errorf("pressKey %q rejected supported value: %v", key, err)
		}
	}
	// Invalid names fail during parsing. REMOTE_UP is not a key; the supported
	// spelling is Remote Dpad Up.
	for _, key := range []string{"", " ", "unknown", "REMOTE_UP", "REMOTE"} {
		if err := parses(t, "- pressKey: '"+key+"'"); err == nil {
			t.Errorf("pressKey %q accepted but is not a supported key", key)
		}
	}
}

func TestSelectorIndexAcceptsStringAndInterpolation(t *testing.T) {
	t.Parallel()

	// `index` is string-typed so quoted integers and interpolation reach runtime
	// resolution.
	for _, value := range []string{"2", "\"2\"", "-1", "${i}", "\"-1\"", "\"prefix-${i}\"", "\"abc\""} {
		if err := parses(t, "- tapOn:\n    text: Item\n    index: "+value); err != nil {
			t.Errorf("index %s rejected supported value: %v", value, err)
		}
	}
}

// TestSetAirplaneModeAcceptsEnabledAndDisabled covers the two case-sensitive
// mode tokens.
func TestSetAirplaneModeAcceptsEnabledAndDisabled(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"enabled", "disabled"} {
		if err := parses(t, "- setAirplaneMode: "+value); err != nil {
			t.Errorf("setAirplaneMode: %s rejected supported value: %v", value, err)
		}
	}
	for _, value := range []string{"enable", "disable", "Enabled", "on", "true"} {
		if err := parses(t, "- setAirplaneMode: "+value); err == nil {
			t.Errorf("setAirplaneMode: %s accepted unsupported value", value)
		}
	}
}

func TestTimeoutFieldsAcceptStringAndInterpolation(t *testing.T) {
	t.Parallel()

	// Command timeouts are string-typed. Quoted integers and interpolation
	// expressions reach runtime millisecond resolution.
	bodies := []string{
		"- extendedWaitUntil:\n    visible:\n      text: Ready\n    timeout: ${t}",
		"- extendedWaitUntil:\n    visible:\n      text: Ready\n    timeout: \"5000\"",
		"- waitForAnimationToEnd:\n    timeout: ${t}",
		"- waitForAnimationToEnd:\n    timeout: \"5000\"",
	}
	for _, body := range bodies {
		if err := parses(t, body); err != nil {
			t.Errorf("%q rejected supported value: %v", body, err)
		}
	}
}

func TestSwipeDurationAcceptsNumericStringNotInterpolation(t *testing.T) {
	t.Parallel()

	// swipe.duration is integer-typed and coerces a native integer or numeric
	// string. Interpolation is invalid because it does not parse as an integer.
	const coords = "    start: \"10%, 50%\"\n    end: \"90%, 50%\"\n"
	for _, dur := range []string{"400", "\"400\""} {
		if err := parses(t, "- swipe:\n"+coords+"    duration: "+dur); err != nil {
			t.Errorf("swipe duration %s rejected supported value: %v", dur, err)
		}
	}
	for _, dur := range []string{"${d}", "\"${d}\"", "\"abc\""} {
		if err := parses(t, "- swipe:\n"+coords+"    duration: "+dur); err == nil {
			t.Errorf("swipe duration %s accepted unsupported value", dur)
		}
	}
}

func TestSelectorNumericAndBoolFieldsCoerceQuotedStrings(t *testing.T) {
	t.Parallel()

	// Selector numeric and boolean fields coerce quoted values while rejecting
	// text outside their target type.
	accepted := []string{
		"- tapOn:\n    text: Hi\n    width: \"100\"\n    height: \"50\"",
		"- tapOn:\n    point: \"50%,50%\"\n    tolerance: \"10\"",
		"- tapOn:\n    text: Hi\n    repeat: \"3\"\n    delay: \"100\"",
		"- tapOn:\n    text: Hi\n    waitToSettleTimeoutMs: \"500\"",
		"- tapOn:\n    text: Hi\n    enabled: \"true\"",
		"- tapOn:\n    text: Hi\n    checked: \"false\"\n    focused: \"True\"",
		"- tapOn:\n    text: Hi\n    selected: \"FALSE\"",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("coercible authored form was rejected:\n%s\n%v", body, err)
		}
	}
	// Non-coercible values remain invalid.
	rejected := []string{
		"- tapOn:\n    text: Hi\n    width: \"abc\"",
		"- tapOn:\n    text: Hi\n    enabled: \"yes\"",
		"- tapOn:\n    text: Hi\n    enabled: \"1\"",
	}
	for _, body := range rejected {
		if err := parses(t, body); err == nil {
			t.Errorf("parser accepted non-coercible value:\n%s", body)
		}
	}
}

func TestSetPermissionsGrantsAreCaseInsensitive(t *testing.T) {
	t.Parallel()

	// Permission grants are case-insensitive over allow, deny, and unset.
	for _, grant := range []string{"allow", "Allow", "ALLOW", "deny", "Deny", "unset", "UNSET"} {
		if err := parses(t, "- setPermissions:\n    permissions:\n      camera: "+grant); err != nil {
			t.Errorf("permission grant %s rejected supported value: %v", grant, err)
		}
	}
	for _, grant := range []string{"grant", "always", "noop", "yes"} {
		if err := parses(t, "- setPermissions:\n    permissions:\n      camera: "+grant); err == nil {
			t.Errorf("permission grant %s accepted but is not a real grant value", grant)
		}
	}
}

func TestScrollUntilVisibleAcceptsHorizontalDirections(t *testing.T) {
	t.Parallel()

	// scrollUntilVisible supports all four compass directions without regard to
	// case. Any other direction is invalid.
	for _, dir := range []string{"UP", "DOWN", "LEFT", "RIGHT", "left", "right"} {
		if err := parses(t, "- scrollUntilVisible:\n    element:\n      text: Hi\n    direction: "+dir); err != nil {
			t.Errorf("scrollUntilVisible direction %s rejected supported value: %v", dir, err)
		}
	}
	if err := parses(t, "- scrollUntilVisible:\n    element:\n      text: Hi\n    direction: diagonal"); err == nil {
		t.Errorf("scrollUntilVisible direction diagonal accepted but is not a valid direction")
	}
}

func TestCommandNumericFieldsCoerceQuotedStrings(t *testing.T) {
	t.Parallel()

	// Command numeric fields coerce quoted numbers and reject non-numeric text.
	accepted := []string{
		"- scrollUntilVisible:\n    element:\n      text: Hi\n    speed: \"40\"\n    visibilityPercentage: \"50\"",
		"- scrollUntilVisible:\n    element:\n      text: Hi\n    waitToSettleTimeoutMs: \"500\"",
		"- inputRandomText:\n    length: \"8\"",
		"- setLocation:\n    latitude: \"48.0\"\n    longitude: \"2.0\"",
		"- travel:\n    points:\n      - \"48.0, 2.0\"\n      - \"48.001, 2.0\"\n    speed: \"1000\"",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("coercible authored form was rejected:\n%s\n%v", body, err)
		}
	}
	// String-typed fields are covered separately; these fields remain numeric.
	rejected := []string{
		"- inputRandomText:\n    length: \"eight\"",
		"- travel:\n    points:\n      - \"48.0, 2.0\"\n    speed: \"fast\"",
		"- scrollUntilVisible:\n    element:\n      text: Hi\n    visibilityPercentage: \"most\"",
	}
	for _, body := range rejected {
		if err := parses(t, body); err == nil {
			t.Errorf("parser accepted non-numeric value:\n%s", body)
		}
	}
}

func TestCommandBoolFieldsCoerceQuotedStrings(t *testing.T) {
	t.Parallel()

	// Command boolean fields coerce quoted booleans. This includes universal
	// optional metadata, launchApp flags, and openLink.browser.
	accepted := []string{
		"- back:\n    optional: \"true\"",
		"- scroll:\n    optional: \"false\"",
		"- launchApp:\n    appId: com.x\n    clearState: \"true\"\n    clearKeychain: \"false\"\n    stopApp: \"true\"",
		"- openLink:\n    link: https://x.invalid\n    browser: \"true\"\n    autoVerify: \"false\"",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("coercible authored form was rejected:\n%s\n%v", body, err)
		}
	}
	rejected := []string{
		"- back:\n    optional: \"yes\"",
		"- launchApp:\n    appId: com.x\n    clearState: \"1\"",
		"- openLink:\n    link: https://x.invalid\n    browser: \"maybe\"",
	}
	for _, body := range rejected {
		if err := parses(t, body); err == nil {
			t.Errorf("parse error = nil for non-boolean value:\n%s", body)
		}
	}
}

func TestUniversalOptionalAndLabelOnEveryCommand(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"- hideKeyboard:\n    optional: true",
		"- hideKeyboard:\n    label: hide it",
		"- hideKeyboard: {}",
		"- back:\n    optional: true\n    label: go back",
		"- back:\n    label: 123", // numeric labels are coerced to strings
		"- scroll:\n    optional: true",
		"- launchApp:\n    appId: com.x\n    optional: true",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("documented authored form was rejected:\n%s\n%v", body, err)
		}
	}

	rejected := []struct{ why, body string }{
		{"optional must be bool", "- hideKeyboard:\n    optional: \"yes\""},
		{"unknown field still rejected", "- hideKeyboard:\n    foo: bar"},
		{"scalar arg on no-arg command", "- hideKeyboard: value"},
	}
	for _, tc := range rejected {
		if err := parses(t, tc.body); err == nil {
			t.Errorf("%s: unsupported authored form was accepted:\n%s", tc.why, tc.body)
		}
	}
}

// TestInputTextMapFormAcceptsDocumentedFields defines the inputText map surface.
// inputText supports a bare string or a map. In map form, `text` and `label` are
// optional strings, `optional` is a bool, and unknown keys are ignored. The map
// form keeps `label` string-only; the universal scalar form coerces scalar labels.
func TestInputTextMapFormAcceptsDocumentedFields(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"- inputText: hello",            // string shorthand
		"- inputText:\n    text: hello", // map with text
		"- inputText:\n    text: hello\n    label: greeting",
		"- inputText:\n    label: greeting",          // no text
		"- inputText:\n    optional: true",           // optional only
		"- inputText:\n    mystery: z",               // unknown key ignored
		"- inputText:\n    text: ok\n    paste: yes", // known text + ignored unknown
		"- inputText: {}",                            // empty map
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("documented authored form was rejected:\n%s\n%v", body, err)
		}
	}

	rejected := []struct{ why, body string }{
		{"text must be a string, not a sequence", "- inputText:\n    text:\n      - a\n      - b"},
		{"text must be a string, not an int", "- inputText:\n    text: 123"},
		// The map form requires a string label.
		{"label must be a string, not an int", "- inputText:\n    text: ok\n    label: 7"},
	}
	for _, tc := range rejected {
		if err := parses(t, tc.body); err == nil {
			t.Errorf("%s: unsupported authored form was accepted:\n%s", tc.why, tc.body)
		}
	}
}

// TestDirectionAndOrientationEnumsCaseInsensitive defines case-insensitive
// direction and orientation values. The engine sends canonical uppercase values
// to the driver. setAirplaneMode remains case-sensitive.
func TestDirectionAndOrientationEnumsCaseInsensitive(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"- swipe:\n    direction: up",
		"- swipe:\n    direction: Left",
		"- swipe:\n    direction: RIGHT",
		"- scrollUntilVisible:\n    element:\n      text: X\n    direction: down",
		"- scrollUntilVisible:\n    element:\n      text: X\n    direction: Up",
		"- setOrientation: portrait",
		"- setOrientation: landscape_left",
		"- setOrientation: Upside_Down",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("documented authored form was rejected:\n%s\n%v", body, err)
		}
	}

	rejected := []struct{ why, body string }{
		{"swipe direction not a compass value", "- swipe:\n    direction: sideways"},
		{"orientation not a supported value", "- setOrientation: tilted"},
		{"scroll direction not a compass value", "- scrollUntilVisible:\n    element:\n      text: X\n    direction: diagonal"},
	}
	for _, tc := range rejected {
		if err := parses(t, tc.body); err == nil {
			t.Errorf("%s: unsupported authored form was accepted:\n%s", tc.why, tc.body)
		}
	}
}

// TestActionAliasCaseInsensitive defines case-insensitive action aliases. The
// engine maps each alias to its canonical camelCase keyword.
func TestActionAliasCaseInsensitive(t *testing.T) {
	t.Parallel()

	for _, alias := range []string{"back", "Back", "BACK", "hideKeyboard", "hidekeyboard", "HIDEKEYBOARD", "scroll", "Scroll", "pasteText", "pastetext"} {
		if err := parses(t, "- action: "+alias); err != nil {
			t.Errorf("action %q rejected supported value: %v", alias, err)
		}
	}
	// Unknown aliases remain invalid.
	for _, alias := range []string{"forward", "tapOn", "swipe"} {
		if err := parses(t, "- action: "+alias); err == nil {
			t.Errorf("action %q accepted but is not a supported alias", alias)
		}
	}
}

// TestSetPermissionsNestedPermissionsMap defines the v0 shape documented in
// 04-wire-protocols.md:54. The object requires a `permissions` map whose values
// use the allow, deny, or unset vocabulary.
func TestSetPermissionsNestedPermissionsMap(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"- setPermissions:\n    permissions:\n      camera: allow",
		"- setPermissions:\n    permissions:\n      camera: allow\n      location: deny\n      notifications: unset",
		"- setPermissions:\n    permissions: {}", // empty inner map is accepted
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("documented authored form was rejected:\n%s\n%v", body, err)
		}
	}

	rejected := []struct{ why, body string }{
		{"direct map without the permissions wrapper", "- setPermissions:\n    camera: allow"},
		{"permissions must be a map, not a list", "- setPermissions:\n    permissions:\n      - camera"},
		{"unknown sibling key rejected", "- setPermissions:\n    permissions:\n      camera: allow\n    foo: bar"},
		{"empty object has no permissions field", "- setPermissions: {}"},
	}
	for _, tc := range rejected {
		if err := parses(t, tc.body); err == nil {
			t.Errorf("%s: unsupported authored form was accepted:\n%s", tc.why, tc.body)
		}
	}
}

// TestPlatformConditionCaseInsensitive defines case-insensitive platform names
// for runFlow and repeat conditions. Web is syntactically valid and fails closed
// later during capability preflight.
func TestPlatformConditionCaseInsensitive(t *testing.T) {
	t.Parallel()

	for _, plat := range []string{"iOS", "IOS", "ios", "iOs", "Android", "ANDROID", "android", "aNdRoId", "Web", "WEB", "web"} {
		body := "- runFlow:\n    when:\n      platform: " + plat + "\n    commands:\n      - back"
		if err := parses(t, body); err != nil {
			t.Errorf("platform %q rejected supported value: %v", plat, err)
		}
	}
	// A genuinely unknown platform stays rejected.
	for _, plat := range []string{"windows", "desktop", "linux"} {
		body := "- runFlow:\n    when:\n      platform: " + plat + "\n    commands:\n      - back"
		if err := parses(t, body); err == nil {
			t.Errorf("platform %q accepted but is not a known platform", plat)
		}
	}
}

// TestExtractTextWithAIOutputVariableOptional pins the authored contract:
// `query` is required and `outputVariable` is optional.
func TestExtractTextWithAIOutputVariableOptional(t *testing.T) {
	t.Parallel()

	accepted := []string{
		"- extractTextWithAI:\n    query: the total",                        // query only, no outputVariable
		"- extractTextWithAI:\n    query: the total\n    outputVariable: v", // both
		"- extractTextWithAI:\n    query: the total\n    optional: true",
	}
	for _, body := range accepted {
		if err := parses(t, body); err != nil {
			t.Errorf("documented authored form was rejected:\n%s\n%v", body, err)
		}
	}

	rejected := []struct{ why, body string }{
		{"query is required", "- extractTextWithAI:\n    outputVariable: v"},
		{"query must not be empty", "- extractTextWithAI: {}"},
		{"bare form has no required query", "- extractTextWithAI"},
	}
	for _, tc := range rejected {
		if err := parses(t, tc.body); err == nil {
			t.Errorf("%s: unsupported authored form was accepted:\n%s", tc.why, tc.body)
		}
	}
}
