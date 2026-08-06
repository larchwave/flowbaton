// Package integrationv1 implements the normative cross-field checks for the
// public FlowBaton Integration v1 handshake.
package integrationv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

type handshakeDocument struct {
	SchemaVersion   int           `json:"schema_version"`
	ContractVersion string        `json:"contract_version"`
	FlowBaton       executable    `json:"flowbaton"`
	Transports      []string      `json:"transports"`
	Protocols       protocols     `json:"protocols"`
	AuthProfiles    []authProfile `json:"auth_profiles"`
	Capabilities    []string      `json:"capabilities"`
}

type executable struct {
	Version      string `json:"version"`
	BinarySHA256 string `json:"binary_sha256"`
	License      string `json:"license"`
	ProcessID    int    `json:"process_id"`
}

type protocols struct {
	FlowContract  string `json:"flow_contract"`
	DeviceSession string `json:"device_session"`
	Report        string `json:"report"`
}

type authProfile struct {
	ProfileID        string `json:"profile_id"`
	Transport        string `json:"transport"`
	Authentication   string `json:"authentication"`
	ChannelBinding   string `json:"channel_binding"`
	PrincipalScope   string `json:"principal_scope"`
	TenantScope      string `json:"tenant_scope"`
	ReplayProtection string `json:"replay_protection"`
}

var localCoreV1 = authProfile{
	ProfileID: "local-core-v1", Transport: "authenticated-local-ipc",
	Authentication: "peer-credentials+launch-token", ChannelBinding: "os-peer-identity+endpoint",
	PrincipalScope: "local-user+process", TenantScope: "single-host",
	ReplayProtection: "per-launch-nonce+expiry",
}

var remoteCloudMacV1 = authProfile{
	ProfileID: "remote-cloud-mac-v1", Transport: "authenticated-remote-ipc",
	Authentication: "mutual-tls+signed-session-token", ChannelBinding: "tls-exporter",
	PrincipalScope: "account+host+process", TenantScope: "tenant-id",
	ReplayProtection: "nonce+expiry+lease-generation",
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateJSON rejects ambiguous or uncovered Integration v1 handshakes. JSON
// Schema validation remains required for generic consumers; this function is the
// normative executable check for invariants spanning transports and profiles.
func ValidateJSON(data []byte) error {
	var document handshakeDocument
	if err := decodeStrict(data, &document); err != nil {
		return fmt.Errorf("decode Integration v1: %w", err)
	}
	errors := []string{}
	if document.SchemaVersion != 1 || document.ContractVersion != "v1" {
		errors = append(errors, "unsupported contract version")
	}
	if document.FlowBaton.Version == "" || !sha256Pattern.MatchString(document.FlowBaton.BinarySHA256) || document.FlowBaton.License != "Apache-2.0" || document.FlowBaton.ProcessID < 1 {
		errors = append(errors, "invalid FlowBaton executable identity")
	}
	if document.Protocols.FlowContract == "" || document.Protocols.DeviceSession == "" || document.Protocols.Report == "" {
		errors = append(errors, "protocol versions must be explicit")
	}

	allowedTransports := map[string]bool{
		"cli-stdio": true, "mcp": true,
		"authenticated-local-ipc": true, "authenticated-remote-ipc": true,
	}
	advertised := map[string]bool{}
	for _, transport := range document.Transports {
		if !allowedTransports[transport] {
			errors = append(errors, "unknown transport")
		}
		if advertised[transport] {
			errors = append(errors, "duplicate transport")
		}
		advertised[transport] = true
	}
	if len(document.Transports) == 0 {
		errors = append(errors, "at least one transport is required")
	}

	expected := map[string]authProfile{
		localCoreV1.Transport:      localCoreV1,
		remoteCloudMacV1.Transport: remoteCloudMacV1,
	}
	seenIDs := map[string]bool{}
	seenTransports := map[string]bool{}
	for _, profile := range document.AuthProfiles {
		if seenIDs[profile.ProfileID] || seenTransports[profile.Transport] {
			errors = append(errors, "duplicate authentication profile")
		}
		seenIDs[profile.ProfileID] = true
		seenTransports[profile.Transport] = true
		want, ok := expected[profile.Transport]
		if !ok || profile != want {
			errors = append(errors, "invalid discriminated authentication profile")
		}
		if !advertised[profile.Transport] {
			errors = append(errors, "authentication profile transport is not advertised")
		}
	}
	for transport := range expected {
		if advertised[transport] && !seenTransports[transport] {
			errors = append(errors, "advertised authenticated transport lacks a profile")
		}
	}

	seenCapabilities := map[string]bool{}
	for _, capability := range document.Capabilities {
		if capability == "" || seenCapabilities[capability] {
			errors = append(errors, "capabilities must be non-empty and unique")
		}
		seenCapabilities[capability] = true
	}
	if len(errors) != 0 {
		sort.Strings(errors)
		return fmt.Errorf("Integration v1 semantic validation: %s", strings.Join(errors, "; "))
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}
