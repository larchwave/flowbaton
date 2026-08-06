package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestClassifyOutcomeUsesTypedErrorTaxonomy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		optional bool
		want     Outcome
	}{
		{name: "success", want: Completed},
		{name: "skipped", err: NewCommandSkippedError("skip", nil), want: Skipped},
		{name: "cancelled", err: context.Canceled, want: Cancelled},
		{name: "deadline", err: context.DeadlineExceeded, want: Cancelled},
		{name: "optional operation", err: NewOperationError("missing", nil), optional: true, want: Warned},
		{name: "optional assertion", err: NewAssertionError("assertion", nil), optional: true, want: Warned},
		{name: "required operation", err: NewOperationError("missing", nil), want: Failed},
		{name: "device", err: NewDeviceConnectionError("offline", nil), optional: true, want: Failed},
		{name: "configuration", err: NewConfigurationError("bad", nil), optional: true, want: Failed},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ClassifyOutcome(test.err, test.optional); got != test.want {
				t.Fatalf("ClassifyOutcome = %q, want %q", got, test.want)
			}
		})
	}
}

func TestSpansSanitizeMalformedProductErrorsBeforeStorage(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	timeline, err := NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline() error: %v", err)
	}
	var typedNil *OperationError
	commandSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandAssertVisible}, 0)
	if err != nil {
		t.Fatalf("BeginCommand() error: %v", err)
	}
	command, commandEvent, err := commandSpan.Finish(Failed, typedNil, CommandMetadata{})
	if err != nil {
		t.Fatalf("CommandSpan.Finish() error: %v", err)
	}
	commandSafe := assertSafeConfigurationError(t, command.ProductError())
	if commandEvent.ProductError() != commandSafe {
		t.Fatalf("command event error type = %T, want shared safe error", commandEvent.ProductError())
	}

	cycle := &graphCausalError{}
	cycle.cause = cycle
	flowSpan, _, err := timeline.BeginFlow("/workspace/malformed.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow() error: %v", err)
	}
	flow, flowEvent, err := flowSpan.Finish(Failed, cycle, []CommandResult{command})
	if err != nil {
		t.Fatalf("FlowSpan.Finish() error: %v", err)
	}
	flowSafe := assertSafeConfigurationError(t, flow.ProductError())
	if flowEvent.ProductError() != flowSafe {
		t.Fatalf("flow event error type = %T, want shared safe error", flowEvent.ProductError())
	}
}

func TestTimelineBuildsImmutableMonotonicCommandAndFlowRecords(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 15, 15, 0, 0, 0, time.UTC)
	clock := enginetest.NewFakeClock(start)
	timeline, err := NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	flowSpan, flowStarted, err := timeline.BeginFlow("/workspace/root.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow error: %v", err)
	}
	if flowStarted.Kind() != EventFlowStarted || flowStarted.Depth() != 0 || !flowStarted.At().Equal(start) {
		t.Fatalf("flow started event = %#v", flowStarted)
	}

	command := model.Command{
		Kind:      model.CommandLaunchApp,
		Arguments: map[string]any{"appId": "com.example"},
	}
	commandSpan, commandStarted, err := timeline.BeginCommand(command, 0)
	if err != nil {
		t.Fatalf("BeginCommand error: %v", err)
	}
	if commandStarted.Kind() != EventCommandStarted || commandStarted.Sequence() != 1 || commandStarted.Depth() != 0 {
		t.Fatalf("command started event = %#v", commandStarted)
	}
	command.Arguments.(map[string]any)["appId"] = "caller-mutated"
	startedCommand, ok := commandStarted.Command()
	if !ok || startedCommand.Arguments.(map[string]any)["appId"] != "com.example" {
		t.Fatalf("started command = %#v, %v", startedCommand, ok)
	}

	evaluated := model.Command{Kind: model.CommandLaunchApp, Arguments: map[string]any{"evaluated": "original"}}
	logs := []string{"first"}
	metadata := NewCommandMetadata(2, &evaluated, logs, "warning insight", "reasoning")
	evaluated.Arguments.(map[string]any)["evaluated"] = "caller-mutated"
	logs[0] = "caller-mutated"
	clock.Advance(125 * time.Millisecond)
	first, commandFinished, err := commandSpan.Finish(Completed, nil, metadata)
	if err != nil {
		t.Fatalf("Finish command error: %v", err)
	}
	if first.Sequence() != 1 || first.Depth() != 0 || first.Outcome() != Completed || first.Duration() != 125*time.Millisecond {
		t.Fatalf("first command result = %#v", first)
	}
	if commandFinished.Kind() != EventCommandFinished || commandFinished.Sequence() != 1 || commandFinished.Outcome() != Completed {
		t.Fatalf("command finished event = %#v", commandFinished)
	}
	assertOriginalMetadata(t, first.Metadata())
	assertOriginalMetadata(t, commandFinished.Metadata())

	nestedSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 1)
	if err != nil {
		t.Fatalf("nested BeginCommand error: %v", err)
	}
	clock.Advance(75 * time.Millisecond)
	nested, _, err := nestedSpan.Finish(Skipped, NewCommandSkippedError("condition false", nil), CommandMetadata{})
	if err != nil {
		t.Fatalf("nested Finish error: %v", err)
	}
	if nested.Sequence() != 2 || nested.Depth() != 1 || nested.Duration() != 75*time.Millisecond {
		t.Fatalf("nested result = %#v", nested)
	}

	flow, flowFinished, err := flowSpan.Finish(Completed, nil, []CommandResult{first, nested})
	if err != nil {
		t.Fatalf("Finish flow error: %v", err)
	}
	if flow.Path() != "/workspace/root.yaml" || flow.Depth() != 0 || flow.Outcome() != Completed || flow.Duration() != 200*time.Millisecond {
		t.Fatalf("flow result = %#v", flow)
	}
	if flowFinished.Kind() != EventFlowFinished || flowFinished.Outcome() != Completed || flowFinished.Depth() != 0 {
		t.Fatalf("flow finished event = %#v", flowFinished)
	}
	commands := flow.Commands()
	if len(commands) != 2 || commands[0].Sequence() != 1 || commands[1].Sequence() != 2 {
		t.Fatalf("flow commands = %#v", commands)
	}

	returnedCommand := first.Command()
	returnedCommand.Arguments.(map[string]any)["appId"] = "accessor-mutated"
	if got := first.Command().Arguments.(map[string]any)["appId"]; got != "com.example" {
		t.Fatalf("second Command appId = %#v, want com.example", got)
	}
	returnedLogs := first.Metadata().LogMessages()
	returnedLogs[0] = "accessor-mutated"
	assertOriginalMetadata(t, first.Metadata())
}

