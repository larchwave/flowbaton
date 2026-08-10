// Package server exposes the authenticated Integration and DeviceSession v1 runtime.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
	integrationv1 "github.com/larchwave/flowbaton/contracts/integration/v1"
	"github.com/larchwave/flowbaton/internal/auth"
	"github.com/larchwave/flowbaton/internal/sessionstore"
	"github.com/larchwave/flowbaton/internal/strictjson"
	"github.com/larchwave/flowbaton/internal/transport"
)

const maxRequestBody = 1 << 20
const maxDurationMilliseconds = int64((1<<63)-1) / int64(time.Millisecond)
const maxTokenNonceLength = 128

type RequestIdentity func(*http.Request) (certificateFingerprint, channelBindingSHA256 string, err error)

type Config struct {
	Store              sessionstore.Store
	Issuer             auth.Issuer
	Verifier           auth.Verifier
	Integration        integrationv1.Document
	RequestIdentity    RequestIdentity
	Readiness          func(context.Context) error
	LeaseDuration      time.Duration
	Heartbeat          time.Duration
	StreamDuration     time.Duration
	StreamHeartbeat    time.Duration
	StreamWriteTimeout time.Duration
}

type Handler struct {
	config Config
	mux    *http.ServeMux
}

func New(config Config) (*Handler, error) {
	if config.Store == nil || len(config.Issuer.PrivateKey) == 0 || len(config.Verifier.Keys) == 0 {
		return nil, errors.New("server requires store, issuer, and verifier")
	}
	if _, err := auth.NormalizeTokenTTL(config.Issuer.TTL); err != nil {
		return nil, errors.New("server requires a valid token lifetime")
	}
	integrationJSON, err := json.Marshal(config.Integration)
	if err != nil || integrationv1.ValidateJSON(integrationJSON) != nil {
		return nil, errors.New("server requires a validated Integration v1 document")
	}
	if config.RequestIdentity == nil {
		config.RequestIdentity = tlsRequestIdentity
	}
	if config.Readiness == nil {
		config.Readiness = config.Store.Ping
	}
	if config.LeaseDuration <= 0 {
		config.LeaseDuration = 60 * time.Second
	}
	if config.Heartbeat <= 0 {
		config.Heartbeat = 15 * time.Second
	}
	if config.StreamDuration <= 0 {
		config.StreamDuration = 30 * time.Second
	}
	if config.StreamHeartbeat <= 0 {
		config.StreamHeartbeat = 5 * time.Second
	}
	if config.StreamWriteTimeout <= 0 {
		config.StreamWriteTimeout = 5 * time.Second
	}
	handler := &Handler{config: config, mux: http.NewServeMux()}
	handler.mux.HandleFunc("GET /health/live", handler.live)
	handler.mux.HandleFunc("GET /health/ready", handler.ready)
	handler.mux.HandleFunc("GET /v1/integration", handler.integration)
	handler.mux.HandleFunc("POST /v1/session-tokens", handler.issueToken)
	handler.mux.HandleFunc("POST /v1/device-sessions", handler.withToken("device-session", handler.acquire))
	handler.mux.HandleFunc("POST /v1/device-sessions/{sessionID}/requests", handler.withToken("device-session", handler.request))
	handler.mux.HandleFunc("GET /v1/device-sessions/{sessionID}/events", handler.withToken("device-session", handler.events))
	handler.mux.HandleFunc("GET /v1/device-sessions/{sessionID}/frames/{streamEpoch}/{frameSequence}", handler.withToken("device-session", handler.frameContent))
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) live(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "live"})
}

func (handler *Handler) ready(writer http.ResponseWriter, request *http.Request) {
	if err := handler.config.Readiness(request.Context()); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "NOT_READY", "runtime is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (handler *Handler) integration(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, handler.config.Integration)
}

