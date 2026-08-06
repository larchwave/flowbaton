package devicesessionv1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidateAcceptsAuthenticatedFencedTranscript(t *testing.T) {
	data := readExample(t)
	err := ValidateJSON(data, AuthenticatedContext{
		TenantID:             "tenant-example",
		PrincipalID:          "studio-core-host-01",
		AuthProfileID:        "remote-cloud-mac-v1",
		ChannelBindingSHA256: strings.Repeat("b", 64),
		RequestNonce:         "nonce-01JZEXAMPLE-0001",
	}, RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z"))
	if err != nil {
		t.Fatalf("valid transcript rejected: %v", err)
	}
}

func TestValidateRejectsContextExpiryAndMixedProfile(t *testing.T) {
	data := readExample(t)
	base := AuthenticatedContext{
		TenantID:             "tenant-example",
		PrincipalID:          "studio-core-host-01",
		AuthProfileID:        "remote-cloud-mac-v1",
		ChannelBindingSHA256: strings.Repeat("b", 64),
		RequestNonce:         "nonce-01JZEXAMPLE-0001",
	}

	crossTenant := base
	crossTenant.TenantID = "other-tenant"
	if err := ValidateJSON(data, crossTenant, RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("cross-tenant context accepted")
	}
	if err := ValidateJSON(data, base, RemoteCloudMacV1, mustTime(t, "2026-07-15T12:06:00Z")); err == nil {
		t.Fatal("expired binding/lease accepted")
	}
	mixed := RemoteCloudMacV1
	mixed.Authentication = "peer-credentials+launch-token"
	if err := ValidateJSON(data, base, mixed, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("mixed local/remote profile accepted")
	}
}

func TestValidateRejectsStaleFenceAndInvalidFrameLinks(t *testing.T) {
	data := string(readExample(t))
	stale := strings.Replace(data, `"generation": 7,`, `"generation": 8,`, 1)
	if err := ValidateJSON([]byte(stale), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("stale request generation accepted")
	}

	unknownFrame := strings.Replace(data, `"based_on_frame_sequence": 1`, `"based_on_frame_sequence": 99`, 1)
	if err := ValidateJSON([]byte(unknownFrame), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("input referencing an unknown frame accepted")
	}

	badReconnect := strings.Replace(data, `"last_acknowledged_sequence": 4`, `"last_acknowledged_sequence": 99`, 1)
	if err := ValidateJSON([]byte(badReconnect), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("reconnect beyond acknowledged server sequence accepted")
	}
}

func TestValidateAcceptsOrdinaryPathWithoutOptionalOperations(t *testing.T) {
	var document sessionDocument
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Requests = filterRequests(document.Requests, "acquire", "input", "release")
	document.Events = filterEvents(document.Events, "acquired", "frame", "input_ack", "released")
	document.Events[len(document.Events)-1].Data = json.RawMessage(`{"release_idempotency_key":"release-01JZEXAMPLE-0001","outcome":"completed","generation":7}`)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err != nil {
		t.Fatalf("ordinary acquire/input/release path rejected: %v", err)
	}
}

func TestValidateRejectsPostReleaseRequestAndCausalFrameMismatch(t *testing.T) {
	var document sessionDocument
	document = sessionDocument{}
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	postRelease := document.Requests[1]
	postRelease.Sequence = len(document.Requests) + 1
	postRelease.RequestID = "request-after-release-0007"
	postRelease.IdempotencyKey = "input-after-release-0007"
	postRelease.Timestamp = "2026-07-15T12:00:09Z"
	document.Requests = append(document.Requests, postRelease)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:09Z")); err == nil {
		t.Fatal("post-release request accepted")
	}

	document = sessionDocument{}
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Requests[1].Data = json.RawMessage(`{"lease_id":"lease-01JZEXAMPLE","generation":7,"fencing_token_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","based_on_stream_epoch":2,"based_on_frame_sequence":1,"command":"tap","payload_sha256":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}`)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("input bound to a frame from another stream epoch accepted")
	}
}

