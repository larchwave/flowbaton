package engine

import (
	"sync"
	"time"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/model"
)

// Outcome is the stable terminal status for a command or flow.
type Outcome string

const (
	Completed Outcome = "Completed"
	Skipped   Outcome = "Skipped"
	Warned    Outcome = "Warned"
	Failed    Outcome = "Failed"
	Cancelled Outcome = "Cancelled"
)

// EventKind identifies an immutable engine lifecycle event.
type EventKind string

const (
	EventFlowStarted            EventKind = "FlowStarted"
	EventFlowFinished           EventKind = "FlowFinished"
	EventCommandStarted         EventKind = "CommandStarted"
	EventCommandFinished        EventKind = "CommandFinished"
	EventCommandReset           EventKind = "CommandReset"
	EventCommandMetadataUpdated EventKind = "CommandMetadataUpdated"
)

// CommandMetadata is an immutable snapshot of execution metadata.
type CommandMetadata struct {
	numberOfRuns    int
	numberOfRunsSet bool

	evaluatedCommand *model.Command
	logMessages      []string
	insight          string
	aiReasoning      string
}

func NewCommandMetadata(numberOfRuns int, evaluatedCommand *model.Command, logMessages []string, insight string, aiReasoning string) CommandMetadata {
	metadata := CommandMetadata{
		numberOfRuns:    numberOfRuns,
		numberOfRunsSet: true,
		logMessages:     cloneResultStrings(logMessages),
		insight:         insight,
		aiReasoning:     aiReasoning,
	}
	if evaluatedCommand != nil {
		cloned := cloneCommand(*evaluatedCommand)
		metadata.evaluatedCommand = &cloned
	}
	return metadata
}

func (m CommandMetadata) NumberOfRuns() int { return m.numberOfRuns }

// HasNumberOfRuns reports whether NumberOfRuns was explicitly populated.
// The zero-value CommandMetadata is absent, while NewCommandMetadata makes
// every supplied value present, including zero.
func (m CommandMetadata) HasNumberOfRuns() bool { return m.numberOfRunsSet }

func (m CommandMetadata) EvaluatedCommand() (model.Command, bool) {
	if m.evaluatedCommand == nil {
		return model.Command{}, false
	}
	return cloneCommand(*m.evaluatedCommand), true
}

func (m CommandMetadata) LogMessages() []string {
	return cloneResultStrings(m.logMessages)
}

func (m CommandMetadata) Insight() string { return m.insight }

func (m CommandMetadata) AIReasoning() string { return m.aiReasoning }

// CommandResult is an immutable terminal command record.
type CommandResult struct {
	identity            *commandResultIdentity
	rootRunID           string
	sequence            uint64
	depth               int
	command             model.Command
	outcome             Outcome
	productError        error
	retryClassification *retryErrorClassification
	startedAt           time.Time
	finishedAt          time.Time
	duration            time.Duration
	metadata            CommandMetadata
	artifacts           []device.Artifact
}

type resultErrorCarrier struct {
	raw error
}

func (*resultErrorCarrier) Error() string {
	return "sealed command result error"
}

// commandResultIdentity authenticates core-produced results across owned
// clones. It is deliberately opaque so a value assembled from public result
// accessors cannot impersonate a ledger record.
type commandResultIdentity struct {
	marker byte
}

func (r CommandResult) RootRunID() string         { return r.rootRunID }
func (r CommandResult) Sequence() uint64          { return r.sequence }
func (r CommandResult) Depth() int                { return r.depth }
func (r CommandResult) Command() model.Command    { return cloneCommand(r.command) }
func (r CommandResult) Outcome() Outcome          { return r.outcome }
func (r CommandResult) ProductError() error       { return unsealResultError(r.productError) }
func (r CommandResult) StartedAt() time.Time      { return r.startedAt }
func (r CommandResult) FinishedAt() time.Time     { return r.finishedAt }
func (r CommandResult) Duration() time.Duration   { return r.duration }
func (r CommandResult) Metadata() CommandMetadata { return cloneMetadata(r.metadata) }
func (r CommandResult) Artifacts() []device.Artifact {
	return cloneResultArtifacts(r.artifacts)
}

// FlowResult is an immutable terminal flow record.
type FlowResult struct {
	rootRunID           string
	path                string
	name                string
	depth               int
	outcome             Outcome
	productError        error
	retryClassification *retryErrorClassification
	exactErrorSource    *CommandResult
	startedAt           time.Time
	finishedAt          time.Time
	duration            time.Duration
	commands            []CommandResult
}

func (r FlowResult) RootRunID() string { return r.rootRunID }
func (r FlowResult) Path() string      { return r.path }

// Name is the flow's authored `name:`, blank when it has none.
func (r FlowResult) Name() string { return r.name }

