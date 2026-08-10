package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	devicesessionv1 "github.com/larchwave/flowbaton/contracts/device-session/v1"
	integrationv1 "github.com/larchwave/flowbaton/contracts/integration/v1"
	"github.com/larchwave/flowbaton/internal/auth"
	"github.com/larchwave/flowbaton/internal/sessionstore"
)

type fakeStore struct {
	identity     sessionstore.Identity
	acquire      sessionstore.AcquireInput
	apply        sessionstore.MutationInput
	waits        int
	nonceErr     error
	frameInput   sessionstore.FrameContentRequest
	frame        sessionstore.FrameContent
	blockEvents  bool
	waitStarted  chan struct{}
	disconnected chan struct{}
	databaseNow  time.Time
	reservedTTL  time.Duration
	currentCalls int
}

func (store *fakeStore) Ping(context.Context) error { return nil }
func (store *fakeStore) CurrentTime(context.Context) (time.Time, error) {
	store.currentCalls++
	if store.databaseNow.IsZero() {
		return time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC), nil
	}
	return store.databaseNow, nil
}

func TestHandlerUsesStoreTimeForTokenWindowAndValidation(t *testing.T) {
	databaseNow := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}, databaseNow: databaseNow}
	handler, err := New(Config{
		Store: store,
		Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: 90 * time.Second,
			Now: func() time.Time { return databaseNow.Add(-24 * time.Hour) }},
		Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey},
			Now: func() time.Time { return databaseNow.Add(24 * time.Hour) }},
		Integration: integration,
		RequestIdentity: func(*http.Request) (string, string, error) {
			return certificateDigest, bindingDigest, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	tokenRecorder := httptest.NewRecorder()
	tokenRequest := httptest.NewRequest(http.MethodPost, "/v1/session-tokens", bytes.NewBufferString(`{"nonce":"nonce-1234567890","scopes":["device-session"]}`))
	handler.ServeHTTP(tokenRecorder, tokenRequest)
	if tokenRecorder.Code != http.StatusCreated {
		t.Fatalf("token status=%d body=%s", tokenRecorder.Code, tokenRecorder.Body.String())
	}
	var response struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokenRecorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	claims, err := handler.config.Verifier.VerifySignature(response.Token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.IssuedAt != databaseNow.Unix() || claims.ExpiresAt != databaseNow.Add(90*time.Second).Unix() || store.reservedTTL != 90*time.Second {
		t.Fatalf("claims=%#v reservedTTL=%s", claims, store.reservedTTL)
	}

	eventsRequest := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/events", nil)
	eventsRequest.Header.Set("Authorization", "FlowBaton "+response.Token)
	eventsRequest.Header.Set("X-FlowBaton-Generation", "1")
	eventsRequest.Header.Set("X-FlowBaton-Fence", "fence")
	eventsRecorder := httptest.NewRecorder()
	handler.ServeHTTP(eventsRecorder, eventsRequest)
	if eventsRecorder.Code != http.StatusOK {
		t.Fatalf("events status=%d body=%s", eventsRecorder.Code, eventsRecorder.Body.String())
	}
}

func TestHandlerRejectsInvalidSignatureBeforeDatabaseTimeQuery(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	store := &fakeStore{}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}}, Integration: integration})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/events", nil)
	request.Header.Set("Authorization", "FlowBaton invalid.token.value")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized || store.currentCalls != 0 {
		t.Fatalf("status=%d database time calls=%d", recorder.Code, store.currentCalls)
	}
}
func (store *fakeStore) ReserveTokenNonce(_ context.Context, _, _ string, ttl time.Duration) (sessionstore.TokenWindow, error) {
	if store.nonceErr != nil {
		return sessionstore.TokenWindow{}, store.nonceErr
	}
	store.reservedTTL = ttl
	now, _ := store.CurrentTime(context.Background())
	return sessionstore.TokenWindow{IssuedAt: now, ExpiresAt: now.Add(ttl)}, nil
}

