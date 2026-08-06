package engine

import (
	"context"
)

type flowExecutionOwnership uint8

const (
	nestedFlowExecution flowExecutionOwnership = iota
	selectedRootExecution
)

// executeCompiledRoot creates and consumes one isolated session for a single
// selected root. The supplied flow must already belong to a fully compiled
// Program; execution performs no parsing, loading, resolution, or compilation.
func executeCompiledRoot(ctx context.Context, dependencies Dependencies, root *compiledFlow) (FlowResult, error) {
	return executeCompiledRootForRun(ctx, dependencies, root, "")
}

func executeCompiledRootForRun(
	ctx context.Context,
	dependencies Dependencies,
	root *compiledFlow,
	rootRunID string,
) (FlowResult, error) {
	session, err := newExecutionSessionForRootRun(ctx, dependencies, root, rootRunID)
	if err != nil {
		return FlowResult{}, err
	}
	result, runErr := session.run(ctx)
	closeErr := session.Close()
	if runErr != nil {
		return result, runErr
	}
	return result, closeErr
}

func (session *executionSession) run(ctx context.Context) (FlowResult, error) {
	if ctx == nil {
		return FlowResult{}, NewConfigurationError("root execution context must not be nil", nil)
	}
	if session == nil {
		return FlowResult{}, NewConfigurationError("execution session must not be nil", nil)
	}
	session.mu.Lock()
	if session.closed {
		session.mu.Unlock()
		return FlowResult{}, NewConfigurationError("execution session is closed", nil)
	}
	if session.running || session.ran {
		session.mu.Unlock()
		return FlowResult{}, NewConfigurationError("execution session root may run only once", nil)
	}
	session.running = true
	root := session.root
	session.mu.Unlock()
	defer func() {
		session.mu.Lock()
		session.running = false
		session.ran = true
		session.mu.Unlock()
	}()
	if root == nil {
		_ = session.Close()
		return FlowResult{}, NewConfigurationError("compiled root flow must not be nil", nil)
	}
	return session.executeFlowLifecycle(ctx, root, session.core.state.dependencies.ExternalEnvironment, 0, selectedRootExecution)
}

func (session *executionSession) executeCompiledFlow(
	ctx context.Context,
	flow *compiledFlow,
	overlay map[string]string,
	depth int,
) (FlowResult, error) {
	return session.executeFlowLifecycle(ctx, flow, overlay, depth, nestedFlowExecution)
}

func (session *executionSession) executeFlowLifecycle(
	ctx context.Context,
	flow *compiledFlow,
	overlay map[string]string,
	depth int,
	ownership flowExecutionOwnership,
) (FlowResult, error) {
	if ctx == nil {
		return FlowResult{}, NewConfigurationError("flow execution context must not be nil", nil)
	}
	if flow == nil {
		return FlowResult{}, NewConfigurationError("compiled flow must not be nil", nil)
	}
	if depth < 0 {
		return FlowResult{}, NewConfigurationError("flow depth must not be negative", nil)
	}
	if ownership != nestedFlowExecution && ownership != selectedRootExecution {
		return FlowResult{}, NewConfigurationError("flow execution ownership is invalid", nil)
	}
	if session == nil || session.core == nil || session.core.timeline == nil || session.core.ledger == nil {
		return FlowResult{}, NewConfigurationError("execution session core is unavailable", nil)
	}

	span, started, err := session.core.timeline.BeginFlow(flow.path, flow.config.Name, depth)
	if err != nil {
		return FlowResult{}, err
	}
	DispatchListeners(ctx, started, nil, session.core.state.dependencies.Listeners...)
	checkpoint := session.core.timeline.Checkpoint()
	primary := ctx.Err()
	rootFailures := rootFailureAccumulator{}
	if ownership == selectedRootExecution {
		rootFailures.add(primary)
		primary = rootFailures.productError
	}

	var scope *flowScope
	if primary == nil {
		scope, primary = session.enterFlow(ctx, flow, overlay)
		if ownership == selectedRootExecution {
			rootFailures.add(primary)
			primary = rootFailures.productError
		}
	}
	if scope != nil {
		if ownership == selectedRootExecution {
			start := session.core.executeRootSequenceWithPolicy(ctx, flow.onStart, depth)
			rootFailures.add(start.failure)
			if !start.halted {
				body := session.core.executeRootSequenceWithPolicy(ctx, flow.body, depth)
				rootFailures.add(body.failure)
			}
			if !isSessionIntegrityError(rootFailures.productError) {
				completion := session.core.executeRootSequenceWithPolicy(ctx, flow.onComplete, depth)
				rootFailures.add(completion.failure)
			}
			rootFailures.add(scope.Close())
			primary = rootFailures.productError
		} else {
			if primary == nil {
				_, primary = session.core.executeCompiledSequence(ctx, flow.onStart, depth)
			}
			if primary == nil {
				_, primary = session.core.executeCompiledSequence(ctx, flow.body, depth)
			}
			if !isSessionIntegrityError(primary) {
				_, completionErr := session.core.executeCompiledSequence(ctx, flow.onComplete, depth)
				primary = firstError(primary, completionErr)
			}
			primary = firstError(primary, scope.Close())
		}
	}
	if ownership == selectedRootExecution {
		rootFailures.add(session.Close())
		primary = rootFailures.productError
	}
	primary = sanitizeMalformedError("flow execution failed", primary)

	commands := session.core.ledger.snapshotAfter(checkpoint)
	outcome := classifyFlowOutcome(primary, commands, depth)
	result, finished, finishErr := span.Finish(outcome, primary, commands)
	if finishErr != nil {
		if primary != nil {
			return FlowResult{}, primary
		}
		return FlowResult{}, finishErr
	}
	DispatchListeners(ctx, finished, primary, session.core.state.dependencies.Listeners...)
	if primary != nil {
		return result, primary
	}
	return result, nil
}

func firstError(primary, later error) error {
	if primary != nil {
		return primary
	}
	return later
}

func classifyFlowOutcome(productError error, commands []CommandResult, depth int) Outcome {
	if productError != nil {
		return ClassifyOutcome(productError, false)
	}
	for _, command := range commands {
		if command.Depth() == depth && command.Outcome() == Warned {
			return Warned
		}
	}
	return Completed
}
