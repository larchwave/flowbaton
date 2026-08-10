package js

import (
	"context"
	"errors"
	"io"
	"math/rand"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHTTPRequestMethodHeadersBodyAndResponseShape(t *testing.T) {
	t.Parallel()

	type receivedRequest struct {
		method string
		header string
		body   string
	}
	received := make(chan receivedRequest, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		received <- receivedRequest{
			method: request.Method,
			header: request.Header.Get("X-Request"),
			body:   string(body),
		}
		return &http.Response{
			StatusCode: http.StatusTeapot,
			Header: http.Header{
				"X-Reply": {"one", "two"},
			},
			Body:    io.NopCloser(strings.NewReader("reply body")),
			Request: request,
		}, nil
	})}

	factory, err := NewFactory(Config{
		Random:     rand.New(rand.NewSource(23)),
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	script := `var response = http.request(` + strconv.Quote("http://example.test/resource") + `, {
		method: "PATCH",
		headers: {"X-Request": "request value"},
		body: "request body"
	}); JSON.stringify([response.ok, response.status, response.body, response.headers["x-reply"]])`
	result, err := runtime.Evaluate(context.Background(), EvalRequest{Script: script})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	const wantResponse = `[false,418,"reply body","one,two"]`
	if result.Value != wantResponse {
		t.Fatalf("HTTP response = %#v, want %s", result.Value, wantResponse)
	}
	if got := <-received; got != (receivedRequest{method: "PATCH", header: "request value", body: "request body"}) {
		t.Fatalf("received request = %#v", got)
	}
}

func TestHTTPConvenienceMethodsAndFiveMinuteTimeout(t *testing.T) {
	t.Parallel()

	methods := make(chan string, 4)
	configuredClient := &http.Client{
		Timeout: time.Second,
		Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			methods <- request.Method
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
	}
	factory, err := NewFactory(Config{
		Random:     rand.New(rand.NewSource(29)),
		HTTPClient: configuredClient,
	})
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtimeContract, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtimeContract.Close() })
	runtimeImplementation := runtimeContract.(*runtime)
	if runtimeImplementation.httpClient.Timeout != DefaultHTTPTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", runtimeImplementation.httpClient.Timeout, DefaultHTTPTimeout)
	}

	for _, method := range []string{"get", "post", "put", "delete"} {
		result, err := runtimeContract.Evaluate(context.Background(), EvalRequest{
			Script: `http.` + method + `("http://example.test").status`,
		})
		if err != nil {
			t.Fatalf("http.%s Evaluate() error = %v", method, err)
		}
		if result.Value != int64(http.StatusNoContent) {
			t.Fatalf("http.%s status = %#v, want %d", method, result.Value, http.StatusNoContent)
		}
	}
	for _, want := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
		if got := <-methods; got != want {
			t.Fatalf("HTTP method = %q, want %q", got, want)
		}
	}

	defaultRuntime := newTestRuntime(t).(*runtime)
	defaultTransport, ok := defaultRuntime.httpClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default HTTP transport = %T, want *http.Transport", defaultRuntime.httpClient.Transport)
	}
	if defaultTransport.ForceAttemptHTTP2 {
		t.Fatal("default HTTP transport enables HTTP/2; compatibility surface requires HTTP/1.1")
	}
}

