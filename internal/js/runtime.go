package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"

	"github.com/dop251/goja"
)

var (
	ErrRandomSourceRequired = errors.New("javascript random source is required")
	ErrEnvScopeUnderflow    = errors.New("javascript environment scope stack is empty")
	ErrRuntimeClosed        = errors.New("javascript runtime is closed")
)

// RandomSource supplies deterministic data to the JavaScript faker binding.
type RandomSource interface {
	Intn(n int) int
	Read(p []byte) (int, error)
}

// Config contains host services copied into each runtime created by a Factory.
type Config struct {
	Random   RandomSource
	Platform string
	// CopiedText is nil until something copies. `flowbaton.copiedText` is then
	// undefined, preserving the distinction between "nothing has been copied"
	// and "an empty string was copied".
	CopiedText *string
	LogSink    func(message string)
	HTTPClient *http.Client
}

// Factory creates one isolated shared-context runtime per root flow.
type Factory interface {
	NewRuntime() (Runtime, error)
}

// Runtime serializes access to one reusable strict JavaScript context.
type Runtime interface {
	Evaluate(ctx context.Context, request EvalRequest) (Result, error)
	Interpolate(ctx context.Context, input string, env map[string]any) (string, error)
	PushEnv() error
	PopEnv() error
	PutEnv(name string, value any) error
	SetCopiedText(value string) error
	SetPlatform(value string) error
	SetLogSink(sink func(message string))
	PushLogSink(sink func(message string)) (restore func())
	Close() error
}

// EvalRequest describes one IIFE-scoped evaluation and its environment overlay.
type EvalRequest struct {
	Script        string
	SourceName    string
	ScriptDir     string
	Env           map[string]any
	RunInSubScope bool
}

// Result contains a Go-native exported value and its JavaScript string form.
type Result struct {
	Value any
	Text  string
}

// OpaqueValue represents a JavaScript value that cannot cross the Go-native
// runtime contract without exposing an engine-specific type.
type OpaqueValue struct {
	Kind string
}

// MissingEnvValueError identifies an environment key whose value was null.
type MissingEnvValueError struct {
	Name string
}

func (e *MissingEnvValueError) Error() string {
	return fmt.Sprintf("environment variable %q is missing a value", e.Name)
}

// EvaluationError reports script failures without exposing goja error types.
type EvaluationError struct {
	SourceName string
	Message    string
}

func (e *EvaluationError) Error() string {
	return fmt.Sprintf("javascript evaluation failed in %s: %s", e.SourceName, e.Message)
}

type factory struct {
	config Config
	rngMu  sync.Mutex
}

// NewFactory validates host services and returns a reusable runtime factory.
func NewFactory(config Config) (Factory, error) {
	if config.Random == nil {
		return nil, ErrRandomSourceRequired
	}

	return &factory{config: config}, nil
}

type runtime struct {
	mu             sync.Mutex
	vm             *goja.Runtime
	global         *goja.Object
	flowbaton      *goja.Object
	httpClient     *http.Client
	evalContext    context.Context
	scriptDir      string
	logSink        func(message string)
	scopedLogSinks []scopedLogSink
	nextLogSinkID  uint64
	transientNames map[string]struct{}
	shadowedValues map[string]goja.Value
	env            map[string]any
	envStack       []map[string]any
	closed         bool
}

type scopedLogSink struct {
	id   uint64
	sink func(message string)
}

