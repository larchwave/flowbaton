package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadPrivateKeyReadsOnlyTheKeygenFormat(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signing.json")
	data := []byte(fmt.Sprintf(`{"key_id":"key-1","algorithm":"Ed25519","private_key":"%s"}`+"\n", base64.RawStdEncoding.EncodeToString(privateKey)))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	keyID, loaded, err := LoadPrivateKey(path, "key-1")
	if err != nil {
		t.Fatalf("LoadPrivateKey() error=%v", err)
	}
	if keyID != "key-1" || !loaded.Equal(privateKey) {
		t.Fatal("loaded key differs")
	}
	if _, _, err := LoadPrivateKey(path, "key-2"); err == nil {
		t.Fatal("mismatched key id was accepted")
	}
}

func TestLoadPrivateKeyRejectsAmbiguousOrExposedFiles(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	encoded := base64.RawStdEncoding.EncodeToString(privateKey)

	duplicate := filepath.Join(directory, "duplicate.json")
	if err := os.WriteFile(duplicate, []byte(fmt.Sprintf(
		`{"key_id":"key-1","key_id":"key-2","algorithm":"Ed25519","private_key":"%s"}`, encoded)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPrivateKey(duplicate, "key-1"); err == nil {
		t.Fatal("duplicate signing-key field was accepted")
	}

	oversized := filepath.Join(directory, "oversized.json")
	if err := os.WriteFile(oversized, make([]byte, maxPrivateKeyDocumentBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPrivateKey(oversized, "key-1"); err == nil {
		t.Fatal("oversized signing-key document was accepted")
	}

	if runtime.GOOS != "windows" {
		exposed := filepath.Join(directory, "exposed.json")
		if err := os.WriteFile(exposed, []byte(fmt.Sprintf(
			`{"key_id":"key-1","algorithm":"Ed25519","private_key":"%s"}`, encoded)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadPrivateKey(exposed, "key-1"); err == nil {
			t.Fatal("group/world-readable signing key was accepted")
		}

		link := filepath.Join(directory, "signing-link.json")
		if err := os.Symlink(duplicate, link); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadPrivateKey(link, "key-1"); err == nil {
			t.Fatal("signing-key symlink was accepted")
		}
	}
}
