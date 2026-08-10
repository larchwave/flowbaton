package explore

import "time"

// Priority ranks a scenario. The tester runs higher priorities first.
type Priority string

// Scenario priorities, highest first.
const (
	PriorityCritical  Priority = "critical"
	PriorityImportant Priority = "important"
	PriorityHigh      Priority = "high"
	PriorityNormal    Priority = "normal"
	PriorityLow       Priority = "low"
)

// PriorityRank returns a sortable rank, lower is more urgent. Unknown
// priorities rank after all known ones.
func PriorityRank(p Priority) int {
	switch p {
	case PriorityCritical:
		return 0
	case PriorityImportant:
		return 1
	case PriorityHigh:
		return 2
	case PriorityNormal:
		return 3
	case PriorityLow:
		return 4
	default:
		return 5
	}
}

// ScenarioStatus tracks a scenario through the session.
type ScenarioStatus string

// Scenario lifecycle states.
const (
	ScenarioPending ScenarioStatus = "pending"
	ScenarioRunning ScenarioStatus = "running"
	ScenarioPassed  ScenarioStatus = "passed"
	ScenarioFailed  ScenarioStatus = "failed"
	ScenarioSkipped ScenarioStatus = "skipped"
)

// Scenario is one planned test: a business-focused goal with atomic steps
// as guidance and atomic expected outcomes for verification. Steps guide,
// outcomes verify; the tester adapts steps to the live app but never
// weakens outcomes.
type Scenario struct {
	Name     string
	Priority Priority
	// Style names the planning style that produced this scenario.
	Style string
	// StartScreen is the screen key the scenario begins on.
	StartScreen string
	Steps       []string
	Expected    []string
	Status      ScenarioStatus
}

// Plan is the growing collection of scenarios for one exploration target.
type Plan struct {
	AppID     string
	CreatedAt time.Time
	Scenarios []Scenario
}

// Pending returns the scenarios still waiting to run, ordered by priority
// rank then insertion order.
func (p *Plan) Pending() []Scenario {
	pending := make([]Scenario, 0, len(p.Scenarios))
	for _, s := range p.Scenarios {
		if s.Status == ScenarioPending || s.Status == "" {
			pending = append(pending, s)
		}
	}
	// Stable insertion-order sort by priority rank.
	sorted := make([]Scenario, 0, len(pending))
	for rank := 0; rank <= 5; rank++ {
		for _, s := range pending {
			if PriorityRank(s.Priority) == rank {
				sorted = append(sorted, s)
			}
		}
	}
	return sorted
}
