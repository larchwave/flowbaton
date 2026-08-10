package contracts_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type jsonSchemaHeader struct {
	Schema               string         `json:"$schema"`
	ID                   string         `json:"$id"`
	Type                 string         `json:"type"`
	AdditionalProperties bool           `json:"additionalProperties"`
	Required             []string       `json:"required"`
	Properties           map[string]any `json:"properties"`
	Defs                 map[string]any `json:"$defs,omitempty"`
}

type deviceSessionBinding struct {
	TenantID             string `json:"tenant_id"`
	PrincipalID          string `json:"principal_id"`
	AuthProfileID        string `json:"auth_profile_id"`
	ChannelBindingSHA256 string `json:"channel_binding_sha256"`
	RequestNonce         string `json:"request_nonce"`
	IssuedAt             string `json:"issued_at"`
	ExpiresAt            string `json:"expires_at"`
}

type deviceSessionLease struct {
	LeaseID               string `json:"lease_id"`
	ResourceID            string `json:"resource_id"`
	TenantID              string `json:"tenant_id"`
	OwnerPrincipalID      string `json:"owner_principal_id"`
	Generation            int    `json:"generation"`
	FencingTokenSHA256    string `json:"fencing_token_sha256"`
	ReleaseIdempotencyKey string `json:"release_idempotency_key"`
	AcquiredAt            string `json:"acquired_at"`
	ExpiresAt             string `json:"expires_at"`
	HeartbeatIntervalMS   int    `json:"heartbeat_interval_ms"`
}

type deviceSessionEvent struct {
	Sequence           int            `json:"sequence"`
	EventID            string         `json:"event_id"`
	Type               string         `json:"type"`
	Timestamp          string         `json:"timestamp"`
	LeaseGeneration    int            `json:"lease_generation"`
	FencingTokenSHA256 string         `json:"fencing_token_sha256"`
	Data               map[string]any `json:"data"`
}

type deviceSessionRequest struct {
	Sequence             int            `json:"sequence"`
	RequestID            string         `json:"request_id"`
	Type                 string         `json:"type"`
	Timestamp            string         `json:"timestamp"`
	IdempotencyKey       string         `json:"idempotency_key"`
	TenantID             string         `json:"tenant_id"`
	PrincipalID          string         `json:"principal_id"`
	ChannelBindingSHA256 string         `json:"channel_binding_sha256"`
	Data                 map[string]any `json:"data"`
}

type deviceSessionExample struct {
	SchemaVersion   int                    `json:"schema_version"`
	ContractVersion string                 `json:"contract_version"`
	SessionID       string                 `json:"session_id"`
	Binding         deviceSessionBinding   `json:"binding"`
	Lease           deviceSessionLease     `json:"lease"`
	Capabilities    []string               `json:"capabilities"`
	Requests        []deviceSessionRequest `json:"requests"`
	Events          []deviceSessionEvent   `json:"events"`
}

type integrationAuthProfile struct {
	ProfileID        string `json:"profile_id"`
	Transport        string `json:"transport"`
	Authentication   string `json:"authentication"`
	ChannelBinding   string `json:"channel_binding"`
	PrincipalScope   string `json:"principal_scope"`
	TenantScope      string `json:"tenant_scope"`
	ReplayProtection string `json:"replay_protection"`
}

