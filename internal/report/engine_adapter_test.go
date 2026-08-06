package report

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/enginetest"
	"github.com/nohavewho/flowbaton/internal/model"
)

func TestFromEngineFlowResultMapsCompletedIdentityAndMarshals(t *testing.T) {
	started := time.Date(2026, time.July, 15, 14, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	clock := enginetest.NewFakeClock(started)
	timeline, err := engine.NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline() error = %v", err)
	}
	flowSpan, _, err := timeline.BeginFlow("/workspace/checkout.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow() error = %v", err)
	}
	commandSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandBack}, 0)
	if err != nil {
		t.Fatalf("BeginCommand() error = %v", err)
	}
	clock.Advance(125 * time.Millisecond)
	command, _, err := commandSpan.Finish(engine.Completed, nil, engine.CommandMetadata{})
	if err != nil {
		t.Fatalf("Finish command() error = %v", err)
	}
	flow, _, err := flowSpan.Finish(engine.Completed, nil, []engine.CommandResult{command})
	if err != nil {
		t.Fatalf("Finish flow() error = %v", err)
	}

	got, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error = %v", err)
	}
	// An unnamed flow uses the file stem as its name and keeps the full file.
	if got.Name != "checkout" || got.File != "checkout.yaml" ||
		got.Description != "/workspace/checkout.yaml" || got.Status != Completed {
		t.Fatalf("flow identity/status = %q / %q / %q / %q",
			got.Name, got.File, got.Description, got.Status)
	}
	if !got.StartedAt.Equal(started.UTC()) || got.StartedAt.Location() != time.UTC || got.DurationMillis != 125 {
		t.Fatalf("flow timing = %v / %d", got.StartedAt, got.DurationMillis)
	}
	if len(got.Commands) != 1 || got.Commands[0].Keyword != "back" || got.Commands[0].Description != "back" {
		t.Fatalf("commands = %#v", got.Commands)
	}
	if _, err := MarshalCommands(got); err != nil {
		t.Fatalf("MarshalCommands() error = %v", err)
	}
}

func TestFromEngineFlowResultSafelyFormatsSanitizedMalformedSpanErrors(t *testing.T) {
	t.Parallel()

	clock := enginetest.NewFakeClock(time.Unix(0, 0))
	timeline, err := engine.NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline() error: %v", err)
	}
	var malformed *engine.OperationError
	commandSpan, _, err := timeline.BeginCommand(model.Command{Kind: model.CommandAssertVisible}, 0)
	if err != nil {
		t.Fatalf("BeginCommand() error: %v", err)
	}
	command, _, err := commandSpan.Finish(engine.Failed, malformed, engine.CommandMetadata{})
	if err != nil {
		t.Fatalf("CommandSpan.Finish() error: %v", err)
	}
	flowSpan, _, err := timeline.BeginFlow("/workspace/malformed.yaml", "", 0)
	if err != nil {
		t.Fatalf("BeginFlow() error: %v", err)
	}
	flow, _, err := flowSpan.Finish(engine.Failed, malformed, []engine.CommandResult{command})
	if err != nil {
		t.Fatalf("FlowSpan.Finish() error: %v", err)
	}

	converted, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error: %v", err)
	}
	if converted.Failure == nil || converted.Failure.Message == "" ||
		len(converted.Commands) != 1 || converted.Commands[0].Failure == nil || converted.Commands[0].Failure.Message == "" {
		t.Fatalf("sanitized failures = flow %#v commands %#v", converted.Failure, converted.Commands)
	}
}

func TestFromEngineFlowResultSafelyFormatsPanickingErrorMethod(t *testing.T) {
	t.Parallel()

	flow := buildEngineAdapterFlow(
		t,
		time.Unix(0, 0),
		"/workspace/panicking-message.yaml",
		engine.Failed,
		panickingMessageError{},
		[]engineAdapterCommandFixture{{
			command: model.Command{Kind: model.CommandAssertVisible},
			outcome: engine.Failed,
			err:     panickingMessageError{},
		}},
	)
	converted, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error: %v", err)
	}
	if converted.Failure == nil || converted.Failure.Message == "" ||
		len(converted.Commands) != 1 || converted.Commands[0].Failure == nil || converted.Commands[0].Failure.Message == "" {
		t.Fatalf("panic-safe failures = flow %#v commands %#v", converted.Failure, converted.Commands)
	}
}

type panickingMessageError struct{}

func (panickingMessageError) Error() string { panic("Error method panic") }

