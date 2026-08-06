package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"golang.org/x/net/websocket"
)

// A minimal Chrome DevTools Protocol client.
//
// Spec 02-device-drivers.md §4 puts every web behavior behind an injected
// script plus pointer/key input, so the protocol surface this driver needs is
// six long-stable methods (Runtime.evaluate, Page.navigate, Page.enable,
// Page.captureScreenshot, Input.dispatchMouseEvent, Input.dispatchKeyEvent).
// That is small enough to speak directly over the websocket package the module
// already depends on, which keeps a whole generated-protocol dependency out of
// a driver — the dependency policy's stdlib-first rule with no exception to
// claim, since we are not tracking the protocol's moving parts, only its
// decade-stable core.

// evaluateReply is Runtime.evaluate's payload. The value stays raw because a
// caller knows what it asked for; exceptionDetails is what turns a page-side
// throw into a driver error instead of a silent empty result.
type evaluateReply struct {
	Result struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception *struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// pageTarget is one entry of the /json/list inventory.
type pageTarget struct {
	Type                 string `json:"type"`
	ID                   string `json:"id"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// discoverPageEndpoint finds the websocket for the browser's page target.
//
// The inventory also lists background pages and service workers; attaching to
// one of those would run every command against an extension rather than the
// page under test, so anything but a page target is skipped and an inventory
// without one is an error rather than a silent fallback.
func discoverPageEndpoint(ctx context.Context, baseURL string, client *http.Client) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/json/list", nil)
	if err != nil {
		return "", fmt.Errorf("web cdp: building the target request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("web cdp: listing targets: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("web cdp: listing targets returned %s", response.Status)
	}
	var targets []pageTarget
	if err := json.NewDecoder(response.Body).Decode(&targets); err != nil {
		return "", fmt.Errorf("web cdp: decoding targets: %w", err)
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return target.WebSocketDebuggerURL, nil
		}
	}
	return "", fmt.Errorf("web cdp: the browser has no open page target")
}

// pending is one in-flight call waiting for its reply.
type pending struct {
	result chan rpcResponse
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// connection is one attached page. It multiplexes calls over the single
// socket: DevTools interleaves replies with events, so a reader goroutine owns
// the socket and routes replies by id rather than each caller reading in turn.
type connection struct {
	socket *websocket.Conn

	mutex   sync.Mutex
	nextID  int
	waiting map[int]*pending
	closed  bool
	failure error

	done chan struct{}
}

func dialEndpoint(ctx context.Context, endpoint string) (*connection, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("web cdp: parsing %q: %w", endpoint, err)
	}
	// The Origin the library insists on is stripped back off on the wire; see
	// cdp_origin.go for the device that refuses every value of it.
	config, err := websocket.NewConfig(parsed.String(), "http://localhost")
	if err != nil {
		return nil, fmt.Errorf("web cdp: websocket config: %w", err)
	}
	if parsed.Scheme != "ws" {
		return nil, fmt.Errorf("web cdp: %q is not a ws:// endpoint", endpoint)
	}
	transport, err := new(net.Dialer).DialContext(ctx, "tcp", hostPort(parsed))
	if err != nil {
		return nil, fmt.Errorf("web cdp: dialing %q: %w", endpoint, err)
	}
	socket, err := websocket.NewClient(config, newOriginStrippingConn(transport))
	if err != nil {
		_ = transport.Close()
		return nil, fmt.Errorf("web cdp: dialing %q: %w", endpoint, err)
	}
	connection := &connection{
		socket:  socket,
		waiting: make(map[int]*pending),
		done:    make(chan struct{}),
	}
	go connection.read()
	return connection, nil
}

// read owns the socket and routes every reply to the caller waiting on its id.
func (c *connection) read() {
	defer close(c.done)
	for {
		var raw string
		if err := websocket.Message.Receive(c.socket, &raw); err != nil {
			c.failAll(fmt.Errorf("web cdp: reading: %w", err))
			return
		}
		var envelope struct {
			ID int `json:"id"`
			rpcResponse
		}
		if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
			continue
		}
		if envelope.ID == 0 {
			// An event, not a reply. This driver drives the page rather than
			// listening to it, so events are dropped instead of buffered.
			continue
		}
		c.mutex.Lock()
		waiter, exists := c.waiting[envelope.ID]
		delete(c.waiting, envelope.ID)
		c.mutex.Unlock()
		if exists {
			waiter.result <- envelope.rpcResponse
		}
	}
}

func (c *connection) failAll(err error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.failure == nil {
		c.failure = err
	}
	for id, waiter := range c.waiting {
		close(waiter.result)
		delete(c.waiting, id)
	}
}

// call sends one command and waits for its reply or the context, whichever
// comes first. A cancelled call abandons its slot rather than blocking the
// reader, so a browser that stops answering fails the flow instead of hanging
// the run.
func (c *connection) call(ctx context.Context, method string, params any, result any) error {
	if params == nil {
		params = map[string]any{}
	}
	c.mutex.Lock()
	if c.closed {
		c.mutex.Unlock()
		return fmt.Errorf("web cdp: the connection is closed")
	}
	if c.failure != nil {
		failure := c.failure
		c.mutex.Unlock()
		return failure
	}
	c.nextID++
	id := c.nextID
	waiter := &pending{result: make(chan rpcResponse, 1)}
	c.waiting[id] = waiter
	c.mutex.Unlock()

	payload, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		c.abandon(id)
		return fmt.Errorf("web cdp: encoding %s: %w", method, err)
	}
	if err := websocket.Message.Send(c.socket, string(payload)); err != nil {
		c.abandon(id)
		return fmt.Errorf("web cdp: sending %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		c.abandon(id)
		return fmt.Errorf("web cdp: %s: %w", method, ctx.Err())
	case reply, open := <-waiter.result:
		if !open {
			c.mutex.Lock()
			failure := c.failure
			c.mutex.Unlock()
			if failure == nil {
				failure = fmt.Errorf("web cdp: %s: the connection dropped", method)
			}
			return failure
		}
		if reply.Error != nil {
			return fmt.Errorf("web cdp: %s: %s", method, reply.Error.Message)
		}
		if result == nil || len(reply.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("web cdp: decoding %s: %w", method, err)
		}
		return nil
	}
}

func (c *connection) abandon(id int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	delete(c.waiting, id)
}

func (c *connection) close() error {
	c.mutex.Lock()
	if c.closed {
		c.mutex.Unlock()
		return nil
	}
	c.closed = true
	c.mutex.Unlock()
	return c.socket.Close()
}

// evaluate runs one expression in the page and decodes its value.
//
// A page-side throw is returned as an error: a flow that asked for the
// hierarchy and got an exception must fail, not proceed against an empty tree.
func (c *connection) evaluate(ctx context.Context, expression string, into any) error {
	var reply evaluateReply
	err := c.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	}, &reply)
	if err != nil {
		return err
	}
	if reply.ExceptionDetails != nil {
		message := reply.ExceptionDetails.Text
		if reply.ExceptionDetails.Exception != nil && reply.ExceptionDetails.Exception.Description != "" {
			message = reply.ExceptionDetails.Exception.Description
		}
		return fmt.Errorf("web cdp: the page threw: %s", message)
	}
	if into == nil || len(reply.Result.Value) == 0 {
		return nil
	}
	if err := json.Unmarshal(reply.Result.Value, into); err != nil {
		return fmt.Errorf("web cdp: decoding the page value: %w", err)
	}
	return nil
}
