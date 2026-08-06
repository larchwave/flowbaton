package grpcwire

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// The test server is x/net's h2c server speaking the gRPC framing by hand —
// deliberately NOT grpc-go, so the client is pinned against the wire shape
// rather than against another gRPC implementation's tolerance.

const testMethod = "/flowbaton_android.FlowBatonDriver/deviceInfo"
const streamMethod = "/flowbaton_android.FlowBatonDriver/addMedia"

func startTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: h2c.NewHandler(handler, &http2.Server{})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return listener.Addr().String()
}

func dialTest(t *testing.T, addr string, options ...Option) *Conn {
	t.Helper()
	conn, err := Dial(addr, options...)
	if err != nil {
		t.Fatalf("Dial(%q): %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// serverFrame builds a length-prefixed gRPC message ON THE SERVER SIDE.
// Kept separate from the production frame() so a framing bug cannot cancel
// itself out; the literal-bytes assertion in the round-trip test is the
// independent pin for the shape itself.
func serverFrame(message []byte) []byte {
	framed := make([]byte, 5+len(message))
	binary.BigEndian.PutUint32(framed[1:5], uint32(len(message)))
	copy(framed[5:], message)
	return framed
}

func replyMessage(w http.ResponseWriter, message []byte, grpcStatus int) {
	w.Header().Set("Content-Type", "application/grpc+proto")
	_, _ = w.Write(serverFrame(message))
	w.Header().Set(http.TrailerPrefix+"Grpc-Status", strconv.Itoa(grpcStatus))
}

func TestInvokeRoundTripsAUnaryMessage(t *testing.T) {
	var mu sync.Mutex
	var gotMethod, gotPath, gotContentType, gotTE string
	var gotBody []byte
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotTE = r.Header.Get("Te")
		gotBody = body
		mu.Unlock()
		replyMessage(w, []byte("reply-bytes"), 0)
	}))

	conn := dialTest(t, addr)
	reply, err := conn.Invoke(context.Background(), testMethod, []byte("hi"))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if string(reply) != "reply-bytes" {
		t.Fatalf("reply = %q, want %q", reply, "reply-bytes")
	}

	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != testMethod {
		t.Errorf("path = %q, want %q", gotPath, testMethod)
	}
	if gotContentType != "application/grpc+proto" {
		t.Errorf("content-type = %q, want application/grpc+proto", gotContentType)
	}
	if gotTE != "trailers" {
		t.Errorf("te = %q, want trailers", gotTE)
	}
	// The literal wire bytes: compressed flag 0, big-endian length 2, "hi".
	// This is the one assertion that pins the frame shape independently of
	// any helper shared with production code.
	want := []byte{0x00, 0x00, 0x00, 0x00, 0x02, 'h', 'i'}
	if !bytes.Equal(gotBody, want) {
		t.Errorf("request body = %v, want %v", gotBody, want)
	}
}

func TestATrailersOnlyResponseCarriesTheModelledError(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// grpc servers answer some failures with a single HEADERS frame:
		// the trailers arrive as ordinary response headers and there is no
		// body at all.
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Grpc-Status", "3")
		w.Header().Set("Grpc-Message", "bad argument")
		w.Header().Set("Error-Type", "java.lang.IllegalArgumentException")
		w.Header().Set("Error-Message", "x out of range")
		w.Header().Set("Error-Cause", "tap outside display")
		w.WriteHeader(http.StatusOK)
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	if rpcErr.Code != 3 {
		t.Errorf("Code = %d, want 3", rpcErr.Code)
	}
	if rpcErr.Message != "bad argument" {
		t.Errorf("Message = %q", rpcErr.Message)
	}
	if rpcErr.ErrorType != "java.lang.IllegalArgumentException" {
		t.Errorf("ErrorType = %q", rpcErr.ErrorType)
	}
	if rpcErr.ErrorMessage != "x out of range" {
		t.Errorf("ErrorMessage = %q", rpcErr.ErrorMessage)
	}
	if rpcErr.ErrorCause != "tap outside display" {
		t.Errorf("ErrorCause = %q", rpcErr.ErrorCause)
	}
}

func TestAMissingGrpcStatusIsATransportDeathNotSuccess(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		_, _ = w.Write(serverFrame([]byte("looks-fine")))
		// No trailers at all: a proxy ate them, or the server died after the
		// body. Success may only be claimed on an explicit grpc-status 0.
	}))

	conn := dialTest(t, addr)
	reply, err := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError; reply=%q", err, err, reply)
	}
	if !strings.Contains(transportErr.Message, "grpc-status") {
		t.Errorf("Message = %q, want it to name the missing grpc-status", transportErr.Message)
	}
}

