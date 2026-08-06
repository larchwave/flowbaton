// Package grpcwire is a minimal gRPC-over-h2c client for the Android agent.
//
// The agent serves the fixed 12-rpc FlowBatonDriver surface
// (proto/flowbaton_android.proto) over plaintext HTTP/2 with prior knowledge.
// This package owns the gRPC frame and trailer layer over x/net/http2 h2c;
// x/net/http2 handles HPACK and HTTP/2 state. Messages cross this API as raw
// protobuf bytes, with encoding owned by the caller.
//
// Error split per specs/04-wire-protocols.md §1: UNAVAILABLE (14) and
// DEADLINE_EXCEEDED (4) are transport death, like a dial failure or a broken
// stream; every other non-zero grpc-status is a modelled failure carrying the
// agent's error-type / error-message / error-cause trailers.
package grpcwire

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// specs/04-wire-protocols.md §1: client keepalive 2min/20s, blocking
// deadline 120s.
const (
	keepaliveReadIdle    = 2 * time.Minute
	keepalivePingTimeout = 20 * time.Second
	defaultCallTimeout   = 120 * time.Second
)

// Dial prepares a connection to a plaintext HTTP/2 (h2c, prior knowledge)
// gRPC server at addr ("host:port"). The underlying transport connects
// lazily, on the first Invoke.
func Dial(addr string, options ...Option) (*Conn, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("grpcwire: dial %q: %w", addr, err)
	}
	conn := &Conn{
		addr: addr,
		transport: &http2.Transport{
			// AllowHTTP plus a TLS dialer that returns a plain TCP
			// connection IS x/net's h2c-with-prior-knowledge client mode.
			AllowHTTP: true,
			DialTLSContext: func(
				ctx context.Context,
				network, addr string,
				_ *tls.Config,
			) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, addr)
			},
			ReadIdleTimeout: keepaliveReadIdle,
			PingTimeout:     keepalivePingTimeout,
		},
		callTimeout: defaultCallTimeout,
	}
	for _, option := range options {
		option(conn)
	}
	return conn, nil
}

// Conn is a client connection to one agent.
type Conn struct {
	addr        string
	transport   *http2.Transport
	callTimeout time.Duration
}

// Option customizes a connection at dial time.
type Option func(*Conn)

// WithCallTimeout replaces the default per-call deadline applied when the
// caller's context has none.
func WithCallTimeout(timeout time.Duration) Option {
	return func(conn *Conn) { conn.callTimeout = timeout }
}

// Close releases the connection's idle transport state.
func (conn *Conn) Close() error {
	conn.transport.CloseIdleConnections()
	return nil
}

// Invoke performs one unary RPC. fullMethod is the gRPC path, like
// "/flowbaton_android.FlowBatonDriver/deviceInfo"; req and the reply are raw
// protobuf message bytes.
func (conn *Conn) Invoke(ctx context.Context, fullMethod string, req []byte) ([]byte, error) {
	ctx, cancel := conn.callContext(ctx)
	defer cancel()
	return conn.call(ctx, fullMethod, bytes.NewReader(frame(req)))
}

// InvokeClientStream performs one client-streaming RPC, sending every
// message received from msgs in order and half-closing when msgs closes.
// The single reply arrives after the stream ends.
func (conn *Conn) InvokeClientStream(
	ctx context.Context,
	fullMethod string,
	msgs <-chan []byte,
) ([]byte, error) {
	ctx, cancel := conn.callContext(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	go func() {
		for {
			select {
			case msg, ok := <-msgs:
				if !ok {
					_ = writer.Close() // half-close: END_STREAM on the request
					return
				}
				if _, err := writer.Write(frame(msg)); err != nil {
					return // the request side died; call reports the error
				}
			case <-ctx.Done():
				// The deferred cancel fires here too, so this goroutine can
				// never outlive the call, even on a channel nobody closes.
				_ = writer.CloseWithError(ctx.Err())
				return
			}
		}
	}()
	return conn.call(ctx, fullMethod, reader)
}

// callContext applies the connection's default deadline only when the caller
// brought none: an explicit caller deadline always outranks the default.
func (conn *Conn) callContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, conn.callTimeout)
}

// call sends one framed request body and decodes the single-message reply
// plus its trailer-carried status.
func (conn *Conn) call(ctx context.Context, fullMethod string, body io.Reader) ([]byte, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, "http://"+conn.addr+fullMethod, body,
	)
	if err != nil {
		return nil, &TransportError{Op: fullMethod, Message: "building request", Err: err}
	}
	request.Header.Set("Content-Type", "application/grpc+proto")
	request.Header.Set("TE", "trailers")

	response, err := conn.transport.RoundTrip(request)
	if err != nil {
		return nil, &TransportError{Op: fullMethod, Err: err}
	}
	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil, &TransportError{
			Op:      fullMethod,
			Message: fmt.Sprintf("HTTP %d instead of a gRPC reply", response.StatusCode),
		}
	}

	// The body must be read to EOF before response.Trailer is populated.
	message, err := readUnaryBody(response.Body)
	if err != nil {
		return nil, &TransportError{Op: fullMethod, Message: err.Error()}
	}

	if failure := statusFailure(fullMethod, response); failure != nil {
		return nil, failure
	}
	if message == nil {
		return nil, &TransportError{
			Op:      fullMethod,
			Message: "grpc-status 0 with no reply message",
		}
	}
	return message, nil
}
