package research

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// The Observer's own doc says it "settles the screen within a bound", and
// its note names a timeout: "screen still moving after 5s, capturing
// anyway". It asked once. internal/ios answers that question by reading two
// frames a tenth of a second apart and internal/android asks the agent once;
// both leave the waiting to their caller, so the note named a bound nothing
// had waited for -- live mmx51 printed it 27 times, mmx52 15 times.
func TestObserveWaitsForTheScreenToSettle(t *testing.T) {
	t.Parallel()

	driver := &fakeDriver{staticSeq: []bool{false, false, true}, tree: testTree()}
	observer := &Observer{Driver: driver, AppID: "app", SettleTimeout: 2 * time.Second}
	if _, err := observer.Observe(context.Background()); err != nil {
		t.Fatalf("Observe error = %v", err)
	}
	if driver.staticCalls != 3 {
		t.Fatalf("asked %d time(s), want 3: the observer must wait for the screen to settle",
			driver.staticCalls)
	}
}

// A screen that never settles is still captured -- the note says so -- but
// only after the bound is spent, and the note may name that bound only
// because it was actually waited out.
func TestObserveSpendsTheSettleBoundBeforeCapturingAMovingScreen(t *testing.T) {
	t.Parallel()

	driver := &fakeDriver{static: false, tree: testTree()}
	notes := &strings.Builder{}
	observer := &Observer{
		Driver:        driver,
		AppID:         "app",
		SettleTimeout: 300 * time.Millisecond,
		Logf:          func(format string, args ...any) { fmt.Fprintf(notes, format, args...) },
	}
	started := time.Now()
	if _, err := observer.Observe(context.Background()); err != nil {
		t.Fatalf("Observe error = %v", err)
	}
	elapsed := time.Since(started)
	if elapsed < 250*time.Millisecond {
		t.Fatalf("returned after %s, want the 300ms bound spent", elapsed)
	}
	// Both ends matter. Fewer than two answers means nothing waited; a
	// flood means the wait spins instead of pacing itself, and every one of
	// those answers is a round trip to the device. A 300ms bound at the
	// 100ms cadence is about four, with room for a loaded machine.
	if driver.staticCalls < 2 || driver.staticCalls > 6 {
		t.Fatalf("asked %d time(s), want between 2 and 6 across a 300ms bound at %s",
			driver.staticCalls, settlePollInterval)
	}
	if got := notes.String(); !strings.Contains(got, "still moving after 300ms") {
		t.Fatalf("note = %q, want it to name the bound that was waited out", got)
	}
}

// A settled screen costs one question and no wait at all.
func TestObserveAsksOnceWhenTheScreenIsAlreadyStatic(t *testing.T) {
	t.Parallel()

	driver := &fakeDriver{static: true, tree: testTree()}
	observer := &Observer{Driver: driver, AppID: "app", SettleTimeout: time.Hour}
	started := time.Now()
	if _, err := observer.Observe(context.Background()); err != nil {
		t.Fatalf("Observe error = %v", err)
	}
	if driver.staticCalls != 1 {
		t.Fatalf("asked %d time(s), want 1 on an already static screen", driver.staticCalls)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("took %s on a static screen, want no wait", elapsed)
	}
}
