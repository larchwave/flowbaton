package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
)

const maxEventBatch = 64

func (store *Postgres) ClaimFrame(ctx context.Context, nodeID string, claimFor time.Duration) (FrameWork, error) {
	if nodeID == "" || claimFor <= 0 {
		return FrameWork{}, errors.New("frame claim is incomplete")
	}
	var work FrameWork
	err := store.Pool.QueryRow(ctx, `WITH candidate AS (
		SELECT j.session_id FROM flowbaton_frame_jobs j
		JOIN flowbaton_sessions s USING(session_id)
		WHERE j.owner_node_id=$1 AND s.owner_node_id=$1 AND s.status='active'
		  AND s.lease_expires_at>clock_timestamp()
		  AND (j.state='pending' OR j.claim_expires_at<clock_timestamp())
		ORDER BY j.created_at FOR UPDATE OF j SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE flowbaton_frame_jobs j SET state='claimed',claimed_by=$1,claim_expires_at=clock_timestamp()+$2::interval
		FROM candidate c WHERE j.session_id=c.session_id RETURNING j.session_id,j.claimed_by
	)
	SELECT c.session_id,s.tenant_id,s.resource_id,s.lease_generation,s.fencing_token_sha256,s.stream_epoch,c.claimed_by
	FROM claimed c JOIN flowbaton_sessions s USING(session_id)`, nodeID, durationInterval(claimFor)).Scan(
		&work.SessionID, &work.TenantID, &work.ResourceID, &work.Generation,
		&work.FencingTokenSHA256, &work.StreamEpoch, &work.ClaimedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return FrameWork{}, ErrNotFound
	}
	return work, err
}

func (store *Postgres) CompleteFrame(ctx context.Context, work FrameWork, payload map[string]any, at time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	if session.OwnerNodeID != work.ClaimedBy || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch || session.Status != "active" {
		return ErrFenced
	}
	var claimedBy string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(claimed_by,'') FROM flowbaton_frame_jobs WHERE session_id=$1 AND state='claimed' FOR UPDATE`, work.SessionID).Scan(&claimedBy); err != nil {
		return err
	}
	if claimedBy != work.ClaimedBy {
		return ErrFenced
	}
	var eventSequence, frameSequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM flowbaton_events WHERE session_id=$1`, work.SessionID).Scan(&eventSequence); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX((payload->>'frame_sequence')::bigint),0)+1 FROM flowbaton_events WHERE session_id=$1 AND type='frame' AND (payload->>'stream_epoch')::bigint=$2`, work.SessionID, work.StreamEpoch).Scan(&frameSequence); err != nil {
		return err
	}
	payload["stream_epoch"] = work.StreamEpoch
	payload["frame_sequence"] = frameSequence
	eventID, err := secureID("event")
	if err != nil {
		return err
	}
	if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "frame", payload, at.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_jobs WHERE session_id=$1 AND claimed_by=$2`, work.SessionID, work.ClaimedBy); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Postgres) FailFrame(ctx context.Context, work FrameWork, code string, retryable bool, message string, at time.Time) error {
	return store.finishFrameWithError(ctx, work, typedErrorPayload(code, retryable, message), at)
}

func (store *Postgres) finishFrameWithError(ctx context.Context, work FrameWork, payload map[string]any, at time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	if session.OwnerNodeID != work.ClaimedBy || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 {
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
	if _, err := persistEvent(ctx, tx, session, eventSequence, eventID, "error", payload, at.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM flowbaton_frame_jobs WHERE session_id=$1 AND claimed_by=$2`, work.SessionID, work.ClaimedBy); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (store *Postgres) ClaimInput(ctx context.Context, nodeID string, claimFor time.Duration) (InputWork, error) {
	if nodeID == "" || claimFor <= 0 {
		return InputWork{}, errors.New("input claim is incomplete")
	}
	var work InputWork
	err := store.Pool.QueryRow(ctx, `WITH candidate AS (
		SELECT j.session_id,j.request_sequence FROM flowbaton_input_jobs j
		JOIN flowbaton_sessions s USING(session_id)
		WHERE j.owner_node_id=$1 AND s.owner_node_id=$1 AND s.status='active'
		  AND s.lease_expires_at>clock_timestamp()
		  AND (j.state='pending' OR (j.state='claimed' AND j.claim_expires_at<clock_timestamp()))
		  AND NOT EXISTS (SELECT 1 FROM flowbaton_frame_jobs f WHERE f.session_id=j.session_id)
		  AND NOT EXISTS (SELECT 1 FROM flowbaton_input_jobs active WHERE active.session_id=j.session_id AND active.request_sequence!=j.request_sequence AND active.state IN ('claimed','executing'))
		ORDER BY j.created_at,j.request_sequence FOR UPDATE OF j SKIP LOCKED LIMIT 1
	), claimed AS (
		UPDATE flowbaton_input_jobs j SET state='claimed',claimed_by=$1,claim_expires_at=clock_timestamp()+$2::interval
		FROM candidate c WHERE j.session_id=c.session_id AND j.request_sequence=c.request_sequence
		RETURNING j.*
	)
	SELECT c.session_id,c.tenant_id,s.resource_id,c.request_sequence,c.request_id,c.idempotency_key,
	 c.lease_generation,c.fencing_token_sha256,c.stream_epoch,c.frame_sequence,c.command,c.command_payload,c.claimed_by
	FROM claimed c JOIN flowbaton_sessions s USING(session_id)`, nodeID, durationInterval(claimFor)).Scan(
		&work.SessionID, &work.TenantID, &work.ResourceID, &work.RequestSequence,
		&work.RequestID, &work.IdempotencyKey, &work.Generation, &work.FencingTokenSHA256,
		&work.StreamEpoch, &work.FrameSequence, &work.Command, &work.CommandPayload, &work.ClaimedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return InputWork{}, ErrNotFound
	}
	return work, err
}

// StartInput is the durable no-return point. A stale claimed job may be retried;
// a stale executing job is never re-executed because the external mutation may
// already have happened.
func (store *Postgres) StartInput(ctx context.Context, work InputWork, at time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	if session.OwnerNodeID != work.ClaimedBy || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 || session.StreamEpoch != work.StreamEpoch || session.Status != "active" {
		return ErrFenced
	}
	var latestEpoch, latestFrame int64
	if err := tx.QueryRow(ctx, `SELECT (payload->>'stream_epoch')::bigint,(payload->>'frame_sequence')::bigint FROM flowbaton_events WHERE session_id=$1 AND type='frame' ORDER BY sequence DESC LIMIT 1`, work.SessionID).Scan(&latestEpoch, &latestFrame); err != nil {
		return err
	}
	if latestEpoch != work.StreamEpoch || latestFrame != work.FrameSequence {
		return ErrFenced
	}
	command, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='executing',started_at=$4,claim_expires_at=NULL WHERE session_id=$1 AND request_sequence=$2 AND state='claimed' AND claimed_by=$3`, work.SessionID, work.RequestSequence, work.ClaimedBy, at.UTC())
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrFenced
	}
	return tx.Commit(ctx)
}

