package cli

// `flowbaton record` — run one flow and render a video of it.
//
// The last subcommand the v0 registry declares that the CLI never dispatched.
// The contract help gives the shape:
//
//	Usage: flowbaton record [-h] [--local] ... <flowFile> [<outputFile>]
//	  <flowFile>       The Flow file to record.
//	  [<outputFile>]   Output file for the rendered video. Only valid for
//	                   local rendering (--local).
//
// Cloud rendering is `excluded` in the registry (DR-0001), so local is not a
// mode here — it is the only one. `--local` is accepted and does nothing, so a
// command line already written for the contract keeps working.

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// RecordRunner records one flow. The session hook is the same one TestRunner
// takes, so a test drives it without a device.
type RecordRunner struct {
	NewSession SessionFactory
}

func (runner RecordRunner) Run(
	ctx context.Context, args []string, stdout, stderr io.Writer,
) int {
	options, err := parseRecordArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "record: %v\n", err)
		return ExitInvalid
	}
	// One recording around the whole run, through the same controller a flow's
	// own startRecording uses, rather than a second recording mechanism beside
	// it. RecordTo is a field, not a flag: `test` has no such flag external.
	inner := TestRunner{NewSession: runner.NewSession, RecordTo: options.RecordTo}
	options.RecordTo = ""
	return inner.RunOptions(ctx, options, stdout, stderr)
}

// parseRecordArgs reads the contract's positional shape — one flow, then an
// optional output file — out of the same options `test` takes.
//
// Reusing that parser lets `record -p android --device X` select its platform
// and device while retaining the `test` option grammar.
func parseRecordArgs(args []string) (TestOptions, error) {
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		// --local is the contract's opt-in to local rendering. Cloud is
		// excluded here, so local is the only mode and the flag means nothing —
		// accepted so an existing command line keeps working.
		if arg == "--local" {
			continue
		}
		filtered = append(filtered, arg)
	}
	options, err := ParseTestOptions(filtered)
	if err != nil {
		return TestOptions{}, err
	}
	switch len(options.Roots) {
	case 1:
		options.RecordTo = defaultRecordingOutput(options.Roots[0])
	case 2:
		options.RecordTo = options.Roots[1]
		options.Roots = options.Roots[:1]
	default:
		return TestOptions{}, fmt.Errorf(
			"record takes one flow and at most one output file, got %d: record [--local] FLOW [OUTPUT]",
			len(options.Roots))
	}
	return options, nil
}

// defaultRecordingOutput names the video after the flow, the way an authored
// screenshot is named after what the author wrote. It lands in the working
// directory rather than beside the flow: that is where the contract puts a
// capture, and where the operator is standing.
func defaultRecordingOutput(flow string) string {
	base := filepath.Base(flow)
	return strings.TrimSuffix(base, filepath.Ext(base)) + recordingExtension
}
