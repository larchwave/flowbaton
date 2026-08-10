// Package auth issues and verifies FlowBaton's channel-bound Ed25519 session tokens.
package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

var (
	ErrInvalidToken = errors.New("invalid FlowBaton session token")
	ErrExpiredToken = errors.New("expired FlowBaton session token")
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

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
	if issuer.KeyID == "" || len(issuer.PrivateKey) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("issue session token: invalid signing key")
	}
	if claims.TenantID == "" || claims.PrincipalID == "" || !sha256Pattern.MatchString(claims.CertificateFingerprint) || !sha256Pattern.MatchString(claims.ChannelBindingSHA256) || len(claims.Nonce) < 16 || len(claims.Scopes) == 0 {
		return "", fmt.Errorf("issue session token: incomplete authenticated claims")
	}
	ttl := issuer.TTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now().UTC()
	if issuer.Now != nil {
		now = issuer.Now().UTC()
	}
	claims.KeyID, claims.IssuedAt, claims.ExpiresAt = issuer.KeyID, now.Unix(), now.Add(ttl).Unix()
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
	if err := decodeStrict(claimsBytes, &claims); err != nil || claims.KeyID != header.KeyID || claims.TenantID == "" || claims.PrincipalID == "" || !sha256Pattern.MatchString(claims.CertificateFingerprint) || !sha256Pattern.MatchString(claims.ChannelBindingSHA256) || len(claims.Nonce) < 16 || len(claims.Scopes) == 0 || claims.IssuedAt <= 0 || claims.ExpiresAt <= claims.IssuedAt {
		return Claims{}, ErrInvalidToken
	}
	now := time.Now().UTC()
	if verifier.Now != nil {
		now = verifier.Now().UTC()
	}
	if now.Unix() < claims.IssuedAt || now.Unix() >= claims.ExpiresAt {
		return Claims{}, ErrExpiredToken
	}
	return claims, nil
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
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalidToken
	}
	return nil
}
