package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestInteractionBatch4HProductionRegistryIsExactThirtyWithRandomInput(t *testing.T) {
	t.Parallel()

	production, err := productionHandlerRegistry()
	if err != nil {
		t.Fatalf("productionHandlerRegistry() error = %v", err)
	}
	if len(production.byKeyword) != len(productionKeywords()) {
		t.Fatalf("production registry keys = %#v, want the complete set", sortedRegistryKeywords(production))
	}
	// The production registry includes all seven random-input commands.
	for _, definition := range randomInputDefinitions() {
		if _, exists := production.lookup(definition.keyword); !exists {
			t.Fatalf("random keyword %s missing from production registry", definition.keyword)
		}
	}
	private, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil || len(private.byKeyword) != 7 {
		t.Fatalf("private random registry = %#v, %v", private.byKeyword, err)
	}
}

// ExecuteBatch4HRootForReportTest exposes one random-input root lifecycle
// only to the external report-projection test package.
func ExecuteBatch4HRootForReportTest(t testing.TB) (
	FlowResult,
	[]Event,
	model.Config,
	[]InputRequest,
	[]device.InputTextRequest,
) {
	t.Helper()

	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	dispatcher := newDispatcher(registry)
	commands := []model.Command{
		batch4HRandomObject(model.CommandInputRandomText, map[string]any{"length": int64(0)}),
		batch4HRandomObject(model.CommandInputRandomNumber, map[string]any{"length": int64(1024)}),
		batch4HRandomBare(model.CommandInputRandomEmail),
		batch4HRandomBare(model.CommandInputRandomPersonName),
		batch4HRandomBare(model.CommandInputRandomCityName),
		batch4HRandomBare(model.CommandInputRandomCountryName),
		batch4HRandomBare(model.CommandInputRandomColorName),
	}
	compiled, err := dispatcher.compileSequence(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch4h-report.yaml"},
		commands,
	)
	if err != nil {
		t.Fatalf("compileSequence() error = %v", err)
	}

	ready := &device.ViewHierarchy{Root: device.TreeNode{Attributes: map[string]string{"ready": "true"}}}
	settles := make([]enginetest.Result[*device.ViewHierarchy], len(commands)*2)
	for index := range settles {
		settles[index] = enginetest.Result[*device.ViewHierarchy]{Value: ready}
	}
	driver := batch4ADriverWithSettle(settles)
	generator := &batch4HSequenceGenerator{values: []string{
		"", "007", "generated@example.test", "Ada Lovelace", "Toronto", "Canada", "ultraviolet",
	}}
	events := make([]Event, 0, len(commands)*2+2)
	dependencies := Dependencies{
		ExecutionID: "batch4h-report", Driver: driver, Clock: &batch1ATraceClock{now: time.Unix(2600, 0).UTC()},
		InputGenerator: generator, JSFactory: tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{Name: "Batch 4B-H", AppID: "com.example.batch4h.report"}
	root := &compiledFlow{path: "/workspace/batch4h-report.yaml", config: config, body: compiled}
	result, err := executeCompiledRootForRun(context.Background(), dependencies, root, "batch4h-report/root-run-000001")
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	return result, append([]Event(nil), events...), cloneConfig(config), generator.Requests(), batch4AInputRequests(driver.Actions())
}

// ExecuteBatch4HFailureRootForReportTest exposes one generator failure to the
// external report-projection test package.
func ExecuteBatch4HFailureRootForReportTest(t testing.TB) (FlowResult, []Event, model.Config, error) {
	t.Helper()

	registry, err := newHandlerRegistry(randomInputHandlerSpecs()...)
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(), compileContext{containingFlow: "/workspace/batch4h-report-failure.yaml"},
		batch4HRandomBare(model.CommandInputRandomEmail),
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	config := model.Config{Name: "Batch 4B-H failure", AppID: "com.example.batch4h.report.failure"}
	root := &compiledFlow{
		path: "/workspace/batch4h-report-failure.yaml", config: config, body: []compiledDispatch{compiled},
	}
	events := make([]Event, 0, 4)
	result, runErr := executeCompiledRootForRun(context.Background(), Dependencies{
		ExecutionID: "batch4h-report-failure", Driver: batch4ADriver(), Clock: newAdvancingClock(),
		InputGenerator: &batch4HRecordingGenerator{value: "partial@example.test", err: errors.New("Batch 4B-H generator failure")},
		JSFactory:      tapJSFactory(t), Controller: NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}, root, "batch4h-report-failure/root-run-000001")
	return result, append([]Event(nil), events...), cloneConfig(config), runErr
}
