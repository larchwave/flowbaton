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
	"io"

	"github.com/larchwave/flowbaton/internal/device"
)

type Frame struct {
	Content     []byte
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
	content, err := executor.Driver.TakeScreenshot(ctx, device.ScreenshotRequest{Compressed: true})
	if err != nil {
		return Frame{}, err
	}
	width, height := 0, 0
	if config, _, decodeErr := image.DecodeConfig(bytes.NewReader(content)); decodeErr == nil {
		width, height = config.Width, config.Height
	} else if info, infoErr := executor.Driver.DeviceInfo(ctx); infoErr == nil {
		width, height = info.WidthPixels, info.HeightPixels
	} else {
		return Frame{}, fmt.Errorf("decode screenshot dimensions: %w", decodeErr)
	}
	orientation := "portrait"
	if width > height {
		orientation = "landscape-left"
	}
	return Frame{Content: content, Orientation: orientation, Width: width, Height: height}, nil
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
		return executor.Driver.SetOrientation(ctx, input.Orientation)
	default:
		return fmt.Errorf("%w: command %q", device.ErrUnsupported, command)
	}
}

func decodeExecutorPayload(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid command payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("invalid command payload: trailing JSON")
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
