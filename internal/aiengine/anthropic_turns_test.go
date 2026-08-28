package aiengine

import (
	"encoding/json"
	"testing"
)

func TestFoldTurnsJoinsNeighboursOfOneRole(t *testing.T) {
	t.Parallel()
	body := []byte(`{"model":"m","messages":[` +
		`{"role":"user","content":"go"},` +
		`{"role":"assistant","content":[{"type":"text","text":"two taps"}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"tap","input":{}}]},` +
		`{"role":"assistant","content":[{"type":"tool_use","id":"b","name":"tap","input":{}}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]},` +
		`{"role":"user","content":[{"type":"tool_result","tool_use_id":"b","content":"ok"}]}]}`)
	folded, err := foldTurns(body)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(folded, &request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "m" || len(request.Messages) != 3 {
		t.Fatalf("folded = %s", folded)
	}
	if got := request.Messages[1]; got.Role != "assistant" || len(got.Content) != 3 || got.Content[2]["id"] != "b" {
		t.Fatalf("assistant turn = %+v", got)
	}
	if got := request.Messages[2]; got.Role != "user" || len(got.Content) != 2 || got.Content[1]["tool_use_id"] != "b" {
		t.Fatalf("user turn = %+v", got)
	}
	if got := request.Messages[0].Content[0]; got["type"] != "text" || got["text"] != "go" {
		t.Fatalf("string content not kept as a text block: %+v", got)
	}
}

func TestFoldTurnsLeavesAnAlternatingBodyUntouched(t *testing.T) {
	t.Parallel()
	body := []byte(`{"messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"}],"system":"s"}`)
	folded, err := foldTurns(body)
	if err != nil || string(folded) != string(body) {
		t.Fatalf("folded = %s, err %v", folded, err)
	}
	if folded, err := foldTurns([]byte(`{"x":1}`)); err != nil || string(folded) != `{"x":1}` {
		t.Fatalf("no messages: %s %v", folded, err)
	}
}
