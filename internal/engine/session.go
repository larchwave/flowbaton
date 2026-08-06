package engine

import (
	"context"
	"sort"
	"sync"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/js"
	"github.com/nohavewho/flowbaton/internal/model"
)

// executionSession owns all mutable host state for one selected root. Flow
// scopes and execution are layered onto this owner without transferring the
// runtime or lookup to individual handlers.
type executionSession struct {
	mu        sync.Mutex
	closeMu   sync.Mutex
	closeErr  error
	poisonErr *sessionIntegrityError
	closed    bool
	running   bool
	ran       bool

	root       *compiledFlow
	core       *executorCore
	runtime    js.Runtime
	lookup     *ElementLookup
	frames     []flowFrame
	copiedText string
}

type flowFrame struct {
	path   string
	config model.Config
}

type flowScope struct {
	mu      sync.Mutex
	session *executionSession
	depth   int
	closed  bool
	err     error
	// previousAppID is the active app of the flow that ran before this one, put
	// back on close. A nested runFlow with its own appId must not leave the
	// parent looking at the child's screen.
	previousAppID string
	restoresApp   bool
}

const sessionIntegrityErrorMessage = "execution session integrity lost after copied text update failed"

// sessionIntegrityError is an engine-owned terminal marker for a root whose
// JavaScript clipboard setter may have mutated state before failing. It
// deliberately exposes no causal edge to the Runtime diagnostic.
type sessionIntegrityError struct {
	configuration *ConfigurationError
}

func newSessionIntegrityError() *sessionIntegrityError {
	return &sessionIntegrityError{
		configuration: NewConfigurationError(sessionIntegrityErrorMessage, nil),
	}
}

func (*sessionIntegrityError) Error() string {
	return sessionIntegrityErrorMessage
}

func (failure *sessionIntegrityError) As(target any) bool {
	configuration, ok := target.(**ConfigurationError)
	if !ok || configuration == nil || failure == nil || failure.configuration == nil {
		return false
	}
	*configuration = failure.configuration
	return true
}

func isSessionIntegrityError(err error) bool {
	failure, ok := err.(*sessionIntegrityError)
	return ok && failure != nil
}

// newExecutionSession constructs one root-scoped runtime and lookup only after
// the complete Program has been compiled into the supplied root template.
func newExecutionSession(ctx context.Context, dependencies Dependencies, root *compiledFlow) (*executionSession, error) {
	return newExecutionSessionForRootRun(ctx, dependencies, root, "")
}

