// Package devicesessionv1 implements the normative semantic checks that JSON
// Schema cannot express across authenticated context, lease fencing, and time.
package devicesessionv1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type AuthenticatedContext struct {
	TenantID             string
	PrincipalID          string
	AuthProfileID        string
	ChannelBindingSHA256 string
	RequestNonce         string
}

type AuthProfile struct {
	ProfileID        string
	Transport        string
	Authentication   string
	ChannelBinding   string
	PrincipalScope   string
	TenantScope      string
	ReplayProtection string
}

var LocalCoreV1 = AuthProfile{
	ProfileID: "local-core-v1", Transport: "authenticated-local-ipc",
	Authentication: "peer-credentials+launch-token", ChannelBinding: "os-peer-identity+endpoint",
	PrincipalScope: "local-user+process", TenantScope: "single-host",
	ReplayProtection: "per-launch-nonce+expiry",
}

var RemoteCloudMacV1 = AuthProfile{
	ProfileID: "remote-cloud-mac-v1", Transport: "authenticated-remote-ipc",
	Authentication: "mutual-tls+signed-session-token", ChannelBinding: "tls-exporter",
	PrincipalScope: "account+host+process", TenantScope: "tenant-id",
	ReplayProtection: "nonce+expiry+lease-generation",
}

type sessionDocument struct {
	SchemaVersion   int       `json:"schema_version"`
	ContractVersion string    `json:"contract_version"`
	SessionID       string    `json:"session_id"`
	Binding         binding   `json:"binding"`
	Lease           lease     `json:"lease"`
	Capabilities    []string  `json:"capabilities"`
	Requests        []request `json:"requests"`
	Events          []event   `json:"events"`
}

type binding struct {
	TenantID             string `json:"tenant_id"`
	PrincipalID          string `json:"principal_id"`
	AuthProfileID        string `json:"auth_profile_id"`
	ChannelBindingSHA256 string `json:"channel_binding_sha256"`
	RequestNonce         string `json:"request_nonce"`
	IssuedAt             string `json:"issued_at"`
	ExpiresAt            string `json:"expires_at"`
}

