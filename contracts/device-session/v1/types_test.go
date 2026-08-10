package devicesessionv1

import (
	"testing"
	"time"
)

func TestTypedRequestAndEventConstructorsPreserveIdentity(t *testing.T) {
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	identity := AuthenticatedContext{TenantID: "tenant-1", PrincipalID: "principal-1", ChannelBindingSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	request, err := NewRequest(1, "request-1", "acquire", "idempotency-key-1", identity, now, map[string]any{"resource_selector": "device-1", "requested_capabilities": []string{"tap"}})
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	event, err := NewEvent(1, "event-001", "acquired", now, LeaseFence{LeaseID: "lease-001", Generation: 1, FencingTokenSHA256: identity.ChannelBindingSHA256}, map[string]any{"lease_id": "lease-001"})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if request.TenantID != identity.TenantID || event.LeaseGeneration != 1 {
		t.Fatalf("constructors lost identity: request=%#v event=%#v", request, event)
	}
}
