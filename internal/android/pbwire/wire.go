// Package pbwire is a hand-rolled proto3 codec for exactly the messages of
// proto/flowbaton_android.proto. The frozen Android v0 surface is 12 rpcs over
// 24 small messages, and transcribing the public protobuf encoding rules
// costs less than adopting protobuf-go as a third runtime dependency
// (docs/dependency-policy.md). contract_test.go reads the frozen proto so
// this package cannot drift from it silently.
package pbwire

import (
	"errors"
	"fmt"
	"math"
)

// Proto3 wire types, from the public protobuf encoding documentation.
// Types 3 and 4 (groups) predate proto3 and 6/7 are unassigned; none of the
// four can appear in a well-formed frame from the agent.
const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

var (
	errTruncated     = errors.New("pbwire: truncated message")
	errVarintTooLong = errors.New("pbwire: varint exceeds ten bytes")
	errLengthOverrun = errors.New("pbwire: length runs past the end of the message")
	errFieldZero     = errors.New("pbwire: field number zero is reserved")
	errWireType      = errors.New("pbwire: wrong wire type")
)

// --- encoding ---

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendTag(b []byte, field, wire int) []byte {
	return appendVarint(b, uint64(field)<<3|uint64(wire))
}

// Scalar appenders omit the field entirely at the zero value, as proto3
// requires; the receiver reads an absent field back as that same zero.

func appendUint32(b []byte, field int, v uint32) []byte {
	if v == 0 {
		return b
	}
	return appendVarint(appendTag(b, field, wireVarint), uint64(v))
}

func appendBool(b []byte, field int, v bool) []byte {
	if !v {
		return b
	}
	return append(appendTag(b, field, wireVarint), 1)
}

// appendDouble checks bits, not values: proto3 emits -0.0 but omits +0.0.
func appendDouble(b []byte, field int, v float64) []byte {
	bits := math.Float64bits(v)
	if bits == 0 {
		return b
	}
	b = appendTag(b, field, wireFixed64)
	return append(b,
		byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24),
		byte(bits>>32), byte(bits>>40), byte(bits>>48), byte(bits>>56))
}

func appendString(b []byte, field int, v string) []byte {
	if v == "" {
		return b
	}
	b = appendVarint(appendTag(b, field, wireBytes), uint64(len(v)))
	return append(b, v...)
}

func appendBytes(b []byte, field int, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	b = appendVarint(appendTag(b, field, wireBytes), uint64(len(v)))
	return append(b, v...)
}

// appendMessage always writes the field: presence of an embedded message is
// the caller's decision (a nil pointer is absent, a pointer to an empty
// message is present with length zero).
func appendMessage(b []byte, field int, encoded []byte) []byte {
	b = appendVarint(appendTag(b, field, wireBytes), uint64(len(encoded)))
	return append(b, encoded...)
}

// --- decoding ---

func consumeVarint(data []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(data) && i < 10; i++ {
		v |= uint64(data[i]&0x7f) << (7 * i)
		if data[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(data) >= 10 {
		return 0, 0, errVarintTooLong
	}
	return 0, 0, fmt.Errorf("%w: unterminated varint", errTruncated)
}

func consumeTag(data []byte) (field, wire, n int, err error) {
	v, n, err := consumeVarint(data)
	if err != nil {
		return 0, 0, 0, err
	}
	if v>>3 == 0 {
		return 0, 0, 0, errFieldZero
	}
	return int(v >> 3), int(v & 7), n, nil
}

func consumeLengthDelimited(data []byte) ([]byte, int, error) {
	length, n, err := consumeVarint(data)
	if err != nil {
		return nil, 0, err
	}
	if length > uint64(len(data)-n) {
		return nil, 0, errLengthOverrun
	}
	return data[n : n+int(length)], n + int(length), nil
}

// skipField discards one unknown field's value by wire type, so a frame from
// a newer agent decodes instead of failing — but malformed bytes still refuse.
func skipField(data []byte, wire int) (int, error) {
	switch wire {
	case wireVarint:
		_, n, err := consumeVarint(data)
		return n, err
	case wireFixed64:
		if len(data) < 8 {
			return 0, fmt.Errorf("%w: fixed64 needs 8 bytes, have %d", errTruncated, len(data))
		}
		return 8, nil
	case wireBytes:
		_, n, err := consumeLengthDelimited(data)
		return n, err
	case wireFixed32:
		if len(data) < 4 {
			return 0, fmt.Errorf("%w: fixed32 needs 4 bytes, have %d", errTruncated, len(data))
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("%w: wire type %d is not assigned in proto3", errWireType, wire)
	}
}

// Typed consumers refuse a known field arriving with the wrong wire type:
// that is not a newer schema, it is a different message shape.

func consumeVarintField(data []byte, wire, field int) (uint64, int, error) {
	if wire != wireVarint {
		return 0, 0, fmt.Errorf("field %d: %w: got %d, want varint", field, errWireType, wire)
	}
	return consumeVarint(data)
}

func consumeBytesField(data []byte, wire, field int) ([]byte, int, error) {
	if wire != wireBytes {
		return nil, 0, fmt.Errorf("field %d: %w: got %d, want length-delimited", field, errWireType, wire)
	}
	return consumeLengthDelimited(data)
}

func consumeDoubleField(data []byte, wire, field int) (float64, int, error) {
	if wire != wireFixed64 {
		return 0, 0, fmt.Errorf("field %d: %w: got %d, want fixed64", field, errWireType, wire)
	}
	if len(data) < 8 {
		return 0, 0, fmt.Errorf("field %d: %w: fixed64 needs 8 bytes, have %d", field, errTruncated, len(data))
	}
	bits := uint64(data[0]) | uint64(data[1])<<8 | uint64(data[2])<<16 | uint64(data[3])<<24 |
		uint64(data[4])<<32 | uint64(data[5])<<40 | uint64(data[6])<<48 | uint64(data[7])<<56
	return math.Float64frombits(bits), 8, nil
}

// discardAll walks a message whose type declares no fields, tolerating any
// well-formed field a newer agent may have added and refusing anything else.
func discardAll(data []byte) error {
	for len(data) > 0 {
		_, wire, n, err := consumeTag(data)
		if err != nil {
			return err
		}
		data = data[n:]
		skip, err := skipField(data, wire)
		if err != nil {
			return err
		}
		data = data[skip:]
	}
	return nil
}
