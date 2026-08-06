package engine

import (
	"sort"
	"sync"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/model"
)

// commandResultLedger retains every terminal command result for one root.
// Completion order may differ from sequence order when composite commands
// finish after their descendants.
type commandResultLedger struct {
	mu                  sync.Mutex
	bySequence          map[uint64]CommandResult
	canonicalBySequence map[uint64]CommandResult
}

// ledgerUntrustedErrorCarrier retains exact raw error identity for rows that
// did not cross the core sealing boundary. It is storage-only and never grants
// the authentication capability carried by resultErrorCarrier.
type ledgerUntrustedErrorCarrier struct {
	raw error
}

func (*ledgerUntrustedErrorCarrier) Error() string {
	return "untrusted command result error"
}

func (ledger *commandResultLedger) authenticate(candidate CommandResult) (CommandResult, bool) {
	if ledger == nil || candidate.sequence == 0 || candidate.identity == nil {
		return CommandResult{}, false
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	recorded, exists := ledger.bySequence[candidate.sequence]
	canonical, canonicalExists := ledger.canonicalBySequence[candidate.sequence]
	if !exists || !canonicalExists || !sameStoredCommandResult(canonical, recorded) ||
		!sameAuthenticatedCommandResult(canonical, candidate) {
		return CommandResult{}, false
	}
	return cloneCommandResult(canonical), true
}

type resultErrorMatcher func(recorded, candidate error) bool

func sameAuthenticatedCommandResult(recorded, candidate CommandResult) bool {
	return sameCommandResult(recorded, candidate, sameSealedResultError)
}

func sameStoredCommandResult(recorded, candidate CommandResult) bool {
	return sameCommandResult(recorded, candidate, sameStoredResultError)
}

func sameCommandResult(recorded, candidate CommandResult, sameError resultErrorMatcher) bool {
	return recorded.identity == candidate.identity &&
		recorded.rootRunID == candidate.rootRunID && recorded.sequence == candidate.sequence &&
		recorded.depth == candidate.depth && recorded.outcome == candidate.outcome &&
		recorded.startedAt == candidate.startedAt &&
		recorded.finishedAt == candidate.finishedAt && recorded.duration == candidate.duration &&
		sameCommandSnapshot(recorded.command, candidate.command) &&
		sameError(recorded.productError, candidate.productError) &&
		sameRetryClassification(recorded.retryClassification, candidate.retryClassification, sameError) &&
		sameCommandMetadata(recorded.metadata, candidate.metadata) &&
		sameResultArtifacts(recorded.artifacts, candidate.artifacts)
}

func (ledger *commandResultLedger) redeemExactError(candidate *CommandResult) (*exactErrorDisposition, bool) {
	if candidate == nil {
		return nil, false
	}
	authenticated, ok := ledger.authenticate(*candidate)
	if !ok || authenticated.retryClassification == nil ||
		authenticated.retryClassification.publication == nil {
		return nil, false
	}
	disposition := *authenticated.retryClassification.publication
	disposition.published = unsealResultError(disposition.published)
	disposition.classification = unsealResultError(disposition.classification)
	return &disposition, true
}

func sameSealedResultError(recorded, candidate error) bool {
	if recorded == nil || candidate == nil {
		return recorded == nil && candidate == nil
	}
	recordedCarrier, recordedOK := recorded.(*resultErrorCarrier)
	candidateCarrier, candidateOK := candidate.(*resultErrorCarrier)
	return recordedOK && candidateOK && recordedCarrier != nil && recordedCarrier == candidateCarrier
}

func sameStoredResultError(recorded, candidate error) bool {
	if recorded == nil || candidate == nil {
		return recorded == nil && candidate == nil
	}
	if recordedCarrier, ok := recorded.(*resultErrorCarrier); ok {
		candidateCarrier, candidateOK := candidate.(*resultErrorCarrier)
		return candidateOK && recordedCarrier != nil && recordedCarrier == candidateCarrier
	}
	recordedCarrier, recordedOK := recorded.(*ledgerUntrustedErrorCarrier)
	candidateCarrier, candidateOK := candidate.(*ledgerUntrustedErrorCarrier)
	return recordedOK && candidateOK && recordedCarrier != nil && recordedCarrier == candidateCarrier
}

func sameRetryClassification(
	recorded, candidate *retryErrorClassification,
	sameError resultErrorMatcher,
) bool {
	if recorded == nil || candidate == nil {
		return recorded == nil && candidate == nil
	}
	if !sameError(recorded.classification, candidate.classification) {
		return false
	}
	if recorded.publication == nil || candidate.publication == nil {
		return recorded.publication == nil && candidate.publication == nil
	}
	return sameError(recorded.publication.published, candidate.publication.published) &&
		sameError(recorded.publication.classification, candidate.publication.classification)
}

func sameCommandMetadata(recorded, candidate CommandMetadata) bool {
	if recorded.numberOfRuns != candidate.numberOfRuns ||
		recorded.numberOfRunsSet != candidate.numberOfRunsSet ||
		recorded.insight != candidate.insight || recorded.aiReasoning != candidate.aiReasoning ||
		!sameStrings(recorded.logMessages, candidate.logMessages) {
		return false
	}
	if recorded.evaluatedCommand == nil || candidate.evaluatedCommand == nil {
		return recorded.evaluatedCommand == nil && candidate.evaluatedCommand == nil
	}
	return sameCommandSnapshot(*recorded.evaluatedCommand, *candidate.evaluatedCommand)
}

func sameStrings(recorded, candidate []string) bool {
	if (recorded == nil) != (candidate == nil) || len(recorded) != len(candidate) {
		return false
	}
	for index := range recorded {
		if recorded[index] != candidate[index] {
			return false
		}
	}
	return true
}

func sameResultArtifacts(recorded, candidate []device.Artifact) bool {
	if (recorded == nil) != (candidate == nil) || len(recorded) != len(candidate) {
		return false
	}
	for index := range recorded {
		if recorded[index].Kind != candidate[index].Kind || recorded[index].Path != candidate[index].Path ||
			!sameStringMap(recorded[index].Metadata, candidate[index].Metadata) {
			return false
		}
	}
	return true
}

func sameStringMap(recorded, candidate map[string]string) bool {
	if (recorded == nil) != (candidate == nil) || len(recorded) != len(candidate) {
		return false
	}
	for key, value := range recorded {
		if candidateValue, exists := candidate[key]; !exists || candidateValue != value {
			return false
		}
	}
	return true
}

func sameCommandSnapshot(recorded, candidate model.Command) bool {
	return recorded.Equivalent(candidate) && sameCommandOrigin(recorded, candidate)
}

func sameCommandOrigin(recorded, candidate model.Command) bool {
	if recorded.Source != candidate.Source ||
		!sameSelectorOrigin(recorded.Selector, candidate.Selector) ||
		!sameConditionOrigin(recorded.Condition, candidate.Condition) ||
		!sameArgumentOrigin(recorded.Arguments, candidate.Arguments) ||
		len(recorded.Children) != len(candidate.Children) || len(recorded.Links) != len(candidate.Links) {
		return false
	}
	for index := range recorded.Children {
		if !sameCommandOrigin(recorded.Children[index], candidate.Children[index]) {
			return false
		}
	}
	for index := range recorded.Links {
		if recorded.Links[index].Source != candidate.Links[index].Source ||
			recorded.Links[index].ResolvedPath != candidate.Links[index].ResolvedPath {
			return false
		}
	}
	return true
}

func sameArgumentOrigin(recorded, candidate any) bool {
	switch recordedConfig := recorded.(type) {
	case model.Config:
		candidateConfig, ok := candidate.(model.Config)
		return ok && sameConfigOrigin(recordedConfig, candidateConfig)
	case *model.Config:
		candidateConfig, ok := candidate.(*model.Config)
		if !ok || recordedConfig == nil || candidateConfig == nil {
			return ok && recordedConfig == nil && candidateConfig == nil
		}
		return sameConfigOrigin(*recordedConfig, *candidateConfig)
	default:
		return true
	}
}

func sameConfigOrigin(recorded, candidate model.Config) bool {
	if recorded.Source != candidate.Source || !sameSourceMap(recorded.FieldSources, candidate.FieldSources) ||
		len(recorded.OnFlowStart) != len(candidate.OnFlowStart) ||
		len(recorded.OnFlowComplete) != len(candidate.OnFlowComplete) {
		return false
	}
	for index := range recorded.OnFlowStart {
		if !sameCommandOrigin(recorded.OnFlowStart[index], candidate.OnFlowStart[index]) {
			return false
		}
	}
	for index := range recorded.OnFlowComplete {
		if !sameCommandOrigin(recorded.OnFlowComplete[index], candidate.OnFlowComplete[index]) {
			return false
		}
	}
	return true
}

func sameSelectorOrigin(recorded, candidate *model.ElementSelector) bool {
	if recorded == nil || candidate == nil {
		return recorded == nil && candidate == nil
	}
	if recorded.Source != candidate.Source || !sameSourceMap(recorded.FieldSources, candidate.FieldSources) ||
		!sameSelectorOrigin(recorded.Below, candidate.Below) ||
		!sameSelectorOrigin(recorded.Above, candidate.Above) ||
		!sameSelectorOrigin(recorded.LeftOf, candidate.LeftOf) ||
		!sameSelectorOrigin(recorded.RightOf, candidate.RightOf) ||
		!sameSelectorOrigin(recorded.ContainsChild, candidate.ContainsChild) ||
		!sameSelectorOrigin(recorded.ChildOf, candidate.ChildOf) ||
		len(recorded.ContainsDescendants) != len(candidate.ContainsDescendants) {
		return false
	}
	for index := range recorded.ContainsDescendants {
		if !sameSelectorOrigin(&recorded.ContainsDescendants[index], &candidate.ContainsDescendants[index]) {
			return false
		}
	}
	return true
}

func sameConditionOrigin(recorded, candidate *model.Condition) bool {
	if recorded == nil || candidate == nil {
		return recorded == nil && candidate == nil
	}
	return recorded.Source == candidate.Source &&
		sameSourceMap(recorded.FieldSources, candidate.FieldSources) &&
		sameSelectorOrigin(recorded.Visible, candidate.Visible) &&
		sameSelectorOrigin(recorded.NotVisible, candidate.NotVisible)
}

func sameSourceMap(recorded, candidate map[string]model.SourceInfo) bool {
	if (recorded == nil) != (candidate == nil) || len(recorded) != len(candidate) {
		return false
	}
	for key, value := range recorded {
		if candidateValue, exists := candidate[key]; !exists || candidateValue != value {
			return false
		}
	}
	return true
}

func newCommandResultLedger() *commandResultLedger {
	return &commandResultLedger{
		bySequence:          make(map[uint64]CommandResult),
		canonicalBySequence: make(map[uint64]CommandResult),
	}
}

func (ledger *commandResultLedger) record(result CommandResult) error {
	if ledger == nil {
		return NewConfigurationError("command result ledger must not be nil", nil)
	}
	if result.sequence == 0 {
		return NewConfigurationError("command result sequence must not be zero", nil)
	}
	if result.depth < 0 {
		return NewConfigurationError("command result depth must not be negative", nil)
	}

	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.bySequence == nil {
		ledger.bySequence = make(map[uint64]CommandResult)
	}
	if ledger.canonicalBySequence == nil {
		ledger.canonicalBySequence = make(map[uint64]CommandResult)
	}
	if _, exists := ledger.bySequence[result.sequence]; exists {
		return NewConfigurationError("command result sequence is already recorded", nil)
	}
	if _, exists := ledger.canonicalBySequence[result.sequence]; exists {
		return NewConfigurationError("command result sequence is already recorded", nil)
	}
	stored := retainLedgerResultErrors(cloneCommandResult(result))
	ledger.bySequence[result.sequence] = cloneCommandResult(stored)
	ledger.canonicalBySequence[result.sequence] = cloneCommandResult(stored)
	return nil
}

func retainLedgerResultErrors(result CommandResult) CommandResult {
	result.productError = retainLedgerResultError(result.productError)
	if result.retryClassification == nil {
		return result
	}
	classification := *result.retryClassification
	classification.classification = retainLedgerResultError(classification.classification)
	if classification.publication != nil {
		publication := *classification.publication
		publication.published = retainLedgerResultError(publication.published)
		publication.classification = retainLedgerResultError(publication.classification)
		classification.publication = &publication
	}
	result.retryClassification = &classification
	return result
}

func retainLedgerResultError(raw error) error {
	if raw == nil {
		return nil
	}
	switch raw.(type) {
	case *resultErrorCarrier, *ledgerUntrustedErrorCarrier:
		return raw
	default:
		return &ledgerUntrustedErrorCarrier{raw: raw}
	}
}

func releaseLedgerResultErrors(result CommandResult) CommandResult {
	result.productError = releaseLedgerResultError(result.productError)
	if result.retryClassification == nil {
		return result
	}
	classification := *result.retryClassification
	classification.classification = releaseLedgerResultError(classification.classification)
	if classification.publication != nil {
		publication := *classification.publication
		publication.published = releaseLedgerResultError(publication.published)
		publication.classification = releaseLedgerResultError(publication.classification)
		classification.publication = &publication
	}
	result.retryClassification = &classification
	return result
}

func releaseLedgerResultError(stored error) error {
	carrier, ok := stored.(*ledgerUntrustedErrorCarrier)
	if !ok || carrier == nil {
		return stored
	}
	return carrier.raw
}

func (ledger *commandResultLedger) snapshot() []CommandResult {
	return ledger.snapshotAfter(0)
}

func (ledger *commandResultLedger) result(sequence uint64) (CommandResult, bool) {
	if ledger == nil || sequence == 0 {
		return CommandResult{}, false
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	recorded, exists := ledger.bySequence[sequence]
	canonical, canonicalExists := ledger.canonicalBySequence[sequence]
	if !exists || !canonicalExists || !sameStoredCommandResult(canonical, recorded) {
		return CommandResult{}, false
	}
	return releaseLedgerResultErrors(cloneCommandResult(canonical)), true
}

func (ledger *commandResultLedger) snapshotAfter(checkpoint uint64) []CommandResult {
	if ledger == nil {
		return nil
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()

	results := make([]CommandResult, 0, len(ledger.canonicalBySequence))
	for sequence, canonical := range ledger.canonicalBySequence {
		if sequence <= checkpoint {
			continue
		}
		recorded, exists := ledger.bySequence[sequence]
		if !exists || !sameStoredCommandResult(canonical, recorded) {
			continue
		}
		results = append(results, releaseLedgerResultErrors(cloneCommandResult(canonical)))
	}
	sort.Slice(results, func(left, right int) bool {
		return results[left].sequence < results[right].sequence
	})
	return results
}
