package engine

import (
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestCommandResultLedgerSortsParentAndChildBySequence(t *testing.T) {
	t.Parallel()

	timeline, err := NewTimeline(enginetest.NewFakeClock(time.Unix(100, 0)))
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	parentSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRunFlow}, 0)
	if err != nil {
		t.Fatalf("BeginCommand parent error: %v", err)
	}
	childSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 1)
	if err != nil {
		t.Fatalf("BeginCommand child error: %v", err)
	}
	child, _, err := childSpan.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish child error: %v", err)
	}
	parent, _, err := parentSpan.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish parent error: %v", err)
	}

	ledger := newCommandResultLedger()
	if err := ledger.record(child); err != nil {
		t.Fatalf("record child error: %v", err)
	}
	if err := ledger.record(parent); err != nil {
		t.Fatalf("record parent error: %v", err)
	}
	got := ledger.snapshot()
	if len(got) != 2 || got[0].Sequence() != 1 || got[1].Sequence() != 2 {
		t.Fatalf("snapshot sequences = %v, want [1 2]", commandSequences(got))
	}
}

func TestCommandResultLedgerRejectsInvalidAndDuplicateResults(t *testing.T) {
	t.Parallel()

	valid := CommandResult{sequence: 1, depth: 0, command: model.Command{Kind: model.CommandBack}}
	var nilLedger *commandResultLedger
	if err := nilLedger.record(valid); err == nil {
		t.Fatal("nil ledger record error = nil")
	}

	ledger := newCommandResultLedger()
	if err := ledger.record(CommandResult{depth: 0}); err == nil {
		t.Fatal("zero sequence record error = nil")
	}
	if err := ledger.record(CommandResult{sequence: 2, depth: -1}); err == nil {
		t.Fatal("negative depth record error = nil")
	}
	if err := ledger.record(valid); err != nil {
		t.Fatalf("valid record error: %v", err)
	}
	duplicate := valid
	duplicate.command = model.Command{Kind: model.CommandLaunchApp}
	if err := ledger.record(duplicate); err == nil {
		t.Fatal("duplicate sequence record error = nil")
	}
	got := ledger.snapshot()
	if len(got) != 1 || got[0].Command().Kind != model.CommandBack {
		t.Fatalf("duplicate changed ledger: %#v", got)
	}
}

func TestCommandResultLedgerSnapshotAfterIsExclusiveAndDefensivelyCopied(t *testing.T) {
	t.Parallel()

	ledger := newCommandResultLedger()
	for sequence := uint64(3); sequence >= 1; sequence-- {
		result := CommandResult{
			sequence: sequence,
			depth:    int(sequence - 1),
			command: model.Command{
				Kind:      model.CommandInputText,
				Arguments: map[string]any{"text": "original"},
			},
			artifacts: []device.Artifact{{
				Kind: "trace", Path: "owned/trace.txt",
				Metadata: map[string]string{"owner": "original"},
			}},
		}
		if err := ledger.record(result); err != nil {
			t.Fatalf("record sequence %d error: %v", sequence, err)
		}
		if sequence == 1 {
			break
		}
	}

	afterOne := ledger.snapshotAfter(1)
	if got := commandSequences(afterOne); len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("snapshotAfter(1) sequences = %v, want [2 3]", got)
	}
	if got := ledger.snapshotAfter(3); len(got) != 0 {
		t.Fatalf("snapshotAfter(3) = %#v, want empty", got)
	}

	afterOne[0].command.Arguments.(map[string]any)["text"] = "accessor-mutated"
	afterOne[0].artifacts[0].Path = "accessor-mutated.txt"
	afterOne[0].artifacts[0].Metadata["owner"] = "accessor-mutated"
	again := ledger.snapshotAfter(1)
	if got := again[0].Command().Arguments.(map[string]any)["text"]; got != "original" {
		t.Fatalf("snapshot command = %#v, want original", got)
	}
	if got := again[0].Artifacts()[0]; got.Path != "owned/trace.txt" || got.Metadata["owner"] != "original" {
		t.Fatalf("snapshot artifact = %#v, want original", got)
	}
}

func commandSequences(results []CommandResult) []uint64 {
	sequences := make([]uint64, len(results))
	for index := range results {
		sequences[index] = results[index].Sequence()
	}
	return sequences
}
