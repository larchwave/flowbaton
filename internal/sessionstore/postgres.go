package sessionstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

const schemaLockID int64 = 0x464c4f574241544f

type Postgres struct{ Pool *pgxpool.Pool }

const maxControlEvents = 256

func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open FlowBaton PostgreSQL: %w", err)
	}
	store := &Postgres{Pool: pool}
	if err := store.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (store *Postgres) Close() {
	if store != nil && store.Pool != nil {
		store.Pool.Close()
	}
}

func (store *Postgres) Ping(ctx context.Context) error {
	if store == nil || store.Pool == nil {
		return errors.New("PostgreSQL store is not configured")
	}
	return store.Pool.Ping(ctx)
}

func (store *Postgres) ApplySchema(ctx context.Context) error {
	connection, err := store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaLockID); err != nil {
		return err
	}
	defer connection.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", schemaLockID) //nolint:errcheck
	entries, err := schemaFiles.ReadDir("schema")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		version := entry.Name()
		var applied bool
		err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM flowbaton_schema_versions WHERE version=$1)", version).Scan(&applied)
		if err != nil && !isUndefinedTable(err) {
			return err
		}
		if applied {
			continue
		}
		sql, err := schemaFiles.ReadFile("schema/" + version)
		if err != nil {
			return err
		}
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		foreignKeyTarget := []byte{82, 69, 70, 69, 82, 69, 78, 67, 69, 83}
		sql = bytes.ReplaceAll(sql, []byte("__FK_TARGET__"), foreignKeyTarget)
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO flowbaton_schema_versions(version) VALUES($1) ON CONFLICT DO NOTHING", version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply schema version %s: %w", version, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func isUndefinedTable(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "42P01"
}

func (store *Postgres) RegisterNode(ctx context.Context, nodeID, address string, at time.Time) error {
	_, err := store.Pool.Exec(ctx, `INSERT INTO flowbaton_nodes(node_id,public_address,last_heartbeat_at) VALUES($1,$2,$3)
		ON CONFLICT(node_id) DO UPDATE SET public_address=excluded.public_address,last_heartbeat_at=excluded.last_heartbeat_at`, nodeID, address, at)
	return err
}

