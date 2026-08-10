package integrationv1

import (
	"encoding/json"
	"fmt"
)

// Document is the public Integration v1 handshake emitted before mutation.
type Document = handshakeDocument

// Executable identifies the exact FlowBaton process producing the handshake.
type Executable = executable

// Protocols names the negotiated public contract versions.
type Protocols = protocols

// AuthProfile describes one authenticated transport profile.
type AuthProfile = authProfile

// LocalCoreProfile returns the normative authenticated local IPC profile.
func LocalCoreProfile() AuthProfile { return localCoreV1 }

// RemoteCloudMacProfile returns the normative authenticated remote profile.
func RemoteCloudMacProfile() AuthProfile { return remoteCloudMacV1 }

// NewDocument constructs and semantically validates an Integration v1
// handshake. Callers cannot accidentally emit a document the validator rejects.
func NewDocument(identity Executable, transports []string, protocolVersions Protocols, profiles []AuthProfile, capabilities []string) (Document, error) {
	document := Document{
		SchemaVersion: 1, ContractVersion: "v1", FlowBaton: identity,
		Transports: append([]string(nil), transports...), Protocols: protocolVersions,
		AuthProfiles: append([]AuthProfile(nil), profiles...), Capabilities: append([]string(nil), capabilities...),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return Document{}, fmt.Errorf("encode Integration v1: %w", err)
	}
	if err := ValidateJSON(encoded); err != nil {
		return Document{}, err
	}
	return document, nil
}
