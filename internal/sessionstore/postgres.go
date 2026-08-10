package sessionstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const migrationLockID int64 = 0x464c4f574241544f

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

func (store *Postgres) Migrate(ctx context.Context) error {
	connection, err := store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return err
	}
	defer connection.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		version := entry.Name()
		var applied bool
		err := connection.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM flowbaton_schema_migrations WHERE version=$1)", version).Scan(&applied)
		if err != nil && !isUndefinedTable(err) {
			return err
		}
		if applied {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + version)
		if err != nil {
			return err
		}
		tx, err := connection.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(sql)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO flowbaton_schema_migrations(version) VALUES($1) ON CONFLICT DO NOTHING", version)
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", version, err)
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

func (store *Postgres) RegisterDevice(ctx context.Context, tenantID, resourceID, nodeID string, capabilities []string) error {
	encoded, _ := json.Marshal(capabilities)
	_, err := store.Pool.Exec(ctx, `INSERT INTO flowbaton_devices(tenant_id,resource_id,owner_node_id,capabilities) VALUES($1,$2,$3,$4)
		ON CONFLICT(tenant_id,resource_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,capabilities=excluded.capabilities,updated_at=clock_timestamp()`, tenantID, resourceID, nullable(nodeID), encoded)
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
	if result, found, err := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash); err != nil || found {
		return result, err
	}
	var capabilitiesJSON []byte
	var generation int64
	var currentSession *string
	err = tx.QueryRow(ctx, `SELECT capabilities,lease_generation,current_session_id FROM flowbaton_devices WHERE tenant_id=$1 AND resource_id=$2 FOR UPDATE`, input.TenantID, input.ResourceID).Scan(&capabilitiesJSON, &generation, &currentSession)
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
	session := Session{SessionID: sessionID, TenantID: input.TenantID, PrincipalID: input.PrincipalID, AuthProfileID: input.AuthProfileID, ChannelBindingSHA256: input.ChannelBindingSHA256, RequestNonce: input.RequestNonce, BindingExpiresAt: input.BindingExpiresAt, ResourceID: input.ResourceID, LeaseID: leaseID, Generation: generation, FencingTokenSHA256: fence, ReleaseIdempotencyKey: input.ReleaseIdempotencyKey, Capabilities: append([]string(nil), input.RequestedCapabilities...), Status: "active", StreamEpoch: 1, AcquiredAt: now, LeaseExpiresAt: leaseExpires, HeartbeatInterval: input.HeartbeatInterval}
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
	hash := requestHash(input.Type, input.SessionID, input.Generation, input.FencingTokenSHA256, json.RawMessage(input.Payload), input.RequestedExtension, input.LastAcknowledgedEvent)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
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
		eventType = "reconnected"
		eventPayload = map[string]any{"request_id": requestID, "idempotency_key": input.IdempotencyKey, "resume_from_sequence": input.LastAcknowledgedEvent, "stream_epoch": session.StreamEpoch, "generation": session.Generation}
	case "release":
		if session.Status != "active" && session.Status != "disconnected" && session.Status != "cancelled" {
			return Result{}, ErrInvalidState
		}
		if input.IdempotencyKey != session.ReleaseIdempotencyKey {
			return Result{}, ErrConflict
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
		return errors.New("acquire request is incomplete")
	}
	return nil
}

func validateMutation(input MutationInput) error {
	if input.SessionID == "" || input.TenantID == "" || input.PrincipalID == "" || input.ChannelBindingSHA256 == "" || input.Type == "" || input.IdempotencyKey == "" || input.Generation < 1 || input.FencingTokenSHA256 == "" {
		return errors.New("session request is incomplete")
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
