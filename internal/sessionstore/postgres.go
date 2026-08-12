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
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
	"github.com/larchwave/flowbaton/internal/strictjson"
)

//go:embed schema/*.sql
var schemaFiles embed.FS

const schemaLockID int64 = 0x464c4f574241544f

type Postgres struct{ Pool *pgxpool.Pool }

const (
	maxTokenNonceLength      = 128
	maxLiveNoncesPerIdentity = 64
	maxTokenTTL              = time.Hour
	maxPendingInputs         = 64
	maxSessionFieldLength    = 256
	maxStoredPayloadBytes    = 1 << 20
)

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

func (store *Postgres) CurrentTime(ctx context.Context) (time.Time, error) {
	var now time.Time
	err := store.Pool.QueryRow(ctx, `SELECT date_trunc('second',clock_timestamp())`).Scan(&now)
	return now.UTC(), err
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

func (store *Postgres) RegisterNode(ctx context.Context, nodeID, address string, leaseFor time.Duration) (NodeLease, error) {
	if nodeID == "" || address == "" || leaseFor <= 0 {
		return NodeLease{}, errors.New("node registration is incomplete")
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NodeLease{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, nodeID); err != nil {
		return NodeLease{}, err
	}
	var epoch int64
	var live bool
	err = tx.QueryRow(ctx, `SELECT worker_epoch,lease_expires_at>clock_timestamp()
		FROM flowbaton_nodes WHERE node_id=$1 FOR UPDATE`, nodeID).Scan(&epoch, &live)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		epoch = 1
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_nodes(node_id,public_address,last_heartbeat_at,lease_expires_at,worker_epoch,ready)
			VALUES($1,$2,clock_timestamp(),clock_timestamp()+$3::interval,$4,false)`, nodeID, address, durationInterval(leaseFor), epoch); err != nil {
			return NodeLease{}, err
		}
	case err != nil:
		return NodeLease{}, err
	case live:
		return NodeLease{}, ErrBusy
	default:
		epoch++
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_nodes SET public_address=$2,last_heartbeat_at=clock_timestamp(),
			lease_expires_at=clock_timestamp()+$3::interval,worker_epoch=$4,ready=false WHERE node_id=$1`, nodeID, address, durationInterval(leaseFor), epoch); err != nil {
			return NodeLease{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return NodeLease{}, err
	}
	return NodeLease{NodeID: nodeID, WorkerEpoch: epoch}, nil
}

func (store *Postgres) ActivateNode(ctx context.Context, lease NodeLease) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return errors.New("node activation is incomplete")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET ready=true
		WHERE node_id=$1 AND worker_epoch=$2 AND lease_expires_at>clock_timestamp()`, lease.NodeID, lease.WorkerEpoch)
	if err == nil && command.RowsAffected() != 1 {
		return ErrFenced
	}
	return err
}

func (store *Postgres) DeactivateNode(ctx context.Context, lease NodeLease) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return errors.New("node deactivation is incomplete")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes SET ready=false,lease_expires_at=clock_timestamp()
		WHERE node_id=$1 AND worker_epoch=$2`, lease.NodeID, lease.WorkerEpoch)
	if err == nil && command.RowsAffected() != 1 {
		return ErrFenced
	}
	return err
}

func (store *Postgres) HeartbeatNode(ctx context.Context, lease NodeLease, leaseFor time.Duration) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 || leaseFor <= 0 {
		return errors.New("node heartbeat is incomplete")
	}
	command, err := store.Pool.Exec(ctx, `UPDATE flowbaton_nodes
		SET last_heartbeat_at=clock_timestamp(),lease_expires_at=clock_timestamp()+$3::interval
		WHERE node_id=$1 AND worker_epoch=$2 AND lease_expires_at>clock_timestamp()`, lease.NodeID, lease.WorkerEpoch, durationInterval(leaseFor))
	if err == nil && command.RowsAffected() != 1 {
		return ErrFenced
	}
	return err
}

