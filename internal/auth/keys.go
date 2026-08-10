package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

// LoadPrivateKey reads the strict JSON format emitted by `flowbaton auth keygen`.
func LoadPrivateKey(path, expectedKeyID string) (string, ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var document struct {
		KeyID      string `json:"key_id"`
		Algorithm  string `json:"algorithm"`
		PrivateKey string `json:"private_key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return "", nil, fmt.Errorf("decode signing key: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", nil, errors.New("decode signing key: trailing JSON")
	}
	if document.KeyID == "" || (expectedKeyID != "" && document.KeyID != expectedKeyID) || document.Algorithm != "Ed25519" {
		return "", nil, errors.New("signing key identity does not match")
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(document.PrivateKey)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return "", nil, errors.New("signing key material is invalid")
	}
	return document.KeyID, ed25519.PrivateKey(key), nil
}
