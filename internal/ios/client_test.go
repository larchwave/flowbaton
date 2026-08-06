package ios

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// These tests exercise the iOS XCTest runner routes, JSON shapes, and error
// mapping against an in-process HTTP server without requiring a simulator.

type routeCase struct {
	name        string
	call        func(context.Context, *Client) error
	wantMethod  string
	wantPath    string
	wantBody    map[string]any
	respondWith string
}

// routeCases is one case per contract route, shared by the behavior and
// completeness checks.
func routeCases() []routeCase {
	return []routeCase{
		{
			name: "runningApp",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.RunningApp(ctx, []string{"com.example.a"})
				return err
			},
			wantMethod:  http.MethodGet,
			wantPath:    "/runningApp",
			wantBody:    map[string]any{"appIds": []any{"com.example.a"}},
			respondWith: `{"runningAppBundleId":"com.example.a"}`,
		},
		{
			name: "swipe",
			call: func(ctx context.Context, client *Client) error {
				return client.Swipe(ctx, SwipeRequest{
					AppID: "com.example.a", StartX: 1, StartY: 2, EndX: 3, EndY: 4, Duration: 0.5,
				})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/swipe",
			wantBody: map[string]any{
				"appId": "com.example.a", "startX": 1.0, "startY": 2.0,
				"endX": 3.0, "endY": 4.0, "duration": 0.5,
			},
		},
		{
			name: "swipeV2",
			call: func(ctx context.Context, client *Client) error {
				return client.SwipeV2(ctx, SwipeV2Request{
					StartX: 1, StartY: 2, EndX: 3, EndY: 4, Duration: 0.5, AppIDs: []string{"com.example.a"},
				})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/swipeV2",
			wantBody: map[string]any{
				"startX": 1.0, "startY": 2.0, "endX": 3.0, "endY": 4.0,
				"duration": 0.5, "appIds": []any{"com.example.a"},
			},
		},
		{
			name: "inputText",
			call: func(ctx context.Context, client *Client) error {
				return client.InputText(ctx, "hello", []string{"com.example.a"})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/inputText",
			wantBody:   map[string]any{"text": "hello", "appIds": []any{"com.example.a"}},
		},
		{
			name: "touch",
			call: func(ctx context.Context, client *Client) error {
				duration := 1.5
				return client.Touch(ctx, TouchRequest{X: 10, Y: 20, Duration: &duration})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/touch",
			wantBody:   map[string]any{"x": 10.0, "y": 20.0, "duration": 1.5},
		},
		{
			name: "touch without a duration omits it",
			call: func(ctx context.Context, client *Client) error {
				return client.Touch(ctx, TouchRequest{X: 10, Y: 20})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/touch",
			// duration is what turns a touch into a long press, so an absent
			// one must not be sent as a zero.
			wantBody: map[string]any{"x": 10.0, "y": 20.0},
		},
		{
			name: "isScreenStatic",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.IsScreenStatic(ctx)
				return err
			},
			wantMethod:  http.MethodGet,
			wantPath:    "/isScreenStatic",
			respondWith: `{"isScreenStatic":true}`,
		},
		{
			name: "pressKey",
			call: func(ctx context.Context, client *Client) error {
				return client.PressKey(ctx, KeyReturn)
			},
			wantMethod: http.MethodPost,
			wantPath:   "/pressKey",
			wantBody:   map[string]any{"key": "return"},
		},
		{
			name: "pressButton",
			call: func(ctx context.Context, client *Client) error {
				return client.PressButton(ctx, ButtonHome)
			},
			wantMethod: http.MethodPost,
			wantPath:   "/pressButton",
			wantBody:   map[string]any{"button": "home"},
		},
		{
			name: "eraseText",
			call: func(ctx context.Context, client *Client) error {
				return client.EraseText(ctx, 3, []string{"com.example.a"})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/eraseText",
			wantBody:   map[string]any{"charactersToErase": 3.0, "appIds": []any{"com.example.a"}},
		},
		{
			name: "deviceInfo",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.DeviceInfo(ctx)
				return err
			},
			wantMethod:  http.MethodGet,
			wantPath:    "/deviceInfo",
			respondWith: `{"widthPoints":390,"heightPoints":844,"widthPixels":1170,"heightPixels":2532}`,
		},
		{
			name: "setOrientation",
			call: func(ctx context.Context, client *Client) error {
				return client.SetOrientation(ctx, OrientationLandscapeLeft)
			},
			wantMethod: http.MethodPost,
			wantPath:   "/setOrientation",
			wantBody:   map[string]any{"orientation": "landscapeLeft"},
		},
		{
			name: "setPermissions",
			call: func(ctx context.Context, client *Client) error {
				return client.SetPermissions(ctx, map[string]string{"camera": "allow"})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/setPermissions",
			wantBody:   map[string]any{"permissions": map[string]any{"camera": "allow"}},
		},
		{
			name: "viewHierarchy",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.ViewHierarchy(ctx, []string{"com.example.a"}, true)
				return err
			},
			wantMethod: http.MethodPost,
			wantPath:   "/viewHierarchy",
			wantBody:   map[string]any{"appIds": []any{"com.example.a"}, "excludeKeyboardElements": true},
			respondWith: `{"depth":1,"axElement":{"identifier":"root","frame":{"X":0,"Y":0,"Width":10,"Height":20},` +
				`"label":"Root","elementType":1,"enabled":true,"horizontalSizeClass":0,"verticalSizeClass":0,` +
				`"selected":false,"hasFocus":false,"windowContextID":0,"displayID":0}}`,
		},
		{
			name: "status",
			call: func(ctx context.Context, client *Client) error {
				return client.Status(ctx)
			},
			wantMethod:  http.MethodGet,
			wantPath:    "/status",
			respondWith: `{"status":"ok"}`,
		},
		{
			name: "keyboard",
			call: func(ctx context.Context, client *Client) error {
				_, err := client.KeyboardVisible(ctx, []string{"com.example.a"})
				return err
			},
			wantMethod:  http.MethodGet,
			wantPath:    "/keyboard",
			wantBody:    map[string]any{"appIds": []any{"com.example.a"}},
			respondWith: `{"isKeyboardVisible":true}`,
		},
		{
			name: "launchApp",
			call: func(ctx context.Context, client *Client) error {
				return client.LaunchApp(ctx, "com.example.a")
			},
			wantMethod: http.MethodPost,
			wantPath:   "/launchApp",
			wantBody:   map[string]any{"bundleId": "com.example.a"},
		},
		{
			name: "terminateApp",
			call: func(ctx context.Context, client *Client) error {
				return client.TerminateApp(ctx, "com.example.a")
			},
			wantMethod: http.MethodPost,
			wantPath:   "/terminateApp",
			wantBody:   map[string]any{"appId": "com.example.a"},
		},
	}
}

func TestClientSpeaksEveryRouteExactly(t *testing.T) {
	t.Parallel()

	for _, test := range routeCases() {
		t.Run(test.name, func(t *testing.T) {
			var seen *http.Request
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				seen = request
				body, _ = io.ReadAll(request.Body)
				if test.respondWith == "" {
					writer.WriteHeader(http.StatusOK)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.respondWith)
			}))
			defer server.Close()

			if err := test.call(context.Background(), NewClient(server.URL)); err != nil {
				t.Fatalf("%s error = %T %v", test.name, err, err)
			}
			if seen.Method != test.wantMethod {
				t.Fatalf("method = %s, want %s", seen.Method, test.wantMethod)
			}
			if seen.URL.Path != test.wantPath {
				t.Fatalf("path = %s, want %s", seen.URL.Path, test.wantPath)
			}
			if test.wantBody == nil {
				if len(body) != 0 {
					t.Fatalf("body = %s, want none", body)
				}
				return
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("body %s is not JSON: %v", body, err)
			}
			if !reflect.DeepEqual(got, test.wantBody) {
				t.Fatalf("body = %#v, want %#v", got, test.wantBody)
			}
		})
	}
}