func (store *Postgres) RegisterDevice(ctx context.Context, tenantID, resourceID string, lease NodeLease, capabilities []string) error {
	if tenantID == "" || resourceID == "" || lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return errors.New("device registration is incomplete")
	}
	encoded, _ := json.Marshal(capabilities)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	var ownerNode string
	var currentSession *string
	err = tx.QueryRow(ctx, `SELECT COALESCE(owner_node_id,''),current_session_id
		FROM flowbaton_devices WHERE tenant_id=$1 AND resource_id=$2 FOR UPDATE`, tenantID, resourceID).Scan(&ownerNode, &currentSession)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if currentSession != nil && ownerNode != lease.NodeID {
		return ErrBusy
	}
	if ownerNode != "" && ownerNode != lease.NodeID {
		var ownerLive bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_devices d
			JOIN flowbaton_nodes n ON n.node_id=d.owner_node_id AND n.worker_epoch=d.owner_worker_epoch
			WHERE d.tenant_id=$1 AND d.resource_id=$2 AND n.lease_expires_at>clock_timestamp())`,
			tenantID, resourceID).Scan(&ownerLive); err != nil {
			return err
		}
		if ownerLive {
			return ErrBusy
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_devices(tenant_id,resource_id,owner_node_id,owner_worker_epoch,capabilities)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT(tenant_id,resource_id) DO UPDATE SET
		owner_node_id=excluded.owner_node_id,owner_worker_epoch=excluded.owner_worker_epoch,
		capabilities=excluded.capabilities,updated_at=clock_timestamp()`, tenantID, resourceID, lease.NodeID, lease.WorkerEpoch, encoded); err != nil {
		return err
	}
	if currentSession != nil {
		command, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET owner_worker_epoch=$2
			WHERE session_id=$1 AND owner_node_id=$3 AND status IN ('active','disconnected','cancelled')`, *currentSession, lease.WorkerEpoch, lease.NodeID)
		if err != nil {
			return err
		}
		if command.RowsAffected() != 1 {
			return ErrFenced
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET owner_worker_epoch=$2,
			state=CASE WHEN state IN ('pending','claimed') THEN 'pending' ELSE state END,
			claimed_by=CASE WHEN state IN ('pending','claimed') THEN NULL ELSE claimed_by END,
			claimed_worker_epoch=CASE WHEN state IN ('pending','claimed') THEN NULL ELSE claimed_worker_epoch END,
			claim_expires_at=CASE WHEN state IN ('pending','claimed') THEN NULL ELSE claim_expires_at END
			WHERE session_id=$1 AND state!='done'`, *currentSession, lease.WorkerEpoch); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_frame_jobs SET owner_worker_epoch=$2,
			state=CASE WHEN state='blocked' THEN state ELSE 'pending' END,
			claimed_by=NULL,claimed_worker_epoch=NULL,claim_expires_at=NULL
			WHERE session_id=$1`, *currentSession, lease.WorkerEpoch); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (store *Postgres) requireNodeLease(ctx context.Context, lease NodeLease) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return ErrFenced
	}
	var live bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM flowbaton_nodes
		WHERE node_id=$1 AND worker_epoch=$2 AND lease_expires_at>clock_timestamp()
	)`, lease.NodeID, lease.WorkerEpoch).Scan(&live); err != nil {
		return err
	}
	if !live {
		return ErrFenced
	}
	return nil
}

