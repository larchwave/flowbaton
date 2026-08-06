package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestCommandSpanBuildsMetadataAndResetEventsWithoutAllocatingSequence(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(100, 0))
	timeline, err := newTimeline(clock, "root-1")
	if err != nil {
		t.Fatalf("newTimeline() error: %v", err)
	}
	parentCommand := model.Command{Kind: model.CommandRepeat, Arguments: map[string]any{"times": 2}}
	wantParentCommand := cloneCommand(parentCommand)
	parent, _, err := timeline.BeginCommand(parentCommand, 3)
	if err != nil {
		t.Fatalf("BeginCommand(parent) error: %v", err)
	}
	child, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 4)
	if err != nil {
		t.Fatalf("BeginCommand(child) error: %v", err)
	}
	childResult, _, err := child.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish(child) error: %v", err)
	}
	parentCommand.Arguments.(map[string]any)["times"] = 99

	clock.Advance(time.Millisecond)
	metadata := NewCommandMetadata(1, nil, []string{"iteration"}, "", "")
	updated, err := parent.MetadataUpdated(metadata)
	if err != nil {
		t.Fatalf("MetadataUpdated() error: %v", err)
	}
	if updated.Kind() != EventCommandMetadataUpdated || updated.RootRunID() != "root-1" ||
		updated.Sequence() != 1 || updated.Depth() != 3 || !updated.At().Equal(time.Unix(100, int64(time.Millisecond))) {
		t.Fatalf("metadata event identity = %#v", updated)
	}
	updatedCommand, ok := updated.Command()
	if !ok || !updatedCommand.Equivalent(wantParentCommand) || updated.Metadata().NumberOfRuns() != 1 {
		t.Fatalf("metadata event payload = command %#v present %t metadata %#v", updatedCommand, ok, updated.Metadata())
	}
	updatedCommand.Arguments.(map[string]any)["times"] = 100
	secondUpdatedCommand, _ := updated.Command()
	if !secondUpdatedCommand.Equivalent(wantParentCommand) {
		t.Fatalf("metadata event aliases command accessor mutation: %#v", secondUpdatedCommand)
	}

	clock.Advance(time.Millisecond)
	reset, err := parent.CommandReset(childResult)
	if err != nil {
		t.Fatalf("CommandReset() error: %v", err)
	}
	if reset.Kind() != EventCommandReset || reset.RootRunID() != childResult.RootRunID() ||
		reset.Sequence() != childResult.Sequence() || reset.Depth() != childResult.Depth() ||
		!reset.At().Equal(time.Unix(100, int64(2*time.Millisecond))) {
		t.Fatalf("reset event identity = %#v", reset)
	}
	resetCommand, ok := reset.Command()
	if !ok || !resetCommand.Equivalent(childResult.Command()) {
		t.Fatalf("reset event command = %#v, present %t", resetCommand, ok)
	}
	resetCommand.Arguments = map[string]any{"mutated": true}
	secondResetCommand, _ := reset.Command()
	if !secondResetCommand.Equivalent(childResult.Command()) {
		t.Fatalf("reset event aliases command accessor mutation: %#v", secondResetCommand)
	}
	if got := timeline.Checkpoint(); got != 2 {
		t.Fatalf("event factories allocated a sequence: checkpoint = %d, want 2", got)
	}
}

