package explore

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// truncateText cut at a byte offset, so any label the device shows in a
// non-Latin script could be sliced through a rune. %q then escapes the
// broken tail into \xNN, so the line stays valid UTF-8 and the corruption
// reaches the artifact, and the supervisor prompt, looking like data.
func TestTruncationCutsBetweenRunesNotThroughThem(t *testing.T) {
	for _, text := range []string{
		strings.Repeat("\u97f3", 60),
		strings.Repeat("a", 39) + "\u00e9" + strings.Repeat("b", 30),
		strings.Repeat("\U0001f600", 30),
	} {
		got := Truncate(text, 40)
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q...) returned broken UTF-8: %q", text[:8], got)
			continue
		}
		if !strings.HasPrefix(text, strings.TrimSuffix(got, "\u2026")) {
			t.Errorf("Truncate(%q...) = %q, which is not a prefix of the input", text[:8], got)
		}
	}
}

// The ellipsis marks a cut, so a value that fits must not carry one.
func TestTruncationLeavesAShortValueAlone(t *testing.T) {
	if got := Truncate("\u97f3\u697d", 40); got != "\u97f3\u697d" {
		t.Errorf("truncateText shortened a value that fits: %q", got)
	}
}

// A rejected model reply is quoted back to the model on the retry
// (ChatJSON), so a reply excerpt cut through a character feeds the model its
// own broken text as the example of what not to send. Model replies carry
// non-ASCII routinely: any non-English app content, smart quotes, an emoji.
func TestARejectedReplyIsQuotedBackWhole(t *testing.T) {
	// 240 is divisible by three, so a reply of nothing but three-byte
	// characters happens to cut cleanly; one leading ASCII byte moves every
	// boundary off the limit.
	reply := "a" + strings.Repeat("音", replyExcerptLimit)
	excerpt := replyExcerpt(reply)
	if !utf8.ValidString(excerpt) {
		t.Fatalf("the retry would quote broken UTF-8 back at the model: %q", excerpt)
	}
	if !strings.HasPrefix(reply, strings.TrimSuffix(excerpt, "…")) {
		t.Errorf("the excerpt is not the start of the reply: %q", excerpt)
	}
	// The quoting has to keep it readable rather than hide the break: %q
	// escapes an invalid byte into \xNN, which is valid UTF-8 and reads as
	// data, which is how this survived.
	if strings.Contains(fmt.Sprintf("%q", excerpt), `\x`) {
		t.Errorf("the excerpt carries escaped bytes: %q", excerpt)
	}
}
