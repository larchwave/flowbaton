package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
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

func TestDriverExecutorRejectsDuplicatePayloadBeforeMutation(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	err := (DriverExecutor{Driver: driver}).Execute(
		context.Background(), "tap", json.RawMessage(`{"x":1,"x":2,"y":3}`))
	if err == nil || len(driver.Actions()) != 0 {
		t.Fatalf("err=%v actions=%v", err, driver.Actions())
	}
}

func TestDriverExecutorProducesContentBoundFrame(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		orientation device.Orientation
		want        string
	}{
		{"PORTRAIT", "portrait"},
		{"UPSIDE_DOWN", "portrait-upside-down"},
		{"LANDSCAPE_LEFT", "landscape-left"},
		{"LANDSCAPE_RIGHT", "landscape-right"},
	} {
		t.Run(string(test.orientation), func(t *testing.T) {
			driver := enginetest.NewFakeDriver()
			driver.Enqueue(enginetest.DriverScript{TakeScreenshot: []enginetest.Result[[]byte]{{Value: encoded.Bytes()}}})
			frame, err := (DriverExecutor{Driver: orientationDriver{
				FakeDriver:  driver,
				orientation: test.orientation,
			}}).CaptureFrame(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if frame.Width != 1 || frame.Height != 1 || frame.ContentType != "image/png" || frame.Orientation != test.want || frameContentDigest(frame.Content) == "" {
				t.Fatalf("frame=%#v", frame)
			}
			if driver.Actions()[0].Request != (device.ScreenshotRequest{Compressed: true}) {
				t.Fatalf("request=%#v", driver.Actions()[0].Request)
			}
		})
	}
}

func TestDriverExecutorDetectsJPEGFrameContent(t *testing.T) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 3)), nil); err != nil {
		t.Fatal(err)
	}
	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{TakeScreenshot: []enginetest.Result[[]byte]{{Value: encoded.Bytes()}}})
	frame, err := (DriverExecutor{Driver: orientationDriver{
		FakeDriver:  driver,
		orientation: "PORTRAIT",
	}}).CaptureFrame(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if frame.ContentType != "image/jpeg" || frame.Width != 2 || frame.Height != 3 {
		t.Fatalf("frame = %#v", frame)
	}
}

func TestDriverExecutorTranslatesEveryWireOrientation(t *testing.T) {
	for input, want := range map[string]device.Orientation{
		"portrait":             "PORTRAIT",
		"portrait-upside-down": "UPSIDE_DOWN",
		"landscape-left":       "LANDSCAPE_LEFT",
		"landscape-right":      "LANDSCAPE_RIGHT",
	} {
		driver := enginetest.NewFakeDriver()
		payload := json.RawMessage(`{"orientation":"` + input + `"}`)
		if err := (DriverExecutor{Driver: driver}).Execute(context.Background(), "set-orientation", payload); err != nil {
			t.Fatalf("set-orientation %q: %v", input, err)
		}
		if got := driver.Actions()[0].Request; got != want {
			t.Fatalf("set-orientation %q request = %q, want %q", input, got, want)
		}
	}
}

func TestDriverExecutorRequiresAuthoritativeOrientation(t *testing.T) {
	driver := enginetest.NewFakeDriver()
	if _, err := (DriverExecutor{Driver: driver}).CaptureFrame(context.Background()); err == nil {
		t.Fatal("CaptureFrame succeeded without an orientation reader")
	}
	if len(driver.Actions()) != 0 {
		t.Fatalf("actions = %v, want none", driver.Actions())
	}
}

type orientationDriver struct {
	*enginetest.FakeDriver
	orientation device.Orientation
}

func (driver orientationDriver) CurrentOrientation(context.Context) (device.Orientation, error) {
	return driver.orientation, nil
}
