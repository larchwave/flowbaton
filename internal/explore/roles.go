package explore

import "context"

// Observer captures the current screen: settle, hierarchy, screenshot,
// flattening, and signature computation.
type Observer interface {
	Observe(ctx context.Context) (*ScreenState, error)
}

// Researcher turns an observed screen into a validated UI map. Results
// are cached by screen signature in the state directory.
type Researcher interface {
	Research(ctx context.Context, state *ScreenState) (*UIMap, error)
}

// PlanRequest carries what planning needs for one iteration.
type PlanRequest struct {
	Map *UIMap
	// Style selects the planning style for this iteration.
	Style string
	// Existing lists scenario names already planned or executed, for
	// dedup.
	Existing []string
	// Unpromised lists expected outcomes the judge ruled this app never
	// offers, gathered from earlier runs of the same session. Naming them
	// keeps the planner from writing the same unverifiable expectation
	// every round.
	Unpromised []string
	// Focus optionally narrows planning to one feature or region.
	Focus string
	// Budget caps how many scenarios to emit.
	Budget int
}

// Planner emits prioritized scenarios for a researched screen.
type Planner interface {
	PlanNext(ctx context.Context, request PlanRequest) ([]Scenario, error)
}

// Tester executes one scenario against the live device, adapting steps
// while never weakening expected outcomes.
type Tester interface {
	RunScenario(ctx context.Context, scenario Scenario, start *ScreenState) (*TestResult, error)
}

// Navigator brings the device to a usable starting state: app launched,
// foregrounded, past login when knowledge covers it.
type Navigator interface {
	// EnsureReady prepares the app for exploration from any state.
	EnsureReady(ctx context.Context) (*ScreenState, error)
	// Reach tries to bring the app to the screen named by key,
	// using learned recipes first. The steps it took come back with the
	// screen: they are the prefix an exported flow needs to replay from
	// the same place, and only the caller of Reach knows the run they
	// belong to.
	Reach(ctx context.Context, key string) (*ScreenState, []StepRecord, error)
}

// Analyst renders a session report: findings clustered by root cause,
// product defects separated from automation problems.
type Analyst interface {
	Report(ctx context.Context, report *SessionReport) (string, error)
}

// Exporter turns a finished run into a runnable flow YAML document.
// Implementations must round-trip the output through the flow parser
// before returning it.
type Exporter interface {
	ExportFlow(result *TestResult, appID string) ([]byte, error)
}
