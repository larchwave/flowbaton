// Package strictjson decodes untrusted JSON without accepting ambiguous
// duplicate object keys, unknown fields, or trailing values.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode"
	"unicode/utf8"
)

// Decode validates one JSON value and decodes it into target.
func Decode(data []byte, target any) error {
	if err := validateUniqueKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}

func validateUniqueKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateValue(decoder); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("trailing JSON: %w", err)
		}
		return fmt.Errorf("trailing JSON token %v", token)
	}
	return nil
}

func validateValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key has type %T", keyToken)
			}
			foldedKey := foldKey(key)
			if _, duplicate := keys[foldedKey]; duplicate {
				return fmt.Errorf("duplicate object key %q", key)
			}
			keys[foldedKey] = struct{}{}
			if err := validateValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("object closed with %v", closing)
		}
	case '[':
		for decoder.More() {
			if err := validateValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("array closed with %v", closing)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

// foldKey uses the same Unicode simple-fold equivalence as encoding/json's
// case-insensitive struct-field lookup.
func foldKey(key string) string {
	folded := make([]byte, 0, len(key))
	for len(key) > 0 {
		r, size := utf8.DecodeRuneInString(key)
		folded = utf8.AppendRune(folded, foldRune(r))
		key = key[size:]
	}
	return string(folded)
}

func foldRune(r rune) rune {
	for {
		next := unicode.SimpleFold(r)
		if next <= r {
			return next
		}
		r = next
	}
}
