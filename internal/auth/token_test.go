package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
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

func TestDecodeStrictRejectsDuplicateTokenFields(t *testing.T) {
	var header tokenHeader
	if err := decodeStrict(
		[]byte(`{"alg":"EdDSA","alg":"none","typ":"FBST","kid":"key-1"}`), &header); err == nil {
		t.Fatal("duplicate token header field was accepted")
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

func TestIssueRejectsNonceOverMaximumLength(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (Issuer{KeyID: "key-1", PrivateKey: privateKey, TTL: time.Minute}).Issue(Claims{
		TenantID: "tenant-1", PrincipalID: "principal-1",
		CertificateFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ChannelBindingSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:                  strings.Repeat("n", maxTokenNonceLength+1),
		Scopes:                 []string{"device-session"},
	})
	if err == nil {
		t.Fatal("Issue() accepted an oversized nonce")
	}
}

func TestIssueAtAndVerifyAtUseExactWindow(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuedAt := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	claims := Claims{
		TenantID: "tenant-1", PrincipalID: "principal-1",
		CertificateFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ChannelBindingSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:                  "nonce-1234567890", Scopes: []string{"device-session"},
	}
	token, err := (Issuer{KeyID: "key-1", PrivateKey: privateKey}).IssueAt(claims, issuedAt, issuedAt.Add(90*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := (Verifier{Keys: map[string]ed25519.PublicKey{"key-1": publicKey}, Now: func() time.Time { return issuedAt.Add(24 * time.Hour) }}).VerifyAt(token, issuedAt.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if verified.IssuedAt != issuedAt.Unix() || verified.ExpiresAt != issuedAt.Add(90*time.Second).Unix() {
		t.Fatalf("claims=%#v", verified)
	}
}

func TestTokenValidityWindowRejectsUnsafePrecisionAndDuration(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	issuer := Issuer{KeyID: "key-1", PrivateKey: privateKey}
	issuedAt := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	claims := Claims{
		TenantID: "tenant-1", PrincipalID: "principal-1",
		CertificateFingerprint: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ChannelBindingSHA256:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Nonce:                  "nonce-1234567890", Scopes: []string{"device-session"},
	}
	if _, err := issuer.IssueAt(claims, issuedAt.Add(time.Nanosecond), issuedAt.Add(time.Minute)); err == nil {
		t.Fatal("IssueAt accepted sub-second precision")
	}
	if _, err := issuer.IssueAt(claims, issuedAt, issuedAt.Add(MaxTokenTTL+time.Second)); err == nil {
		t.Fatal("IssueAt accepted an excessive lifetime")
	}
	for _, ttl := range []time.Duration{-time.Second, time.Millisecond, MaxTokenTTL + time.Second} {
		if _, err := NormalizeTokenTTL(ttl); err == nil {
			t.Fatalf("NormalizeTokenTTL(%s) succeeded", ttl)
		}
	}
}
