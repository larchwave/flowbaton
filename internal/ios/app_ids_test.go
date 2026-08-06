package ios

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// `assertVisible` with no app filter passes a nil []string. Go marshals a nil
// slice as JSON `null`, while the Swift runner decodes appIds as [String], so
// decoding null as a non-optional array fails with
//   400 viewHierarchy: Cannot get unkeyed decoding container -- found null value
// A nil slice and an empty slice have equivalent caller semantics but different
// JSON encodings.
//
// Five routes carry appIds, so normalization belongs in one place.

func captureRequestBody(t *testing.T, call func(*Client) error) map[string]any {
	t.Helper()
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("reading request body: %v", err)
			}
			captured = body
			writer.Header().Set("Content-Type", "application/json")
			// Enough of a response for every route under test to succeed.
			_, _ = writer.Write([]byte(`{"appId":"com.example.a","visible":true,` +
				`"windowContextHierarchy":[{"axElement":{"elementType":0,"frame":` +
				`{"x":0,"y":0,"width":1,"height":1}}}]}`))
		}))
	t.Cleanup(server.Close)

	if err := call(NewClient(server.URL)); err != nil {
		t.Fatalf("call error = %v", err)
	}
	if len(captured) == 0 {
		t.Fatal("no request body was sent")
	}
	var decoded map[string]any
	if err := json.Unmarshal(captured, &decoded); err != nil {
		t.Fatalf("request body %s is not JSON: %v", captured, err)
	}
	if strings.Contains(string(captured), `"appIds":null`) {
		t.Fatalf("appIds was sent as null: %s", captured)
	}
	return decoded
}

func TestNoAppFilterSendsAnEmptyListNotNull(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		call func(*Client) error
	}{
		{
			name: "viewHierarchy",
			call: func(client *Client) error {
				_, err := client.ViewHierarchy(context.Background(), nil, false)
				return err
			},
		},
		{
			name: "runningApp",
			call: func(client *Client) error {
				_, err := client.RunningApp(context.Background(), nil)
				return err
			},
		},
		{
			name: "inputText",
			call: func(client *Client) error {
				return client.InputText(context.Background(), "hello", nil)
			},
		},
		{
			name: "eraseText",
			call: func(client *Client) error {
				return client.EraseText(context.Background(), 3, nil)
			},
		},
		{
			name: "keyboard",
			call: func(client *Client) error {
				_, err := client.KeyboardVisible(context.Background(), nil)
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := captureRequestBody(t, test.call)
			value, present := body["appIds"]
			if !present {
				t.Fatalf("appIds is absent from %#v", body)
			}
			list, ok := value.([]any)
			if !ok {
				t.Fatalf("appIds = %#v, want an array", value)
			}
			if len(list) != 0 {
				t.Fatalf("appIds = %#v, want it empty", list)
			}
		})
	}
}

func TestAnAppFilterIsStillSent(t *testing.T) {
	t.Parallel()

	// The control: coercing every list to empty would satisfy the test above
	// and silently drop the filter the caller asked for.
	body := captureRequestBody(t, func(client *Client) error {
		_, err := client.ViewHierarchy(
			context.Background(), []string{"com.example.a", "com.example.b"}, false)
		return err
	})
	list, ok := body["appIds"].([]any)
	if !ok || len(list) != 2 || list[0] != "com.example.a" {
		t.Fatalf("appIds = %#v, want both requested bundle ids", body["appIds"])
	}
}