func TestScreenshotSendsTheCompressedQueryAndReturnsBytes(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		compressed bool
		wantQuery  string
	}{
		{name: "compressed", compressed: true, wantQuery: "compressed=true"},
		{name: "uncompressed", compressed: false, wantQuery: "compressed=false"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.RawQuery != test.wantQuery {
					t.Errorf("query = %q, want %q", request.URL.RawQuery, test.wantQuery)
				}
				writer.Header().Set("Content-Type", "image/png")
				_, _ = writer.Write([]byte{0x89, 'P', 'N', 'G'})
			}))
			defer server.Close()

			image, err := NewClient(server.URL).Screenshot(context.Background(), test.compressed)
			if err != nil {
				t.Fatalf("Screenshot() error = %v", err)
			}
			if !reflect.DeepEqual(image, []byte{0x89, 'P', 'N', 'G'}) {
				t.Fatalf("image = %v, want the four served bytes", image)
			}
		})
	}
}

func TestErrorStatusesMapToTheContractCodes(t *testing.T) {
	t.Parallel()

	// contracts/v0/ios-http.json error_contract: 400 precondition,
	// 408 timeout, 500 internal. The body carries the authoritative code; the
	// status mapping is the fallback when a runner answers without one.
	for _, test := range []struct {
		name     string
		status   int
		body     string
		wantCode Code
	}{
		{name: "precondition", status: http.StatusBadRequest, body: `{"code":"precondition","errorMessage":"bad"}`, wantCode: CodePrecondition},
		{name: "timeout", status: http.StatusRequestTimeout, body: `{"code":"timeout","errorMessage":"slow"}`, wantCode: CodeTimeout},
		{name: "internal", status: http.StatusInternalServerError, body: `{"code":"internal","errorMessage":"boom"}`, wantCode: CodeInternal},
		{name: "bodyless 400 falls back to the status", status: http.StatusBadRequest, body: "", wantCode: CodePrecondition},
		{name: "bodyless 408 falls back to the status", status: http.StatusRequestTimeout, body: "", wantCode: CodeTimeout},
		{name: "unmapped status is internal", status: http.StatusTeapot, body: "", wantCode: CodeInternal},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()

			err := NewClient(server.URL).Status(context.Background())
			var runnerErr *Error
			if !errors.As(err, &runnerErr) {
				t.Fatalf("error = %T %v, want *ios.Error", err, err)
			}
			if runnerErr.Code != test.wantCode {
				t.Fatalf("code = %q, want %q", runnerErr.Code, test.wantCode)
			}
			if runnerErr.Status != test.status {
				t.Fatalf("status = %d, want %d", runnerErr.Status, test.status)
			}
		})
	}
}

