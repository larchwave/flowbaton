package explore

import (
	"strings"
	"testing"
)

func TestDecodeReplyQuotesTheRejectedReply(t *testing.T) {
	// A bare syntax error hides what the model sent; the excerpt is the only
	// evidence a failed session leaves behind.
	var target struct{ A int }
	err := DecodeReply("Sure! Here is the JSON:\n```json\n{\"A\": 1 \"B\": 2}\n```", &target)
	if err == nil || !strings.Contains(err.Error(), `reply begins "Sure! Here is the JSON:`) {
		t.Fatalf("error lacks the reply excerpt: %v", err)
	}
	if err := DecodeReply("```json\n{\"A\": 3}\n```", &target); err != nil || target.A != 3 {
		t.Fatalf("fenced reply not decoded: %v %+v", err, target)
	}
}
