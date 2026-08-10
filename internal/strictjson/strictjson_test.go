package strictjson

import (
	"strings"
	"testing"
)

func TestDecodeRejectsAmbiguousOrOpenJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "duplicate", data: `{"value":1,"value":2}`, want: "duplicate object key"},
		{name: "escaped duplicate", data: `{"value":1,"val\u0075e":2}`, want: "duplicate object key"},
		{name: "case-fold duplicate", data: `{"value":1,"Value":2}`, want: "duplicate object key"},
		{name: "escaped case-fold duplicate", data: `{"value":1,"\u0056alue":2}`, want: "duplicate object key"},
		{name: "unicode-fold duplicate", data: `{"kelvin":1,"\u212AELVIN":2}`, want: "duplicate object key"},
		{name: "nested duplicate", data: `{"nested":{"value":1,"value":2}}`, want: "duplicate object key"},
		{name: "nested case-fold duplicate", data: `{"nested":[{"value":1,"VALUE":2}]}`, want: "duplicate object key"},
		{name: "nested escaped case-fold duplicate", data: `{"nested":{"value":1,"\u0056ALUE":2}}`, want: "duplicate object key"},
		{name: "unknown", data: `{"value":1,"extra":2}`, want: "unknown field"},
		{name: "trailing", data: `{"value":1} {"value":2}`, want: "trailing JSON"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var target struct {
				Value int `json:"value"`
			}
			err := Decode([]byte(test.data), &target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode(%s) error = %v, want %q", test.data, err, test.want)
			}
		})
	}
}

func TestDecodeAcceptsOneClosedValue(t *testing.T) {
	t.Parallel()
	var target struct {
		Value int `json:"value"`
	}
	if err := Decode([]byte("{\n  \"value\": 7\n}\n"), &target); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if target.Value != 7 {
		t.Fatalf("value = %d, want 7", target.Value)
	}
}

func TestDecodeAcceptsDistinctFoldedKeys(t *testing.T) {
	t.Parallel()
	var target struct {
		Value int `json:"value"`
		Other int `json:"other"`
	}
	if err := Decode([]byte(`{"value":7,"other":9}`), &target); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if target.Value != 7 || target.Other != 9 {
		t.Fatalf("target = %#v", target)
	}
}
