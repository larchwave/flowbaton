package engine

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/model"
)

func TestDecodeNoArgumentsDistinguishesBareCommands(t *testing.T) {
	t.Parallel()

	if err := decodeNoArguments(model.Command{Kind: model.CommandBack, Form: model.CommandFormScalar}); err != nil {
		t.Fatalf("decodeNoArguments(bare) error: %v", err)
	}

	tests := []struct {
		name    string
		command model.Command
	}{
		{
			name:    "scalar form carries arguments",
			command: model.Command{Kind: model.CommandBack, Form: model.CommandFormScalar, Arguments: "unexpected"},
		},
		{
			name:    "object form with null",
			command: model.Command{Kind: model.CommandBack, Form: model.CommandFormObject},
		},
		{
			name:    "object form with empty object",
			command: model.Command{Kind: model.CommandBack, Form: model.CommandFormObject, Arguments: map[string]any{}},
		},
		{
			name:    "unknown form",
			command: model.Command{Kind: model.CommandBack, Form: model.CommandForm("future")},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertConfigurationError(t, decodeNoArguments(test.command), "back")
		})
	}
}

func TestDecodeStringAndOptionalStringPreserveRawValues(t *testing.T) {
	t.Parallel()

	raw := "${APP_ID}"
	got, err := decodeString(model.Command{
		Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: raw,
	})
	if err != nil || got != raw {
		t.Fatalf("decodeString() = %q, %v; want raw interpolation string", got, err)
	}

	bare, present, err := decodeOptionalString(model.Command{
		Kind: model.CommandStopApp, Form: model.CommandFormScalar,
	})
	if err != nil || present || bare != "" {
		t.Fatalf("decodeOptionalString(bare) = %q, %t, %v", bare, present, err)
	}
	explicit, present, err := decodeOptionalString(model.Command{
		Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: "",
	})
	if err != nil || !present || explicit != "" {
		t.Fatalf("decodeOptionalString(explicit empty) = %q, %t, %v", explicit, present, err)
	}

	for _, command := range []model.Command{
		{Kind: model.CommandStopApp, Form: model.CommandFormScalar},
		{Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: int64(1)},
		{Kind: model.CommandStopApp, Form: model.CommandFormScalar, Arguments: raw},
	} {
		_, decodeErr := decodeString(command)
		assertConfigurationError(t, decodeErr, "stopApp")
	}
	_, _, err = decodeOptionalString(model.Command{
		Kind: model.CommandStopApp, Form: model.CommandFormObject, Arguments: false,
	})
	assertConfigurationError(t, err, "stopApp")
}

