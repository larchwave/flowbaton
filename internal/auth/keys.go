package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/larchwave/flowbaton/internal/strictjson"
)

const maxPrivateKeyDocumentBytes = 4 << 10

// LoadPrivateKey reads the strict JSON format emitted by `flowbaton auth keygen`.
func LoadPrivateKey(path, expectedKeyID string) (string, ed25519.PrivateKey, error) {
	data, err := readPrivateKeyDocument(path)
	if err != nil {
		return "", nil, err
	}
	defer clear(data)
	var document struct {
		KeyID      string `json:"key_id"`
		Algorithm  string `json:"algorithm"`
		PrivateKey string `json:"private_key"`
	}
	if err := strictjson.Decode(data, &document); err != nil {
		return "", nil, fmt.Errorf("decode signing key: %w", err)
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

func readPrivateKeyDocument(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("signing key must be a regular non-symlink file")
	}
	if err := validatePrivateKeyInfo(info); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, openedInfo) {
		return nil, errors.New("signing key changed while it was opened")
	}
	if err := validatePrivateKeyInfo(openedInfo); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, maxPrivateKeyDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > maxPrivateKeyDocumentBytes {
		return nil, errors.New("signing key document size is invalid")
	}
	return data, nil
}

func validatePrivateKeyInfo(info os.FileInfo) error {
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxPrivateKeyDocumentBytes {
		return errors.New("signing key document size or type is invalid")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("signing key permissions expose private material")
	}
	return nil
}