func (r FlowResult) Depth() int              { return r.depth }
func (r FlowResult) Outcome() Outcome        { return r.outcome }
func (r FlowResult) ProductError() error     { return r.productError }
func (r FlowResult) StartedAt() time.Time    { return r.startedAt }
func (r FlowResult) FinishedAt() time.Time   { return r.finishedAt }
func (r FlowResult) Duration() time.Duration { return r.duration }

func (r FlowResult) Commands() []CommandResult {
	return cloneCommandResults(r.commands)
}

// Event is an immutable listener-facing lifecycle snapshot.
type Event struct {
	rootRunID    string
	kind         EventKind
	at           time.Time
	sequence     uint64
	depth        int
	flowPath     string
	command      *model.Command
	outcome      Outcome
	productError error
	metadata     CommandMetadata
	artifacts    []device.Artifact
}

func (e Event) RootRunID() string         { return e.rootRunID }
func (e Event) Kind() EventKind           { return e.kind }
func (e Event) At() time.Time             { return e.at }
func (e Event) Sequence() uint64          { return e.sequence }
func (e Event) Depth() int                { return e.depth }
func (e Event) FlowPath() string          { return e.flowPath }
func (e Event) Outcome() Outcome          { return e.outcome }
func (e Event) ProductError() error       { return e.productError }
func (e Event) Metadata() CommandMetadata { return cloneMetadata(e.metadata) }
func (e Event) Artifacts() []device.Artifact {
	return cloneResultArtifacts(e.artifacts)
}

func (e Event) Command() (model.Command, bool) {
	if e.command == nil {
		return model.Command{}, false
	}
	return cloneCommand(*e.command), true
}

// Timeline is the shared monotonic command-sequence and time source for one
// execution run.
type Timeline struct {
	mu        sync.Mutex
	clock     Clock
	rootRunID string
	next      uint64
}

func NewTimeline(clock Clock) (*Timeline, error) {
	return newTimeline(clock, "")
}

func newTimeline(clock Clock, rootRunID string) (*Timeline, error) {
	if clock == nil {
		return nil, NewConfigurationError("engine Timeline requires a clock", nil)
	}
	return &Timeline{clock: clock, rootRunID: rootRunID}, nil
}

// Checkpoint returns the highest command sequence allocated so far, including
// commands whose spans have not finished yet.
func (t *Timeline) Checkpoint() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.next
}

// BeginCommand allocates the next sequence number and captures an immutable
// start event.
func (t *Timeline) BeginCommand(command model.Command, depth int) (*CommandSpan, Event, error) {
	if depth < 0 {
		return nil, Event{}, NewConfigurationError("command depth must not be negative", nil)
	}
	t.mu.Lock()
	t.next++
	sequence := t.next
	startedAt := t.clock.Now()
	t.mu.Unlock()
	cloned := cloneCommand(command)
	span := &CommandSpan{
		clock: t.clock, rootRunID: t.rootRunID, sequence: sequence,
		depth: depth, command: cloned, startedAt: startedAt,
	}
	eventCommand := cloneCommand(cloned)
	return span, Event{
		rootRunID: t.rootRunID, kind: EventCommandStarted, at: startedAt, sequence: sequence,
		depth: depth, command: &eventCommand,
	}, nil
}

// BeginFlow captures a flow start without allocating a command sequence.
//
// name is the flow's authored `name:`, blank when it has none. It is carried
// because this is the only point where the compiled config and the result being
// built are both in scope: a consumer holding a FlowResult has just a path, and
// a path cannot be turned back into an authored name.
func (t *Timeline) BeginFlow(path string, name string, depth int) (*FlowSpan, Event, error) {
	if depth < 0 {
		return nil, Event{}, NewConfigurationError("flow depth must not be negative", nil)
	}
	startedAt := t.clock.Now()
	return &FlowSpan{
			clock: t.clock, rootRunID: t.rootRunID, path: path, name: name,
			depth: depth, startedAt: startedAt,
		}, Event{
			rootRunID: t.rootRunID, kind: EventFlowStarted, at: startedAt, depth: depth, flowPath: path,
		}, nil
}

// CommandSpan is a single-use command timing scope.
type CommandSpan struct {
	mu        sync.Mutex
	clock     Clock
	rootRunID string
	sequence  uint64
	depth     int
	command   model.Command
	startedAt time.Time
	finished  bool
}

// MetadataUpdated captures an immutable metadata event for this active parent
// without completing the span or allocating another command sequence.
func (s *CommandSpan) MetadataUpdated(metadata CommandMetadata) (Event, error) {
	if s == nil {
		return Event{}, NewConfigurationError("command span must not be nil", nil)
	}
	if err := validateCommandMetadata(metadata); err != nil {
		return Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return Event{}, NewConfigurationError("command span already finished", nil)
	}
	command := cloneCommand(s.command)
	return Event{
		rootRunID: s.rootRunID,
		kind:      EventCommandMetadataUpdated,
		at:        s.clock.Now(),
		sequence:  s.sequence,
		depth:     s.depth,
		command:   &command,
		metadata:  cloneMetadata(metadata),
	}, nil
}