func TestErrorTrailersAfterABodyPopulateTheRPCError(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		// A failed RPC can still have streamed nothing: an empty body with
		// REAL trailers (not trailers-only) is the ordinary Netty shape.
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "13")
		w.Header().Set(http.TrailerPrefix+"Grpc-Message", "boom")
		w.Header().Set(http.TrailerPrefix+"Error-Type", "java.lang.IllegalStateException")
		w.Header().Set(http.TrailerPrefix+"Error-Message", "driver not ready")
		w.Header().Set(http.TrailerPrefix+"Error-Cause", "instrumentation gone")
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	if rpcErr.Code != 13 {
		t.Errorf("Code = %d, want 13", rpcErr.Code)
	}
	if rpcErr.Message != "boom" {
		t.Errorf("Message = %q, want boom", rpcErr.Message)
	}
	if rpcErr.ErrorType != "java.lang.IllegalStateException" ||
		rpcErr.ErrorMessage != "driver not ready" ||
		rpcErr.ErrorCause != "instrumentation gone" {
		t.Errorf("error trailers = %q/%q/%q", rpcErr.ErrorType, rpcErr.ErrorMessage, rpcErr.ErrorCause)
	}
}

func TestUnavailableAndDeadlineExceededAreTransportDeath(t *testing.T) {
	for _, status := range []int{statusUnavailable, statusDeadlineExceeded} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/grpc+proto")
				w.Header().Set("Grpc-Status", strconv.Itoa(status))
				w.Header().Set("Grpc-Message", "server going down")
				w.WriteHeader(http.StatusOK)
			}))

			conn := dialTest(t, addr)
			_, err := conn.Invoke(context.Background(), testMethod, nil)
			var transportErr *TransportError
			if !errors.As(err, &transportErr) {
				t.Fatalf("err = %v (%T), want *TransportError", err, err)
			}
			if transportErr.Code != status {
				t.Errorf("Code = %d, want %d", transportErr.Code, status)
			}
			if transportErr.Message != "server going down" {
				t.Errorf("Message = %q", transportErr.Message)
			}
		})
	}
}

func TestTheGrpcMessageIsPercentDecoded(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Grpc-Status", "2")
		w.Header().Set("Grpc-Message", "boom%20from%0Aagent %zz")
		w.WriteHeader(http.StatusOK)
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	// Valid %XX sequences decode; a malformed one is carried as-is rather
	// than failing the whole message, per the gRPC HTTP/2 spec.
	if rpcErr.Message != "boom from\nagent %zz" {
		t.Errorf("Message = %q", rpcErr.Message)
	}
}

func TestAnOversizedFrameIsRefusedBeforeItIsRead(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		prefix := make([]byte, 5)
		binary.BigEndian.PutUint32(prefix[1:], 1<<31) // 2 GiB claimed
		_, _ = w.Write(prefix)
		_, _ = w.Write([]byte("only a little data"))
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
	// The refusal must come from the SIZE CHECK, not from failing to read
	// 2 GiB that never arrives — otherwise the guard could be deleted and
	// this test would stay green on the truncation error instead.
	if !strings.Contains(transportErr.Message, "exceeds") {
		t.Errorf("Message = %q, want the size-ceiling refusal", transportErr.Message)
	}
}

func TestACompressedMessageIsRefused(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		_, _ = w.Write([]byte{0x01, 0x00, 0x00, 0x00, 0x01, 'x'})
		w.Header().Set(http.TrailerPrefix+"Grpc-Status", "0")
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
	if !strings.Contains(transportErr.Message, "compressed") {
		t.Errorf("Message = %q, want the compression refusal", transportErr.Message)
	}
}

func TestAMidStreamConnectionDropIsATransportDeath(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc+proto")
		prefix := make([]byte, 5)
		binary.BigEndian.PutUint32(prefix[1:], 100)
		_, _ = w.Write(prefix)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // resets the stream mid-message
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
}

func TestANonGRPCHTTPStatusIsATransportDeath(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "who are you", http.StatusServiceUnavailable)
	}))

	conn := dialTest(t, addr)
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
	if !strings.Contains(transportErr.Message, "503") {
		t.Errorf("Message = %q, want it to carry HTTP 503", transportErr.Message)
	}
}