func TestHTTPMultipartResolvesFilesRelativeToScriptDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptDir := filepath.Join(root, "scripts")
	mediaDir := filepath.Join(scriptDir, "media")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir) error = %v", err)
	}
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(mediaDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "payload.txt"), []byte("file contents"), 0o644); err != nil {
		t.Fatalf("WriteFile(payload) error = %v", err)
	}

	type part struct {
		name      string
		fileName  string
		mediaType string
		body      string
	}
	parts := make(chan []part, 1)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mediaType, parameters, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		if mediaType != "multipart/form-data" {
			return nil, errors.New("request is not multipart/form-data")
		}
		reader := multipart.NewReader(request.Body, parameters["boundary"])
		var captured []part
		for {
			item, nextErr := reader.NextPart()
			if errors.Is(nextErr, io.EOF) {
				break
			}
			if nextErr != nil {
				return nil, nextErr
			}
			contents, readErr := io.ReadAll(item)
			if readErr != nil {
				return nil, readErr
			}
			captured = append(captured, part{
				name:      item.FormName(),
				fileName:  item.FileName(),
				mediaType: item.Header.Get("Content-Type"),
				body:      string(contents),
			})
		}
		parts <- captured
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("uploaded")),
			Request:    request,
		}, nil
	})}
	runtime := newRuntimeWithConfig(t, Config{
		Random:     rand.New(rand.NewSource(31)),
		HTTPClient: client,
	})
	result, err := runtime.Evaluate(context.Background(), EvalRequest{
		ScriptDir: scriptDir,
		Script: `http.post("http://example.test/upload", {multipartForm: {
			uploadType: "import",
			data: {filePath: "media/payload.txt", mediaType: "text/plain"}
		}}).body`,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.Value != "uploaded" {
		t.Fatalf("multipart response = %#v, want uploaded", result.Value)
	}
	wantParts := []part{
		{name: "data", fileName: "payload.txt", mediaType: "text/plain", body: "file contents"},
		{name: "uploadType", body: "import"},
	}
	gotParts := <-parts
	if len(gotParts) != len(wantParts) {
		t.Fatalf("multipart parts = %#v, want %#v", gotParts, wantParts)
	}
	for index := range wantParts {
		if gotParts[index] != wantParts[index] {
			t.Fatalf("multipart part %d = %#v, want %#v", index, gotParts[index], wantParts[index])
		}
	}
}

func TestHTTPMultipartRejectsFilesOutsideScriptDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptDir := filepath.Join(root, "scripts")
	outsideDir := filepath.Join(root, "outside")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(scriptDir) error = %v", err)
	}
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(outsideDir) error = %v", err)
	}
	outsideFile := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("WriteFile(outside) error = %v", err)
	}
	symlinkPath := filepath.Join(scriptDir, "escape.txt")
	if err := os.Symlink(outsideFile, symlinkPath); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	tests := []struct {
		name      string
		scriptDir string
		filePath  string
	}{
		{name: "absolute path", scriptDir: scriptDir, filePath: outsideFile},
		{name: "lexical traversal", scriptDir: scriptDir, filePath: "../outside/secret.txt"},
		{name: "symlink escape", scriptDir: scriptDir, filePath: "escape.txt"},
		{name: "missing script directory", filePath: "secret.txt"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transportCalled := false
			runtime := newRuntimeWithConfig(t, Config{
				Random: rand.New(rand.NewSource(43)),
				HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
					transportCalled = true
					return nil, errors.New("transport must not be called")
				})},
			})
			_, err := runtime.Evaluate(context.Background(), EvalRequest{
				ScriptDir: test.scriptDir,
				Script: `http.post("http://example.test/upload", {multipartForm: {
					data: {filePath: ` + strconv.Quote(test.filePath) + `}
				}})`,
			})
			if err == nil || !strings.Contains(err.Error(), "multipart file") {
				t.Fatalf("Evaluate() error = %v, want multipart file confinement error", err)
			}
			if transportCalled {
				t.Fatal("HTTP transport was called after multipart confinement failure")
			}
		})
	}
}

func TestHTTPMultipartRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	filePath := filepath.Join(scriptDir, "too-large.bin")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := file.Truncate(MaxMultipartFileSize + 1); err != nil {
		_ = file.Close()
		t.Fatalf("Truncate() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	transportCalled := false
	runtime := newRuntimeWithConfig(t, Config{
		Random: rand.New(rand.NewSource(47)),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			transportCalled = true
			return nil, errors.New("transport must not be called")
		})},
	})
	_, err = runtime.Evaluate(context.Background(), EvalRequest{
		ScriptDir: scriptDir,
		Script: `http.post("http://example.test/upload", {multipartForm: {
			data: {filePath: "too-large.bin"}
		}})`,
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Evaluate() error = %v, want file size error", err)
	}
	if transportCalled {
		t.Fatal("HTTP transport was called after multipart size failure")
	}
}