func TestFromEngineFlowResultMapsEveryStatus(t *testing.T) {
	tests := []struct {
		outcome engine.Outcome
		want    Status
	}{
		{outcome: engine.Completed, want: Completed},
		{outcome: engine.Skipped, want: Skipped},
		{outcome: engine.Warned, want: Warned},
		{outcome: engine.Failed, want: Failed},
		{outcome: engine.Cancelled, want: Cancelled},
	}

	for _, test := range tests {
		t.Run(string(test.outcome), func(t *testing.T) {
			flow := buildEngineAdapterFlow(t, time.Unix(100, 0), "/workspace/status.yaml", test.outcome, nil, []engineAdapterCommandFixture{{
				command:  model.Command{Kind: model.CommandBack},
				outcome:  test.outcome,
				duration: time.Millisecond,
			}})
			got, err := FromEngineFlowResult(flow, model.Config{Name: "status flow"})
			if err != nil {
				t.Fatalf("FromEngineFlowResult() error = %v", err)
			}
			if got.Status != test.want || len(got.Commands) != 1 || got.Commands[0].Status != test.want {
				t.Fatalf("statuses = flow %q command %#v, want %q", got.Status, got.Commands, test.want)
			}
		})
	}
}

func TestFromEngineFlowResultMapsFailureLabelsNestedOrderAndTiming(t *testing.T) {
	zone := time.FixedZone("EDT", -4*60*60)
	started := time.Date(2026, time.July, 15, 14, 30, 0, 0, zone)
	blank := " \t "
	trimmed := "  return home  "
	commandFailure := errors.New("button not found")
	flowFailure := errors.New("checkout flow failed")
	flow := buildEngineAdapterFlow(t, started, "/workspace/flows/checkout.yaml", engine.Failed, flowFailure, []engineAdapterCommandFixture{
		{
			command:  model.Command{Kind: model.CommandTapOn, Label: &blank},
			depth:    0,
			outcome:  engine.Failed,
			err:      commandFailure,
			duration: 125 * time.Millisecond,
		},
		{
			command:  model.Command{Kind: model.CommandBack, Label: &trimmed},
			depth:    2,
			outcome:  engine.Completed,
			duration: 250 * time.Millisecond,
		},
	})

	got, err := FromEngineFlowResult(flow, model.Config{Name: "Configured checkout"})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error = %v", err)
	}
	if got.Name != "Configured checkout" || got.Description != "/workspace/flows/checkout.yaml" {
		t.Fatalf("flow identity = %q / %q", got.Name, got.Description)
	}
	if got.Failure == nil || got.Failure.Message != flowFailure.Error() || got.Failure.Details != "" {
		t.Fatalf("flow failure = %#v", got.Failure)
	}
	if !got.StartedAt.Equal(started.UTC()) || !got.EndedAt.Equal(started.Add(375*time.Millisecond).UTC()) ||
		got.StartedAt.Location() != time.UTC || got.EndedAt.Location() != time.UTC || got.DurationMillis != 375 {
		t.Fatalf("flow timing = %v / %v / %d", got.StartedAt, got.EndedAt, got.DurationMillis)
	}
	if len(got.Commands) != 2 || got.Commands[0].Sequence != 1 || got.Commands[1].Sequence != 2 ||
		got.Commands[0].Depth != 0 || got.Commands[1].Depth != 2 {
		t.Fatalf("nested command order = %#v", got.Commands)
	}
	first := got.Commands[0]
	if first.Keyword != "tapOn" || first.Description != "tapOn" || first.DurationMillis != 125 {
		t.Fatalf("blank-label command = %#v", first)
	}
	if !first.StartedAt.Equal(started.UTC()) || !first.EndedAt.Equal(started.Add(125*time.Millisecond).UTC()) ||
		first.StartedAt.Location() != time.UTC || first.EndedAt.Location() != time.UTC {
		t.Fatalf("first command timing = %v / %v", first.StartedAt, first.EndedAt)
	}
	if first.Failure == nil || first.Failure.Message != commandFailure.Error() || first.Failure.Details != "" {
		t.Fatalf("command failure = %#v", first.Failure)
	}
	second := got.Commands[1]
	if second.Keyword != "back" || second.Description != "return home" || second.Failure != nil || second.DurationMillis != 250 {
		t.Fatalf("trimmed-label command = %#v", second)
	}
}