func TestDecodeObjectAndOptionalObjectDefensivelyCopyNormalizedMaps(t *testing.T) {
	t.Parallel()

	arguments := map[string]any{
		"name": "${NAME}",
		"env":  map[string]any{"ROLE": "admin"},
		"tags": []any{"smoke", "ios"},
	}
	object, err := decodeObject(model.Command{
		Kind: model.CommandRunFlow, Form: model.CommandFormObject, Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("decodeObject() error: %v", err)
	}
	arguments["name"] = "mutated"
	arguments["env"].(map[string]any)["ROLE"] = "mutated"
	arguments["tags"].([]any)[0] = "mutated"
	if got, err := object.requireString("name"); err != nil || got != "${NAME}" {
		t.Fatalf("decoded name = %q, %v", got, err)
	}
	env, present, err := object.optionalStringMap("env")
	if err != nil || !present || env["ROLE"] != "admin" {
		t.Fatalf("decoded env = %#v, %t, %v", env, present, err)
	}
	tags, present, err := object.optionalStrings("tags")
	if err != nil || !present || len(tags) != 2 || tags[0] != "smoke" {
		t.Fatalf("decoded tags = %#v, %t, %v", tags, present, err)
	}

	bare, present, err := decodeOptionalObject(model.Command{
		Kind: model.CommandInputRandomText, Form: model.CommandFormScalar,
	})
	if err != nil || present {
		t.Fatalf("decodeOptionalObject(bare) = %#v, %t, %v", bare, present, err)
	}
	if err := bare.rejectUnknown(); err != nil {
		t.Fatalf("bare optional object is not usable: %v", err)
	}
	explicit, present, err := decodeOptionalObject(model.Command{
		Kind: model.CommandInputRandomText,
		Form: model.CommandFormObject,
		Arguments: map[string]any{
			"length": int64(12),
		},
	})
	if err != nil || !present {
		t.Fatalf("decodeOptionalObject(explicit) = %#v, %t, %v", explicit, present, err)
	}
	if length, ok, fieldErr := explicit.optionalInt("length"); fieldErr != nil || !ok || length != 12 {
		t.Fatalf("optional length = %d, %t, %v", length, ok, fieldErr)
	}

	for _, command := range []model.Command{
		{Kind: model.CommandRunFlow, Form: model.CommandFormScalar},
		{Kind: model.CommandRunFlow, Form: model.CommandFormObject},
		{Kind: model.CommandRunFlow, Form: model.CommandFormObject, Arguments: "child.yaml"},
	} {
		_, decodeErr := decodeObject(command)
		assertConfigurationError(t, decodeErr, "runFlow")
	}
	_, _, err = decodeOptionalObject(model.Command{
		Kind: model.CommandInputRandomText, Form: model.CommandFormObject, Arguments: []any{},
	})
	assertConfigurationError(t, err, "inputRandomText")
}

func TestDecodeStringOrObjectRetainsVariantAndCopiesObject(t *testing.T) {
	t.Parallel()

	stringVariant, err := decodeStringOrObject(model.Command{
		Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: "${URL}",
	})
	if err != nil || stringVariant.stringValue == nil || *stringVariant.stringValue != "${URL}" || stringVariant.objectValue != nil {
		t.Fatalf("string variant = %#v, %v", stringVariant, err)
	}

	arguments := map[string]any{"link": "https://example.invalid", "autoVerify": true}
	objectVariant, err := decodeStringOrObject(model.Command{
		Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: arguments,
	})
	if err != nil || objectVariant.stringValue != nil || objectVariant.objectValue == nil {
		t.Fatalf("object variant = %#v, %v", objectVariant, err)
	}
	arguments["link"] = "mutated"
	if link, fieldErr := objectVariant.objectValue.requireString("link"); fieldErr != nil || link != "https://example.invalid" {
		t.Fatalf("object variant link = %q, %v", link, fieldErr)
	}

	for _, command := range []model.Command{
		{Kind: model.CommandOpenLink, Form: model.CommandFormScalar},
		{Kind: model.CommandOpenLink, Form: model.CommandFormObject, Arguments: true},
	} {
		_, decodeErr := decodeStringOrObject(command)
		assertConfigurationError(t, decodeErr, "openLink")
	}
}

func TestDecodedObjectTypedFieldsAndUnknownRejection(t *testing.T) {
	t.Parallel()

	object, err := decodeObject(model.Command{
		Kind: model.CommandLaunchApp,
		Form: model.CommandFormObject,
		Arguments: map[string]any{
			"appId":           "${APP_ID}",
			"clearState":      true,
			"retries":         int64(3),
			"threshold":       0.75,
			"wholeNumber":     int64(4),
			"permissions":     map[string]any{"camera": "allow", "photos": "deny"},
			"launchArguments": []any{"--uitesting", "${TOKEN}"},
			"nested": map[string]any{
				"items": []any{"one", map[string]any{"key": "value"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("decodeObject() error: %v", err)
	}

	if got, fieldErr := object.requireString("appId"); fieldErr != nil || got != "${APP_ID}" {
		t.Fatalf("requireString(appId) = %q, %v", got, fieldErr)
	}
	if got, present, fieldErr := object.optionalString("missing"); fieldErr != nil || present || got != "" {
		t.Fatalf("optionalString(missing) = %q, %t, %v", got, present, fieldErr)
	}
	if got, present, fieldErr := object.optionalBool("clearState"); fieldErr != nil || !present || !got {
		t.Fatalf("optionalBool(clearState) = %t, %t, %v", got, present, fieldErr)
	}
	if got, present, fieldErr := object.optionalInt("retries"); fieldErr != nil || !present || got != 3 {
		t.Fatalf("optionalInt(retries) = %d, %t, %v", got, present, fieldErr)
	}
	if got, present, fieldErr := object.optionalNumber("threshold"); fieldErr != nil || !present || got != 0.75 {
		t.Fatalf("optionalNumber(threshold) = %v, %t, %v", got, present, fieldErr)
	}
	if got, present, fieldErr := object.optionalNumber("wholeNumber"); fieldErr != nil || !present || got != 4 {
		t.Fatalf("optionalNumber(wholeNumber) = %v, %t, %v", got, present, fieldErr)
	}
	permissions, present, fieldErr := object.optionalStringMap("permissions")
	if fieldErr != nil || !present || permissions["camera"] != "allow" || permissions["photos"] != "deny" {
		t.Fatalf("optionalStringMap(permissions) = %#v, %t, %v", permissions, present, fieldErr)
	}
	arguments, present, fieldErr := object.optionalStrings("launchArguments")
	if fieldErr != nil || !present || len(arguments) != 2 || arguments[1] != "${TOKEN}" {
		t.Fatalf("optionalStrings(launchArguments) = %#v, %t, %v", arguments, present, fieldErr)
	}

	raw, present := object.raw("nested")
	if !present {
		t.Fatal("raw(nested) is absent")
	}
	rawMap := raw.(map[string]any)
	rawMap["items"].([]any)[1].(map[string]any)["key"] = "mutated"
	again, _ := object.raw("nested")
	if got := again.(map[string]any)["items"].([]any)[1].(map[string]any)["key"]; got != "value" {
		t.Fatalf("raw() exposed object storage: got %#v", got)
	}
	permissions["camera"] = "mutated"
	againPermissions, _, _ := object.optionalStringMap("permissions")
	if againPermissions["camera"] != "allow" {
		t.Fatal("optionalStringMap() exposed object storage")
	}
	arguments[0] = "mutated"
	againArguments, _, _ := object.optionalStrings("launchArguments")
	if againArguments[0] != "--uitesting" {
		t.Fatal("optionalStrings() exposed object storage")
	}

	if err := object.rejectUnknown(
		"appId", "clearState", "retries", "threshold", "wholeNumber", "permissions", "launchArguments", "nested",
	); err != nil {
		t.Fatalf("rejectUnknown(complete allowlist) error: %v", err)
	}
	err = object.rejectUnknown("appId")
	assertConfigurationError(t, err, "launchApp", "clearState")
}

func TestDecodedObjectRejectsMissingAndWrongFieldTypesWithContext(t *testing.T) {
	t.Parallel()

	object, err := decodeObject(model.Command{
		Kind: model.CommandTravel,
		Form: model.CommandFormObject,
		Arguments: map[string]any{
			"string":      int64(1),
			"bool":        "notbool",
			"int":         int(2),
			"number":      "notnumber",
			"stringMap":   map[string]any{"bad": false},
			"strings":     []any{"good", int64(2)},
			"stringSlice": []string{"not", "normalized"},
		},
	})
	if err != nil {
		t.Fatalf("decodeObject() error: %v", err)
	}

	_, err = object.requireString("missing")
	assertConfigurationError(t, err, "travel", "missing")
	_, err = object.requireString("string")
	assertConfigurationError(t, err, "travel", "string")
	_, _, err = object.optionalString("string")
	assertConfigurationError(t, err, "travel", "string")
	_, _, err = object.optionalBool("bool")
	assertConfigurationError(t, err, "travel", "bool")
	_, _, err = object.optionalInt("int")
	assertConfigurationError(t, err, "travel", "int")
	_, _, err = object.optionalNumber("number")
	assertConfigurationError(t, err, "travel", "number")
	_, _, err = object.optionalStringMap("stringMap")
	assertConfigurationError(t, err, "travel", "stringMap")
	_, _, err = object.optionalStrings("strings")
	assertConfigurationError(t, err, "travel", "strings")
	_, _, err = object.optionalStrings("stringSlice")
	assertConfigurationError(t, err, "travel", "stringSlice")
}

func TestDecodedObjectNumberRejectsNonFiniteValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value float64
	}{
		{name: "NaN", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
		{name: "negative infinity", value: math.Inf(-1)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			object, err := decodeObject(model.Command{
				Kind:      model.CommandSetLocation,
				Form:      model.CommandFormObject,
				Arguments: map[string]any{"latitude": test.value},
			})
			if err != nil {
				t.Fatalf("decodeObject() error: %v", err)
			}
			_, _, err = object.optionalNumber("latitude")
			assertConfigurationError(t, err, "setLocation", "latitude")
		})
	}
}

func assertConfigurationError(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *ConfigurationError")
	}
	var configurationError *ConfigurationError
	if !errors.As(err, &configurationError) {
		t.Fatalf("error type = %T, want *ConfigurationError: %v", err, err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error %q does not contain context %q", err, fragment)
		}
	}
}