// CommandReset captures an immutable reset event for a previously executed
// immediate child. The child's identity is reused and no sequence is allocated.
func (s *CommandSpan) CommandReset(previous CommandResult) (Event, error) {
	if s == nil {
		return Event{}, NewConfigurationError("command span must not be nil", nil)
	}
	if previous.identity == nil || previous.sequence == 0 {
		return Event{}, NewConfigurationError("command reset requires an executed child result", nil)
	}
	if previous.rootRunID != s.rootRunID {
		return Event{}, NewConfigurationError("command reset child must share the parent root run", nil)
	}
	if previous.depth != s.depth+1 {
		return Event{}, NewConfigurationError("command reset requires an immediate child result", nil)
	}
	if previous.sequence <= s.sequence {
		return Event{}, NewConfigurationError("command reset child must follow its active parent", nil)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return Event{}, NewConfigurationError("command span already finished", nil)
	}
	command := cloneCommand(previous.command)
	return Event{
		rootRunID: previous.rootRunID,
		kind:      EventCommandReset,
		at:        s.clock.Now(),
		sequence:  previous.sequence,
		depth:     previous.depth,
		command:   &command,
	}, nil
}

func (s *CommandSpan) Finish(outcome Outcome, productError error, metadata CommandMetadata) (CommandResult, Event, error) {
	return s.FinishWithArtifacts(outcome, productError, metadata, nil)
}

// FinishWithArtifacts completes the command with finalized host-owned
// artifacts while preserving immutable result and listener snapshots.
func (s *CommandSpan) FinishWithArtifacts(
	outcome Outcome,
	productError error,
	metadata CommandMetadata,
	artifacts []device.Artifact,
) (CommandResult, Event, error) {
	if !validOutcome(outcome) {
		return CommandResult{}, Event{}, NewConfigurationError("invalid command outcome", nil)
	}
	productError = sanitizeMalformedError("command result finalization failed", productError)
	if err := validateCommandMetadata(metadata); err != nil {
		if productError != nil {
			return CommandResult{}, Event{}, productError
		}
		return CommandResult{}, Event{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return CommandResult{}, Event{}, NewConfigurationError("command span already finished", nil)
	}
	s.finished = true
	finishedAt := s.clock.Now()
	result := CommandResult{
		identity:  &commandResultIdentity{marker: 1},
		rootRunID: s.rootRunID, sequence: s.sequence, depth: s.depth, command: cloneCommand(s.command),
		outcome: outcome, productError: productError, startedAt: s.startedAt,
		finishedAt: finishedAt, duration: finishedAt.Sub(s.startedAt), metadata: cloneMetadata(metadata),
		artifacts: cloneResultArtifacts(artifacts),
	}
	eventCommand := cloneCommand(s.command)
	event := Event{
		rootRunID: s.rootRunID, kind: EventCommandFinished, at: finishedAt, sequence: s.sequence,
		depth: s.depth, command: &eventCommand, outcome: outcome,
		productError: productError, metadata: cloneMetadata(metadata),
		artifacts: cloneResultArtifacts(artifacts),
	}
	return result, event, nil
}

func validateCommandMetadata(metadata CommandMetadata) error {
	if metadata.numberOfRunsSet && metadata.numberOfRuns < 0 {
		return NewConfigurationError("command metadata numberOfRuns must not be negative", nil)
	}
	return nil
}

// FlowSpan is a single-use flow timing scope.
type FlowSpan struct {
	mu        sync.Mutex
	clock     Clock
	rootRunID string
	path      string
	name      string
	depth     int
	startedAt time.Time
	finished  bool
}

func (s *FlowSpan) Finish(outcome Outcome, productError error, commands []CommandResult) (FlowResult, Event, error) {
	if !validOutcome(outcome) {
		return FlowResult{}, Event{}, NewConfigurationError("invalid flow outcome", nil)
	}
	if err := validateCommandOrder(commands); err != nil {
		return FlowResult{}, Event{}, err
	}
	productError = sanitizeMalformedError("flow result finalization failed", productError)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return FlowResult{}, Event{}, NewConfigurationError("flow span already finished", nil)
	}
	s.finished = true
	finishedAt := s.clock.Now()
	result := FlowResult{
		rootRunID: s.rootRunID, path: s.path, name: s.name, depth: s.depth,
		outcome: outcome, productError: productError,
		retryClassification: flowRetryClassification(s.depth, productError, commands),
		exactErrorSource:    flowExactErrorSource(s.depth, productError, commands),
		startedAt:           s.startedAt, finishedAt: finishedAt,
		duration: finishedAt.Sub(s.startedAt), commands: cloneCommandResults(commands),
	}
	return result, Event{
		rootRunID: s.rootRunID, kind: EventFlowFinished, at: finishedAt, depth: s.depth, flowPath: s.path,
		outcome: outcome, productError: productError,
	}, nil
}