func TestCommandSpanEventFactoriesRejectInvalidOrFinishedParentState(t *testing.T) {
	t.Parallel()

	timeline, err := newTimeline(enginetest.NewFakeClock(time.Unix(200, 0)), "root-1")
	if err != nil {
		t.Fatalf("newTimeline() error: %v", err)
	}
	parent, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRepeat}, 2)
	if err != nil {
		t.Fatalf("BeginCommand(parent) error: %v", err)
	}
	child, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 3)
	if err != nil {
		t.Fatalf("BeginCommand(child) error: %v", err)
	}
	previous, _, err := child.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish(child) error: %v", err)
	}

	invalidMetadata := NewCommandMetadata(-1, nil, nil, "", "")
	if _, err := parent.MetadataUpdated(invalidMetadata); err == nil {
		t.Fatal("MetadataUpdated() accepted negative numberOfRuns")
	}
	invalidResets := []struct {
		name   string
		mutate func(*CommandResult)
	}{
		{name: "zero sequence", mutate: func(result *CommandResult) { result.sequence = 0 }},
		{name: "different root", mutate: func(result *CommandResult) { result.rootRunID = "root-2" }},
		{name: "non-immediate depth", mutate: func(result *CommandResult) { result.depth++ }},
	}
	for _, test := range invalidResets {
		test := test
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneCommandResult(previous)
			test.mutate(&candidate)
			if _, err := parent.CommandReset(candidate); err == nil {
				t.Fatalf("CommandReset() accepted %#v", candidate)
			}
		})
	}

	if _, _, err := parent.Finish(Completed, nil, NewCommandMetadata(1, nil, nil, "", "")); err != nil {
		t.Fatalf("Finish(parent) error: %v", err)
	}
	if _, err := parent.MetadataUpdated(NewCommandMetadata(1, nil, nil, "", "")); err == nil {
		t.Fatal("MetadataUpdated() accepted a finished parent")
	}
	if _, err := parent.CommandReset(previous); err == nil {
		t.Fatal("CommandReset() accepted a finished parent")
	}
}

func TestCommandResetRejectsRecordedChildOwnedByEarlierParent(t *testing.T) {
	t.Parallel()

	timeline, err := newTimeline(enginetest.NewFakeClock(time.Unix(250, 0)), "root-1")
	if err != nil {
		t.Fatalf("newTimeline() error: %v", err)
	}
	earlierParent, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRepeat}, 2)
	if err != nil {
		t.Fatalf("BeginCommand(earlier parent) error: %v", err)
	}
	earlierChild, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 3)
	if err != nil {
		t.Fatalf("BeginCommand(earlier child) error: %v", err)
	}
	earlierChildResult, _, err := earlierChild.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish(earlier child) error: %v", err)
	}
	if _, _, err := earlierParent.Finish(Completed, nil, CommandMetadata{}); err != nil {
		t.Fatalf("Finish(earlier parent) error: %v", err)
	}

	activeParent, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRepeat}, 2)
	if err != nil {
		t.Fatalf("BeginCommand(active parent) error: %v", err)
	}
	if _, err := activeParent.CommandReset(earlierChildResult); err == nil {
		t.Fatal("CommandReset() accepted a recorded child owned by an earlier parent")
	}
}

