package pbwire

import (
	"errors"
	"reflect"
	"testing"
)

// Round trips prove the encoder and the decoder agree on every field of every
// message. On their own they would also pass when both halves share the same
// mistake, which is why the golden hex literals in proto_test.go exist; the
// two files are one gate with two directions.

type wireMessage[T any] interface {
	*T
	Marshal() []byte
	Unmarshal([]byte) error
}

func assertRoundTrip[T any, P wireMessage[T]](t *testing.T, name string, in T) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		t.Parallel()
		encoded := P(&in).Marshal()
		var got T
		if err := P(&got).Unmarshal(encoded); err != nil {
			t.Fatalf("Unmarshal(Marshal()) = %v on % x", err, encoded)
		}
		if !reflect.DeepEqual(got, in) {
			t.Fatalf("round trip changed the message:\n got %#v\nwant %#v", got, in)
		}
	})
}

func TestEveryMessageSurvivesARoundTrip(t *testing.T) {
	t.Parallel()

	assertRoundTrip(t, "DeviceInfoRequest", DeviceInfoRequest{})
	assertRoundTrip(t, "DeviceInfo", DeviceInfo{WidthPixels: 1080, HeightPixels: 2400})
	assertRoundTrip(t, "ViewHierarchyRequest", ViewHierarchyRequest{})
	assertRoundTrip(t, "ViewHierarchyResponse", ViewHierarchyResponse{Hierarchy: "<node text='&amp;'/>"})
	assertRoundTrip(t, "ScreenshotRequest", ScreenshotRequest{})
	assertRoundTrip(t, "ScreenshotResponse", ScreenshotResponse{Bytes: []byte{0x89, 'P', 'N', 'G', 0}})
	assertRoundTrip(t, "TapRequest", TapRequest{X: 540, Y: 1200})
	assertRoundTrip(t, "TapResponse", TapResponse{})
	assertRoundTrip(t, "InputTextRequest", InputTextRequest{Text: "héllo\n"})
	assertRoundTrip(t, "InputTextResponse", InputTextResponse{})
	assertRoundTrip(t, "EraseAllTextRequest", EraseAllTextRequest{CharactersToErase: 300000})
	assertRoundTrip(t, "EraseAllTextResponse", EraseAllTextResponse{})
	assertRoundTrip(t, "SetLocationRequest", SetLocationRequest{Latitude: 37.7749, Longitude: -122.4194})
	assertRoundTrip(t, "SetLocationResponse", SetLocationResponse{})
	assertRoundTrip(t, "CheckWindowUpdatingRequest", CheckWindowUpdatingRequest{AppID: "com.example.app"})
	assertRoundTrip(t, "CheckWindowUpdatingResponse", CheckWindowUpdatingResponse{IsWindowUpdating: true})
	assertRoundTrip(t, "LaunchAppRequest", LaunchAppRequest{
		PackageName: "com.example.app",
		Arguments: []ArgumentValue{
			{Key: "flag", Value: "true", Type: "java.lang.Boolean"},
			{Key: "user", Value: "u1", Type: "java.lang.String"},
		},
	})
	assertRoundTrip(t, "LaunchAppResponse", LaunchAppResponse{})
	assertRoundTrip(t, "ArgumentValue", ArgumentValue{Key: "k", Value: "v", Type: "t"})
	assertRoundTrip(t, "AddMediaRequest with a payload", AddMediaRequest{
		Payload:   &Payload{Data: []byte{1, 2, 3}},
		MediaName: "clip",
		MediaExt:  "mp4",
	})
	assertRoundTrip(t, "AddMediaRequest with a present empty payload", AddMediaRequest{Payload: &Payload{}})
	assertRoundTrip(t, "AddMediaRequest with no payload chunk", AddMediaRequest{MediaName: "clip", MediaExt: "mp4"})
	assertRoundTrip(t, "AddMediaResponse", AddMediaResponse{})
	assertRoundTrip(t, "Payload", Payload{Data: []byte("chunk")})
	assertRoundTrip(t, "EmptyRequest", EmptyRequest{})
	assertRoundTrip(t, "EmptyResponse", EmptyResponse{})
}

// The malformed table in proto_test.go aims everything at DeviceInfo, whose
// known fields are varints. Each field TYPE has its own way of being wrong,
// so each gets its own refusal case here.
func TestEachFieldTypeRefusesItsOwnMalformedShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		unmarshal func([]byte) error
		data      string
		wantErr   error
	}{
		// tag 0x08 = field 1 varint, but hierarchy is a string field.
		{"string field sent as a varint",
			func(b []byte) error { return new(ViewHierarchyResponse).Unmarshal(b) },
			"0801", errWireType},
		// tag 0x0a, declared length 5, only "abc" behind it.
		{"string length runs past the end",
			func(b []byte) error { return new(ViewHierarchyResponse).Unmarshal(b) },
			"0a05616263", errLengthOverrun},
		// latitude is fixed64; a varint tag for it is a different message shape.
		{"double field sent as a varint",
			func(b []byte) error { return new(SetLocationRequest).Unmarshal(b) },
			"0801", errWireType},
		// tag 0x09 promises 8 bytes of IEEE-754 and delivers 7.
		{"double truncated mid-value",
			func(b []byte) error { return new(SetLocationRequest).Unmarshal(b) },
			"09000000000000f0", errTruncated},
		// widthPixels is a varint; tag 0x0a hands it a length-delimited body.
		{"uint32 field sent as bytes",
			func(b []byte) error { return new(DeviceInfo).Unmarshal(b) },
			"0a0100", errWireType},
		// isWindowUpdating is a varint bool; tag 0x09 is fixed64.
		{"bool field sent as fixed64",
			func(b []byte) error { return new(CheckWindowUpdatingResponse).Unmarshal(b) },
			"090000000000000000", errWireType},
		// tag 0x12 wraps two bytes 0x0a 0x02: a nested ArgumentValue whose own
		// field 1 declares length 2 with nothing behind it.
		{"malformed nested launch argument",
			func(b []byte) error { return new(LaunchAppRequest).Unmarshal(b) },
			"12020a02", errLengthOverrun},
		{"malformed nested payload",
			func(b []byte) error { return new(AddMediaRequest).Unmarshal(b) },
			"0a020a02", errLengthOverrun},
		// A message with no declared fields still refuses a tag whose value
		// never arrives; tolerating unknown fields is not tolerating garbage.
		{"empty message with a truncated unknown field",
			func(b []byte) error { return new(TapResponse).Unmarshal(b) },
			"08", errTruncated},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			err := testCase.unmarshal(mustHex(t, testCase.data))
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("Unmarshal(%s) = %v, want %v", testCase.data, err, testCase.wantErr)
			}
		})
	}
}

// Empty messages are the ones a newer agent is most likely to grow fields on,
// so they must skip well-formed unknown fields of every wire type.
func TestAnEmptyMessageSkipsUnknownFieldsOfEveryWireType(t *testing.T) {
	t.Parallel()

	// Field 5 varint, 6 length-delimited, 7 fixed64, 9 fixed32.
	payload := "28ff01" + "3202abcd" + "390000000000000000" + "4d00000000"
	var response TapResponse
	if err := response.Unmarshal(mustHex(t, payload)); err != nil {
		t.Fatalf("Unmarshal(%s) = %v, want unknown fields skipped", payload, err)
	}
}
