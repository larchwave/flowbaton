package devicesessionv1

import (
	"encoding/json"
	"fmt"
	"regexp"
	"time"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var requestTypes = map[string]bool{"acquire": true, "input": true, "heartbeat": true, "reconnect": true, "cancel": true, "release": true}
var eventTypes = map[string]bool{"acquired": true, "frame": true, "input_ack": true, "heartbeat": true, "disconnected": true, "reconnected": true, "cancelled": true, "released": true, "error": true}

// Document is the public DeviceSession v1 lifecycle transcript.
type Document = sessionDocument

// Binding binds a session to its authenticated transport identity.
type Binding = binding

// Lease contains the current generation and fencing digest.
type Lease = lease

// Request is one ordered request-plane message.
type Request = request

// Event is one ordered control-plane event.
type Event = event

// NewDocument constructs and validates a complete DeviceSession v1 transcript.
func NewDocument(sessionID string, sessionBinding Binding, sessionLease Lease, capabilities []string, requests []Request, events []Event, context AuthenticatedContext, profile AuthProfile, at time.Time) (Document, error) {
	document := Document{
		SchemaVersion: 1, ContractVersion: "v1", SessionID: sessionID,
		Binding: sessionBinding, Lease: sessionLease, Capabilities: append([]string(nil), capabilities...),
		Requests: append([]Request(nil), requests...), Events: append([]Event(nil), events...),
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return Document{}, fmt.Errorf("encode DeviceSession v1: %w", err)
	}
	if err := ValidateJSON(encoded, context, profile, at); err != nil {
		return Document{}, err
	}
	return document, nil
}

// NewRequest builds a typed request using strict JSON payload encoding.
func NewRequest(sequence int, requestID, kind, idempotencyKey string, identity AuthenticatedContext, timestamp time.Time, payload any) (Request, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Request{}, fmt.Errorf("encode DeviceSession request payload: %w", err)
	}
	if sequence < 1 || len(requestID) < 8 || !requestTypes[kind] || len(idempotencyKey) < 16 || len(identity.TenantID) < 3 || len(identity.PrincipalID) < 3 || !digestPattern.MatchString(identity.ChannelBindingSHA256) || timestamp.IsZero() || len(data) < 2 || data[0] != '{' {
		return Request{}, fmt.Errorf("DeviceSession request identity is incomplete")
	}
	return Request{Sequence: sequence, RequestID: requestID, Type: kind, Timestamp: timestamp.UTC().Format(time.RFC3339Nano), IdempotencyKey: idempotencyKey, TenantID: identity.TenantID, PrincipalID: identity.PrincipalID, ChannelBindingSHA256: identity.ChannelBindingSHA256, Data: data}, nil
}

// NewEvent builds a typed event using strict JSON payload encoding.
func NewEvent(sequence int, eventID, kind string, timestamp time.Time, fence LeaseFence, payload any) (Event, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode DeviceSession event payload: %w", err)
	}
	if sequence < 1 || len(eventID) < 8 || !eventTypes[kind] || timestamp.IsZero() || fence.Generation < 1 || !digestPattern.MatchString(fence.FencingTokenSHA256) || len(data) < 2 || data[0] != '{' {
		return Event{}, fmt.Errorf("DeviceSession event identity is incomplete")
	}
	return Event{Sequence: sequence, EventID: eventID, Type: kind, Timestamp: timestamp.UTC().Format(time.RFC3339Nano), LeaseGeneration: fence.Generation, FencingTokenSHA256: fence.FencingTokenSHA256, Data: data}, nil
}