func (store *Postgres) HeartbeatNode(ctx context.Context, nodeID string, at time.Time) error {
	command, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET last_heartbeat_at=$2 WHERE node_id=$1`, nodeID, at.UTC())
	if err == nil && command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (store *Postgres) RegisterDevice(ctx context.Context, tenantID, resourceID, nodeID string, capabilities []string) error {
	encoded, _ := json.Marshal(capabilities)
	command, err := store.Pool.Exec(ctx, `INSERT INTO flowbaton_devices(tenant_id,resource_id,owner_node_id,capabilities) VALUES($1,$2,$3,$4)
		ON CONFLICT(tenant_id,resource_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,capabilities=excluded.capabilities,updated_at=clock_timestamp()
		WHERE flowbaton_devices.current_session_id IS NULL OR flowbaton_devices.owner_node_id=excluded.owner_node_id`, tenantID, resourceID, nullable(nodeID), encoded)
	if err == nil && command.RowsAffected() != 1 {
		return ErrBusy
	}
	return err
}

func (store *Postgres) UpsertIdentity(ctx context.Context, identity Identity) error {
	if identity.CertificateFingerprint == "" || identity.TenantID == "" || identity.PrincipalID == "" {
		return errors.New("identity mapping is incomplete")
	}
	_, err := store.Pool.Exec(ctx, `INSERT INTO flowbaton_identity_mappings(certificate_fingerprint_sha256,tenant_id,principal_id,revoked_at)
		VALUES($1,$2,$3,NULL) ON CONFLICT(certificate_fingerprint_sha256) DO UPDATE SET tenant_id=excluded.tenant_id,principal_id=excluded.principal_id,revoked_at=NULL`, identity.CertificateFingerprint, identity.TenantID, identity.PrincipalID)
	return err
}

func (store *Postgres) RevokeIdentity(ctx context.Context, fingerprint string, at time.Time) error {
	command, err := store.Pool.Exec(ctx, `UPDATE flowbaton_identity_mappings SET revoked_at=$2 WHERE certificate_fingerprint_sha256=$1`, fingerprint, at)
	if err == nil && command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (store *Postgres) ResolveIdentity(ctx context.Context, fingerprint string) (Identity, error) {
	var identity Identity
	var revokedAt *time.Time
	err := store.Pool.QueryRow(ctx, `SELECT certificate_fingerprint_sha256,tenant_id,principal_id,revoked_at FROM flowbaton_identity_mappings WHERE certificate_fingerprint_sha256=$1`, fingerprint).Scan(&identity.CertificateFingerprint, &identity.TenantID, &identity.PrincipalID, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrNotFound
	}
	if err != nil {
		return Identity{}, err
	}
	if revokedAt != nil {
		identity.RevokedAt = revokedAt
		return Identity{}, ErrIdentityRevoked
	}
	return identity, nil
}

func (store *Postgres) ListIdentities(ctx context.Context) ([]Identity, error) {
	rows, err := store.Pool.Query(ctx, `SELECT certificate_fingerprint_sha256,tenant_id,principal_id,revoked_at FROM flowbaton_identity_mappings ORDER BY tenant_id,principal_id,certificate_fingerprint_sha256`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var identities []Identity
	for rows.Next() {
		var identity Identity
		var revokedAt *time.Time
		if err := rows.Scan(&identity.CertificateFingerprint, &identity.TenantID, &identity.PrincipalID, &revokedAt); err != nil {
			return nil, err
		}
		if revokedAt != nil {
			identity.RevokedAt = revokedAt
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

func (store *Postgres) ConsumeTokenNonce(ctx context.Context, fingerprint, nonce string, expiresAt time.Time) error {
	if fingerprint == "" || nonce == "" || expiresAt.IsZero() {
		return errors.New("token nonce is incomplete")
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_token_nonces WHERE expires_at < clock_timestamp()`); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `INSERT INTO flowbaton_token_nonces(certificate_fingerprint_sha256,nonce,expires_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, fingerprint, nonce, expiresAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}

func (store *Postgres) Acquire(ctx context.Context, input AcquireInput) (Result, error) {
	if err := validateAcquire(input); err != nil {
		return Result{}, err
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if !now.Before(input.BindingExpiresAt) {
		return Result{}, ErrExpired
	}
	hash := requestHash("acquire", input.ResourceID, input.RequestedCapabilities, input.ReleaseIdempotencyKey)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, requestHash(input.TenantID, input.PrincipalID, input.IdempotencyKey)); err != nil {
		return Result{}, err
	}
	if result, found, err := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash); err != nil || found {
		return result, err
	}
	var capabilitiesJSON []byte
	var generation int64
	var currentSession *string
	var ownerNodeID *string
	err = tx.QueryRow(ctx, `SELECT d.capabilities,d.lease_generation,d.current_session_id,d.owner_node_id FROM flowbaton_devices d JOIN flowbaton_nodes n ON n.node_id=d.owner_node_id WHERE d.tenant_id=$1 AND d.resource_id=$2 AND n.last_heartbeat_at>$3::timestamptz-interval '30 seconds' FOR UPDATE OF d`, input.TenantID, input.ResourceID, now).Scan(&capabilitiesJSON, &generation, &currentSession, &ownerNodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	if currentSession != nil {
		var status string
		var expires time.Time
		if err := tx.QueryRow(ctx, `SELECT status,lease_expires_at FROM flowbaton_sessions WHERE session_id=$1 FOR UPDATE`, *currentSession).Scan(&status, &expires); err != nil {
			return Result{}, err
		}
		if status != "released" && !expires.Before(now) {
			return Result{}, ErrBusy
		}
	}
	var available []string
	if err := json.Unmarshal(capabilitiesJSON, &available); err != nil {
		return Result{}, err
	}
	if !containsAll(available, input.RequestedCapabilities) {
		return Result{}, ErrNotFound
	}
	generation++
	sessionID, err := secureID("session")
	if err != nil {
		return Result{}, err
	}
	leaseID, err := secureID("lease")
	if err != nil {
		return Result{}, err
	}
	eventID, err := secureID("event")
	if err != nil {
		return Result{}, err
	}
	fence, err := secureDigest()
	if err != nil {
		return Result{}, err
	}
	requestID, err := secureID("request")
	if err != nil {
		return Result{}, err
	}
	leaseExpires := now.Add(input.LeaseDuration)
	if leaseExpires.After(input.BindingExpiresAt) {
		leaseExpires = input.BindingExpiresAt
	}
	if ownerNodeID == nil || *ownerNodeID == "" {
		return Result{}, ErrInvalidState
	}
	session := Session{SessionID: sessionID, TenantID: input.TenantID, PrincipalID: input.PrincipalID, AuthProfileID: input.AuthProfileID, ChannelBindingSHA256: input.ChannelBindingSHA256, RequestNonce: input.RequestNonce, BindingExpiresAt: input.BindingExpiresAt, ResourceID: input.ResourceID, OwnerNodeID: *ownerNodeID, LeaseID: leaseID, Generation: generation, FencingTokenSHA256: fence, ReleaseIdempotencyKey: input.ReleaseIdempotencyKey, Capabilities: append([]string(nil), input.RequestedCapabilities...), Status: "active", StreamEpoch: 1, AcquiredAt: now, LeaseExpiresAt: leaseExpires, HeartbeatInterval: input.HeartbeatInterval}
	requested, _ := json.Marshal(input.RequestedCapabilities)
	_, err = tx.Exec(ctx, `INSERT INTO flowbaton_sessions(session_id,tenant_id,principal_id,auth_profile_id,channel_binding_sha256,request_nonce,binding_expires_at,resource_id,owner_node_id,lease_id,lease_generation,fencing_token_sha256,release_idempotency_key,capabilities,status,acquired_at,lease_expires_at,heartbeat_interval_ms)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,(SELECT owner_node_id FROM flowbaton_devices WHERE tenant_id=$2 AND resource_id=$8),$9,$10,$11,$12,$13,'active',$14,$15,$16)`, sessionID, input.TenantID, input.PrincipalID, input.AuthProfileID, input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt, input.ResourceID, leaseID, generation, fence, input.ReleaseIdempotencyKey, requested, now, leaseExpires, input.HeartbeatInterval.Milliseconds())
	if err != nil {
		return Result{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE flowbaton_devices SET lease_generation=$3,current_session_id=$4,updated_at=$5 WHERE tenant_id=$1 AND resource_id=$2`, input.TenantID, input.ResourceID, generation, sessionID, now)
	if err != nil {
		return Result{}, err
	}
	acquirePayload, _ := json.Marshal(map[string]any{"resource_selector": input.ResourceID, "requested_capabilities": input.RequestedCapabilities})
	_, err = tx.Exec(ctx, `INSERT INTO flowbaton_requests(session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,channel_binding_sha256,payload,created_at) VALUES($1,1,$2,'acquire',$3,$4,$5,$6,$7,$8)`, sessionID, requestID, input.IdempotencyKey, input.TenantID, input.PrincipalID, input.ChannelBindingSHA256, acquirePayload, now)
	if err != nil {
		return Result{}, err
	}
	eventPayload := map[string]any{"lease_id": leaseID, "resource_id": input.ResourceID, "tenant_id": input.TenantID, "owner_principal_id": input.PrincipalID, "generation": generation}
	event, err := persistEvent(ctx, tx, session, 1, eventID, "acquired", eventPayload, now)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence) VALUES($1,$2,$3,$4,$5,1)`, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash, sessionID); err != nil {
		return Result{}, err
	}
	if session.OwnerNodeID == "" {
		return Result{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,created_at) VALUES($1,$2,$3)`, sessionID, session.OwnerNodeID, now); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('flowbaton_work',$1)`, session.OwnerNodeID); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{Session: session, Event: event}, nil
}

func (store *Postgres) Apply(ctx context.Context, input MutationInput) (Result, error) {
	if err := validateMutation(input); err != nil {
		return Result{}, err
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hash := requestHash(input.Type, input.SessionID, input.Generation, input.FencingTokenSHA256, json.RawMessage(input.Payload), json.RawMessage(input.CommandPayload), input.RequestedExtension, input.LastAcknowledgedEvent)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, requestHash(input.TenantID, input.PrincipalID, input.IdempotencyKey)); err != nil {
		return Result{}, err
	}
	if result, found, err := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash); err != nil || found {
		return result, err
	}
	session, err := loadSessionForUpdate(ctx, tx, input.TenantID, input.SessionID)
	if err != nil {
		return Result{}, err
	}
	if session.PrincipalID != input.PrincipalID || session.ChannelBindingSHA256 != input.ChannelBindingSHA256 {
		return Result{}, ErrNotFound
	}
	if session.Generation != input.Generation || session.FencingTokenSHA256 != input.FencingTokenSHA256 {
		return Result{}, ErrFenced
	}
	if !now.Before(session.BindingExpiresAt) || !now.Before(session.LeaseExpiresAt) {
		return Result{}, ErrExpired
	}
	if input.Type == "input" {
		return store.enqueueInput(ctx, tx, session, input, hash, now)
	}
	var requestSequence, eventSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_requests WHERE session_id=$1`, input.SessionID).Scan(&requestSequence); err != nil {
		return Result{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, input.SessionID).Scan(&eventSequence); err != nil {
		return Result{}, err
	}
	if eventSequence > maxControlEvents {
		return Result{}, ErrInvalidState
	}
	requestID := input.RequestID
	if requestID == "" {
		requestID, err = secureID("request")
		if err != nil {
			return Result{}, err
		}
	}
	payload := input.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_requests(session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,channel_binding_sha256,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.SessionID, requestSequence, requestID, input.Type, input.IdempotencyKey, input.TenantID, input.PrincipalID, input.ChannelBindingSHA256, payload, now); err != nil {
		return Result{}, err
	}
	eventType := ""
	eventPayload := map[string]any{}
	switch input.Type {
	case "heartbeat":
		if session.Status != "active" || input.RequestedExtension <= 0 {
			return Result{}, ErrInvalidState
		}
		session.LeaseExpiresAt = session.LeaseExpiresAt.Add(input.RequestedExtension)
		if session.LeaseExpiresAt.After(session.BindingExpiresAt) {
			session.LeaseExpiresAt = session.BindingExpiresAt
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET lease_expires_at=$2 WHERE session_id=$1`, session.SessionID, session.LeaseExpiresAt); err != nil {
			return Result{}, err
		}
		eventType = "heartbeat"
		eventPayload = map[string]any{"request_id": requestID, "idempotency_key": input.IdempotencyKey, "lease_expires_at": session.LeaseExpiresAt.Format(time.RFC3339Nano), "generation": session.Generation}
	case "cancel":
		if session.Status != "active" && session.Status != "disconnected" {
			return Result{}, ErrInvalidState
		}
		if pending, err := pendingInputCount(ctx, tx, session.SessionID); err != nil {
			return Result{}, err
		} else if pending != 0 {
			return Result{}, ErrInvalidState
		}
		reason := payloadString(payload, "reason", "user_requested")
		if !validCancelReason(reason) {
			return Result{}, ErrInvalidState
		}
		session.Status = "cancelled"
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='cancelled' WHERE session_id=$1`, session.SessionID); err != nil {
			return Result{}, err
		}
		eventType = "cancelled"
		eventPayload = map[string]any{"request_id": requestID, "idempotency_key": input.IdempotencyKey, "reason": reason, "terminal_outcome": "cancelled"}
	case "reconnect":
		if session.Status != "disconnected" {
			return Result{}, ErrInvalidState
		}
		if input.LastAcknowledgedEvent < 1 || input.LastAcknowledgedEvent >= eventSequence {
			return Result{}, ErrInvalidState
		}
		session.Status, session.StreamEpoch = "active", session.StreamEpoch+1
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='active',stream_epoch=$2 WHERE session_id=$1`, session.SessionID, session.StreamEpoch); err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,created_at) VALUES($1,$2,$3) ON CONFLICT(session_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,state='pending',claimed_by=NULL,claim_expires_at=NULL,created_at=excluded.created_at`, session.SessionID, session.OwnerNodeID, now); err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify('flowbaton_work',$1)`, session.OwnerNodeID); err != nil {
			return Result{}, err
		}
		eventType = "reconnected"
		eventPayload = map[string]any{"request_id": requestID, "idempotency_key": input.IdempotencyKey, "resume_from_sequence": input.LastAcknowledgedEvent, "stream_epoch": session.StreamEpoch, "generation": session.Generation}
	case "release":
		if session.Status != "active" && session.Status != "disconnected" && session.Status != "cancelled" {
			return Result{}, ErrInvalidState
		}
		if input.IdempotencyKey != session.ReleaseIdempotencyKey {
			return Result{}, ErrConflict
		}
		if pending, err := pendingInputCount(ctx, tx, session.SessionID); err != nil {
			return Result{}, err
		} else if pending != 0 {
			return Result{}, ErrInvalidState
		}
		outcome := "completed"
		if session.Status == "cancelled" {
			outcome = "cancelled"
		}
		session.Status = "released"
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='released',released_at=$2 WHERE session_id=$1`, session.SessionID, now); err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_devices SET current_session_id=NULL,updated_at=$3 WHERE tenant_id=$1 AND resource_id=$2 AND current_session_id=$4`, session.TenantID, session.ResourceID, now, session.SessionID); err != nil {
			return Result{}, err
		}
		eventType = "released"
		eventPayload = map[string]any{"release_idempotency_key": input.IdempotencyKey, "outcome": outcome, "generation": session.Generation}
	default:
		return Result{}, ErrInvalidState
	}
	eventID, err := secureID("event")
	if err != nil {
		return Result{}, err
	}
	event, err := persistEvent(ctx, tx, session, eventSequence, eventID, eventType, eventPayload, now)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence) VALUES($1,$2,$3,$4,$5,$6)`, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash, input.SessionID, eventSequence); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{Session: session, Event: event}, nil
}

