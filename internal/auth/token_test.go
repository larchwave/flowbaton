package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

func TestIssueAndVerifyChannelBoundToken(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	issuer := Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	token, err := issuer.Issue(Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChannelBindingSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claims, err := (Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now.Add(time.Minute) }}).Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.TenantID != "tenant-1" || !claims.HasScope("device-session") {
		t.Fatalf("Verify() claims = %#v", claims)
	}
}

func TestVerifyRejectsExpiryAndTampering(t *testing.T) {
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	token, err := (Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: time.Minute, Now: func() time.Time { return now }}).Issue(Claims{TenantID: "tenant-1", PrincipalID: "principal-1", CertificateFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChannelBindingSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Nonce: "nonce-1234567890", Scopes: []string{"device-session"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return now.Add(2 * time.Minute) }}
	if _, err := verifier.Verify(token); !errors.Is(err, ErrExpiredToken) {
		t.Fatalf("expired Verify() error = %v", err)
	}
	verifier.Now = func() time.Time { return now }
	if _, err := verifier.Verify(token + "x"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered Verify() error = %v", err)
	}
}
