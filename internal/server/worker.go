package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type WorkerStore interface {
	HeartbeatNode(context.Context, string, time.Time) error
	ClaimFrame(context.Context, string, time.Duration) (sessionstore.FrameWork, error)
	CompleteFrame(context.Context, sessionstore.FrameWork, map[string]any, time.Time) error
	FailFrame(context.Context, sessionstore.FrameWork, string, bool, string, time.Time) error
	ClaimInput(context.Context, string, time.Duration) (sessionstore.InputWork, error)
	StartInput(context.Context, sessionstore.InputWork, time.Time) error
	CompleteInput(context.Context, sessionstore.InputWork, string, time.Duration, *sessionstore.ExecutionFailure, time.Time) error
	RecoverAmbiguousInputs(context.Context, string, time.Time) (int64, error)
	WaitForWork(context.Context, string, time.Duration) error
}

type Worker struct {
	NodeID            string
	Store             WorkerStore
	Executors         map[string]DeviceExecutor
	PollInterval      time.Duration
	ClaimDuration     time.Duration
	ExecutionTimeout  time.Duration
	HeartbeatInterval time.Duration
	Now               func() time.Time
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker.NodeID == "" || worker.Store == nil {
		return errors.New("worker requires node identity and store")
	}
	worker.defaults()
	if _, err := worker.Store.RecoverAmbiguousInputs(ctx, worker.NodeID, worker.now().Add(-worker.ExecutionTimeout)); err != nil {
		return fmt.Errorf("recover ambiguous device input: %w", err)
	}
	heartbeat := time.NewTicker(worker.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		worked, err := worker.runOne(ctx)
		if err != nil {
			return err
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-heartbeat.C:
			if err := worker.Store.HeartbeatNode(ctx, worker.NodeID, worker.now()); err != nil {
				return fmt.Errorf("heartbeat worker node: %w", err)
			}
			if _, err := worker.Store.RecoverAmbiguousInputs(ctx, worker.NodeID, worker.now().Add(-worker.ExecutionTimeout)); err != nil {
				return fmt.Errorf("recover ambiguous device input: %w", err)
			}
		default:
			if err := worker.Store.WaitForWork(ctx, worker.NodeID, worker.PollInterval); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("wait for device work: %w", err)
			}
		}
	}
}

func (worker *Worker) runOne(ctx context.Context) (bool, error) {
	frame, err := worker.Store.ClaimFrame(ctx, worker.NodeID, worker.ClaimDuration)
	if err == nil {
		return true, worker.produceFrame(ctx, frame)
	}
	if !errors.Is(err, sessionstore.ErrNotFound) {
		return false, fmt.Errorf("claim frame: %w", err)
	}
	input, err := worker.Store.ClaimInput(ctx, worker.NodeID, worker.ClaimDuration)
	if errors.Is(err, sessionstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim input: %w", err)
	}
	return true, worker.executeInput(ctx, input)
}

func (worker *Worker) produceFrame(ctx context.Context, work sessionstore.FrameWork) error {
	executor := worker.Executors[work.ResourceID]
	if executor == nil {
		return worker.Store.FailFrame(ctx, work, "DEVICE_UNAVAILABLE", false, "no executor is registered for the leased device", worker.now())
	}
	operationCtx, cancel := context.WithTimeout(ctx, worker.ExecutionTimeout)
	frame, err := executor.CaptureFrame(operationCtx)
	cancel()
	if err != nil {
		code, retryable := classifyExecutionError(err)
		return worker.Store.FailFrame(ctx, work, code, retryable, "device frame capture failed", worker.now())
	}
	if len(frame.Content) == 0 || frame.Width < 1 || frame.Height < 1 || !validOrientation(frame.Orientation) {
		return worker.Store.FailFrame(ctx, work, "DEVICE_UNAVAILABLE", false, "device returned an invalid frame", worker.now())
	}
	payload := map[string]any{"orientation": frame.Orientation, "width": frame.Width, "height": frame.Height, "content_sha256": frameContentDigest(frame.Content), "queue_depth": 0, "dropped_since_previous": 0}
	return worker.Store.CompleteFrame(ctx, work, payload, worker.now())
}

func (worker *Worker) executeInput(ctx context.Context, work sessionstore.InputWork) error {
	if err := worker.Store.StartInput(ctx, work, worker.now()); err != nil {
		if errors.Is(err, sessionstore.ErrFenced) || errors.Is(err, sessionstore.ErrInvalidState) {
			failure := &sessionstore.ExecutionFailure{Code: "FENCED", Retryable: false, SafeMessage: "input frame or lease is stale"}
			if completeErr := worker.Store.CompleteInput(ctx, work, "rejected", 0, failure, worker.now()); completeErr == nil {
				return nil
			}
		}
		return fmt.Errorf("start input: %w", err)
	}
	started := worker.now()
	executor := worker.Executors[work.ResourceID]
	if executor == nil {
		failure := &sessionstore.ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "no executor is registered for the leased device"}
		return worker.Store.CompleteInput(ctx, work, "rejected", worker.now().Sub(started), failure, worker.now())
	}
	operationCtx, cancel := context.WithTimeout(ctx, worker.ExecutionTimeout)
	err := executor.Execute(operationCtx, work.Command, work.CommandPayload)
	cancel()
	if err != nil {
		code, retryable := classifyExecutionError(err)
		failure := &sessionstore.ExecutionFailure{Code: code, Retryable: retryable, SafeMessage: "device input was rejected"}
		return worker.Store.CompleteInput(ctx, work, "rejected", worker.now().Sub(started), failure, worker.now())
	}
	return worker.Store.CompleteInput(ctx, work, "applied", worker.now().Sub(started), nil, worker.now())
}

func (worker *Worker) defaults() {
	if worker.PollInterval <= 0 {
		worker.PollInterval = 250 * time.Millisecond
	}
	if worker.ClaimDuration <= 0 {
		worker.ClaimDuration = 30 * time.Second
	}
	if worker.ExecutionTimeout <= 0 {
		worker.ExecutionTimeout = 20 * time.Second
	}
	if worker.HeartbeatInterval <= 0 {
		worker.HeartbeatInterval = 5 * time.Second
	}
}

func (worker *Worker) now() time.Time {
	if worker.Now != nil {
		return worker.Now().UTC()
	}
	return time.Now().UTC()
}

func classifyExecutionError(err error) (string, bool) {
	if errors.Is(err, device.ErrUnsupported) {
		return "CAPABILITY_UNSUPPORTED", false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "DEVICE_UNAVAILABLE", true
	}
	return "DEVICE_UNAVAILABLE", false
}

func validOrientation(value string) bool {
	switch value {
	case "portrait", "portrait-upside-down", "landscape-left", "landscape-right":
		return true
	default:
		return false
	}
}
