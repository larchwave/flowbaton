package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
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
	identity sessionstore.Identity
	acquire  sessionstore.AcquireInput
	apply    sessionstore.MutationInput
}

func (store *fakeStore) Ping(context.Context) error { return nil }
func (store *fakeStore) ConsumeTokenNonce(context.Context, string, string, time.Time) error {
	return nil
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
	return sessionstore.Result{Session: sessionstore.Session{SessionID: input.SessionID}}, nil
}
func (store *fakeStore) Events(context.Context, string, string, string, int64) ([]devicesessionv1.Event, error) {
	return []devicesessionv1.Event{{Sequence: 2, EventID: "event-0002", Type: "heartbeat"}}, nil
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
	handler, err := New(Config{Store: store, Issuer: auth.Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: 5 * time.Minute, Now: func() time.Time { return now }}, Verifier: auth.Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now }}, Integration: integration, RequestIdentity: func(*http.Request) (string, string, error) { return certificateDigest, bindingDigest, nil }, Now: func() time.Time { return now }})
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