func pendingInputCount(ctx context.Context, tx pgx.Tx, sessionID string) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM flowbaton_input_jobs WHERE session_id=$1 AND state!='done'`, sessionID).Scan(&count)
	return count, err
}

type inputEnvelope struct {
	LeaseID              string `json:"lease_id"`
	Generation           int64  `json:"generation"`
	FencingTokenSHA256   string `json:"fencing_token_sha256"`
	BasedOnStreamEpoch   int64  `json:"based_on_stream_epoch"`
	BasedOnFrameSequence int64  `json:"based_on_frame_sequence"`
	Command              string `json:"command"`
	PayloadSHA256        string `json:"payload_sha256"`
}

func (store *Postgres) enqueueInput(ctx context.Context, tx pgx.Tx, session Session, input MutationInput, hash string, now time.Time) (Result, error) {
	if session.Status != "active" || len(input.CommandPayload) == 0 {
		return Result{}, ErrInvalidState
	}
	var envelope inputEnvelope
	if err := decodeStrictJSON(input.Payload, &envelope); err != nil {
		return Result{}, fmt.Errorf("%w: input envelope: %v", ErrInvalidArgument, err)
	}
	if envelope.LeaseID != session.LeaseID || envelope.Generation != session.Generation || envelope.FencingTokenSHA256 != session.FencingTokenSHA256 || envelope.BasedOnStreamEpoch != session.StreamEpoch || envelope.BasedOnFrameSequence < 1 || !validInputCommand(envelope.Command) {
		return Result{}, ErrFenced
	}
	digest := sha256.Sum256(input.CommandPayload)
	if envelope.PayloadSHA256 != hex.EncodeToString(digest[:]) {
		return Result{}, ErrConflict
	}
	if err := validateCommandPayload(envelope.Command, input.CommandPayload); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	var latestEpoch, latestFrame int64
	err := tx.QueryRow(ctx, `SELECT (payload->>'stream_epoch')::bigint,(payload->>'frame_sequence')::bigint FROM flowbaton_events WHERE session_id=$1 AND type='frame' ORDER BY sequence DESC LIMIT 1`, session.SessionID).Scan(&latestEpoch, &latestFrame)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrInvalidState
	}
	if err != nil {
		return Result{}, err
	}
	if latestEpoch != envelope.BasedOnStreamEpoch || latestFrame != envelope.BasedOnFrameSequence {
		return Result{}, ErrFenced
	}
	var existingHash, existingSession string
	var completed *int64
	err = tx.QueryRow(ctx, `SELECT request_hash,session_id,completed_event_sequence FROM flowbaton_input_jobs WHERE tenant_id=$1 AND principal_id=$2 AND idempotency_key=$3`, input.TenantID, input.PrincipalID, input.IdempotencyKey).Scan(&existingHash, &existingSession, &completed)
	if err == nil {
		if existingHash != hash || existingSession != session.SessionID {
			return Result{}, ErrConflict
		}
		if completed != nil {
			result, _, replayErr := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash)
			return result, replayErr
		}
		return Result{Session: session, Replay: true, Queued: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	var requestSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_requests WHERE session_id=$1`, session.SessionID).Scan(&requestSequence); err != nil {
		return Result{}, err
	}
	requestID := input.RequestID
	if requestID == "" {
		requestID, err = secureID("request")
		if err != nil {
			return Result{}, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_requests(session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,channel_binding_sha256,payload,created_at) VALUES($1,$2,$3,'input',$4,$5,$6,$7,$8,$9)`, session.SessionID, requestSequence, requestID, input.IdempotencyKey, input.TenantID, input.PrincipalID, input.ChannelBindingSHA256, input.Payload, now); err != nil {
		return Result{}, err
	}
	if session.OwnerNodeID == "" {
		return Result{}, ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_input_jobs(session_id,request_sequence,tenant_id,principal_id,owner_node_id,request_id,idempotency_key,request_hash,lease_generation,fencing_token_sha256,stream_epoch,frame_sequence,command,command_payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`, session.SessionID, requestSequence, input.TenantID, input.PrincipalID, session.OwnerNodeID, requestID, input.IdempotencyKey, hash, session.Generation, session.FencingTokenSHA256, envelope.BasedOnStreamEpoch, envelope.BasedOnFrameSequence, envelope.Command, input.CommandPayload, now); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('flowbaton_work',$1)`, session.OwnerNodeID); err != nil {
		return Result{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Result{}, err
	}
	return Result{Session: session, Queued: true}, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func validInputCommand(command string) bool {
	switch command {
	case "tap", "input-text", "press-key", "swipe", "set-orientation":
		return true
	default:
		return false
	}
}

func validateCommandPayload(command string, payload []byte) error {
	switch command {
	case "tap":
		var value struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		return decodeStrictJSON(payload, &value)
	case "input-text":
		var value struct {
			Text   string   `json:"text"`
			AppIDs []string `json:"app_ids,omitempty"`
		}
		if err := decodeStrictJSON(payload, &value); err != nil {
			return err
		}
		if value.Text == "" {
			return errors.New("input-text text is required")
		}
		return nil
	case "press-key":
		var value struct {
			Code   string   `json:"code"`
			AppIDs []string `json:"app_ids,omitempty"`
		}
		if err := decodeStrictJSON(payload, &value); err != nil {
			return err
		}
		if value.Code == "" {
			return errors.New("press-key code is required")
		}
		return nil
	case "swipe":
		type point struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		}
		var value struct {
			Start          *point `json:"start,omitempty"`
			End            *point `json:"end,omitempty"`
			Direction      string `json:"direction,omitempty"`
			ElementPoint   *point `json:"element_point,omitempty"`
			DurationMillis int64  `json:"duration_millis"`
		}
		if err := decodeStrictJSON(payload, &value); err != nil {
			return err
		}
		if (value.Start == nil) != (value.End == nil) || (value.Start == nil && value.Direction == "") || value.DurationMillis < 0 {
			return errors.New("swipe shape is invalid")
		}
		return nil
	case "set-orientation":
		var value struct {
			Orientation string `json:"orientation"`
		}
		if err := decodeStrictJSON(payload, &value); err != nil {
			return err
		}
		switch value.Orientation {
		case "portrait", "portrait-upside-down", "landscape-left", "landscape-right":
			return nil
		default:
			return errors.New("orientation is invalid")
		}
	default:
		return errors.New("command is unsupported")
	}
}

// MarkDisconnected records transport loss without releasing or changing the
// fence. Repeated observations are idempotent; reconnect remains client-driven.
func (store *Postgres) MarkDisconnected(ctx context.Context, tenantID, sessionID string, generation int64, fence, reason string, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if session.Generation != generation || session.FencingTokenSHA256 != fence {
		return ErrFenced
	}
	if !at.Before(session.BindingExpiresAt) || !at.Before(session.LeaseExpiresAt) {
		return ErrExpired
	}
	if session.Status == "disconnected" {
		return tx.Commit(ctx)
	}
	if session.Status != "active" {
		return ErrInvalidState
	}
	var eventSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, sessionID).Scan(&eventSequence); err != nil {
		return err
	}
	if eventSequence > maxControlEvents {
		return ErrInvalidState
	}
	if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='disconnected' WHERE session_id=$1`, sessionID); err != nil {
		return err
	}
	if reason == "" {
		reason = "transport_interrupted"
	}
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	_, err = persistEvent(ctx, tx, session, eventSequence, eventID, "disconnected", map[string]any{"reason": reason, "last_server_sequence": eventSequence - 1}, at.UTC())
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Postgres) Events(ctx context.Context, tenantID, principalID, sessionID string, after int64) ([]devicesessionv1.Event, error) {
	rows, err := store.Pool.Query(ctx, `SELECT e.sequence,e.event_id,e.type,e.created_at,e.lease_generation,e.fencing_token_sha256,e.payload FROM flowbaton_events e JOIN flowbaton_sessions s USING(session_id) WHERE s.tenant_id=$1 AND s.principal_id=$2 AND e.session_id=$3 AND e.sequence>$4 ORDER BY e.sequence`, tenantID, principalID, sessionID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []devicesessionv1.Event
	for rows.Next() {
		var item devicesessionv1.Event
		var at time.Time
		if err := rows.Scan(&item.Sequence, &item.EventID, &item.Type, &at, &item.LeaseGeneration, &item.FencingTokenSHA256, &item.Data); err != nil {
			return nil, err
		}
		item.Timestamp = at.UTC().Format(time.RFC3339Nano)
		events = append(events, item)
	}
	return events, rows.Err()
}

func validateAcquire(input AcquireInput) error {
	if input.TenantID == "" || input.PrincipalID == "" || input.AuthProfileID == "" || input.ChannelBindingSHA256 == "" || input.RequestNonce == "" || input.ResourceID == "" || input.IdempotencyKey == "" || input.ReleaseIdempotencyKey == "" || len(input.RequestedCapabilities) == 0 || input.LeaseDuration <= 0 || input.HeartbeatInterval <= 0 || input.BindingExpiresAt.IsZero() {
		return fmt.Errorf("%w: acquire request is incomplete", ErrInvalidArgument)
	}
	return nil
}

func validateMutation(input MutationInput) error {
	if input.SessionID == "" || input.TenantID == "" || input.PrincipalID == "" || input.ChannelBindingSHA256 == "" || input.Type == "" || input.IdempotencyKey == "" || input.Generation < 1 || input.FencingTokenSHA256 == "" {
		return fmt.Errorf("%w: session request is incomplete", ErrInvalidArgument)
	}
	return nil
}

func replay(ctx context.Context, tx pgx.Tx, tenantID, principalID, key, hash string) (Result, bool, error) {
	var storedHash, sessionID string
	var eventSequence int64
	err := tx.QueryRow(ctx, `SELECT request_hash,session_id,event_sequence FROM flowbaton_idempotency WHERE tenant_id=$1 AND principal_id=$2 AND idempotency_key=$3`, tenantID, principalID, key).Scan(&storedHash, &sessionID, &eventSequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, false, nil
	}
	if err != nil {
		return Result{}, false, err
	}
	if storedHash != hash {
		return Result{}, true, ErrConflict
	}
	session, err := loadSessionForUpdate(ctx, tx, tenantID, sessionID)
	if err != nil {
		return Result{}, true, err
	}
	var event devicesessionv1.Event
	var at time.Time
	err = tx.QueryRow(ctx, `SELECT sequence,event_id,type,created_at,lease_generation,fencing_token_sha256,payload FROM flowbaton_events WHERE session_id=$1 AND sequence=$2`, sessionID, eventSequence).Scan(&event.Sequence, &event.EventID, &event.Type, &at, &event.LeaseGeneration, &event.FencingTokenSHA256, &event.Data)
	if err != nil {
		return Result{}, true, err
	}
	event.Timestamp = at.UTC().Format(time.RFC3339Nano)
	return Result{Session: session, Event: event, Replay: true}, true, nil
}

func loadSessionForUpdate(ctx context.Context, tx pgx.Tx, tenantID, sessionID string) (Session, error) {
	var session Session
	var capabilities []byte
	var heartbeatMS int64
	err := tx.QueryRow(ctx, `SELECT session_id,tenant_id,principal_id,auth_profile_id,channel_binding_sha256,request_nonce,binding_expires_at,resource_id,COALESCE(owner_node_id,''),lease_id,lease_generation,fencing_token_sha256,release_idempotency_key,capabilities,status,stream_epoch,acquired_at,lease_expires_at,heartbeat_interval_ms FROM flowbaton_sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenantID, sessionID).Scan(&session.SessionID, &session.TenantID, &session.PrincipalID, &session.AuthProfileID, &session.ChannelBindingSHA256, &session.RequestNonce, &session.BindingExpiresAt, &session.ResourceID, &session.OwnerNodeID, &session.LeaseID, &session.Generation, &session.FencingTokenSHA256, &session.ReleaseIdempotencyKey, &capabilities, &session.Status, &session.StreamEpoch, &session.AcquiredAt, &session.LeaseExpiresAt, &heartbeatMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	if err := json.Unmarshal(capabilities, &session.Capabilities); err != nil {
		return Session{}, err
	}
	session.HeartbeatInterval = time.Duration(heartbeatMS) * time.Millisecond
	return session, nil
}

func persistEvent(ctx context.Context, tx pgx.Tx, session Session, sequence int64, eventID, kind string, payload any, at time.Time) (devicesessionv1.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return devicesessionv1.Event{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO flowbaton_events(session_id,sequence,event_id,type,lease_generation,fencing_token_sha256,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, session.SessionID, sequence, eventID, kind, session.Generation, session.FencingTokenSHA256, data, at)
	if err != nil {
		return devicesessionv1.Event{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify('flowbaton_events',$1)`, session.SessionID); err != nil {
		return devicesessionv1.Event{}, err
	}
	return devicesessionv1.Event{Sequence: int(sequence), EventID: eventID, Type: kind, Timestamp: at.UTC().Format(time.RFC3339Nano), LeaseGeneration: int(session.Generation), FencingTokenSHA256: session.FencingTokenSHA256, Data: data}, nil
}

func requestHash(values ...any) string {
	data, _ := json.Marshal(values)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func secureID(prefix string) (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate %s identity: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(data[:]), nil
}
func secureDigest() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate lease fence: %w", err)
	}
	digest := sha256.Sum256(data[:])
	return hex.EncodeToString(digest[:]), nil
}
func containsAll(available, requested []string) bool {
	set := map[string]bool{}
	for _, item := range available {
		set[item] = true
	}
	for _, item := range requested {
		if !set[item] {
			return false
		}
	}
	return true
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func payloadString(payload []byte, key, fallback string) string {
	var value map[string]any
	if json.Unmarshal(payload, &value) == nil {
		if text, ok := value[key].(string); ok && text != "" {
			return text
		}
	}
	return fallback
}

func validCancelReason(reason string) bool {
	switch reason {
	case "user_requested", "budget_exhausted", "authorization_revoked", "deadline_exceeded":
		return true
	default:
		return false
	}
}
