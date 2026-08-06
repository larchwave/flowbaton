package report

import (
	"strings"
	"testing"
	"time"
)

func TestStatusValid(t *testing.T) {
	t.Parallel()

	valid := []Status{Completed, Skipped, Warned, Failed, Cancelled}
	for _, status := range valid {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			if !status.Valid() {
				t.Fatalf("Status(%q).Valid() = false, want true", status)
			}
		})
	}

	if Status("unknown").Valid() {
		t.Fatal("Status(\"unknown\").Valid() = true, want false")
	}
}

func TestMarshalCommandsIsCanonicalAndDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, time.July, 15, 14, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	flow := FlowResult{
		Name:           "checkout & pay",
		Description:    "Checkout flow",
		Status:         Failed,
		StartedAt:      started,
		EndedAt:        started.Add(1500 * time.Millisecond),
		DurationMillis: 1500,
		Failure:        &Failure{Message: "button not found", Details: "selector: Pay"},
		Metadata:       map[string]string{"zeta": "last", "alpha": "first"},
		Artifacts: []Artifact{
			{Kind: "screenshot", Path: "checkout/failure.png"},
			{Kind: "hierarchy", Path: "checkout/hierarchy.xml"},
		},
		Commands: []CommandResult{
			{
				Sequence:       2,
				Depth:          1,
				Keyword:        "tapOn",
				Description:    "Tap Pay",
				Status:         Failed,
				StartedAt:      started.Add(time.Second),
				EndedAt:        started.Add(1500 * time.Millisecond),
				DurationMillis: 500,
				Failure:        &Failure{Message: "button not found"},
			},
			{
				Sequence:       1,
				Depth:          0,
				Keyword:        "launchApp",
				Description:    "Launch application",
				Status:         Completed,
				StartedAt:      started,
				EndedAt:        started.Add(time.Second),
				DurationMillis: 1000,
				Metadata:       map[string]string{"device": "simulator"},
			},
		},
	}

	got, err := MarshalCommands(flow)
	if err != nil {
		t.Fatalf("MarshalCommands() error = %v", err)
	}

	want := `{
  "schemaVersion": "flowbaton.commands/v1",
  "flow": {
    "name": "checkout & pay",
    "description": "Checkout flow",
    "status": "Failed",
    "startedAt": "2026-07-15T18:30:00Z",
    "endedAt": "2026-07-15T18:30:01.5Z",
    "durationMillis": 1500,
    "failure": {
      "message": "button not found",
      "details": "selector: Pay"
    },
    "metadata": {
      "alpha": "first",
      "zeta": "last"
    },
    "artifacts": [
      {
        "kind": "hierarchy",
        "path": "checkout/hierarchy.xml"
      },
      {
        "kind": "screenshot",
        "path": "checkout/failure.png"
      }
    ],
    "commands": [
      {
        "sequence": 1,
        "depth": 0,
        "keyword": "launchApp",
        "description": "Launch application",
        "status": "Completed",
        "startedAt": "2026-07-15T18:30:00Z",
        "endedAt": "2026-07-15T18:30:01Z",
        "durationMillis": 1000,
        "failure": null,
        "metadata": {
          "device": "simulator"
        },
        "artifacts": []
      },
      {
        "sequence": 2,
        "depth": 1,
        "keyword": "tapOn",
        "description": "Tap Pay",
        "status": "Failed",
        "startedAt": "2026-07-15T18:30:01Z",
        "endedAt": "2026-07-15T18:30:01.5Z",
        "durationMillis": 500,
        "failure": {
          "message": "button not found",
          "details": ""
        },
        "metadata": {},
        "artifacts": []
      }
    ]
  }
}
`
	if string(got) != want {
		t.Fatalf("MarshalCommands() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
	}

	if flow.Commands[0].Sequence != 2 {
		t.Fatalf("MarshalCommands() mutated command order: first sequence = %d, want 2", flow.Commands[0].Sequence)
	}
	if flow.Artifacts[0].Kind != "screenshot" {
		t.Fatalf("MarshalCommands() mutated artifact order: first kind = %q, want screenshot", flow.Artifacts[0].Kind)
	}
}

func TestMarshalCommandsRejectsUnknownStatus(t *testing.T) {
	t.Parallel()

	_, err := MarshalCommands(FlowResult{Name: "flow", Status: Status("unknown")})
	if err == nil {
		t.Fatal("MarshalCommands() error = nil, want invalid status error")
	}
	if !strings.Contains(err.Error(), `flow status "unknown"`) {
		t.Fatalf("MarshalCommands() error = %q, want flow status context", err)
	}
}

func TestMarshalCommandsRejectsUnknownCommandStatus(t *testing.T) {
	t.Parallel()

	_, err := MarshalCommands(FlowResult{
		Name:   "flow",
		Status: Completed,
		Commands: []CommandResult{
			{Sequence: 7, Status: Status("unknown")},
		},
	})
	if err == nil {
		t.Fatal("MarshalCommands() error = nil, want invalid command status error")
	}
	if !strings.Contains(err.Error(), `command 7 status "unknown"`) {
		t.Fatalf("MarshalCommands() error = %q, want command sequence context", err)
	}
}
