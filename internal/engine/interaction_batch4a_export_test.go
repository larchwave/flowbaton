package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// ExecuteBatch4ARootForReportTest exposes one text-input root lifecycle
// only to the external engine test package.
func ExecuteBatch4ARootForReportTest(t testing.TB) (
	FlowResult,
	[]Event,
	model.Config,
	[]device.InputTextRequest,
	[]device.EraseTextRequest,
) {
	t.Helper()

	registry, err := newHandlerRegistry(inputTextHandlerSpec(), eraseTextHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	dispatcher := newDispatcher(registry)
	compiled, err := dispatcher.compileSequence(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch4a-report.yaml"},
		[]model.Command{
			batch4AInputObject("${TEXT}", stringPointer("${LABEL}")),
			batch4AErase(int64(7)),
		},
	)
	if err != nil {
		t.Fatalf("compileSequence() error = %v", err)
	}

	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	driver := batch4ADriverWithSettle([]enginetest.Result[*device.ViewHierarchy]{
		{Value: ready}, {Value: ready}, {Value: ready}, {Value: ready},
	})
	events := make([]Event, 0, 6)
	dependencies := Dependencies{
		ExecutionID: "batch4a-report", Driver: driver, Clock: &batch1ATraceClock{now: time.Unix(2200, 0).UTC()},
		JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{
		Name: "Batch 4A", AppID: "com.example.batch4a.report",
		Env: map[string]string{"TEXT": "hello 世界", "LABEL": "Typing profile"},
	}
	root := &compiledFlow{path: "/workspace/batch4a-report.yaml", config: config, body: compiled}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "batch4a-report/root-run-000001")
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	return result, append([]Event(nil), events...), cloneConfig(config),
		batch4AInputRequests(driver.Actions()), batch4AEraseRequests(driver.Actions())
}

// ExecuteBatch4AFailureRootForReportTest exposes one text-input Driver
// failure only to the external report-projection test.
func ExecuteBatch4AFailureRootForReportTest(t testing.TB) (FlowResult, model.Config, error) {
	t.Helper()

	registry, err := newHandlerRegistry(inputTextHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch4a-report-failure.yaml"},
		batch4AInputObject("failure", stringPointer("Typing failure")),
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	driver := batch4ADriver()
	driver.Enqueue(enginetest.DriverScript{
		InputText: []enginetest.Result[struct{}]{{Err: errors.New("Batch 4A report Driver failure")}},
	})
	config := model.Config{Name: "Batch 4A failure", AppID: "com.example.batch4a.report.failure"}
	root := &compiledFlow{
		path: "/workspace/batch4a-report-failure.yaml", config: config, body: []compiledDispatch{compiled},
	}
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4a-report-failure", Driver: driver, Clock: newAdvancingClock(),
		JSFactory: tapJSFactory(t), Controller: NoopController{},
	}, root, "batch4a-report-failure/root-run-000001")
	return result, cloneConfig(config), runErr
}