func flowExactErrorSource(depth int, productError error, commands []CommandResult) *CommandResult {
	if productError == nil {
		return nil
	}
	for _, command := range commands {
		if command.depth != depth || command.ProductError() == nil {
			continue
		}
		if command.retryClassification == nil || command.retryClassification.publication == nil {
			return nil
		}
		source := cloneCommandResult(command)
		return &source
	}
	return nil
}

func flowRetryClassification(
	depth int,
	productError error,
	commands []CommandResult,
) *retryErrorClassification {
	if productError == nil {
		return nil
	}
	for _, command := range commands {
		if command.depth != depth || command.ProductError() == nil {
			continue
		}
		return command.retryClassification
	}
	return nil
}

// ClassifyOutcome applies the engine's skipped, cancelled, and optional-warning
// taxonomy to a product error.
func ClassifyOutcome(err error, optional bool) Outcome {
	switch classifyTerminalError(err) {
	case terminalErrorNone:
		return Completed
	case terminalErrorSkipped:
		return Skipped
	case terminalErrorCancelled:
		return Cancelled
	case terminalErrorRetryable:
		if optional {
			return Warned
		}
	}
	return Failed
}

func validOutcome(outcome Outcome) bool {
	switch outcome {
	case Completed, Skipped, Warned, Failed, Cancelled:
		return true
	default:
		return false
	}
}

func validateCommandOrder(commands []CommandResult) error {
	var previous uint64
	for index, command := range commands {
		if command.sequence == 0 || (index > 0 && command.sequence <= previous) {
			return NewConfigurationError("flow command sequences must be strictly increasing", nil)
		}
		if command.depth < 0 {
			return NewConfigurationError("flow command depth must not be negative", nil)
		}
		previous = command.sequence
	}
	return nil
}

func cloneMetadata(metadata CommandMetadata) CommandMetadata {
	cloned := CommandMetadata{
		numberOfRuns:    metadata.numberOfRuns,
		numberOfRunsSet: metadata.numberOfRunsSet,
		logMessages:     cloneResultStrings(metadata.logMessages),
		insight:         metadata.insight,
		aiReasoning:     metadata.aiReasoning,
	}
	if metadata.evaluatedCommand != nil {
		evaluated := cloneCommand(*metadata.evaluatedCommand)
		cloned.evaluatedCommand = &evaluated
	}
	return cloned
}

func sealCommandResultErrors(result CommandResult) CommandResult {
	result.productError = sealResultError(result.productError)
	if result.retryClassification == nil {
		return result
	}
	classification := *result.retryClassification
	classification.classification = sealResultError(classification.classification)
	if classification.publication != nil {
		publication := *classification.publication
		publication.published = sealResultError(publication.published)
		publication.classification = sealResultError(publication.classification)
		classification.publication = &publication
	}
	result.retryClassification = &classification
	return result
}

func sealResultError(raw error) error {
	if raw == nil {
		return nil
	}
	return &resultErrorCarrier{raw: raw}
}

func unsealResultError(stored error) error {
	carrier, ok := stored.(*resultErrorCarrier)
	if !ok || carrier == nil {
		return stored
	}
	return carrier.raw
}

func cloneResultStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append(make([]string, 0, len(values)), values...)
}

func cloneCommandResult(result CommandResult) CommandResult {
	result.command = cloneCommand(result.command)
	result.metadata = cloneMetadata(result.metadata)
	result.artifacts = cloneResultArtifacts(result.artifacts)
	if result.retryClassification != nil {
		classification := *result.retryClassification
		if classification.publication != nil {
			publication := *classification.publication
			classification.publication = &publication
		}
		result.retryClassification = &classification
	}
	return result
}

func cloneCommandResults(results []CommandResult) []CommandResult {
	if results == nil {
		return nil
	}
	cloned := make([]CommandResult, len(results))
	for index := range results {
		cloned[index] = cloneCommandResult(results[index])
	}
	return cloned
}

func cloneResultArtifacts(artifacts []device.Artifact) []device.Artifact {
	if artifacts == nil {
		return nil
	}
	cloned := make([]device.Artifact, len(artifacts))
	for index, artifact := range artifacts {
		cloned[index] = artifact
		cloned[index].Metadata = cloneStringMap(artifact.Metadata)
	}
	return cloned
}
