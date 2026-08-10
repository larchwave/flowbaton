package sessionstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

type runtimeTestFixture struct {
	ctx     context.Context
	store   *Postgres
	node    NodeLease
	acquire AcquireInput
	session Session
}

func newRuntimeTestStore(t *testing.T) (context.Context, *Postgres) {
	t.Helper()
	databaseURL := os.Getenv("FLOWBATON_TEST_POSTGRES_URL")
	if databaseURL == "" {
		t.Skip("FLOWBATON_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if err := store.ApplySchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `TRUNCATE flowbaton_frame_content,flowbaton_input_jobs,
		flowbaton_frame_jobs,flowbaton_idempotency,flowbaton_events,flowbaton_requests,
		flowbaton_devices,flowbaton_sessions,flowbaton_token_nonces,
		flowbaton_identity_mappings,flowbaton_nodes CASCADE`); err != nil {
		t.Fatal(err)
	}
	return ctx, store
}

func newRuntimeTestFixture(t *testing.T) runtimeTestFixture {
	t.Helper()
	ctx, store := newRuntimeTestStore(t)
	node, err := store.RegisterNode(ctx, "node-1", "https://node-1", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-1", node, []string{"tap"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	acquire := AcquireInput{
		TenantID:              "tenant-1",
		PrincipalID:           "principal-1",
		AuthProfileID:         "remote-cloud-mac-v1",
		ChannelBindingSHA256:  "binding-1",
		RequestNonce:          "nonce-1234567890",
		BindingExpiresAt:      time.Now().UTC().Add(10 * time.Minute),
		ResourceID:            "device-1",
		RequestedCapabilities: []string{"tap"},
		IdempotencyKey:        "acquire-key-0001",
		ReleaseIdempotencyKey: "release-key-0001",
		LeaseDuration:         5 * time.Minute,
		HeartbeatInterval:     15 * time.Second,
	}
	result, err := store.Acquire(ctx, acquire)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeTestFixture{ctx: ctx, store: store, node: node, acquire: acquire, session: result.Session}
}

func (fixture runtimeTestFixture) completeFrame(t *testing.T, content []byte) (int64, string) {
	t.Helper()
	work, err := fixture.store.ClaimFrame(fixture.ctx, fixture.node, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteFrame(fixture.ctx, work, FrameData{
		Content: content, ContentType: "image/png", Orientation: "portrait", Width: 100, Height: 200,
	}); err != nil {
		t.Fatal(err)
	}
	var sequence int64
	var digest string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT
		(payload->>'frame_sequence')::bigint,payload->>'content_sha256'
		FROM flowbaton_events WHERE session_id=$1 AND type='frame'
		ORDER BY sequence DESC LIMIT 1`, fixture.session.SessionID).Scan(&sequence, &digest); err != nil {
		t.Fatal(err)
	}
	return sequence, digest
}

func (fixture runtimeTestFixture) queueInput(t *testing.T, suffix string, frameSequence int64) MutationInput {
	t.Helper()
	commandPayload := json.RawMessage(`{"x":12,"y":34}`)
	digest := sha256.Sum256(commandPayload)
	payload, err := json.Marshal(inputEnvelope{
		LeaseID:              fixture.session.LeaseID,
		Generation:           fixture.session.Generation,
		FencingTokenSHA256:   fixture.session.FencingTokenSHA256,
		BasedOnStreamEpoch:   fixture.session.StreamEpoch,
		BasedOnFrameSequence: frameSequence,
		Command:              "tap",
		PayloadSHA256:        fmt.Sprintf("%x", digest),
	})
	if err != nil {
		t.Fatal(err)
	}
	input := MutationInput{
		SessionID: fixture.session.SessionID, TenantID: fixture.session.TenantID,
		PrincipalID: fixture.session.PrincipalID, ChannelBindingSHA256: fixture.acquire.ChannelBindingSHA256,
		RequestNonce: fixture.acquire.RequestNonce, BindingExpiresAt: fixture.acquire.BindingExpiresAt,
		RequestID: "request-input-00000000-" + suffix, Type: "input", IdempotencyKey: "input-key-00000000-" + suffix,
		Generation: fixture.session.Generation, FencingTokenSHA256: fixture.session.FencingTokenSHA256,
		Payload: payload, CommandPayload: commandPayload,
	}
	result, err := fixture.store.Apply(fixture.ctx, input)
	if err != nil || !result.Queued {
		t.Fatalf("queue input: result=%#v error=%v", result, err)
	}
	return input
}

func (fixture runtimeTestFixture) mutation(kind, key string) MutationInput {
	return MutationInput{
		SessionID: fixture.session.SessionID, TenantID: fixture.session.TenantID,
		PrincipalID: fixture.session.PrincipalID, ChannelBindingSHA256: fixture.acquire.ChannelBindingSHA256,
		RequestNonce: fixture.acquire.RequestNonce, BindingExpiresAt: fixture.acquire.BindingExpiresAt,
		RequestID: "request-" + kind + "-00000001", Type: kind, IdempotencyKey: key,
		Generation: fixture.session.Generation, FencingTokenSHA256: fixture.session.FencingTokenSHA256,
		Payload: json.RawMessage(`{}`),
	}
}

func TestPostgresAcquireUsesDatabaseClock(t *testing.T) {
	ctx, store := newRuntimeTestStore(t)
	node, err := store.RegisterNode(ctx, "node-clock", "https://node-clock", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-clock", "device-clock", node, []string{"tap"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateNode(ctx, node); err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	result, err := store.Acquire(ctx, AcquireInput{
		TenantID: "tenant-clock", PrincipalID: "principal-clock", AuthProfileID: "profile-clock",
		ChannelBindingSHA256: "binding-clock", RequestNonce: "nonce-clock-123456",
		BindingExpiresAt: before.Add(5 * time.Minute), ResourceID: "device-clock",
		RequestedCapabilities: []string{"tap"}, IdempotencyKey: "acquire-clock-0001",
		ReleaseIdempotencyKey: "release-clock-0001", LeaseDuration: time.Minute,
		HeartbeatInterval: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var after time.Time
	if err := store.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if result.Session.AcquiredAt.Before(before) || result.Session.AcquiredAt.After(after) {
		t.Fatalf("acquired_at=%s outside database interval [%s,%s]", result.Session.AcquiredAt, before, after)
	}
}

func TestPostgresRebindKeepsUnlistedDeviceOnOldEpoch(t *testing.T) {
	ctx, store := newRuntimeTestStore(t)
	oldLease, err := store.RegisterNode(ctx, "node-1", "https://node-old", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for _, deviceID := range []string{"device-kept", "device-unlisted"} {
		if err := store.RegisterDevice(ctx, "tenant-1", deviceID, oldLease, []string{"tap"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE node_id=$1`, oldLease.NodeID); err != nil {
		t.Fatal(err)
	}
	newLease, err := store.RegisterNode(ctx, "node-1", "https://node-new", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterDevice(ctx, "tenant-1", "device-kept", newLease, []string{"tap"}); err != nil {
		t.Fatal(err)
	}
	if err := store.ActivateNode(ctx, newLease); err != nil {
		t.Fatal(err)
	}
	var keptEpoch, unlistedEpoch int64
	if err := store.Pool.QueryRow(ctx, `SELECT owner_worker_epoch FROM flowbaton_devices WHERE tenant_id='tenant-1' AND resource_id='device-kept'`).Scan(&keptEpoch); err != nil {
		t.Fatal(err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT owner_worker_epoch FROM flowbaton_devices WHERE tenant_id='tenant-1' AND resource_id='device-unlisted'`).Scan(&unlistedEpoch); err != nil {
		t.Fatal(err)
	}
	if keptEpoch != newLease.WorkerEpoch || unlistedEpoch != oldLease.WorkerEpoch {
		t.Fatalf("device epochs kept=%d unlisted=%d old=%d new=%d", keptEpoch, unlistedEpoch, oldLease.WorkerEpoch, newLease.WorkerEpoch)
	}
	input := AcquireInput{
		TenantID: "tenant-1", PrincipalID: "principal-1", AuthProfileID: "profile-1",
		ChannelBindingSHA256: "binding-1", RequestNonce: "nonce-1234567890",
		BindingExpiresAt: time.Now().Add(time.Minute), ResourceID: "device-unlisted",
		RequestedCapabilities: []string{"tap"}, IdempotencyKey: "acquire-unlisted",
		ReleaseIdempotencyKey: "release-unlisted", LeaseDuration: 30 * time.Second,
		HeartbeatInterval: 10 * time.Second,
	}
	if _, err := store.Acquire(ctx, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unlisted device acquire error=%v", err)
	}
}

func TestPostgresTakeoverWaitsForOldExecutionWindow(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	frameSequence, _ := fixture.completeFrame(t, []byte("frame-one"))
	fixture.queueInput(t, "0001", frameSequence)
	work, err := fixture.store.ClaimInput(fixture.ctx, fixture.node, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.StartInput(fixture.ctx, work); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE flowbaton_nodes SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE node_id=$1`, fixture.node.NodeID); err != nil {
		t.Fatal(err)
	}
	newLease, err := fixture.store.RegisterNode(fixture.ctx, fixture.node.NodeID, "https://node-new", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RegisterDevice(fixture.ctx, fixture.session.TenantID, fixture.session.ResourceID, newLease, []string{"tap"}); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(fixture.ctx, 50*time.Millisecond)
	defer cancel()
	if err := fixture.store.WaitForExecutionQuiescence(waitCtx, newLease, time.Second, 5*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("early quiescence error=%v", err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE flowbaton_input_jobs SET started_at=clock_timestamp()-interval '2 seconds' WHERE session_id=$1`, fixture.session.SessionID); err != nil {
		t.Fatal(err)
	}
	finishCtx, finishCancel := context.WithTimeout(fixture.ctx, time.Second)
	defer finishCancel()
	if err := fixture.store.WaitForExecutionQuiescence(finishCtx, newLease, time.Second, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM flowbaton_input_jobs WHERE session_id=$1`, fixture.session.SessionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "done" {
		t.Fatalf("recovered input state=%q", state)
	}
}

func TestPostgresReconnectRotatesChannelBinding(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	refreshedToken := fixture.mutation("heartbeat", "heartbeat-new-token")
	refreshedToken.RequestNonce = "nonce-1234567891"
	refreshedToken.BindingExpiresAt = fixture.acquire.BindingExpiresAt.Add(time.Minute)
	refreshedToken.RequestedExtension = time.Second
	if _, err := fixture.store.Apply(fixture.ctx, refreshedToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-channel refreshed token error=%v", err)
	}
	if err := fixture.store.MarkDisconnected(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, fixture.acquire.ChannelBindingSHA256, fixture.acquire.RequestNonce,
		fixture.acquire.BindingExpiresAt, fixture.session.Generation,
		fixture.session.FencingTokenSHA256, "transport_interrupted"); err != nil {
		t.Fatal(err)
	}
	reconnect := fixture.mutation("reconnect", "reconnect-key-0001")
	reconnect.ChannelBindingSHA256 = "binding-2"
	reconnect.RequestNonce = "nonce-1234567891"
	reconnect.BindingExpiresAt = time.Now().UTC().Add(5 * time.Minute)
	reconnect.LastAcknowledgedEvent = 1
	sameChannel := reconnect
	sameChannel.ChannelBindingSHA256 = fixture.acquire.ChannelBindingSHA256
	sameChannel.IdempotencyKey = "reconnect-key-same-channel"
	if _, err := fixture.store.Apply(fixture.ctx, sameChannel); !errors.Is(err, ErrNotFound) {
		t.Fatalf("same-channel reconnect error=%v", err)
	}
	sameNonce := reconnect
	sameNonce.RequestNonce = fixture.acquire.RequestNonce
	sameNonce.IdempotencyKey = "reconnect-key-same-nonce"
	if _, err := fixture.store.Apply(fixture.ctx, sameNonce); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unchanged-nonce reconnect error=%v", err)
	}
	sameExpiry := reconnect
	sameExpiry.BindingExpiresAt = fixture.acquire.BindingExpiresAt
	sameExpiry.IdempotencyKey = "reconnect-key-same-expiry"
	if _, err := fixture.store.Apply(fixture.ctx, sameExpiry); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unchanged-expiry reconnect error=%v", err)
	}
	if _, err := fixture.store.Apply(fixture.ctx, reconnect); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkDisconnected(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, fixture.acquire.ChannelBindingSHA256, fixture.acquire.RequestNonce,
		fixture.acquire.BindingExpiresAt, fixture.session.Generation,
		fixture.session.FencingTokenSHA256, "transport_interrupted"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old session identity disconnect error=%v", err)
	}
	oldBinding := fixture.mutation("heartbeat", "heartbeat-old-binding")
	oldBinding.RequestedExtension = time.Second
	if _, err := fixture.store.Apply(fixture.ctx, oldBinding); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old channel heartbeat error=%v", err)
	}
	newBinding := oldBinding
	newBinding.ChannelBindingSHA256 = reconnect.ChannelBindingSHA256
	newBinding.RequestNonce = reconnect.RequestNonce
	newBinding.BindingExpiresAt = reconnect.BindingExpiresAt
	newBinding.IdempotencyKey = "heartbeat-new-binding"
	if _, err := fixture.store.Apply(fixture.ctx, newBinding); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.ValidateSessionAccess(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, reconnect.ChannelBindingSHA256, reconnect.RequestNonce,
		reconnect.BindingExpiresAt, fixture.session.Generation, fixture.session.FencingTokenSHA256); err != nil {
		t.Fatalf("rotated session access error=%v", err)
	}
	if err := fixture.store.ValidateSessionAccess(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, reconnect.ChannelBindingSHA256, fixture.acquire.RequestNonce,
		reconnect.BindingExpiresAt, fixture.session.Generation, fixture.session.FencingTokenSHA256); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old nonce session access error=%v", err)
	}
}

func TestPostgresCancelStopsWatcherAndFinishesQueuedInput(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	frameSequence, _ := fixture.completeFrame(t, []byte("frame-one"))
	firstInput := fixture.queueInput(t, "0001", frameSequence)
	fixture.queueInput(t, "0002", frameSequence)
	work, err := fixture.store.ClaimInput(fixture.ctx, fixture.node, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.StartInput(fixture.ctx, work); err != nil {
		t.Fatal(err)
	}
	// The production watcher is bounded by the device-operation timeout. Give
	// the database transition enough room under the full race suite's load;
	// this check is about observing the durable cancellation, not sub-second
	// database latency.
	watchCtx, watchCancel := context.WithTimeout(fixture.ctx, 10*time.Second)
	defer watchCancel()
	watched := make(chan struct {
		active bool
		err    error
	}, 1)
	go func() {
		active, err := fixture.store.WaitInputActive(watchCtx, work, 5*time.Millisecond)
		watched <- struct {
			active bool
			err    error
		}{active: active, err: err}
	}()
	cancelInput := fixture.mutation("cancel", "cancel-key-00000001")
	cancelInput.Payload = json.RawMessage(`{"reason":"user_requested"}`)
	if _, err := fixture.store.Apply(fixture.ctx, cancelInput); err != nil {
		t.Fatal(err)
	}
	result := <-watched
	if result.err != nil || result.active {
		t.Fatalf("watch result active=%v error=%v", result.active, result.err)
	}
	rows, err := fixture.store.Pool.Query(fixture.ctx, `SELECT state FROM flowbaton_input_jobs WHERE session_id=$1 ORDER BY request_sequence`, fixture.session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var states []string
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			t.Fatal(err)
		}
		states = append(states, state)
	}
	if len(states) != 2 || states[0] != "executing" || states[1] != "done" {
		t.Fatalf("input states=%v", states)
	}
	if err := fixture.store.CompleteInput(fixture.ctx, work, "applied", time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	replayed, err := fixture.store.Apply(fixture.ctx, firstInput)
	if err != nil || !replayed.Replay || replayed.Event.Type != "error" {
		t.Fatalf("cancelled completion replay=%#v error=%v", replayed, err)
	}
	var failure struct {
		Code        string `json:"code"`
		Retryable   bool   `json:"retryable"`
		SafeMessage string `json:"safe_message"`
	}
	if err := json.Unmarshal(replayed.Event.Data, &failure); err != nil {
		t.Fatal(err)
	}
	if failure.Code != "DEVICE_UNAVAILABLE" || failure.Retryable || failure.SafeMessage != "device input outcome is unknown after session cancellation" {
		t.Fatalf("cancelled completion failure=%#v", failure)
	}
}

func TestPostgresReleasedSessionRejectsPriorReplays(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	frameSequence, _ := fixture.completeFrame(t, []byte("frame-one"))
	input := fixture.queueInput(t, "0001", frameSequence)
	work, err := fixture.store.ClaimInput(fixture.ctx, fixture.node, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.StartInput(fixture.ctx, work); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.CompleteInput(fixture.ctx, work, "applied", time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	heartbeat := fixture.mutation("heartbeat", "heartbeat-key-0001")
	heartbeat.RequestedExtension = time.Second
	if _, err := fixture.store.Apply(fixture.ctx, heartbeat); err != nil {
		t.Fatal(err)
	}
	cancelInput := fixture.mutation("cancel", "cancel-key-00000001")
	cancelInput.Payload = json.RawMessage(`{"reason":"user_requested"}`)
	if _, err := fixture.store.Apply(fixture.ctx, cancelInput); err != nil {
		t.Fatal(err)
	}
	release := fixture.mutation("release", fixture.acquire.ReleaseIdempotencyKey)
	if _, err := fixture.store.Apply(fixture.ctx, release); err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.store.Acquire(fixture.ctx, fixture.acquire); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("released acquire replay error=%v", err)
	}
	for name, mutation := range map[string]MutationInput{
		"input":     input,
		"heartbeat": heartbeat,
		"cancel":    cancelInput,
	} {
		if _, err := fixture.store.Apply(fixture.ctx, mutation); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("released %s replay error=%v", name, err)
		}
	}
	replayed, err := fixture.store.Apply(fixture.ctx, release)
	if err != nil || !replayed.Replay || replayed.Event.Type != "released" {
		t.Fatalf("release replay=%#v error=%v", replayed, err)
	}
	alteredRelease := release
	alteredRelease.Payload = json.RawMessage(`{"outcome":"different"}`)
	if _, err := fixture.store.Apply(fixture.ctx, alteredRelease); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered release replay error=%v", err)
	}
}

func TestPostgresFrameFailureBlocksAfterThirdAttempt(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	for attempt := int64(1); attempt <= 3; attempt++ {
		work, err := fixture.store.ClaimFrame(fixture.ctx, fixture.node, time.Second)
		if err != nil {
			t.Fatalf("attempt %d claim: %v", attempt, err)
		}
		if work.ClaimGeneration != attempt {
			t.Fatalf("attempt %d claim generation=%d", attempt, work.ClaimGeneration)
		}
		if err := fixture.store.FailFrame(fixture.ctx, work, "DEVICE_UNAVAILABLE", true, "capture failed"); err != nil {
			t.Fatalf("attempt %d fail: %v", attempt, err)
		}
	}
	if _, err := fixture.store.ClaimFrame(fixture.ctx, fixture.node, time.Second); !errors.Is(err, ErrNotFound) {
		t.Fatalf("blocked frame claim error=%v", err)
	}
	var state string
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT state FROM flowbaton_frame_jobs WHERE session_id=$1`, fixture.session.SessionID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "blocked" {
		t.Fatalf("frame job state=%q", state)
	}
}

func TestPostgresFrameContentRequiresExactSessionCoordinates(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	frameSequence, digest := fixture.completeFrame(t, []byte("frame-one"))
	exact := FrameContentRequest{
		SessionID: fixture.session.SessionID, TenantID: fixture.session.TenantID,
		PrincipalID: fixture.session.PrincipalID, ChannelBindingSHA256: fixture.acquire.ChannelBindingSHA256,
		RequestNonce: fixture.acquire.RequestNonce, BindingExpiresAt: fixture.acquire.BindingExpiresAt,
		Generation: fixture.session.Generation, FencingTokenSHA256: fixture.session.FencingTokenSHA256,
		StreamEpoch: fixture.session.StreamEpoch, FrameSequence: frameSequence, ContentSHA256: digest,
	}
	content, err := fixture.store.FrameContent(fixture.ctx, exact)
	if err != nil || string(content.Content) != "frame-one" || content.ContentType != "image/png" {
		t.Fatalf("exact frame content=%#v error=%v", content, err)
	}
	cases := []struct {
		name   string
		change func(*FrameContentRequest)
	}{
		{name: "tenant", change: func(input *FrameContentRequest) { input.TenantID = "tenant-2" }},
		{name: "principal", change: func(input *FrameContentRequest) { input.PrincipalID = "principal-2" }},
		{name: "channel", change: func(input *FrameContentRequest) { input.ChannelBindingSHA256 = "binding-2" }},
		{name: "nonce", change: func(input *FrameContentRequest) { input.RequestNonce = "nonce-1234567891" }},
		{name: "binding expiry", change: func(input *FrameContentRequest) { input.BindingExpiresAt = input.BindingExpiresAt.Add(time.Second) }},
		{name: "generation", change: func(input *FrameContentRequest) { input.Generation++ }},
		{name: "fence", change: func(input *FrameContentRequest) { input.FencingTokenSHA256 = "wrong-fence" }},
		{name: "epoch", change: func(input *FrameContentRequest) { input.StreamEpoch++ }},
		{name: "sequence", change: func(input *FrameContentRequest) { input.FrameSequence++ }},
		{name: "digest", change: func(input *FrameContentRequest) { input.ContentSHA256 = fmt.Sprintf("%064x", 1) }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := exact
			testCase.change(&input)
			if _, err := fixture.store.FrameContent(fixture.ctx, input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("FrameContent() error=%v", err)
			}
		})
	}
}

func TestPostgresNewFrameRemovesOldFrameBytes(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	firstSequence, firstDigest := fixture.completeFrame(t, []byte("frame-one"))
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,owner_worker_epoch,created_at) VALUES($1,$2,$3,clock_timestamp())`, fixture.session.SessionID, fixture.node.NodeID, fixture.node.WorkerEpoch); err != nil {
		t.Fatal(err)
	}
	secondSequence, secondDigest := fixture.completeFrame(t, []byte("frame-two"))
	request := FrameContentRequest{
		SessionID: fixture.session.SessionID, TenantID: fixture.session.TenantID,
		PrincipalID: fixture.session.PrincipalID, ChannelBindingSHA256: fixture.acquire.ChannelBindingSHA256,
		RequestNonce: fixture.acquire.RequestNonce, BindingExpiresAt: fixture.acquire.BindingExpiresAt,
		Generation: fixture.session.Generation, FencingTokenSHA256: fixture.session.FencingTokenSHA256,
		StreamEpoch: fixture.session.StreamEpoch, FrameSequence: firstSequence, ContentSHA256: firstDigest,
	}
	if _, err := fixture.store.FrameContent(fixture.ctx, request); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old frame content error=%v", err)
	}
	request.FrameSequence = secondSequence
	request.ContentSHA256 = secondDigest
	content, err := fixture.store.FrameContent(fixture.ctx, request)
	if err != nil || string(content.Content) != "frame-two" {
		t.Fatalf("new frame content=%q error=%v", content.Content, err)
	}
}

func TestPostgresExpiryReleasesSessionAndRemovesRuntimeData(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	frameSequence, _ := fixture.completeFrame(t, []byte("frame-one"))
	fixture.queueInput(t, "0001", frameSequence)
	fixture.queueInput(t, "0002", frameSequence)
	work, err := fixture.store.ClaimInput(fixture.ctx, fixture.node, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.StartInput(fixture.ctx, work); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE flowbaton_sessions SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE session_id=$1`, fixture.session.SessionID); err != nil {
		t.Fatal(err)
	}
	expired, err := fixture.store.ExpireSessions(fixture.ctx, 1)
	if err != nil || expired != 1 {
		t.Fatalf("ExpireSessions() count=%d error=%v", expired, err)
	}
	var status string
	var contentCount, frameJobCount, unfinishedInputCount int64
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT status FROM flowbaton_sessions WHERE session_id=$1`, fixture.session.SessionID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM flowbaton_frame_content WHERE session_id=$1`, fixture.session.SessionID).Scan(&contentCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM flowbaton_frame_jobs WHERE session_id=$1`, fixture.session.SessionID).Scan(&frameJobCount); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT COUNT(*) FROM flowbaton_input_jobs WHERE session_id=$1 AND state!='done'`, fixture.session.SessionID).Scan(&unfinishedInputCount); err != nil {
		t.Fatal(err)
	}
	if status != "released" || contentCount != 0 || frameJobCount != 0 || unfinishedInputCount != 0 {
		t.Fatalf("status=%q content=%d frame_jobs=%d unfinished_inputs=%d", status, contentCount, frameJobCount, unfinishedInputCount)
	}
	var releasePayload json.RawMessage
	if err := fixture.store.Pool.QueryRow(fixture.ctx, `SELECT payload FROM flowbaton_requests
		WHERE session_id=$1 AND type='release' ORDER BY sequence DESC LIMIT 1`, fixture.session.SessionID).Scan(&releasePayload); err != nil {
		t.Fatal(err)
	}
	release := fixture.mutation("release", fixture.acquire.ReleaseIdempotencyKey)
	release.Payload = releasePayload
	replayed, err := fixture.store.Apply(fixture.ctx, release)
	if err != nil || !replayed.Replay || replayed.Event.Type != "released" {
		t.Fatalf("expired release replay=%#v error=%v", replayed, err)
	}
	var alteredPayload map[string]any
	if err := json.Unmarshal(releasePayload, &alteredPayload); err != nil {
		t.Fatal(err)
	}
	alteredPayload["server_reason"] = "different"
	release.Payload, err = json.Marshal(alteredPayload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.Apply(fixture.ctx, release); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered expired release replay error=%v", err)
	}
	events, terminal, err := fixture.store.WaitEvents(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID, fixture.session.SessionID, 0, 0)
	if err != nil || !terminal || len(events) == 0 || events[len(events)-1].Type != "released" {
		t.Fatalf("terminal=%v events=%v error=%v", terminal, events, err)
	}
	active, err := fixture.store.WaitInputActive(fixture.ctx, work, time.Millisecond)
	if err != nil || active {
		t.Fatalf("expired input active=%v error=%v", active, err)
	}
}

func TestPostgresControlsRemainAvailableAfterManyEvents(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `INSERT INTO flowbaton_events(
		session_id,sequence,event_id,type,lease_generation,fencing_token_sha256,payload,created_at)
		SELECT $1,n,'event-seed-'||n,'heartbeat',$2,$3,'{}'::jsonb,
		clock_timestamp()+(n||' microseconds')::interval FROM generate_series(2,300) n`,
		fixture.session.SessionID, fixture.session.Generation, fixture.session.FencingTokenSHA256); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkDisconnected(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, fixture.acquire.ChannelBindingSHA256, fixture.acquire.RequestNonce,
		fixture.acquire.BindingExpiresAt, fixture.session.Generation,
		fixture.session.FencingTokenSHA256, "transport_interrupted"); err != nil {
		t.Fatal(err)
	}
	reconnect := fixture.mutation("reconnect", "reconnect-key-many-events")
	reconnect.ChannelBindingSHA256 = "binding-2"
	reconnect.RequestNonce = "nonce-1234567891"
	reconnect.BindingExpiresAt = time.Now().UTC().Add(5 * time.Minute)
	reconnect.LastAcknowledgedEvent = 300
	if _, err := fixture.store.Apply(fixture.ctx, reconnect); err != nil {
		t.Fatal(err)
	}
	cancelInput := fixture.mutation("cancel", "cancel-key-many-events")
	cancelInput.ChannelBindingSHA256 = reconnect.ChannelBindingSHA256
	cancelInput.RequestNonce = reconnect.RequestNonce
	cancelInput.BindingExpiresAt = reconnect.BindingExpiresAt
	cancelInput.Payload = json.RawMessage(`{"reason":"user_requested"}`)
	if _, err := fixture.store.Apply(fixture.ctx, cancelInput); err != nil {
		t.Fatal(err)
	}
	release := cancelInput
	release.Type = "release"
	release.RequestID = "request-release"
	release.IdempotencyKey = fixture.session.ReleaseIdempotencyKey
	release.Payload = json.RawMessage(`{}`)
	result, err := fixture.store.Apply(fixture.ctx, release)
	if err != nil || result.Event.Type != "released" {
		t.Fatalf("release event=%#v error=%v", result.Event, err)
	}
}

func TestPostgresEventTimesIncreaseWithSequence(t *testing.T) {
	fixture := newRuntimeTestFixture(t)
	if _, err := fixture.store.Pool.Exec(fixture.ctx, `UPDATE flowbaton_events SET created_at=clock_timestamp()+interval '1 second' WHERE session_id=$1 AND sequence=1`, fixture.session.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.MarkDisconnected(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID,
		fixture.session.SessionID, fixture.acquire.ChannelBindingSHA256, fixture.acquire.RequestNonce,
		fixture.acquire.BindingExpiresAt, fixture.session.Generation,
		fixture.session.FencingTokenSHA256, "transport_interrupted"); err != nil {
		t.Fatal(err)
	}
	events, err := fixture.store.Events(fixture.ctx, fixture.session.TenantID, fixture.session.PrincipalID, fixture.session.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("event count=%d", len(events))
	}
	first, err := time.Parse(time.RFC3339Nano, events[0].Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	second, err := time.Parse(time.RFC3339Nano, events[1].Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if !second.After(first) {
		t.Fatalf("event times first=%s second=%s", first, second)
	}
}

func TestPostgresTokenNonceRejectsReplayLengthAndQuota(t *testing.T) {
	ctx, store := newRuntimeTestStore(t)
	for _, fingerprint := range []string{"cert-1", "cert-2", "cert-quota"} {
		if err := store.UpsertIdentity(ctx, Identity{
			CertificateFingerprint: fingerprint, TenantID: "tenant-1", PrincipalID: "principal-1",
		}); err != nil {
			t.Fatal(err)
		}
	}
	before := time.Now().UTC().Add(-time.Second)
	window, err := store.ReserveTokenNonce(ctx, "cert-1", "nonce-0000000000", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)
	if window.IssuedAt.Before(before) || window.IssuedAt.After(after) || !window.IssuedAt.Equal(window.IssuedAt.Truncate(time.Second)) || window.ExpiresAt.Sub(window.IssuedAt) != time.Minute {
		t.Fatalf("database token window=%#v local bounds=[%s,%s]", window, before, after)
	}
	if _, err := store.ReserveTokenNonce(ctx, "cert-1", "nonce-0000000000", time.Minute); !errors.Is(err, ErrConflict) {
		t.Fatalf("nonce replay error=%v", err)
	}
	if _, err := store.ReserveTokenNonce(ctx, "cert-2", string(make([]byte, maxTokenNonceLength+1)), time.Minute); err == nil {
		t.Fatal("oversized nonce was accepted")
	}
	for _, ttl := range []time.Duration{-time.Second, time.Millisecond, maxTokenTTL + time.Second} {
		if _, err := store.ReserveTokenNonce(ctx, "cert-2", "nonce-invalid-ttl", ttl); err == nil {
			t.Fatalf("TTL %s was accepted", ttl)
		}
	}
	for index := 0; index < maxLiveNoncesPerIdentity; index++ {
		nonce := fmt.Sprintf("nonce-quota-%04d", index)
		if _, err := store.ReserveTokenNonce(ctx, "cert-quota", nonce, time.Minute); err != nil {
			t.Fatalf("nonce %d error=%v", index, err)
		}
	}
	if _, err := store.ReserveTokenNonce(ctx, "cert-quota", "nonce-quota-over", time.Minute); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("nonce quota error=%v", err)
	}
}