func (f *factory) NewRuntime() (Runtime, error) {
	vm := goja.New()
	global := vm.GlobalObject()
	proxy := vm.NewProxy(global, &goja.ProxyTrapConfig{
		Has: func(_ *goja.Object, _ string) bool {
			return true
		},
		HasIdx: func(_ *goja.Object, _ int) bool {
			return true
		},
		HasSym: func(_ *goja.Object, _ *goja.Symbol) bool {
			return true
		},
		Get: func(target *goja.Object, property string, _ goja.Value) goja.Value {
			if value := target.Get(property); value != nil {
				return value
			}
			return goja.Undefined()
		},
	})
	vm.SetGlobalObject(vm.ToValue(proxy).(*goja.Object))
	if err := global.Set("globalThis", vm.GlobalObject()); err != nil {
		return nil, fmt.Errorf("install global proxy: %w", err)
	}

	output := vm.NewObject()
	faker := vm.NewObject()
	httpBinding := vm.NewObject()
	flowbaton := vm.NewObject()
	console := vm.NewObject()
	runtime := &runtime{
		vm:             vm,
		global:         global,
		flowbaton:      flowbaton,
		httpClient:     newHTTPClient(f.config.HTTPClient),
		logSink:        f.config.LogSink,
		transientNames: make(map[string]struct{}),
		shadowedValues: make(map[string]goja.Value),
		env:            make(map[string]any),
	}
	if f.config.CopiedText != nil {
		if err := flowbaton.Set("copiedText", *f.config.CopiedText); err != nil {
			return nil, fmt.Errorf("install flowbaton copiedText: %w", err)
		}
	}
	if err := flowbaton.Set("platform", f.config.Platform); err != nil {
		return nil, fmt.Errorf("install flowbaton platform: %w", err)
	}
	consoleWrite := func(call goja.FunctionCall) goja.Value {
		parts := make([]string, len(call.Arguments))
		for index, argument := range call.Arguments {
			parts[index] = argument.String()
		}
		message := strings.Join(parts, " ")
		if runtime.logSink != nil {
			runtime.logSink(message)
		}
		if count := len(runtime.scopedLogSinks); count > 0 {
			if sink := runtime.scopedLogSinks[count-1].sink; sink != nil {
				sink(message)
			}
		}
		return goja.Undefined()
	}
	for _, method := range []string{"log", "info", "warn", "error"} {
		if err := console.Set(method, consoleWrite); err != nil {
			return nil, fmt.Errorf("install console.%s: %w", method, err)
		}
	}
	if err := faker.Set("randomInt", f.randomInt); err != nil {
		return nil, fmt.Errorf("install faker.randomInt: %w", err)
	}
	if err := faker.Set("uuid", f.randomUUID); err != nil {
		return nil, fmt.Errorf("install faker.uuid: %w", err)
	}
	if err := installHTTPBinding(vm, httpBinding, runtime); err != nil {
		return nil, err
	}
	jsonObject := global.Get("JSON")
	if jsonObject == nil {
		return nil, errors.New("install json helper: JSON global is unavailable")
	}
	jsonParse, ok := goja.AssertFunction(jsonObject.ToObject(vm).Get("parse"))
	if !ok {
		return nil, errors.New("install json helper: JSON.parse is not callable")
	}
	bindings := map[string]any{
		"http":      httpBinding,
		"faker":     faker,
		"output":    output,
		"flowbaton": flowbaton,
		"json": func(call goja.FunctionCall) goja.Value {
			value, err := jsonParse(goja.Undefined(), call.Argument(0))
			if err != nil {
				panic(err)
			}
			return value
		},
		"relativePoint": func(x, y float64) string {
			return fmt.Sprintf("%.0f%%,%.0f%%", math.Ceil(x*100), math.Ceil(y*100))
		},
		"console": console,
	}
	for name, value := range bindings {
		if err := global.Set(name, value); err != nil {
			return nil, fmt.Errorf("install permanent binding %q: %w", name, err)
		}
	}

	return runtime, nil
}

