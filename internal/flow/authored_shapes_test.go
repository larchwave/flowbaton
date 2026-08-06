package flow

import (
	"strings"
	"testing"
)

// These tests pin the openLink and travel shapes defined by
// specs/05-command-semantics-addendum.md §3.

func TestParserAcceptsDocumentedOpenLinkAndTravelShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command string
	}{
		{
			// spec 05 §1.2 / §3.1 — browser is an Android-only boolean.
			name:    "openLink with the browser flag",
			command: "- openLink: {link: 'https://example.invalid/path', browser: true}",
		},
		{
			name:    "openLink with browser false",
			command: "- openLink: {link: 'https://example.invalid/path', browser: false}",
		},
		{
			// spec 05 §2.1 / §3.2 — points are "lat, long" strings, speed is m/s.
			name:    "travel with string points and speed",
			command: "- travel: {points: ['48.8578065, 2.295188', '41.8902, 12.4922'], speed: 150000}",
		},
		{
			name:    "travel without speed",
			command: "- travel: {points: ['48.8578065, 2.295188']}",
		},
		{
			// An empty array is syntactically valid; point count is a runtime concern.
			name:    "travel with an empty point list",
			command: "- travel: {points: []}",
		},
		{
			// Point strings are parsed at runtime.
			name:    "travel with an unparsed point string",
			command: "- travel: {points: ['not-a-point']}",
		},
		{
			// Speed sign is validated at runtime.
			name:    "travel with a negative speed",
			command: "- travel: {points: ['1.0,2.0'], speed: -3}",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseBytes("/flow.yaml", authoredFlow(test.command)); err != nil {
				t.Fatalf("ParseBytes(%s) error = %v", test.command, err)
			}
		})
	}
}

func TestParserRejectsUnsupportedOpenLinkAndTravelShapes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		command string
		code    string
	}{
		{
			// speedMPS is not an authored field and must not survive as an alias.
			name:    "travel speedMPS is not a field",
			command: "- travel: {points: ['1.0,2.0'], speedMPS: 10}",
			code:    "unknown_command_field",
		},
		{
			// Travel points use the "latitude, longitude" string form.
			name:    "travel object points",
			command: "- travel: {points: [{latitude: 43.65, longitude: -79.38}]}",
			code:    "command_argument_type",
		},
		{
			// Travel requires points.
			name:    "travel without points",
			command: "- travel: {speed: 5}",
			code:    "missing_command_field",
		},
		{
			// Speed must be numeric.
			name:    "travel speed is not a number",
			command: "- travel: {points: ['1.0,2.0'], speed: 'fast'}",
			code:    "command_argument_type",
		},
		{
			// browser must be boolean.
			name:    "openLink browser is not a boolean",
			command: "- openLink: {link: 'https://example.invalid', browser: CHROME}",
			code:    "command_argument_type",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseBytes("/flow.yaml", authoredFlow(test.command))
			if err == nil {
				t.Fatalf("ParseBytes(%s) succeeded", test.command)
			}
			if !strings.Contains(err.Error(), test.code) {
				t.Fatalf("ParseBytes(%s) error = %v, want code %s", test.command, err, test.code)
			}
		})
	}
}

func authoredFlow(command string) []byte {
	return []byte("appId: com.example\n---\n" + command + "\n")
}
