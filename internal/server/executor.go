package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

type Frame struct {
	Content     []byte
	ContentType string
	Orientation string
	Width       int
	Height      int
}

type FrameProducer interface {
	CaptureFrame(context.Context) (Frame, error)
}

type DeviceExecutor interface {
	FrameProducer
	Execute(context.Context, string, json.RawMessage) error
}

type DriverExecutor struct{ Driver device.Driver }

func (executor DriverExecutor) CaptureFrame(ctx context.Context) (Frame, error) {
	if executor.Driver == nil {
		return Frame{}, errors.New("device executor has no driver")
	}
	orientationReader, ok := executor.Driver.(device.OrientationReader)
	if !ok {
		return Frame{}, errors.New("device executor driver cannot read orientation")
	}
	orientation, err := orientationReader.CurrentOrientation(ctx)
	if err != nil {
		return Frame{}, fmt.Errorf("read device orientation: %w", err)
	}
	frameOrientation, ok := frameOrientations[orientation]
	if !ok {
		return Frame{}, fmt.Errorf("device reported unsupported orientation %q", orientation)
	}
	content, err := executor.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{Compressed: true})
	if err != nil {
		return Frame{}, err
	}
	config, format, decodeErr := image.DecodeConfig(bytes.NewReader(content))
	if decodeErr != nil {
		return Frame{}, fmt.Errorf("decode screenshot dimensions: %w", decodeErr)
	}
	contentType, ok := frameContentTypes[format]
	if !ok {
		return Frame{}, fmt.Errorf("unsupported screenshot format %q", format)
	}
	return Frame{
		Content: content, ContentType: contentType,
		Orientation: frameOrientation, Width: config.Width, Height: config.Height,
	}, nil
}

var frameContentTypes = map[string]string{
	"jpeg": "image/jpeg",
	"png":  "image/png",
}

var frameOrientations = map[device.Orientation]string{
	"PORTRAIT":        "portrait",
	"UPSIDE_DOWN":     "portrait-upside-down",
	"LANDSCAPE_LEFT":  "landscape-left",
	"LANDSCAPE_RIGHT": "landscape-right",
}

var commandOrientations = map[device.Orientation]device.Orientation{
	"portrait":             "PORTRAIT",
	"portrait-upside-down": "UPSIDE_DOWN",
	"landscape-left":       "LANDSCAPE_LEFT",
	"landscape-right":      "LANDSCAPE_RIGHT",
}

func (executor DriverExecutor) Execute(ctx context.Context, command string, payload json.RawMessage) error {
	if executor.Driver == nil {
		return errors.New("device executor has no driver")
	}
	switch command {
	case "tap":
		var input struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		if err := decodeExecutorPayload(payload, &input); err != nil {
			return err
		}
		return executor.Driver.Tap(ctx, device.TapRequest{Point: device.Point{X: input.X, Y: input.Y}})
	case "input-text":
		var input device.InputTextRequest
		if err := decodeExecutorPayload(payload, &input); err != nil || input.Text == "" {
			return invalidPayload(err)
		}
		return executor.Driver.InputText(ctx, input)
	case "press-key":
		var input device.PressKeyRequest
		if err := decodeExecutorPayload(payload, &input); err != nil || input.Code == "" {
			return invalidPayload(err)
		}
		return executor.Driver.PressKey(ctx, input)
	case "swipe":
		var input device.SwipeRequest
		if err := decodeExecutorPayload(payload, &input); err != nil {
			return err
		}
		if (input.Start == nil) != (input.End == nil) || (input.Start == nil && input.Direction == "") || input.DurationMillis < 0 {
			return errors.New("invalid swipe payload")
		}
		return executor.Driver.Swipe(ctx, input)
	case "set-orientation":
		var input struct {
			Orientation device.Orientation `json:"orientation"`
		}
		if err := decodeExecutorPayload(payload, &input); err != nil || input.Orientation == "" {
			return invalidPayload(err)
		}
		orientation, ok := commandOrientations[input.Orientation]
		if !ok {
			return errors.New("invalid command payload: unsupported orientation")
		}
		return executor.Driver.SetOrientation(ctx, orientation)
	default:
		return fmt.Errorf("%w: command %q", device.ErrUnsupported, command)
	}
}

func decodeExecutorPayload(payload []byte, target any) error {
	if err := strictjson.Decode(payload, target); err != nil {
		return fmt.Errorf("invalid command payload: %w", err)
	}
	return nil
}

func invalidPayload(err error) error {
	if err != nil {
		return err
	}
	return errors.New("invalid command payload")
}

func frameContentDigest(content []byte) string {
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}
