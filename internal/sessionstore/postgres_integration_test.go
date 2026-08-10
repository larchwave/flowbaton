package sessionstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresWorkerFencingSchemaBackfillsClaims(t *testing.T) {
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
	if _, err := store.Pool.Exec(ctx, `DROP TABLE IF EXISTS
		flowbaton_frame_content,flowbaton_input_jobs,flowbaton_frame_jobs,flowbaton_idempotency,flowbaton_events,
		flowbaton_requests,flowbaton_devices,flowbaton_sessions,flowbaton_token_nonces,
		flowbaton_identity_mappings,flowbaton_nodes,flowbaton_schema_versions CASCADE`); err != nil {
		t.Fatal(err)
	}
	foreignKeyTarget := []byte{82, 69, 70, 69, 82, 69, 78, 67, 69, 83}
	for _, version := range []string{"0001_runtime.sql", "0002_execution.sql"} {
		schema, err := schemaFiles.ReadFile("schema/" + version)
		if err != nil {
			t.Fatal(err)
		}
		schema = bytes.ReplaceAll(schema, []byte("__FK_TARGET__"), foreignKeyTarget)
		if _, err := store.Pool.Exec(ctx, string(schema)); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
		if _, err := store.Pool.Exec(ctx, `INSERT INTO flowbaton_schema_versions(version) VALUES($1)`, version); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	tx, err := store.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_nodes(node_id,public_address,last_heartbeat_at,worker_epoch)
		VALUES('node-1','https://node-1',$1,7)`, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_devices(tenant_id,resource_id,owner_node_id,capabilities)
		VALUES('tenant-1','device-1','node-1','["tap"]')`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_sessions(
			session_id,tenant_id,principal_id,auth_profile_id,channel_binding_sha256,request_nonce,
			binding_expires_at,resource_id,owner_node_id,lease_id,lease_generation,
			fencing_token_sha256,release_idempotency_key,capabilities,status,acquired_at,
			lease_expires_at,heartbeat_interval_ms
		) VALUES(
			'session-1','tenant-1','principal-1','remote-cloud-mac-v1','binding','nonce-1234567890',
			$1::timestamptz + interval '5 minutes','device-1','node-1','lease-1',1,'fence-1',
			'release-key-0001','["tap"]','active',$1,$1::timestamptz + interval '1 minute',15000
		)`, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_requests(
			session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,
			channel_binding_sha256,payload,created_at
		) VALUES(
			'session-1',1,'request-1','input','input-key-0000001','tenant-1','principal-1',
			'binding','{}',$1
		)`, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_input_jobs(
			session_id,request_sequence,tenant_id,principal_id,owner_node_id,request_id,
			idempotency_key,request_hash,lease_generation,fencing_token_sha256,stream_epoch,
			frame_sequence,command,command_payload,state,claimed_by,started_at,created_at
		) VALUES(
			'session-1',1,'tenant-1','principal-1','node-1','request-1','input-key-0000001',
			'hash-1',1,'fence-1',1,1,'tap','{"x":1,"y":2}','executing','node-1',$1,$1
		)`, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,state,claimed_by,created_at)
		VALUES('session-1','node-1','claimed','node-1',$1)`, now); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplySchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"flowbaton_devices", "flowbaton_sessions", "flowbaton_input_jobs", "flowbaton_frame_jobs"} {
		var ownerEpoch int64
		query := fmt.Sprintf("SELECT owner_worker_epoch FROM %s LIMIT 1", table)
		if err := store.Pool.QueryRow(ctx, query).Scan(&ownerEpoch); err != nil || ownerEpoch != 7 {
			t.Fatalf("%s owner epoch=%d err=%v", table, ownerEpoch, err)
		}
	}
	for _, table := range []string{"flowbaton_input_jobs", "flowbaton_frame_jobs"} {
		var claimedEpoch int64
		query := fmt.Sprintf("SELECT claimed_worker_epoch FROM %s LIMIT 1", table)
		if err := store.Pool.QueryRow(ctx, query).Scan(&claimedEpoch); err != nil || claimedEpoch != 7 {
			t.Fatalf("%s claimed epoch=%d err=%v", table, claimedEpoch, err)
		}
	}
}

