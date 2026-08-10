package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

func TestDriverExecutorDispatchesClosedV1Commands(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	executor := DriverExecutor{Driver: driver}
	commands := []struct {
		name    string
		payload string
		method  enginetest.Method
	}{
		{"tap", `{"x":12.5,"y":30}`, enginetest.MethodTap},
		{"input-text", `{"text":"hello","app_ids":["com.example"]}`, enginetest.MethodInputText},
		{"press-key", `{"code":"ENTER","app_ids":["com.example"]}`, enginetest.MethodPressKey},
		{"swipe", `{"start":{"x":1,"y":2},"end":{"x":3,"y":4},"duration_millis":100}`, enginetest.MethodSwipe},
		{"set-orientation", `{"orientation":"landscape-left"}`, enginetest.MethodSetOrientation},
	}
	for _, test := range commands {
		if err := executor.Execute(context.Background(), test.name, json.RawMessage(test.payload)); err != nil {
			t.Fatalf("%s: %v", test.name, err)
		}
	}
	actions := driver.Actions()
	if len(actions) != len(commands) {
		t.Fatalf("actions=%v", actions)
	}
	for index, test := range commands {
		if actions[index].Method != test.method {
			t.Fatalf("action %d=%s want=%s", index, actions[index].Method, test.method)
		}
	}
}

func TestDriverExecutorRejectsUnknownPayloadBeforeMutation(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	err := (DriverExecutor{Driver: driver}).Execute(context.Background(), "tap", json.RawMessage(`{"x":1,"y":2,"extra":true}`))
	if err == nil || len(driver.Actions()) != 0 {
		t.Fatalf("err=%v actions=%v", err, driver.Actions())
	}
}

func TestDriverExecutorProducesContentBoundFrame(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{TakeScreenshot: []enginetest.Result[[]byte]{{Value: encoded.Bytes()}}})
	frame, err := (DriverExecutor{Driver: driver}).CaptureFrame(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width != 1 || frame.Height != 1 || frame.Orientation != "portrait" || frameContentDigest(frame.Content) == "" {
		t.Fatalf("frame=%#v", frame)
	}
	if driver.Actions()[0].Request != (device.ScreenshotRequest{Compressed: true}) {
		t.Fatalf("request=%#v", driver.Actions()[0].Request)
	}
}
