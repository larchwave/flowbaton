package sessionstore

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
)

const (
	maxEventBatch        = 64
	maxFrameContentBytes = 16 << 20
	maxFrameAttempts     = 3
)

func (store *Postgres) ClaimFrame(ctx context.Context, lease NodeLease, claimFor time.Duration) (FrameWork, error) {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 || claimFor <= 0 {
		return FrameWork{}, errors.New("frame claim is incomplete")
	}
	if err := store.requireNodeLease(ctx, lease); err != nil {
		return FrameWork{}, err
	}
	var work FrameWork
	err := store.Pool.QueryRow(ctx, `WITH candidate AS (
		SELECT j.session_id FROM flowbaton_frame_jobs j
		JOIN flowbaton_sessions s USING(session_id)
		JOIN flowbaton_nodes n ON n.node_id=$1 AND n.worker_epoch=$2 AND n.ready=true AND n.lease_expires_at>clock_timestamp()
		WHERE j.owner_node_id=$1 AND j.owner_worker_epoch=$2
		  AND s.owner_node_id=$1 AND s.owner_worker_epoch=$2 AND s.status='active'
		  AND s.lease_expires_at>clock_timestamp()
		  AND (j.state='pending' OR j.claim_expires_at<clock_timestamp())
		ORDER BY j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE flowbaton_frame_jobs j SET state='claimed',claimed_by=$1,claimed_worker_epoch=$2,
			claim_generation=j.claim_generation+1,claim_expires_at=clock_timestamp()+$3::interval
		FROM candidate c WHERE j.session_id=c.session_id
		RETURNING j.session_id,j.claimed_by,j.claimed_worker_epoch,j.claim_generation
	)
	SELECT c.session_id,s.tenant_id,s.resource_id,s.lease_generation,s.fencing_token_sha256,s.stream_epoch,
		c.claimed_by,c.claimed_worker_epoch,c.claim_generation
	FROM claimed c JOIN flowbaton_sessions s USING(session_id)`, lease.NodeID, lease.WorkerEpoch, durationInterval(claimFor)).Scan(
		&work.SessionID, &work.TenantID, &work.ResourceID, &work.Generation,
		&work.FencingTokenSHA256, &work.StreamEpoch, &work.ClaimedBy, &work.WorkerEpoch, &work.ClaimGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		if leaseErr := store.requireNodeLease(ctx, lease); leaseErr != nil {
			return FrameWork{}, leaseErr
		}
		return FrameWork{}, ErrNotFound
	}
	return work, err
}

func (store *Postgres) CompleteFrame(ctx context.Context, work FrameWork, frame FrameData) error {
	if len(frame.Content) == 0 || len(frame.Content) > maxFrameContentBytes || frame.Width < 1 || frame.Height < 1 ||
		(frame.ContentType != "image/png" && frame.ContentType != "image/jpeg") || !validFrameOrientation(frame.Orientation) {
		return ErrInvalidArgument
	}
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease := NodeLease{NodeID: work.ClaimedBy, WorkerEpoch: work.WorkerEpoch}
	if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	if sessionExpiredAt(session, now) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrExpired
	}
	if session.OwnerNodeID != work.ClaimedBy || session.OwnerWorkerEpoch != work.WorkerEpoch || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch || session.Status != "active" {
		return ErrFenced
	}
	if err := requireCurrentDeviceTx(ctx, tx, session); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_frame_jobs
		WHERE session_id=$1 AND state='claimed' AND claimed_by=$2 AND claimed_worker_epoch=$3 AND claim_generation=$4
		FOR UPDATE)`, work.SessionID, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrFenced
	}
	var eventSequence, frameSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, work.SessionID).Scan(&eventSequence); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX((payload->>'frame_sequence')::bigint),0)+1 FROM flowbaton_events WHERE session_id=$1 AND type='frame' AND (payload->>'stream_epoch')::bigint=$2`, work.SessionID, work.StreamEpoch).Scan(&frameSequence); err != nil {
		return err
	}
	digest := sha256.Sum256(frame.Content)
	contentSHA256 := fmt.Sprintf("%x", digest)
	payload := map[string]any{"orientation": frame.Orientation, "width": frame.Width, "height": frame.Height,
		"content_sha256": contentSHA256, "queue_depth": 0, "dropped_since_previous": 0,
		"stream_epoch": work.StreamEpoch, "frame_sequence": frameSequence}
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_content WHERE session_id=$1`, work.SessionID); err != nil {
		return err
	}
	expiresAt := session.LeaseExpiresAt
	if session.BindingExpiresAt.Before(expiresAt) {
		expiresAt = session.BindingExpiresAt
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_content(
		session_id,stream_epoch,frame_sequence,content_sha256,content_type,content,created_at,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, work.SessionID, work.StreamEpoch, frameSequence,
		contentSHA256, frame.ContentType, frame.Content, now, expiresAt); err != nil {
		return err
	}
	if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "frame", payload, now); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_jobs
		WHERE session_id=$1 AND state='claimed' AND claimed_by=$2 AND claimed_worker_epoch=$3 AND claim_generation=$4`, work.SessionID, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenced
	}
	return tx.Commit(ctx)
}

