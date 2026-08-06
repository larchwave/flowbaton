// Package ios is the host-side client for the iOS XCTest runner's HTTP API.
//
// contracts/v0/ios-http.json is the contract: eighteen routes on a loopback
// server, their exact JSON shapes, and the mapping from HTTP status to error
// code. Nothing here talks to a simulator; the client is pure transport and is
// testable with an HTTP server without a simulator.
package ios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DefaultPort is the runner's default loopback port, frozen by the contract's
// transport block.
const DefaultPort = 22087

// defaultTimeout bounds a single request. The runner answers a stalled
// XCUITest call with 408 itself; this is the backstop for a runner that has
// stopped answering at all.
const defaultTimeout = 30 * time.Second

// DefaultBaseURL renders the contract's loopback address for a port.
func DefaultBaseURL(port int) string {
	return "http://127.0.0.1:" + strconv.Itoa(port)
}

// Client speaks the frozen runner API.
type Client struct {
	baseURL string
	http    *http.Client
}

// Option customizes a client at construction.
type Option func(*Client)

// WithHTTPClient replaces the underlying HTTP client, for callers that need
// their own transport or timeout.
func WithHTTPClient(client *http.Client) Option {
	return func(target *Client) {
		if client != nil {
			target.http = client
		}
	}
}

// NewClient builds a client for a runner reachable at baseURL.
func NewClient(baseURL string, options ...Option) *Client {
	client := &Client{baseURL: baseURL, http: &http.Client{Timeout: defaultTimeout}}
	for _, option := range options {
		option(client)
	}
	return client
}

// Code is the runner's error vocabulary, frozen by the contract.
type Code string

const (
	CodeInternal     Code = "internal"
	CodePrecondition Code = "precondition"
	CodeTimeout      Code = "timeout"
)

// Error is a runner-reported failure. The body's code is authoritative; the
// status mapping is the fallback for a runner that answers without one.
type Error struct {
	Code    Code
	Message string
	Status  int
}

func (err *Error) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("ios runner: %s (HTTP %d)", err.Code, err.Status)
	}
	return fmt.Sprintf("ios runner: %s: %s (HTTP %d)", err.Code, err.Message, err.Status)
}

// Retryable reports whether resending the request could plausibly succeed.
// Both XCUITest timeout signatures the contract pins are non-retryable, and a
// precondition failure will fail again the same way; only an internal error is
// worth another attempt.
func (err *Error) Retryable() bool {
	return err.Code == CodeInternal
}

// codeForStatus is the contract's status-to-code mapping. An unmapped status
// is treated as internal: something went wrong and the runner did not say
// which of the two non-retryable kinds it was, so the retryable reading is the
// safe one for the caller to see.
func codeForStatus(status int) Code {
	switch status {
	case http.StatusBadRequest:
		return CodePrecondition
	case http.StatusRequestTimeout:
		return CodeTimeout
	default:
		return CodeInternal
	}
}

// do performs one request. A nil request body sends no body at all, which is
// what the contract's NoBody routes require.
func (client *Client) do(
	ctx context.Context,
	method, path, query string,
	requestBody any,
	responseBody any,
) ([]byte, error) {
	var reader io.Reader
	if requestBody != nil {
		encoded, err := json.Marshal(requestBody)
		if err != nil {
			return nil, fmt.Errorf("ios runner: encoding %s request: %w", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	url := client.baseURL + path
	if query != "" {
		url += "?" + query
	}
	request, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("ios runner: building %s request: %w", path, err)
	}
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("ios runner: %s: %w", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("ios runner: reading %s response: %w", path, err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, decodeError(response.StatusCode, payload)
	}
	if responseBody != nil {
		if err := json.Unmarshal(payload, responseBody); err != nil {
			return nil, fmt.Errorf("ios runner: decoding %s response: %w", path, err)
		}
	}
	return payload, nil
}

func decodeError(status int, payload []byte) *Error {
	failure := &Error{Code: codeForStatus(status), Status: status}
	var body struct {
		Code    Code   `json:"code"`
		Message string `json:"errorMessage"`
	}
	if err := json.Unmarshal(payload, &body); err == nil {
		// The code and the message are taken independently. The contract
		// requires both, but a runner that sends only a message is exactly the
		// case where that message is worth keeping: discarding it leaves an
		// operator with a bare status and nothing to act on. Status remains the
		// fallback for a missing code.
		if body.Code != "" {
			failure.Code = body.Code
		}
		failure.Message = body.Message
	}
	return failure
}
