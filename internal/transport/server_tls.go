package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
)

const (
	maxTLSCertificateBytes = 2 << 20
	maxTLSPrivateKeyBytes  = 128 << 10
)

// LoadServerTLS builds the mandatory TLS 1.3 mutual-authentication profile for
// the remote DeviceSession transport.
func LoadServerTLS(certificatePath, privateKeyPath, clientCAPath string) (*tls.Config, error) {
	certificatePEM, err := readTLSFile(certificatePath, maxTLSCertificateBytes, false)
	if err != nil {
		return nil, fmt.Errorf("read server certificate: %w", err)
	}
	privateKeyPEM, err := readTLSFile(privateKeyPath, maxTLSPrivateKeyBytes, true)
	if err != nil {
		return nil, fmt.Errorf("read server private key: %w", err)
	}
	defer clear(privateKeyPEM)
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		return nil, err
	}
	clientCA, err := readTLSFile(clientCAPath, maxTLSCertificateBytes, false)
	if err != nil {
		return nil, fmt.Errorf("read client CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(clientCA) {
		return nil, errors.New("client CA contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, NextProtos: []string{"h2"}}, nil
}

func readTLSFile(path string, maximum int64, private bool) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 1 || info.Size() > maximum {
		return nil, errors.New("PEM input must be a bounded regular non-symlink file")
	}
	if private && runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private-key permissions expose secret material")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) {
		return nil, errors.New("PEM input changed while it was opened")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if len(contents) == 0 || int64(len(contents)) > maximum {
		return nil, errors.New("PEM input exceeds its size limit")
	}
	return contents, nil
}
