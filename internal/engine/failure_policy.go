package engine

import "context"

type rootSequencePolicyResult struct {
	failure error
	halted  bool
}

type rootFailureAccumulator struct {
	productError error
}

func (accumulator *rootFailureAccumulator) add(productError error) {
	if productError == nil {
		return
	}
	if accumulator.productError == nil || rootTerminalPrecedence(productError) > rootTerminalPrecedence(accumulator.productError) {
		accumulator.productError = productError
	}
}

func rootTerminalPrecedence(productError error) uint8 {
	if isSessionIntegrityError(productError) {
		return terminalClassPrecedence(terminalErrorConfiguration)
	}
	class := classifyTerminalError(productError)
	switch class {
	case terminalErrorCancelled, terminalErrorDeviceConnection:
		return terminalClassPrecedence(class)
	default:
		return 0
	}
}

// executeRootSequenceWithPolicy is reserved for the selected root lifecycle.
// Raw child sequences and nested flow lifecycles use executeCompiledSequence.
func (core *executorCore) executeRootSequenceWithPolicy(
	ctx context.Context,
	compiled []compiledDispatch,
	depth int,
) rootSequencePolicyResult {
	if err := validateExecutionInput(ctx, depth); err != nil {
		return rootSequencePolicyResult{failure: err, halted: true}
	}
	result := rootSequencePolicyResult{}
	failures := rootFailureAccumulator{}
	for _, item := range compiled {
		candidate, executeErr := core.executeCompiled(ctx, item, depth)
		command, authenticated := core.ledger.authenticate(candidate)
		if !authenticated {
			rejection := executeErr
			if rejection == nil {
				rejection = NewConfigurationError("command execution returned an unauthenticated result", nil)
			}
			failures.add(rejection)
			result.failure = failures.productError
			result.halted = true
			return result
		}
		if executeErr == nil {
			continue
		}
		failures.add(executeErr)
		result.failure = failures.productError
		if failureResolverBypassed(executeErr) {
			result.halted = true
			return result
		}
		if !failureResolverContinues(ctx, core.state.dependencies.FailureResolver, command) {
			result.halted = true
			return result
		}
	}
	return result
}

func failureResolverBypassed(productError error) bool {
	if isSessionIntegrityError(productError) {
		return true
	}
	switch classifyTerminalError(productError) {
	case terminalErrorCancelled, terminalErrorDeviceConnection:
		return true
	default:
		return false
	}
}

func failureResolverContinues(
	ctx context.Context,
	resolver FailureResolver,
	result CommandResult,
) (continues bool) {
	if isNilValue(resolver) {
		return false
	}
	defer func() {
		if recover() != nil {
			continues = false
		}
	}()
	return resolver.ResolveFailure(ctx, result) == FailureDecisionContinue
}
