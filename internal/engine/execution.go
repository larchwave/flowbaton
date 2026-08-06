package engine

import (
	"context"
	"fmt"
	"strings"
)

// Execute compiles the complete prepared Program before creating a runtime or
// touching the driver, then executes selected roots in plan order. Duplicate
// root selections intentionally produce independent execution sessions.
func Execute(ctx context.Context, program *Program, dependencies Dependencies) (results []FlowResult, err error) {
	defer func() {
		err = sanitizeMalformedError("engine Execute failed", err)
	}()
	if err := validateExecutionID(dependencies.ExecutionID); err != nil {
		return nil, err
	}
	if err := validateReservedEnvironment(dependencies.ReservedEnvironment); err != nil {
		return nil, err
	}
	dependencies.ExternalEnvironment = snapshotExternalEnvironment(dependencies.ExternalEnvironment)
	// Snapshotted for the same reason as the external map: the caller's map
	// must not change under a run, and a run must not change the caller's.
	dependencies.ReservedEnvironment = cloneStringMap(dependencies.ReservedEnvironment)
	registry, err := productionHandlerRegistry()
	if err != nil {
		return nil, err
	}
	compiled, err := compileProgram(ctx, program, registry)
	if err != nil {
		return nil, err
	}

	results = make([]FlowResult, 0, len(compiled.roots))
	var firstFailure error
	for index, path := range compiled.roots {
		root, exists := compiled.Flow(path)
		if !exists {
			return results, NewConfigurationError("compiled root flow is missing", nil)
		}
		rootRunID := fmt.Sprintf("%s/root-run-%06d", dependencies.ExecutionID, index+1)
		result, executeErr := executeCompiledRootForRun(ctx, dependencies, root, rootRunID)
		if result.Path() != "" {
			results = append(results, result)
		}
		if executeErr == nil {
			continue
		}
		if firstFailure == nil {
			firstFailure = executeErr
		}
		// A declared sequence stops after failure unless continuation is
		// enabled. Independent roots continue so each produces a result.
		if index < dependencies.SequencedRoots && !dependencies.ContinueOnFailure {
			return results, firstFailure
		}
		// Cancellation stops all remaining roots.
		if ctx.Err() != nil {
			return results, firstFailure
		}
	}
	return results, firstFailure
}

func snapshotExternalEnvironment(values map[string]string) map[string]string {
	snapshot := cloneStringMap(values)
	delete(snapshot, "FLOWBATON_SHARD_ID")
	delete(snapshot, "FLOWBATON_SHARD_INDEX")
	return snapshot
}

func validateExecutionID(executionID string) error {
	if strings.TrimSpace(executionID) == "" {
		return NewConfigurationError("engine Execute requires a non-blank execution ID", nil)
	}
	if strings.TrimSpace(executionID) != executionID {
		return NewConfigurationError("engine Execute execution ID must not contain surrounding whitespace", nil)
	}
	return nil
}