// FLOWBATON_TEST_POSTGRES_URL must name a disposable PostgreSQL database. The
// test applies the schema and truncates only FlowBaton-owned tables.
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
	if err := store.ApplySchema(ctx); err != nil {
		t.Fatal(err)
	}
	_, err = store.Pool.Exec(ctx, `TRUNCATE flowbaton_frame_content,flowbaton_input_jobs,flowbaton_frame_jobs,flowbaton_idempotency,flowbaton_events,flowbaton_requests,flowbaton_devices,flowbaton_sessions,flowbaton_token_nonces,flowbaton_identity_mappings,flowbaton_nodes CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	leaseFor := 30 * time.Second
	node1, err := store.RegisterNode(ctx, "node-1", "https://node-1", leaseFor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RegisterNode(ctx, "node-1", "https://node-1-duplicate", leaseFor); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate live node registration error=%v", err)
	}
	node2, err := store.RegisterNode(ctx, "node-2", "https://node-2", leaseFor)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", node1, []string{"tap", "input-text"}); err != nil {
		t.Fatal(err)
	}
	var ready bool
	if err := store.Pool.QueryRow(ctx, `SELECT ready FROM flowbaton_nodes WHERE node_id=$1`, node1.NodeID).Scan(&ready); err != nil || ready {
		t.Fatalf("new node ready=%v err=%v", ready, err)
	}
	if err := store.ActivateNode(ctx, node1); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateNode(ctx, node2); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertIdentity(ctx, Identity{CertificateFingerprint: "cert-1", TenantID: "tenant-1", PrincipalID: "principal-1"}); err != nil {
		t.Fatal(err)
	}
	tokenWindow, err := store.ReserveTokenNonce(ctx, "cert-1", "nonce-1234567890", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !tokenWindow.IssuedAt.Equal(tokenWindow.IssuedAt.Truncate(time.Second)) || tokenWindow.ExpiresAt.Sub(tokenWindow.IssuedAt) != 5*time.Minute {
		t.Fatalf("token window=%#v", tokenWindow)
	}
	if _, err := store.ReserveTokenNonce(ctx, "cert-1", "nonce-1234567890", 5*time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonce replay error=%v", err)
	}
	acquire := AcquireInput{TenantID: "tenant-1", PrincipalID: "principal-1", AuthProfileID: "remote-cloud-mac-v1", ChannelBindingSHA256: "binding", RequestNonce: "nonce-1234567890", BindingExpiresAt: tokenWindow.ExpiresAt, ResourceID: "device-1", RequestedCapabilities: []string{"tap"}, IdempotencyKey: "acquire-key-0001", ReleaseIdempotencyKey: "release-key-0001", LeaseDuration: time.Minute, HeartbeatInterval: 15 * time.Second}
	first, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", node2, []string{"tap"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("live device ownership changed: %v", err)
	}
	replayResult, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if !replayResult.Replay || replayResult.Session.SessionID != first.Session.SessionID {
		t.Fatalf("replay=%#v", replayResult)
	}
	if _, err := store.ClaimFrame(ctx, node2, 10*time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner frame claim error=%v", err)
	}
	frame, err := store.ClaimFrame(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteFrame(ctx, frame, FrameData{Content: []byte("frame-1"), ContentType: "image/png", Orientation: "portrait", Width: 100, Height: 200}); err != nil {
		t.Fatal(err)
	}
	commandPayload := json.RawMessage(`{"x":12,"y":34}`)
	commandDigest := sha256.Sum256(commandPayload)
	inputPayload, _ := json.Marshal(inputEnvelope{LeaseID: first.Session.LeaseID, Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, BasedOnStreamEpoch: 1, BasedOnFrameSequence: 1, Command: "tap", PayloadSHA256: fmt.Sprintf("%x", commandDigest)})
	input := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding", RequestNonce: acquire.RequestNonce, BindingExpiresAt: acquire.BindingExpiresAt, RequestID: "request-input-0001", Type: "input", IdempotencyKey: "input-key-0000001", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: inputPayload, CommandPayload: commandPayload}
	queued, err := store.Apply(ctx, input)
	if err != nil || !queued.Queued {
		t.Fatalf("queue input=%#v err=%v", queued, err)
	}
	if _, err := store.ClaimInput(ctx, node2, 10*time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner input claim error=%v", err)
	}
	work, err := store.ClaimInput(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInput(ctx, work, "applied", 20*time.Millisecond, nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("claimed input completion error=%v", err)
	}
	if err := store.StartInput(ctx, work); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInput(ctx, node1, 10*time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("executing input was claimed twice: %v", err)
	}
	if err := store.CompleteInput(ctx, work, "applied", 20*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	inputReplay, err := store.Apply(ctx, input)
	if err != nil || !inputReplay.Replay || inputReplay.Event.Type != "input_ack" {
		t.Fatalf("input replay=%#v err=%v", inputReplay, err)
	}
	resumed, terminal, err := store.WaitEvents(ctx, "tenant-1", "principal-1", first.Session.SessionID, 2, 0)
	if err != nil || terminal || len(resumed) != 1 || resumed[0].Type != "input_ack" {
		t.Fatalf("resumed=%#v terminal=%v err=%v", resumed, terminal, err)
	}
	staleRefresh, err := store.ClaimFrame(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_frame_jobs SET claim_expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`, first.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE node_id=$1`, node1.NodeID); err != nil {
		t.Fatal(err)
	}
	previousNode1 := node1
	node1, err = store.RegisterNode(ctx, "node-1", "https://node-1-takeover", leaseFor)
	if err != nil || node1.WorkerEpoch != previousNode1.WorkerEpoch+1 {
		t.Fatalf("stale takeover lease=%#v err=%v", node1, err)
	}
	if err := store.HeartbeatNode(ctx, previousNode1, leaseFor); !errors.Is(err, ErrFenced) {
		t.Fatalf("old epoch heartbeat error=%v", err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", node1, []string{"tap", "input-text"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateNode(ctx, node1); err != nil {
		t.Fatal(err)
	}
	refresh, err := store.ClaimFrame(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if refresh.ClaimGeneration <= staleRefresh.ClaimGeneration {
		t.Fatalf("claim generation did not advance: old=%d new=%d", staleRefresh.ClaimGeneration, refresh.ClaimGeneration)
	}
	if err := store.CompleteFrame(ctx, staleRefresh, FrameData{Content: []byte("stale-frame"), ContentType: "image/png", Orientation: "portrait", Width: 100, Height: 200}); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale frame completion error=%v", err)
	}
	if err := store.CompleteFrame(ctx, refresh, FrameData{Content: []byte("frame-2"), ContentType: "image/png", Orientation: "portrait", Width: 100, Height: 200}); err != nil {
		t.Fatal(err)
	}
	secondPayload, _ := json.Marshal(inputEnvelope{LeaseID: first.Session.LeaseID, Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, BasedOnStreamEpoch: 1, BasedOnFrameSequence: 2, Command: "tap", PayloadSHA256: fmt.Sprintf("%x", commandDigest)})
	ambiguous := input
	ambiguous.RequestID = "request-input-0002"
	ambiguous.IdempotencyKey = "input-key-0000002"
	ambiguous.Payload = secondPayload
	if _, err := store.Apply(ctx, ambiguous); err != nil {
		t.Fatal(err)
	}
	staleInput, err := store.ClaimInput(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_input_jobs SET claim_expires_at=clock_timestamp()-interval '1 second'
		WHERE session_id=$1 AND request_sequence=$2`, staleInput.SessionID, staleInput.RequestSequence); err != nil {
		t.Fatal(err)
	}
	ambiguousWork, err := store.ClaimInput(ctx, node1, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if ambiguousWork.ClaimGeneration <= staleInput.ClaimGeneration {
		t.Fatalf("input claim generation did not advance: old=%d new=%d", staleInput.ClaimGeneration, ambiguousWork.ClaimGeneration)
	}
	if err := store.StartInput(ctx, staleInput); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale input claim start error=%v", err)
	}
	if err := store.StartInput(ctx, ambiguousWork); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverAmbiguousInputs(ctx, node2, time.Nanosecond); err != nil || recovered != 0 {
		t.Fatalf("non-owner recovered=%d err=%v", recovered, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE node_id=$1`, node1.NodeID); err != nil {
		t.Fatal(err)
	}
	previousNode1 = node1
	node1, err = store.RegisterNode(ctx, "node-1", "https://node-1", leaseFor)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", node1, []string{"tap", "input-text"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInput(ctx, previousNode1, 10*time.Second); !errors.Is(err, ErrFenced) {
		t.Fatalf("old epoch input claim error=%v", err)
	}
	if err := store.StartInput(ctx, ambiguousWork); !errors.Is(err, ErrFenced) {
		t.Fatalf("old epoch input start error=%v", err)
	}
	if err := store.CompleteInput(ctx, ambiguousWork, "applied", 20*time.Millisecond, nil); !errors.Is(err, ErrFenced) {
		t.Fatalf("old epoch input completion error=%v", err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_input_jobs SET started_at=clock_timestamp()-interval '1 second'
		WHERE session_id=$1 AND request_sequence=$2`, ambiguousWork.SessionID, ambiguousWork.RequestSequence); err != nil {
		t.Fatal(err)
	}
	if recovered, err := store.RecoverAmbiguousInputs(ctx, node1, time.Millisecond); err != nil || recovered != 1 {
		t.Fatalf("owner recovered=%d err=%v", recovered, err)
	}
	if _, err := store.ClaimInput(ctx, node1, 10*time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recovered executing input was replayed: %v", err)
	}
	if err := store.ActivateNode(ctx, node1); err != nil {
		t.Fatal(err)
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
	heartbeat := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding", RequestNonce: acquire.RequestNonce, BindingExpiresAt: acquire.BindingExpiresAt, RequestID: "request-heartbeat", Type: "heartbeat", IdempotencyKey: "heartbeat-key-01", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: heartbeatPayload, RequestedExtension: 10 * time.Second}
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
	if err := store.MarkDisconnected(ctx, "tenant-1", "principal-1", first.Session.SessionID, "binding", acquire.RequestNonce, acquire.BindingExpiresAt, first.Session.Generation, first.Session.FencingTokenSHA256, "transport_interrupted"); err != nil {
		t.Fatal(err)
	}
	reconnect := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding-2", RequestNonce: "nonce-1234567891", BindingExpiresAt: now.Add(6 * time.Minute), RequestID: "request-reconnect", Type: "reconnect", IdempotencyKey: "reconnect-key-001", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: json.RawMessage(`{}`), LastAcknowledgedEvent: 4}
	if _, err := store.Apply(ctx, reconnect); err != nil {
		t.Fatal(err)
	}
	release := MutationInput{SessionID: first.Session.SessionID, TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "binding-2", RequestNonce: reconnect.RequestNonce, BindingExpiresAt: reconnect.BindingExpiresAt, RequestID: "request-release", Type: "release", IdempotencyKey: "release-key-0001", Generation: first.Session.Generation, FencingTokenSHA256: first.Session.FencingTokenSHA256, Payload: json.RawMessage(`{}`)}
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
	second, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	if second.Session.Generation != first.Session.Generation+1 {
		t.Fatalf("generation=%d want=%d", second.Session.Generation, first.Session.Generation+1)
	}
	if err := store.DeactivateNode(ctx, node1); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatNode(ctx, node1, leaseFor); !errors.Is(err, ErrFenced) {
		t.Fatalf("deactivated node heartbeat error=%v", err)
	}
	if err := store.DeactivateNode(ctx, previousNode1); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale node deactivation error=%v", err)
	}
}
