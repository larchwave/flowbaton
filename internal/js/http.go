package js

import (
	"bytes"
	"context"
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
		contents, readErr := io.ReadAll(response.Body)
		if readErr != nil {
			return nil, fmt.Errorf("read HTTP response: %w", readErr)
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
		var buffer bytes.Buffer
		writer := multipart.NewWriter(&buffer)
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

func (r *runtime) writeMultipartPart(writer *multipart.Writer, name string, value any) error {
	fileSpec, isFile := value.(map[string]any)
	filePath, hasFilePath := fileSpec["filePath"].(string)
	if !isFile || !hasFilePath {
		return writer.WriteField(name, fmt.Sprint(value))
	}

	resolvedPath := resolveMultipartFile(filePath, r.scriptDir)
	file, err := os.Open(resolvedPath)
	if err != nil {
		return fmt.Errorf("open multipart file %q: %w", resolvedPath, err)
	}
	defer file.Close()

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, escapeMultipartQuotes(name), escapeMultipartQuotes(filepath.Base(resolvedPath))))
	if mediaType, ok := fileSpec["mediaType"].(string); ok && mediaType != "" {
		header.Set("Content-Type", mediaType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create multipart file part %q: %w", name, err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("write multipart file part %q: %w", name, err)
	}
	return nil
}

func resolveMultipartFile(filePath, scriptDir string) string {
	if filepath.IsAbs(filePath) {
		if _, err := os.Stat(filePath); err == nil {
			return filePath
		}
	}
	if scriptDir != "" {
		candidate := filepath.Join(scriptDir, filePath)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return filePath
}

func escapeMultipartQuotes(value string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, `\"`).Replace(value)
}