type lease struct {
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

type request struct {
	Sequence             int             `json:"sequence"`
	RequestID            string          `json:"request_id"`
	Type                 string          `json:"type"`
	Timestamp            string          `json:"timestamp"`
	IdempotencyKey       string          `json:"idempotency_key"`
	TenantID             string          `json:"tenant_id"`
	PrincipalID          string          `json:"principal_id"`
	ChannelBindingSHA256 string          `json:"channel_binding_sha256"`
	Data                 json.RawMessage `json:"data"`
}

type event struct {
	Sequence           int             `json:"sequence"`
	EventID            string          `json:"event_id"`
	Type               string          `json:"type"`
	Timestamp          string          `json:"timestamp"`
	LeaseGeneration    int             `json:"lease_generation"`
	FencingTokenSHA256 string          `json:"fencing_token_sha256"`
	Data               json.RawMessage `json:"data"`
}

type LeaseFence struct {
	LeaseID            string `json:"lease_id"`
	Generation         int    `json:"generation"`
	FencingTokenSHA256 string `json:"fencing_token_sha256"`
}

type frameRef struct {
	StreamEpoch int
	Sequence    int
}

type requestFact struct {
	RequestID      string
	Type           string
	IdempotencyKey string
	Timestamp      time.Time
	Frame          frameRef
	ExtensionMS    int
	LastAck        int
}

type requestFacts struct {
	ByIdempotency map[string]requestFact
	Ordered       []requestFact
}

type renewal struct {
	EffectiveAt time.Time
	ExpiresAt   time.Time
}

type eventFacts struct {
	Frames           map[frameRef]time.Time
	Renewals         []renewal
	FinalLeaseExpiry time.Time
}

func ValidateJSON(data []byte, context AuthenticatedContext, profile AuthProfile, at time.Time) error {
	var document sessionDocument
	if err := decodeStrict(data, &document); err != nil {
		return fmt.Errorf("decode DeviceSession v1: %w", err)
	}
	errors := []string{}
	if document.SchemaVersion != 1 || document.ContractVersion != "v1" {
		errors = append(errors, "unsupported contract version")
	}
	if profile != LocalCoreV1 && profile != RemoteCloudMacV1 {
		errors = append(errors, "authentication profile is not a valid discriminated v1 profile")
	}
	if document.Binding.AuthProfileID != profile.ProfileID || context.AuthProfileID != profile.ProfileID {
		errors = append(errors, "authentication profile does not match negotiated binding")
	}
	if document.Binding.TenantID != context.TenantID || document.Lease.TenantID != context.TenantID {
		errors = append(errors, "tenant differs from authenticated context")
	}
	if document.Binding.PrincipalID != context.PrincipalID || document.Lease.OwnerPrincipalID != context.PrincipalID {
		errors = append(errors, "principal differs from authenticated context")
	}
	if document.Binding.ChannelBindingSHA256 != context.ChannelBindingSHA256 {
		errors = append(errors, "channel binding differs from authenticated transport")
	}
	if document.Binding.RequestNonce != context.RequestNonce {
		errors = append(errors, "request nonce differs from authenticated token")
	}

	bindingIssued := parseTime(&errors, "binding issued_at", document.Binding.IssuedAt)
	bindingExpires := parseTime(&errors, "binding expires_at", document.Binding.ExpiresAt)
	leaseAcquired := parseTime(&errors, "lease acquired_at", document.Lease.AcquiredAt)
	leaseExpires := parseTime(&errors, "lease expires_at", document.Lease.ExpiresAt)
	if !bindingIssued.IsZero() && !bindingExpires.IsZero() && !bindingIssued.Before(bindingExpires) {
		errors = append(errors, "binding validity interval is empty")
	}
	if !leaseAcquired.IsZero() && !leaseExpires.IsZero() && !leaseAcquired.Before(leaseExpires) {
		errors = append(errors, "lease validity interval is empty")
	}
	requestFacts, requestErrors := validateRequests(document, context, bindingIssued, bindingExpires)
	errors = append(errors, requestErrors...)
	eventFacts, eventErrors := validateEvents(document, requestFacts, bindingIssued, bindingExpires, leaseAcquired, leaseExpires)
	errors = append(errors, eventErrors...)
	errors = append(errors, validateRequestLeaseAndFrameTimes(requestFacts, eventFacts, leaseAcquired, leaseExpires)...)
	if !at.IsZero() && (!within(at, bindingIssued, bindingExpires) || !within(at, leaseAcquired, eventFacts.FinalLeaseExpiry)) {
		errors = append(errors, "authenticated binding or lease is not live at verification time")
	}
	if len(errors) != 0 {
		sort.Strings(errors)
		return fmt.Errorf("DeviceSession v1 semantic validation: %s", strings.Join(errors, "; "))
	}
	return nil
}

func validateRequests(document sessionDocument, context AuthenticatedContext, bindingIssued, bindingExpires time.Time) (requestFacts, []string) {
	errors := []string{}
	facts := requestFacts{ByIdempotency: map[string]requestFact{}}
	seenIDs := map[string]bool{}
	seenIdempotency := map[string]string{}
	state := "new"
	lastTimestamp := time.Time{}
	seenAcquire := false
	seenRelease := false
	for index, item := range document.Requests {
		if item.Sequence != index+1 || seenIDs[item.RequestID] {
			errors = append(errors, "request sequence gap or replayed request_id")
		}
		seenIDs[item.RequestID] = true
		if item.TenantID != context.TenantID || item.PrincipalID != context.PrincipalID || item.ChannelBindingSHA256 != context.ChannelBindingSHA256 {
			errors = append(errors, "request authentication binding mismatch")
		}
		canonicalData := compactJSON(item.Data)
		requestIdentity := item.Type + ":" + canonicalData
		previousIdentity, duplicateIdempotency := seenIdempotency[item.IdempotencyKey]
		exactRetry := duplicateIdempotency && previousIdentity == requestIdentity
		if duplicateIdempotency && !exactRetry {
			errors = append(errors, "idempotency key reused for a different request")
		}
		seenIdempotency[item.IdempotencyKey] = requestIdentity
		when := parseTime(&errors, "request timestamp", item.Timestamp)
		if !when.IsZero() && !lastTimestamp.IsZero() && when.Before(lastTimestamp) {
			errors = append(errors, "request timestamps are not monotonic")
		}
		if !when.IsZero() {
			lastTimestamp = when
		}
		if !when.IsZero() && !within(when, bindingIssued, bindingExpires) {
			errors = append(errors, "request lies outside authenticated binding")
		}
		fact := requestFact{RequestID: item.RequestID, Type: item.Type, IdempotencyKey: item.IdempotencyKey, Timestamp: when}

		switch item.Type {
		case "acquire":
			var payload struct {
				ResourceSelector      string   `json:"resource_selector"`
				RequestedCapabilities []string `json:"requested_capabilities"`
			}
			decodePayload(&errors, item.Data, &payload)
			if index != 0 || state != "new" || payload.ResourceSelector != document.Lease.ResourceID || !subset(payload.RequestedCapabilities, document.Capabilities) {
				errors = append(errors, "invalid acquire request")
			}
			state = "active"
			seenAcquire = true
		case "input":
			var payload struct {
				LeaseFence
				BasedOnStreamEpoch   int    `json:"based_on_stream_epoch"`
				BasedOnFrameSequence int    `json:"based_on_frame_sequence"`
				Command              string `json:"command"`
				PayloadSHA256        string `json:"payload_sha256"`
			}
			decodePayload(&errors, item.Data, &payload)
			checkFence(&errors, payload.LeaseFence, document.Lease)
			if state != "active" {
				errors = append(errors, "input request outside active lifecycle")
			}
			if !subset([]string{payload.Command}, document.Capabilities) {
				errors = append(errors, "input command lacks negotiated capability")
			}
			fact.Frame = frameRef{StreamEpoch: payload.BasedOnStreamEpoch, Sequence: payload.BasedOnFrameSequence}
		case "heartbeat":
			var payload struct {
				LeaseFence
				RequestedExtensionMS int `json:"requested_extension_ms"`
			}
			decodePayload(&errors, item.Data, &payload)
			checkFence(&errors, payload.LeaseFence, document.Lease)
			if state != "active" || payload.RequestedExtensionMS <= 0 {
				errors = append(errors, "heartbeat request outside active lifecycle or invalid extension")
			}
			fact.ExtensionMS = payload.RequestedExtensionMS
		case "reconnect":
			var payload struct {
				LeaseFence
				SessionID                string `json:"session_id"`
				LastAcknowledgedSequence int    `json:"last_acknowledged_sequence"`
			}
			decodePayload(&errors, item.Data, &payload)
			checkFence(&errors, payload.LeaseFence, document.Lease)
			if state != "active" || payload.SessionID != document.SessionID || payload.LastAcknowledgedSequence >= len(document.Events) {
				errors = append(errors, "invalid reconnect cursor or session")
			}
			fact.LastAck = payload.LastAcknowledgedSequence
		case "cancel":
			var payload struct {
				LeaseFence
				Reason string `json:"reason"`
			}
			decodePayload(&errors, item.Data, &payload)
			checkFence(&errors, payload.LeaseFence, document.Lease)
			if state != "active" && !(state == "cancelled" && exactRetry) {
				errors = append(errors, "cancel request outside active lifecycle")
			}
			if state == "active" {
				state = "cancelled"
			}
		case "release":
			var payload LeaseFence
			decodePayload(&errors, item.Data, &payload)
			checkFence(&errors, payload, document.Lease)
			firstRelease := state == "active" || state == "cancelled"
			releaseRetry := state == "released" && exactRetry
			if (!firstRelease && !releaseRetry) || item.IdempotencyKey != document.Lease.ReleaseIdempotencyKey {
				errors = append(errors, "release idempotency key mismatch")
			}
			if firstRelease {
				state = "released"
			}
			seenRelease = true
		default:
			errors = append(errors, "unknown request type")
		}
		facts.Ordered = append(facts.Ordered, fact)
		if _, exists := facts.ByIdempotency[item.IdempotencyKey]; !exists {
			facts.ByIdempotency[item.IdempotencyKey] = fact
		}
	}
	if !seenAcquire || !seenRelease || state != "released" {
		errors = append(errors, "session requires one acquire and terminal release request")
	}
	return facts, errors
}

func validateEvents(document sessionDocument, requests requestFacts, bindingIssued, bindingExpires, leaseAcquired, leaseExpires time.Time) (eventFacts, []string) {
	errors := []string{}
	facts := eventFacts{Frames: map[frameRef]time.Time{}, FinalLeaseExpiry: leaseExpires}
	seenIDs := map[string]bool{}
	consumedRequestKeys := map[string]bool{}
	state := "new"
	lastFrame := 0
	streamEpoch := 0
	lastDisconnectedSequence := 0
	lastTimestamp := time.Time{}
	effectiveExpiry := leaseExpires
	for index, item := range document.Events {
		if item.Sequence != index+1 || seenIDs[item.EventID] {
			errors = append(errors, "event sequence gap or replayed event_id")
		}
		seenIDs[item.EventID] = true
		when := parseTime(&errors, "event timestamp", item.Timestamp)
		if !when.IsZero() && !lastTimestamp.IsZero() && when.Before(lastTimestamp) {
			errors = append(errors, "event timestamps are not monotonic")
		}
		if !when.IsZero() {
			lastTimestamp = when
			if !within(when, bindingIssued, bindingExpires) {
				errors = append(errors, "event lies outside authenticated binding")
			}
			if !within(when, leaseAcquired, effectiveExpiry) {
				errors = append(errors, "event lies outside effective lease lifetime")
			}
		}
		if item.LeaseGeneration != document.Lease.Generation || item.FencingTokenSHA256 != document.Lease.FencingTokenSHA256 {
			errors = append(errors, "event carries stale lease generation or fence")
		}
		if state == "released" {
			errors = append(errors, "event appears after terminal release")
			continue
		}
		switch item.Type {
		case "acquired":
			var payload struct {
				LeaseID          string `json:"lease_id"`
				ResourceID       string `json:"resource_id"`
				TenantID         string `json:"tenant_id"`
				OwnerPrincipalID string `json:"owner_principal_id"`
				Generation       int    `json:"generation"`
			}
			decodePayload(&errors, item.Data, &payload)
			if state != "new" || payload.LeaseID != document.Lease.LeaseID || payload.ResourceID != document.Lease.ResourceID || payload.TenantID != document.Lease.TenantID || payload.OwnerPrincipalID != document.Lease.OwnerPrincipalID || payload.Generation != document.Lease.Generation {
				errors = append(errors, "acquired event does not match lease")
			}
			state = "active"
		case "frame":
			var payload struct {
				StreamEpoch          int    `json:"stream_epoch"`
				FrameSequence        int    `json:"frame_sequence"`
				Orientation          string `json:"orientation"`
				Width                int    `json:"width"`
				Height               int    `json:"height"`
				ContentSHA256        string `json:"content_sha256"`
				QueueDepth           int    `json:"queue_depth"`
				DroppedSincePrevious int    `json:"dropped_since_previous"`
			}
			decodePayload(&errors, item.Data, &payload)
			if state != "active" || payload.StreamEpoch < streamEpoch || (payload.StreamEpoch == streamEpoch && payload.FrameSequence <= lastFrame) {
				errors = append(errors, "non-monotonic frame or frame outside active state")
			}
			if payload.StreamEpoch > streamEpoch {
				streamEpoch = payload.StreamEpoch
				lastFrame = 0
			}
			lastFrame = payload.FrameSequence
			ref := frameRef{StreamEpoch: payload.StreamEpoch, Sequence: payload.FrameSequence}
			if _, exists := facts.Frames[ref]; exists {
				errors = append(errors, "duplicate frame identity")
			}
			facts.Frames[ref] = when
		case "input_ack":
			var payload struct {
				InputID              string `json:"input_id"`
				IdempotencyKey       string `json:"idempotency_key"`
				BasedOnStreamEpoch   int    `json:"based_on_stream_epoch"`
				BasedOnFrameSequence int    `json:"based_on_frame_sequence"`
				LatencyMS            int    `json:"latency_ms"`
				Result               string `json:"result"`
			}
			decodePayload(&errors, item.Data, &payload)
			request, ok := requests.ByIdempotency[payload.IdempotencyKey]
			ref := frameRef{StreamEpoch: payload.BasedOnStreamEpoch, Sequence: payload.BasedOnFrameSequence}
			frameTime, hasFrame := facts.Frames[ref]
			if state != "active" || !ok || request.Type != "input" || request.Frame != ref || !hasFrame || (!when.IsZero() && when.Before(request.Timestamp)) || (!request.Timestamp.IsZero() && request.Timestamp.Before(frameTime)) {
				errors = append(errors, "input acknowledgement is not bound to request/frame")
			}
		case "heartbeat":
			var payload struct {
				RequestID      string `json:"request_id"`
				IdempotencyKey string `json:"idempotency_key"`
				LeaseExpiresAt string `json:"lease_expires_at"`
				Generation     int    `json:"generation"`
			}
			decodePayload(&errors, item.Data, &payload)
			request, ok := requests.ByIdempotency[payload.IdempotencyKey]
			newExpiry := parseTime(&errors, "heartbeat lease_expires_at", payload.LeaseExpiresAt)
			expectedExpiry := effectiveExpiry.Add(time.Duration(request.ExtensionMS) * time.Millisecond)
			if state != "active" || payload.Generation != document.Lease.Generation || !ok || request.Type != "heartbeat" || request.RequestID != payload.RequestID || consumedRequestKeys[payload.IdempotencyKey] || (!when.IsZero() && when.Before(request.Timestamp)) || request.Timestamp.After(effectiveExpiry) || newExpiry != expectedExpiry || newExpiry.After(bindingExpires) {
				errors = append(errors, "heartbeat outside active lease or generation mismatch")
			}
			if !newExpiry.IsZero() && newExpiry == expectedExpiry && !newExpiry.After(bindingExpires) {
				effectiveExpiry = newExpiry
				facts.Renewals = append(facts.Renewals, renewal{EffectiveAt: when, ExpiresAt: newExpiry})
			}
			consumedRequestKeys[payload.IdempotencyKey] = true
		case "disconnected":
			var payload struct {
				Reason             string `json:"reason"`
				LastServerSequence int    `json:"last_server_sequence"`
			}
			decodePayload(&errors, item.Data, &payload)
			if state != "active" || payload.LastServerSequence > index {
				errors = append(errors, "invalid disconnect cursor/state")
			}
			lastDisconnectedSequence = payload.LastServerSequence
			state = "disconnected"
		case "reconnected":
			var payload struct {
				RequestID          string `json:"request_id"`
				IdempotencyKey     string `json:"idempotency_key"`
				ResumeFromSequence int    `json:"resume_from_sequence"`
				StreamEpoch        int    `json:"stream_epoch"`
				Generation         int    `json:"generation"`
			}
			decodePayload(&errors, item.Data, &payload)
			request, ok := requests.ByIdempotency[payload.IdempotencyKey]
			if state != "disconnected" || !ok || request.Type != "reconnect" || request.RequestID != payload.RequestID || request.LastAck != payload.ResumeFromSequence || payload.ResumeFromSequence > lastDisconnectedSequence || payload.StreamEpoch <= streamEpoch || payload.Generation != document.Lease.Generation || consumedRequestKeys[payload.IdempotencyKey] || (!when.IsZero() && when.Before(request.Timestamp)) {
				errors = append(errors, "invalid reconnect state/cursor/epoch")
			}
			consumedRequestKeys[payload.IdempotencyKey] = true
			streamEpoch = payload.StreamEpoch
			lastFrame = 0
			state = "active"
		case "cancelled":
			var payload struct {
				RequestID       string `json:"request_id"`
				IdempotencyKey  string `json:"idempotency_key"`
				Reason          string `json:"reason"`
				TerminalOutcome string `json:"terminal_outcome"`
			}
			decodePayload(&errors, item.Data, &payload)
			request, ok := requests.ByIdempotency[payload.IdempotencyKey]
			if (state != "active" && state != "disconnected") || !ok || request.Type != "cancel" || request.RequestID != payload.RequestID || consumedRequestKeys[payload.IdempotencyKey] || (!when.IsZero() && when.Before(request.Timestamp)) {
				errors = append(errors, "cancellation is not bound to a live request")
			}
			if payload.TerminalOutcome != "cancelled" {
				errors = append(errors, "cancelled event has contradictory terminal outcome")
			}
			consumedRequestKeys[payload.IdempotencyKey] = true
			state = "cancelled"
		case "released":
			var payload struct {
				ReleaseIdempotencyKey string `json:"release_idempotency_key"`
				Outcome               string `json:"outcome"`
				Generation            int    `json:"generation"`
			}
			decodePayload(&errors, item.Data, &payload)
			request, ok := requests.ByIdempotency[payload.ReleaseIdempotencyKey]
			releaseState := state
			if (releaseState != "active" && releaseState != "disconnected" && releaseState != "cancelled") || !ok || request.Type != "release" || payload.ReleaseIdempotencyKey != document.Lease.ReleaseIdempotencyKey || payload.Generation != document.Lease.Generation || consumedRequestKeys[payload.ReleaseIdempotencyKey] || (!when.IsZero() && when.Before(request.Timestamp)) {
				errors = append(errors, "invalid terminal release")
			}
			if (releaseState == "cancelled") != (payload.Outcome == "cancelled") {
				errors = append(errors, "released outcome contradicts lifecycle state")
			}
			consumedRequestKeys[payload.ReleaseIdempotencyKey] = true
			state = "released"
		case "error":
			var payload struct {
				Code        string `json:"code"`
				Retryable   bool   `json:"retryable"`
				SafeMessage string `json:"safe_message"`
			}
			decodePayload(&errors, item.Data, &payload)
			if state == "new" {
				errors = append(errors, "error before acquisition")
			}
		default:
			errors = append(errors, "unknown event type")
		}
	}
	if state != "released" {
		errors = append(errors, "session lacks terminal release")
	}
	facts.FinalLeaseExpiry = effectiveExpiry
	return facts, errors
}

func validateRequestLeaseAndFrameTimes(requests requestFacts, events eventFacts, leaseAcquired, initialExpiry time.Time) []string {
	errors := []string{}
	for _, request := range requests.Ordered {
		if request.Type == "acquire" || request.Timestamp.IsZero() {
			continue
		}
		expiry := initialExpiry
		for _, renewal := range events.Renewals {
			if renewal.EffectiveAt.After(request.Timestamp) {
				break
			}
			expiry = renewal.ExpiresAt
		}
		if !within(request.Timestamp, leaseAcquired, expiry) {
			errors = append(errors, "fenced request lies outside effective lease lifetime")
		}
		if request.Type == "input" {
			frameTime, ok := events.Frames[request.Frame]
			if !ok || request.Timestamp.Before(frameTime) {
				errors = append(errors, "input names unknown or future frame identity")
			}
		}
	}
	return errors
}

func checkFence(errors *[]string, value LeaseFence, expected lease) {
	if value.LeaseID != expected.LeaseID || value.Generation != expected.Generation || value.FencingTokenSHA256 != expected.FencingTokenSHA256 {
		*errors = append(*errors, "request carries stale lease generation or fence")
	}
}

func decodePayload(errors *[]string, data []byte, target any) {
	if err := decodeStrict(data, target); err != nil {
		*errors = append(*errors, "invalid typed payload: "+err.Error())
	}
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

func parseTime(errors *[]string, label, value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		*errors = append(*errors, label+" is invalid")
	}
	return parsed
}

func within(value, start, end time.Time) bool {
	return !value.Before(start) && !value.After(end)
}

func subset(values, allowed []string) bool {
	set := map[string]bool{}
	for _, value := range allowed {
		set[value] = true
	}
	for _, value := range values {
		if !set[value] {
			return false
		}
	}
	return true
}

func compactJSON(data []byte) string {
	var buffer bytes.Buffer
	if err := json.Compact(&buffer, data); err != nil {
		return string(data)
	}
	return buffer.String()
}

func requestKeys(requests []request, kind string) map[string]bool {
	keys := map[string]bool{}
	for _, item := range requests {
		if item.Type == kind {
			keys[item.IdempotencyKey] = true
		}
	}
	return keys
}