func TestTokenNonceFailureDoesNotClaimReplay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{
		identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"},
		nonceErr: errors.New("database unavailable"),
	}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/session-tokens", bytes.NewBufferString(`{"nonce":"nonce-1234567890","scopes":["device-session"]}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"INTERNAL"`)) || bytes.Contains(recorder.Body.Bytes(), []byte("NONCE_REPLAY")) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDurationFromMillisecondsRejectsOverflow(t *testing.T) {
	t.Parallel()

	if _, err := durationFromMilliseconds(maxDurationMilliseconds + 1); err == nil {
		t.Fatal("overflowing millisecond duration was accepted")
	}
	got, err := durationFromMilliseconds(maxDurationMilliseconds)
	if err != nil || got <= 0 {
		t.Fatalf("largest safe duration = %v, %v", got, err)
	}
}

func TestIntegrationIsPublishedWhileRuntimeReadinessIsClosed(t *testing.T) {
	t.Parallel()

	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, err := integrationv1.NewDocument(
		integrationv1.Executable{
			Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			License: "Apache-2.0", ProcessID: 1,
		},
		[]string{"authenticated-remote-ipc"},
		integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"},
		[]integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(Config{
		Store: &fakeStore{}, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey},
		Verifier:    auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}},
		Integration: integration,
		Readiness:   func(context.Context) error { return errors.New("drivers are opening") },
	})
	if err != nil {
		t.Fatal(err)
	}
	integrationResponse := httptest.NewRecorder()
	handler.ServeHTTP(integrationResponse, httptest.NewRequest(http.MethodGet, "/v1/integration", nil))
	if integrationResponse.Code != http.StatusOK {
		t.Fatalf("integration status=%d body=%s", integrationResponse.Code, integrationResponse.Body.String())
	}
	readyResponse := httptest.NewRecorder()
	handler.ServeHTTP(readyResponse, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status=%d body=%s", readyResponse.Code, readyResponse.Body.String())
	}
}

