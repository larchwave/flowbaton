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
	input     sessionstore.InputWork
	started   bool
	completed *sessionstore.ExecutionFailure
	result    string
}

func (*fakeWorkerStore) HeartbeatNode(context.Context, string, time.Time) error { return nil }
func (*fakeWorkerStore) ClaimFrame(context.Context, string, time.Duration) (sessionstore.FrameWork, error) {
	return sessionstore.FrameWork{}, sessionstore.ErrNotFound
}
func (*fakeWorkerStore) CompleteFrame(context.Context, sessionstore.FrameWork, map[string]any, time.Time) error {
	return nil
}
func (*fakeWorkerStore) FailFrame(context.Context, sessionstore.FrameWork, string, bool, string, time.Time) error {
	return nil
}
func (store *fakeWorkerStore) ClaimInput(context.Context, string, time.Duration) (sessionstore.InputWork, error) {
	if store.input.SessionID == "" {
		return sessionstore.InputWork{}, sessionstore.ErrNotFound
	}
	work := store.input
	store.input = sessionstore.InputWork{}
	return work, nil
}
func (store *fakeWorkerStore) StartInput(_ context.Context, _ sessionstore.InputWork, _ time.Time) error {
	store.started = true
	return nil
}
func (store *fakeWorkerStore) CompleteInput(_ context.Context, _ sessionstore.InputWork, result string, _ time.Duration, failure *sessionstore.ExecutionFailure, _ time.Time) error {
	store.result, store.completed = result, failure
	return nil
}
func (*fakeWorkerStore) RecoverAmbiguousInputs(context.Context, string, time.Time) (int64, error) {
	return 0, nil
}
func (*fakeWorkerStore) WaitForWork(context.Context, string, time.Duration) error { return nil }

type fakeExecutor struct {
	command string
	payload json.RawMessage
	err     error
}

func (*fakeExecutor) CaptureFrame(context.Context) (Frame, error) {
	return Frame{}, errors.New("unused")
}
func (executor *fakeExecutor) Execute(_ context.Context, command string, payload json.RawMessage) error {
	executor.command, executor.payload = command, append(json.RawMessage(nil), payload...)
	return executor.err
}

func TestWorkerStartsFenceBeforeExactlyOneDeviceMutation(t *testing.T) {
	work := sessionstore.InputWork{SessionID: "session-1", ResourceID: "device-1", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)}
	store := &fakeWorkerStore{input: work}
	executor := &fakeExecutor{}
	worker := &Worker{NodeID: "node-1", Store: store, Executors: map[string]DeviceExecutor{"device-1": executor}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || !store.started || store.result != "applied" || store.completed != nil || executor.command != "tap" {
		t.Fatalf("worked=%v err=%v started=%v result=%q failure=%#v executor=%#v", worked, err, store.started, store.result, store.completed, executor)
	}
}

func TestWorkerMissingExecutorEmitsTypedFailure(t *testing.T) {
	store := &fakeWorkerStore{input: sessionstore.InputWork{SessionID: "session-1", ResourceID: "missing", Command: "tap", CommandPayload: json.RawMessage(`{"x":1,"y":2}`)}}
	worker := &Worker{NodeID: "node-1", Store: store, Executors: map[string]DeviceExecutor{}}
	worker.defaults()
	worked, err := worker.runOne(context.Background())
	if err != nil || !worked || store.completed == nil || store.completed.Code != "DEVICE_UNAVAILABLE" || store.completed.Retryable {
		t.Fatalf("worked=%v err=%v failure=%#v", worked, err, store.completed)
	}
}
