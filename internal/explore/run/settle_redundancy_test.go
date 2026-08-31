package run

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
)

// Every mutation asked the driver to settle and then threw the answer away
// (`_, _ =`), while neither mobile driver waits: internal/ios answers
// WaitForAppToSettle with one IsScreenStatic sample and internal/android
// with one isWindowUpdating, both discarding the request's TimeoutMillis.
// So the call could not settle anything -- it was a round trip whose result
// was dropped -- and the observation that follows it is where the waiting
// now happens, with a bound it actually keeps.
func TestMutationDoesNotAskTheDriverToSettleAndDropTheAnswer(t *testing.T) {
	t.Parallel()

	session, driver := inputSession(t, screen("Search", textField("query", false, true)))
	handler := session.box().Handlers["input_text"]
	if _, err := handler(context.Background(), json.RawMessage(`{"text":"hello"}`)); err != nil {
		t.Fatalf("input_text: %v", err)
	}
	if slices.Contains(driver.calls, "Settle") {
		t.Fatalf("driver calls = %v, want no discarded settle probe", driver.calls)
	}
}

// The observation after a mutation is what settles the screen, so it must
// still happen and still record its step.
func TestMutationStillReobservesTheScreen(t *testing.T) {
	t.Parallel()

	session, _ := inputSession(t, screen("Search", textField("query", false, true)))
	before := len(session.steps)
	handler := session.box().Handlers["input_text"]
	if _, err := handler(context.Background(), json.RawMessage(`{"text":"hello"}`)); err != nil {
		t.Fatalf("input_text: %v", err)
	}
	if len(session.steps) != before+1 {
		t.Fatalf("steps = %d, want one recorded step", len(session.steps))
	}
}