func (store *fakeStore) ResolveIdentity(context.Context, string) (sessionstore.Identity, error) {
	return store.identity, nil
}
func (store *fakeStore) Acquire(_ context.Context, input sessionstore.AcquireInput) (sessionstore.Result, error) {
	store.acquire = input
	return sessionstore.Result{Session: sessionstore.Session{SessionID: "session-0001", TenantID: input.TenantID, PrincipalID: input.PrincipalID, LeaseID: "lease-0001", Generation: 1, FencingTokenSHA256: "fence", Status: "active"}}, nil
}
func (store *fakeStore) Apply(_ context.Context, input sessionstore.MutationInput) (sessionstore.Result, error) {
	store.apply = input
	return sessionstore.Result{Session: sessionstore.Session{SessionID: input.SessionID}, Queued: input.Type == "input"}, nil
}
func (store *fakeStore) Events(context.Context, string, string, string, int64) ([]devicesessionv1.Event, error) {
	return []devicesessionv1.Event{{Sequence: 2, EventID: "event-0002", Type: "heartbeat"}}, nil
}
func (store *fakeStore) WaitEvents(ctx context.Context, _, _, _ string, after int64, _ time.Duration) ([]devicesessionv1.Event, bool, error) {
	store.waits++
	if store.blockEvents {
		select {
		case store.waitStarted <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	if after < 2 {
		return []devicesessionv1.Event{{Sequence: 2, EventID: "event-0002", Type: "heartbeat"}}, false, nil
	}
	return []devicesessionv1.Event{{Sequence: 3, EventID: "event-0003", Type: "released"}}, true, nil
}

func (store *fakeStore) ValidateSessionAccess(context.Context, string, string, string, string, string, time.Time, int64, string) error {
	return nil
}

func (store *fakeStore) FrameContent(_ context.Context, input sessionstore.FrameContentRequest) (sessionstore.FrameContent, error) {
	store.frameInput = input
	if store.frame.Content == nil {
		return sessionstore.FrameContent{}, sessionstore.ErrNotFound
	}
	return store.frame, nil
}

func (store *fakeStore) MarkDisconnected(context.Context, string, string, string, string, string, time.Time, int64, string, string) error {
	if store.disconnected != nil {
		select {
		case store.disconnected <- struct{}{}:
		default:
		}
	}
	return nil
}

func TestHandlerDerivesIdentityAndBindsSessionRequests(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, err := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	if err != nil {
		t.Fatal(err)
	}
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: 5 * time.Minute, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }})
	if err != nil {
		t.Fatal(err)
	}
	tokenRecorder := httptest.NewRecorder()
	tokenRequest := httptest.NewRequest(http.MethodPost, "/v1/session-tokens", bytes.NewBufferString(`{"nonce":"nonce-1234567890","scopes":["device-session"]}`))
	handler.ServeHTTP(tokenRecorder, tokenRequest)
	if tokenRecorder.Code != http.StatusCreated {
		t.Fatalf("token status=%d body=%s", tokenRecorder.Code, tokenRecorder.Body.String())
	}
	var tokenResponse struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(tokenRecorder.Body.Bytes(), &tokenResponse); err != nil {
		t.Fatal(err)
	}
	acquireRecorder := httptest.NewRecorder()
	acquireRequest := httptest.NewRequest(http.MethodPost, "/v1/device-sessions", bytes.NewBufferString(`{"resource_id":"device-1","capabilities":["tap"],"idempotency_key":"idempotency-key-1","release_idempotency_key":"release-key-0001"}`))
	acquireRequest.Header.Set("Authorization", "FlowBaton "+tokenResponse.Token)
	handler.ServeHTTP(acquireRecorder, acquireRequest)
	if acquireRecorder.Code != http.StatusCreated {
		t.Fatalf("acquire status=%d body=%s", acquireRecorder.Code, acquireRecorder.Body.String())
	}
	if store.acquire.TenantID != "tenant-1" || store.acquire.PrincipalID != "principal-1" || store.acquire.ChannelBindingSHA256 != bindingDigest {
		t.Fatalf("acquire identity=%#v", store.acquire)
	}
	inputRecorder := httptest.NewRecorder()
	inputRequest := httptest.NewRequest(http.MethodPost, "/v1/device-sessions/session-0001/requests", bytes.NewBufferString(`{"request_id":"request-input-0001","type":"input","idempotency_key":"input-key-0000001","generation":1,"fencing_token_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{"lease_id":"lease-0001","generation":1,"fencing_token_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","based_on_stream_epoch":1,"based_on_frame_sequence":1,"command":"tap","payload_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},"command_payload":{"x":1,"y":2}}`))
	inputRequest.Header.Set("Authorization", "FlowBaton "+tokenResponse.Token)
	handler.ServeHTTP(inputRecorder, inputRequest)
	if inputRecorder.Code != http.StatusAccepted || store.apply.Type != "input" || string(store.apply.CommandPayload) != `{"x":1,"y":2}` {
		t.Fatalf("input status=%d apply=%#v body=%s", inputRecorder.Code, store.apply, inputRecorder.Body.String())
	}
}