func (handler *Handler) issueToken(writer http.ResponseWriter, request *http.Request) {
	fingerprint, binding, err := handler.config.RequestIdentity(request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "mutually authenticated TLS is required")
		return
	}
	identity, err := handler.config.Store.ResolveIdentity(request.Context(), fingerprint)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "client certificate is not authorized")
		return
	}
	var input struct {
		Nonce  string   `json:"nonce"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeRequest(writer, request, &input); err != nil {
		return
	}
	if len(input.Nonce) < 16 || len(input.Nonce) > maxTokenNonceLength || len(input.Scopes) != 1 || input.Scopes[0] != "device-session" {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "nonce and the device-session scope are required")
		return
	}
	ttl, err := auth.NormalizeTokenTTL(handler.config.Issuer.TTL)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "token lifetime is invalid")
		return
	}
	window, err := handler.config.Store.ReserveTokenNonce(request.Context(), fingerprint, input.Nonce, ttl)
	if err != nil {
		if errors.Is(err, sessionstore.ErrConflict) {
			writeError(writer, http.StatusConflict, "NONCE_REPLAY", "token nonce has already been used")
		} else if errors.Is(err, sessionstore.ErrBackpressure) {
			writeError(writer, http.StatusTooManyRequests, "BACKPRESSURE_LIMIT", "too many live token nonces")
		} else {
			writeError(writer, http.StatusInternalServerError, "INTERNAL", "token nonce could not be stored")
		}
		return
	}
	token, err := handler.config.Issuer.IssueAt(auth.Claims{TenantID: identity.TenantID, PrincipalID: identity.PrincipalID, CertificateFingerprint: fingerprint, ChannelBindingSHA256: binding, Nonce: input.Nonce, Scopes: input.Scopes}, window.IssuedAt, window.ExpiresAt)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "token issuance failed")
		return
	}
	claims, err := handler.config.Verifier.VerifyAt(token, window.IssuedAt)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "issued token failed verification")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"token": token, "expires_at": time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339), "token_type": "FlowBaton"})
}

type authenticatedHandler func(http.ResponseWriter, *http.Request, auth.Claims)

func (handler *Handler) withToken(scope string, next authenticatedHandler) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		const prefix = "FlowBaton "
		authorization := request.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) {
			writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "FlowBaton session token is required")
			return
		}
		token := strings.TrimPrefix(authorization, prefix)
		if _, err := handler.config.Verifier.VerifySignature(token); err != nil {
			writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "session token is invalid")
			return
		}
		databaseNow, err := handler.config.Store.CurrentTime(request.Context())
		if err != nil {
			writeError(writer, http.StatusServiceUnavailable, "AUTHENTICATION_UNAVAILABLE", "session token time could not be validated")
			return
		}
		claims, err := handler.config.Verifier.VerifyAt(token, databaseNow)
		if err != nil || !claims.HasScope(scope) {
			writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "session token is invalid")
			return
		}
		fingerprint, binding, err := handler.config.RequestIdentity(request)
		if err != nil || fingerprint != claims.CertificateFingerprint || binding != claims.ChannelBindingSHA256 {
			writeError(writer, http.StatusUnauthorized, "CHANNEL_BINDING_MISMATCH", "session token is bound to another TLS channel")
			return
		}
		identity, err := handler.config.Store.ResolveIdentity(request.Context(), fingerprint)
		if err != nil || identity.TenantID != claims.TenantID || identity.PrincipalID != claims.PrincipalID {
			writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "certificate identity is revoked or changed")
			return
		}
		next(writer, request, claims)
	}
}

func (handler *Handler) acquire(writer http.ResponseWriter, request *http.Request, claims auth.Claims) {
	var input struct {
		ResourceID            string   `json:"resource_id"`
		Capabilities          []string `json:"capabilities"`
		IdempotencyKey        string   `json:"idempotency_key"`
		ReleaseIdempotencyKey string   `json:"release_idempotency_key"`
	}
	if err := decodeRequest(writer, request, &input); err != nil {
		return
	}
	result, err := handler.config.Store.Acquire(request.Context(), sessionstore.AcquireInput{TenantID: claims.TenantID, PrincipalID: claims.PrincipalID, AuthProfileID: devicesessionv1.RemoteCloudMacV1.ProfileID, ChannelBindingSHA256: claims.ChannelBindingSHA256, RequestNonce: claims.Nonce, BindingExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(), ResourceID: input.ResourceID, RequestedCapabilities: input.Capabilities, IdempotencyKey: input.IdempotencyKey, ReleaseIdempotencyKey: input.ReleaseIdempotencyKey, LeaseDuration: handler.config.LeaseDuration, HeartbeatInterval: handler.config.Heartbeat})
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	status := http.StatusCreated
	if result.Replay {
		status = http.StatusOK
	}
	writeJSON(writer, status, result)
}

func (handler *Handler) request(writer http.ResponseWriter, request *http.Request, claims auth.Claims) {
	var input struct {
		RequestID             string          `json:"request_id"`
		Type                  string          `json:"type"`
		IdempotencyKey        string          `json:"idempotency_key"`
		Generation            int64           `json:"generation"`
		FencingTokenSHA256    string          `json:"fencing_token_sha256"`
		Payload               json.RawMessage `json:"payload"`
		CommandPayload        json.RawMessage `json:"command_payload,omitempty"`
		RequestedExtensionMS  int64           `json:"requested_extension_ms,omitempty"`
		LastAcknowledgedEvent int64           `json:"last_acknowledged_sequence,omitempty"`
	}
	if err := decodeRequest(writer, request, &input); err != nil {
		return
	}
	requestedExtension, err := durationFromMilliseconds(input.RequestedExtensionMS)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "requested extension is outside the supported duration range")
		return
	}
	result, err := handler.config.Store.Apply(request.Context(), sessionstore.MutationInput{SessionID: request.PathValue("sessionID"), TenantID: claims.TenantID, PrincipalID: claims.PrincipalID, ChannelBindingSHA256: claims.ChannelBindingSHA256, RequestNonce: claims.Nonce, BindingExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(), RequestID: input.RequestID, Type: input.Type, IdempotencyKey: input.IdempotencyKey, Generation: input.Generation, FencingTokenSHA256: input.FencingTokenSHA256, Payload: input.Payload, CommandPayload: input.CommandPayload, RequestedExtension: requestedExtension, LastAcknowledgedEvent: input.LastAcknowledgedEvent})
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	status := http.StatusOK
	if result.Queued && !result.Replay {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, result)
}

func durationFromMilliseconds(value int64) (time.Duration, error) {
	if value < 0 || value > maxDurationMilliseconds {
		return 0, sessionstore.ErrInvalidArgument
	}
	return time.Duration(value) * time.Millisecond, nil
}

func (handler *Handler) events(writer http.ResponseWriter, request *http.Request, claims auth.Claims) {
	after, err := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	if request.URL.Query().Get("after") == "" {
		after = 0
		err = nil
	}
	if err != nil || after < 0 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "after must be a non-negative sequence")
		return
	}
	generation, fence, err := sessionFenceHeaders(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "session generation and fence headers are required")
		return
	}
	if err := handler.config.Store.ValidateSessionAccess(request.Context(), claims.TenantID, claims.PrincipalID,
		request.PathValue("sessionID"), claims.ChannelBindingSHA256, claims.Nonce,
		time.Unix(claims.ExpiresAt, 0).UTC(), generation, fence); err != nil {
		writeStoreError(writer, err)
		return
	}
	waiter, live := handler.config.Store.(interface {
		WaitEvents(context.Context, string, string, string, int64, time.Duration) ([]devicesessionv1.Event, bool, error)
	})
	var pending []devicesessionv1.Event
	terminal := false
	if live {
		pending, terminal, err = waiter.WaitEvents(request.Context(), claims.TenantID, claims.PrincipalID, request.PathValue("sessionID"), after, 0)
	} else {
		pending, err = handler.config.Store.Events(request.Context(), claims.TenantID, claims.PrincipalID, request.PathValue("sessionID"), after)
		terminal = true
	}
	if err != nil {
		if request.Context().Err() != nil {
			handler.recordDisconnect(request, claims, generation, fence)
		}
		writeStoreError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", "application/x-ndjson")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.Header().Set("Trailer", "X-FlowBaton-Next-Sequence")
	writer.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(writer)
	controller := http.NewResponseController(writer)
	deadline := time.Now().Add(handler.config.StreamDuration)
	for {
		events := pending
		pending = nil
		if len(events) == 0 && !terminal && live {
			wait := min(handler.config.StreamHeartbeat, time.Until(deadline))
			if wait < 0 {
				wait = 0
			}
			events, terminal, err = waiter.WaitEvents(request.Context(), claims.TenantID, claims.PrincipalID, request.PathValue("sessionID"), after, wait)
		}
		if err != nil {
			handler.recordDisconnect(request, claims, generation, fence)
			return
		}
		if accessErr := handler.config.Store.ValidateSessionAccess(request.Context(), claims.TenantID, claims.PrincipalID,
			request.PathValue("sessionID"), claims.ChannelBindingSHA256, claims.Nonce,
			time.Unix(claims.ExpiresAt, 0).UTC(), generation, fence); accessErr != nil {
			return
		}
		for _, event := range events {
			_ = controller.SetWriteDeadline(time.Now().Add(handler.config.StreamWriteTimeout))
			if err := encoder.Encode(event); err != nil {
				handler.recordDisconnect(request, claims, generation, fence)
				return
			}
			after = int64(event.Sequence)
			writer.Header().Set("X-FlowBaton-Next-Sequence", strconv.FormatInt(after, 10))
		}
		_ = controller.SetWriteDeadline(time.Now().Add(handler.config.StreamWriteTimeout))
		if err := controller.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			handler.recordDisconnect(request, claims, generation, fence)
			return
		}
		if request.Context().Err() != nil && !terminal {
			handler.recordDisconnect(request, claims, generation, fence)
			return
		}
		if terminal || time.Now().After(deadline) {
			return
		}
	}
}

func (handler *Handler) frameContent(writer http.ResponseWriter, request *http.Request, claims auth.Claims) {
	generation, fence, err := sessionFenceHeaders(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "session generation and fence headers are required")
		return
	}
	streamEpoch, err := strconv.ParseInt(request.PathValue("streamEpoch"), 10, 64)
	if err != nil || streamEpoch < 1 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "stream epoch must be positive")
		return
	}
	frameSequence, err := strconv.ParseInt(request.PathValue("frameSequence"), 10, 64)
	if err != nil || frameSequence < 1 {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "frame sequence must be positive")
		return
	}
	digest := request.Header.Get("X-FlowBaton-Content-SHA256")
	if err := handler.config.Store.ValidateSessionAccess(request.Context(), claims.TenantID, claims.PrincipalID,
		request.PathValue("sessionID"), claims.ChannelBindingSHA256, claims.Nonce,
		time.Unix(claims.ExpiresAt, 0).UTC(), generation, fence); err != nil {
		writeStoreError(writer, err)
		return
	}
	provider, ok := handler.config.Store.(interface {
		FrameContent(context.Context, sessionstore.FrameContentRequest) (sessionstore.FrameContent, error)
	})
	if !ok {
		writeError(writer, http.StatusNotImplemented, "CAPABILITY_UNSUPPORTED", "frame content is unavailable")
		return
	}
	content, err := provider.FrameContent(request.Context(), sessionstore.FrameContentRequest{
		SessionID: request.PathValue("sessionID"), TenantID: claims.TenantID,
		PrincipalID: claims.PrincipalID, ChannelBindingSHA256: claims.ChannelBindingSHA256,
		RequestNonce: claims.Nonce, BindingExpiresAt: time.Unix(claims.ExpiresAt, 0).UTC(),
		Generation: generation, FencingTokenSHA256: fence, StreamEpoch: streamEpoch,
		FrameSequence: frameSequence, ContentSHA256: digest,
	})
	if err != nil {
		writeStoreError(writer, err)
		return
	}
	writer.Header().Set("Content-Type", content.ContentType)
	writer.Header().Set("Content-Length", strconv.Itoa(len(content.Content)))
	writer.Header().Set("ETag", `"`+content.SHA256+`"`)
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(content.Content)
}

func sessionFenceHeaders(request *http.Request) (int64, string, error) {
	generation, err := strconv.ParseInt(request.Header.Get("X-FlowBaton-Generation"), 10, 64)
	fence := request.Header.Get("X-FlowBaton-Fence")
	if err != nil || generation < 1 || fence == "" {
		return 0, "", sessionstore.ErrInvalidArgument
	}
	return generation, fence, nil
}

func (handler *Handler) recordDisconnect(request *http.Request, claims auth.Claims, generation int64, fence string) {
	marker, ok := handler.config.Store.(interface {
		MarkDisconnected(context.Context, string, string, string, string, string, time.Time, int64, string, string) error
	})
	if !ok {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 5*time.Second)
	defer cancel()
	_ = marker.MarkDisconnected(cleanupCtx, claims.TenantID, claims.PrincipalID,
		request.PathValue("sessionID"), claims.ChannelBindingSHA256, claims.Nonce,
		time.Unix(claims.ExpiresAt, 0).UTC(), generation, fence,
		"transport_interrupted")
}

func tlsRequestIdentity(request *http.Request) (string, string, error) {
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 {
		return "", "", errors.New("client certificate is required")
	}
	fingerprint, err := transport.CertificateFingerprint(request.TLS.PeerCertificates[0])
	if err != nil {
		return "", "", err
	}
	binding, err := transport.ChannelBinding(request.TLS)
	if err != nil {
		return "", "", err
	}
	return fingerprint, binding, nil
}

func decodeRequest(writer http.ResponseWriter, request *http.Request, target any) error {
	reader := http.MaxBytesReader(writer, request.Body, maxRequestBody)
	data, err := io.ReadAll(reader)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request body is invalid or too large")
		return err
	}
	if err := strictjson.Decode(data, target); err != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request JSON is invalid")
		return err
	}
	return nil
}

func writeStoreError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sessionstore.ErrNotFound):
		writeError(writer, http.StatusNotFound, "NOT_FOUND", "resource was not found")
	case errors.Is(err, sessionstore.ErrBusy):
		writeError(writer, http.StatusConflict, "RESOURCE_BUSY", "resource is already leased")
	case errors.Is(err, sessionstore.ErrFenced):
		writeError(writer, http.StatusConflict, "FENCED", "lease generation or fence is stale")
	case errors.Is(err, sessionstore.ErrConflict):
		writeError(writer, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key was reused for different input")
	case errors.Is(err, sessionstore.ErrExpired):
		writeError(writer, http.StatusGone, "STALE_LEASE", "binding or lease expired")
	case errors.Is(err, sessionstore.ErrInvalidState):
		writeError(writer, http.StatusConflict, "INVALID_TRANSITION", "session transition is invalid")
	case errors.Is(err, sessionstore.ErrIdentityRevoked):
		writeError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "certificate identity is revoked")
	case errors.Is(err, sessionstore.ErrInvalidArgument):
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "request arguments are invalid")
	case errors.Is(err, sessionstore.ErrBackpressure):
		writeError(writer, http.StatusTooManyRequests, "BACKPRESSURE_LIMIT", "runtime backpressure limit was reached")
	default:
		writeError(writer, http.StatusInternalServerError, "INTERNAL", "request failed")
	}
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSONStatus(writer, status, map[string]any{"error": map[string]any{"code": code, "retryable": status >= 500, "safe_message": message}})
}
func writeJSON(writer http.ResponseWriter, status int, value any) {
	writeJSONStatus(writer, status, value)
}
func writeJSONStatus(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
