package js

import (
	"context"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func TestScopedConsoleSinkDeliversOnlyToInnermostScope(t *testing.T) {
	t.Parallel()

	var host []string
	runtime := newLogScopeRuntime(t, func(message string) { host = append(host, message) })
	var parent []string
	var child []string
	restoreParent := runtime.PushLogSink(func(message string) { parent = append(parent, message) })
	logMessage(t, runtime, "parent-before")
	restoreChild := runtime.PushLogSink(func(message string) { child = append(child, message) })
	logMessage(t, runtime, "child")
	restoreChild()
	logMessage(t, runtime, "parent-after")
	restoreParent()

	if want := []string{"parent-before", "child", "parent-after"}; !reflect.DeepEqual(host, want) {
		t.Fatalf("host messages = %#v, want %#v", host, want)
	}
	if want := []string{"parent-before", "parent-after"}; !reflect.DeepEqual(parent, want) {
		t.Fatalf("parent messages = %#v, want %#v", parent, want)
	}
	if want := []string{"child"}; !reflect.DeepEqual(child, want) {
		t.Fatalf("child messages = %#v, want %#v", child, want)
	}
}

func TestScopedConsoleSinkRestoreIsSafeOutOfOrderIdempotentAndNilAware(t *testing.T) {
	t.Parallel()

	var host []string
	runtime := newLogScopeRuntime(t, func(message string) { host = append(host, message) })
	var outer []string
	var inner []string
	restoreOuter := runtime.PushLogSink(func(message string) { outer = append(outer, message) })
	restoreInner := runtime.PushLogSink(func(message string) { inner = append(inner, message) })
	restoreNil := runtime.PushLogSink(nil)

	logMessage(t, runtime, "nil-top")
	restoreOuter()
	restoreOuter()
	logMessage(t, runtime, "nil-top-after-outer-restore")
	restoreNil()
	restoreNil()
	logMessage(t, runtime, "inner-after-nil-restore")
	restoreInner()
	restoreInner()
	logMessage(t, runtime, "host-only")

	if want := []string{"nil-top", "nil-top-after-outer-restore", "inner-after-nil-restore", "host-only"}; !reflect.DeepEqual(host, want) {
		t.Fatalf("host messages = %#v, want %#v", host, want)
	}
	if len(outer) != 0 {
		t.Fatalf("outer messages = %#v, want none", outer)
	}
	if want := []string{"inner-after-nil-restore"}; !reflect.DeepEqual(inner, want) {
		t.Fatalf("inner messages = %#v, want %#v", inner, want)
	}
}

func TestScopedConsoleSinkIsRuntimeLocalAndRaceSafe(t *testing.T) {
	t.Parallel()

	first := newLogScopeRuntime(t, nil)
	second := newLogScopeRuntime(t, nil)
	var firstMu sync.Mutex
	var firstMessages []string
	var secondMu sync.Mutex
	var secondMessages []string
	restoreFirst := first.PushLogSink(func(message string) {
		firstMu.Lock()
		defer firstMu.Unlock()
		firstMessages = append(firstMessages, message)
	})
	restoreSecond := second.PushLogSink(func(message string) {
		secondMu.Lock()
		defer secondMu.Unlock()
		secondMessages = append(secondMessages, message)
	})
	defer restoreFirst()
	defer restoreSecond()

	var wait sync.WaitGroup
	for index := 0; index < 8; index++ {
		wait.Add(2)
		go func() {
			defer wait.Done()
			logMessage(t, first, "first")
		}()
		go func() {
			defer wait.Done()
			logMessage(t, second, "second")
		}()
	}
	wait.Wait()

	firstMu.Lock()
	defer firstMu.Unlock()
	secondMu.Lock()
	defer secondMu.Unlock()
	if len(firstMessages) != 8 || len(secondMessages) != 8 {
		t.Fatalf("runtime-local message counts = (%d, %d), want (8, 8)", len(firstMessages), len(secondMessages))
	}
	for _, message := range firstMessages {
		if message != "first" {
			t.Fatalf("first runtime received %q", message)
		}
	}
	for _, message := range secondMessages {
		if message != "second" {
			t.Fatalf("second runtime received %q", message)
		}
	}
}

func newLogScopeRuntime(t *testing.T, host func(string)) Runtime {
	t.Helper()
	factory, err := NewFactory(Config{Random: rand.New(rand.NewSource(53)), LogSink: host})
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

func logMessage(t *testing.T, runtime Runtime, message string) {
	t.Helper()
	if _, err := runtime.Evaluate(context.Background(), EvalRequest{
		Script: `console.log("` + message + `")`,
	}); err != nil {
		t.Errorf("Evaluate(console.log(%q)) error = %v", message, err)
	}
}
