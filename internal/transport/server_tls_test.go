package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadServerTLSRequiresBoundedPrivateInputs(t *testing.T) {
	directory := t.TempDir()
	certificatePath, keyPath, caPath := writeTestTLSMaterial(t, directory)
	config, err := LoadServerTLS(certificatePath, keyPath, caPath)
	if err != nil {
		t.Fatalf("LoadServerTLS() error = %v", err)
	}
	if config.MinVersion != tls.VersionTLS13 || config.ClientAuth != tls.RequireAndVerifyClientCert || len(config.Certificates) != 1 {
		t.Fatalf("TLS config = %#v", config)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(keyPath, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadServerTLS(certificatePath, keyPath, caPath); err == nil {
			t.Fatal("group/world-readable TLS private key was accepted")
		}
		if err := os.Chmod(keyPath, 0o600); err != nil {
			t.Fatal(err)
		}
		certificateLink := filepath.Join(directory, "server-link.pem")
		if err := os.Symlink(certificatePath, certificateLink); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadServerTLS(certificateLink, keyPath, caPath); err == nil {
			t.Fatal("TLS certificate symlink was accepted")
		}
	}
}

func writeTestTLSMaterial(t *testing.T, directory string) (string, string, string) {
	t.Helper()
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "FlowBaton test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	serverPublic, serverPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), DNSNames: []string{"localhost"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, serverPublic, caPrivate)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(serverPrivate)
	if err != nil {
		t.Fatal(err)
	}
	certificatePath := filepath.Join(directory, "server.pem")
	keyPath := filepath.Join(directory, "server-key.pem")
	caPath := filepath.Join(directory, "ca.pem")
	write := func(path string, block *pem.Block, mode os.FileMode) {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(certificatePath, &pem.Block{Type: "CERTIFICATE", Bytes: serverDER}, 0o644)
	write(keyPath, &pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}, 0o600)
	write(caPath, &pem.Block{Type: "CERTIFICATE", Bytes: caDER}, 0o644)
	return certificatePath, keyPath, caPath
}