func TestValidateRejectsInvalidRenewalAndLateEvent(t *testing.T) {
	var document sessionDocument
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Events[3].Data = json.RawMessage(`{"request_id":"request-heartbeat-0003","idempotency_key":"heartbeat-01JZEXAMPLE-0001","lease_expires_at":"2026-07-15T12:09:59Z","generation":7}`)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("heartbeat event granting an unrequested extension accepted")
	}

	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Events[len(document.Events)-1].Timestamp = "2026-07-15T12:10:01Z"
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("event after authenticated binding expiry accepted")
	}
}

func TestValidateRejectsInputWithoutNegotiatedCapability(t *testing.T) {
	var document sessionDocument
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Capabilities = withoutString(document.Capabilities, "tap")
	var acquire struct {
		ResourceSelector      string   `json:"resource_selector"`
		RequestedCapabilities []string `json:"requested_capabilities"`
	}
	if err := decodeStrict(document.Requests[0].Data, &acquire); err != nil {
		t.Fatal(err)
	}
	acquire.RequestedCapabilities = withoutString(acquire.RequestedCapabilities, "tap")
	document.Requests[0].Data = mustJSON(t, acquire)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("input command without negotiated capability accepted")
	}
}

func TestValidateAcceptsExactTerminalRequestRetriesAndRejectsOutcomeDrift(t *testing.T) {
	var document sessionDocument
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	cancelRetry := document.Requests[4]
	cancelRetry.Sequence = 6
	cancelRetry.RequestID = "cancel-request-retry-0002"
	cancelRetry.Timestamp = "2026-07-15T12:00:08.025Z"
	release := document.Requests[5]
	release.Sequence = 7
	document.Requests = append(document.Requests[:5], cancelRetry, release)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err != nil {
		t.Fatalf("exact cancellation retry rejected: %v", err)
	}

	document = sessionDocument{}
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	releaseRetry := document.Requests[5]
	releaseRetry.Sequence = 7
	releaseRetry.RequestID = "request-release-retry-0007"
	releaseRetry.Timestamp = "2026-07-15T12:00:08.075Z"
	document.Requests = append(document.Requests, releaseRetry)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err != nil {
		t.Fatalf("exact release retry rejected: %v", err)
	}

	document = sessionDocument{}
	if err := decodeStrict(readExample(t), &document); err != nil {
		t.Fatal(err)
	}
	document.Events[len(document.Events)-1].Data = json.RawMessage(`{"release_idempotency_key":"release-01JZEXAMPLE-0001","outcome":"completed","generation":7}`)
	if err := ValidateJSON(mustDocumentJSON(t, document), validContext(), RemoteCloudMacV1, mustTime(t, "2026-07-15T12:00:08Z")); err == nil {
		t.Fatal("released outcome contradicting cancellation accepted")
	}
}

func validContext() AuthenticatedContext {
	return AuthenticatedContext{
		TenantID:             "tenant-example",
		PrincipalID:          "studio-core-host-01",
		AuthProfileID:        "remote-cloud-mac-v1",
		ChannelBindingSHA256: strings.Repeat("b", 64),
		RequestNonce:         "nonce-01JZEXAMPLE-0001",
	}
}

func readExample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("example.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustDocumentJSON(t *testing.T, document sessionDocument) []byte {
	t.Helper()
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func withoutString(values []string, excluded string) []string {
	result := []string{}
	for _, value := range values {
		if value != excluded {
			result = append(result, value)
		}
	}
	return result
}

func filterRequests(items []request, allowed ...string) []request {
	set := map[string]bool{}
	for _, value := range allowed {
		set[value] = true
	}
	result := []request{}
	for _, item := range items {
		if set[item.Type] {
			item.Sequence = len(result) + 1
			result = append(result, item)
		}
	}
	return result
}

func filterEvents(items []event, allowed ...string) []event {
	set := map[string]bool{}
	for _, value := range allowed {
		set[value] = true
	}
	result := []event{}
	for _, item := range items {
		if set[item.Type] {
			item.Sequence = len(result) + 1
			result = append(result, item)
		}
	}
	return result
}
