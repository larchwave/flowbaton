package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

// cdpServer is a fake DevTools endpoint: it answers /json/list with a target
// list and speaks the JSON-RPC framing on the socket, so the transport is
// tested without a browser.
type cdpServer struct {
	*httptest.Server
	// handle returns the result payload for one call, or an error message.
	handle func(method string, params json.RawMessage) (any, string)
	// events are pushed to the client before the first reply.
	calls []string
}

func newCDPServer(t *testing.T, handle func(string, json.RawMessage) (any, string)) *cdpServer {
	t.Helper()
	server := &cdpServer{handle: handle}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(writer http.ResponseWriter, request *http.Request) {
		socket := "ws" + strings.TrimPrefix(server.Server.URL, "http") + "/devtools/page/ABC"
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
		  {"type":"background_page","id":"BG","url":"chrome-extension://x","webSocketDebuggerUrl":"ws://ignored"},
		  {"type":"page","id":"ABC","url":"about:blank","webSocketDebuggerUrl":"` + socket + `"}
		]`))
	})
	mux.Handle("/devtools/page/ABC", acceptWithoutOrigin(websocket.Handler(func(connection *websocket.Conn) {
		for {
			var raw string
			if err := websocket.Message.Receive(connection, &raw); err != nil {
				return
			}
			var request struct {
				ID     int             `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal([]byte(raw), &request); err != nil {
				return
			}
			server.calls = append(server.calls, request.Method)
			result, failure := server.handle(request.Method, request.Params)
			var reply []byte
			if failure != "" {
				reply, _ = json.Marshal(map[string]any{
					"id": request.ID, "error": map[string]any{"code": -1, "message": failure},
				})
			} else {
				encoded, _ := json.Marshal(result)
				reply, _ = json.Marshal(map[string]any{"id": request.ID, "result": json.RawMessage(encoded)})
			}
			if err := websocket.Message.Send(connection, string(reply)); err != nil {
				return
			}
		}
	})))
	server.Server = httptest.NewServer(mux)
	t.Cleanup(server.Server.Close)
	return server
}

func TestDiscoverPageEndpointPicksThePageTarget(t *testing.T) {
	t.Parallel()

	server := newCDPServer(t, func(string, json.RawMessage) (any, string) { return nil, "" })
	endpoint, err := discoverPageEndpoint(context.Background(), server.Server.URL, nil)
	if err != nil {
		t.Fatalf("discoverPageEndpoint() error = %v", err)
	}
	// The background page must be skipped: attaching to it would run every
	// command against an extension instead of the page under test.
	if !strings.HasSuffix(endpoint, "/devtools/page/ABC") {
		t.Fatalf("endpoint = %q, want the page target", endpoint)
	}
}

func TestDiscoverPageEndpointFailsWhenNoPageIsOpen(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`[{"type":"background_page","webSocketDebuggerUrl":"ws://x"}]`))
	}))
	defer server.Close()

	if _, err := discoverPageEndpoint(context.Background(), server.URL, nil); err == nil {
		t.Fatal("discoverPageEndpoint() error = nil, want a failure when no page target exists")
	}
}

func TestConnectionCallReturnsTheResult(t *testing.T) {
	t.Parallel()

	server := newCDPServer(t, func(method string, _ json.RawMessage) (any, string) {
		if method == "Runtime.evaluate" {
			return map[string]any{"result": map[string]any{"type": "string", "value": "hello"}}, ""
		}
		return map[string]any{}, ""
	})
	connection, err := dialEndpoint(context.Background(), mustEndpoint(t, server))
	if err != nil {
		t.Fatalf("dialEndpoint() error = %v", err)
	}
	defer connection.close()

	var reply evaluateReply
	if err := connection.call(context.Background(), "Runtime.evaluate", map[string]any{"expression": "1"}, &reply); err != nil {
		t.Fatalf("call() error = %v", err)
	}
	if reply.Result.Value == nil || string(reply.Result.Value) != `"hello"` {
		t.Fatalf("reply = %#v", reply.Result)
	}
}

// A protocol-level error must surface as an error, not as a zero result: a
// silently-empty hierarchy would read as "the page is blank" to every selector.
func TestConnectionCallSurfacesProtocolErrors(t *testing.T) {
	t.Parallel()

	server := newCDPServer(t, func(string, json.RawMessage) (any, string) {
		return nil, "Cannot find context with specified id"
	})
	connection, err := dialEndpoint(context.Background(), mustEndpoint(t, server))
	if err != nil {
		t.Fatalf("dialEndpoint() error = %v", err)
	}
	defer connection.close()

	err = connection.call(context.Background(), "Runtime.evaluate", map[string]any{}, &evaluateReply{})
	if err == nil || !strings.Contains(err.Error(), "Cannot find context") {
		t.Fatalf("call() error = %v, want the protocol message", err)
	}
}

// Every call must respect its context: a browser that stops answering has to
// fail the flow rather than hang the run forever.
func TestConnectionCallHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	server := newCDPServer(t, func(string, json.RawMessage) (any, string) {
		<-blocked
		return map[string]any{}, ""
	})
	connection, err := dialEndpoint(context.Background(), mustEndpoint(t, server))
	if err != nil {
		t.Fatalf("dialEndpoint() error = %v", err)
	}
	defer connection.close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := connection.call(ctx, "Runtime.evaluate", map[string]any{}, &evaluateReply{}); err == nil {
		t.Fatal("call() error = nil, want the context deadline")
	}
}

func mustEndpoint(t *testing.T, server *cdpServer) string {
	t.Helper()
	endpoint, err := discoverPageEndpoint(context.Background(), server.Server.URL, nil)
	if err != nil {
		t.Fatalf("discoverPageEndpoint() error = %v", err)
	}
	return endpoint
}
