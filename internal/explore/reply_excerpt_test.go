package explore

import (
	"fmt"
	"strings"
	"testing"
)

// A reply that breaks in its hundredth line is not explained by its first
// 240 characters. Two live sessions died on
// "invalid character '}' after array element" with an excerpt that stopped
// inside the first scenario, which says nothing about where the fault is.
// The excerpt is also what the retry shows the model, and the model has the
// same problem: it needs the place it went wrong, not its own opening.
func TestDecodeReplyQuotesWhereALongReplyBroke(t *testing.T) {
	t.Parallel()

	var target struct {
		Scenarios []struct {
			Name string `json:"name"`
		} `json:"scenarios"`
	}
	filler := strings.Repeat(`{"name":"scroll the list and read every row"},`, 24)
	reply := fmt.Sprintf(`{"scenarios":[%s{"name":"close the search field"}}`, filler)
	if len(reply) < 3*replyExcerptLimit {
		t.Fatalf("reply is %d characters, too short to test the window", len(reply))
	}
	err := DecodeReply(reply, &target)
	if err == nil {
		t.Fatal("decode accepted a reply that closes an array with a brace")
	}
	if !strings.Contains(err.Error(), "close the search field") {
		t.Fatalf("error does not quote the fault site: %v", err)
	}
}

// A short reply keeps quoting its opening: that is where the fault is, and
// it is the whole reply anyway.
func TestDecodeReplyQuotesTheOpeningOfAShortReply(t *testing.T) {
	t.Parallel()

	var target struct {
		A int `json:"A"`
	}
	err := DecodeReply(`{"A": 1 "B": 2}`, &target)
	if err == nil || !strings.Contains(err.Error(), `reply begins "{\"A\": 1 \"B\": 2}"`) {
		t.Fatalf("unexpected error %v", err)
	}
}
