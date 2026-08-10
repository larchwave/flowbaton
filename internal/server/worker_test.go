package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type fakeWorkerStore struct {
	input           sessionstore.InputWork
	started         bool
	completed       *sessionstore.ExecutionFailure
	result          string
	cancelExecution bool
}

func (*fakeWorkerStore) ClaimFrame(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.FrameWork, error) {
	return sessionstore.FrameWork{}, sessionstore.ErrNotFound
}
func (*fakeWorkerStore) CompleteFrame(context.Context, sessionstore.FrameWork, sessionstore.FrameData) error {
	return nil
}
func (*fakeWorkerStore) FailFrame(context.Context, sessionstore.FrameWork, string, bool, string) error {
	return nil
}
func (store *fakeWorkerStore) ClaimInput(context.Context, sessionstore.NodeLease, time.Duration) (sessionstore.InputWork, error) {
	if store.input.SessionID == "" {
		return sessionstore.InputWork{}, sessionstore.ErrNotFound
	}
	work := store.input
	store.input = sessionstore.InputWork{}
	return work, nil
}
func (store *fakeWorkerStore) StartInput(_ context.Context, _ sessionstore.InputWork) error {
	store.started = true
	return nil
}
func (store *fakeWorkerStore) CompleteInput(_ context.Context, _ sessionstore.InputWork, result string, _ time.Duration, failure *sessionstore.ExecutionFailure) error {
	store.result, store.completed = result, failure
	return nil
}
func (store *fakeWorkerStore) WaitInputActive(ctx context.Context, _ sessionstore.InputWork, _ time.Duration) (bool, error) {
	if store.cancelExecution {
		return false, nil
	}
	<-ctx.Done()
	return false, ctx.Err()
}
func (*fakeWorkerStore) WaitForWork(context.Context, string, time.Duration) error { return nil }

type fakeExecutor struct {
	command            string
	payload            json.RawMessage
	err                error
	wait               bool
	ignoreCancellation bool
}

func (*fakeExecutor) CaptureFrame(context.Context) (Frame, error) {
	return Frame{}, errors.New("unused")
}
func (executor *fakeExecutor) Execute(ctx context.Context, command string, payload json.RawMessage) error {
	executor.command, executor.payload = command, append(json.RawMessage(nil), payload...)
	if executor.wait {
		<-ctx.Done()
		if executor.ignoreCancellation {
			return executor.err
		}
		return ctx.Err()
	}
	return executor.err
}

func TestWorkerStartsFenceBeforeExactlyOneDeviceMutation(t *testing.T) {
	work := sessionstore.InputWork{SessionID: "session-1", ResourceID: "device-1", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)}
	store := &fakeWorkerStore{input: work}
	executor := &fakeExecutor{}
	worker := &Worker{NodeLease: sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1}, Store: store, Executors: map[string]DeviceExecutor{"device-1": executor}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || !store.started || store.result != "applied" || store.completed != nil || executor.command != "tap" {
		t.Fatalf("worked=%v err=%v started=%v result=%q failure=%#v executor=%#v", worked, err, store.started, store.result, store.completed, executor)
	}
}

func TestWorkerMissingExecutorEmitsTypedFailure(t *testing.T) {
	store := &fakeWorkerStore{input: sessionstore.InputWork{SessionID: "session-1", ResourceID: "missing", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)}}
	worker := &Worker{NodeLease: sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1}, Store: store, Executors: map[string]DeviceExecutor{}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || store.completed == nil || store.completed.Code != "DEVICE_UNAVAILABLE" || store.completed.Retryable {
		t.Fatalf("worked=%v err=%v failure=%#v", worked, err, store.completed)
	}
}

func TestWorkerCancellationAfterStartIsNonRetryableUnknown(t *testing.T) {
	store := &fakeWorkerStore{
		input:           sessionstore.InputWork{SessionID: "session-1", ResourceID: "device-1", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)},
		cancelExecution: true,
	}
	executor := &fakeExecutor{wait: true}
	worker := &Worker{NodeLease: sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1}, Store: store, Executors: map[string]DeviceExecutor{"device-1": executor}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || !store.started || store.completed == nil || store.completed.Retryable ||
		store.completed.Code != "DEVICE_UNAVAILABLE" || store.completed.SafeMessage != "device input outcome is unknown after cancellation" {
		t.Fatalf("worked=%v err=%v started=%v failure=%#v", worked, err, store.started, store.completed)
	}
}

func TestWorkerSuccessfulExecutorAfterCancellationIsNonRetryableUnknown(t *testing.T) {
	store := &fakeWorkerStore{
		input:           sessionstore.InputWork{SessionID: "session-1", ResourceID: "device-1", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)},
		cancelExecution: true,
	}
	executor := &fakeExecutor{wait: true, ignoreCancellation: true}
	worker := &Worker{NodeLease: sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1}, Store: store, Executors: map[string]DeviceExecutor{"device-1": executor}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || !store.started || store.result != "rejected" || store.completed == nil ||
		store.completed.Code != "DEVICE_UNAVAILABLE" || store.completed.Retryable ||
		store.completed.SafeMessage != "device input outcome is unknown after cancellation" {
		t.Fatalf("worked=%v err=%v started=%v result=%q failure=%#v", worked, err, store.started, store.result, store.completed)
	}
}

func TestWorkerCancellationIsACleanShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worker := &Worker{
		NodeLease: sessionstore.NodeLease{NodeID: "node-1", WorkerEpoch: 1},
		Store:     &fakeWorkerStore{},
	}
	if err := worker.Run(ctx); err != nil {
		t.Fatalf("Worker.Run() error = %v, want clean cancellation", err)
	}
}