func TestHTTPMultipartRejectsExcessivePartsAndAggregateSize(t *testing.T) {
	t.Parallel()

	t.Run("part count", func(t *testing.T) {
		t.Parallel()
		implementation := newRuntimeWithConfig(t, Config{Random: rand.New(rand.NewSource(53))}).(*runtime)
		form := make(map[string]any, MaxMultipartParts+1)
		for index := 0; index <= MaxMultipartParts; index++ {
			form[strconv.Itoa(index)] = "value"
		}
		if _, _, err := implementation.requestBody(map[string]any{"multipartForm": form}); err == nil || !strings.Contains(err.Error(), "maximum") {
			t.Fatalf("requestBody() error = %v, want part-count limit", err)
		}
	})

	t.Run("aggregate encoded bytes", func(t *testing.T) {
		t.Parallel()
		scriptDir := t.TempDir()
		for _, name := range []string{"one.bin", "two.bin"} {
			file, err := os.Create(filepath.Join(scriptDir, name))
			if err != nil {
				t.Fatalf("Create(%s) error = %v", name, err)
			}
			if err := file.Truncate(MaxMultipartFileSize); err != nil {
				_ = file.Close()
				t.Fatalf("Truncate(%s) error = %v", name, err)
			}
			if err := file.Close(); err != nil {
				t.Fatalf("Close(%s) error = %v", name, err)
			}
		}

		implementation := newRuntimeWithConfig(t, Config{Random: rand.New(rand.NewSource(59))}).(*runtime)
		implementation.scriptDir = scriptDir
		form := map[string]any{
			"one": map[string]any{"filePath": "one.bin"},
			"two": map[string]any{"filePath": "two.bin"},
		}
		if _, _, err := implementation.requestBody(map[string]any{"multipartForm": form}); err == nil || !strings.Contains(err.Error(), "multipart request exceeds") {
			t.Fatalf("requestBody() error = %v, want aggregate byte limit", err)
		}
	})
}

func TestHTTPResponseBodyIsBoundedAndCancellationAware(t *testing.T) {
	t.Parallel()

	t.Run("overflow", func(t *testing.T) {
		t.Parallel()
		implementation := newRuntimeWithConfig(t, Config{
			Random: rand.New(rand.NewSource(61)),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(io.LimitReader(repeatingByteReader{}, MaxHTTPResponseSize+1)),
					Request:    request,
				}, nil
			})},
		}).(*runtime)
		if _, err := implementation.executeHTTPRequest("http://example.test", http.MethodGet, nil); err == nil || !strings.Contains(err.Error(), "body exceeds") {
			t.Fatalf("executeHTTPRequest() error = %v, want response-size limit", err)
		}
	})

	t.Run("cancellation while reading", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		implementation := newRuntimeWithConfig(t, Config{
			Random: rand.New(rand.NewSource(67)),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       &contextReadCloser{ctx: request.Context(), started: started},
					Request:    request,
				}, nil
			})},
		}).(*runtime)
		ctx, cancel := context.WithCancel(context.Background())
		implementation.evalContext = ctx
		done := make(chan error, 1)
		go func() {
			_, err := implementation.executeHTTPRequest("http://example.test", http.MethodGet, nil)
			done <- err
		}()
		<-started
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("executeHTTPRequest() error = %v, want context.Canceled", err)
			}
		case <-time.After(time.Second):
			t.Fatal("response read did not stop after cancellation")
		}
	})
}

func TestHTTPTransportErrorsAndBinaryTextBodies(t *testing.T) {
	t.Parallel()

	t.Run("transport error", func(t *testing.T) {
		offline := errors.New("offline transport")
		runtime := newRuntimeWithConfig(t, Config{
			Random: rand.New(rand.NewSource(37)),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, offline
			})},
		})
		_, err := runtime.Evaluate(context.Background(), EvalRequest{Script: `http.get("http://example.test")`})
		if err == nil || !strings.Contains(err.Error(), offline.Error()) {
			t.Fatalf("Evaluate() error = %v, want transport failure containing %q", err, offline)
		}
	})

	t.Run("binary-safe string body", func(t *testing.T) {
		runtime := newRuntimeWithConfig(t, Config{
			Random: rand.New(rand.NewSource(41)),
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("A\x00B")),
					Request:    request,
				}, nil
			})},
		})
		result, err := runtime.Evaluate(context.Background(), EvalRequest{
			Script: `var body = http.get("http://example.test").body; body.length + ":" + body.charCodeAt(1)`,
		})
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		if result.Value != "3:0" {
			t.Fatalf("binary body probe = %#v, want 3:0", result.Value)
		}
	})
}

type roundTripFunc func(request *http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type repeatingByteReader struct{}

func (repeatingByteReader) Read(contents []byte) (int, error) {
	for index := range contents {
		contents[index] = 'x'
	}
	return len(contents), nil
}

type contextReadCloser struct {
	ctx     context.Context
	started chan<- struct{}
}

func (r *contextReadCloser) Read([]byte) (int, error) {
	close(r.started)
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (*contextReadCloser) Close() error { return nil }

func newRuntimeWithConfig(t *testing.T, config Config) Runtime {
	t.Helper()
	factory, err := NewFactory(config)
	if err != nil {
		t.Fatalf("NewFactory() error = %v", err)
	}
	runtime, err := factory.NewRuntime()
	if err != nil {
		t.Fatalf("NewRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}
