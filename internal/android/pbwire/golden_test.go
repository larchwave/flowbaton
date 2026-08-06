package pbwire

import (
	"bytes"
	"encoding/hex"
	"math"
	"reflect"
	"testing"
)

// Golden bytes with the arithmetic derived by hand in comments. The round
// trips in roundtrip_test.go prove the two halves agree with each other;
// these hex literals pin proto3 encoding itself, so a shared
// mistake cannot pass. Tag arithmetic throughout: tag = field<<3 | wiretype,
// wiretype 0 = varint, 1 = fixed64, 2 = length-delimited, 5 = fixed32.

func mustHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("bad hex literal %q: %v", value, err)
	}
	return decoded
}

func TestMarshalWritesTheExactProto3Bytes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		message interface{ Marshal() []byte }
		want    string
	}{
		{"a message with no fields is zero bytes", DeviceInfoRequest{}, ""},
		// x: tag = 1<<3|0 = 0x08, varint 10 = 0x0a.
		// y: tag = 2<<3|0 = 0x10, varint 20 = 0x14.
		{"tap carries both coordinates", TapRequest{X: 10, Y: 20}, "080a1014"},
		// proto3 omits a zero scalar entirely rather than sending the default.
		{"tap at the origin sends nothing", TapRequest{}, ""},
		{"a zero coordinate is omitted, the other kept", TapRequest{Y: 7}, "1007"},
		// text: tag = 1<<3|2 = 0x0a, length 2, then "hi" = 0x68 0x69.
		{"input text is length-delimited", InputTextRequest{Text: "hi"}, "0a026869"},
		// 300 = 0b10_0101100: low 7 bits 0101100|0x80 = 0xac, then 0b10 = 0x02.
		{"a count above 127 spans two varint bytes",
			EraseAllTextRequest{CharactersToErase: 300}, "08ac02"},
		// 300000 = 0x493e0 = 0b1_0010_0100111_1100000:
		// 0xe0 = 1100000|0x80, 0xa7 = 0100111|0x80, 0x12 = 10010.
		{"a large count spans three varint bytes",
			EraseAllTextRequest{CharactersToErase: 300000}, "08e0a712"},
		{"a window check names the app", CheckWindowUpdatingRequest{AppID: "a"}, "0a0161"},
		{"a window that is updating is one varint", CheckWindowUpdatingResponse{IsWindowUpdating: true}, "0801"},
		// latitude: tag = 1<<3|1 = 0x09; 1.0 = 0x3ff0000000000000 little-endian.
		// longitude: tag = 2<<3|1 = 0x11; -2.5 = 0xc004000000000000 little-endian.
		{"a location is two little-endian fixed64 doubles",
			SetLocationRequest{Latitude: 1, Longitude: -2.5},
			"09000000000000f03f" + "1100000000000004c0"},
		// width 960 = 0b111_1000000: 0xc0, 0x07. height 1920 = 0b1111_0000000: 0x80, 0x0f.
		{"device info is two varints", DeviceInfo{WidthPixels: 960, HeightPixels: 1920},
			"08c00710800f"},
		{"a hierarchy is one string field", ViewHierarchyResponse{Hierarchy: "<h>"}, "0a033c683e"},
		{"screenshot bytes go out verbatim", ScreenshotResponse{Bytes: []byte{0x89, 0x50}}, "0a028950"},
		// key: 0x0a 01 6b; value: tag 2<<3|2 = 0x12, 01 76; type: tag 3<<3|2 = 0x1a, then 16 bytes.
		{"an argument value is three strings",
			ArgumentValue{Key: "k", Value: "v", Type: "java.lang.String"},
			"0a016b" + "120176" + "1a106a6176612e6c616e672e537472696e67"},
		// Each repeated element is its own tag-2 field. The first element body
		// is 0a016b 120176 1a10 + 16 bytes = 24 = 0x18; the second swaps in
		// "n"/"1"/java.lang.Integer (17 bytes) for a body of 25 = 0x19.
		{"launch arguments repeat the same field number",
			LaunchAppRequest{PackageName: "p", Arguments: []ArgumentValue{
				{Key: "k", Value: "v", Type: "java.lang.String"},
				{Key: "n", Value: "1", Type: "java.lang.Integer"},
			}},
			"0a0170" +
				"12180a016b1201761a106a6176612e6c616e672e537472696e67" +
				"12190a016e1201311a116a6176612e6c616e672e496e7465676572"},
		// payload body = 0a020102 (field 1, two bytes), wrapped as field 1
		// length 4; media_name tag 0x12; media_ext tag 0x1a.
		{"a nested payload is a length-delimited submessage",
			AddMediaRequest{Payload: &Payload{Data: []byte{1, 2}}, MediaName: "a", MediaExt: "png"},
			"0a040a020102" + "120161" + "1a03706e67"},
		// A present-but-empty message is a tag with length zero: presence of an
		// embedded message survives even when every field inside is default.
		{"a present empty payload is a zero-length field",
			AddMediaRequest{Payload: &Payload{}}, "0a00"},
		{"an absent payload writes no field at all", AddMediaRequest{MediaName: "a"}, "120161"},
		{"payload data is one bytes field", Payload{Data: []byte{9}}, "0a0109"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := testCase.message.Marshal()
			if want := mustHex(t, testCase.want); !bytes.Equal(got, want) {
				t.Fatalf("Marshal() = %x, want %x", got, want)
			}
		})
	}
}

