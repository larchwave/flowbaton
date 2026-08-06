package engine

import (
	"context"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/model"
)

// ExecuteBatch1ARootForReportTest exposes a root lifecycle
// only to the external engine test package. This file is absent from normal
// package builds and does not change production registry or API exposure.
func ExecuteBatch1ARootForReportTest(
	t testing.TB,
	keyword model.CommandKeyword,
	label string,
) (FlowResult, []Event, model.Config) {
	t.Helper()

	command := batch1ACommand(keyword, "", "20,30", nil, &label, nil, intPointerForTap(0))
	registry, err := newHandlerRegistry(doubleTapOnHandlerSpec(), longPressOnHandlerSpec())
	if err != nil {
		t.Fatalf("newHandlerRegistry() error = %v", err)
	}
	compiled, err := newDispatcher(registry).compile(
		context.Background(),
		compileContext{containingFlow: "/workspace/batch1a.yaml"},
		command,
	)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}

	driver := batch1APointDriver(400, 884)
	clock := &batch1ATraceClock{now: time.Unix(700, 0).UTC()}
	events := make([]Event, 0, 4)
	dependencies := Dependencies{
		ExecutionID: "batch1a-root",
		Driver:      driver,
		Clock:       clock,
		JSFactory:   tapJSFactory(t),
		Controller:  NoopController{},
		Listeners: []Listener{ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		})},
	}
	config := model.Config{Name: "Batch 1A", AppID: "com.example.batch1a"}
	root := &compiledFlow{
		path:   "/workspace/batch1a.yaml",
		config: config,
		body:   []compiledDispatch{compiled},
	}
	result, err := executeCompiledRootForRun(
		context.Background(),
		dependencies,
		root,
		"batch1a-root/root-run-000001",
	)
	if err != nil {
		t.Fatalf("executeCompiledRootForRun() error = %v", err)
	}
	return result, append([]Event(nil), events...), config
}
