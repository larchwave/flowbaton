package flow

import (
	"strings"
	"testing"
)

// Selector text accepts any YAML scalar and preserves its authored text.
// Collections and missing values remain invalid. assertTrue.condition retains
// its separate boolean and string rules.

// mustParse supplies the shared flow header through parses.
func mustParse(t *testing.T, body string) {
	t.Helper()
	if err := parses(t, body); err != nil {
		t.Fatalf("parses(%q) error = %v", body, err)
	}
}

func TestABareSelectorMayBeANonStringScalar(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"assertVisible", "assertNotVisible", "tapOn", "copyTextFrom"} {
		mustParse(t, "- "+command+": 42")
	}
}

func TestSelectorTextAndIDMayBeNonStringScalars(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		"- tapOn:\n    id: 42",
		"- assertVisible:\n    text: 42",
		"- assertVisible:\n    text: true",
		"- tapOn:\n    text: 3.5",
	} {
		mustParse(t, body)
	}
}

// Nested selector fields use the same scalar rule.
func TestNestedSelectorsMayBeNonStringScalars(t *testing.T) {
	t.Parallel()

	mustParse(t, "- scrollUntilVisible:\n    element:\n      text: 42")
	mustParse(t, "- extendedWaitUntil:\n    visible:\n      text: 42\n    timeout: 1000")
}

// The scalar keeps its text exactly as written, which is what a matcher needs:
// a selector for 42 must look for "42", not for 42 formatted some other way.
func TestAScalarSelectorKeepsItsText(t *testing.T) {
	t.Parallel()

	flow, err := ParseBytes("flow.yaml", []byte("appId: a\n---\n- assertVisible: 0042\n"))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	_ = flow
	selector := flow.Commands[0].Selector
	if selector == nil || selector.TextRegex == nil || *selector.TextRegex != "0042" {
		t.Fatalf("selector = %+v, want the text 0042 as written", selector)
	}
}

// Widening is not the same as accepting anything. A map or a sequence where a
// selector value belongs is still a mistake, and so is a missing value.
func TestAScalarSelectorIsStillNotAMapOrASequence(t *testing.T) {
	t.Parallel()

	for _, source := range []string{
		"appId: a\n---\n- assertVisible:\n    text:\n      - 42\n",
		"appId: a\n---\n- tapOn:\n    id:\n      nested: 42\n",
	} {
		if _, err := ParseBytes("flow.yaml", []byte(source)); err == nil {
			t.Fatalf("ParseBytes(%q) was accepted", source)
		} else if !strings.Contains(err.Error(), "selector_field_type") {
			t.Fatalf("error = %v, want a selector_field_type diagnostic", err)
		}
	}
}

// assertTrue, inputText, evalScript, and openBrowser accept bare scalar text.
// setClipboard, openLink, takeScreenshot, startRecording, and runScript reject
// non-string bare values.

func TestSupportedBareCommandScalarsBecomeText(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"assertTrue", "inputText", "evalScript", "openBrowser"} {
		mustParse(t, "- "+command+": 42")
	}
}

func TestUnsupportedBareCommandScalarsAreRejected(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"setClipboard", "openLink", "takeScreenshot", "startRecording", "runScript"} {
		if err := parses(t, "- "+command+": 42"); err == nil {
			t.Fatalf("%s accepted a bare 42, which the contract refuses", command)
		}
	}
}

// openLink rejects a non-string bare value and accepts scalar text in its
// object form.
func TestOpenLinkTakesAScalarInItsMapFormOnly(t *testing.T) {
	t.Parallel()

	mustParse(t, "- openLink:\n    link: 42")
}

// assertTrue stringifies its bare scalar form but keeps condition string-only.
func TestAssertTrueRefusesABooleanInItsMapForm(t *testing.T) {
	t.Parallel()

	if err := parses(t, "- assertTrue:\n    condition: true"); err == nil {
		t.Fatal("assertTrue.condition accepted a boolean, which the contract refuses")
	}
}

// Schema scalar coercion stores text in Command.Arguments so engine decoders
// receive strings.

func TestAScalarArgumentReachesTheEngineAsText(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ body, want string }{
		{"- inputText: 12345", "12345"},
		{"- assertTrue: 42", "42"},
		{"- evalScript: 5", "5"},
		{"- openBrowser: 42", "42"},
	} {
		flow, err := ParseBytes("flow.yaml", []byte("appId: a\n---\n"+test.body+"\n"))
		if err != nil {
			t.Fatalf("ParseBytes(%q) error = %v", test.body, err)
		}
		if got, ok := flow.Commands[0].Arguments.(string); !ok || got != test.want {
			t.Fatalf("%q arguments = %#v, want the string %q", test.body, flow.Commands[0].Arguments, test.want)
		}
	}
}

func TestAScalarFieldReachesTheEngineAsText(t *testing.T) {
	t.Parallel()

	flow, err := ParseBytes("flow.yaml", []byte("appId: a\n---\n- openLink:\n    link: 42\n"))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	fields, ok := flow.Commands[0].Arguments.(map[string]any)
	if !ok {
		t.Fatalf("arguments = %#v, want a map", flow.Commands[0].Arguments)
	}
	if fields["link"] != "42" {
		t.Fatalf("link = %#v, want the string 42", fields["link"])
	}
}

// Scalar-to-text coercion is explicit. eraseText retains its numeric count.
func TestANumericArgumentThatIsMeantToBeANumberStaysOne(t *testing.T) {
	t.Parallel()

	flow, err := ParseBytes("flow.yaml", []byte("appId: a\n---\n- eraseText: 5\n"))
	if err != nil {
		t.Fatalf("ParseBytes() error = %v", err)
	}
	if _, isText := flow.Commands[0].Arguments.(string); isText {
		t.Fatalf("eraseText arguments = %#v, want a number", flow.Commands[0].Arguments)
	}
}