func TestNegativeZeroGoesOnTheWireBecauseProtobufChecksBits(t *testing.T) {
	t.Parallel()

	// Proto3 omits only the all-zero bit pattern, so -0.0 is sent while +0.0 is
	// not. Checking the float value would drop -0.0, and a dropped
	// longitude reads as the prime meridian.
	encoded := SetLocationRequest{Latitude: math.Copysign(0, -1)}.Marshal()
	if want := mustHex(t, "090000000000000080"); !bytes.Equal(encoded, want) {
		t.Fatalf("Marshal(-0.0) = %x, want %x", encoded, want)
	}
	if positive := (SetLocationRequest{Latitude: 0}).Marshal(); len(positive) != 0 {
		t.Fatalf("Marshal(+0.0) = %x, want nothing", positive)
	}
}

func TestUnmarshalToleratesWhatProto3RequiresItTo(t *testing.T) {
	t.Parallel()

	t.Run("fields arrive in any order", func(t *testing.T) {
		t.Parallel()
		var info DeviceInfo
		if err := info.Unmarshal(mustHex(t, "10800f08c007")); err != nil {
			t.Fatal(err)
		}
		if info.WidthPixels != 960 || info.HeightPixels != 1920 {
			t.Fatalf("DeviceInfo = %#v, want 960x1920", info)
		}
	})

	t.Run("unknown fields of every wire type are skipped", func(t *testing.T) {
		t.Parallel()
		// A newer agent may add fields; refusing them would make an upgrade on
		// the device break the host. Field 5 varint (0x28), field 6
		// length-delimited (0x32), field 7 fixed64 (0x39), field 9 fixed32 (0x4d).
		var info DeviceInfo
		payload := "08c007" + "28ff01" + "3202abcd" + "390000000000000000" +
			"4d00000000" + "10800f"
		if err := info.Unmarshal(mustHex(t, payload)); err != nil {
			t.Fatal(err)
		}
		if info.WidthPixels != 960 || info.HeightPixels != 1920 {
			t.Fatalf("DeviceInfo = %#v, want 960x1920", info)
		}
	})

	t.Run("the last value of a duplicated scalar wins", func(t *testing.T) {
		t.Parallel()
		var info DeviceInfo
		if err := info.Unmarshal(mustHex(t, "08010802")); err != nil {
			t.Fatal(err)
		}
		if info.WidthPixels != 2 {
			t.Fatalf("WidthPixels = %d, want the last value 2", info.WidthPixels)
		}
	})

	t.Run("any non-zero varint is a true bool", func(t *testing.T) {
		t.Parallel()
		// protobuf does not promise a 1; an == 1 check would read a 2 as false.
		var response CheckWindowUpdatingResponse
		if err := response.Unmarshal(mustHex(t, "0802")); err != nil {
			t.Fatal(err)
		}
		if !response.IsWindowUpdating {
			t.Fatal("IsWindowUpdating = false for varint 2")
		}
	})

	t.Run("no bytes at all is the zero message, not an error", func(t *testing.T) {
		t.Parallel()
		var response CheckWindowUpdatingResponse
		if err := response.Unmarshal(nil); err != nil {
			t.Fatal(err)
		}
		if response.IsWindowUpdating {
			t.Fatal("IsWindowUpdating = true from no bytes at all")
		}
	})
}

func TestUnmarshalRefusesBytesItCannotTrust(t *testing.T) {
	t.Parallel()

	// A truncated frame from the device is a transport problem the caller has
	// to hear about, not a zero-valued screenshot.
	cases := map[string]string{
		"a varint that never terminates":      "08ffffffffffffffffffff7f",
		"a varint cut off by the frame end":   "0880",
		"a tag with no bytes behind it":       "08",
		"a length that runs past the end":     "0a05616263",
		"a field number of zero":              "0000",
		"wire type 3 is a proto2 group":       "3b00",
		"wire type 6 is not assigned":         "3e00",
		"a truncated unknown fixed64":         "390000",
		"a truncated unknown fixed32":         "3d00",
		"a wrong wire type for a known field": "0d00000000",
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var info DeviceInfo
			if err := info.Unmarshal(mustHex(t, payload)); err == nil {
				t.Fatalf("Unmarshal(%s) accepted the bytes and produced %#v", payload, info)
			}
		})
	}
}

func TestDecodedBytesDoNotAliasTheFrame(t *testing.T) {
	t.Parallel()

	// The frame is a buffer the transport will reuse. Handing out a subslice
	// of it means a screenshot changes under the caller after the next read.
	frame := mustHex(t, "0a028950")
	var response ScreenshotResponse
	if err := response.Unmarshal(frame); err != nil {
		t.Fatal(err)
	}
	frame[2] = 0xff
	if !reflect.DeepEqual(response.Bytes, []byte{0x89, 0x50}) {
		t.Fatal("the decoded bytes changed when the frame was overwritten")
	}
}
