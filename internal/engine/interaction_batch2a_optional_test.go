package engine

import (
	"context"
	"errors"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/js"
	"github.com/larchwave/flowbaton/internal/model"
)

// TestExecuteOptionalHideKeyboardFailureWarns pins issue #11 end to end: the
// object form `hideKeyboard: {optional: true}` compiles, and when the device
// cannot hide the keyboard the command warns instead of failing the flow.
func TestExecuteOptionalHideKeyboardFailureWarns(t *testing.T) {
	t.Parallel()

	optional := true
	flow := model.Flow{
		SchemaVersion: model.ASTVersionV0,
		Path:          "/workspace/optional-hide-keyboard.yaml",
		Config:        model.Config{AppID: "com.example.optional"},
		Commands: []model.Command{{
			Kind: model.CommandHideKeyboard, Form: model.CommandFormObject,
			Arguments: map[string]any{"optional": true}, Optional: &optional,
		}},
	}
	driver := enginetest.NewFakeDriver()
	cause := errors.New("the keyboard route answered 500")
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("ios"), WidthGrid: 300, HeightGrid: 600},
		}},
		HideKeyboard: []enginetest.Result[struct{}]{{Err: cause}},
	})
	factory, err := js.NewFactory(js.Config{Random: deterministicRandom{}})
	if err != nil {
		t.Fatalf("js.NewFactory() error: %v", err)
	}

	results, err := Execute(context.Background(), singleCompileProgram(flow), Dependencies{
		ExecutionID: "optional-hide-keyboard",
		Driver:      driver, Clock: newAdvancingClock(), JSFactory: factory, Controller: NoopController{},
	})
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if len(results) != 1 || results[0].Outcome() != Warned {
		t.Fatalf("Execute() results = %#v, want one warned flow", results)
	}
	commands := results[0].Commands()
	if len(commands) != 1 || commands[0].Outcome() != Warned || !errors.Is(commands[0].ProductError(), cause) {
		t.Fatalf("command results = %#v, want one warned hideKeyboard carrying the device error", commands)
	}
}
