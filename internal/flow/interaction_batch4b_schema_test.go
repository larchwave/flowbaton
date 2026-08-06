package flow

import (
	"fmt"
	"testing"
)

func TestInteractionBatch4HSchemaRejectsLengthForNamedKinds(t *testing.T) {
	t.Parallel()

	for _, keyword := range []string{
		"inputRandomEmail",
		"inputRandomPersonName",
		"inputRandomCityName",
		"inputRandomCountryName",
		"inputRandomColorName",
	} {
		keyword := keyword
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf("appId: com.example.batch4h\n---\n- %s: {length: 0}\n", keyword)
			if _, err := ParseBytes("/workspace/batch4h-named-length.yaml", []byte(input)); err == nil {
				t.Fatalf("ParseBytes(%s length) error = nil, want named-kind length rejection", keyword)
			}
		})
	}
}

func TestInteractionBatch4HSchemaRejectsOversizedVariableLength(t *testing.T) {
	t.Parallel()

	for _, keyword := range []string{"inputRandomText", "inputRandomNumber"} {
		keyword := keyword
		t.Run(keyword, func(t *testing.T) {
			t.Parallel()
			input := fmt.Sprintf("appId: com.example.batch4h\n---\n- %s: {length: 1025}\n", keyword)
			if _, err := ParseBytes("/workspace/batch4h-oversized-length.yaml", []byte(input)); err == nil {
				t.Fatalf("ParseBytes(%s length 1025) error = nil, want upper-bound rejection", keyword)
			}
		})
	}
}

func TestInteractionBatch4HSchemaAcceptsExactRandomInputGrammar(t *testing.T) {
	t.Parallel()

	for _, keyword := range []string{"inputRandomText", "inputRandomNumber"} {
		keyword := keyword
		for _, value := range []string{"", "{}", "{length: 0}", "{length: 1}", "{length: 8}", "{length: 1024}"} {
			value := value
			t.Run(keyword+"/"+value, func(t *testing.T) {
				t.Parallel()
				batch4HParseRandomCommand(t, keyword, value)
			})
		}
	}
	for _, keyword := range []string{
		"inputRandomEmail",
		"inputRandomPersonName",
		"inputRandomCityName",
		"inputRandomCountryName",
		"inputRandomColorName",
	} {
		keyword := keyword
		for _, value := range []string{"", "{}"} {
			value := value
			t.Run(keyword+"/"+value, func(t *testing.T) {
				t.Parallel()
				batch4HParseRandomCommand(t, keyword, value)
			})
		}
	}
}

func TestInteractionBatch4HSchemaRejectsClosedGrammarAndAliases(t *testing.T) {
	t.Parallel()

	variableInvalid := []string{
		"{length: -1}",
		"{length: 1025}",
		"{length: 9223372036854775808}",
		"{length: 1.5}",
		"{length: 1e3}",
		"{length: 'eight'}",
		"{length: '${LENGTH}'}",
		"{length: true}",
		"{length: null}",
		"{length: [8]}",
		"8",
		"'8'",
		"true",
		"[]",
		"null",
		"{mystery: true}",
		// {label: ...} and {optional: ...} are universal Command fields — link
		// These forms are valid and therefore excluded from the invalid set.
		"{text: target}",
		"{when: {platform: android}}",
		"{commands: []}",
		"{file: child.yaml}",
	}
	for _, keyword := range []string{"inputRandomText", "inputRandomNumber"} {
		keyword := keyword
		for _, value := range variableInvalid {
			value := value
			t.Run(keyword+"/"+value, func(t *testing.T) {
				t.Parallel()
				batch4HRejectRandomCommand(t, keyword, value)
			})
		}
	}

	for _, keyword := range []string{
		"inputRandomEmail",
		"inputRandomPersonName",
		"inputRandomCityName",
		"inputRandomCountryName",
		"inputRandomColorName",
	} {
		keyword := keyword
		for _, value := range []string{
			"{length: -1}", "{length: 0}", "{length: 8}", "{length: 1.5}", "{length: 1e3}",
			"{length: 'eight'}", "{length: '${LENGTH}'}", "{length: true}", "{length: null}", "{length: [8]}",
			"{mystery: true}", "8", "'8'", "true", "[]", "null",
			// label and optional are universal command fields.
			"{text: target}", "{when: {platform: android}}", "{commands: []}", "{file: child.yaml}",
		} {
			value := value
			t.Run(keyword+"/"+value, func(t *testing.T) {
				t.Parallel()
				batch4HRejectRandomCommand(t, keyword, value)
			})
		}
	}

	aliasInput := "appId: com.example.batch4h\n---\n- inputRandomText: {length: &randomLength 8}\n- inputRandomNumber: {length: *randomLength}\n"
	if _, err := ParseBytes("/workspace/batch4h-alias.yaml", []byte(aliasInput)); err == nil {
		t.Fatal("ParseBytes(random length alias) error = nil, want alias rejection")
	}
	mapAliasInput := "appId: com.example.batch4h\n---\n- inputRandomText: &randomArgs {length: 8}\n- inputRandomNumber: *randomArgs\n"
	if _, err := ParseBytes("/workspace/batch4h-map-alias.yaml", []byte(mapAliasInput)); err == nil {
		t.Fatal("ParseBytes(random map alias) error = nil, want alias rejection")
	}
}

func batch4HParseRandomCommand(t *testing.T, keyword, value string) {
	t.Helper()
	separator := ": "
	if value == "" {
		separator = ""
	}
	input := fmt.Sprintf("appId: com.example.batch4h\n---\n- %s%s%s\n", keyword, separator, value)
	if _, err := ParseBytes("/workspace/batch4h-valid.yaml", []byte(input)); err != nil {
		t.Fatalf("ParseBytes(%s %s) error = %v", keyword, value, err)
	}
}

func batch4HRejectRandomCommand(t *testing.T, keyword, value string) {
	t.Helper()
	input := fmt.Sprintf("appId: com.example.batch4h\n---\n- %s: %s\n", keyword, value)
	if _, err := ParseBytes("/workspace/batch4h-invalid.yaml", []byte(input)); err == nil {
		t.Fatalf("ParseBytes(%s %s) error = nil, want rejection", keyword, value)
	}
}