func TestFromEngineFlowResultMapsArtifactsMetadataAndDefensiveCopies(t *testing.T) {
	evaluated := model.Command{Kind: model.CommandTapOn, Arguments: "evaluated"}
	metadata := engine.NewCommandMetadata(
		2,
		&evaluated,
		[]string{"first", "line\n\"quoted\""},
		"retry insight",
		"source-provided reasoning",
	)
	flow := buildEngineAdapterFlow(t, time.Unix(200, 0), "nested.yaml", engine.Completed, nil, []engineAdapterCommandFixture{
		{
			command:  model.Command{Kind: model.CommandTapOn},
			depth:    0,
			outcome:  engine.Completed,
			duration: time.Millisecond,
			metadata: metadata,
			artifacts: []device.Artifact{
				{Kind: "screenshot", Path: "artifacts/failure.png", Metadata: map[string]string{"owner": "first"}},
				{Kind: "hierarchy", Path: "artifacts/hierarchy.xml", Metadata: map[string]string{"format": "xml"}},
			},
		},
		{
			command:  model.Command{Kind: model.CommandInputText},
			depth:    2,
			outcome:  engine.Completed,
			duration: time.Millisecond,
			artifacts: []device.Artifact{
				{Kind: "screenshot", Path: "artifacts/failure.png", Metadata: map[string]string{"owner": "duplicate"}},
				{Kind: "log", Path: "artifacts/device.log", Metadata: map[string]string{"stream": "device"}},
			},
		},
	})

	got, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error = %v", err)
	}
	wantMetadata := map[string]string{
		"numberOfRuns":     "2",
		"logMessages":      `["first","line\n\"quoted\""]`,
		"evaluatedCommand": `{"kind":"tapOn","form":"","arguments":"evaluated"}`,
		"insight":          "retry insight",
		"aiReasoning":      "source-provided reasoning",
	}
	if !reflect.DeepEqual(got.Commands[0].Metadata, wantMetadata) {
		t.Fatalf("metadata = %#v, want %#v", got.Commands[0].Metadata, wantMetadata)
	}
	if len(got.Commands[1].Metadata) != 0 {
		t.Fatalf("empty metadata = %#v", got.Commands[1].Metadata)
	}
	wantArtifacts := []Artifact{
		{Kind: "screenshot", Path: "artifacts/failure.png"},
		{Kind: "hierarchy", Path: "artifacts/hierarchy.xml"},
		{Kind: "log", Path: "artifacts/device.log"},
	}
	if !reflect.DeepEqual(got.Commands[0].Artifacts, wantArtifacts[:2]) {
		t.Fatalf("first command artifacts = %#v", got.Commands[0].Artifacts)
	}
	if !reflect.DeepEqual(got.Artifacts, wantArtifacts) {
		t.Fatalf("flow artifacts = %#v, want %#v", got.Artifacts, wantArtifacts)
	}

	got.Commands[0].Metadata["numberOfRuns"] = "mutated"
	got.Commands[0].Artifacts[0].Path = "mutated-command.png"
	got.Commands[0].Description = "mutated description"
	got.Artifacts[0].Path = "mutated-flow.png"
	again, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("second FromEngineFlowResult() error = %v", err)
	}
	if !reflect.DeepEqual(again.Commands[0].Metadata, wantMetadata) ||
		again.Commands[0].Artifacts[0].Path != "artifacts/failure.png" ||
		again.Commands[0].Description != "tapOn" ||
		again.Artifacts[0].Path != "artifacts/failure.png" {
		t.Fatalf("second conversion aliases first result: %#v", again)
	}
	sourceArtifact := flow.Commands()[0].Artifacts()[0]
	if sourceArtifact.Path != "artifacts/failure.png" || sourceArtifact.Metadata["owner"] != "first" {
		t.Fatalf("conversion mutated engine artifact = %#v", sourceArtifact)
	}
}

func TestFromEngineFlowResultPrefersEvaluatedCommandIdentityAndDescription(t *testing.T) {
	evaluatedLabel := "  evaluated description  "
	evaluated := model.Command{
		Kind: model.CommandAssertVisible, Form: model.CommandFormObject,
		Arguments: "Ready", Label: &evaluatedLabel,
	}
	flow := buildEngineAdapterFlow(t, time.Unix(250, 0), "evaluated.yaml", engine.Completed, nil, []engineAdapterCommandFixture{{
		command:  model.Command{Kind: model.CommandTapOn, Form: model.CommandFormObject, Arguments: "${TARGET}"},
		outcome:  engine.Completed,
		duration: time.Millisecond,
		metadata: engine.NewCommandMetadata(1, &evaluated, []string{"late log"}, "", ""),
	}})

	got, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error: %v", err)
	}
	if len(got.Commands) != 1 || got.Commands[0].Keyword != "assertVisible" || got.Commands[0].Description != "evaluated description" {
		t.Fatalf("evaluated report identity = %#v", got.Commands)
	}
	encoded, err := json.Marshal(evaluated)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	if got.Commands[0].Metadata["evaluatedCommand"] != string(encoded) || got.Commands[0].Metadata["logMessages"] != `["late log"]` {
		t.Fatalf("evaluated report metadata = %#v", got.Commands[0].Metadata)
	}
}