func (r *runtime) Evaluate(ctx context.Context, request EvalRequest) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Result{}, ErrRuntimeClosed
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	for name, value := range request.Env {
		if value == nil {
			return Result{}, &MissingEnvValueError{Name: name}
		}
	}
	if request.RunInSubScope {
		previousEnv := cloneEnv(r.env)
		defer func() {
			r.env = previousEnv
		}()
	}
	for name, value := range request.Env {
		if !isPermanentBinding(name) {
			r.env[name] = value
		}
	}
	effectiveEnv := cloneEnv(r.env)
	for name := range r.transientNames {
		if previous := r.shadowedValues[name]; previous != nil {
			if err := r.global.Set(name, previous); err != nil {
				return Result{}, fmt.Errorf("restore shadowed global %q: %w", name, err)
			}
		} else if err := r.global.Delete(name); err != nil {
			return Result{}, fmt.Errorf("clear environment variable %q: %w", name, err)
		}
		delete(r.transientNames, name)
		delete(r.shadowedValues, name)
	}
	for name, value := range effectiveEnv {
		if isPermanentBinding(name) {
			continue
		}
		r.shadowedValues[name] = r.global.Get(name)
		if err := r.global.Set(name, value); err != nil {
			return Result{}, fmt.Errorf("set environment variable %q: %w", name, err)
		}
		r.transientNames[name] = struct{}{}
	}

	script, err := json.Marshal(request.Script)
	if err != nil {
		return Result{}, err
	}
	sourceName := request.SourceName
	if sourceName == "" {
		sourceName = "inline-script"
	}
	r.evalContext = ctx
	r.scriptDir = request.ScriptDir
	defer func() {
		r.evalContext = nil
		r.scriptDir = ""
	}()
	interruptDone := make(chan struct{})
	stopInterrupt := context.AfterFunc(ctx, func() {
		r.vm.Interrupt(ctx.Err())
		close(interruptDone)
	})
	defer func() {
		if !stopInterrupt() {
			<-interruptDone
		}
		r.vm.ClearInterrupt()
	}()
	value, err := r.vm.RunScript(sourceName, `(function() { "use strict"; return eval(`+string(script)+`); }).call(undefined)`)
	if err != nil {
		if contextError := ctx.Err(); contextError != nil && errors.Is(err, contextError) {
			return Result{}, contextError
		}
		return Result{}, &EvaluationError{SourceName: sourceName, Message: err.Error()}
	}
	return Result{Value: sanitizeExportedValue(value.Export()), Text: value.String()}, nil
}

func (r *runtime) Interpolate(ctx context.Context, input string, env map[string]any) (string, error) {
	var output strings.Builder
	output.Grow(len(input))

	for index := 0; index < len(input); {
		token := interpolationTokenAt(input, index)
		switch token.kind {
		case interpolationTokenEscaped:
			output.WriteString("${")
		case interpolationTokenExpression:
			if strings.TrimSpace(token.expression) != "" {
				result, err := r.Evaluate(ctx, EvalRequest{
					Script: token.expression,
					Env:    env,
				})
				if err != nil {
					return "", err
				}
				output.WriteString(result.Text)
			}
		default:
			output.WriteByte(input[index])
		}
		index = token.next
	}

	return output.String(), nil
}

// HasInterpolationExpression reports whether Interpolate will recognize at
// least one complete expression token under its single-pass dollar grammar.
func HasInterpolationExpression(input string) bool {
	for index := 0; index < len(input); {
		token := interpolationTokenAt(input, index)
		if token.kind == interpolationTokenExpression {
			return true
		}
		index = token.next
	}
	return false
}

type interpolationTokenKind uint8

const (
	interpolationTokenLiteral interpolationTokenKind = iota
	interpolationTokenEscaped
	interpolationTokenExpression
)

type interpolationToken struct {
	kind       interpolationTokenKind
	next       int
	expression string
}

func interpolationTokenAt(input string, index int) interpolationToken {
	if index+2 < len(input) && input[index] == '\\' && input[index+1] == '$' && input[index+2] == '{' {
		return interpolationToken{kind: interpolationTokenEscaped, next: index + 3}
	}
	if index+1 < len(input) && input[index] == '$' && input[index+1] == '{' {
		// The expression runs to the last `}` before the next `$`, not the first.
		// This accepts nested object expressions while preserving the established
		// interpolation boundary for subsequent `$` characters.
		region := index + 2
		for region < len(input) && input[region] != '$' {
			region++
		}
		if offset := strings.LastIndexByte(input[index+2:region], '}'); offset >= 0 {
			end := index + 2 + offset
			return interpolationToken{
				kind:       interpolationTokenExpression,
				next:       end + 1,
				expression: input[index+2 : end],
			}
		}
	}
	return interpolationToken{kind: interpolationTokenLiteral, next: index + 1}
}

