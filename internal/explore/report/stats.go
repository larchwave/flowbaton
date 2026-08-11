package report

import (
	"fmt"

	"github.com/larchwave/flowbaton/internal/explore"
)

// Stats accumulates token spend across one exploration session.
type Stats struct {
	total explore.Usage
}

// Add folds one usage record into the running totals.
func (s *Stats) Add(usage explore.Usage) {
	s.total.InputTokens += usage.InputTokens
	s.total.OutputTokens += usage.OutputTokens
}

// Total returns the accumulated usage.
func (s *Stats) Total() explore.Usage {
	return s.total
}

// Apply returns a copy of the session with the accumulated usage set; the
// input session is left untouched.
func (s *Stats) Apply(session explore.SessionReport) explore.SessionReport {
	session.Usage = s.total
	return session
}

// CostLine renders a one-line token summary, no currency.
func (s *Stats) CostLine() string {
	return fmt.Sprintf("token spend: %d input + %d output = %d total",
		s.total.InputTokens, s.total.OutputTokens, s.total.InputTokens+s.total.OutputTokens)
}
