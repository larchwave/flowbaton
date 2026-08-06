package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nohavewho/flowbaton/internal/capability"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

// The workspace contract defines ordered and parallel continuation behavior.
// specs/03-cli-tooling.md:30 requires "sequence flows first
// (continueOnFailure), then parallel-eligible flows". Only the flows
// executionOrder.flowsOrder named are gated, and only when continueOnFailure is
// false. Everything else always runs.

func suiteOf(t *testing.T, flows ...string) *Program {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(flows))
	for index, body := range flows {
		path := filepath.Join(root, string(rune('a'+index))+".yaml")
		if err := os.WriteFile(path, []byte("appId: com.example\n---\n"+body+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	program, err := Prepare(context.Background(),
		model.ExecutionPlan{SelectedRoots: paths}, capability.FileLoader{})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	return program
}

func runSuite(t *testing.T, program *Program, sequenced int, continueOnFailure bool) []FlowResult {
	t.Helper()
	results, _ := Execute(context.Background(), program, Dependencies{
		ExecutionID:       "suite-continuation",
		Driver:            enginetest.NewFakeDriver(),
		Clock:             newAdvancingClock(),
		JSFactory:         tapJSFactory(t),
		Controller:        NoopController{},
		SequencedRoots:    sequenced,
		ContinueOnFailure: continueOnFailure,
	})
	return results
}

// Nothing is in the sequence, so nothing gates: a failure does not end the run.
func TestAFailedFlowDoesNotEndASuiteThatNamedNoOrder(t *testing.T) {
	t.Parallel()

	program := suiteOf(t, "- assertTrue: \"false\"", "- assertTrue: \"true\"")
	if got := runSuite(t, program, 0, false); len(got) != 2 {
		t.Fatalf("ran %d flows, want both", len(got))
	}
}

// An ordered sequence is the case where stopping is the point: the flows were
// declared to depend on each other.
func TestAFailedFlowEndsAnOrderedSequence(t *testing.T) {
	t.Parallel()

	program := suiteOf(t, "- assertTrue: \"false\"", "- assertTrue: \"true\"")
	if got := runSuite(t, program, 2, false); len(got) != 1 {
		t.Fatalf("ran %d flows, want to stop after the failure", len(got))
	}
}

func TestContinueOnFailureCarriesAnOrderedSequencePastAFailure(t *testing.T) {
	t.Parallel()

	program := suiteOf(t, "- assertTrue: \"false\"", "- assertTrue: \"true\"")
	if got := runSuite(t, program, 2, true); len(got) != 2 {
		t.Fatalf("ran %d flows, want both", len(got))
	}
}

// A flow after the gated prefix runs even when the prefix stopped — no. The
// prefix stopping IS the end of the run, and this pins that reading: with one
// flow in the sequence and one after it, a failure in the sequence ends both.
func TestAFailedSequenceStopsTheFlowsBehindIt(t *testing.T) {
	t.Parallel()

	program := suiteOf(t, "- assertTrue: \"false\"", "- assertTrue: \"true\"")
	if got := runSuite(t, program, 1, false); len(got) != 1 {
		t.Fatalf("ran %d flows, want the run to end with the sequence", len(got))
	}
}

// Continuing is not swallowing. The suite still reports the failure, or CI
// would call a broken run green.
func TestAContinuedSuiteStillFails(t *testing.T) {
	t.Parallel()

	program := suiteOf(t, "- assertTrue: \"false\"", "- assertTrue: \"true\"")
	_, err := Execute(context.Background(), program, Dependencies{
		ExecutionID: "suite-continuation-error",
		Driver:      enginetest.NewFakeDriver(),
		Clock:       newAdvancingClock(),
		JSFactory:   tapJSFactory(t),
		Controller:  NoopController{},
	})
	if err == nil {
		t.Fatal("a suite with a failed flow reported no error")
	}
}
