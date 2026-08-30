package explore

import (
	"context"
	"errors"
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

// Three of five live sessions on 2026-08-30 (mmx28, mmx29, mmx32) ended with
// the whole run thrown away because one model reply stopped mid-object:
// "decode section proposal: unexpected EOF", "decode scenarios: invalid
// character '}' after array element". The reply was cut at a different point
// each time on screens of much the same size, so it is not a fixed ceiling
// being hit -- asking again is worth one call against losing the session.
func TestChatJSONAsksAgainWhenTheReplyIsCutOff(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, Text: `{"scenarios":[{"name":"Add a contact"`},
		{Role: RoleAssistant, Text: `{"scenarios":[{"name":"Add a contact"}]}`},
	}}
	var reply struct {
		Scenarios []struct {
			Name string `json:"name"`
		} `json:"scenarios"`
	}
	if _, err := ChatJSON(context.Background(), llm, ChatRequest{}, &reply); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if llm.calls != 2 {
		t.Fatalf("calls = %d, want the truncated reply to be asked again once", llm.calls)
	}
	if len(reply.Scenarios) != 1 || reply.Scenarios[0].Name != "Add a contact" {
		t.Fatalf("reply = %+v, want the second answer decoded", reply)
	}
}

func TestChatJSONAsksOnlyOnceWhenTheReplyParses(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{{Role: RoleAssistant, Text: `{"ok":true}`}}}
	var reply struct {
		OK bool `json:"ok"`
	}
	if _, err := ChatJSON(context.Background(), llm, ChatRequest{}, &reply); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("calls = %d, want one", llm.calls)
	}
}

// A reply that is readable but wrong is the model's answer, not a cut cable:
// asking again would spend a call to get the same thing. Only a reply that
// does not parse is retried, and a second unreadable one keeps its own
// message so the operator sees what the model actually sent.
func TestChatJSONGivesUpAfterASecondUnreadableReply(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, Text: `{"scenarios":[`},
		{Role: RoleAssistant, Text: `still not json`},
	}}
	var reply struct{}
	_, err := ChatJSON(context.Background(), llm, ChatRequest{}, &reply)
	if err == nil {
		t.Fatal("ChatJSON accepted two unreadable replies")
	}
	if !strings.Contains(err.Error(), "still not json") {
		t.Fatalf("error does not quote the second reply: %v", err)
	}
	if llm.calls != 2 {
		t.Fatalf("calls = %d, want exactly two", llm.calls)
	}
}

// A transport failure is not a truncated reply; the caller decides what to do
// with a provider that is down, and asking again here would double the wait.
func TestChatJSONDoesNotRetryATransportFailure(t *testing.T) {
	llm := &scriptedLLM{}
	var reply struct{}
	if _, err := ChatJSON(context.Background(), llm, ChatRequest{}, &reply); err == nil {
		t.Fatal("ChatJSON hid a transport failure")
	}
	if llm.calls != 0 {
		t.Fatalf("calls = %d", llm.calls)
	}
}

// Retrying blind wastes the strongest hint there is: the model is the only
// one who can see why its own reply did not parse. The planner already fed
// the error back before this helper existed; keeping that is what makes
// routing the planner through here a simplification and not a downgrade.
func TestChatJSONTellsTheModelWhatWasWrongWithTheFirstReply(t *testing.T) {
	llm := &scriptedLLM{replies: []Message{
		{Role: RoleAssistant, Text: `{"ok":`},
		{Role: RoleAssistant, Text: `{"ok":true}`},
	}}
	var reply struct {
		OK bool `json:"ok"`
	}
	first := ChatRequest{Messages: []Message{{Role: RoleUser, Text: "plan the run"}}}
	if _, err := ChatJSON(context.Background(), llm, first, &reply); err != nil {
		t.Fatalf("ChatJSON: %v", err)
	}
	if len(llm.seen) != 2 {
		t.Fatalf("requests = %d, want two", len(llm.seen))
	}
	retry := llm.seen[1].Messages
	if len(retry) != 3 {
		t.Fatalf("retry carried %d messages, want the original plus the bad reply plus a correction", len(retry))
	}
	if retry[0].Text != "plan the run" {
		t.Fatalf("retry dropped the original request: %+v", retry[0])
	}
	if retry[1].Text != `{"ok":` || retry[1].Role != RoleAssistant {
		t.Fatalf("retry does not carry the reply being corrected: %+v", retry[1])
	}
	if retry[2].Role != RoleUser || !strings.Contains(retry[2].Text, `did not decode: EOF; reply begins "{\"ok\":"`) {
		t.Fatalf("correction does not name the decode failure: %+v", retry[2])
	}
	// The first request must not have grown the correction turns.
	if len(llm.seen[0].Messages) != 1 {
		t.Fatalf("ChatJSON mutated the caller's request: %+v", llm.seen[0].Messages)
	}
}

// A judge that cannot read one reply still writes the reason into the report
// (verdict.go), and "the model sent something unreadable" and "the provider
// is down" send an operator to different places. Both arrive through one
// return value, so the difference has to survive in the error itself.
func TestUnreadableRepliesAreDistinguishableFromATransportFailure(t *testing.T) {
	var target struct{ A int }
	if err := DecodeReply("not json", &target); !errors.Is(err, ErrUnreadableReply) {
		t.Fatalf("decode failure is not marked unreadable: %v", err)
	}
	llm := &scriptedLLM{}
	if _, err := ChatJSON(context.Background(), llm, ChatRequest{}, &target); errors.Is(err, ErrUnreadableReply) {
		t.Fatalf("transport failure marked as an unreadable reply: %v", err)
	}
	twice := &scriptedLLM{replies: []Message{{Text: "no"}, {Text: "still no"}}}
	if _, err := ChatJSON(context.Background(), twice, ChatRequest{}, &target); !errors.Is(err, ErrUnreadableReply) {
		t.Fatalf("second unreadable reply lost the mark: %v", err)
	}
}
