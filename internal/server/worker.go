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
	ClaimFrame(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.FrameWork, error)
	CompleteFrame(context.Context, sessionstore.FrameWork, sessionstore.FrameData) error
	FailFrame(context.Context, sessionstore.FrameWork, string, bool, string) error
	ClaimInput(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.InputWork, error)
	StartInput(context.Context, sessionstore.InputWork) error
	CompleteInput(context.Context, sessionstore.InputWork, string, time.Duration, *sessionstore.ExecutionFailure) error
	WaitInputActive(context.Context, sessionstore.InputWork, time.Duration) (bool, error)
	WaitForWork(context.Context, string, time.Duration) error
}

type Worker struct {
	NodeLease        sessionstore.NodeLease
	Store            WorkerStore
	Executors        map[string]DeviceExecutor
	PollInterval     time.Duration
	ClaimDuration    time.Duration
	ExecutionTimeout time.Duration
	Now              func() time.Time
}

func (worker *Worker) Run(ctx context.Context) error {
	if worker.NodeLease.NodeID == "" || worker.NodeLease.WorkerEpoch <= 0 || worker.Store == nil {
		return errors.New("worker requires node identity and store")
	}
	worker.defaults()
	for {
		if ctx.Err() != nil {
			return nil
		}
		worked, err := worker.runOne(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		default:
			if err := worker.Store.WaitForWork(ctx, worker.NodeLease.NodeID, worker.PollInterval); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("wait for device work: %w", err)
			}
		}
	}
}

func (worker *Worker) runOne(ctx context.Context) (bool, error) {
	frame, err := worker.Store.ClaimFrame(ctx, worker.NodeLease, worker.ClaimDuration)
	if err == nil {
		return true, worker.produceFrame(ctx, frame)
	}
	if !errors.Is(err, sessionstore.ErrNotFound) {
		return false, fmt.Errorf("claim frame: %w", err)
	}
	input, err := worker.Store.ClaimInput(ctx, worker.NodeLease, worker.ClaimDuration)
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
		return ignoreAbandonedWork(worker.Store.FailFrame(ctx, work, "DEVICE_UNAVAILABLE", false, "no executor is registered for the leased device"))
	}
	operationCtx, cancel := context.WithTimeout(ctx, worker.ExecutionTimeout)
	frame, err := executor.CaptureFrame(operationCtx)
	cancel()
	if err != nil {
		code, retryable := classifyExecutionError(err)
		return ignoreAbandonedWork(worker.Store.FailFrame(ctx, work, code, retryable, "device frame capture failed"))
	}
	if len(frame.Content) == 0 || frame.Width < 1 || frame.Height < 1 || !validOrientation(frame.Orientation) {
		return ignoreAbandonedWork(worker.Store.FailFrame(ctx, work, "DEVICE_UNAVAILABLE", false, "device returned an invalid frame"))
	}
	return ignoreAbandonedWork(worker.Store.CompleteFrame(ctx, work, sessionstore.FrameData{Content: frame.Content,
		ContentType: frame.ContentType, Orientation: frame.Orientation, Width: frame.Width, Height: frame.Height}))
}

func (worker *Worker) executeInput(ctx context.Context, work sessionstore.InputWork) error {
	if err := worker.Store.StartInput(ctx, work); err != nil {
		if errors.Is(err, sessionstore.ErrFenced) || errors.Is(err, sessionstore.ErrInvalidState) || errors.Is(err, sessionstore.ErrExpired) {
			return nil
		}
		return fmt.Errorf("start input: %w", err)
	}
	started := worker.now()
	executor := worker.Executors[work.ResourceID]
	if executor == nil {
		failure := &sessionstore.ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "no executor is registered for the leased device"}
		return worker.finishInput(ctx, work, "rejected", worker.now().Sub(started), failure)
	}
	operationCtx, cancel := context.WithTimeout(ctx, worker.ExecutionTimeout)
	watchCtx, stopWatch := context.WithCancel(ctx)
	type inputWatchResult struct {
		active bool
		err    error
	}
	watchResult := make(chan inputWatchResult, 1)
	go func() {
		active, err := worker.Store.WaitInputActive(watchCtx, work, 100*time.Millisecond)
		if err == nil && !active {
			cancel()
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			cancel()
		}
		watchResult <- inputWatchResult{active: active, err: err}
	}()
	err := executor.Execute(operationCtx, work.Command, work.CommandPayload)
	cancel()
	stopWatch()
	watch := <-watchResult
	if watch.err == nil && !watch.active {
		failure := &sessionstore.ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "device input outcome is unknown after cancellation"}
		return worker.finishInput(ctx, work, "rejected", worker.now().Sub(started), failure)
	}
	if watch.err != nil && !errors.Is(watch.err, context.Canceled) {
		err = errors.Join(err, fmt.Errorf("watch input cancellation: %w", watch.err))
	}
	if err != nil {
		code, retryable := classifyExecutionError(err)
		message := "device input was rejected"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			retryable = false
			message = "device input outcome is unknown after interruption"
		}
		failure := &sessionstore.ExecutionFailure{Code: code, Retryable: retryable, SafeMessage: message}
		return worker.finishInput(ctx, work, "rejected", worker.now().Sub(started), failure)
	}
	return worker.finishInput(ctx, work, "applied", worker.now().Sub(started), nil)
}

func (worker *Worker) finishInput(ctx context.Context, work sessionstore.InputWork, result string, latency time.Duration, failure *sessionstore.ExecutionFailure) error {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := worker.Store.CompleteInput(finishCtx, work, result, latency, failure)
	if errors.Is(err, sessionstore.ErrFenced) || errors.Is(err, sessionstore.ErrInvalidState) || errors.Is(err, sessionstore.ErrExpired) {
		return nil
	}
	return err
}

func ignoreAbandonedWork(err error) error {
	if errors.Is(err, sessionstore.ErrFenced) || errors.Is(err, sessionstore.ErrInvalidState) || errors.Is(err, sessionstore.ErrExpired) {
		return nil
	}
	return err
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
