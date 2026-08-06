package engine

import (
	"context"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

// ExecuteBatch2ARootForReportTest exposes one gesture root lifecycle
// only to the external engine test package.
func ExecuteBatch2ARootForReportTest(t testing.TB) (FlowResult, []Event, model.Config, device.PressKeyRequest) {
	t.Helper()

	registry, err := newHandlerRegistry(backHandlerSpec(), hideKeyboardHandlerSpec(), scrollHandlerSpec(), pressKeyHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	command := batch2APressKeyCommand("volume down")
	compiled, err := newDispatcher(registry).compile(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch2a.yaml"},
		command,
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}

	driver := batch2ADriver("ios")
	clock := &batch1ATraceClock{now: time.Unix(1800, 0).UTC()}
	events := make([]Event, 0, 4)
	dependencies := Dependencies{
		ExecutionID: "batch2a-root", Driver: driver, Clock: clock,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{Name: "Batch 2A", AppID: "com.example.batch2a.report"}
	root := &compiledFlow{path: "/workspace/batch2a.yaml", config: config, body: []compiledDispatch{compiled}}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "batch2a-root/root-run-000001")
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	requests := batch2APressKeyRequests(driver.Actions())
	if len(requests) != 1 {
		t.Fatalf("PressKey requests = %#v, want one", requests)
	}
	return result, append([]Event(nil), events...), config, requests[0]
}