func TestCommandSpanFinishWithArtifactsOwnsImmutableArtifacts(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(30, 0))
	timeline, err := NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	span, _, err := timeline.BeginCommand(model.Command{
		Kind:      model.CommandTakeScreenshot,
		Arguments: map[string]any{"name": "checkout"},
	}, 1)
	if err != nil {
		t.Fatalf("BeginCommand error: %v", err)
	}

	artifacts := []device.Artifact{{
		Kind: "screenshot", Path: "owned/checkout.png",
		Metadata: map[string]string{"screen": "checkout"},
	}}
	result, finished, err := span.FinishWithArtifacts(Completed, nil, CommandMetadata{}, artifacts)
	if err != nil {
		t.Fatalf("FinishWithArtifacts error: %v", err)
	}
	artifacts[0].Path = "caller-mutated.png"
	artifacts[0].Metadata["screen"] = "caller-mutated"

	assertOriginalArtifacts(t, result.Artifacts())
	assertOriginalArtifacts(t, finished.Artifacts())
	returned := result.Artifacts()
	returned[0].Path = "accessor-mutated.png"
	returned[0].Metadata["screen"] = "accessor-mutated"
	assertOriginalArtifacts(t, result.Artifacts())

	flowSpan, _, err := timeline.BeginFlow("root.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow error: %v", err)
	}
	flow, _, err := flowSpan.Finish(Completed, nil, []CommandResult{result})
	if err != nil {
		t.Fatalf("Finish flow error: %v", err)
	}
	flowCommands := flow.Commands()
	flowCommands[0].artifacts[0].Path = "flow-accessor-mutated.png"
	flowCommands[0].artifacts[0].Metadata["screen"] = "flow-accessor-mutated"
	assertOriginalArtifacts(t, flow.Commands()[0].Artifacts())
}

func TestCommandSpanFinishCompatibilityProducesNoArtifacts(t *testing.T) {
	t.Parallel()

	timeline, err := NewTimeline(enginetest.NewFakeClock(time.Unix(40, 0)))
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	span, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 0)
	if err != nil {
		t.Fatalf("BeginCommand error: %v", err)
	}
	result, finished, err := span.Finish(Completed, nil, CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish error: %v", err)
	}
	if len(result.Artifacts()) != 0 || len(finished.Artifacts()) != 0 {
		t.Fatalf("Finish artifacts = %#v / %#v, want none", result.Artifacts(), finished.Artifacts())
	}
}