func (r *runtime) PushEnv() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.envStack = append(r.envStack, cloneEnv(r.env))
	return nil
}

func (r *runtime) PopEnv() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.envStack) == 0 {
		return ErrEnvScopeUnderflow
	}
	last := len(r.envStack) - 1
	r.env = r.envStack[last]
	r.envStack = r.envStack[:last]
	return nil
}

func (r *runtime) PutEnv(name string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if value == nil {
		return &MissingEnvValueError{Name: name}
	}
	r.env[name] = value
	return nil
}

func (r *runtime) SetCopiedText(value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.flowbaton.Set("copiedText", value)
}

func (r *runtime) SetPlatform(value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.flowbaton.Set("platform", value)
}

func (r *runtime) SetLogSink(sink func(message string)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.logSink = sink
}

func (r *runtime) PushLogSink(sink func(message string)) (restore func()) {
	r.mu.Lock()
	r.nextLogSinkID++
	id := r.nextLogSinkID
	r.scopedLogSinks = append(r.scopedLogSinks, scopedLogSink{id: id, sink: sink})
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			for index := range r.scopedLogSinks {
				if r.scopedLogSinks[index].id != id {
					continue
				}
				copy(r.scopedLogSinks[index:], r.scopedLogSinks[index+1:])
				last := len(r.scopedLogSinks) - 1
				r.scopedLogSinks[last] = scopedLogSink{}
				r.scopedLogSinks = r.scopedLogSinks[:last]
				return
			}
		})
	}
}

func (r *runtime) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.closed = true
	r.httpClient.CloseIdleConnections()
	return nil
}

func (f *factory) randomInt(minimum, maximum int) (int, error) {
	if maximum < minimum {
		return 0, fmt.Errorf("faker.randomInt maximum %d is less than minimum %d", maximum, minimum)
	}
	if maximum == minimum {
		return minimum, nil
	}

	f.rngMu.Lock()
	defer f.rngMu.Unlock()
	return minimum + f.config.Random.Intn(maximum-minimum+1), nil
}

func (f *factory) randomUUID() (string, error) {
	bytes := make([]byte, 16)
	f.rngMu.Lock()
	_, err := io.ReadFull(f.config.Random, bytes)
	f.rngMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("faker.uuid random source: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		bytes[0:4],
		bytes[4:6],
		bytes[6:8],
		bytes[8:10],
		bytes[10:16],
	), nil
}

func cloneEnv(env map[string]any) map[string]any {
	copy := make(map[string]any, len(env))
	for name, value := range env {
		copy[name] = value
	}
	return copy
}

func isPermanentBinding(name string) bool {
	switch name {
	case "http", "faker", "output", "flowbaton", "json", "relativePoint", "console":
		return true
	default:
		return false
	}
}

func sanitizeExportedValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copy := make(map[string]any, len(typed))
		for key, nested := range typed {
			copy[key] = sanitizeExportedValue(nested)
		}
		return copy
	case []any:
		copy := make([]any, len(typed))
		for index, nested := range typed {
			copy[index] = sanitizeExportedValue(nested)
		}
		return copy
	}
	if value == nil {
		return nil
	}
	typeName := fmt.Sprintf("%T", value)
	if strings.Contains(typeName, "goja.") {
		kind := "object"
		if strings.Contains(typeName, "Promise") {
			kind = "promise"
		} else if strings.HasPrefix(typeName, "func(") {
			kind = "function"
		}
		return OpaqueValue{Kind: kind}
	}
	return value
}