func TestIntegrationV1IsASeparatePublicProcessContract(t *testing.T) {
	schema := loadSchema(t, "integration", "v1")
	requireSchemaFields(t, schema, "schema_version", "contract_version", "flowbaton", "transports", "protocols", "auth_profiles", "capabilities")

	var example struct {
		SchemaVersion   int    `json:"schema_version"`
		ContractVersion string `json:"contract_version"`
		FlowBaton       struct {
			Version      string `json:"version"`
			BinarySHA256 string `json:"binary_sha256"`
			License      string `json:"license"`
			ProcessID    int    `json:"process_id"`
		} `json:"flowbaton"`
		Transports   []string                 `json:"transports"`
		Protocols    map[string]string        `json:"protocols"`
		AuthProfiles []integrationAuthProfile `json:"auth_profiles"`
		Capabilities []string                 `json:"capabilities"`
	}
	loadStrictJSON(t, filepath.Join("integration", "v1", "example.json"), &example)
	if example.SchemaVersion != 1 || example.ContractVersion != "v1" {
		t.Fatalf("integration example version = %d/%q, want 1/v1", example.SchemaVersion, example.ContractVersion)
	}
	if example.FlowBaton.License != "Apache-2.0" || example.FlowBaton.ProcessID <= 0 {
		t.Fatalf("integration identity = %+v, want separate Apache-2.0 process", example.FlowBaton)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(example.FlowBaton.BinarySHA256) {
		t.Fatalf("integration binary digest %q is not SHA-256", example.FlowBaton.BinarySHA256)
	}
	for _, transport := range []string{"cli-stdio", "mcp", "authenticated-local-ipc", "authenticated-remote-ipc"} {
		if !contains(example.Transports, transport) {
			t.Errorf("integration example omits transport %q", transport)
		}
	}
	for _, protocol := range []string{"flow_contract", "device_session", "report"} {
		if example.Protocols[protocol] == "" {
			t.Errorf("integration example omits protocol %q", protocol)
		}
	}
	profiles := map[string]bool{}
	for _, profile := range example.AuthProfiles {
		if profile.Authentication == "" || profile.ChannelBinding == "" || profile.PrincipalScope == "" || profile.TenantScope == "" || profile.ReplayProtection == "" {
			t.Errorf("integration auth profile is incomplete: %+v", profile)
		}
		profiles[profile.Transport] = true
	}
	for _, transport := range []string{"authenticated-local-ipc", "authenticated-remote-ipc"} {
		if !profiles[transport] {
			t.Errorf("integration example has no auth profile for %q", transport)
		}
	}
	if errors := validateAuthProfiles(example.Transports, example.AuthProfiles); len(errors) != 0 {
		t.Fatalf("invalid integration auth profiles: %v", errors)
	}
	readme := readContractFile(t, filepath.Join("integration", "v1", "README.md"))
	for _, want := range []string{"separate process", "must not import", "N/N-1", "fail closed", "tenant", "channel binding"} {
		if !strings.Contains(readme, want) {
			t.Errorf("integration README omits %q", want)
		}
	}
}

func TestIntegrationV1RejectsMixedAndDuplicateAuthProfiles(t *testing.T) {
	valid := []integrationAuthProfile{
		{ProfileID: "local-core-v1", Transport: "authenticated-local-ipc", Authentication: "peer-credentials+launch-token", ChannelBinding: "os-peer-identity+endpoint", PrincipalScope: "local-user+process", TenantScope: "single-host", ReplayProtection: "per-launch-nonce+expiry"},
		{ProfileID: "remote-cloud-mac-v1", Transport: "authenticated-remote-ipc", Authentication: "mutual-tls+signed-session-token", ChannelBinding: "tls-exporter", PrincipalScope: "account+host+process", TenantScope: "tenant-id", ReplayProtection: "nonce+expiry+lease-generation"},
	}
	transports := []string{"authenticated-local-ipc", "authenticated-remote-ipc"}

	mixed := append([]integrationAuthProfile(nil), valid...)
	mixed[1].Authentication = "peer-credentials+launch-token"
	if errors := validateAuthProfiles(transports, mixed); len(errors) == 0 {
		t.Fatal("mixed remote/local authentication tuple was accepted")
	}

	duplicate := append([]integrationAuthProfile(nil), valid...)
	duplicate = append(duplicate, valid[1])
	if errors := validateAuthProfiles(transports, duplicate); len(errors) == 0 {
		t.Fatal("duplicate auth profile ID was accepted")
	}
}

func validateAuthProfiles(transports []string, profiles []integrationAuthProfile) []string {
	errors := []string{}
	expected := map[string]integrationAuthProfile{
		"authenticated-local-ipc":  {ProfileID: "local-core-v1", Transport: "authenticated-local-ipc", Authentication: "peer-credentials+launch-token", ChannelBinding: "os-peer-identity+endpoint", PrincipalScope: "local-user+process", TenantScope: "single-host", ReplayProtection: "per-launch-nonce+expiry"},
		"authenticated-remote-ipc": {ProfileID: "remote-cloud-mac-v1", Transport: "authenticated-remote-ipc", Authentication: "mutual-tls+signed-session-token", ChannelBinding: "tls-exporter", PrincipalScope: "account+host+process", TenantScope: "tenant-id", ReplayProtection: "nonce+expiry+lease-generation"},
	}
	advertised := map[string]bool{}
	for _, transport := range transports {
		advertised[transport] = true
	}
	seenIDs := map[string]bool{}
	seenTransports := map[string]bool{}
	for _, profile := range profiles {
		if seenIDs[profile.ProfileID] {
			errors = append(errors, "duplicate auth profile ID")
		}
		seenIDs[profile.ProfileID] = true
		want, ok := expected[profile.Transport]
		if !ok || profile != want {
			errors = append(errors, "invalid discriminated auth profile")
		}
		if !advertised[profile.Transport] {
			errors = append(errors, "auth profile transport is not advertised")
		}
		seenTransports[profile.Transport] = true
	}
	for transport := range expected {
		if advertised[transport] && !seenTransports[transport] {
			errors = append(errors, "advertised authenticated transport lacks a profile")
		}
	}
	return errors
}

func TestDeviceSessionV1CoversLeaseStreamInputAndRecovery(t *testing.T) {
	schema := loadSchema(t, "device-session", "v1")
	requireSchemaFields(t, schema, "schema_version", "contract_version", "session_id", "binding", "lease", "capabilities", "requests", "events")

	var example deviceSessionExample
	loadStrictJSON(t, filepath.Join("device-session", "v1", "example.json"), &example)
	if example.SchemaVersion != 1 || example.ContractVersion != "v1" || example.SessionID == "" {
		t.Fatalf("device session identity is incomplete: %+v", example)
	}
	if example.Lease.LeaseID == "" || example.Lease.ResourceID == "" || example.Lease.AcquiredAt == "" || example.Lease.ExpiresAt == "" || example.Lease.HeartbeatIntervalMS <= 0 {
		t.Fatalf("device session lease is incomplete: %+v", example.Lease)
	}
	if example.Binding.TenantID == "" || example.Binding.PrincipalID == "" || example.Binding.AuthProfileID == "" || example.Binding.ChannelBindingSHA256 == "" || example.Binding.RequestNonce == "" {
		t.Fatalf("device session binding is incomplete: %+v", example.Binding)
	}
	if example.Lease.TenantID != example.Binding.TenantID || example.Lease.OwnerPrincipalID != example.Binding.PrincipalID || example.Lease.Generation <= 0 || example.Lease.FencingTokenSHA256 == "" || example.Lease.ReleaseIdempotencyKey == "" {
		t.Fatalf("device session lease is not fenced to its authenticated owner: %+v", example.Lease)
	}
	if errors := validateDeviceRequests(example); len(errors) != 0 {
		t.Fatalf("device request protocol validation failed: %v", errors)
	}
	wantTypes := []string{"acquired", "frame", "input_ack", "heartbeat", "disconnected", "reconnected", "cancelled", "released"}
	seen := map[string]bool{}
	for index, event := range example.Events {
		if event.Sequence != index+1 {
			t.Errorf("event %d sequence = %d, want %d", index, event.Sequence, index+1)
		}
		if event.EventID == "" || event.Timestamp == "" || event.Data == nil {
			t.Errorf("event %d lacks timestamp or data", index)
		}
		if event.LeaseGeneration != example.Lease.Generation || event.FencingTokenSHA256 != example.Lease.FencingTokenSHA256 {
			t.Errorf("event %d is not fenced to current lease generation", index)
		}
		seen[event.Type] = true
	}
	for _, eventType := range wantTypes {
		if !seen[eventType] {
			t.Errorf("device session example omits %q event", eventType)
		}
	}
	if errors := validateDeviceSession(example.Binding.TenantID, example.Binding.PrincipalID, example.Lease.TenantID, example.Lease.OwnerPrincipalID, example.Lease.Generation, example.Lease.FencingTokenSHA256, example.Lease.ReleaseIdempotencyKey, example.Events); len(errors) != 0 {
		t.Fatalf("device session semantic validation failed: %v", errors)
	}
	readme := readContractFile(t, filepath.Join("device-session", "v1", "README.md"))
	for _, want := range []string{"transport-neutral", "backpressure", "orientation", "idempotency", "typed errors", "fencing", "cross-tenant"} {
		if !strings.Contains(readme, want) {
			t.Errorf("device-session README omits %q", want)
		}
	}
}

func validateDeviceRequests(example deviceSessionExample) []string {
	errors := []string{}
	seenIDs := map[string]bool{}
	seenTypes := map[string]bool{}
	currentChannelBinding := example.Binding.ChannelBindingSHA256
	for index, request := range example.Requests {
		if request.Sequence != index+1 || seenIDs[request.RequestID] {
			errors = append(errors, "request replay or sequence gap")
		}
		seenIDs[request.RequestID] = true
		seenTypes[request.Type] = true
		if request.TenantID != example.Binding.TenantID || request.PrincipalID != example.Binding.PrincipalID {
			errors = append(errors, "request authentication binding mismatch")
		}
		if request.Type == "reconnect" {
			if request.ChannelBindingSHA256 == currentChannelBinding {
				errors = append(errors, "reconnect did not rotate the channel binding")
			}
			currentChannelBinding = request.ChannelBindingSHA256
		} else if request.ChannelBindingSHA256 != currentChannelBinding {
			errors = append(errors, "request authentication binding mismatch")
		}
		if request.Type == "acquire" {
			if index != 0 {
				errors = append(errors, "acquire must be first")
			}
			continue
		}
		if request.Data["lease_id"] != example.Lease.LeaseID || intFromJSON(request.Data["generation"]) != example.Lease.Generation || request.Data["fencing_token_sha256"] != example.Lease.FencingTokenSHA256 {
			errors = append(errors, "request is not fenced to current lease")
		}
	}
	for _, requestType := range []string{"acquire", "input", "heartbeat", "reconnect", "cancel", "release"} {
		if !seenTypes[requestType] {
			errors = append(errors, "required request type is absent")
		}
	}
	if len(example.Requests) != 0 && example.Requests[len(example.Requests)-1].IdempotencyKey != example.Lease.ReleaseIdempotencyKey {
		errors = append(errors, "release request idempotency key mismatch")
	}
	return errors
}

func intFromJSON(value any) int {
	number, ok := value.(float64)
	if !ok {
		return 0
	}
	return int(number)
}

func TestDeviceSessionV1RejectsStaleLeaseReplayCrossTenantAndDuplicateRelease(t *testing.T) {
	var example deviceSessionExample
	loadStrictJSON(t, filepath.Join("device-session", "v1", "example.json"), &example)

	stale := append([]deviceSessionEvent(nil), example.Events...)
	stale[1].LeaseGeneration++
	if errors := validateDeviceSession(example.Binding.TenantID, example.Binding.PrincipalID, example.Lease.TenantID, example.Lease.OwnerPrincipalID, example.Lease.Generation, example.Lease.FencingTokenSHA256, example.Lease.ReleaseIdempotencyKey, stale); len(errors) == 0 {
		t.Fatal("stale lease generation was accepted")
	}

	replayed := append([]deviceSessionEvent(nil), example.Events...)
	replayed[1].EventID = replayed[0].EventID
	if errors := validateDeviceSession(example.Binding.TenantID, example.Binding.PrincipalID, example.Lease.TenantID, example.Lease.OwnerPrincipalID, example.Lease.Generation, example.Lease.FencingTokenSHA256, example.Lease.ReleaseIdempotencyKey, replayed); len(errors) == 0 {
		t.Fatal("replayed event ID was accepted")
	}

	if errors := validateDeviceSession(example.Binding.TenantID, example.Binding.PrincipalID, "other-tenant", example.Lease.OwnerPrincipalID, example.Lease.Generation, example.Lease.FencingTokenSHA256, example.Lease.ReleaseIdempotencyKey, example.Events); len(errors) == 0 {
		t.Fatal("cross-tenant lease was accepted")
	}

	duplicateRelease := append([]deviceSessionEvent(nil), example.Events...)
	terminal := duplicateRelease[len(duplicateRelease)-1]
	terminal.Sequence++
	terminal.EventID = "event-release-replay"
	duplicateRelease = append(duplicateRelease, terminal)
	if errors := validateDeviceSession(example.Binding.TenantID, example.Binding.PrincipalID, example.Lease.TenantID, example.Lease.OwnerPrincipalID, example.Lease.Generation, example.Lease.FencingTokenSHA256, example.Lease.ReleaseIdempotencyKey, duplicateRelease); len(errors) == 0 {
		t.Fatal("duplicate terminal release was accepted")
	}
}

func validateDeviceSession(tenantID, principalID, leaseTenantID, ownerPrincipalID string, generation int, fencingDigest, releaseKey string, events []deviceSessionEvent) []string {
	errors := []string{}
	if tenantID != leaseTenantID || principalID != ownerPrincipalID {
		errors = append(errors, "lease owner or tenant differs from authenticated binding")
	}
	seenIDs := map[string]bool{}
	state := "new"
	for index, event := range events {
		if event.Sequence != index+1 || seenIDs[event.EventID] {
			errors = append(errors, "event replay or sequence gap")
		}
		seenIDs[event.EventID] = true
		if event.LeaseGeneration != generation || event.FencingTokenSHA256 != fencingDigest {
			errors = append(errors, "stale lease generation or fencing token")
		}
		if state == "released" {
			errors = append(errors, "event after terminal release")
			continue
		}
		switch event.Type {
		case "acquired":
			if state != "new" || event.Data["tenant_id"] != tenantID || event.Data["owner_principal_id"] != principalID {
				errors = append(errors, "invalid or cross-tenant acquisition")
			}
			state = "active"
		case "frame", "input_ack", "heartbeat":
			if state != "active" {
				errors = append(errors, "active event outside active lifecycle")
			}
		case "disconnected":
			if state != "active" {
				errors = append(errors, "disconnect outside active lifecycle")
			}
			state = "disconnected"
		case "reconnected":
			if state != "disconnected" {
				errors = append(errors, "reconnect without disconnect")
			}
			state = "active"
		case "cancelled":
			if state != "active" && state != "disconnected" {
				errors = append(errors, "cancel outside live lifecycle")
			}
			state = "cancelled"
		case "released":
			if state != "active" && state != "disconnected" && state != "cancelled" {
				errors = append(errors, "release outside live lifecycle")
			}
			if event.Data["release_idempotency_key"] != releaseKey {
				errors = append(errors, "release idempotency key mismatch")
			}
			state = "released"
		case "error":
			if state == "new" {
				errors = append(errors, "error before acquisition")
			}
		default:
			errors = append(errors, "unknown lifecycle event")
		}
	}
	if state != "released" {
		errors = append(errors, "session has no terminal release")
	}
	return errors
}

func loadSchema(t *testing.T, namespace, version string) jsonSchemaHeader {
	t.Helper()
	var schema jsonSchemaHeader
	loadStrictJSON(t, filepath.Join(namespace, version, "schema.json"), &schema)
	if schema.Schema != "https://json-schema.org/draft/2020-12/schema" || schema.ID == "" || schema.Type != "object" || schema.AdditionalProperties {
		t.Fatalf("invalid %s/%s schema header: %+v", namespace, version, schema)
	}
	if len(schema.Properties) == 0 {
		t.Fatalf("%s/%s schema has no properties", namespace, version)
	}
	return schema
}

func requireSchemaFields(t *testing.T, schema jsonSchemaHeader, fields ...string) {
	t.Helper()
	for _, field := range fields {
		if !contains(schema.Required, field) {
			t.Errorf("schema required list omits %q", field)
		}
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("schema properties omit %q", field)
		}
	}
}

func loadStrictJSON(t *testing.T, path string, target any) {
	t.Helper()
	data := []byte(readContractFile(t, path))
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