func (store *Postgres) FailFrame(ctx context.Context, work FrameWork, code string, retryable bool, message string) error {
	return store.finishFrameWithError(ctx, work, typedErrorPayload(code, retryable, message))
}

func (store *Postgres) finishFrameWithError(ctx context.Context, work FrameWork, payload map[string]any) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease := NodeLease{NodeID: work.ClaimedBy, WorkerEpoch: work.WorkerEpoch}
	if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	if sessionExpiredAt(session, now) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrExpired
	}
	if session.OwnerNodeID != work.ClaimedBy || session.OwnerWorkerEpoch != work.WorkerEpoch || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch || session.Status != "active" {
		return ErrFenced
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_frame_jobs
		WHERE session_id=$1 AND state='claimed' AND claimed_by=$2 AND claimed_worker_epoch=$3 AND claim_generation=$4
		FOR UPDATE)`, work.SessionID, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrFenced
	}
	var eventSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, work.SessionID).Scan(&eventSequence); err != nil {
		return err
	}
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "error", payload, now); err != nil {
		return err
	}
	nextState := "blocked"
	if retryable, _ := payload["retryable"].(bool); retryable && work.ClaimGeneration < maxFrameAttempts {
		nextState = "pending"
	}
	command, err := tx.Exec(ctx, `UPDATE flowbaton_frame_jobs SET state=$5,claimed_by=NULL,
		claimed_worker_epoch=NULL,claim_expires_at=NULL,created_at=clock_timestamp()
		WHERE session_id=$1 AND state='claimed' AND claimed_by=$2 AND claimed_worker_epoch=$3 AND claim_generation=$4`,
		work.SessionID, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration, nextState)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenced
	}
	return tx.Commit(ctx)
}

func (store *Postgres) ClaimInput(ctx context.Context, lease NodeLease, claimFor time.Duration) (InputWork, error) {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 || claimFor <= 0 {
		return InputWork{}, errors.New("input claim is incomplete")
	}
	if err := store.requireNodeLease(ctx, lease); err != nil {
		return InputWork{}, err
	}
	var work InputWork
	err := store.Pool.QueryRow(ctx, `WITH candidate AS (
		SELECT j.session_id,j.request_sequence FROM flowbaton_input_jobs j
		JOIN flowbaton_sessions s USING(session_id)
		JOIN flowbaton_nodes n ON n.node_id=$1 AND n.worker_epoch=$2 AND n.ready=true AND n.lease_expires_at>clock_timestamp()
		WHERE j.owner_node_id=$1 AND j.owner_worker_epoch=$2
		  AND s.owner_node_id=$1 AND s.owner_worker_epoch=$2 AND s.status='active'
		  AND s.lease_expires_at>clock_timestamp()
		  AND (j.state='pending' OR (j.state='claimed' AND j.claim_expires_at<clock_timestamp()))
		  AND NOT EXISTS (SELECT 1 FROM flowbaton_frame_jobs f WHERE f.session_id=j.session_id)
		  AND NOT EXISTS (SELECT 1 FROM flowbaton_input_jobs active WHERE active.session_id=j.session_id AND active.request_sequence!=j.request_sequence AND active.state IN ('claimed','executing'))
		ORDER BY j.created_at,j.request_sequence FOR UPDATE OF j SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE flowbaton_input_jobs j SET state='claimed',claimed_by=$1,claimed_worker_epoch=$2,
			claim_generation=j.claim_generation+1,claim_expires_at=clock_timestamp()+$3::interval
		FROM candidate c WHERE j.session_id=c.session_id AND j.request_sequence=c.request_sequence
		RETURNING j.*
	)
	SELECT c.session_id,c.tenant_id,s.resource_id,c.request_sequence,c.request_id,c.idempotency_key,
	 c.lease_generation,c.fencing_token_sha256,c.stream_epoch,c.frame_sequence,c.command,c.command_payload,
	 c.claimed_by,c.claimed_worker_epoch,c.claim_generation
	FROM claimed c JOIN flowbaton_sessions s USING(session_id)`, lease.NodeID, lease.WorkerEpoch, durationInterval(claimFor)).Scan(
		&work.SessionID, &work.TenantID, &work.ResourceID, &work.RequestSequence,
		&work.RequestID, &work.IdempotencyKey, &work.Generation, &work.FencingTokenSHA256,
		&work.StreamEpoch, &work.FrameSequence, &work.Command, &work.CommandPayload,
		&work.ClaimedBy, &work.WorkerEpoch, &work.ClaimGeneration)
	if errors.Is(err, pgx.ErrNoRows) {
		if leaseErr := store.requireNodeLease(ctx, lease); leaseErr != nil {
			return InputWork{}, leaseErr
		}
		return InputWork{}, ErrNotFound
	}
	return work, err
}

// StartInput is the durable no-return point. A stale claimed job may be retried;
// a stale executing job is never re-executed because the external mutation may
// already have happened.
func (store *Postgres) StartInput(ctx context.Context, work InputWork) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease := NodeLease{NodeID: work.ClaimedBy, WorkerEpoch: work.WorkerEpoch}
	if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	if sessionExpiredAt(session, now) {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return ErrExpired
	}
	if session.OwnerNodeID != work.ClaimedBy || session.OwnerWorkerEpoch != work.WorkerEpoch || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch || session.Status != "active" {
		return ErrFenced
	}
	if err := requireCurrentDeviceTx(ctx, tx, session); err != nil {
		return err
	}
	var latestEpoch, latestFrame int64
	if err := tx.QueryRow(ctx, `SELECT (payload->>'stream_epoch')::bigint,(payload->>'frame_sequence')::bigint FROM flowbaton_events WHERE session_id=$1 AND type='frame' ORDER BY sequence DESC LIMIT 1`, work.SessionID).Scan(&latestEpoch, &latestFrame); err != nil {
		return err
	}
	if latestEpoch != work.StreamEpoch || latestFrame != work.FrameSequence {
		return ErrFenced
	}
	command, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='executing',started_at=clock_timestamp(),claim_expires_at=NULL
		WHERE session_id=$1 AND request_sequence=$2 AND state='claimed' AND claimed_by=$3
		  AND claimed_worker_epoch=$4 AND claim_generation=$5`, work.SessionID, work.RequestSequence, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenced
	}
	return tx.Commit(ctx)
}

