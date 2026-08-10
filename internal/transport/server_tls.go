package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
)

// LoadServerTLS builds the mandatory TLS 1.3 mutual-authentication profile for
// the remote DeviceSession transport.
func LoadServerTLS(certificatePath, privateKeyPath, clientCAPath string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certificatePath, privateKeyPath)
	if err != nil {
		return nil, err
	}
	clientCA, err := os.ReadFile(clientCAPath)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(clientCA) {
		return nil, errors.New("client CA contains no certificates")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, NextProtos: []string{"h2"}}, nil
}
