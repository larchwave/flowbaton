package aiengine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// mergedTurns is the HTTP transport behind the anthropic client. It folds
// consecutive messages of one role into one message before the request
// leaves. chatMessages splits an assistant reply into one message per tool
// call (the adapter serializes only the first part of a message), and the
// adapter sends every tool result as a user message of its own. Anthropic's
// own API folds such runs itself; an endpoint speaking the same protocol
// may not, and MiniMax answers "invalid params (2013)" to a run of seven
// tool results. Folding here makes the wire shape the same everywhere.
type mergedTurns struct {
	next http.RoundTripper
}

func (m mergedTurns) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body == nil || request.Method != http.MethodPost {
		return m.next.RoundTrip(request)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, fmt.Errorf("aiengine: read request body: %w", err)
	}
	folded, err := foldTurns(body)
	if err != nil {
		return nil, fmt.Errorf("aiengine: fold turns: %w", err)
	}
	clone := request.Clone(request.Context())
	clone.Body = io.NopCloser(bytes.NewReader(folded))
	clone.ContentLength = int64(len(folded))
	clone.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(folded)), nil }
	return m.next.RoundTrip(clone)
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// foldTurns rewrites the "messages" array of one request body so that no
// two neighbours share a role. String content becomes one text block so
// blocks can be concatenated. A body without messages passes through.
func foldTurns(body []byte) ([]byte, error) {
	var request map[string]json.RawMessage
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	raw, ok := request["messages"]
	if !ok {
		return body, nil
	}
	var messages []wireMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		return nil, err
	}
	var folded []wireMessage
	var blocks [][]json.RawMessage
	for _, message := range messages {
		content, err := contentBlocks(message.Content)
		if err != nil {
			return nil, err
		}
		if n := len(folded); n > 0 && folded[n-1].Role == message.Role {
			blocks[n-1] = append(blocks[n-1], content...)
			continue
		}
		folded = append(folded, wireMessage{Role: message.Role})
		blocks = append(blocks, content)
	}
	if len(folded) == len(messages) {
		return body, nil
	}
	for i := range folded {
		encoded, err := json.Marshal(blocks[i])
		if err != nil {
			return nil, err
		}
		folded[i].Content = encoded
	}
	encoded, err := json.Marshal(folded)
	if err != nil {
		return nil, err
	}
	request["messages"] = encoded
	return json.Marshal(request)
}

func contentBlocks(content json.RawMessage) ([]json.RawMessage, error) {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		block, err := json.Marshal(map[string]string{"type": "text", "text": text})
		if err != nil {
			return nil, err
		}
		return []json.RawMessage{block}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(content, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}
