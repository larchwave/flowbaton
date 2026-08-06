package report

import (
	"errors"
	"testing"
	"time"

	"github.com/larchwave/flowbaton/internal/engine"
	"github.com/larchwave/flowbaton/internal/model"
)

func TestFromEngineFlowResultEmitsExplicitZeroAndOmitsAbsentNumberOfRuns(t *testing.T) {
	t.Parallel()

	flow := buildEngineAdapterFlow(
		t,
		time.Unix(300, 0),
		"repeat-zero.yaml",
		engine.Completed,
		nil,
		[]engineAdapterCommandFixture{
			{
				command:  model.Command{Kind: model.CommandRepeat},
				outcome:  engine.Skipped,
				metadata: engine.NewCommandMetadata(0, nil, nil, "", ""),
			},
			{
				command: model.Command{Kind: model.CommandBack},
				outcome: engine.Completed,
			},
		},
	)

	converted, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("FromEngineFlowResult() error: %v", err)
	}
	if got, exists := converted.Commands[0].Metadata["numberOfRuns"]; !exists || got != "0" {
		t.Fatalf("explicit zero numberOfRuns = %q, present %t; want present zero", got, exists)
	}
	if _, exists := converted.Commands[1].Metadata["numberOfRuns"]; exists {
		t.Fatalf("absent numberOfRuns was serialized: %#v", converted.Commands[1].Metadata)
	}

	converted.Commands[0].Metadata["numberOfRuns"] = "caller-mutated"
	again, err := FromEngineFlowResult(flow, model.Config{})
	if err != nil {
		t.Fatalf("second FromEngineFlowResult() error: %v", err)
	}
	if got := again.Commands[0].Metadata["numberOfRuns"]; got != "0" {
		t.Fatalf("second conversion numberOfRuns = %q, want immutable zero", got)
	}
}

func TestEngineMetadataToReportRejectsNegativePresentNumberOfRuns(t *testing.T) {
	t.Parallel()

	converted, err := engineMetadataToReport(engine.NewCommandMetadata(-1, nil, nil, "", ""))
	if err == nil {
		t.Fatalf("engineMetadataToReport() = %#v, nil; want failure", converted)
	}
	var configuration *engine.ConfigurationError
	if !errors.As(err, &configuration) {
		t.Fatalf("engineMetadataToReport() error = %T %v, want ConfigurationError", err, err)
	}
}