func newExecutionSessionForRootRun(
	ctx context.Context,
	dependencies Dependencies,
	root *compiledFlow,
	rootRunID string,
) (result *executionSession, err error) {
	defer func() {
		err = sanitizeMalformedError("execution session setup failed", err)
	}()
	if ctx == nil {
		return nil, NewConfigurationError("execution session context must not be nil", nil)
	}
	if root == nil {
		return nil, NewConfigurationError("compiled root flow must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	core, err := newExecutorCoreForRootRun(dependencies, rootRunID)
	if err != nil {
		return nil, err
	}
	runtime, err := dependencies.JSFactory.NewRuntime()
	if err != nil {
		if !isNilValue(runtime) {
			_ = runtime.Close()
		}
		return nil, err
	}
	if isNilValue(runtime) {
		return nil, NewConfigurationError("JavaScript factory returned a nil runtime", nil)
	}
	fail := func(primary error) (*executionSession, error) {
		_ = runtime.Close()
		return nil, primary
	}

	lookup := NewElementLookup(dependencies.Driver, dependencies.Clock)
	info, err := lookup.cachedDeviceInfo(ctx)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if err := runtime.SetPlatform(string(info.Platform)); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	// `flowbaton.copiedText` remains undefined until a command copies text, which
	// distinguishes an unset value from a copied empty string.

	session := &executionSession{
		root: root, core: core, runtime: runtime, lookup: lookup,
	}
	core.state.runtimeFn = session.jsRuntime
	core.state.lookupFn = session.elementLookup
	core.state.currentConfigFn = session.currentConfig
	core.state.copiedTextFn = session.copiedTextValue
	core.state.setCopiedTextFn = session.setCopiedText
	core.state.putEnvFn = session.putEnv
	core.state.executeCompiledFlow = session.executeCompiledFlow
	return session, nil
}

// Close releases only session-owned state. Driver lifetime remains owned by
// the caller that acquired it.
func (session *executionSession) Close() error {
	if session == nil {
		return NewConfigurationError("execution session must not be nil", nil)
	}
	session.closeMu.Lock()
	defer session.closeMu.Unlock()
	session.mu.Lock()
	if session.poisonErr != nil {
		primary := session.poisonErr
		session.mu.Unlock()
		return primary
	}
	if session.closed {
		session.mu.Unlock()
		return session.closeErr
	}
	if len(session.frames) != 0 {
		session.mu.Unlock()
		return NewConfigurationError("execution session has active flow scopes", nil)
	}
	session.closed = true
	runtime := session.runtime
	session.mu.Unlock()
	if isNilValue(runtime) {
		session.closeErr = NewConfigurationError("execution session runtime must not be nil", nil)
		return session.closeErr
	}
	session.closeErr = sanitizeMalformedError("execution session close failed", runtime.Close())
	return session.closeErr
}

func (session *executionSession) jsRuntime() (js.Runtime, error) {
	if session == nil {
		return nil, NewConfigurationError("execution session must not be nil", nil)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.poisonErr != nil {
		return nil, session.poisonErr
	}
	if session.closed {
		return nil, NewConfigurationError("execution session is closed", nil)
	}
	if isNilValue(session.runtime) {
		return nil, NewConfigurationError("execution session runtime must not be nil", nil)
	}
	return session.runtime, nil
}

func (session *executionSession) elementLookup() (*ElementLookup, error) {
	if session == nil {
		return nil, NewConfigurationError("execution session must not be nil", nil)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.poisonErr != nil {
		return nil, session.poisonErr
	}
	if session.closed {
		return nil, NewConfigurationError("execution session is closed", nil)
	}
	if session.lookup == nil {
		return nil, NewConfigurationError("execution session element lookup must not be nil", nil)
	}
	return session.lookup, nil
}

func (session *executionSession) enterFlow(
	ctx context.Context,
	flow *compiledFlow,
	overlay map[string]string,
) (*flowScope, error) {
	if ctx == nil {
		return nil, NewConfigurationError("flow scope context must not be nil", nil)
	}
	if flow == nil {
		return nil, NewConfigurationError("compiled flow must not be nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session.closeMu.Lock()
	defer session.closeMu.Unlock()
	runtime, err := session.jsRuntime()
	if err != nil {
		return nil, err
	}
	if err := runtime.PushEnv(); err != nil {
		return nil, err
	}
	rollback := func(primary error) (*flowScope, error) {
		_ = runtime.PopEnv()
		return nil, primary
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	// Apply injected values first and flow declarations last. This gives the
	// flow's `env:` block precedence over operator and parent runFlow values, as
	// specified by specs/03-cli-tooling.md:30 and env_precedence_test.go.
	if err := putSortedEnvironment(ctx, runtime, overlay); err != nil {
		return rollback(err)
	}
	if err := putSortedEnvironment(ctx, runtime, flow.config.Env); err != nil {
		return rollback(err)
	}
	// Reserved names go last, so neither the flow nor the operator can shadow
	// them. A flow that could overwrite FLOWBATON_SHARD_INDEX could lie to itself
	// about where it is running.
	if err := putSortedEnvironment(ctx, runtime, reservedEnvironmentFor(
		session.core.state.dependencies.ReservedEnvironment, flow.path)); err != nil {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}

	// Before any frame is pushed, so a driver that refuses unwinds through the
	// same rollback as a failed environment write.
	if err := session.applyWebViewHierarchy(ctx, flow.config); err != nil {
		return rollback(err)
	}

	session.mu.Lock()
	if session.poisonErr != nil {
		primary := session.poisonErr
		session.mu.Unlock()
		return rollback(primary)
	}
	if session.closed {
		session.mu.Unlock()
		return rollback(NewConfigurationError("execution session is closed", nil))
	}
	session.frames = append(session.frames, flowFrame{
		path: flow.path, config: cloneConfig(flow.config),
	})
	depth := len(session.frames)
	session.mu.Unlock()

	// The flow's declared app becomes what hierarchy lookups are about, scoped
	// the same way the environment is: set on entry, put back on close. A driver
	// may need it to answer at all — XCUITest cannot snapshot "the frontmost
	// app" without a bundle id — so a flow that declares one and does not pass
	// it gets the home screen behind its own app.
	scope := &flowScope{session: session, depth: depth}
	if lookup, lookupErr := session.elementLookup(); lookupErr == nil && lookup != nil {
		scope.previousAppID = lookup.SetActiveApp(flow.config.EffectiveAppID())
		scope.restoresApp = true
	}
	return scope, nil
}

// webViewHierarchyKey and webViewHierarchyDevTools name the flow-config option
// that selects where an Android WebView's contents come from.
const (
	webViewHierarchyKey      = "androidWebViewHierarchy"
	webViewHierarchyDevTools = "devtools"
)

// applyWebViewHierarchy is specs/01-core-engine.md:62 — applying a flow's
// configuration inits the Android Chrome DevTools hierarchy when the flow asked
// for it. Without this the key is parsed, classified by the capability
// registry, and then has no effect: the flow gets the opaque WebView it asked
// not to have, with no error to say so.
//
// Only "devtools" turns it on. "accessibility" is the other documented value
// and is what the driver already does, so acting on the key's mere presence
// would enable the merge for flows that explicitly asked for the plain dump.
func (session *executionSession) applyWebViewHierarchy(
	ctx context.Context, config model.Config,
) error {
	mode, _ := config.Ext[webViewHierarchyKey].(string)
	if mode != webViewHierarchyDevTools {
		return nil
	}
	driver := session.core.state.dependencies.Driver
	if driver == nil {
		return NewConfigurationError("the devtools hierarchy needs a driver", nil)
	}
	return driver.SetAndroidChromeDevToolsEnabled(
		ctx, device.ChromeDevToolsRequest{Enabled: true})
}

func putSortedEnvironment(ctx context.Context, runtime js.Runtime, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := runtime.PutEnv(key, values[key]); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func (scope *flowScope) Close() error {
	if scope == nil || scope.session == nil {
		return NewConfigurationError("flow scope must not be nil", nil)
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.closed {
		return scope.err
	}

	session := scope.session
	session.closeMu.Lock()
	defer session.closeMu.Unlock()
	session.mu.Lock()
	if len(session.frames) != scope.depth {
		session.mu.Unlock()
		return NewConfigurationError("flow scopes must close in LIFO order", nil)
	}
	session.frames = session.frames[:len(session.frames)-1]
	runtime := session.runtime
	primary := session.poisonErr
	session.mu.Unlock()

	// Restore the parent app before returning from the child scope.
	if scope.restoresApp {
		if lookup, lookupErr := session.elementLookup(); lookupErr == nil && lookup != nil {
			lookup.SetActiveApp(scope.previousAppID)
		}
	}

	scope.closed = true
	if primary != nil {
		scope.err = primary
		return scope.err
	}
	if isNilValue(runtime) {
		scope.err = NewConfigurationError("execution session runtime must not be nil", nil)
		return scope.err
	}
	scope.err = sanitizeMalformedError("flow scope close failed", runtime.PopEnv())
	return scope.err
}

func (session *executionSession) currentConfig() (model.Config, error) {
	if session == nil {
		return model.Config{}, NewConfigurationError("execution session must not be nil", nil)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.poisonErr != nil {
		return model.Config{}, session.poisonErr
	}
	if session.closed {
		return model.Config{}, NewConfigurationError("execution session is closed", nil)
	}
	if len(session.frames) == 0 {
		return model.Config{}, NewConfigurationError("execution session has no active flow", nil)
	}
	return cloneConfig(session.frames[len(session.frames)-1].config), nil
}

func (session *executionSession) currentAppID() (string, error) {
	config, err := session.currentConfig()
	if err != nil {
		return "", err
	}
	return config.EffectiveAppID(), nil
}

func (session *executionSession) copiedTextValue() (string, error) {
	if session == nil {
		return "", NewConfigurationError("execution session must not be nil", nil)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.poisonErr != nil {
		return "", session.poisonErr
	}
	if session.closed {
		return "", NewConfigurationError("execution session is closed", nil)
	}
	return session.copiedText, nil
}

func (session *executionSession) setCopiedText(value string) error {
	if session == nil {
		return NewConfigurationError("execution session must not be nil", nil)
	}
	session.closeMu.Lock()
	defer session.closeMu.Unlock()
	session.mu.Lock()
	if session.poisonErr != nil {
		primary := session.poisonErr
		session.mu.Unlock()
		return primary
	}
	if session.closed {
		session.mu.Unlock()
		return NewConfigurationError("execution session is closed", nil)
	}
	runtime := session.runtime
	session.mu.Unlock()
	if isNilValue(runtime) {
		return NewConfigurationError("execution session runtime must not be nil", nil)
	}
	if err := runtime.SetCopiedText(value); err != nil {
		primary := newSessionIntegrityError()
		session.mu.Lock()
		session.poisonErr = primary
		session.closed = true
		session.mu.Unlock()
		session.closeErr = primary
		closePoisonedRuntime(runtime)
		return primary
	}
	session.mu.Lock()
	session.copiedText = value
	session.mu.Unlock()
	return nil
}

// putEnv writes into the current runtime environment scope
// (used by extractTextWithAI's outputVariable) and poisons the session if the
// runtime mutation fails, since the env may then be in an unknown state.
func (session *executionSession) putEnv(name, value string) error {
	if session == nil {
		return NewConfigurationError("execution session must not be nil", nil)
	}
	session.closeMu.Lock()
	defer session.closeMu.Unlock()
	session.mu.Lock()
	if session.poisonErr != nil {
		primary := session.poisonErr
		session.mu.Unlock()
		return primary
	}
	if session.closed {
		session.mu.Unlock()
		return NewConfigurationError("execution session is closed", nil)
	}
	runtime := session.runtime
	session.mu.Unlock()
	if isNilValue(runtime) {
		return NewConfigurationError("execution session runtime must not be nil", nil)
	}
	if err := runtime.PutEnv(name, value); err != nil {
		primary := newSessionIntegrityError()
		session.mu.Lock()
		session.poisonErr = primary
		session.closed = true
		session.mu.Unlock()
		session.closeErr = primary
		closePoisonedRuntime(runtime)
		return primary
	}
	return nil
}

func closePoisonedRuntime(runtime js.Runtime) {
	defer func() {
		_ = recover()
	}()
	_ = runtime.Close()
}
