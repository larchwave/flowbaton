package js

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// DefaultHTTPTimeout matches the product runtime's read, write, and call limit.
const DefaultHTTPTimeout = 5 * time.Minute

const (
	// MaxMultipartFileSize bounds each file buffered into a JavaScript-authored
	// multipart request.
	MaxMultipartFileSize int64 = 16 << 20
	// MaxMultipartParts bounds metadata work before the multipart body is built.
	MaxMultipartParts = 64
	// MaxMultipartRequestSize bounds the complete encoded multipart body,
	// including fields, file contents, headers, boundaries, and closing bytes.
	MaxMultipartRequestSize int64 = 20 << 20
	// MaxHTTPResponseSize bounds response data exposed to JavaScript.
	MaxHTTPResponseSize int64 = 16 << 20
)

func newHTTPClient(configured *http.Client) *http.Client {
	if configured != nil {
		client := *configured
		client.Timeout = DefaultHTTPTimeout
		return &client
	}

	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: DefaultHTTPTimeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

func installHTTPBinding(vm *goja.Runtime, binding *goja.Object, runtime *runtime) error {
	methods := map[string]string{
		"get":    http.MethodGet,
		"post":   http.MethodPost,
		"put":    http.MethodPut,
		"delete": http.MethodDelete,
	}
	for name, method := range methods {
		method := method
		if err := binding.Set(name, runtime.httpFunction(vm, method, false)); err != nil {
			return fmt.Errorf("install http.%s: %w", name, err)
		}
	}
	if err := binding.Set("request", runtime.httpFunction(vm, http.MethodGet, true)); err != nil {
		return fmt.Errorf("install http.request: %w", err)
	}
	return nil
}

func (r *runtime) httpFunction(vm *goja.Runtime, defaultMethod string, methodFromParams bool) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		params := exportHTTPParams(call.Argument(1))
		method := defaultMethod
		if methodFromParams {
			if requested, ok := params["method"].(string); ok {
				method = requested
			}
		}
		response, err := r.executeHTTPRequest(url, method, params)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(response)
	}
}

func exportHTTPParams(value goja.Value) map[string]any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	params, _ := value.Export().(map[string]any)
	return params
}

func (r *runtime) executeHTTPRequest(url, method string, params map[string]any) (map[string]any, error) {
	ctx := r.evalContext
	if ctx == nil {
		ctx = context.Background()
	}
	body, contentType, err := r.requestBody(params)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build HTTP request: %w", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if headers, ok := params["headers"].(map[string]any); ok {
		for key, value := range headers {
			request.Header.Add(key, fmt.Sprint(value))
		}
	}

	response, err := r.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("execute HTTP request: %w", err)
	}

	var responseBody any
	if response.Body != nil {
		defer response.Body.Close()
		contents, readErr := io.ReadAll(io.LimitReader(response.Body, MaxHTTPResponseSize+1))
		if readErr != nil {
			return nil, fmt.Errorf("read HTTP response: %w", readErr)
		}
		if int64(len(contents)) > MaxHTTPResponseSize {
			return nil, fmt.Errorf("read HTTP response: body exceeds %d bytes", MaxHTTPResponseSize)
		}
		responseBody = string(contents)
	}
	headers := make(map[string]string, len(response.Header))
	for name, values := range response.Header {
		headers[strings.ToLower(name)] = strings.Join(values, ",")
	}
	return map[string]any{
		"ok":      response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices,
		"status":  response.StatusCode,
		"body":    responseBody,
		"headers": headers,
	}, nil
}

func (r *runtime) requestBody(params map[string]any) (io.Reader, string, error) {
	if multipartForm, ok := params["multipartForm"].(map[string]any); ok {
		if len(multipartForm) > MaxMultipartParts {
			return nil, "", fmt.Errorf("multipart request has %d parts; maximum is %d", len(multipartForm), MaxMultipartParts)
		}
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&boundedMultipartWriter{
			destination: &buffer,
			maximum:     MaxMultipartRequestSize,
		})
		keys := make([]string, 0, len(multipartForm))
		for key := range multipartForm {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := r.writeMultipartPart(writer, key, multipartForm[key]); err != nil {
				return nil, "", err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, "", fmt.Errorf("close multipart request: %w", err)
		}
		return &buffer, writer.FormDataContentType(), nil
	}
	if body, ok := params["body"].(string); ok {
		return strings.NewReader(body), "", nil
	}
	return nil, "", nil
}

type boundedMultipartWriter struct {
	destination *bytes.Buffer
	maximum     int64
}

func (w *boundedMultipartWriter) Write(contents []byte) (int, error) {
	remaining := w.maximum - int64(w.destination.Len())
	if remaining <= 0 {
		return 0, fmt.Errorf("multipart request exceeds %d bytes", w.maximum)
	}
	if int64(len(contents)) <= remaining {
		return w.destination.Write(contents)
	}
	written, err := w.destination.Write(contents[:int(remaining)])
	if err != nil {
		return written, err
	}
	return written, fmt.Errorf("multipart request exceeds %d bytes", w.maximum)
}

func (r *runtime) writeMultipartPart(writer *multipart.Writer, name string, value any) error {
	fileSpec, isFile := value.(map[string]any)
	filePath, hasFilePath := fileSpec["filePath"].(string)
	if !isFile || !hasFilePath {
		return writer.WriteField(name, fmt.Sprint(value))
	}

	resolvedPath, err := resolveMultipartFile(filePath, r.scriptDir)
	if err != nil {
		return err
	}
	file, err := os.Open(resolvedPath)
	if err != nil {
		return fmt.Errorf("open multipart file %q: %w", resolvedPath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat multipart file %q: %w", resolvedPath, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("multipart file %q is not a regular file", filePath)
	}
	if info.Size() > MaxMultipartFileSize {
		return fmt.Errorf("multipart file %q size %d exceeds %d bytes", filePath, info.Size(), MaxMultipartFileSize)
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeMultipartQuotes(name), escapeMultipartQuotes(filepath.Base(resolvedPath))))
	if mediaType, ok := fileSpec["mediaType"].(string); ok && mediaType != "" {
		header.Set("Content-Type", mediaType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create multipart file part %q: %w", name, err)
	}
	written, err := io.Copy(part, io.LimitReader(file, MaxMultipartFileSize+1))
	if err != nil {
		return fmt.Errorf("write multipart file part %q: %w", name, err)
	}
	if written > MaxMultipartFileSize {
		return fmt.Errorf("multipart file %q exceeds %d bytes", filePath, MaxMultipartFileSize)
	}
	return nil
}

func resolveMultipartFile(filePath, scriptDir string) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", errors.New("multipart file path is required")
	}
	if scriptDir == "" {
		return "", errors.New("multipart file requires a script directory")
	}
	if filepath.IsAbs(filePath) || filepath.VolumeName(filePath) != "" {
		return "", fmt.Errorf("multipart file path %q must be relative to the script directory", filePath)
	}
	cleanPath := filepath.Clean(filePath)
	if cleanPath == "." || cleanPath == ".." || strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("multipart file path %q escapes the script directory", filePath)
	}

	root, err := filepath.Abs(scriptDir)
	if err != nil {
		return "", fmt.Errorf("resolve multipart script directory %q: %w", scriptDir, err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve multipart script directory %q: %w", scriptDir, err)
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(root, cleanPath))
	if err != nil {
		return "", fmt.Errorf("resolve multipart file %q: %w", filePath, err)
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return "", fmt.Errorf("check multipart file %q: %w", filePath, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("multipart file path %q escapes the script directory", filePath)
	}
	return candidate, nil
}

func escapeMultipartQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}
