package engine

import (
	"context"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/model"
)

// ExecuteBatch1BRootForReportTest exposes the private swipe root lifecycle
// only to the external engine test package.
func ExecuteBatch1BRootForReportTest(t testing.TB) (FlowResult, []Event, model.Config) {
	t.Helper()

	command := swipeCommand(map[string]any{
		"direction": "RIGHT", "duration": int64(60000),
		"waitToSettleTimeoutMs": int64(0),
	}, nil)
	registry, err := newHandlerRegistry(swipeHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := newDispatcher(registry).compile(context.Background(), compileContext{containingFlow: "/workspace/batch1b.yaml"}, command)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}

	driver := batch1APointDriver(400, 884)
	clock := &batch1ATraceClock{now: time.Unix(800, 0).UTC()}
	events := make([]Event, 0, 4)
	dependencies := Dependencies{
		ExecutionID: "batch1b-root", Driver: driver, Clock: clock,
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{Name: "Batch 1B", AppID: "com.example.batch1b"}
	root := &compiledFlow{path: "/workspace/batch1b.yaml", config: config, body: []compiledDispatch{compiled}}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "batch1b-root/root-run-000001")
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	return result, append([]Event(nil), events...), config
}
