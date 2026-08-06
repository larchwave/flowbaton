package engine

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/model"
)

// decodedObject is an owned snapshot of one normalized object-form command.
// It intentionally retains raw strings so interpolation remains an execution
// concern rather than a structural-decoding side effect.
type decodedObject struct {
	command model.CommandKeyword
	fields  map[string]any
}

// decodedStringOrObject retains which normalized form the caller supplied.
// Exactly one field is non-nil for a successfully decoded value.
type decodedStringOrObject struct {
	stringValue *string
	objectValue *decodedObject
}

func decodeNoArguments(command model.Command) error {
	if command.Form != model.CommandFormScalar {
		return commandDecodeError(command.Kind, "must use bare scalar form without arguments")
	}
	if command.Arguments != nil {
		return commandDecodeError(command.Kind, "bare scalar form must not carry arguments")
	}
	return nil
}

func decodeString(command model.Command) (string, error) {
	if command.Form != model.CommandFormObject {
		return "", commandDecodeError(command.Kind, "requires object form with a string argument")
	}
	value, ok := command.Arguments.(string)
	if !ok {
		return "", commandDecodeError(command.Kind, "argument must be a string")
	}
	return value, nil
}

func decodeOptionalString(command model.Command) (string, bool, error) {
	switch command.Form {
	case model.CommandFormScalar:
		if command.Arguments != nil {
			return "", false, commandDecodeError(command.Kind, "bare scalar form must not carry arguments")
		}
		return "", false, nil
	case model.CommandFormObject:
		value, ok := command.Arguments.(string)
		if !ok {
			return "", false, commandDecodeError(command.Kind, "argument must be a string when provided")
		}
		return value, true, nil
	default:
		return "", false, commandDecodeError(command.Kind, "has an invalid command form")
	}
}

func decodeObject(command model.Command) (decodedObject, error) {
	if command.Form != model.CommandFormObject {
		return decodedObject{}, commandDecodeError(command.Kind, "requires object form with an object argument")
	}
	fields, ok := command.Arguments.(map[string]any)
	if !ok {
		return decodedObject{}, commandDecodeError(command.Kind, "argument must be an object")
	}
	return newDecodedObject(command.Kind, fields), nil
}

func decodeOptionalObject(command model.Command) (decodedObject, bool, error) {
	switch command.Form {
	case model.CommandFormScalar:
		if command.Arguments != nil {
			return decodedObject{}, false, commandDecodeError(command.Kind, "bare scalar form must not carry arguments")
		}
		return newDecodedObject(command.Kind, nil), false, nil
	case model.CommandFormObject:
		fields, ok := command.Arguments.(map[string]any)
		if !ok {
			return decodedObject{}, false, commandDecodeError(command.Kind, "argument must be an object when provided")
		}
		return newDecodedObject(command.Kind, fields), true, nil
	default:
		return decodedObject{}, false, commandDecodeError(command.Kind, "has an invalid command form")
	}
}

func decodeStringOrObject(command model.Command) (decodedStringOrObject, error) {
	if command.Form != model.CommandFormObject {
		return decodedStringOrObject{}, commandDecodeError(command.Kind, "requires object form with a string or object argument")
	}
	switch value := command.Arguments.(type) {
	case string:
		cloned := value
		return decodedStringOrObject{stringValue: &cloned}, nil
	case map[string]any:
		object := newDecodedObject(command.Kind, value)
		return decodedStringOrObject{objectValue: &object}, nil
	default:
		return decodedStringOrObject{}, commandDecodeError(command.Kind, "argument must be a string or object")
	}
}

func newDecodedObject(command model.CommandKeyword, fields map[string]any) decodedObject {
	cloned := make(map[string]any, len(fields))
	for field, value := range fields {
		cloned[field] = cloneDecodedValue(value)
	}
	return decodedObject{command: command, fields: cloned}
}

func (object decodedObject) requireString(field string) (string, error) {
	value, exists := object.fields[field]
	if !exists {
		return "", object.fieldError(field, "is required")
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", object.fieldError(field, "must be a string")
	}
	return stringValue, nil
}

