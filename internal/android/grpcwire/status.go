package grpcwire

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// The two grpc-status values specs/04-wire-protocols.md §1 defines as
// transport death rather than modelled failure.
const (
	statusDeadlineExceeded = 4
	statusUnavailable      = 14
)

// RPCError is a modelled failure: the server answered with a non-zero
// grpc-status other than the two transport-death ones. The three error-*
// fields are the agent's exception trailers from spec 04 §1.
type RPCError struct {
	Code         int
	Message      string
	ErrorType    string
	ErrorMessage string
	ErrorCause   string
}

func (err *RPCError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "grpcwire: rpc failed: grpc-status %d", err.Code)
	if err.Message != "" {
		b.WriteString(": " + err.Message)
	}
	if err.ErrorType != "" {
		b.WriteString(" (" + err.ErrorType + ")")
	}
	return b.String()
}

// TransportError is the connection dying rather than the server modelling a
// failure: dial and stream errors, a missed deadline, a malformed wire, a
// reply with no grpc-status at all, and the UNAVAILABLE / DEADLINE_EXCEEDED
// statuses. Retrying it means re-establishing the transport, not re-sending
// the same call on the same channel.
type TransportError struct {
	// Op is what was being attempted, normally the full method.
	Op string
	// Code is the grpc-status when death arrived as one (4 or 14); 0 otherwise.
	Code int
	// Message is the grpc-message or a wire-level diagnosis.
	Message string
	// Err is the underlying transport error, when one exists.
	Err error
}

func (err *TransportError) Error() string {
	var b strings.Builder
	b.WriteString("grpcwire: transport death: " + err.Op)
	if err.Code != 0 {
		fmt.Fprintf(&b, ": grpc-status %d", err.Code)
	}
	if err.Message != "" {
		b.WriteString(": " + err.Message)
	}
	if err.Err != nil {
		b.WriteString(": " + err.Err.Error())
	}
	return b.String()
}

func (err *TransportError) Unwrap() error { return err.Err }

// trailerValue finds a gRPC trailer. Real trailers live in response.Trailer;
// a trailers-only reply (a single HEADERS frame) delivers them as ordinary
// headers, so that is the fallback.
func trailerValue(response *http.Response, key string) (string, bool) {
	if values := response.Trailer.Values(key); len(values) > 0 {
		return values[0], true
	}
	if values := response.Header.Values(key); len(values) > 0 {
		return values[0], true
	}
	return "", false
}

// statusFailure turns the reply's grpc-status into the caller-facing error,
// or nil for an explicit success. A MISSING grpc-status is transport death,
// never success: something between here and the agent dropped the trailers.
func statusFailure(fullMethod string, response *http.Response) error {
	raw, found := trailerValue(response, "grpc-status")
	if !found {
		return &TransportError{Op: fullMethod, Message: "reply carried no grpc-status"}
	}
	code, err := strconv.Atoi(raw)
	if err != nil {
		return &TransportError{
			Op:      fullMethod,
			Message: fmt.Sprintf("unparseable grpc-status %q", raw),
		}
	}
	if code == 0 {
		return nil
	}
	rawMessage, _ := trailerValue(response, "grpc-message")
	message := decodeGrpcMessage(rawMessage)
	if code == statusUnavailable || code == statusDeadlineExceeded {
		return &TransportError{Op: fullMethod, Code: code, Message: message}
	}
	errorType, _ := trailerValue(response, "error-type")
	errorMessage, _ := trailerValue(response, "error-message")
	errorCause, _ := trailerValue(response, "error-cause")
	return &RPCError{
		Code:         code,
		Message:      message,
		ErrorType:    errorType,
		ErrorMessage: errorMessage,
		ErrorCause:   errorCause,
	}
}

// decodeGrpcMessage undoes the gRPC HTTP/2 spec's percent-encoding of
// grpc-message. A malformed sequence is carried through untouched — the spec
// directs implementations not to fail on one, and a mangled diagnostic beats
// a lost one.
func decodeGrpcMessage(encoded string) string {
	if !strings.Contains(encoded, "%") {
		return encoded
	}
	var b strings.Builder
	b.Grow(len(encoded))
	for i := 0; i < len(encoded); {
		if encoded[i] == '%' && i+2 < len(encoded) {
			high, okHigh := unhex(encoded[i+1])
			low, okLow := unhex(encoded[i+2])
			if okHigh && okLow {
				b.WriteByte(high<<4 | low)
				i += 3
				continue
			}
		}
		b.WriteByte(encoded[i])
		i++
	}
	return b.String()
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
