// Package auth issues and verifies FlowBaton's channel-bound Ed25519 session tokens.
package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/larchwave/flowbaton/internal/strictjson"
)

var (
	ErrInvalidToken = errors.New("invalid FlowBaton session token")
	ErrExpiredToken = errors.New("expired FlowBaton session token")
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const maxTokenNonceLength = 128

const (
	DefaultTokenTTL = 5 * time.Minute
	MaxTokenTTL     = time.Hour
)

// Claims are the authenticated facts carried by a FlowBaton-issued token.
// Tenant and principal are derived from a certificate mapping, never request JSON.
type Claims struct {
	KeyID                  string   `json:"kid"`
	TenantID               string   `json:"tenant_id"`
	PrincipalID            string   `json:"principal_id"`
	CertificateFingerprint string   `json:"certificate_fingerprint_sha256"`
	ChannelBindingSHA256   string   `json:"channel_binding_sha256"`
	Nonce                  string   `json:"nonce"`
	Scopes                 []string `json:"scopes"`
	IssuedAt               int64    `json:"iat"`
	ExpiresAt              int64    `json:"exp"`
}

type tokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}

// Issuer signs short-lived session tokens with one Ed25519 key.
type Issuer struct {
	KeyID      string
	PrivateKey ed25519.PrivateKey
	TTL        time.Duration
	Now        func() time.Time
}

// Issue signs claims after overwriting key and validity fields.
func (issuer Issuer) Issue(claims Claims) (string, error) {
	ttl, err := NormalizeTokenTTL(issuer.TTL)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	if issuer.Now != nil {
		now = issuer.Now().UTC().Truncate(time.Second)
	}
	return issuer.IssueAt(claims, now, now.Add(ttl))
}

// IssueAt signs claims using an exact, externally-authoritative validity
// window. Both endpoints must use whole-second precision.
func (issuer Issuer) IssueAt(claims Claims, issuedAt, expiresAt time.Time) (string, error) {
	if issuer.KeyID == "" || len(issuer.PrivateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("issue session token: invalid signing key")
	}
	if claims.TenantID == "" || claims.PrincipalID == "" || !sha256Pattern.MatchString(claims.CertificateFingerprint) || !sha256Pattern.MatchString(claims.ChannelBindingSHA256) || len(claims.Nonce) < 16 || len(claims.Nonce) > maxTokenNonceLength || len(claims.Scopes) == 0 {
		return "", fmt.Errorf("issue session token: incomplete authenticated claims")
	}
	issuedAt = issuedAt.UTC()
	expiresAt = expiresAt.UTC()
	if !issuedAt.Equal(issuedAt.Truncate(time.Second)) || !expiresAt.Equal(expiresAt.Truncate(time.Second)) {
		return "", fmt.Errorf("issue session token: validity window must use whole-second precision")
	}
	ttl := expiresAt.Sub(issuedAt)
	if ttl <= 0 || ttl > MaxTokenTTL {
		return "", fmt.Errorf("issue session token: validity window is outside the supported range")
	}
	claims.KeyID, claims.IssuedAt, claims.ExpiresAt = issuer.KeyID, issuedAt.Unix(), expiresAt.Unix()
	header := tokenHeader{Algorithm: "EdDSA", Type: "FBST", KeyID: issuer.KeyID}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := encode(headerJSON) + "." + encode(claimsJSON)
	signature := ed25519.Sign(issuer.PrivateKey, []byte(unsigned))
	return unsigned + "." + encode(signature), nil
}

// Verifier accepts the active and previous public keys during rotation.
type Verifier struct {
	Keys map[string]ed25519.PublicKey
	Now  func() time.Time
}

// Verify validates strict encoding, the Ed25519 signature, time bounds, and all
// mandatory channel-bound identity fields.
func (verifier Verifier) Verify(token string) (Claims, error) {
	now := time.Now().UTC()
	if verifier.Now != nil {
		now = verifier.Now().UTC()
	}
	return verifier.VerifyAt(token, now)
}

// VerifyAt validates a token at an explicitly supplied authoritative time.
func (verifier Verifier) VerifyAt(token string, now time.Time) (Claims, error) {
	claims, err := verifier.VerifySignature(token)
	if err != nil {
		return Claims{}, err
	}
	if now.UTC().Unix() < claims.IssuedAt || now.UTC().Unix() >= claims.ExpiresAt {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
}

// VerifySignature validates strict encoding, the Ed25519 signature, and all
// mandatory claims without consulting a clock.
func (verifier Verifier) VerifySignature(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return Claims{}, ErrInvalidToken
	}
	headerBytes, err := decode(parts[0])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var header tokenHeader
	if err := decodeStrict(headerBytes, &header); err != nil || header.Algorithm != "EdDSA" || header.Type != "FBST" || header.KeyID == "" {
		return Claims{}, ErrInvalidToken
	}
	publicKey := verifier.Keys[header.KeyID]
	if len(publicKey) != ed25519.PublicKeySize {
		return Claims{}, ErrInvalidToken
	}
	signature, err := decode(parts[2])
	if err != nil || !ed25519.Verify(publicKey, []byte(parts[0]+"."+parts[1]), signature) {
		return Claims{}, ErrInvalidToken
	}
	claimsBytes, err := decode(parts[1])
	if err != nil {
		return Claims{}, ErrInvalidToken
	}
	var claims Claims
	if err := decodeStrict(claimsBytes, &claims); err != nil || claims.KeyID != header.KeyID || claims.TenantID == "" || claims.PrincipalID == "" || !sha256Pattern.MatchString(claims.CertificateFingerprint) || !sha256Pattern.MatchString(claims.ChannelBindingSHA256) || len(claims.Nonce) < 16 || len(claims.Nonce) > maxTokenNonceLength || len(claims.Scopes) == 0 || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt || claims.ExpiresAt-claims.IssuedAt > int64(MaxTokenTTL/time.Second) {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}

// NormalizeTokenTTL applies the safe default and rejects negative,
// sub-second, or overly long token lifetimes.
func NormalizeTokenTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	if ttl <= 0 || ttl > MaxTokenTTL || ttl%time.Second != 0 {
		return 0, fmt.Errorf("token TTL must be a whole number of seconds between 1s and %s", MaxTokenTTL)
	}
	return ttl, nil
}

// HasScope reports exact scope membership.
func (claims Claims) HasScope(scope string) bool {
	for _, candidate := range claims.Scopes {
		if candidate == scope {
			return true
		}
	}
	return false
}

func encode(value []byte) string { return base64.RawURLEncoding.EncodeToString(value) }

func decode(value string) ([]byte, error) { return base64.RawURLEncoding.Strict().DecodeString(value) }

func decodeStrict(data []byte, target any) error {
	return strictjson.Decode(data, target)
}