func TestCorePublishesCompositeMetadataAndResetEventsFromRecordedChild(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(300, 0))
	events := make([]Event, 0)
	listenerFailure := errors.New("diagnostic listener failure")
	dependencies := coreDependencies(enginetest.NewFakeDriver(), clock)
	dependencies.Listeners = []Listener{
		ListenerFunc(func(context.Context, Event) error { return listenerFailure }),
		ListenerFunc(func(_ context.Context, event Event) error {
			events = append(events, event)
			return nil
		}),
	}

	var retainedMetadata func(context.Context, CommandMetadata) error
	var retainedReset func(context.Context, CommandResult) error
	var invalidResetErrors []error
	core, err := newExecutorCoreForRootRun(
		dependencies,
		"root-1",
		handlerSpec{
			keyword: model.CommandRepeat, effectClass: EffectComposite,
			compile:  pureCompiler(func(command model.Command) (any, error) { return command.Children, nil }),
			evaluate: identityEvaluator,
			execute: func(ctx context.Context, state *executionState, evaluated evaluatedDispatch) (commandEffect, error) {
				children, executeErr := state.executeChildren(ctx, evaluated.command.Children)
				if executeErr != nil {
					return commandEffect{effectClass: EffectComposite}, executeErr
				}
				previous := children[0]

				unrecorded := cloneCommandResult(previous)
				unrecorded.sequence += 100
				invalidResetErrors = append(invalidResetErrors, state.resetCommand(ctx, unrecorded))
				mismatched := cloneCommandResult(previous)
				mismatched.outcome = Failed
				invalidResetErrors = append(invalidResetErrors, state.resetCommand(ctx, mismatched))
				forged := cloneCommandResult(previous)
				forged.identity = nil
				invalidResetErrors = append(invalidResetErrors, state.resetCommand(ctx, forged))

				clock.Advance(time.Millisecond)
				metadata := NewCommandMetadata(1, nil, []string{"iteration"}, "", "")
				if updateErr := state.updateCommandMetadata(ctx, metadata); updateErr != nil {
					return commandEffect{effectClass: EffectComposite}, updateErr
				}
				clock.Advance(time.Millisecond)
				if resetErr := state.resetCommand(ctx, previous); resetErr != nil {
					return commandEffect{effectClass: EffectComposite}, resetErr
				}
				retainedMetadata = state.updateCommandMetadata
				retainedReset = state.resetCommand
				clock.Advance(time.Millisecond)
				return commandEffect{effectClass: EffectComposite, numberOfRuns: 1, numberOfRunsSet: true}, nil
			},
		},
		handlerSpec{
			keyword: model.CommandBack, effectClass: EffectObserved,
			compile:  pureCompiler(func(model.Command) (any, error) { return struct{}{}, nil }),
			evaluate: identityEvaluator,
			execute: func(context.Context, *executionState, evaluatedDispatch) (commandEffect, error) {
				return commandEffect{effectClass: EffectObserved}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("newExecutorCoreForRootRun() error: %v", err)
	}

	parent, err := core.execute(context.Background(), model.Command{
		Kind: model.CommandRepeat, Children: []model.Command{{Kind: model.CommandBack}},
	}, 2)
	if err != nil {
		t.Fatalf("execute(parent) error: %v", err)
	}
	for index, invalidErr := range invalidResetErrors {
		if invalidErr == nil {
			t.Fatalf("invalid reset %d succeeded", index)
		}
	}
	wantKinds := []EventKind{
		EventCommandStarted,
		EventCommandStarted,
		EventCommandFinished,
		EventCommandMetadataUpdated,
		EventCommandReset,
		EventCommandFinished,
	}
	gotKinds := make([]EventKind, len(events))
	for index, event := range events {
		gotKinds[index] = event.Kind()
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("event order = %v, want %v", gotKinds, wantKinds)
	}
	metadataEvent := events[3]
	if metadataEvent.RootRunID() != parent.RootRunID() || metadataEvent.Sequence() != parent.Sequence() ||
		metadataEvent.Depth() != parent.Depth() || metadataEvent.Metadata().NumberOfRuns() != 1 {
		t.Fatalf("metadata event = %#v, want parent identity and iteration metadata", metadataEvent)
	}
	resetEvent := events[4]
	ledger := core.ledger.snapshot()
	if len(ledger) != 2 {
		t.Fatalf("ledger = %#v, want parent and child", ledger)
	}
	childResult := ledger[1]
	if resetEvent.RootRunID() != childResult.RootRunID() || resetEvent.Sequence() != childResult.Sequence() ||
		resetEvent.Depth() != childResult.Depth() {
		t.Fatalf("reset event = %#v, want child identity %#v", resetEvent, childResult)
	}
	if resetEvent.Outcome() != "" || resetEvent.ProductError() != nil || resetEvent.Metadata().HasNumberOfRuns() {
		t.Fatalf("reset event leaked terminal child payload: %#v", resetEvent)
	}
	returnedLogs := metadataEvent.Metadata().LogMessages()
	returnedLogs[0] = "mutated"
	if got := metadataEvent.Metadata().LogMessages(); !reflect.DeepEqual(got, []string{"iteration"}) {
		t.Fatalf("metadata event aliases accessor mutation: %v", got)
	}

	eventCount := len(events)
	if err := retainedMetadata(context.Background(), NewCommandMetadata(2, nil, nil, "", "")); err == nil {
		t.Fatal("retained metadata callback accepted finished parent")
	}
	if err := retainedReset(context.Background(), childResult); err == nil {
		t.Fatal("retained reset callback accepted finished parent")
	}
	if len(events) != eventCount {
		t.Fatalf("retained callbacks dispatched after finish: before %d after %d", eventCount, len(events))
	}
	if got := core.timeline.Checkpoint(); got != 2 {
		t.Fatalf("metadata/reset events allocated sequence: %d", got)
	}
}