func (store *Postgres) CompleteInput(ctx context.Context, work InputWork, result string, latency time.Duration, failure *ExecutionFailure) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	lease := NodeLease{NodeID: work.ClaimedBy, WorkerEpoch: work.WorkerEpoch}
	if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
		return err
	}
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	if session.OwnerNodeID != work.ClaimedBy || session.OwnerWorkerEpoch != work.WorkerEpoch || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch {
		return ErrFenced
	}
	var state, requestHash, claimedBy string
	var claimedEpoch, claimGeneration *int64
	if err := tx.QueryRow(ctx, `SELECT state,request_hash,COALESCE(claimed_by,''),claimed_worker_epoch,claim_generation
		FROM flowbaton_input_jobs WHERE session_id=$1 AND request_sequence=$2 FOR UPDATE`, work.SessionID, work.RequestSequence).Scan(&state, &requestHash, &claimedBy, &claimedEpoch, &claimGeneration); err != nil {
		return err
	}
	if claimedEpoch == nil || claimGeneration == nil || claimedBy != work.ClaimedBy || *claimedEpoch != work.WorkerEpoch || *claimGeneration != work.ClaimGeneration {
		return ErrFenced
	}
	if state != "executing" {
		return ErrInvalidState
	}
	now, err := databaseNowTx(ctx, tx)
	if err != nil {
		return err
	}
	expired := sessionExpiredAt(session, now)
	if expired {
		result = "rejected"
		failure = &ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "device input outcome is unknown after session expiry"}
	} else if session.Status == "cancelled" {
		result = "rejected"
		failure = &ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "device input outcome is unknown after session cancellation"}
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, work.SessionID).Scan(&sequence); err != nil {
		return err
	}
	kind := "input_ack"
	payload := map[string]any{"input_id": work.RequestID, "idempotency_key": work.IdempotencyKey, "based_on_stream_epoch": work.StreamEpoch, "based_on_frame_sequence": work.FrameSequence, "latency_ms": max(latency.Milliseconds(), 0), "result": result}
	if failure != nil {
		kind = "error"
		payload = typedErrorPayload(failure.Code, failure.Retryable, failure.SafeMessage)
	}
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	if _, err := persistEvent(ctx, tx, session, sequence, eventID, kind, payload, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence) SELECT tenant_id,principal_id,idempotency_key,request_hash,session_id,$3 FROM flowbaton_input_jobs WHERE session_id=$1 AND request_sequence=$2`, work.SessionID, work.RequestSequence, sequence); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='done',completed_event_sequence=$6
		WHERE session_id=$1 AND request_sequence=$2 AND state='executing' AND claimed_by=$3
		  AND claimed_worker_epoch=$4 AND claim_generation=$5`, work.SessionID, work.RequestSequence, work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration, sequence)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenced
	}
	if expired {
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			return err
		}
	} else if session.Status == "active" {
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,owner_worker_epoch,created_at) VALUES($1,$2,$3,$4)
			ON CONFLICT(session_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,owner_worker_epoch=excluded.owner_worker_epoch,
			state='pending',claimed_by=NULL,claimed_worker_epoch=NULL,claim_expires_at=NULL,created_at=excluded.created_at`, work.SessionID, work.ClaimedBy, work.WorkerEpoch, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify('flowbaton_work',$1)`, work.ClaimedBy); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

