package aiengine

import "testing"

// langchaingo's clients take a base URL that ALREADY carries the version
// segment (their defaults are https://api.openai.com/v1 and
// https://api.anthropic.com/v1) and append "/messages" or "/chat/completions"
// to it. Vendors document the other half: MiniMax publishes
// https://api.minimax.io/anthropic, and Anthropic's own ANTHROPIC_BASE_URL is
// the host root, because their SDKs add /v1 themselves.
//
// Normalizing the version segment prevents a documented host root from reaching
// an unversioned messages path.
func TestBaseURLGetsTheVersionSegmentTheClientsExpect(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		authored string
		want     string
	}{
		{"vendor documented root", "https://api.minimax.io/anthropic", "https://api.minimax.io/anthropic/v1"},
		{"trailing slash", "https://api.minimax.io/anthropic/", "https://api.minimax.io/anthropic/v1"},
		{"already versioned", "https://api.minimax.io/anthropic/v1", "https://api.minimax.io/anthropic/v1"},
		{"another version", "https://gateway.internal/openai/v2", "https://gateway.internal/openai/v2"},
		{"bare host", "https://api.anthropic.com", "https://api.anthropic.com/v1"},
		{"blank stays blank", "", ""},
	} {
		if got := normalizeBaseURL(test.authored); got != test.want {
			t.Errorf("%s: normalizeBaseURL(%q) = %q, want %q", test.name, test.authored, got, test.want)
		}
	}
}
