package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
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