type ExecutionFailure struct {
	Code        string
	Retryable   bool
	SafeMessage string
}

func (store *Postgres) RecoverAmbiguousInputs(ctx context.Context, lease NodeLease, olderThan time.Duration) (int64, error) {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 || olderThan <= 0 {
		return 0, errors.New("input recovery is incomplete")
	}
	var recovered int64
	for {
		tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return recovered, err
		}
		if err := requireNodeLeaseTx(ctx, tx, lease); err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		var work InputWork
		var requestHash string
		err = tx.QueryRow(ctx, `SELECT j.session_id,j.request_sequence,j.tenant_id,s.resource_id,j.request_id,j.idempotency_key,
			j.request_hash,j.lease_generation,j.fencing_token_sha256,j.stream_epoch,j.frame_sequence,j.command,
			j.command_payload,j.claimed_by,j.claimed_worker_epoch,j.claim_generation,j.started_at
			FROM flowbaton_input_jobs j JOIN flowbaton_sessions s USING(session_id)
			WHERE j.owner_node_id=$1 AND j.owner_worker_epoch=$2 AND j.state='executing'
			  AND j.claimed_worker_epoch<$2 AND j.started_at<clock_timestamp()-$3::interval
			  AND s.owner_node_id=$1 AND s.owner_worker_epoch=$2 AND s.status IN ('active','disconnected','cancelled')
			ORDER BY j.started_at FOR UPDATE OF j,s SKIP LOCKED LIMIT 1`, lease.NodeID, lease.WorkerEpoch, durationInterval(olderThan)).Scan(
			&work.SessionID, &work.RequestSequence, &work.TenantID, &work.ResourceID, &work.RequestID,
			&work.IdempotencyKey, &requestHash, &work.Generation, &work.FencingTokenSHA256, &work.StreamEpoch,
			&work.FrameSequence, &work.Command, &work.CommandPayload, &work.ClaimedBy, &work.WorkerEpoch,
			&work.ClaimGeneration, &work.StartedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return recovered, nil
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		if session.OwnerNodeID != lease.NodeID || session.OwnerWorkerEpoch != lease.WorkerEpoch ||
			session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 {
			_ = tx.Rollback(ctx)
			return recovered, ErrFenced
		}
		var sequence int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, work.SessionID).Scan(&sequence); err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		eventID, err := secureID("event")
		if err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		var recoveredAt time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&recoveredAt); err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		payload := typedErrorPayload("DEVICE_UNAVAILABLE", false, "device input outcome is unknown after worker restart")
		if _, err := persistEvent(ctx, tx, session, sequence, eventID, "error", payload, recoveredAt.UTC()); err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence)
			SELECT tenant_id,principal_id,idempotency_key,$3,session_id,$4 FROM flowbaton_input_jobs
			WHERE session_id=$1 AND request_sequence=$2`, work.SessionID, work.RequestSequence, requestHash, sequence); err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		command, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='done',completed_event_sequence=$6
			WHERE session_id=$1 AND request_sequence=$2 AND state='executing' AND claimed_by=$3
			  AND claimed_worker_epoch=$4 AND claim_generation=$5`, work.SessionID, work.RequestSequence,
			work.ClaimedBy, work.WorkerEpoch, work.ClaimGeneration, sequence)
		if err != nil {
			_ = tx.Rollback(ctx)
			return recovered, err
		}
		if command.RowsAffected() != 1 {
			_ = tx.Rollback(ctx)
			return recovered, ErrFenced
		}
		if session.Status == "active" {
			if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(
				session_id,owner_node_id,owner_worker_epoch,created_at) VALUES($1,$2,$3,clock_timestamp())
				ON CONFLICT(session_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,
				owner_worker_epoch=excluded.owner_worker_epoch,state='pending',claimed_by=NULL,
				claimed_worker_epoch=NULL,claim_expires_at=NULL,created_at=excluded.created_at`,
				work.SessionID, lease.NodeID, lease.WorkerEpoch); err != nil {
				_ = tx.Rollback(ctx)
				return recovered, err
			}
		}
		if err := tx.Commit(ctx); err != nil {
			return recovered, err
		}
		recovered++
	}
}

func (store *Postgres) WaitForExecutionQuiescence(ctx context.Context, lease NodeLease, olderThan, poll time.Duration) error {
	if olderThan <= 0 || poll <= 0 {
		return ErrInvalidArgument
	}
	for {
		if _, err := store.RecoverAmbiguousInputs(ctx, lease, olderThan); err != nil {
			return err
		}
		var remaining int64
		if err := store.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM flowbaton_input_jobs
			WHERE owner_node_id=$1 AND owner_worker_epoch=$2 AND state='executing'
			AND claimed_worker_epoch<$2`, lease.NodeID, lease.WorkerEpoch).Scan(&remaining); err != nil {
			return err
		}
		if remaining == 0 {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (store *Postgres) WaitInputActive(ctx context.Context, work InputWork, poll time.Duration) (bool, error) {
	if poll <= 0 {
		poll = 100 * time.Millisecond
	}
	for {
		var active bool
		err := store.Pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM flowbaton_input_jobs j
			JOIN flowbaton_sessions s USING(session_id)
			JOIN flowbaton_devices d ON d.tenant_id=s.tenant_id AND d.resource_id=s.resource_id
			JOIN flowbaton_nodes n ON n.node_id=s.owner_node_id AND n.worker_epoch=s.owner_worker_epoch
			WHERE j.session_id=$1 AND j.request_sequence=$2 AND j.state='executing'
			AND j.claimed_by=$3 AND j.claimed_worker_epoch=$4 AND j.claim_generation=$5
			AND s.status IN ('active','disconnected') AND s.lease_generation=$6
			AND s.fencing_token_sha256=$7 AND s.owner_node_id=$3 AND s.owner_worker_epoch=$4
			AND j.owner_node_id=$3 AND j.owner_worker_epoch=$4 AND d.current_session_id=s.session_id
			AND s.lease_expires_at>clock_timestamp() AND s.binding_expires_at>clock_timestamp()
			AND n.lease_expires_at>clock_timestamp()
		)`, work.SessionID, work.RequestSequence, work.ClaimedBy, work.WorkerEpoch,
			work.ClaimGeneration, work.Generation, work.FencingTokenSHA256).Scan(&active)
		if err != nil || !active {
			return active, err
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}

func (store *Postgres) FrameContent(ctx context.Context, input FrameContentRequest) (FrameContent, error) {
	if input.SessionID == "" || input.TenantID == "" || input.PrincipalID == "" ||
		input.ChannelBindingSHA256 == "" || input.RequestNonce == "" || input.BindingExpiresAt.IsZero() ||
		input.Generation < 1 || input.FencingTokenSHA256 == "" ||
		input.StreamEpoch < 1 || input.FrameSequence < 1 || len(input.ContentSHA256) != 64 {
		return FrameContent{}, ErrInvalidArgument
	}
	var result FrameContent
	err := store.Pool.QueryRow(ctx, `SELECT f.content,f.content_type,f.content_sha256
		FROM flowbaton_frame_content f JOIN flowbaton_sessions s USING(session_id)
		JOIN flowbaton_devices d ON d.tenant_id=s.tenant_id AND d.resource_id=s.resource_id
		WHERE f.session_id=$1 AND s.tenant_id=$2 AND s.principal_id=$3
		AND s.channel_binding_sha256=$4 AND s.request_nonce=$5 AND s.binding_expires_at=$6
		AND s.lease_generation=$7 AND s.fencing_token_sha256=$8
		AND f.stream_epoch=$9 AND f.frame_sequence=$10 AND f.content_sha256=$11
		AND f.expires_at>clock_timestamp() AND s.lease_expires_at>clock_timestamp()
		AND s.binding_expires_at>clock_timestamp() AND s.status IN ('active','disconnected','cancelled')
		AND d.current_session_id=s.session_id`, input.SessionID, input.TenantID, input.PrincipalID,
		input.ChannelBindingSHA256, input.RequestNonce, input.BindingExpiresAt, input.Generation,
		input.FencingTokenSHA256, input.StreamEpoch,
		input.FrameSequence, input.ContentSHA256).Scan(&result.Content, &result.ContentType, &result.SHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return FrameContent{}, ErrNotFound
	}
	if err != nil {
		return FrameContent{}, err
	}
	if len(result.Content) == 0 || len(result.Content) > maxFrameContentBytes {
		return FrameContent{}, ErrInvalidState
	}
	return result, nil
}

func (store *Postgres) ExpireSessions(ctx context.Context, limit int) (int64, error) {
	if limit < 1 {
		limit = 64
	}
	var expired int64
	for expired < int64(limit) {
		tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			return expired, err
		}
		var tenantID, sessionID string
		err = tx.QueryRow(ctx, `SELECT tenant_id,session_id FROM flowbaton_sessions
			WHERE status IN ('active','disconnected','cancelled')
			AND (lease_expires_at<=clock_timestamp() OR binding_expires_at<=clock_timestamp())
			ORDER BY LEAST(lease_expires_at,binding_expires_at)
			FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&tenantID, &sessionID)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return expired, nil
		}
		if err != nil {
			_ = tx.Rollback(ctx)
			return expired, err
		}
		session, err := loadSessionForUpdate(ctx, tx, tenantID, sessionID)
		if err != nil {
			_ = tx.Rollback(ctx)
			return expired, err
		}
		now, err := databaseNowTx(ctx, tx)
		if err != nil {
			_ = tx.Rollback(ctx)
			return expired, err
		}
		if err := expireSessionTx(ctx, tx, session, now, "lease_expired"); err != nil {
			_ = tx.Rollback(ctx)
			return expired, err
		}
		if err := tx.Commit(ctx); err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

func requireNodeLeaseTx(ctx context.Context, tx pgx.Tx, lease NodeLease) error {
	if lease.NodeID == "" || lease.WorkerEpoch <= 0 {
		return ErrFenced
	}
	var live bool
	if err := tx.QueryRow(ctx, `SELECT worker_epoch=$2 AND lease_expires_at>clock_timestamp()
		FROM flowbaton_nodes WHERE node_id=$1 FOR SHARE`, lease.NodeID, lease.WorkerEpoch).Scan(&live); errors.Is(err, pgx.ErrNoRows) {
		return ErrFenced
	} else if err != nil {
		return err
	}
	if !live {
		return ErrFenced
	}
	return nil
}

func requireCurrentDeviceTx(ctx context.Context, tx pgx.Tx, session Session) error {
	var current bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM flowbaton_devices
		WHERE tenant_id=$1 AND resource_id=$2 AND current_session_id=$3
		AND owner_node_id=$4 AND owner_worker_epoch=$5)`, session.TenantID, session.ResourceID,
		session.SessionID, session.OwnerNodeID, session.OwnerWorkerEpoch).Scan(&current); err != nil {
		return err
	}
	if !current {
		return ErrFenced
	}
	return nil
}

func (store *Postgres) WaitEvents(ctx context.Context, tenantID, principalID, sessionID string, after int64, wait time.Duration) ([]devicesessionv1.Event, bool, error) {
	deadline := time.Now().Add(wait)
	for {
		events, terminal, err := store.eventBatch(ctx, tenantID, principalID, sessionID, after)
		if err != nil || len(events) != 0 || terminal || wait <= 0 {
			return events, terminal, err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false, nil
		}
		connection, err := store.Pool.Acquire(ctx)
		if err != nil {
			return nil, false, err
		}
		if _, err = connection.Exec(ctx, `LISTEN flowbaton_events`); err == nil {
			waitCtx, cancel := context.WithTimeout(ctx, min(remaining, 5*time.Second))
			_, err = connection.Conn().WaitForNotification(waitCtx)
			cancel()
		}
		_, _ = connection.Exec(context.WithoutCancel(ctx), `UNLISTEN flowbaton_events`)
		connection.Release()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			return nil, false, err
		}
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
	}
}

func (store *Postgres) WaitForWork(ctx context.Context, nodeID string, wait time.Duration) error {
	if nodeID == "" || wait <= 0 {
		return nil
	}
	connection, err := store.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `LISTEN flowbaton_work`); err != nil {
		return err
	}
	defer connection.Exec(context.WithoutCancel(ctx), `UNLISTEN flowbaton_work`) //nolint:errcheck
	waitCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		notification, err := connection.Conn().WaitForNotification(waitCtx)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if err != nil {
			return err
		}
		if notification.Payload == nodeID {
			return nil
		}
	}
}

func (store *Postgres) eventBatch(ctx context.Context, tenantID, principalID, sessionID string, after int64) ([]devicesessionv1.Event, bool, error) {
	rows, err := store.Pool.Query(ctx, `SELECT e.sequence,e.event_id,e.type,e.created_at,e.lease_generation,e.fencing_token_sha256,e.payload,s.status FROM flowbaton_events e JOIN flowbaton_sessions s USING(session_id) WHERE s.tenant_id=$1 AND s.principal_id=$2 AND e.session_id=$3 AND e.sequence>$4 ORDER BY e.sequence LIMIT $5`, tenantID, principalID, sessionID, after, maxEventBatch)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var events []devicesessionv1.Event
	terminal := false
	for rows.Next() {
		var event devicesessionv1.Event
		var at time.Time
		var status string
		if err := rows.Scan(&event.Sequence, &event.EventID, &event.Type, &at, &event.LeaseGeneration, &event.FencingTokenSHA256, &event.Data, &status); err != nil {
			return nil, false, err
		}
		event.Timestamp = at.UTC().Format(time.RFC3339Nano)
		events = append(events, event)
		terminal = status == "released" && event.Type == "released"
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(events) == 0 {
		var status string
		if err := store.Pool.QueryRow(ctx, `SELECT status FROM flowbaton_sessions WHERE tenant_id=$1 AND principal_id=$2 AND session_id=$3`, tenantID, principalID, sessionID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
			return nil, false, ErrNotFound
		} else if err != nil {
			return nil, false, err
		}
		terminal = status == "released"
	}
	return events, terminal, nil
}

func durationInterval(duration time.Duration) string {
	return fmt.Sprintf("%d microseconds", duration.Microseconds())
}

func typedErrorPayload(code string, retryable bool, message string) map[string]any {
	if code == "" {
		code = "DEVICE_UNAVAILABLE"
	}
	if message == "" {
		message = "device operation failed"
	}
	return map[string]any{"code": code, "retryable": retryable, "safe_message": message}
}

func validFrameOrientation(value string) bool {
	switch value {
	case "portrait", "portrait-upside-down", "landscape-left", "landscape-right":
		return true
	default:
		return false
	}
}

func sessionExpiredAt(session Session, at time.Time) bool {
	return !at.Before(session.LeaseExpiresAt) || !at.Before(session.BindingExpiresAt)
}