func (store *Postgres) RequireReadyNode(ctx context.Context, lease NodeLease) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return ErrFenced
	}
	var ready bool
	if err := store.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_nodes
		WHERE node_id=$1 AND worker_epoch=$2 AND ready=true AND lease_expires_at>clock_timestamp())`,
		lease.NodeID, lease.WorkerEpoch).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return ErrFenced
	}
	return nil
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

func (store *Postgres) ReserveTokenNonce(ctx context.Context, fingerprint, nonce string, ttl time.Duration) (TokenWindow, error) {
	if fingerprint == "" || nonce == "" || len(nonce) > maxTokenNonceLength || ttl <= 0 || ttl > maxTokenTTL || ttl%time.Second != 0 {
		return TokenWindow{}, errors.New("token nonce reservation is incomplete")
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return TokenWindow{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, fingerprint); err != nil {
		return TokenWindow{}, err
	}
	var window TokenWindow
	if err := tx.QueryRow(ctx, `WITH db_time AS (SELECT date_trunc('second',clock_timestamp()) AS issued_at)
		SELECT issued_at,issued_at+$1::interval FROM db_time`, durationInterval(ttl)).Scan(&window.IssuedAt, &window.ExpiresAt); err != nil {
		return TokenWindow{}, err
	}
	window.IssuedAt, window.ExpiresAt = window.IssuedAt.UTC(), window.ExpiresAt.UTC()
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_token_nonces WHERE expires_at < $1`, window.IssuedAt); err != nil {
		return TokenWindow{}, err
	}
	var used bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_token_nonces
		WHERE certificate_fingerprint_sha256=$1 AND nonce=$2)`, fingerprint, nonce).Scan(&used); err != nil {
		return TokenWindow{}, err
	}
	if used {
		return TokenWindow{}, ErrConflict
	}
	var live int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM flowbaton_token_nonces
		WHERE certificate_fingerprint_sha256=$1 AND expires_at>=$2`, fingerprint, window.IssuedAt).Scan(&live); err != nil {
		return TokenWindow{}, err
	}
	if live >= maxLiveNoncesPerIdentity {
		return TokenWindow{}, ErrBackpressure
	}
	command, err := tx.Exec(ctx, `INSERT INTO flowbaton_token_nonces(certificate_fingerprint_sha256,nonce,expires_at) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, fingerprint, nonce, window.ExpiresAt)
	if err != nil {
		return TokenWindow{}, err
	}
	if command.RowsAffected() != 1 {
		return TokenWindow{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return TokenWindow{}, err
	}
	return window, nil
}

func (store *Postgres) Acquire(ctx context.Context, input AcquireInput) (Result, error) {
	if err := validateAcquire(input); err != nil {
		return Result{}, err
	}
	input.BindingExpiresAt = postgresTimestamp(input.BindingExpiresAt)
	hash := requestHash("acquire", input.ResourceID, input.RequestedCapabilities, input.ReleaseIdempotencyKey, input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if !now.Before(input.BindingExpiresAt) {
		return Result{}, ErrExpired
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, requestHash(input.TenantID, input.PrincipalID, input.IdempotencyKey)); err != nil {
		return Result{}, err
	}
	if result, found, err := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash); err != nil || found {
		if err == nil && result.Session.Status == "released" {
			return Result{}, ErrInvalidState
		}
		return result, err
	}
	var capabilitiesJSON []byte
	var generation int64
	var currentSession *string
	var ownerNodeID *string
	var ownerWorkerEpoch int64
	err = tx.QueryRow(ctx, `SELECT d.capabilities,d.lease_generation,d.current_session_id,d.owner_node_id,d.owner_worker_epoch
		FROM flowbaton_devices d JOIN flowbaton_nodes n
		  ON n.node_id=d.owner_node_id AND n.worker_epoch=d.owner_worker_epoch
		WHERE d.tenant_id=$1 AND d.resource_id=$2 AND n.ready=true AND n.lease_expires_at>clock_timestamp()
		FOR UPDATE OF d`, input.TenantID, input.ResourceID).Scan(&capabilitiesJSON, &generation, &currentSession, &ownerNodeID, &ownerWorkerEpoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	if currentSession != nil {
		current, err := loadSessionForUpdate(ctx, tx, input.TenantID, *currentSession)
		if err != nil {
			return Result{}, err
		}
		if current.Status != "released" && now.Before(current.LeaseExpiresAt) && now.Before(current.BindingExpiresAt) {
			return Result{}, ErrBusy
		}
		if current.Status != "released" {
			if err := expireSessionTx(ctx, tx, current, now, "lease_expired"); err != nil {
				return Result{}, err
			}
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
	session := Session{SessionID: sessionID, TenantID: input.TenantID, PrincipalID: input.PrincipalID, AuthProfileID: input.AuthProfileID, ChannelBindingSHA256: input.ChannelBindingSHA256, RequestNonce: input.RequestNonce, BindingExpiresAt: input.BindingExpiresAt, ResourceID: input.ResourceID, OwnerNodeID: *ownerNodeID, OwnerWorkerEpoch: ownerWorkerEpoch, LeaseID: leaseID, Generation: generation, FencingTokenSHA256: fence, ReleaseIdempotencyKey: input.ReleaseIdempotencyKey, Capabilities: append([]string(nil), input.RequestedCapabilities...), Status: "active", StreamEpoch: 1, AcquiredAt: now, LeaseExpiresAt: leaseExpires, HeartbeatInterval: input.HeartbeatInterval}
	requested, _ := json.Marshal(input.RequestedCapabilities)
	_, err = tx.Exec(ctx, `INSERT INTO flowbaton_sessions(session_id,tenant_id,principal_id,auth_profile_id,channel_binding_sha256,request_nonce,binding_expires_at,resource_id,owner_node_id,owner_worker_epoch,lease_id,lease_generation,fencing_token_sha256,release_idempotency_key,capabilities,status,acquired_at,lease_expires_at,heartbeat_interval_ms)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'active',$16,$17,$18)`, sessionID, input.TenantID, input.PrincipalID, input.AuthProfileID, input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt, input.ResourceID, session.OwnerNodeID, session.OwnerWorkerEpoch, leaseID, generation, fence, input.ReleaseIdempotencyKey, requested, now, leaseExpires, input.HeartbeatInterval.Milliseconds())
	if err != nil {
		return Result{}, err
	}
	command, err := tx.Exec(ctx, `UPDATE flowbaton_devices SET lease_generation=$3,current_session_id=$4,updated_at=$5
		WHERE tenant_id=$1 AND resource_id=$2 AND owner_node_id=$6 AND owner_worker_epoch=$7
		AND current_session_id IS NULL`, input.TenantID, input.ResourceID, generation, sessionID, now, session.OwnerNodeID, session.OwnerWorkerEpoch)
	if err != nil {
		return Result{}, err
	}
	if command.RowsAffected() != 1 {
		return Result{}, ErrFenced
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
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,owner_worker_epoch,created_at) VALUES($1,$2,$3,$4)`, sessionID, session.OwnerNodeID, session.OwnerWorkerEpoch, now); err != nil {
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
	input.BindingExpiresAt = postgresTimestamp(input.BindingExpiresAt)
	hash := mutationRequestHash(input)
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, requestHash(input.TenantID, input.PrincipalID, input.IdempotencyKey)); err != nil {
		return Result{}, err
	}
	session, err := loadSessionForUpdate(ctx, tx, input.TenantID, input.SessionID)
	if err != nil {
		return Result{}, err
	}
	if session.PrincipalID != input.PrincipalID {
		return Result{}, ErrNotFound
	}
	if session.Generation != input.Generation || session.FencingTokenSHA256 != input.FencingTokenSHA256 {
		return Result{}, ErrFenced
	}
	var current bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_devices
		WHERE tenant_id=$1 AND resource_id=$2 AND current_session_id=$3
		AND owner_node_id=$4 AND owner_worker_epoch=$5)`, session.TenantID, session.ResourceID,
		session.SessionID, session.OwnerNodeID, session.OwnerWorkerEpoch).Scan(&current); err != nil {
		return Result{}, err
	}
	if !current && session.Status != "released" {
		return Result{}, ErrFenced
	}
	if input.Type == "reconnect" {
		if session.Status == "active" && !matchesSessionBinding(session, input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt) {
			return Result{}, ErrNotFound
		}
	} else if !matchesSessionBinding(session, input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt) {
		return Result{}, ErrNotFound
	}
	if session.Status != "released" && (!now.Before(session.BindingExpiresAt) || !now.Before(session.LeaseExpiresAt)) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return Result{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Result{}, err
		}
		return Result{}, ErrExpired
	}
	if session.Status == "released" {
		if input.Type != "release" || input.IdempotencyKey != session.ReleaseIdempotencyKey {
			return Result{}, ErrInvalidState
		}
		result, found, replayErr := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash)
		if replayErr != nil {
			return Result{}, replayErr
		}
		if !found {
			return Result{}, ErrInvalidState
		}
		return result, nil
	}
	if result, found, replayErr := replay(ctx, tx, input.TenantID, input.PrincipalID, input.IdempotencyKey, hash); replayErr != nil || found {
		return result, replayErr
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
	storedPayload, err := canonicalControlPayload(session, input, payload)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_requests(session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,channel_binding_sha256,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, input.SessionID, requestSequence, requestID, input.Type, input.IdempotencyKey, input.TenantID, input.PrincipalID, input.ChannelBindingSHA256, storedPayload, now); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_frame_content SET expires_at=$2 WHERE session_id=$1`, session.SessionID, session.LeaseExpiresAt); err != nil {
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
		if err := cancelQueuedInputsTx(ctx, tx, session, now); err != nil {
			return Result{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, session.SessionID).Scan(&eventSequence); err != nil {
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
		if input.RequestNonce == "" || input.BindingExpiresAt.IsZero() || !now.Before(input.BindingExpiresAt) {
			return Result{}, ErrExpired
		}
		if input.ChannelBindingSHA256 == session.ChannelBindingSHA256 || input.RequestNonce == session.RequestNonce || input.BindingExpiresAt.Equal(session.BindingExpiresAt) {
			return Result{}, ErrNotFound
		}
		session.Status, session.StreamEpoch = "active", session.StreamEpoch+1
		session.ChannelBindingSHA256 = input.ChannelBindingSHA256
		session.RequestNonce = input.RequestNonce
		session.BindingExpiresAt = input.BindingExpiresAt
		if session.LeaseExpiresAt.After(session.BindingExpiresAt) {
			session.LeaseExpiresAt = session.BindingExpiresAt
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='active',stream_epoch=$2,
			channel_binding_sha256=$3,request_nonce=$4,binding_expires_at=$5,lease_expires_at=$6 WHERE session_id=$1`,
			session.SessionID, session.StreamEpoch, session.ChannelBindingSHA256, session.RequestNonce,
			session.BindingExpiresAt, session.LeaseExpiresAt); err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,owner_worker_epoch,created_at) VALUES($1,$2,$3,$4)
			ON CONFLICT(session_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,owner_worker_epoch=excluded.owner_worker_epoch,
			state='pending',claimed_by=NULL,claimed_worker_epoch=NULL,claim_expires_at=NULL,created_at=excluded.created_at`, session.SessionID, session.OwnerNodeID, session.OwnerWorkerEpoch, now); err != nil {
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
		if executing, err := executingInputCount(ctx, tx, session.SessionID); err != nil {
			return Result{}, err
		} else if executing != 0 {
			return Result{}, ErrInvalidState
		}
		if err := cancelQueuedInputsTx(ctx, tx, session, now); err != nil {
			return Result{}, err
		}
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, session.SessionID).Scan(&eventSequence); err != nil {
			return Result{}, err
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
		if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_content WHERE session_id=$1`, session.SessionID); err != nil {
			return Result{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_jobs WHERE session_id=$1`, session.SessionID); err != nil {
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

func executingInputCount(ctx context.Context, tx pgx.Tx, sessionID string) (int64, error) {
	var count int64
	err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM flowbaton_input_jobs WHERE session_id=$1 AND state='executing'`, sessionID).Scan(&count)
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
	var pending int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM flowbaton_input_jobs WHERE session_id=$1 AND state IN ('pending','claimed','executing')`, session.SessionID).Scan(&pending); err != nil {
		return Result{}, err
	}
	if pending >= maxPendingInputs {
		return Result{}, ErrBackpressure
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
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_input_jobs(session_id,request_sequence,tenant_id,principal_id,owner_node_id,owner_worker_epoch,request_id,idempotency_key,request_hash,lease_generation,fencing_token_sha256,stream_epoch,frame_sequence,command,command_payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, session.SessionID, requestSequence, input.TenantID, input.PrincipalID, session.OwnerNodeID, session.OwnerWorkerEpoch, requestID, input.IdempotencyKey, hash, session.Generation, session.FencingTokenSHA256, envelope.BasedOnStreamEpoch, envelope.BasedOnFrameSequence, envelope.Command, input.CommandPayload, now); err != nil {
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
	return strictjson.Decode(data, target)
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
func (store *Postgres) MarkDisconnected(ctx context.Context, tenantID, principalID, sessionID, channelBinding, requestNonce string, bindingExpiresAt time.Time, generation int64, fence, reason string) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if session.PrincipalID != principalID || !matchesSessionBinding(session, channelBinding, requestNonce, bindingExpiresAt) {
		return ErrNotFound
	}
	if session.Generation != generation || session.FencingTokenSHA256 != fence {
		return ErrFenced
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	if !now.Before(session.BindingExpiresAt) || !now.Before(session.LeaseExpiresAt) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
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
	_, err = persistEvent(ctx, tx, session, eventSequence, eventID, "disconnected", map[string]any{"reason": reason, "last_server_sequence": eventSequence - 1}, now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Postgres) ValidateSessionAccess(ctx context.Context, tenantID, principalID, sessionID, channelBinding, requestNonce string, bindingExpiresAt time.Time, generation int64, fence string) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if session.PrincipalID != principalID || !matchesSessionBinding(session, channelBinding, requestNonce, bindingExpiresAt) {
		return ErrNotFound
	}
	if session.Generation != generation || session.FencingTokenSHA256 != fence {
		return ErrFenced
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	if session.Status != "released" && sessionExpiredAt(session, now) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func matchesSessionBinding(session Session, channelBinding, requestNonce string, bindingExpiresAt time.Time) bool {
	return session.ChannelBindingSHA256 == channelBinding &&
		session.RequestNonce == requestNonce &&
		session.BindingExpiresAt.Equal(postgresTimestamp(bindingExpiresAt))
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
	if !boundedSessionField(input.TenantID, 3) || !boundedSessionField(input.PrincipalID, 3) ||
		!boundedSessionField(input.AuthProfileID, 8) || !boundedSessionField(input.ChannelBindingSHA256, 1) ||
		len(input.RequestNonce) < 16 || len(input.RequestNonce) > maxTokenNonceLength ||
		!boundedSessionField(input.ResourceID, 3) || !boundedSessionField(input.IdempotencyKey, 16) ||
		!boundedSessionField(input.ReleaseIdempotencyKey, 16) || len(input.RequestedCapabilities) > 16 {
		return fmt.Errorf("%w: acquire request fields are outside supported bounds", ErrInvalidArgument)
	}
	for _, capability := range input.RequestedCapabilities {
		if !boundedSessionField(capability, 1) {
			return fmt.Errorf("%w: acquire capability is outside supported bounds", ErrInvalidArgument)
		}
	}
	return nil
}

func validateMutation(input MutationInput) error {
	if input.SessionID == "" || input.TenantID == "" || input.PrincipalID == "" || input.ChannelBindingSHA256 == "" || input.RequestNonce == "" || input.BindingExpiresAt.IsZero() || input.Type == "" || input.IdempotencyKey == "" || input.Generation < 1 || input.FencingTokenSHA256 == "" {
		return fmt.Errorf("%w: session request is incomplete", ErrInvalidArgument)
	}
	if !boundedSessionField(input.SessionID, 8) || !boundedSessionField(input.TenantID, 3) ||
		!boundedSessionField(input.PrincipalID, 3) || !boundedSessionField(input.ChannelBindingSHA256, 1) ||
		len(input.RequestNonce) < 16 || len(input.RequestNonce) > maxTokenNonceLength ||
		!boundedSessionField(input.Type, 1) || !boundedSessionField(input.IdempotencyKey, 16) ||
		!boundedSessionField(input.FencingTokenSHA256, 1) ||
		(input.RequestID != "" && !boundedSessionField(input.RequestID, 8)) ||
		len(input.Payload) > maxStoredPayloadBytes || len(input.CommandPayload) > maxStoredPayloadBytes {
		return fmt.Errorf("%w: session request fields are outside supported bounds", ErrInvalidArgument)
	}
	return nil
}

func boundedSessionField(value string, minimum int) bool {
	return len(value) >= minimum && len(value) <= maxSessionFieldLength
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
	err := tx.QueryRow(ctx, `SELECT session_id,tenant_id,principal_id,auth_profile_id,channel_binding_sha256,request_nonce,binding_expires_at,resource_id,COALESCE(owner_node_id,''),owner_worker_epoch,lease_id,lease_generation,fencing_token_sha256,release_idempotency_key,capabilities,status,stream_epoch,acquired_at,lease_expires_at,heartbeat_interval_ms FROM flowbaton_sessions WHERE tenant_id=$1 AND session_id=$2 FOR UPDATE`, tenantID, sessionID).Scan(&session.SessionID, &session.TenantID, &session.PrincipalID, &session.AuthProfileID, &session.ChannelBindingSHA256, &session.RequestNonce, &session.BindingExpiresAt, &session.ResourceID, &session.OwnerNodeID, &session.OwnerWorkerEpoch, &session.LeaseID, &session.Generation, &session.FencingTokenSHA256, &session.ReleaseIdempotencyKey, &capabilities, &session.Status, &session.StreamEpoch, &session.AcquiredAt, &session.LeaseExpiresAt, &heartbeatMS)
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

func persistEvent(ctx context.Context, tx pgx.Tx, session Session, sequence int64, eventID, kind string, payload any, _ time.Time) (devicesessionv1.Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return devicesessionv1.Event{}, err
	}
	var at time.Time
	err = tx.QueryRow(ctx, `SELECT GREATEST(clock_timestamp(),COALESCE(
		(SELECT MAX(created_at)+interval '1 microsecond' FROM flowbaton_events WHERE session_id=$1),
		clock_timestamp()))`, session.SessionID).Scan(&at)
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

func databaseNowTx(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now.UTC(), err
}

// PostgreSQL timestamps have microsecond precision. Normalize caller-provided
// token times before hashing, storing, or comparing them so the value returned
// from Acquire remains usable on hosts whose clocks expose nanoseconds.
func postgresTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func cancelQueuedInputsTx(ctx context.Context, tx pgx.Tx, session Session, at time.Time) error {
	rows, err := tx.Query(ctx, `SELECT request_sequence,idempotency_key,request_hash
		FROM flowbaton_input_jobs WHERE session_id=$1 AND state IN ('pending','claimed')
		ORDER BY request_sequence FOR UPDATE`, session.SessionID)
	if err != nil {
		return err
	}
	type queued struct {
		sequence       int64
		idempotencyKey string
		requestHash    string
	}
	var queuedInputs []queued
	for rows.Next() {
		var item queued
		if err := rows.Scan(&item.sequence, &item.idempotencyKey, &item.requestHash); err != nil {
			rows.Close()
			return err
		}
		queuedInputs = append(queuedInputs, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range queuedInputs {
		var eventSequence int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, session.SessionID).Scan(&eventSequence); err != nil {
			return err
		}
		eventID, err := secureID("event")
		if err != nil {
			return err
		}
		payload := typedErrorPayload("DEVICE_UNAVAILABLE", false, "device input was cancelled before execution")
		if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "error", payload, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(
			tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence)
			VALUES($1,$2,$3,$4,$5,$6)`, session.TenantID, session.PrincipalID,
			item.idempotencyKey, item.requestHash, session.SessionID, eventSequence); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='done',completed_event_sequence=$3,
			claimed_by=NULL,claimed_worker_epoch=NULL,claim_expires_at=NULL
			WHERE session_id=$1 AND request_sequence=$2`, session.SessionID, item.sequence, eventSequence); err != nil {
			return err
		}
	}
	return nil
}

func expireSessionTx(ctx context.Context, tx pgx.Tx, session Session, at time.Time, reason string) error {
	if session.Status == "released" {
		return nil
	}
	if err := cancelQueuedInputsTx(ctx, tx, session, at); err != nil {
		return err
	}
	if err := abandonExecutingInputsTx(ctx, tx, session, at); err != nil {
		return err
	}
	var requestSequence, eventSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_requests WHERE session_id=$1`, session.SessionID).Scan(&requestSequence); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, session.SessionID).Scan(&eventSequence); err != nil {
		return err
	}
	requestID, err := secureID("request")
	if err != nil {
		return err
	}
	key := session.ReleaseIdempotencyKey
	releasePayload := leaseFencePayload(session)
	releasePayload["server_reason"] = reason
	payload, _ := json.Marshal(releasePayload)
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_requests(
		session_id,sequence,request_id,type,idempotency_key,tenant_id,principal_id,
		channel_binding_sha256,payload,created_at) VALUES($1,$2,$3,'release',$4,$5,$6,$7,$8,$9)`,
		session.SessionID, requestSequence, requestID, key, session.TenantID, session.PrincipalID,
		session.ChannelBindingSHA256, payload, at); err != nil {
		return err
	}
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "released", map[string]any{
		"release_idempotency_key": key, "outcome": "error", "generation": session.Generation,
	}, at); err != nil {
		return err
	}
	releaseHash := mutationRequestHash(MutationInput{
		SessionID: session.SessionID, TenantID: session.TenantID, PrincipalID: session.PrincipalID,
		ChannelBindingSHA256: session.ChannelBindingSHA256, RequestNonce: session.RequestNonce,
		BindingExpiresAt: session.BindingExpiresAt, Type: "release", IdempotencyKey: key,
		Generation: session.Generation, FencingTokenSHA256: session.FencingTokenSHA256,
		Payload: payload,
	})
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(
		tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence)
		VALUES($1,$2,$3,$4,$5,$6)`, session.TenantID, session.PrincipalID, key,
		releaseHash, session.SessionID, eventSequence); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE flowbaton_sessions SET status='released',released_at=$2 WHERE session_id=$1`, session.SessionID, at); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE flowbaton_devices SET current_session_id=NULL,updated_at=$3
		WHERE tenant_id=$1 AND resource_id=$2 AND current_session_id=$4`, session.TenantID, session.ResourceID, at, session.SessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_jobs WHERE session_id=$1`, session.SessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_content WHERE session_id=$1`, session.SessionID); err != nil {
		return err
	}
	return nil
}

func canonicalControlPayload(session Session, input MutationInput, raw json.RawMessage) (json.RawMessage, error) {
	var value any
	switch input.Type {
	case "heartbeat":
		value = map[string]any{
			"lease_id": session.LeaseID, "generation": session.Generation,
			"fencing_token_sha256":   session.FencingTokenSHA256,
			"requested_extension_ms": input.RequestedExtension.Milliseconds(),
		}
	case "reconnect":
		value = map[string]any{
			"session_id": session.SessionID, "lease_id": session.LeaseID,
			"generation": session.Generation, "fencing_token_sha256": session.FencingTokenSHA256,
			"last_acknowledged_sequence": input.LastAcknowledgedEvent, "request_nonce": input.RequestNonce,
			"binding_expires_at": input.BindingExpiresAt.UTC().Format(time.RFC3339Nano),
		}
	case "cancel":
		value = map[string]any{
			"lease_id": session.LeaseID, "generation": session.Generation,
			"fencing_token_sha256": session.FencingTokenSHA256,
			"reason":               payloadString(raw, "reason", "user_requested"),
		}
	case "release":
		value = leaseFencePayload(session)
	default:
		return nil, ErrInvalidState
	}
	encoded, err := json.Marshal(value)
	return encoded, err
}

func leaseFencePayload(session Session) map[string]any {
	return map[string]any{
		"lease_id": session.LeaseID, "generation": session.Generation,
		"fencing_token_sha256": session.FencingTokenSHA256,
	}
}

func abandonExecutingInputsTx(ctx context.Context, tx pgx.Tx, session Session, at time.Time) error {
	rows, err := tx.Query(ctx, `SELECT request_sequence,idempotency_key,request_hash
		FROM flowbaton_input_jobs WHERE session_id=$1 AND state='executing'
		ORDER BY request_sequence FOR UPDATE`, session.SessionID)
	if err != nil {
		return err
	}
	type executing struct {
		sequence       int64
		idempotencyKey string
		requestHash    string
	}
	var inputs []executing
	for rows.Next() {
		var item executing
		if err := rows.Scan(&item.sequence, &item.idempotencyKey, &item.requestHash); err != nil {
			rows.Close()
			return err
		}
		inputs = append(inputs, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, item := range inputs {
		var eventSequence int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, session.SessionID).Scan(&eventSequence); err != nil {
			return err
		}
		eventID, err := secureID("event")
		if err != nil {
			return err
		}
		payload := typedErrorPayload("DEVICE_UNAVAILABLE", false, "device input outcome is unknown after session expiry")
		if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "error", payload, at); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(
			tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence)
			VALUES($1,$2,$3,$4,$5,$6)`, session.TenantID, session.PrincipalID,
			item.idempotencyKey, item.requestHash, session.SessionID, eventSequence); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='done',completed_event_sequence=$3
			WHERE session_id=$1 AND request_sequence=$2 AND state='executing'`, session.SessionID, item.sequence, eventSequence); err != nil {
			return err
		}
	}
	return nil
}

func requestHash(values ...any) string {
	data, _ := json.Marshal(values)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func mutationRequestHash(input MutationInput) string {
	payload := json.RawMessage(input.Payload)
	bindingExpiresAt := postgresTimestamp(input.BindingExpiresAt)
	if input.Type == "release" {
		if len(payload) != 0 {
			var value any
			if err := json.Unmarshal(payload, &value); err == nil {
				if canonical, err := json.Marshal(value); err == nil {
					payload = canonical
				}
			}
		}
	}
	return requestHash(input.Type, input.SessionID, input.Generation, input.FencingTokenSHA256,
		input.ChannelBindingSHA256, input.RequestNonce, bindingExpiresAt,
		payload, json.RawMessage(input.CommandPayload),
		input.RequestedExtension, input.LastAcknowledgedEvent)
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