func TestHandlerRejectsTokenOnAnotherChannel(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}}
	channel := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	handler, _ := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, channel, nil }})
	token, _ := handler.config.Issuer.Issue(auth.Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: certificateDigest, ChannelBindingSHA256: channel, Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	channel = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	request := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/events", nil)
	request.Header.Set("Authorization", "FlowBaton "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestEventStreamResumesFlushesAndClosesOnTerminalEvent(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }, StreamDuration: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := handler.config.Issuer.Issue(auth.Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: certificateDigest, ChannelBindingSHA256: bindingDigest, Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	request := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/events?after=1", nil)
	request.Header.Set("Authorization", "FlowBaton "+token)
	request.Header.Set("X-FlowBaton-Generation", "1")
	request.Header.Set("X-FlowBaton-Fence", "fence")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || store.waits != 2 || !bytes.Contains(recorder.Body.Bytes(), []byte(`"sequence":2`)) || !bytes.Contains(recorder.Body.Bytes(), []byte(`"sequence":3`)) {
		t.Fatalf("status=%d waits=%d body=%s", recorder.Code, store.waits, recorder.Body.String())
	}
}

func TestFrameContentRequiresCurrentFenceAndReturnsBoundedBytes(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	contentDigest := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	store := &fakeStore{
		identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"},
		frame:    sessionstore.FrameContent{Content: []byte("png-bytes"), ContentType: "image/png", SHA256: contentDigest},
	}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := handler.config.Issuer.Issue(auth.Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: certificateDigest, ChannelBindingSHA256: bindingDigest, Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	request := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/frames/2/7", nil)
	request.Header.Set("Authorization", "FlowBaton "+token)
	request.Header.Set("X-FlowBaton-Generation", "3")
	request.Header.Set("X-FlowBaton-Fence", "fence-3")
	request.Header.Set("X-FlowBaton-Content-SHA256", contentDigest)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "png-bytes" || recorder.Header().Get("Content-Type") != "image/png" ||
		store.frameInput.RequestNonce != "nonce-1234567890" || !store.frameInput.BindingExpiresAt.Equal(now.Add(5*time.Minute)) ||
		store.frameInput.Generation != 3 || store.frameInput.StreamEpoch != 2 || store.frameInput.FrameSequence != 7 || store.frameInput.ContentSHA256 != contentDigest {
		t.Fatalf("status=%d headers=%v body=%q input=%#v", recorder.Code, recorder.Header(), recorder.Body.String(), store.frameInput)
	}
}

func TestEventTransportCancellationRecordsDisconnect(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}, blockEvents: true, waitStarted: make(chan struct{}, 1), disconnected: make(chan struct{}, 1)}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }})
	if err != nil {
		t.Fatal(err)
	}
	token, _ := handler.config.Issuer.Issue(auth.Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: certificateDigest, ChannelBindingSHA256: bindingDigest, Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/v1/device-sessions/session-1/events", nil).WithContext(ctx)
	request.Header.Set("Authorization", "FlowBaton "+token)
	request.Header.Set("X-FlowBaton-Generation", "1")
	request.Header.Set("X-FlowBaton-Fence", "fence-1")
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(done)
	}()
	select {
	case <-store.waitStarted:
	case <-time.After(time.Second):
		t.Fatal("event stream did not begin waiting")
	}
	cancel()
	select {
	case <-store.disconnected:
	case <-time.After(time.Second):
		t.Fatal("transport cancellation did not record disconnect")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled event stream did not stop")
	}
}

func TestTokenNonceBackpressureIsRateLimited(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	integration, _ := integrationv1.NewDocument(integrationv1.Executable{Version: "0.2.0", BinarySHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", License: "Apache-2.0", ProcessID: 1}, []string{"authenticated-remote-ipc"}, integrationv1.Protocols{FlowContract: "v1", DeviceSession: "v1", Report: "v1"}, []integrationv1.AuthProfile{integrationv1.RemoteCloudMacProfile()}, []string{"tap"})
	certificateDigest := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	bindingDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	store := &fakeStore{identity: sessionstore.Identity{CertificateFingerprint: certificateDigest, TenantID: "tenant-1", PrincipalID: "principal-1"}, nonceErr: sessionstore.ErrBackpressure}
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }})
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/session-tokens", bytes.NewBufferString(`{"nonce":"nonce-1234567890","scopes":["device-session"]}`))
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusTooManyRequests || !bytes.Contains(recorder.Body.Bytes(), []byte(`"code":"BACKPRESSURE_LIMIT"`)) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
