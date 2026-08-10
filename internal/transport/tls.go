// Package transport contains authenticated transport identity primitives.
package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
)

const exporterLabel = "EXPORTER-FlowBaton-DeviceSession-v1"

// CertificateFingerprint returns the lowercase SHA-256 digest of a certificate DER encoding.
func CertificateFingerprint(certificate *x509.Certificate) (string, error) {
	if certificate == nil || len(certificate.Raw) == 0 {
		return "", errors.New("client certificate is required")
	}
	digest := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(digest[:]), nil
}

// ChannelBinding returns the TLS-exporter digest used in FlowBaton tokens.
func ChannelBinding(state *tls.ConnectionState) (string, error) {
	if state == nil || len(state.PeerCertificates) == 0 {
		return "", errors.New("mutually authenticated TLS is required")
	}
	material, err := state.ExportKeyingMaterial(exporterLabel, nil, 32)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(material)
	return hex.EncodeToString(digest[:]), nil
}
