package grpcwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// maxMessageBytes bounds a single reply message. The prefix is untrusted
// input: a corrupt length must be refused before anything allocates it. The
// largest legitimate reply is a screenshot PNG, comfortably inside this.
// ponytail: fixed 64 MiB ceiling; make it an Option if a real reply outgrows it.
const maxMessageBytes = 64 << 20

// frame wraps one message in the gRPC length prefix: compressed flag 0 plus
// a 4-byte big-endian length.
func frame(message []byte) []byte {
	framed := make([]byte, 5+len(message))
	binary.BigEndian.PutUint32(framed[1:5], uint32(len(message)))
	copy(framed[5:], message)
	return framed
}

// readMessage reads one length-prefixed message. io.EOF means a clean end of
// the stream BEFORE a prefix — no message; every other failure is a wire
// defect.
func readMessage(r io.Reader) ([]byte, error) {
	var prefix [5]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("reading message prefix: %w", err)
	}
	if prefix[0] != 0 {
		return nil, fmt.Errorf("compressed message (flag %d); compression is not negotiated", prefix[0])
	}
	length := binary.BigEndian.Uint32(prefix[1:])
	if length > maxMessageBytes {
		return nil, fmt.Errorf("message of %d bytes exceeds the %d-byte ceiling", length, maxMessageBytes)
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(r, message); err != nil {
		return nil, fmt.Errorf("message truncated: %w", err)
	}
	return message, nil
}

// readUnaryBody reads a response body holding AT MOST one message and drains
// it to EOF, which is what lets net/http surface the trailers. nil with no
// error is a bodyless reply — trailers-only, or a failed RPC that streamed
// nothing.
func readUnaryBody(r io.Reader) ([]byte, error) {
	message, err := readMessage(r)
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var probe [1]byte
	if _, err := io.ReadFull(r, probe[:]); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("after the reply message: %w", err)
		}
		return nil, errors.New("a second message on a unary reply")
	}
	return message, nil
}
