package cli

import (
	"fmt"
	"io"

	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/report"
)

// Command metadata for a finished shard.
//
// specs/03-cli-tooling.md:36 requires command metadata among a run's debug
// artifacts. The document uses FlowBaton's public report model and is reachable
// from the run manifest.

// writeDebugArtifacts renders one shard's command metadata beneath its own
// output directory.
//
// A failure here does NOT fail the shard. The artifacts describe a run that has
// already happened, and turning a green suite red because a debug file could not
// be written would report the wrong thing — but it is reported, never swallowed.
func writeDebugArtifacts(
	stderr io.Writer,
	options TestOptions,
	shard Shard,
	results []engine.FlowResult,
) {
	if shard.OutputDirectory == "" || len(results) == 0 {
		return
	}
	if err := writeCommandMetadata(options, shard, results); err != nil {
		fmt.Fprintf(stderr, "flowbaton test: debug artifacts: %v\n", err)
	}
}

func writeCommandMetadata(options TestOptions, shard Shard, results []engine.FlowResult) error {
	flows, err := convertFlows(options, results)
	if err != nil {
		return err
	}
	writer, err := report.NewWriter(shard.OutputDirectory)
	if err != nil {
		return err
	}
	for _, flow := range flows {
		if _, err := writer.WriteCommands(flow); err != nil {
			return fmt.Errorf("commands for %s: %w", flow.Name, err)
		}
	}
	// The manifest is what makes the directory readable by anything other than
	// a human with `find`, so it is written from the same place, once.
	if _, err := writer.WriteManifest(); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	return nil
}
