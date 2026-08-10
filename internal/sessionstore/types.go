// Package sessionstore persists the fenced DeviceSession state machine.
package sessionstore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
)

var (
	ErrNotFound        = errors.New("resource not found")
	ErrBusy            = errors.New("resource already leased")
	ErrFenced          = errors.New("stale lease generation or fence")
	ErrConflict        = errors.New("idempotency key conflict")
	ErrExpired         = errors.New("binding or lease expired")
	ErrInvalidState    = errors.New("invalid session state transition")
	ErrIdentityRevoked = errors.New("identity mapping is revoked")
	ErrInvalidArgument = errors.New("invalid request argument")
	ErrBackpressure    = errors.New("runtime backpressure limit reached")
)

type Identity struct {
	CertificateFingerprint string     `json:"certificate_fingerprint_sha256"`
	TenantID               string     `json:"tenant_id"`
	PrincipalID            string     `json:"principal_id"`
	RevokedAt              *time.Time `json:"revoked_at,omitempty"`
}

// NodeLease identifies one live worker process. WorkerEpoch changes whenever
// an expired node identity is taken over, fencing every operation issued by an
// older process that used the same NodeID.
type NodeLease struct {
	NodeID      string
	WorkerEpoch int64
}

type Session struct {
	SessionID             string        `json:"session_id"`
	TenantID              string        `json:"tenant_id"`
	PrincipalID           string        `json:"principal_id"`
	AuthProfileID         string        `json:"auth_profile_id"`
	ChannelBindingSHA256  string        `json:"channel_binding_sha256"`
	RequestNonce          string        `json:"request_nonce"`
	BindingExpiresAt      time.Time     `json:"binding_expires_at"`
	ResourceID            string        `json:"resource_id"`
	OwnerNodeID           string        `json:"owner_node_id,omitempty"`
	OwnerWorkerEpoch      int64         `json:"owner_worker_epoch,omitempty"`
	LeaseID               string        `json:"lease_id"`
	Generation            int64         `json:"generation"`
	FencingTokenSHA256    string        `json:"fencing_token_sha256"`
	ReleaseIdempotencyKey string        `json:"release_idempotency_key"`
	Capabilities          []string      `json:"capabilities"`
	Status                string        `json:"status"`
	StreamEpoch           int64         `json:"stream_epoch"`
	AcquiredAt            time.Time     `json:"acquired_at"`
	LeaseExpiresAt        time.Time     `json:"lease_expires_at"`
	HeartbeatInterval     time.Duration `json:"heartbeat_interval"`
}

type AcquireInput struct {
	TenantID              string
	PrincipalID           string
	AuthProfileID         string
	ChannelBindingSHA256  string
	RequestNonce          string
	BindingExpiresAt      time.Time
	ResourceID            string
	RequestedCapabilities []string
	IdempotencyKey        string
	ReleaseIdempotencyKey string
	LeaseDuration         time.Duration
	HeartbeatInterval     time.Duration
}

type MutationInput struct {
	SessionID             string
	TenantID              string
	PrincipalID           string
	ChannelBindingSHA256  string
	RequestNonce          string
	BindingExpiresAt      time.Time
	RequestID             string
	Type                  string
	IdempotencyKey        string
	Generation            int64
	FencingTokenSHA256    string
	Payload               json.RawMessage
	CommandPayload        json.RawMessage
	RequestedExtension    time.Duration
	LastAcknowledgedEvent int64
}

type FrameData struct {
	Content     []byte
	ContentType string
	Orientation string
	Width       int
	Height      int
}

type FrameContentRequest struct {
	SessionID            string
	TenantID             string
	PrincipalID          string
	ChannelBindingSHA256 string
	RequestNonce         string
	BindingExpiresAt     time.Time
	Generation           int64
	FencingTokenSHA256   string
	StreamEpoch          int64
	FrameSequence        int64
	ContentSHA256        string
}

type FrameContent struct {
	Content     []byte
	ContentType string
	SHA256      string
}

type Result struct {
	Session Session               `json:"session"`
	Event   devicesessionv1.Event `json:"event"`
	Replay  bool                  `json:"replay"`
	Queued  bool                  `json:"queued,omitempty"`
}

type TokenWindow struct {
	IssuedAt  time.Time
	ExpiresAt time.Time
}

type InputWork struct {
	SessionID          string
	TenantID           string
	ResourceID         string
	RequestSequence    int64
	RequestID          string
	IdempotencyKey     string
	Generation         int64
	FencingTokenSHA256 string
	StreamEpoch        int64
	FrameSequence      int64
	Command            string
	CommandPayload     json.RawMessage
	ClaimedBy          string
	WorkerEpoch        int64
	ClaimGeneration    int64
	StartedAt          time.Time
}

type FrameWork struct {
	SessionID          string
	TenantID           string
	ResourceID         string
	Generation         int64
	FencingTokenSHA256 string
	StreamEpoch        int64
	ClaimedBy          string
	WorkerEpoch        int64
	ClaimGeneration    int64
}

type Store interface {
	Ping(context.Context) error
	CurrentTime(context.Context) (time.Time, error)
	ReserveTokenNonce(context.Context, string, string, time.Duration) (TokenWindow, error)
	Acquire(context.Context, AcquireInput) (Result, error)
	Apply(context.Context, MutationInput) (Result, error)
	Events(context.Context, string, string, string, int64) ([]devicesessionv1.Event, error)
	ResolveIdentity(context.Context, string) (Identity, error)
	ValidateSessionAccess(context.Context, string, string, string, string, string, time.Time, int64, string) error
}

type IdentityAdmin interface {
	UpsertIdentity(context.Context, Identity) error
	RevokeIdentity(context.Context, string, time.Time) error
	ListIdentities(context.Context) ([]Identity, error)
}