func TestASlowServerHitsTheCallDeadline(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // never answer; return when the client gives up
	}))

	conn := dialTest(t, addr, WithCallTimeout(200*time.Millisecond))
	started := time.Now()
	// No deadline on the caller's context: the connection's own default must
	// bound the call, which is what WithCallTimeout configures.
	_, err := conn.Invoke(context.Background(), testMethod, nil)
	elapsed := time.Since(started)
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("call took %v; the 200ms call timeout did not apply", elapsed)
	}
}

func TestACallerDeadlineOutranksTheDefault(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))

	conn := dialTest(t, addr) // default call timeout: 120s
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := conn.Invoke(ctx, testMethod, nil)
	if time.Since(started) > 5*time.Second {
		t.Fatalf("caller deadline was ignored")
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", err, err)
	}
}

func TestAConnectionRefusedIsATransportDeath(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close() // nothing is listening there any more

	conn := dialTest(t, addr)
	_, invokeErr := conn.Invoke(context.Background(), testMethod, nil)
	var transportErr *TransportError
	if !errors.As(invokeErr, &transportErr) {
		t.Fatalf("err = %v (%T), want *TransportError", invokeErr, invokeErr)
	}
}

func TestInvokeClientStreamSendsEveryChunkFramedAndInOrder(t *testing.T) {
	var mu sync.Mutex
	var chunks [][]byte
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var joined []byte
		for {
			var prefix [5]byte
			if _, err := io.ReadFull(r.Body, prefix[:]); err != nil {
				break // EOF: the client half-closed
			}
			if prefix[0] != 0 {
				t.Errorf("chunk arrived with compressed flag %d", prefix[0])
			}
			chunk := make([]byte, binary.BigEndian.Uint32(prefix[1:]))
			if _, err := io.ReadFull(r.Body, chunk); err != nil {
				t.Errorf("reading chunk: %v", err)
				break
			}
			mu.Lock()
			chunks = append(chunks, chunk)
			mu.Unlock()
			joined = append(joined, chunk...)
		}
		replyMessage(w, joined, 0)
	}))

	msgs := make(chan []byte, 3)
	msgs <- []byte("first,")
	msgs <- []byte("second,")
	msgs <- []byte("third")
	close(msgs)

	conn := dialTest(t, addr)
	reply, err := conn.InvokeClientStream(context.Background(), streamMethod, msgs)
	if err != nil {
		t.Fatalf("InvokeClientStream: %v", err)
	}
	if string(reply) != "first,second,third" {
		t.Fatalf("reply = %q, want the chunks joined in order", reply)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(chunks) != 3 {
		t.Fatalf("server saw %d chunks, want 3 separate frames", len(chunks))
	}
}

func TestAClientStreamStillSurfacesTheModelledError(t *testing.T) {
	addr := startTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Grpc-Status", "8")
		w.Header().Set("Grpc-Message", "media store full")
		w.WriteHeader(http.StatusOK)
	}))

	msgs := make(chan []byte, 1)
	msgs <- []byte("payload")
	close(msgs)

	conn := dialTest(t, addr)
	_, err := conn.InvokeClientStream(context.Background(), streamMethod, msgs)
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	if rpcErr.Code != 8 || rpcErr.Message != "media store full" {
		t.Errorf("got %d/%q, want 8/media store full", rpcErr.Code, rpcErr.Message)
	}
}

func TestDialRefusesAnUnparseableAddress(t *testing.T) {
	if _, err := Dial("no-port-here"); err == nil {
		t.Fatal("Dial accepted an address with no port")
	}
}

func TestTheTransportConfigurationMatchesTheSpec(t *testing.T) {
	conn := dialTest(t, "127.0.0.1:7001")
	// specs/04-wire-protocols.md §1: client keepalive 2min/20s, blocking
	// deadline 120s.
	if conn.transport.ReadIdleTimeout != 2*time.Minute {
		t.Errorf("ReadIdleTimeout = %v, want 2m", conn.transport.ReadIdleTimeout)
	}
	if conn.transport.PingTimeout != 20*time.Second {
		t.Errorf("PingTimeout = %v, want 20s", conn.transport.PingTimeout)
	}
	if conn.callTimeout != 120*time.Second {
		t.Errorf("default callTimeout = %v, want 120s", conn.callTimeout)
	}
	if !conn.transport.AllowHTTP {
		t.Error("AllowHTTP = false; h2c prior knowledge requires it")
	}
}