func (store *Postgres) CompleteInput(ctx context.Context, work InputWork, result string, latency time.Duration, failure *ExecutionFailure, at time.Time) error {
	tx, err := store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	session, err := loadSessionForUpdate(ctx, tx, work.TenantID, work.SessionID)
	if err != nil {
		return err
	}
	if session.OwnerNodeID != work.ClaimedBy || session.Generation != work.Generation || session.FencingTokenSHA256 != work.FencingTokenSHA256 {
		return ErrFenced
	}
	var state, requestHash string
	if err := tx.QueryRow(ctx, `SELECT state,request_hash FROM flowbaton_input_jobs WHERE session_id=$1 AND request_sequence=$2 FOR UPDATE`, work.SessionID, work.RequestSequence).Scan(&state, &requestHash); err != nil {
		return err
	}
	if state != "executing" && state != "claimed" {
		return ErrInvalidState
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
	if _, err := persistEvent(ctx, tx, session, sequence, eventID, kind, payload, at.UTC()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_idempotency(tenant_id,principal_id,idempotency_key,request_hash,session_id,event_sequence) SELECT tenant_id,principal_id,idempotency_key,request_hash,session_id,$3 FROM flowbaton_input_jobs WHERE session_id=$1 AND request_sequence=$2`, work.SessionID, work.RequestSequence, sequence); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE flowbaton_input_jobs SET state='done',completed_event_sequence=$3 WHERE session_id=$1 AND request_sequence=$2`, work.SessionID, work.RequestSequence, sequence); err != nil {
		return err
	}
	if failure == nil && session.Status == "active" {
		if _, err := tx.Exec(ctx, `INSERT INTO flowbaton_frame_jobs(session_id,owner_node_id,created_at) VALUES($1,$2,$3) ON CONFLICT(session_id) DO UPDATE SET owner_node_id=excluded.owner_node_id,state='pending',claimed_by=NULL,claim_expires_at=NULL,created_at=excluded.created_at`, work.SessionID, work.ClaimedBy, at.UTC()); err != nil {
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

func (store *Postgres) RecoverAmbiguousInputs(ctx context.Context, nodeID string, before time.Time) (int64, error) {
	rows, err := store.Pool.Query(ctx, `SELECT session_id,request_sequence,tenant_id,request_id,idempotency_key,lease_generation,fencing_token_sha256,stream_epoch,frame_sequence,command,command_payload,started_at FROM flowbaton_input_jobs WHERE owner_node_id=$1 AND state='executing' AND started_at<$2 ORDER BY started_at`, nodeID, before.UTC())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var works []InputWork
	for rows.Next() {
		var work InputWork
		if err := rows.Scan(&work.SessionID, &work.RequestSequence, &work.TenantID, &work.RequestID, &work.IdempotencyKey, &work.Generation, &work.FencingTokenSHA256, &work.StreamEpoch, &work.FrameSequence, &work.Command, &work.CommandPayload, &work.StartedAt); err != nil {
			return 0, err
		}
		work.ClaimedBy = nodeID
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var recovered int64
	for _, work := range works {
		failure := &ExecutionFailure{Code: "DEVICE_UNAVAILABLE", Retryable: false, SafeMessage: "device input outcome is unknown after worker restart"}
		if err := store.CompleteInput(ctx, work, "rejected", 0, failure, time.Now().UTC()); err == nil {
			recovered++
		} else if !errors.Is(err, ErrFenced) && !errors.Is(err, ErrInvalidState) {
			return recovered, err
		}
	}
	return recovered, nil
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

func framePayload(data []byte, orientation string, width, height int) map[string]any {
	return map[string]any{"orientation": orientation, "width": width, "height": height, "content_sha256": requestHash(json.RawMessage(data)), "queue_depth": 0, "dropped_since_previous": 0}
}