func TestEngineSequenceToReportRejectsOverflow(t *testing.T) {
	_, err := engineSequenceToReport(uint64(math.MaxInt64) + 1)
	assertEngineAdapterConfigurationError(t, err, "command sequence", "overflows")
}

func TestEngineOutcomeToReportRejectsUnknownStatus(t *testing.T) {
	_, err := engineOutcomeToReport(engine.Outcome("unknown"))
	assertEngineAdapterConfigurationError(t, err, "engine outcome", "unknown")
}

func TestFromEngineFlowResultWrapsMarshalFailure(t *testing.T) {
	invalidTime := time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	flow := buildEngineAdapterFlow(t, invalidTime, "future.yaml", engine.Completed, nil, nil)

	_, err := FromEngineFlowResult(flow, model.Config{})
	assertEngineAdapterConfigurationError(t, err, "report flow result", "marshal")
}

type engineAdapterCommandFixture struct {
	command   model.Command
	depth     int
	outcome   engine.Outcome
	err       error
	duration  time.Duration
	metadata  engine.CommandMetadata
	artifacts []device.Artifact
}

func buildEngineAdapterFlow(
	t *testing.T,
	started time.Time,
	path string,
	outcome engine.Outcome,
	productError error,
	fixtures []engineAdapterCommandFixture,
) engine.FlowResult {
	t.Helper()
	clock := enginetest.NewFakeClock(started)
	timeline, err := engine.NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline() error = %v", err)
	}
	flowSpan, _, err := timeline.BeginFlow(path, "", 0)
	if err != nil {
		t.Fatalf("BeginFlow() error = %v", err)
	}
	commands := make([]engine.CommandResult, 0, len(fixtures))
	for _, fixture := range fixtures {
		span, _, beginErr := timeline.BeginCommand(fixture.command, fixture.depth)
		if beginErr != nil {
			t.Fatalf("BeginCommand() error = %v", beginErr)
		}
		clock.Advance(fixture.duration)
		command, _, finishErr := span.FinishWithArtifacts(
			fixture.outcome,
			fixture.err,
			fixture.metadata,
			fixture.artifacts,
		)
		if finishErr != nil {
			t.Fatalf("Finish command() error = %v", finishErr)
		}
		commands = append(commands, command)
	}
	flow, _, err := flowSpan.Finish(outcome, productError, commands)
	if err != nil {
		t.Fatalf("Finish flow() error = %v", err)
	}
	return flow
}

func assertEngineAdapterConfigurationError(t *testing.T, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want *engine.ConfigurationError")
	}
	var configurationError *engine.ConfigurationError
	if !errors.As(err, &configurationError) {
		t.Fatalf("error = %T %v, want *engine.ConfigurationError", err, err)
	}
	for _, fragment := range fragments {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want fragment %q", err, fragment)
		}
	}
}

func TestTheFlowsRecordedNameOutranksItsFileName(t *testing.T) {
	t.Parallel()

	// The engine result carries the authored flow name for report callers that
	// do not hold the parsed configuration.
	clock := enginetest.NewFakeClock(time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC))
	timeline, err := engine.NewTimeline(clock)
	if err != nil {
		t.Fatalf("NewTimeline() error = %v", err)
	}
	span, _, err := timeline.BeginFlow("/workspace/checkout.yaml", "authored name", 0)
	if err != nil {
		t.Fatalf("BeginFlow() error = %v", err)
	}
	flow, _, err := span.Finish(engine.Completed, nil, nil)
	if err != nil {
		t.Fatalf("Finish() error = %v", err)
	}

	got, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error = %v", err)
	}
	if got.Name != "authored name" {
		t.Fatalf("name = %q, want the name the engine recorded", got.Name)
	}

	// The control on the other side: an explicit config still outranks it, so a
	// caller that knows more than the run did is not overridden.
	got, err = FromEngineFlowResult(flow, model.Config{Name: "caller wins"})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error = %v", err)
	}
	if got.Name != "caller wins" {
		t.Fatalf("name = %q, want the caller's config to outrank the run", got.Name)
	}
}