func TestTheRunnerMessageSurvivesAMissingCode(t *testing.T) {
	t.Parallel()

	// The contract requires both fields, so a body with only a message is a
	// runner that is already misbehaving — which is exactly when its own
	// explanation is the most valuable thing in the response. Taking the
	// message only when a code is also present would leave an operator with a
	// bare "internal (HTTP 500)".
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(writer, `{"errorMessage":"runner not ready"}`)
	}))
	defer server.Close()

	err := NewClient(server.URL).Status(context.Background())
	var runnerErr *Error
	if !errors.As(err, &runnerErr) {
		t.Fatalf("error = %T %v, want *ios.Error", err, err)
	}
	if runnerErr.Message != "runner not ready" {
		t.Fatalf("message = %q, want the runner's own text", runnerErr.Message)
	}
	// A missing runner code maps from the HTTP status.
	if runnerErr.Code != CodeInternal {
		t.Fatalf("code = %q, want the status fallback", runnerErr.Code)
	}
}

func TestTimeoutErrorsAreNotRetryable(t *testing.T) {
	t.Parallel()

	// The contract pins both XCUITest timeout signatures as non-retryable, so
	// a caller must be able to tell without parsing the message.
	for _, test := range []struct {
		status        int
		wantRetryable bool
	}{
		{status: http.StatusRequestTimeout, wantRetryable: false},
		{status: http.StatusInternalServerError, wantRetryable: true},
		{status: http.StatusBadRequest, wantRetryable: false},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(test.status)
		}))
		err := NewClient(server.URL).Status(context.Background())
		server.Close()
		var runnerErr *Error
		if !errors.As(err, &runnerErr) {
			t.Fatalf("error = %T %v, want *ios.Error", err, err)
		}
		if runnerErr.Retryable() != test.wantRetryable {
			t.Fatalf("status %d retryable = %v, want %v", test.status, runnerErr.Retryable(), test.wantRetryable)
		}
	}
}

func TestStatusRejectsAnUnexpectedBody(t *testing.T) {
	t.Parallel()

	// A runner that answers 200 with something other than {"status":"ok"} is
	// not healthy, and reporting it as healthy would be worse than an error.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, `{"status":"degraded"}`)
	}))
	defer server.Close()

	if err := NewClient(server.URL).Status(context.Background()); err == nil {
		t.Fatal("Status() succeeded on a non-ok body; want a refusal")
	}
}

func TestDefaultPortMatchesTheFrozenContract(t *testing.T) {
	t.Parallel()

	if DefaultPort != 22087 {
		t.Fatalf("DefaultPort = %d, want the contract's 22087", DefaultPort)
	}
	if got := DefaultBaseURL(DefaultPort); got != "http://127.0.0.1:22087" {
		t.Fatalf("DefaultBaseURL() = %q, want the loopback contract address", got)
	}
}