func TestTimelineCheckpointIncludesAllocatedButUnfinishedCommands(t *testing.T) {
	t.Parallel()

	timeline, err := NewTimeline(enginetest.NewFakeClock(time.Unix(50, 0)))
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	if got := timeline.Checkpoint(); got != 0 {
		t.Fatalf("initial Checkpoint = %d, want 0", got)
	}
	parent, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandRunFlow}, 0)
	if err != nil {
		t.Fatalf("parent BeginCommand error: %v", err)
	}
	if got := timeline.Checkpoint(); got != 1 {
		t.Fatalf("Checkpoint with unfinished parent = %d, want 1", got)
	}
	child, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 1)
	if err != nil {
		t.Fatalf("child BeginCommand error: %v", err)
	}
	if got := timeline.Checkpoint(); got != 2 {
		t.Fatalf("Checkpoint with unfinished child = %d, want 2", got)
	}
	if _, _, err := child.Finish(Completed, nil, CommandMetadata{}); err != nil {
		t.Fatalf("child Finish error: %v", err)
	}
	if _, _, err := parent.Finish(Completed, nil, CommandMetadata{}); err != nil {
		t.Fatalf("parent Finish error: %v", err)
	}
	if got := timeline.Checkpoint(); got != 2 {
		t.Fatalf("Checkpoint after finishes = %d, want 2", got)
	}
}

func assertOriginalArtifacts(t *testing.T, artifacts []device.Artifact) {
	t.Helper()
	if len(artifacts) != 1 || artifacts[0].Kind != "screenshot" || artifacts[0].Path != "owned/checkout.png" {
		t.Fatalf("artifacts = %#v, want original screenshot", artifacts)
	}
	if artifacts[0].Metadata["screen"] != "checkout" {
		t.Fatalf("artifact metadata = %#v, want original screen", artifacts[0].Metadata)
	}
}

func assertOriginalMetadata(t *testing.T, metadata CommandMetadata) {
	t.Helper()
	if metadata.NumberOfRuns() != 2 || metadata.Insight() != "warning insight" || metadata.AIReasoning() != "reasoning" {
		t.Fatalf("metadata scalars = %#v", metadata)
	}
	if got := metadata.LogMessages(); len(got) != 1 || got[0] != "first" {
		t.Fatalf("metadata logs = %#v, want first", got)
	}
	evaluated, ok := metadata.EvaluatedCommand()
	if !ok || evaluated.Arguments.(map[string]any)["evaluated"] != "original" {
		t.Fatalf("evaluated command = %#v, %v", evaluated, ok)
	}
}

func TestTimelineRejectsInvalidDepthOutcomeReuseAndCommandOrder(t *testing.T) {
	t.Parallel()

	if timeline, err := NewTimeline(nil); timeline != nil || err == nil {
		t.Fatalf("NewTimeline(nil) = %#v, %v; want nil error", timeline, err)
	}
	clock := enginetest.NewFakeClock(time.Unix(20, 0))
	timeline, err := NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline error: %v", err)
	}
	if span, _, err := timeline.BeginCommand(model.Command{}, -1); span != nil || err == nil {
		t.Fatalf("negative BeginCommand = %#v, %v", span, err)
	}
	if span, _, err := timeline.BeginFlow("root", "", -1); span != nil || err == nil {
		t.Fatalf("negative BeginFlow = %#v, %v", span, err)
	}

	firstSpan, _, _ := timeline.BeginCommand(model.Command{Kind: model.CommandLaunchApp}, 0)
	if _, _, err := firstSpan.Finish(Outcome("Unknown"), nil, CommandMetadata{}); err == nil {
		t.Fatal("invalid command outcome accepted")
	}
	first, _, err := firstSpan.Finish(Failed, errors.New("product"), CommandMetadata{})
	if err != nil {
		t.Fatalf("first Finish error: %v", err)
	}
	if first.ProductError() == nil || first.StartedAt().IsZero() || first.FinishedAt().IsZero() {
		t.Fatalf("first result accessors = %#v", first)
	}
	if _, _, err := firstSpan.Finish(Completed, nil, CommandMetadata{}); err == nil {
		t.Fatal("second command Finish succeeded")
	}
	secondSpan, _, _ := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 1)
	second, _, _ := secondSpan.Finish(Completed, nil, CommandMetadata{})
	flowSpan, started, _ := timeline.BeginFlow("root", "", 0)
	if started.FlowPath() != "root" {
		t.Fatalf("flow event path = %q, want root", started.FlowPath())
	}
	if _, ok := started.Command(); ok {
		t.Fatal("flow event unexpectedly contains a command")
	}
	if _, _, err := flowSpan.Finish(Completed, nil, []CommandResult{second, first}); err == nil {
		t.Fatal("out-of-order commands accepted")
	}
	flow, finished, err := flowSpan.Finish(Failed, errors.New("flow product"), []CommandResult{first, second})
	if err != nil {
		t.Fatalf("flow Finish error: %v", err)
	}
	if flow.ProductError() == nil || flow.StartedAt().IsZero() || flow.FinishedAt().IsZero() || finished.ProductError() == nil {
		t.Fatalf("flow result/event accessors = %#v / %#v", flow, finished)
	}
	if _, _, err := flowSpan.Finish(Completed, nil, nil); err == nil {
		t.Fatal("second flow Finish succeeded")
	}
}
