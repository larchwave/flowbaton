package report

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func TestStatsTotalsAndApply(t *testing.T) {
	stats := &Stats{}
	stats.Add(explore.Usage{InputTokens: 1200, OutputTokens: 300})
	stats.Add(explore.Usage{InputTokens: 800, OutputTokens: 200})

	total := stats.Total()
	if total.InputTokens != 2000 || total.OutputTokens != 500 {
		t.Fatalf("Total = %+v, want 2000 in / 500 out", total)
	}

	original := explore.SessionReport{AppID: "com.example.app"}
	applied := stats.Apply(original)
	if applied.Usage != total {
		t.Errorf("Apply usage = %+v, want %+v", applied.Usage, total)
	}
	if original.Usage != (explore.Usage{}) {
		t.Errorf("Apply must not touch the input session: %+v", original.Usage)
	}

	want := "token spend: 2000 input + 500 output = 2500 total"
	if got := stats.CostLine(); got != want {
		t.Errorf("CostLine = %q, want %q", got, want)
	}
}
