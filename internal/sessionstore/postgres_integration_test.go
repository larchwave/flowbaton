package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

// FLOWBATON_TEST_POSTGRES_URL must name a disposable PostgreSQL database. The
// test migrates it and truncates only FlowBaton-owned tables.
func TestPostgresFencingIdempotencyAndLifecycle(t *testing.T) {
	databaseURL := os.Getenv("FLOWBATON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("FLOWBATON_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `TRUNCATE flowbaton_idempotency,flowbaton_events,flowbaton_requests,flowbaton_devices,flowbaton_sessions,flowbaton_token_nonces,flowbaton_identity_mappings,flowbaton_nodes CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	if err := store.RegisterNode(ctx, "node-1", "https://node-1", now); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", "node-1", []string{"tap", "input-text"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertIdentity(ctx, Identity{CertificateFingerprint: "cert-1", TenantID: "tenant-1", PrincipalID: "principal-1"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeTokenNonce(ctx, "cert-1", "nonce-1234567890", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := store.ConsumeTokenNonce(ctx, "cert-1", "nonce-1234567890", now.Add(5*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonce replay error=%v", err)
	}
	acquire := AcquireInput{TenantID: "tenant-1", PrincipalID: "principal-1", AuthProfileID: "remote-cloud-mac-v1", ChannelBindingSHA256: "binding", RequestNonce: "nonce-1234567890", BindingExpiresAt: now.Add(5 * time.Minute), ResourceID: "device-1", RequestedCapabilities: []string{"tap"}, IdempotencyKey: "acquire-key-0001", ReleaseIdempotencyKey: "release-key-0001", LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second, Now: now}
	first, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	replayResult, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if !replayResult.Replay || replayResult.Session.SessionID != first.Session.SessionID {
		t.Fatalf("replay=%#v", replayResult)
	}
	contender := acquire
	contender.IdempotencyKey = "acquire-key-busy"
	contender.ReleaseIdempotencyKey = "release-key-busy"
	if _, err := store.Acquire(ctx, contender); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent lease error=%v", err)
	}
	conflict := acquire
	conflict.ResourceID = "device-2"
	if _, err := store.Acquire(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	heartbeatPayload, _ := json.Marshal(map[string]any{"requested_extension_ms": 10000})
	heartbeat := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding", RequestID: "request-heartbeat", Type: "heartbeat", IdempotencyKey: "heartbeat-key-01", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: heartbeatPayload, RequestedExtension: 10 * time.Second, Now: now.Add(time.Second)}
	renewed, err := store.Apply(ctx, heartbeat)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.Session.LeaseExpiresAt.After(first.Session.LeaseExpiresAt) {
		t.Fatalf("lease was not extended")
	}
	stale := heartbeat
	stale.IdempotencyKey = "heartbeat-key-02"
	stale.Generation++
	if _, err := store.Apply(ctx, stale); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale error=%v", err)
	}
	if err := store.MarkDisconnected(ctx, "tenant-1", first.Session.SessionID, first.Session.Generation, first.Session.FencingTokenSHA256, "transport_interrupted", now.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	reconnect := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding", RequestID: "request-reconnect", Type: "reconnect", IdempotencyKey: "reconnect-key-001", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: json.RawMessage(`{}`), LastAcknowledgedEvent: 2, Now: now.Add(1800 * time.Millisecond)}
	if _, err := store.Apply(ctx, reconnect); err != nil {
		t.Fatal(err)
	}
	release := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding", RequestID: "request-release", Type: "release", IdempotencyKey: "release-key-0001", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: json.RawMessage(`{}`), Now: now.Add(2 * time.Second)}
	if _, err := store.Apply(ctx, release); err != nil {
		t.Fatal(err)
	}
	releaseReplay, err := store.Apply(ctx, release)
	if err != nil {
		t.Fatal(err)
	}
	if !releaseReplay.Replay {
		t.Fatal("release retry was not replayed")
	}
	acquire.IdempotencyKey = "acquire-key-0002"
	acquire.ReleaseIdempotencyKey = "release-key-0002"
	acquire.Now = now.Add(3 * time.Second)
	second, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if second.Session.Generation != first.Session.Generation+1 {
		t.Fatalf("generation=%d want=%d", second.Session.Generation, first.Session.Generation+1)
	}
}
