package engine

import (
	"context"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

// ExecuteBatch3RootForReportTest exposes one tap root lifecycle
// only to the external engine test package.
func ExecuteBatch3RootForReportTest(t testing.TB) (FlowResult, []Event, model.Config, []device.ScrollVerticalRequest) {
	t.Helper()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	command := batch3Command("Ready", map[string]any{
		"visibilityPercentage": int64(10), "centerElement": true,
	})
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch3-report.yaml"}, command,
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}

	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Ready", device.Bounds{Y: 80, Width: 100, Height: 20}),
	}, []error{nil}, nil)
	events := make([]Event, 0, 4)
	dependencies := Dependencies{
		ExecutionID: "batch3-report", Driver: driver, Clock: newBatch3Clock(time.Unix(1000, 0), true),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{Name: "Batch 3", AppID: "com.example.batch3.report"}
	root := &compiledFlow{path: "/workspace/batch3-report.yaml", config: config, body: []compiledDispatch{compiled}}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "batch3-report/root-run-000001")
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	return result, append([]Event(nil), events...), config, batch3ScrollRequests(driver.Actions())
}

// ExecuteBatch3FailureRootForReportTest exposes required tap exhaustion
// only to the external report-projection test.
func ExecuteBatch3FailureRootForReportTest(t testing.TB) (FlowResult, model.Config, error) {
	t.Helper()

	registry, err := newHandlerRegistry(scrollUntilVisibleHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	command := batch3Command("Ready", map[string]any{"timeout": int64(0)})
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch3-report-failure.yaml"}, command,
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	config := model.Config{Name: "Batch 3 failure", AppID: "com.example.batch3.report"}
	root := &compiledFlow{path: "/workspace/batch3-report-failure.yaml", config: config, body: []compiledDispatch{compiled}}
	driver := batch3Driver(batch3Info(100, 100), []device.TreeNode{
		batch3Tree("Other", device.Bounds{Width: 100, Height: 100}),
	}, nil, nil)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch3-report-failure", Driver: driver, Clock: newBatch3Clock(time.Unix(1001, 0), true),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch3-report-failure/root-run-000001")
	return result, config, runErr
}
