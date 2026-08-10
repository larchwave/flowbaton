package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/sessionstore"
)

func TestServeRunnerParsesRequiredRuntimeInputs(t *testing.T) {
	var got ServeOptions
	runner := ServeRunner{Serve: func(_ context.Context, options ServeOptions) error { got = options; return nil }}
	args := []string{"--database-url", "postgres://db", "--tls-cert", "cert.pem", "--tls-key", "key.pem", "--client-ca", "ca.pem", "--signing-key", "signing.json", "--signing-key-id", "key-1"}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), args, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if got.DatabaseURL != "postgres://db" || got.Address != "127.0.0.1:7443" {
		t.Fatalf("options=%#v", got)
	}
}

type fakeIdentityAdmin struct{ added sessionstore.Identity }

func (admin *fakeIdentityAdmin) UpsertIdentity(_ context.Context, identity sessionstore.Identity) error {
	admin.added = identity
	return nil
}
func (*fakeIdentityAdmin) RevokeIdentity(context.Context, string, time.Time) error { return nil }
func (*fakeIdentityAdmin) ListIdentities(context.Context) ([]sessionstore.Identity, error) {
	return nil, nil
}

func TestAuthRunnerStoresCertificateMappingAndGeneratesKeys(t *testing.T) {
	admin := &fakeIdentityAdmin{}
	runner := AuthRunner{OpenAdmin: func(context.Context, string) (sessionstore.IdentityAdmin, func(), error) {
		return admin, func() {}, nil
	}}
	var stdout, stderr bytes.Buffer
	args := []string{"cert-map", "add", "--database-url", "postgres://db", "fingerprint", "tenant-1", "principal-1"}
	if code := runner.Run(context.Background(), args, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if admin.added.TenantID != "tenant-1" || admin.added.PrincipalID != "principal-1" {
		t.Fatalf("identity=%#v", admin.added)
	}
	var writes []os.FileMode
	runner.WriteFile = func(_ string, _ []byte, mode os.FileMode) error { writes = append(writes, mode); return nil }
	stdout.Reset()
	stderr.Reset()
	if code := runner.Run(context.Background(), []string{"keygen", "--key-id", "key-1", "--private-key", "private.json", "--public-key", "public.json"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("keygen exit=%d stderr=%s", code, stderr.String())
	}
	if len(writes) != 2 || writes[0] != 0o600 || writes[1] != 0o644 {
		t.Fatalf("key modes=%v", writes)
	}
}

func TestDBRunnerDelegatesSchemaApplication(t *testing.T) {
	called := ""
	runner := DBRunner{ApplySchema: func(_ context.Context, url string) error { called = url; return nil }}
	var stdout, stderr bytes.Buffer
	if code := runner.Run(context.Background(), []string{"apply-schema", "--database-url", "postgres://db"}, &stdout, &stderr); code != ExitOK {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if called != "postgres://db" {
		t.Fatalf("URL=%q", called)
	}
}

func TestServeBootstrapBuildsExactExecutableIdentityAndStrictInventory(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "flowbaton")
	if err := os.WriteFile(executable, []byte("exact executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	bootstrap := ServeBootstrap{Executable: func() (string, error) { return executable, nil }, ProcessID: func() int { return 42 }}
	document, err := bootstrap.integrationDocument([]string{"tap"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(document)
	if !bytes.Contains(encoded, []byte(`"process_id":42`)) || !bytes.Contains(encoded, []byte(`"version":"dev"`)) {
		t.Fatalf("document=%s", encoded)
	}
	inventoryPath := filepath.Join(directory, "inventory.json")
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[{"tenant_id":"tenant-1","resource_id":"device-1","platform":"android","device":"serial-1","port":7001,"reinstall_driver":false,"capabilities":["tap"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if inventory, err := loadServeInventory(inventoryPath); err != nil || len(inventory.Devices) != 1 {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}
	if err := os.WriteFile(inventoryPath, []byte(`{"devices":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadServeInventory(inventoryPath); err == nil {
		t.Fatal("unknown inventory field was accepted")
	}
}
