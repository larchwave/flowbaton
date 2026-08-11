package aiengine

import (
	"fmt"
	"strings"

	"github.com/larchwave/flowbaton/internal/explore"
)

// ChatModelsFromEnv builds the explore model tiers from the same environment
// surface as FromEnv: FLOWBATON_AI_PROVIDER selects the backend, the
// provider's own key variable authenticates it, and FLOWBATON_AI_MODEL,
// FLOWBATON_AI_BASE_URL and FLOWBATON_AI_TIMEOUT shape the shared client.
//
// The manager tier runs on FLOWBATON_AI_MODEL (or the provider default).
// FLOWBATON_AI_WORKER_MODEL and FLOWBATON_AI_VISION_MODEL optionally point
// their tiers at other models on the same provider; blank falls back to the
// manager client itself.
//
// With no key configured the return is a zero ModelSet and nil error — the
// caller fails closed, matching FromEnv.
func ChatModelsFromEnv(getenv func(string) string) (explore.ModelSet, error) {
	built, err := FromEnv(getenv)
	if err != nil {
		return explore.ModelSet{}, err
	}
	if built == nil {
		return explore.ModelSet{}, nil
	}
	base, ok := built.(*Engine)
	if !ok {
		return explore.ModelSet{}, fmt.Errorf("aiengine: unexpected engine type %T", built)
	}
	manager := NewChatClient(base.model, base.provider, "", base.timeout)
	set := explore.ModelSet{Manager: manager, Worker: manager, Vision: manager}
	if worker := strings.TrimSpace(getenv("FLOWBATON_AI_WORKER_MODEL")); worker != "" {
		set.Worker = NewChatClient(base.model, base.provider, worker, base.timeout)
	}
	if vision := strings.TrimSpace(getenv("FLOWBATON_AI_VISION_MODEL")); vision != "" {
		set.Vision = NewChatClient(base.model, base.provider, vision, base.timeout)
	}
	return set, nil
}