func (object decodedObject) optionalString(field string) (string, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return "", false, nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return "", false, object.fieldError(field, "must be a string")
	}
	return stringValue, true, nil
}

func (object decodedObject) optionalBool(field string) (bool, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return false, false, nil
	}
	switch typed := value.(type) {
	case bool:
		return typed, true, nil
	case string:
		// Quoted booleans are case-insensitive. The parser has already validated
		// the string, so this step normalizes the retained value.
		switch strings.ToLower(typed) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		}
	}
	return false, false, object.fieldError(field, "must be a boolean")
}

func (object decodedObject) optionalInt(field string) (int64, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return 0, false, nil
	}
	switch typed := value.(type) {
	case int64:
		return typed, true, nil
	case string:
		// Quoted integer strings are normalized after parser validation.
		if parsed, err := strconv.Atoi(typed); err == nil {
			return int64(parsed), true, nil
		}
	}
	return 0, false, object.fieldError(field, "must be an int64 integer")
}

func (object decodedObject) optionalNumber(field string) (float64, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return 0, false, nil
	}
	var number float64
	switch typed := value.(type) {
	case int64:
		number = float64(typed)
	case float64:
		number = typed
	case string:
		// Quoted numeric strings are normalized after parser validation.
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false, object.fieldError(field, "must be an int64 or float64 number")
		}
		number = parsed
	default:
		return 0, false, object.fieldError(field, "must be an int64 or float64 number")
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, false, object.fieldError(field, "must be a finite number")
	}
	return number, true, nil
}

func (object decodedObject) optionalStringMap(field string) (map[string]string, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return nil, false, nil
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, false, object.fieldError(field, "must be an object with string values")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]string, len(values))
	for _, key := range keys {
		stringValue, ok := values[key].(string)
		if !ok {
			return nil, false, object.fieldError(field, fmt.Sprintf("entry %s must be a string", key))
		}
		result[key] = stringValue
	}
	return result, true, nil
}

func (object decodedObject) optionalStrings(field string) ([]string, bool, error) {
	value, exists := object.fields[field]
	if !exists {
		return nil, false, nil
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false, object.fieldError(field, "must be an array of strings")
	}
	result := make([]string, len(values))
	for index, value := range values {
		stringValue, ok := value.(string)
		if !ok {
			return nil, false, object.fieldError(field, fmt.Sprintf("entry %d must be a string", index))
		}
		result[index] = stringValue
	}
	return result, true, nil
}

func (object decodedObject) raw(field string) (any, bool) {
	value, exists := object.fields[field]
	if !exists {
		return nil, false
	}
	return cloneDecodedValue(value), true
}

func (object decodedObject) rejectUnknown(allowed ...string) error {
	allowedFields := make(map[string]struct{}, len(allowed)+2)
	for _, field := range allowed {
		allowedFields[field] = struct{}{}
	}
	// `label` and `optional` are universal metadata. The parser hoists both onto
	// the typed command while retaining their raw argument entries, so every
	// object decoder admits them without per-command declarations.
	allowedFields["label"] = struct{}{}
	allowedFields["optional"] = struct{}{}
	unknown := make([]string, 0)
	for field := range object.fields {
		if _, exists := allowedFields[field]; !exists {
			unknown = append(unknown, field)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return object.fieldError(unknown[0], "is unknown")
}

func (object decodedObject) fieldError(field, message string) error {
	return NewConfigurationError(
		fmt.Sprintf("command %s field %s %s", object.command, field, message),
		nil,
	)
}

func commandDecodeError(command model.CommandKeyword, message string) error {
	return NewConfigurationError(fmt.Sprintf("command %s %s", command, message), nil)
}

func cloneDecodedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		cloned := make(map[string]any, len(typed))
		for key, nested := range typed {
			cloned[key] = cloneDecodedValue(nested)
		}
		return cloned
	case []any:
		cloned := make([]any, len(typed))
		for index, nested := range typed {
			cloned[index] = cloneDecodedValue(nested)
		}
		return cloned
	case map[string]string:
		cloned := make(map[string]string, len(typed))
		for key, nested := range typed {
			cloned[key] = nested
		}
		return cloned
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
