package model

import "testing"

// specs/01-core-engine.md:17 — "`url` (web flows; if present becomes effective
// appId)". Every consumer of the active app has to agree on that one rule, or a
// web flow reaches the driver with no target at all: the parser accepts a flow
// carrying only `url`, and reading AppID directly would then hand the driver an
// empty string and navigate nowhere.
func TestEffectiveAppIDPrefersTheURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{name: "app only", config: Config{AppID: "com.example.app"}, want: "com.example.app"},
		{name: "url only", config: Config{URL: "https://example.com"}, want: "https://example.com"},
		{
			// The spec says url *becomes* the effective appId, so it wins.
			name:   "both",
			config: Config{AppID: "com.example.app", URL: "https://example.com"},
			want:   "https://example.com",
		},
		{name: "neither", config: Config{}, want: ""},
	}
	for _, test := range tests {
		if got := test.config.EffectiveAppID(); got != test.want {
			t.Errorf("%s: EffectiveAppID() = %q, want %q", test.name, got, test.want)
		}
	}
}
